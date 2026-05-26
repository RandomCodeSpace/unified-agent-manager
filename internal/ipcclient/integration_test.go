package ipcclient

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/supervisor"
)

// TestEndToEndDispatchListPeekKill exercises the full native-backend
// pipeline:
//
//  1. Build the uam binary so the supervisor can fork+exec it as
//     `uam internal-host --config <json>`.
//  2. Start a supervisor against an isolated runtime dir.
//  3. Open an ipcclient.Client to it.
//  4. Spawn a session running /bin/cat.
//  5. Verify List shows it.
//  6. Write "smoke-test\n"; Capture; expect the line back.
//  7. Kill the session; verify List goes back to zero.
//
// This is the test the plan's Task 19 "manual smoke" verifies in code.
func TestEndToEndDispatchListPeekKill(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in -short")
	}
	uamExe := buildUam(t)
	runtimeDir := t.TempDir()

	sup, err := supervisor.New(supervisor.Options{
		RuntimeDir: runtimeDir,
		HostExe:    uamExe,
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}
	supCtx, supCancel := context.WithCancel(context.Background())
	supDone := make(chan error, 1)
	go func() { supDone <- sup.Run(supCtx) }()
	t.Cleanup(func() {
		supCancel()
		select {
		case <-supDone:
		case <-time.After(3 * time.Second):
			t.Errorf("supervisor.Run did not exit")
		}
	})

	// Wait for control socket.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sup.ControlSocketPath()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	c, err := New(Options{SocketPath: sup.ControlSocketPath()})
	if err != nil {
		t.Fatalf("ipcclient.New: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Spawn.
	handle, err := c.Spawn(ctx, mux.SpawnSpec{
		SessionName: "uam-smoke-cat",
		Argv:        []string{"/bin/cat"},
		Cwd:         "/tmp",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if handle != "uam-smoke-cat" {
		t.Fatalf("expected handle uam-smoke-cat, got %q", handle)
	}

	// List.
	infos, err := c.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 session, got %d: %+v", len(infos), infos)
	}

	// Write + Capture.
	if err := c.Write(ctx, handle, []byte("smoke-test\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Allow a brief moment for the PTY to echo into the journal.
	var cap mux.PaneCapture
	pollDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(pollDeadline) {
		cap, err = c.Capture(ctx, handle, 0)
		if err != nil {
			t.Fatalf("Capture: %v", err)
		}
		for _, l := range cap.Lines {
			if strings.Contains(l, "smoke-test") {
				goto found
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("smoke-test not seen within 3s; lines=%v", cap.Lines)

found:
	// Kill.
	if err := c.Kill(ctx, handle); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	// Has should be false after Kill.
	has, err := c.Has(ctx, handle)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if has {
		t.Fatalf("expected Has false after Kill")
	}
}

// buildUam builds the project's uam binary into the test's temp dir and
// returns the absolute path. Caches via the test binary name.
func buildUam(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "uam")
	// Resolve the module root by walking up until we find go.mod.
	root := moduleRoot(t)
	// #nosec G204 -- go is on PATH; cmd is hardcoded
	cmd := exec.Command("go", "build", "-o", exe, "./cmd/uam")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return exe
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find module root")
		}
		dir = parent
	}
}
