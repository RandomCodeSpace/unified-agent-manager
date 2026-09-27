package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/daemonruntime"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/web"
)

// TestMain lets web.Spawn start this test binary as the service: it runs
// `<binary> __web <flags>` with the readiness pipe on fd 3.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__web" && os.Getenv("UAM_WEB_READY_FD") == "3" {
		os.Exit(testWebService(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// testWebService serves like `uam __web`, with a fake provider in place of
// Copilot. UAM_TEST_WEB_SERVICE selects a misbehaving service instead.
func testWebService(args []string) int {
	opts, err := webFlags("__web", args)
	if err != nil {
		return 2
	}
	switch os.Getenv("UAM_TEST_WEB_SERVICE") {
	case "ready-without-state":
		_, _ = fmt.Fprintln(os.NewFile(3, "ready"), "ok")
		return 0
	case "legacy":
		st := web.DaemonState{PID: os.Getpid(), StartTime: daemonruntime.ProcStartTime(os.Getpid()), Listen: opts.listen, LegacyNoAuth: true}
		data, err := json.Marshal(st)
		if err != nil || os.WriteFile(filepath.Join(daemonruntime.DefaultDir(), "web.json"), data, 0o600) != nil {
			return 1
		}
		_, _ = fmt.Fprintln(os.NewFile(3, "ready"), "ok")
		time.Sleep(time.Minute) // the test kills it
		return 0
	}
	cfg := web.DaemonConfig{Listen: opts.listen, PublicOrigins: opts.origins, LogHeaders: opts.logHeaders, Version: "test",
		Providers: []agentapi.Provider{agenttest.NewProvider("fake", agentapi.Capabilities{})}}
	if web.RunDaemon(cfg) != nil {
		return 1
	}
	return 0
}

// killWebService ends a service a test started and left running.
func killWebService(sessionDir string) {
	if st, ok := web.ReadRunning(sessionDir); ok && st.PID != os.Getpid() {
		_ = syscall.Kill(st.PID, syscall.SIGKILL)
	}
}

// uam web starts the service detached and prints how to reach it; uam web
// status shows it and uam web stop ends it.
func TestWebStartStatusAndStop(t *testing.T) {
	sessionDir := secureSessionDir(t)
	t.Setenv("UAM_SESSION_DIR", sessionDir)
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	t.Setenv("UAM_TEST_WEB_SERVICE", "")
	t.Cleanup(func() { killWebService(sessionDir) })
	ctx := context.Background()
	var startErr error
	out := captureCLIStdout(t, func() {
		startErr = Run(ctx, []string{"web", "--listen", "127.0.0.1:0", "--public-origin", "https://uam.example.com", "--log-headers"})
	})
	must(t, startErr)
	st, running := web.ReadRunning(sessionDir)
	if !running || st.PID == os.Getpid() || strings.HasSuffix(st.Listen, ":0") {
		t.Fatalf("service state = %+v, %v", st, running)
	}
	token, err := web.LoadOrCreateToken(web.TokenPath())
	must(t, err)
	for _, want := range []string{fmt.Sprintf("uam web started (pid %d)", st.PID), "URL:           " + st.URL(), token, "Public origin: https://uam.example.com", logHeadersNotice} {
		if !strings.Contains(out, want) {
			t.Fatalf("start output lacks %q: %q", want, out)
		}
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get(st.URL() + "api/auth")
	must(t, err)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/auth = %d", resp.StatusCode)
	}

	status := captureCLIStdout(t, func() { must(t, Run(ctx, []string{"web", "status"})) })
	for _, want := range []string{fmt.Sprintf("uam web is running (pid %d)", st.PID), "Public origin: https://uam.example.com", logHeadersNotice} {
		if !strings.Contains(status, want) {
			t.Fatalf("status lacks %q: %q", want, status)
		}
	}

	if out := captureCLIStdout(t, func() { must(t, Run(ctx, []string{"web", "stop"})) }); strings.TrimSpace(out) != "uam web stopped" {
		t.Fatalf("stop = %q", out)
	}
	if _, running := web.ReadRunning(sessionDir); running {
		t.Fatal("the service still runs after stop")
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "web.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("web.json after stop: %v", err)
	}
	if resp, err := client.Get(st.URL() + "api/auth"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("the service still answers after stop")
	}
}

// uam web reports a service only once it verifiably runs with sign-in
// required: a refused start, a ready report without web.json and a legacy
// insecure service are errors, and no access details are printed.
func TestWebStartRefusesAnUnverifiedService(t *testing.T) {
	busy, err := net.Listen("tcp4", "127.0.0.1:0")
	must(t, err)
	defer func() { _ = busy.Close() }()
	for _, tc := range []struct{ name, mode, listen, want string }{
		{"service refused", "", busy.Addr().String(), "listen on " + busy.Addr().String()},
		{"ready without state", "ready-without-state", "127.0.0.1:0", "uam web reported ready but is not running"},
		{"legacy service", "legacy", "127.0.0.1:0", legacyNoAuthNotice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessionDir := secureSessionDir(t)
			t.Setenv("UAM_SESSION_DIR", sessionDir)
			t.Setenv("UAM_CONFIG_DIR", t.TempDir())
			t.Setenv("UAM_TEST_WEB_SERVICE", tc.mode)
			t.Cleanup(func() { killWebService(sessionDir) })
			var startErr error
			out := captureCLIStdout(t, func() { startErr = Run(context.Background(), []string{"web", "--listen", tc.listen}) })
			if startErr == nil || !strings.Contains(startErr.Error(), tc.want) {
				t.Fatalf("uam web = %v, want %q", startErr, tc.want)
			}
			if out != "" {
				t.Fatalf("a refused start printed %q", out)
			}
		})
	}
}

// Bad arguments are refused before anything runs; help is not an error.
func TestWebArgumentErrorsAndHelp(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", secureSessionDir(t))
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	for _, tc := range []struct {
		args []string
		want string // "" for help
	}{
		{[]string{"web", "extra"}, `web: unexpected arguments ["extra"]`},
		{[]string{"web", "status", "extra"}, `web status: unexpected arguments ["extra"]`},
		{[]string{"web", "status", "--bogus"}, "flag provided but not defined: -bogus"},
		{[]string{"web", "stop", "extra"}, `web stop: unexpected arguments ["extra"]`},
		{[]string{"__web", "--listen", "example.com:8260"}, "not an IP address"},
		{[]string{"serve"}, `unknown command "serve"`},
		{[]string{"web", "--help"}, ""},
		{[]string{"web", "status", "-h"}, ""},
	} {
		var err error
		var out string
		stderr := captureCLIStderr(t, func() {
			out = captureCLIStdout(t, func() { err = Run(context.Background(), tc.args) })
		})
		if out != "" {
			t.Fatalf("%q ran: %q", tc.args, out)
		}
		if tc.want == "" {
			if err != nil || !strings.Contains(stderr, "Usage of") {
				t.Fatalf("%q = %v, %q; want usage", tc.args, err, stderr)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q = %v, want %q", tc.args, err, tc.want)
		}
	}
	if _, err := os.Stat(web.TokenPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("argument errors created a token: %v", err)
	}
}

// uam web cannot start without its token directory, nor uam __web without
// its runtime directory; neither leaves a service behind.
func TestWebStartupNeedsItsDirectories(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	must(t, os.WriteFile(blocked, []byte("x"), 0o600))
	t.Setenv("UAM_WEB_READY_FD", "")
	for _, tc := range []struct {
		command, env, want string
	}{
		{"web", "UAM_CONFIG_DIR", "create token directory"},
		{"__web", "UAM_SESSION_DIR", "not a directory"},
	} {
		sessionDir := secureSessionDir(t)
		t.Setenv("UAM_SESSION_DIR", sessionDir)
		t.Setenv("UAM_CONFIG_DIR", t.TempDir())
		t.Setenv(tc.env, blocked)
		if err := Run(context.Background(), []string{tc.command, "--listen", "127.0.0.1:0"}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("uam %s = %v, want %q", tc.command, err, tc.want)
		}
		if _, running := web.ReadRunning(sessionDir); running {
			t.Fatalf("uam %s left a service running", tc.command)
		}
	}
}

// uam web stop reports a service that has not stopped yet when the caller
// gives up waiting.
func TestWebStopReportsAnUnfinishedStop(t *testing.T) {
	start := daemonruntime.ProcStartTime(os.Getpid())
	if start == 0 {
		t.Skip("process start identity is unavailable")
	}
	sessionDir := secureSessionDir(t)
	t.Setenv("UAM_SESSION_DIR", sessionDir)
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	// This process stands in for the service: it holds the service lock, is
	// named in web.json and absorbs the SIGTERM that uam web stop sends.
	lock, err := os.OpenFile(filepath.Join(sessionDir, "web.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	must(t, err)
	defer func() { _ = lock.Close() }()
	must(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	terms := make(chan os.Signal, 1)
	signal.Notify(terms, syscall.SIGTERM)
	defer signal.Stop(terms)
	data, err := json.Marshal(web.DaemonState{PID: os.Getpid(), StartTime: start, Listen: "127.0.0.1:8260", Version: "test"})
	must(t, err)
	must(t, os.WriteFile(filepath.Join(sessionDir, "web.json"), data, 0o600))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stopErr error
	out := captureCLIStdout(t, func() { stopErr = Run(ctx, []string{"web", "stop"}) })
	if !errors.Is(stopErr, context.Canceled) || out != "" {
		t.Fatalf("stop = %v, %q; want the caller's error and no claim", stopErr, out)
	}
	select {
	case <-terms:
	case <-time.After(10 * time.Second):
		t.Fatal("uam web stop did not signal the service")
	}
}

// Access details for a service whose recorded listen address cannot be
// parsed fall back to the default port for the SSH forward.
func TestWebAccessFallsBackToTheDefaultForward(t *testing.T) {
	sessionDir := secureSessionDir(t)
	t.Setenv("UAM_SESSION_DIR", sessionDir)
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	// A state file naming this test process stands in for a running service.
	data, err := json.Marshal(web.DaemonState{PID: os.Getpid(), StartTime: daemonruntime.ProcStartTime(os.Getpid()), Listen: "unix-socket", Version: "test"})
	must(t, err)
	must(t, os.WriteFile(filepath.Join(sessionDir, "web.json"), data, 0o600))
	out := captureCLIStdout(t, func() { must(t, Run(context.Background(), []string{"web"})) })
	for _, want := range []string{"already running", "Listen:        unix-socket", "ssh -N -L 127.0.0.1:8260:127.0.0.1:8260 ", "then open http://127.0.0.1:8260/"} {
		if !strings.Contains(out, want) {
			t.Fatalf("access details lack %q: %q", want, out)
		}
	}
}

// uam web token set --help explains the command without setting a token,
// and input that cannot be read sets nothing.
func TestWebTokenSetHelpAndUnreadableInput(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", secureSessionDir(t))
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	var runErr error
	help := captureCLIStderr(t, func() { runErr = Run(context.Background(), []string{"web", "token", "set", "--help"}) })
	if runErr != nil || !strings.Contains(help, "usage: uam web token set") || !strings.Contains(help, "run uam web stop first") {
		t.Fatalf("token set --help = %v, %q", runErr, help)
	}

	directory, err := os.Open(t.TempDir())
	must(t, err)
	defer func() { _ = directory.Close() }()
	old := os.Stdin
	os.Stdin = directory
	runErr = Run(context.Background(), []string{"web", "token", "set"})
	os.Stdin = old
	if runErr == nil || !strings.Contains(runErr.Error(), "read access token") {
		t.Fatalf("token set from unreadable stdin = %v, want a read error", runErr)
	}
	if _, err := os.Stat(web.TokenPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token set without input created a token: %v", err)
	}
}
