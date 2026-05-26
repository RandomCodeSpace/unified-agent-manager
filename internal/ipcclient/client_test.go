package ipcclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/supervisor"
)

// Compile-time interface check.
var _ mux.Backend = (*Client)(nil)

// startSupervisor runs an isolated supervisor for the duration of a test
// and returns the socket path it listens on.
func startSupervisor(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sup, err := supervisor.New(supervisor.Options{
		RuntimeDir: dir,
		HostExe:    "/bin/true", // we don't actually spawn hosts in client tests
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("supervisor.Run did not exit")
		}
	})
	// Wait for control socket.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sup.ControlSocketPath()); err == nil {
			return sup.ControlSocketPath()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("control socket %q never appeared", sup.ControlSocketPath())
	return ""
}

func TestClientHasReturnsFalseForUnknown(t *testing.T) {
	sock := startSupervisor(t)
	c, err := New(Options{SocketPath: sock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()
	ok, err := c.Has(context.Background(), mux.SessionHandle("nope"))
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if ok {
		t.Fatalf("expected Has to return false for unknown session")
	}
}

func TestClientListEmpty(t *testing.T) {
	sock := startSupervisor(t)
	c, err := New(Options{SocketPath: sock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()
	infos, err := c.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("expected zero sessions, got %d", len(infos))
	}
}

func TestClientAutostartFailsWhenSocketMissing(t *testing.T) {
	// No supervisor running; autostart=false should produce an error.
	dir := t.TempDir()
	bogus := filepath.Join(dir, "missing.sock")
	if _, err := New(Options{SocketPath: bogus, AutoStart: false}); err == nil {
		t.Fatalf("expected dial error when no supervisor and autostart=false")
	}
}

func TestDefaultSocketPathRespectsEnv(t *testing.T) {
	t.Setenv("UAM_SOCKET", "/tmp/from-env.sock")
	if got := DefaultSocketPath(); got != "/tmp/from-env.sock" {
		t.Fatalf("expected UAM_SOCKET to win, got %q", got)
	}
}

func TestDefaultSocketPathRespectsXDG(t *testing.T) {
	t.Setenv("UAM_SOCKET", "")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	want := "/run/user/1000/uam/control.sock"
	if got := DefaultSocketPath(); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
