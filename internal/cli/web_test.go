package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/web"
)

func TestLastSeenIDIgnoresWebRecords(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := store.Config{Sessions: map[string]store.SessionRecord{
		store.Key("fake", "aaaaaaaa"):    {ID: "aaaaaaaa", Agent: "fake", SessionName: "uam-fake-aaaaaaaa", LastSeenAt: base},
		store.Key("copilot", "bbbbbbbb"): {ID: "bbbbbbbb", Agent: "copilot", Surface: store.SurfaceWeb, LastSeenAt: base.Add(time.Hour)},
	}}
	if got := lastSeenID(cfg); got != "aaaaaaaa" {
		t.Fatalf("lastSeenID = %q, want the terminal record", got)
	}
	webOnly := store.Config{Sessions: map[string]store.SessionRecord{
		store.Key("copilot", "bbbbbbbb"): {ID: "bbbbbbbb", Agent: "copilot", Surface: store.SurfaceWeb, LastSeenAt: base},
	}}
	if got := lastSeenID(webOnly); got != "" {
		t.Fatalf("lastSeenID = %q, want none", got)
	}
}

func TestWebCommandsRouteBeforeStoreAndRejectHostNames(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", secureSessionDir(t))
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// An unusable config directory must not matter for argument errors.
	t.Setenv("UAM_CONFIG_DIR", blocked)
	for _, listen := range []string{"example.com:8260", "uam.local:8260"} {
		err := RunWithTUI(context.Background(), []string{"web", "--listen", listen}, noopRunTUI)
		if err == nil || !strings.Contains(err.Error(), "not an IP address") {
			t.Fatalf("uam web --listen %s = %v, want a host name error", listen, err)
		}
	}
	if err := RunWithTUI(context.Background(), []string{"web", "--public-origin", "ftp://x"}, noopRunTUI); err == nil {
		t.Fatal("an invalid public origin must be rejected")
	}
	out := captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "status"}, noopRunTUI)) })
	if strings.TrimSpace(out) != "uam web is not running" {
		t.Fatalf("status = %q", out)
	}
	out = captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "status", "--json"}, noopRunTUI)) })
	if strings.TrimSpace(out) != `{"running":false}` {
		t.Fatalf("status --json = %q", out)
	}
	out = captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "stop"}, noopRunTUI)) })
	if strings.TrimSpace(out) != "uam web is not running" {
		t.Fatalf("stop = %q", out)
	}
}

