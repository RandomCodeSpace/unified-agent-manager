package web

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/daemonruntime"
	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// lockedBuffer collects log records written from the service's goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// shutdownFails is a provider that stops but reports a shutdown failure.
type shutdownFails struct{ *agenttest.Provider }

func (p shutdownFails) Shutdown(ctx context.Context) error {
	_ = p.Provider.Shutdown(ctx)
	return errors.New("provider did not stop cleanly")
}

// ownerOnlyDir returns a fresh 0700 directory: t.TempDir creates it 0777
// minus the umask, which ReadRunning refuses.
func ownerOnlyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// serveDaemon runs the service in this process until the test stops it, and
// returns once it reported ready. Signals to this process are safe only from
// then on: the service registers for them just before it reports ready.
func serveDaemon(t *testing.T, cfg DaemonConfig) (dir string, done <-chan error, logs *lockedBuffer) {
	t.Helper()
	dir = ownerOnlyDir(t)
	t.Setenv("UAM_SESSION_DIR", dir)
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	logs = &lockedBuffer{}
	previous := uamlog.SetLogger(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { uamlog.SetLogger(previous) })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	result := make(chan error, 1)
	go func() { result <- runDaemon(cfg, w) }()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(r).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ok\n" {
			t.Fatalf("readiness report = %q", line)
		}
	case err := <-result:
		t.Fatalf("service ended before it was ready: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("service did not report ready")
	}
	return dir, result, logs
}

// stopDaemon stops the service with Stop and returns runDaemon's result.
func stopDaemon(t *testing.T, dir string, done <-chan error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if running, err := Stop(ctx, dir); !running || err != nil {
		t.Fatalf("Stop = %v, %v; want a stopped service", running, err)
	}
	select {
	case err := <-done:
		return err
	case <-time.After(20 * time.Second):
		t.Fatal("service did not return after Stop")
		return nil
	}
}

// daemonClient opens a fresh connection per request, so a stopped service
// cannot answer from a pooled one.
var daemonClient = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

func getAuth(url string) (int, string, error) {
	resp, err := daemonClient.Get(url + "api/auth")
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(body)), err
}

// The service publishes web.json and holds the single-instance lock while it
// serves with the owner's token, ignores SIGHUP (a closing terminal), and on
// SIGTERM from Stop stops serving, removes web.json, releases the lock and
// shuts its providers down.
func TestDaemonServesUntilStopped(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	dir, done, logs := serveDaemon(t, DaemonConfig{
		Listen: "127.0.0.1:0", PublicOrigins: []string{"https://UAM.example.com/"}, LogHeaders: true,
		Providers: []agentapi.Provider{prov}, Version: "test",
	})
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_, _ = Stop(context.Background(), dir)
		}
	})

	st, running := ReadRunning(dir)
	if !running || st.PID != os.Getpid() || strings.HasSuffix(st.Listen, ":0") || !strings.HasPrefix(st.Listen, "127.0.0.1:") {
		t.Fatalf("published state = %+v, %v", st, running)
	}
	if !slices.Equal(st.PublicOrigins, []string{"https://uam.example.com"}) || !st.LogHeaders || st.Version != "test" || st.LegacyNoAuth {
		t.Fatalf("published settings = %+v", st)
	}
	token, err := LoadOrCreateToken(TokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(statePath(dir)); err != nil || strings.Contains(string(data), token) {
		t.Fatalf("web.json = %q, %v; it must not hold the access token", data, err)
	}
	if code, body, err := getAuth(st.URL()); err != nil || code != http.StatusOK || body != `{"authenticated":false,"required":true}` {
		t.Fatalf("/api/auth = %d %q, %v", code, body, err)
	}
	login, err := http.NewRequest(http.MethodPost, st.URL()+"api/login", strings.NewReader(`{"token":"`+token+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	login.Header.Set("Content-Type", "application/json")
	resp, err := daemonClient.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || len(resp.Cookies()) != 1 {
		t.Fatalf("login with the token file's token = %d", resp.StatusCode)
	}

	// A second service refuses while this one holds the lock.
	if err := runDaemon(DaemonConfig{Listen: "127.0.0.1:0"}, nil); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second service = %v, want already running", err)
	}
	if again, ok := ReadRunning(dir); !ok || again.PID != st.PID || again.Listen != st.Listen {
		t.Fatalf("refused second service changed the state: %+v, %v", again, ok)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "SIGHUP to be ignored", func() bool { return strings.Contains(logs.String(), "uam web ignoring SIGHUP") })
	select {
	case err := <-done:
		t.Fatalf("SIGHUP stopped the service: %v", err)
	default:
	}
	if code, _, err := getAuth(st.URL()); err != nil || code != http.StatusOK {
		t.Fatalf("after SIGHUP /api/auth = %d, %v", code, err)
	}

	stopped = true
	if err := stopDaemon(t, dir, done); err != nil {
		t.Fatalf("service result = %v", err)
	}
	if _, err := os.Stat(statePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("web.json after stop: %v", err)
	}
	if _, running := ReadRunning(dir); running || !lockFree(dir) {
		t.Fatal("a stopped service still looks running or holds the lock")
	}
	if prov.ShutdownCalls() != 1 {
		t.Fatalf("provider shutdown calls = %d", prov.ShutdownCalls())
	}
	if _, _, err := getAuth(st.URL()); err == nil {
		t.Fatal("the service still answers after stop")
	}
	if out := logs.String(); !strings.Contains(out, "uam web stopping") || !strings.Contains(out, "signal=terminated") || !strings.Contains(out, "uam web stopped") {
		t.Fatalf("stop was not logged: %q", out)
	}
}

// A provider that fails to shut down is logged; the service still stops
// cleanly and leaves no running state.
func TestDaemonStopsDespiteAProviderShutdownFailure(t *testing.T) {
	prov := shutdownFails{agenttest.NewProvider("fake", allCaps)}
	dir, done, logs := serveDaemon(t, DaemonConfig{Listen: "127.0.0.1:0", Providers: []agentapi.Provider{prov}})
	if err := stopDaemon(t, dir, done); err != nil {
		t.Fatalf("service result = %v", err)
	}
	if prov.ShutdownCalls() != 1 {
		t.Fatalf("provider shutdown calls = %d", prov.ShutdownCalls())
	}
	if out := logs.String(); !strings.Contains(out, "uam web manager shutdown") || !strings.Contains(out, "provider did not stop cleanly") {
		t.Fatalf("shutdown failure was not logged: %q", out)
	}
	if _, running := ReadRunning(dir); running || !lockFree(dir) {
		t.Fatal("the service left running state behind")
	}
}

// The service refuses to start without a usable runtime directory and lock,
// and a refusal publishes nothing.
func TestDaemonStartupRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string) string // returns UAM_SESSION_DIR
		want  string
	}{
		{"runtime dir is a file", func(t *testing.T, dir string) string {
			file := filepath.Join(dir, "file")
			if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			return file
		}, "not a directory"},
		{"lock is not a file", func(t *testing.T, dir string) string {
			if err := os.Mkdir(filepath.Join(dir, lockFileName), 0o700); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "open web lock"},
		{"lock is held", func(t *testing.T, dir string) string {
			lock, err := lockDaemon(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lock.Close() })
			return dir
		}, "uam web is already running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := ownerOnlyDir(t)
			runtimeDir := tc.setup(t, dir)
			t.Setenv("UAM_SESSION_DIR", runtimeDir)
			t.Setenv("UAM_CONFIG_DIR", t.TempDir())
			prov := agenttest.NewProvider("fake", allCaps)
			if err := runDaemon(DaemonConfig{Listen: "127.0.0.1:0", Providers: []agentapi.Provider{prov}}, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("startup = %v, want %q", err, tc.want)
			}
			if runtimeDir == dir && lockFree(dir) {
				t.Fatal("the lock reads as free")
			}
			if _, err := os.Stat(statePath(dir)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refused start published web.json: %v", err)
			}
			if prov.ShutdownCalls() != 0 {
				t.Fatal("refused start ran the providers")
			}
		})
	}
}

