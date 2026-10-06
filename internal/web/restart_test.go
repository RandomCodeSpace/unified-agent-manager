package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// installBinary writes a fake uam at path that runs script, replacing any
// file there the way an install does: a new file renamed into place.
func installBinary(t *testing.T, path, script string) {
	t.Helper()
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// fakeBinary is a fake uam printing version, and a count of the version
// reads its watch makes.
func fakeBinary(t *testing.T, version string) (string, *binaryWatch, *atomic.Int32) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "uam")
	installBinary(t, path, "echo "+version)
	w := newBinaryWatch(path, version, nil)
	reads := &atomic.Int32{}
	w.readVersion = func(ctx context.Context, path string) (string, error) {
		reads.Add(1)
		return runVersion(ctx, path)
	}
	return path, w, reads
}

// restartManager starts a manager following w.
func restartManager(t *testing.T, w *binaryWatch) (*Manager, *agenttest.Provider, *testServer, reqOpt) {
	t.Helper()
	prov := agenttest.NewProvider("fake", allCaps)
	m := NewManager(openTestStore(t), []agentapi.Provider{prov})
	m.binary = w
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: w.running})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m}
	return m, prov, ts, withCookie(ts)
}

func serviceCall(t *testing.T, ts *testServer, auth reqOpt, method, path string) (int, ServiceStatus, string) {
	t.Helper()
	w := ts.do(method, path, map[string]string{http.MethodGet: "", http.MethodPost: "{}"}[method], auth)
	var st ServiceStatus
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return w.Code, st, w.Body.String()
}

func restartBegun(w *binaryWatch) bool {
	select {
	case <-w.requests:
		return true
	default:
		return false
	}
}

// An unchanged binary is never run; a replaced one is read once, and a
// version other than the running one offers a restart.
func TestBinaryWatchReadsAReplacedBinaryOnce(t *testing.T) {
	path, w, reads := fakeBinary(t, "v1.0.0")
	now := time.Now()
	w.look(context.Background(), now, 0)
	if st := w.status(); reads.Load() != 0 || st != (ServiceStatus{Running: "v1.0.0", Installed: "v1.0.0", Restart: restartNone}) {
		t.Fatalf("unchanged binary: %d reads, %+v", reads.Load(), st)
	}

	installBinary(t, path, "echo v1.1.0")
	// A status read inside the gap does not look.
	w.look(context.Background(), now.Add(binaryReadGap/2), binaryReadGap)
	if reads.Load() != 0 {
		t.Fatalf("a look inside the gap read the binary")
	}
	w.look(context.Background(), now.Add(binaryReadGap), binaryReadGap)
	if st := w.status(); reads.Load() != 1 || st != (ServiceStatus{Running: "v1.0.0", Installed: "v1.1.0", Restart: restartAvailable}) {
		t.Fatalf("replaced binary: %d reads, %+v", reads.Load(), st)
	}
	w.look(context.Background(), now, 0)
	if reads.Load() != 1 {
		t.Fatalf("an unchanged replacement was read again: %d reads", reads.Load())
	}

	// The running version installed again offers nothing.
	installBinary(t, path, "echo v1.0.0")
	w.look(context.Background(), now, 0)
	if st := w.status(); st.Restart != restartNone || st.Installed != "v1.0.0" {
		t.Fatalf("the running version reinstalled: %+v", st)
	}
}