// The web service must outlive the terminal and process that started it: a
// launcher with a controlling terminal starts it, then the terminal and the
// launcher go away, and the service keeps answering until `uam web stop`.
func TestWebServiceOutlivesLauncherTerminal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("inspects /proc")
	}
	if testing.Short() {
		t.Skip("builds and runs the uam binary")
	}
	binary := filepath.Join(t.TempDir(), "uam")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/uam")
	build.Dir = todo7RepoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build uam: %v\n%s", err, out)
	}
	root := t.TempDir()
	sessionDir := filepath.Join(root, "run")
	if err := os.Mkdir(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"UAM_CONFIG_DIR="+filepath.Join(root, "config"),
		"UAM_SESSION_DIR="+sessionDir,
		"UAM_CACHE_DIR="+filepath.Join(root, "cache"),
	)
	listen := "127.0.0.1:" + freeTCPPort(t)
	statePath := filepath.Join(sessionDir, "web.json")
	t.Cleanup(func() {
		if st, ok := readWebState(statePath); ok {
			_ = syscall.Kill(st.PID, syscall.SIGKILL)
		}
	})

	launcher := exec.Command(binary, "web", "--listen", listen)
	launcher.Env = env
	ptmx, err := pty.Start(launcher)
	if err != nil {
		t.Fatal(err)
	}
	outCh := make(chan string, 1)
	go func() { data, _ := io.ReadAll(ptmx); outCh <- string(data) }()
	var output string
	select {
	case output = <-outCh:
	case <-time.After(90 * time.Second):
		t.Fatal("uam web launcher did not finish")
	}
	_ = ptmx.Close()
	_ = launcher.Process.Kill()
	_ = launcher.Wait()
	if !strings.Contains(output, "uam web started") || !strings.Contains(output, "ssh -N -L 127.0.0.1:") {
		t.Fatalf("launcher output = %q", output)
	}
	match := regexp.MustCompile(`Access token:\s+([0-9a-f]{64})`).FindStringSubmatch(output)
	if match == nil {
		t.Fatalf("no access token in %q", output)
	}
	token := match[1]
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("web.json: %v", err)
	}
	if strings.Contains(string(data), token) {
		t.Fatal("web.json must not contain the access token")
	}
	st, ok := readWebState(statePath)
	if !ok || st.PID == launcher.Process.Pid || st.Listen != listen {
		t.Fatalf("state = %+v", st)
	}
	time.Sleep(200 * time.Millisecond) // the terminal hang-up has been delivered by now
	for fd := range 3 {
		target, err := os.Readlink("/proc/" + strconv.Itoa(st.PID) + "/fd/" + strconv.Itoa(fd))
		if err != nil || target != "/dev/null" {
			t.Fatalf("daemon fd %d -> %q (%v), want /dev/null", fd, target, err)
		}
	}
	if sid := procSessionID(t, st.PID); sid != st.PID {
		t.Fatalf("daemon session id = %d, want its own session %d", sid, st.PID)
	}

	base := "http://" + listen
	resp, err := http.Get(base + "/api/auth")
	if err != nil {
		t.Fatalf("daemon stopped answering after its terminal closed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"authenticated":false,"required":true}` {
		t.Fatalf("/api/auth = %d %s", resp.StatusCode, body)
	}
	login, _ := http.NewRequest(http.MethodPost, base+"/api/login", strings.NewReader(`{"token":"`+token+`"}`))
	login.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || len(resp.Cookies()) != 1 {
		t.Fatalf("login = %d", resp.StatusCode)
	}

	status := runUAM(t, binary, env, "web", "status", "--json")
	var parsed struct {
		Running bool `json:"running"`
		PID     int  `json:"pid"`
	}
	if err := json.Unmarshal([]byte(status), &parsed); err != nil || !parsed.Running || parsed.PID != st.PID {
		t.Fatalf("status --json = %q", status)
	}
	if again := runUAM(t, binary, env, "web", "--listen", listen); !strings.Contains(again, "already running") || !strings.Contains(again, token) {
		t.Fatalf("second uam web = %q", again)
	}
	if out := runUAM(t, binary, env, "web", "stop"); strings.TrimSpace(out) != "uam web stopped" {
		t.Fatalf("stop = %q", out)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("web.json still present after stop: %v", err)
	}
	if _, err := http.Get(base + "/api/auth"); err == nil {
		t.Fatal("the service still answers after stop")
	}
	if out := runUAM(t, binary, env, "web", "status"); strings.TrimSpace(out) != "uam web is not running" {
		t.Fatalf("status after stop = %q", out)
	}

	// Both entry points reject the removed flag without starting a listener.
	for _, command := range []string{"web", "__web"} {
		cmd := exec.Command(binary, command, "--listen", listen, "--no-auth")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "flag provided but not defined: -no-auth") || strings.Contains(string(out), token) {
			t.Fatalf("%s accepted the removed flag or returned the wrong error", command)
		}
		if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s created running state: %v", command, err)
		}
		if resp, err := http.Get(base + "/api/auth"); err == nil {
			_ = resp.Body.Close()
			t.Fatalf("%s started a listener for the removed flag", command)
		}
	}
}

func TestWebFlagsRequireAuthentication(t *testing.T) {
	for _, command := range []string{"web", "__web"} {
		for _, flag := range []string{"--no-auth", "--no-auth=true", "--no-auth=false"} {
			if _, err := webFlags(command, []string{flag}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -no-auth") {
				t.Fatalf("%s %s = %v, want unknown flag", command, flag, err)
			}
		}
	}
	opts, err := webFlags("web", []string{"--listen", "localhost:9000", "--public-origin", "https://UAM.example.com/"})
	if err != nil || opts.listen != "127.0.0.1:9000" {
		t.Fatalf("webFlags = %+v, %v", opts, err)
	}
	want := []string{"--listen", "127.0.0.1:9000", "--public-origin", "https://uam.example.com"}
	if got := opts.args(); !slices.Equal(got, want) {
		t.Fatalf("service args = %q, want %q", got, want)
	}
	service, err := webFlags("__web", opts.args())
	if err != nil || !reflect.DeepEqual(service, opts) {
		t.Fatalf("__web parsed %+v, %v; want %+v", service, err, opts)
	}
}

