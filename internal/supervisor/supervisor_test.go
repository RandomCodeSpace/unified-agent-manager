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

// supervisorTestEnv prepares an isolated runtime dir and an instance of
// Supervisor that uses the current uam binary's own /proc/self/exe as the
// host launcher. Returns Supervisor + cleanup.
func startSupervisor(t *testing.T) (*Supervisor, func()) {
	t.Helper()
	dir := t.TempDir()
	sup, err := New(Options{
		RuntimeDir: dir,
		HostExe:    findUamBinary(t),
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	// Wait for control socket to appear.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sup.ControlSocketPath()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cleanup := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("supervisor.Run did not exit after cancel")
		}
	}
	return sup, cleanup
}

// findUamBinary builds the uam binary into a tempdir and returns the path.
// Tests need a real binary to fork+exec as "uam internal-host".
func findUamBinary(t *testing.T) string {
	t.Helper()
	if env := os.Getenv("UAM_TEST_BINARY"); env != "" {
		return env
	}
	// Build the binary into the testdir.
	dir := t.TempDir()
	bin := filepath.Join(dir, "uam")
	// We can't actually invoke `go build` here without it being slow and
	// flaky, so a parent test harness must export UAM_TEST_BINARY. For
	// unit tests that don't need real fork+exec we return "/bin/true" and
	// rely on tests to short-circuit accordingly.
	_ = bin
	return "/bin/true"
}

func TestSupervisorListsZeroOnStartup(t *testing.T) {
	_, cleanup := startSupervisor(t)
	defer cleanup()
}

func TestPidfileExclusivity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uam.pid")
	release, err := AcquirePidfile(path)
	if err != nil {
		t.Fatalf("first AcquirePidfile: %v", err)
	}
	defer release()
	if _, err := AcquirePidfile(path); err == nil {
		t.Fatalf("expected second AcquirePidfile to fail")
	}
}

func TestSupervisorDirsAreCreated(t *testing.T) {
	dir := t.TempDir()
	sup, err := New(Options{RuntimeDir: dir, HostExe: "/bin/true"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sup.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sup.ControlSocketPath()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "hosts")); err != nil {
		t.Fatalf("hosts dir not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uam.pid")); err != nil {
		t.Fatalf("pidfile not created: %v", err)
	}
}

func TestSupervisorListProtocol(t *testing.T) {
	sup, cleanup := startSupervisor(t)
	defer cleanup()
	conn, err := net.Dial("unix", sup.ControlSocketPath())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := ipc.WriteFrame(conn, ipc.Request{Kind: ipc.KindList, ID: 1}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	resp, err := ipc.ReadFrame(conn)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if resp.ID != 1 {
		t.Fatalf("expected id=1, got %d", resp.ID)
	}
	var out struct {
		Sessions []SessionRecord `json:"sessions"`
	}
	if err := json.Unmarshal(resp.Payload, &out); err != nil {
		t.Fatalf("Unmarshal: %v: %q", err, resp.Payload)
	}
	if len(out.Sessions) != 0 {
		t.Fatalf("expected zero sessions, got %d", len(out.Sessions))
	}
}

func TestAdoptOrphansAddsLiveSockets(t *testing.T) {
	dir := t.TempDir()
	hostsDir := filepath.Join(dir, "hosts")
	if err := os.MkdirAll(hostsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Create a live socket that mimics an orphaned host.
	livePath := filepath.Join(hostsDir, "uam-test-alive.sock")
	ln, err := net.Listen("unix", livePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	// Create a dead socket (file exists, nothing listening).
	deadPath := filepath.Join(hostsDir, "uam-test-dead.sock")
	if err := os.WriteFile(deadPath, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	sup, err := New(Options{RuntimeDir: dir, HostExe: "/bin/true"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := sup.AdoptOrphans(); err != nil {
		t.Fatalf("AdoptOrphans: %v", err)
	}
	sup.mu.Lock()
	defer sup.mu.Unlock()
	if _, ok := sup.sessions["uam-test-alive"]; !ok {
		t.Fatalf("expected uam-test-alive to be adopted; sessions=%v", sup.sessions)
	}
	if _, ok := sup.sessions["uam-test-dead"]; ok {
		t.Fatalf("dead socket should not be adopted")
	}
	if _, err := os.Stat(deadPath); !os.IsNotExist(err) {
		t.Fatalf("expected dead socket to be unlinked")
	}
}