// A binary that cannot be run, or is gone, is a short error and no restart.
func TestBinaryWatchReportsUnreadableBinaries(t *testing.T) {
	path, w, _ := fakeBinary(t, "v1.0.0")
	installBinary(t, path, "echo v2.0.0")
	w.look(context.Background(), time.Now(), 0)
	if !w.newerLocked() {
		t.Fatal("v2.0.0 was not read")
	}

	installBinary(t, path, "echo broken >&2; exit 3")
	w.look(context.Background(), time.Now(), 0)
	if st := w.status(); st.Installed != "" || st.Restart != restartNone || !strings.Contains(st.Error, "exit status 3") {
		t.Fatalf("failing version run: %+v", st)
	}
	if err := w.request(); err == nil || !strings.Contains(err.Error(), "cannot restart") {
		t.Fatalf("restart onto a failing binary = %v", err)
	}

	installBinary(t, path, "echo")
	w.look(context.Background(), time.Now(), 0)
	if st := w.status(); !strings.Contains(st.Error, "unexpected version output") {
		t.Fatalf("empty version output: %+v", st)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w.look(context.Background(), time.Now(), 0)
	if st := w.status(); st.Installed != "" || st.Restart != restartNone || !strings.Contains(st.Error, "no such file") {
		t.Fatalf("missing binary: %+v", st)
	}
	// Installed again, it is read again.
	installBinary(t, path, "echo v2.0.1")
	w.look(context.Background(), time.Now(), 0)
	if st := w.status(); st.Installed != "v2.0.1" || st.Error != "" || st.Restart != restartAvailable {
		t.Fatalf("reinstalled binary: %+v", st)
	}

	unresolved := newBinaryWatch("", "v1.0.0", errors.New("readlink /proc/self/exe: no such file"))
	unresolved.look(context.Background(), time.Now(), 0)
	if st := unresolved.status(); st.Restart != restartNone || !strings.Contains(st.Error, "locate the uam binary") {
		t.Fatalf("unresolved binary: %+v", st)
	}
}

func TestParseVersion(t *testing.T) {
	for out, want := range map[string]string{"v0.15.4\n": "v0.15.4", " v0.7.1-179-g81303b25 \nmore\n": "v0.7.1-179-g81303b25", "dev": "dev"} {
		if got, err := parseVersion(out); err != nil || got != want {
			t.Errorf("parseVersion(%q) = %q, %v", out, got, err)
		}
	}
	for _, out := range []string{"", "\n", "uam v1", "v1\x1b[31m", strings.Repeat("v", 129)} {
		if got, err := parseVersion(out); err == nil {
			t.Errorf("parseVersion(%q) = %q, want an error", out, got)
		}
	}
}

// With no Task busy a restart begins at once; meta reports it for the dot.
func TestRestartBeginsWhenIdle(t *testing.T) {
	path, w, _ := fakeBinary(t, "v1.0.0")
	m, prov, ts, auth := restartManager(t, w)
	createSession(t, m, prov)
	installBinary(t, path, "echo v1.1.0")
	code, st, body := serviceCall(t, ts, auth, http.MethodGet, "/api/service")
	if code != http.StatusOK || st.Installed != "v1.1.0" || st.Restart != restartAvailable {
		t.Fatalf("GET = %d %s", code, body)
	}
	var meta Meta
	if err := json.Unmarshal(ts.do(http.MethodGet, "/api/meta", "", auth).Body.Bytes(), &meta); err != nil || meta.Service == nil || meta.Service.Restart != restartAvailable || meta.Service.Installed != "v1.1.0" {
		t.Fatalf("meta service = %+v, %v", meta.Service, err)
	}
	code, st, body = serviceCall(t, ts, auth, http.MethodPost, "/api/service/restart")
	if code != http.StatusOK || st.Restart != restartRestarting {
		t.Fatalf("POST = %d %s", code, body)
	}
	if !restartBegun(w) {
		t.Fatal("the daemon was not asked to restart")
	}
	if stopped, err := m.shutdownIfIdle(context.Background()); !stopped || err != nil {
		t.Fatalf("shutdownIfIdle = %v, %v", stopped, err)
	}
}

// A restart waits while a Task works, and begins once none does; the
// daemon's final check refuses a Task that started meanwhile.
func TestRestartWaitsForBusyTasks(t *testing.T) {
	path, w, _ := fakeBinary(t, "v1.0.0")
	m, prov, ts, auth := restartManager(t, w)
	sum, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	installBinary(t, path, "echo v1.1.0")

	code, st, body := serviceCall(t, ts, auth, http.MethodPost, "/api/service/restart")
	if code != http.StatusOK || st.Restart != restartPending || st.Installed != "v1.1.0" {
		t.Fatalf("POST while working = %d %s", code, body)
	}
	m.tryRestart()
	if restartBegun(w) {
		t.Fatal("a restart began while a Task works")
	}
	if stopped, _ := m.shutdownIfIdle(context.Background()); stopped {
		t.Fatal("shutdownIfIdle shut down while a Task works")
	}

	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "the restart to begin", func() bool { return w.status().Restart == restartRestarting })
	if !restartBegun(w) {
		t.Fatal("the daemon was not asked to restart")
	}

	// A Task that starts before the daemon shuts down puts it back to pending.
	conv.EmitTurn(agentapi.TurnWorking, "")
	waitUntil(t, "the Task to work", func() bool { return mustSummary(t, m, sum.ID).State == StateWorking })
	if stopped, _ := m.shutdownIfIdle(context.Background()); stopped {
		t.Fatal("shutdownIfIdle shut down while a Task works")
	}
	w.postpone()
	if st := w.status(); st.Restart != restartPending {
		t.Fatalf("postponed restart = %+v", st)
	}
}