// ReadRunning trusts web.json only when it parses and names a live process
// with a known, matching start identity.
func TestReadRunningRefusesUnusableState(t *testing.T) {
	start := daemonruntime.ProcStartTime(os.Getpid())
	if start == 0 {
		t.Skip("process start identity is unavailable")
	}
	self := DaemonState{PID: os.Getpid(), StartTime: start, Listen: "127.0.0.1:8260", Version: "test"}
	pid, startTime := strconv.Itoa(self.PID), strconv.FormatInt(start, 10)
	for _, tc := range []struct {
		name, data string
		running    bool
	}{
		{"live", `{"pid":` + pid + `,"start_time":` + startTime + `,"listen":"127.0.0.1:8260","version":"test"}`, true},
		{"corrupt", `{"pid":`, false},
		{"no pid", `{"pid":0,"start_time":` + startTime + `,"listen":"127.0.0.1:8260"}`, false},
		{"unknown start identity", `{"pid":` + pid + `,"listen":"127.0.0.1:8260"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := ownerOnlyDir(t)
			if err := os.WriteFile(statePath(dir), []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			st, running := ReadRunning(dir)
			want := DaemonState{}
			if tc.running {
				want = self
			}
			if running != tc.running || !reflect.DeepEqual(st, want) {
				t.Fatalf("ReadRunning = %+v, %v; want %+v, %v", st, running, want, tc.running)
			}
		})
	}
	missing := filepath.Join(ownerOnlyDir(t), "missing")
	if err := writeStateFile(missing, self); err == nil {
		t.Fatal("web.json was published into a missing directory")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publishing created the runtime directory: %v", err)
	}
}

// Stop gives up when its caller does: a service that holds the lock but has
// not exited yet is reported as running, with the caller's error.
func TestStopReturnsWhenTheCallerGivesUp(t *testing.T) {
	start := daemonruntime.ProcStartTime(os.Getpid())
	if start == 0 {
		t.Skip("process start identity is unavailable")
	}
	dir := ownerOnlyDir(t)
	lock, err := lockDaemon(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if _, err := lockDaemon(dir); err == nil || err.Error() != "uam web is already running" {
		t.Fatalf("second lock = %v, want already running", err)
	}
	// This process stands in for the service: web.json names it and it
	// absorbs the SIGTERM that Stop sends.
	terms := make(chan os.Signal, 1)
	signal.Notify(terms, syscall.SIGTERM)
	defer signal.Stop(terms)
	if err := writeStateFile(dir, DaemonState{PID: os.Getpid(), StartTime: start, Listen: "127.0.0.1:8260"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if running, err := Stop(ctx, dir); !running || !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop = %v, %v; want running with the caller's error", running, err)
	}
	select {
	case <-terms:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not signal the service")
	}
	if _, running := ReadRunning(dir); !running {
		t.Fatal("an unfinished stop removed the service's state")
	}
}