// Old state remains visible for diagnosis and stop, but cannot be reused.
func TestWebStatusReportsLegacyInsecureDaemon(t *testing.T) {
	sessionDir := secureSessionDir(t)
	t.Setenv("UAM_SESSION_DIR", sessionDir)
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	listen := "0.0.0.0:" + freeTCPPort(t)
	for _, legacy := range []bool{true, false} {
		st := web.DaemonState{PID: os.Getpid(), StartTime: session.ProcStartTime(os.Getpid()), Listen: listen, LegacyNoAuth: legacy, Version: "test"}
		data, err := json.Marshal(st)
		must(t, err)
		statePath := filepath.Join(sessionDir, "web.json")
		must(t, os.WriteFile(statePath, data, 0o600))
		out := captureCLIStdout(t, func() { must(t, runWebStatus([]string{"--json"})) })
		var status struct {
			Running         bool  `json:"running"`
			NoAuth          *bool `json:"no_auth"`
			RestartRequired bool  `json:"restart_required"`
		}
		if err := json.Unmarshal([]byte(out), &status); err != nil || !status.Running || status.NoAuth == nil || *status.NoAuth != legacy || status.RestartRequired != legacy {
			t.Fatalf("legacy=%v: status --json = %q", legacy, out)
		}
		out = captureCLIStdout(t, func() { must(t, runWebStatus(nil)) })
		if strings.Contains(out, legacyNoAuthNotice) != legacy || strings.Contains(out, "sign-in is required") == legacy {
			t.Fatalf("legacy=%v: misleading status = %q", legacy, out)
		}
		var startErr error
		out = captureCLIStdout(t, func() { startErr = runWeb(context.Background(), []string{"--listen", listen}) })
		if legacy {
			if startErr == nil || !strings.Contains(startErr.Error(), legacyNoAuthNotice) || out != "" {
				t.Fatal("legacy daemon was not rejected before printing access details")
			}
			if _, err := os.Stat(web.TokenPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("legacy refusal must not create or read a token: %v", err)
			}
		} else {
			must(t, startErr)
			token, err := web.LoadOrCreateToken(web.TokenPath())
			must(t, err)
			if !strings.Contains(out, "stop it first to change its settings") || !strings.Contains(out, token) {
				t.Fatal("secure running service did not report access details")
			}
		}
		if after, err := os.ReadFile(statePath); err != nil || string(after) != string(data) {
			t.Fatalf("status/start changed the existing service state: %v", err)
		}
	}
}

// Wherever the settings are shown, a bind beyond loopback is named, reached
// over loopback on this host, and reported as requiring sign-in.
func TestWebStatusWarnsBeyondLoopback(t *testing.T) {
	sessionDir := secureSessionDir(t)
	t.Setenv("UAM_SESSION_DIR", sessionDir)
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	port := freeTCPPort(t)
	const exposed = "so other machines can reach this service"
	for _, tc := range []struct {
		listen, url string
	}{
		{"127.0.0.1:" + port, "http://127.0.0.1:" + port + "/"},
		{"0.0.0.0:" + port, "http://127.0.0.1:" + port + "/"},
		{"[::]:" + port, "http://[::1]:" + port + "/"},
	} {
		// A state file naming this test process stands in for a running service.
		st := web.DaemonState{PID: os.Getpid(), StartTime: session.ProcStartTime(os.Getpid()), Listen: tc.listen, Version: "test"}
		data, err := json.Marshal(st)
		must(t, err)
		must(t, os.WriteFile(filepath.Join(sessionDir, "web.json"), data, 0o600))
		beyond := !strings.HasPrefix(tc.listen, "127.")

		out := captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "status", "--json"}, noopRunTUI)) })
		var status struct {
			URL    string `json:"url"`
			Listen string `json:"listen"`
		}
		if err := json.Unmarshal([]byte(out), &status); err != nil || status.URL != tc.url || status.Listen != tc.listen {
			t.Fatalf("%+v: status --json = %q", tc, out)
		}
		statusOut := captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "status"}, noopRunTUI)) })
		startOut := captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "--listen", tc.listen}, noopRunTUI)) })
		for name, out := range map[string]string{"status": statusOut, "uam web": startOut} {
			if !strings.Contains(out, tc.url) || !strings.Contains(out, tc.listen) {
				t.Fatalf("%+v: %s must show the URL and the listen address: %q", tc, name, out)
			}
			if strings.Contains(out, "Warning: listening on "+tc.listen+", "+exposed) != beyond {
				t.Fatalf("%+v: %s exposure warning: %q", tc, name, out)
			}
			if strings.Contains(out, "sign-in is required") != beyond {
				t.Fatalf("%+v: %s sign-in note: %q", tc, name, out)
			}
		}
		if hostPort := strings.TrimSuffix(strings.TrimPrefix(tc.url, "http://"), "/"); !strings.Contains(startOut, "ssh -N -L 127.0.0.1:"+port+":"+hostPort+" ") {
			t.Fatalf("%+v: SSH forward must target the loopback address: %q", tc, startOut)
		}
	}
}

