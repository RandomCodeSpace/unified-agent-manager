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

func TestWebCommandsRouteBeforeStoreAndRejectNonLoopback(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", secureSessionDir(t))
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// An unusable config directory must not matter for argument errors.
	t.Setenv("UAM_CONFIG_DIR", blocked)
	for _, listen := range []string{"0.0.0.0:8260", "192.0.2.1:8260", "example.com:8260"} {
		err := RunWithTUI(context.Background(), []string{"web", "--listen", listen}, noopRunTUI)
		if err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Fatalf("uam web --listen %s = %v, want loopback error", listen, err)
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

	// --no-auth reaches the service: no sign-in, no token printed, and the
	// setting is reported until the service is stopped.
	if out := runUAM(t, binary, env, "web", "--listen", listen, "--no-auth"); !strings.Contains(out, "uam web started") ||
		!strings.Contains(out, "Authentication: disabled") || strings.Contains(out, token) {
		t.Fatalf("uam web --no-auth = %q", out)
	}
	resp, err = http.Get(base + "/api/auth")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.TrimSpace(string(body)) != `{"authenticated":true,"required":false}` {
		t.Fatalf("/api/auth with --no-auth = %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(runUAM(t, binary, env, "web", "status", "--json"), `"no_auth":true`) {
		t.Fatal("status --json must report no_auth")
	}
	if again := runUAM(t, binary, env, "web", "--listen", listen); !strings.Contains(again, "already running") ||
		!strings.Contains(again, "Authentication: disabled") || strings.Contains(again, token) {
		t.Fatalf("uam web while --no-auth runs = %q", again)
	}
	if out := runUAM(t, binary, env, "web", "stop"); strings.TrimSpace(out) != "uam web stopped" {
		t.Fatalf("stop = %q", out)
	}
}

func TestWebFlagsNoAuthReachesServiceArgs(t *testing.T) {
	opts, err := webFlags("web", []string{"--listen", "localhost:9000", "--public-origin", "https://UAM.example.com/", "--no-auth"})
	if err != nil || !opts.noAuth || opts.listen != "127.0.0.1:9000" {
		t.Fatalf("webFlags = %+v, %v", opts, err)
	}
	want := []string{"--listen", "127.0.0.1:9000", "--public-origin", "https://uam.example.com", "--no-auth"}
	if got := opts.args(); !slices.Equal(got, want) {
		t.Fatalf("service args = %q, want %q", got, want)
	}
	service, err := webFlags("__web", opts.args())
	if err != nil || !reflect.DeepEqual(service, opts) {
		t.Fatalf("__web parsed %+v, %v; want %+v", service, err, opts)
	}
	def, err := webFlags("web", nil)
	if err != nil || def.noAuth || slices.Contains(def.args(), "--no-auth") {
		t.Fatalf("default = %+v %q, %v", def, def.args(), err)
	}
}

// Status and the already-running message report the running service's
// setting; with auth disabled the token is never printed.
func TestWebStatusReportsNoAuth(t *testing.T) {
	sessionDir := secureSessionDir(t)
	t.Setenv("UAM_SESSION_DIR", sessionDir)
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	listen := "127.0.0.1:" + freeTCPPort(t)
	for _, noAuth := range []bool{true, false} {
		// A state file naming this test process stands in for a running service.
		st := web.DaemonState{PID: os.Getpid(), StartTime: session.ProcStartTime(os.Getpid()), Listen: listen, NoAuth: noAuth, Version: "test"}
		data, err := json.Marshal(st)
		must(t, err)
		must(t, os.WriteFile(filepath.Join(sessionDir, "web.json"), data, 0o600))

		out := captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "status", "--json"}, noopRunTUI)) })
		var status struct {
			Running bool  `json:"running"`
			NoAuth  *bool `json:"no_auth"`
		}
		if err := json.Unmarshal([]byte(out), &status); err != nil || !status.Running || status.NoAuth == nil || *status.NoAuth != noAuth {
			t.Fatalf("no_auth=%v: status --json = %q", noAuth, out)
		}
		out = captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "status"}, noopRunTUI)) })
		if strings.Contains(out, noAuthNotice) != noAuth {
			t.Fatalf("no_auth=%v: status = %q", noAuth, out)
		}
		// Only reached with the fake service verified as running, so this
		// never spawns anything.
		out = captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"web", "--listen", listen}, noopRunTUI)) })
		token, err := web.LoadOrCreateToken(web.TokenPath())
		must(t, err)
		if !strings.Contains(out, "stop it first to change its settings") || strings.Contains(out, noAuthNotice) != noAuth || strings.Contains(out, token) == noAuth {
			t.Fatalf("no_auth=%v: uam web = %q", noAuth, out)
		}
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
