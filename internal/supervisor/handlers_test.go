package supervisor

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/ipc"
)

// supForHandlers builds a supervisor without running it; just enough to
// exercise dispatch handlers in-process.
func supForHandlers(t *testing.T) *Supervisor {
	t.Helper()
	s, err := New(Options{RuntimeDir: t.TempDir(), HostExe: "/bin/true"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestDispatchEachKindReachesHandler(t *testing.T) {
	s := supForHandlers(t)
	kinds := []ipc.Kind{ipc.KindHello, ipc.KindList, ipc.KindHas, ipc.KindSpawn,
		ipc.KindCapture, ipc.KindWrite, ipc.KindResize, ipc.KindKill, ipc.KindStatus}
	for _, k := range kinds {
		resp := s.dispatch(ipc.Request{Kind: k, Payload: []byte("{}")})
		if resp == nil {
			t.Fatalf("dispatch(%d) returned nil", k)
		}
	}
}

func TestDispatchHello(t *testing.T) {
	s := supForHandlers(t)
	resp := s.dispatch(ipc.Request{Kind: ipc.KindHello, ID: 1})
	var out struct {
		Pid int `json:"pid"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("Unmarshal: %v: %q", err, resp)
	}
	if out.Pid != os.Getpid() {
		t.Fatalf("expected pid %d, got %d", os.Getpid(), out.Pid)
	}
}

func TestDispatchUnknownKind(t *testing.T) {
	s := supForHandlers(t)
	resp := s.dispatch(ipc.Request{Kind: ipc.Kind(200)})
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("Unmarshal: %v: %q", err, resp)
	}
	if out.Error == "" {
		t.Fatalf("expected non-empty error")
	}
}

func TestHandleHasReturnsFalseForUnknown(t *testing.T) {
	s := supForHandlers(t)
	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
	}{Handle: "nope"})
	resp := s.handleHas(payload)
	var out struct {
		Has bool `json:"has"`
	}
	_ = json.Unmarshal(resp, &out)
	if out.Has {
		t.Fatalf("expected false for unknown session")
	}
}

func TestHandleHasReturnsTrueWhenPresent(t *testing.T) {
	s := supForHandlers(t)
	s.sessions["k"] = SessionRecord{ID: "k"}
	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
	}{Handle: "k"})
	resp := s.handleHas(payload)
	var out struct {
		Has bool `json:"has"`
	}
	_ = json.Unmarshal(resp, &out)
	if !out.Has {
		t.Fatalf("expected true")
	}
}

func TestHandleListAppliesPrefix(t *testing.T) {
	s := supForHandlers(t)
	s.sessions["alpha-1"] = SessionRecord{ID: "alpha-1"}
	s.sessions["beta-2"] = SessionRecord{ID: "beta-2"}
	payload, _ := json.Marshal(struct {
		Prefix string `json:"prefix"`
	}{Prefix: "alpha-"})
	resp := s.handleList(payload)
	var out struct {
		Sessions []SessionRecord `json:"sessions"`
	}
	_ = json.Unmarshal(resp, &out)
	if len(out.Sessions) != 1 {
		t.Fatalf("expected 1, got %d", len(out.Sessions))
	}
	if out.Sessions[0].ID != "alpha-1" {
		t.Fatalf("expected alpha-1, got %s", out.Sessions[0].ID)
	}
}

func TestHandleSpawnReportsBadPayload(t *testing.T) {
	s := supForHandlers(t)
	resp := s.handleSpawn([]byte("not-json"))
	var out struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(resp, &out)
	if out.Error == "" {
		t.Fatalf("expected error payload, got %q", resp)
	}
}

func TestHandleCaptureRejectsBadPayload(t *testing.T) {
	s := supForHandlers(t)
	resp := s.handleCapture([]byte("{invalid"))
	if !hasErr(resp) {
		t.Fatalf("expected error: %q", resp)
	}
}

func TestHandleWriteRejectsBadPayload(t *testing.T) {
	s := supForHandlers(t)
	if !hasErr(s.handleWrite([]byte("{invalid"))) {
		t.Fatalf("expected error")
	}
}

func TestHandleResizeRejectsBadPayload(t *testing.T) {
	s := supForHandlers(t)
	if !hasErr(s.handleResize([]byte("{invalid"))) {
		t.Fatalf("expected error")
	}
}

func TestHandleKillUnknownSession(t *testing.T) {
	s := supForHandlers(t)
	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
	}{Handle: "nope"})
	if !hasErr(s.handleKill(payload)) {
		t.Fatalf("expected error for unknown session")
	}
}

func TestHandleStatusUnknownSession(t *testing.T) {
	s := supForHandlers(t)
	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
	}{Handle: "nope"})
	if !hasErr(s.handleStatus(payload)) {
		t.Fatalf("expected error for unknown session")
	}
}

func TestHandleShutdownClosesStop(t *testing.T) {
	s := supForHandlers(t)
	resp := s.dispatch(ipc.Request{Kind: ipc.KindShutdown})
	if hasErr(resp) {
		t.Fatalf("unexpected error: %q", resp)
	}
	// Calling Shutdown twice must be safe (sync.Once).
	s.Shutdown()
}

func TestHasPrefixHelper(t *testing.T) {
	cases := []struct {
		s, p string
		want bool
	}{
		{"alpha-1", "alpha-", true},
		{"beta-2", "alpha-", false},
		{"", "", true},
		{"x", "xy", false},
	}
	for _, c := range cases {
		if got := hasPrefix(c.s, c.p); got != c.want {
			t.Fatalf("hasPrefix(%q,%q)=%v want %v", c.s, c.p, got, c.want)
		}
	}
}

func TestHostPathHelpers(t *testing.T) {
	s := supForHandlers(t)
	if got := s.hostSocketPath("foo"); got != filepath.Join(s.hostsDir, "foo.sock") {
		t.Fatalf("hostSocketPath: %s", got)
	}
	if got := s.hostJournalPath("foo"); got != filepath.Join(s.runtimeDir, "journals", "foo.log") {
		t.Fatalf("hostJournalPath: %s", got)
	}
	if got := s.HostsDir(); got != s.hostsDir {
		t.Fatalf("HostsDir mismatch")
	}
}

func TestDefaultRuntimeDirHonorsEnv(t *testing.T) {
	t.Setenv("UAM_RUNTIME_DIR", "/some/custom")
	if got := DefaultRuntimeDir(); got != "/some/custom" {
		t.Fatalf("UAM_RUNTIME_DIR not honored: %s", got)
	}
	t.Setenv("UAM_RUNTIME_DIR", "")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got := DefaultRuntimeDir(); got != "/run/user/1000/uam" {
		t.Fatalf("XDG_RUNTIME_DIR not honored: %s", got)
	}
}

func TestRemoveSession(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(Options{RuntimeDir: dir, HostExe: "/bin/true"})
	id := "k"
	s.sessions[id] = SessionRecord{ID: id, SocketPath: s.hostSocketPath(id)}
	// Touch the socket file so removeSession can unlink it.
	_ = os.MkdirAll(s.hostsDir, 0o700)
	_ = os.WriteFile(s.hostSocketPath(id), []byte{}, 0o600)
	s.removeSession(id)
	s.mu.Lock()
	_, ok := s.sessions[id]
	s.mu.Unlock()
	if ok {
		t.Fatalf("session %q not removed", id)
	}
	if _, err := os.Stat(s.hostSocketPath(id)); !os.IsNotExist(err) {
		t.Fatalf("socket not unlinked")
	}
}

func TestAsJSONFallsBackOnError(t *testing.T) {
	// Channels cannot be marshaled; verify fallback to "{}".
	got := asJSON(make(chan int))
	if string(got) != "{}" {
		t.Fatalf("expected fallback {}, got %q", got)
	}
}

func TestProbeHostAliveOnLivenessAndDeath(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.sock")
	ln, err := net.Listen("unix", live)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if !probeHostAlive(live) {
		t.Fatalf("expected live socket to be detected")
	}
	dead := filepath.Join(dir, "dead.sock")
	if probeHostAlive(dead) {
		t.Fatalf("expected non-existent socket to be dead")
	}
}

func TestErrPayloadShape(t *testing.T) {
	out := errPayload("boom")
	var p struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatal(err)
	}
	if p.Error != "boom" {
		t.Fatalf("got %q", p.Error)
	}
}

func TestPidfileWriteUnlocksOnFlockHeld(t *testing.T) {
	// Acquire, then try to acquire again — must fail without races.
	dir := t.TempDir()
	path := filepath.Join(dir, "uam.pid")
	rel1, err := AcquirePidfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquirePidfile(path); err == nil {
		t.Fatalf("expected second AcquirePidfile to fail")
	}
	rel1()
	// After release, acquisition succeeds again.
	rel2, err := AcquirePidfile(path)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	rel2()
}

func TestSupervisorRunFailsOnLockedPidfile(t *testing.T) {
	dir := t.TempDir()
	// Pre-acquire the lock so the supervisor's Run cannot.
	release, err := AcquirePidfile(filepath.Join(dir, "uam.pid"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	s, _ := New(Options{RuntimeDir: dir, HostExe: "/bin/true"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Run(ctx); err == nil {
		t.Fatalf("expected Run to fail with locked pidfile")
	}
}

// hasErr returns true when payload contains a non-empty {"error": "..."}.
func hasErr(payload []byte) bool {
	var p struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return false
	}
	return p.Error != ""
}