func TestWebLogHeadersReachesServiceArgsAndStatus(t *testing.T) {
	opts, err := webFlags("web", []string{"--log-headers"})
	if err != nil || !opts.logHeaders || !slices.Contains(opts.args(), "--log-headers") {
		t.Fatalf("webFlags = %+v %q, %v", opts, opts.args(), err)
	}
	if service, err := webFlags("__web", opts.args()); err != nil || !reflect.DeepEqual(service, opts) {
		t.Fatalf("__web parsed %+v, %v; want %+v", service, err, opts)
	}
	if def, err := webFlags("web", nil); err != nil || def.logHeaders || slices.Contains(def.args(), "--log-headers") {
		t.Fatalf("default = %+v %q, %v", def, def.args(), err)
	}
	sessionDir := secureSessionDir(t)
	t.Setenv("UAM_SESSION_DIR", sessionDir)
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	for _, on := range []bool{true, false} {
		st := web.DaemonState{PID: os.Getpid(), StartTime: session.ProcStartTime(os.Getpid()), Listen: "127.0.0.1:" + freeTCPPort(t), LogHeaders: on, Version: "test"}
		data, err := json.Marshal(st)
		must(t, err)
		must(t, os.WriteFile(filepath.Join(sessionDir, "web.json"), data, 0o600))
		out := captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "status", "--json"}, noopRunTUI)) })
		if strings.Contains(out, `"log_headers":true`) != on {
			t.Fatalf("log_headers=%v: status --json = %q", on, out)
		}
		out = captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "status"}, noopRunTUI)) })
		if strings.Contains(out, logHeadersNotice) != on {
			t.Fatalf("log_headers=%v: status = %q", on, out)
		}
	}
}

