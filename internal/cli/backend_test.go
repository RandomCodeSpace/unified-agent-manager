package cli

import (
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// TestNewServiceFallsBackToTmuxWhenSupervisorMissing exercises the native
// branch: when UAM_BACKEND=native and the supervisor is not reachable,
// NewService prints a warning to stderr and returns the tmux-backed
// service. The test asserts the function returns a non-nil service
// (no panic; legacy registry initializes).
func TestNewServiceFallsBackToTmuxWhenSupervisorMissing(t *testing.T) {
	// Point UAM_SOCKET at a path that cannot be a valid Unix socket so
	// ipcclient.New(autostart=true) fails fast. Use a directory: dialing
	// it produces ENOTSOCK regardless of autostart attempts.
	t.Setenv("UAM_BACKEND", "native")
	t.Setenv("UAM_SOCKET", t.TempDir())
	// Avoid the autostart fork+exec invoking us recursively in the test:
	// override the runtime dir so the pid/sock paths sit in a tempdir,
	// and rely on the autostart's 5-second timeout. The fork+exec'd
	// child will run `daemon start` against the `go test` binary which
	// does not handle the subcommand — its stderr is suppressed.
	//
	// This is a slow test; mark it as not Short.
	if testing.Short() {
		t.Skip("native fallback test skipped in -short mode")
	}
	t.Setenv("UAM_RUNTIME_DIR", t.TempDir())

	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st)
	if svc == nil {
		t.Fatalf("NewService returned nil")
	}
	if svc.Registry == nil {
		t.Fatalf("NewService.Registry is nil")
	}
}

func TestNewServiceUsesTmuxByDefault(t *testing.T) {
	t.Setenv("UAM_BACKEND", "")
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st)
	if svc == nil || svc.Registry == nil {
		t.Fatalf("default service not initialized")
	}
}