// A requested restart is dropped when the binary is replaced by the running
// version again before it begins.
func TestRestartDroppedWhenTheRunningVersionReturns(t *testing.T) {
	path, w, _ := fakeBinary(t, "v1.0.0")
	m, prov, ts, auth := restartManager(t, w)
	_, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	installBinary(t, path, "echo v1.1.0")
	if code, st, body := serviceCall(t, ts, auth, http.MethodPost, "/api/service/restart"); code != http.StatusOK || st.Restart != restartPending {
		t.Fatalf("POST = %d %s", code, body)
	}
	installBinary(t, path, "echo v1.0.0")
	conv.EmitTurn(agentapi.TurnCompleted, "")
	m.tryRestart()
	if restartBegun(w) || w.status().Restart != restartNone {
		t.Fatalf("restart after the running version returned: %+v", w.status())
	}
	if code, _, body := serviceCall(t, ts, auth, http.MethodPost, "/api/service/restart"); code != http.StatusConflict || !strings.Contains(body, "nothing to restart onto") {
		t.Fatalf("POST with the running version installed = %d %s", code, body)
	}
}

func TestServiceRoutesWithoutABinaryWatch(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		path := map[string]string{http.MethodGet: "/api/service", http.MethodPost: "/api/service/restart"}[method]
		if code, _, body := serviceCall(t, ts, auth, method, path); code != http.StatusNotFound {
			t.Fatalf("%s %s = %d %s", method, path, code, body)
		}
	}
	var meta Meta
	if err := json.Unmarshal(ts.do(http.MethodGet, "/api/meta", "", auth).Body.Bytes(), &meta); err != nil || meta.Service != nil {
		t.Fatalf("meta service = %+v, %v", meta.Service, err)
	}
}

// A restart stops the service like uam web stop (state removed, lock
// released, providers shut down) and ends runDaemon with errRestart.
func TestDaemonStopsForARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uam")
	installBinary(t, path, "echo v1.0.0")
	prov := agenttest.NewProvider("fake", allCaps)
	dir, done, logs := serveDaemon(t, DaemonConfig{Listen: "127.0.0.1:0", Providers: []agentapi.Provider{prov}, Version: "v1.0.0", Executable: path})
	st, running := ReadRunning(dir)
	if !running {
		t.Fatal("service not running")
	}
	installBinary(t, path, "echo v1.1.0")
	token, err := LoadOrCreateToken(TokenPath())
	if err != nil {
		t.Fatal(err)
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
	restart, err := http.NewRequest(http.MethodPost, st.URL()+"api/service/restart", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	restart.Header.Set("Content-Type", "application/json")
	restart.AddCookie(resp.Cookies()[0])
	resp, err = daemonClient.Do(restart)
	if err != nil {
		t.Fatal(err)
	}
	var status ServiceStatus
	_ = json.NewDecoder(resp.Body).Decode(&status)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || status.Restart != restartRestarting {
		t.Fatalf("POST restart = %d %+v", resp.StatusCode, status)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errRestart) {
			t.Fatalf("runDaemon = %v, want errRestart", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("service did not stop for the restart")
	}
	if _, running := ReadRunning(dir); running || !lockFree(dir) {
		t.Fatal("a service stopped for a restart still looks running or holds the lock")
	}
	if _, err := os.Stat(statePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("web.json after the stop: %v", err)
	}
	if prov.ShutdownCalls() != 1 || !strings.Contains(logs.String(), "uam web stopping to restart") {
		t.Fatalf("provider shutdowns %d; logs %q", prov.ShutdownCalls(), logs.String())
	}
}

// The restart runs the binary at the resolved path with the service's
// arguments and environment; a failed exec is an error, not a return to
// serving.
func TestRestartServiceExecsTheBinary(t *testing.T) {
	var gotPath string
	var gotArgv, gotEnv []string
	execve = func(path string, argv, env []string) error {
		gotPath, gotArgv, gotEnv = path, argv, env
		return syscall.ENOEXEC
	}
	t.Cleanup(func() { execve = syscall.Exec })
	err := restartService("/opt/uam/bin/uam", []string{"__web", "--listen", "127.0.0.1:8260", "--log-headers"}, []string{"HOME=/home/u", "PATH=/usr/bin"})
	if !errors.Is(err, syscall.ENOEXEC) || !strings.Contains(err.Error(), "restart uam web onto /opt/uam/bin/uam") {
		t.Fatalf("restartService = %v", err)
	}
	if gotPath != "/opt/uam/bin/uam" || !slices.Equal(gotArgv, []string{"/opt/uam/bin/uam", "__web", "--listen", "127.0.0.1:8260", "--log-headers"}) || !slices.Equal(gotEnv, []string{"HOME=/home/u", "PATH=/usr/bin"}) {
		t.Fatalf("exec %q %q %q", gotPath, gotArgv, gotEnv)
	}
}