// uam web token set takes the token from stdin only, never echoes it, and
// replaces the token file the service loads.
func TestWebTokenSet(t *testing.T) {
	sessionDir := secureSessionDir(t)
	t.Setenv("UAM_SESSION_DIR", sessionDir)
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	old, err := web.LoadOrCreateToken(web.TokenPath())
	must(t, err)
	must(t, os.Chmod(web.TokenPath(), 0o644))
	const chosen = "Owner-Chosen_token.0123456789"
	set := func(input string, args ...string) (string, error) {
		var runErr error
		out := captureCLIStdout(t, func() {
			withCLIStdin(t, input, func() {
				runErr = RunWithTUI(context.Background(), append([]string{"web", "token", "set"}, args...), noopRunTUI)
			})
		})
		return out, runErr
	}
	for _, args := range [][]string{{chosen}, {"--token", chosen}, {"--token=" + chosen}, {"-"}} {
		out, err := set(chosen+"\n", args...)
		if err == nil || strings.Contains(err.Error(), chosen) || strings.Contains(out, chosen) {
			t.Fatalf("token set %q = %v, %q; want a refusal that does not echo the token", args, err, out)
		}
	}
	if err := RunWithTUI(context.Background(), []string{"web", "token", chosen}, noopRunTUI); err == nil || strings.Contains(err.Error(), chosen) {
		t.Fatalf("uam web token <token> = %v", err)
	}
	for input, want := range map[string]string{
		"short\n": "too short", "": "too short", strings.Repeat("a", 300) + "\n": "too long",
		"has inner space 0123456789abcdef\n": "without whitespace", strings.Repeat("a", 30) + "\x1b\n": "without whitespace",
	} {
		if _, err := set(input); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("token set with %q = %v, want %q", input, err, want)
		}
	}
	if got, err := web.LoadOrCreateToken(web.TokenPath()); err != nil || got != old {
		t.Fatalf("refused input changed the token: %v", err)
	}
	out, err := set("  " + chosen + " \nsecond line\n")
	if err != nil || strings.Contains(out, chosen) || !strings.Contains(out, "Access token set in "+web.TokenPath()) || strings.Contains(out, "is running") {
		t.Fatalf("token set = %v, %q", err, out)
	}
	info, err := os.Stat(web.TokenPath())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, %v", info, err)
	}
	if got, err := web.LoadOrCreateToken(web.TokenPath()); err != nil || got != chosen {
		t.Fatalf("LoadOrCreateToken = %q, %v; want the set token", got, err)
	}
	// A state file naming this test process stands in for a running service.
	st := web.DaemonState{PID: os.Getpid(), StartTime: session.ProcStartTime(os.Getpid()), Listen: "127.0.0.1:" + freeTCPPort(t), Version: "test"}
	data, err := json.Marshal(st)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(sessionDir, "web.json"), data, 0o600))
	const next = "Next-Owner-Chosen_token.0123456789"
	if out, err := set(next + "\n"); err == nil || !strings.Contains(err.Error(), "run uam web stop first") || strings.Contains(out, next) {
		t.Fatalf("token set while running = %v, %q; want a refusal", err, out)
	}
	if got, err := web.LoadOrCreateToken(web.TokenPath()); err != nil || got != chosen {
		t.Fatalf("token set while running changed the token: %v", err)
	}
}

// At a terminal the token is typed at a prompt that does not echo it.
func TestWebTokenSetPromptDoesNotEcho(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", secureSessionDir(t))
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	ptmx, tty, err := pty.Open()
	must(t, err)
	defer func() { _ = ptmx.Close() }()
	oldIn, oldErr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = tty, tty
	t.Cleanup(func() { os.Stdin, os.Stderr = oldIn, oldErr })
	const chosen = "Typed-At-The-Prompt_0123456789"
	done := make(chan error, 1)
	go func() { done <- RunWithTUI(context.Background(), []string{"web", "token", "set"}, noopRunTUI) }()
	var seen []byte
	buf := make([]byte, 256)
	for !strings.Contains(string(seen), "New access token") {
		n, err := ptmx.Read(buf)
		if err != nil {
			t.Fatalf("no prompt: %v %q", err, seen)
		}
		seen = append(seen, buf[:n]...)
	}
	_, _ = ptmx.Write([]byte(chosen + "\n"))
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("token set did not finish")
	}
	os.Stdin, os.Stderr = oldIn, oldErr
	_ = tty.Close()
	rest, _ := io.ReadAll(ptmx)
	if runErr != nil || strings.Contains(string(seen)+string(rest), chosen) {
		t.Fatalf("token set = %v; terminal showed %q", runErr, string(seen)+string(rest))
	}
	if got, err := web.LoadOrCreateToken(web.TokenPath()); err != nil || got != chosen {
		t.Fatalf("LoadOrCreateToken = %q, %v", got, err)
	}
}

func runUAM(t *testing.T, binary string, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("uam %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func readWebState(path string) (web.DaemonState, bool) {
	var st web.DaemonState
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &st) != nil || st.PID <= 0 {
		return st, false
	}
	return st, true
}

func freeTCPPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

// procSessionID reads field 6 (session) of /proc/<pid>/stat.
func procSessionID(t *testing.T, pid int) int {
	t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	rest := string(data)
	if i := strings.LastIndexByte(rest, ')'); i >= 0 {
		rest = rest[i+1:]
	}
	fields := strings.Fields(rest)
	if len(fields) < 4 {
		t.Fatalf("short stat %q", data)
	}
	sid, err := strconv.Atoi(fields[3])
	if err != nil {
		t.Fatal(err)
	}
	return sid
}
