package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// captureStderr swaps os.Stderr for a pipe for the duration of fn and
// returns whatever fn wrote to stderr. Used to differentiate the native
// fallback branch (prints a warning) from the tmux opt-out branch
// (silent) when both produce structurally identical *app.Service values.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	doneCh := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		doneCh <- buf.Bytes()
	}()

	fn()

	_ = w.Close()
	out := <-doneCh
	os.Stderr = orig
	return string(out)
}

// invokeNewService configures the requested backend env plus isolated
// socket/runtime paths and returns whatever NewService wrote to stderr.
// The service itself is also validated as non-nil; any failure there
// fails the test directly.
func invokeNewService(t *testing.T, backend string) string {
	t.Helper()
	t.Setenv("UAM_BACKEND", backend)
	// Isolate socket and runtime locations so a developer's running
	// supervisor cannot influence the test outcome.
	t.Setenv("UAM_SOCKET", t.TempDir())
	t.Setenv("UAM_RUNTIME_DIR", t.TempDir())

	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	stderr := captureStderr(t, func() {
		svc := NewService(st)
		if svc == nil || svc.Registry == nil {
			t.Fatalf("NewService returned nil or empty registry")
		}
	})
	return stderr
}

// TestNewServiceDefaultsToNative confirms that with UAM_BACKEND unset
// (the empty-string case from t.Setenv), NewService enters the native
// branch. Because no supervisor is reachable in the test environment
// the branch falls back to tmux after printing a warning to stderr;
// presence of that warning is the observable signal that the native
// branch executed.
func TestNewServiceDefaultsToNative(t *testing.T) {
	stderr := invokeNewService(t, "")
	if !strings.Contains(stderr, "native backend unavailable") {
		t.Fatalf("expected native fallback warning, got stderr=%q", stderr)
	}
}

// TestNewServiceNativeExplicit covers UAM_BACKEND=native. Behavior must
// match the unset case: native branch is selected, fallback warning is
// emitted when the supervisor cannot be reached.
func TestNewServiceNativeExplicit(t *testing.T) {
	stderr := invokeNewService(t, "native")
	if !strings.Contains(stderr, "native backend unavailable") {
		t.Fatalf("expected native fallback warning, got stderr=%q", stderr)
	}
}

// TestNewServiceTmuxOptOut covers UAM_BACKEND=tmux. The tmux branch must
// not attempt to dial the supervisor and therefore must not emit the
// native-fallback warning.
func TestNewServiceTmuxOptOut(t *testing.T) {
	stderr := invokeNewService(t, "tmux")
	if strings.Contains(stderr, "native backend unavailable") {
		t.Fatalf("tmux opt-out should not probe native backend, got stderr=%q", stderr)
	}
}
