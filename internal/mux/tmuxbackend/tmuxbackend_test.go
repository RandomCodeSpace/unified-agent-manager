package tmuxbackend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

// Compile-time assertion that *Backend satisfies mux.Backend.
var _ mux.Backend = (*Backend)(nil)

func TestNewWrapsClient(t *testing.T) {
	c := tmux.New("uam-test")
	b := New(c)
	if b == nil {
		t.Fatal("New returned nil")
	}
}

func TestNewNilClientUsesDefault(t *testing.T) {
	b := New(nil)
	if b == nil {
		t.Fatal("New(nil) should construct a Backend with a default client")
	}
}

func TestSpawnRoutesToCreateSession(t *testing.T) {
	// Integration-like: requires the real tmux client to round-trip.
	// Guard with the integration tag and skip otherwise.
	t.Skip("covered by integration test in backend_agent_integration_test.go")
}

func TestPaneCaptureLinesFromTmuxOutput(t *testing.T) {
	// Capture conversion: tmux returns a single string; we split on '\n'
	// into Lines while preserving order.
	raw := "line1\nline2\nline3"
	pc := mux.PaneCapture{Lines: splitCapture(raw), CapturedAt: time.Now()}
	if len(pc.Lines) != 3 || pc.Lines[0] != "line1" || pc.Lines[2] != "line3" {
		t.Fatalf("splitCapture failed: %+v", pc.Lines)
	}
}

func TestEnvSliceToMap(t *testing.T) {
	in := []string{"K1=v1", "K2=v2", "bogus", "=novalue"}
	out := envSliceToMap(in)
	if out["K1"] != "v1" || out["K2"] != "v2" {
		t.Fatalf("envSliceToMap basic: %+v", out)
	}
	if _, ok := out["bogus"]; ok {
		t.Fatalf("entries without '=' must be skipped")
	}
	if _, ok := out[""]; ok {
		t.Fatalf("leading-= entries must be skipped")
	}
	if envSliceToMap(nil) != nil {
		t.Fatalf("nil input should return nil map")
	}
}

func TestSplitCaptureEmpty(t *testing.T) {
	if splitCapture("") != nil {
		t.Fatalf("empty string should yield nil slice")
	}
	if got := splitCapture("only\n"); len(got) != 1 || got[0] != "only" {
		t.Fatalf("trailing newline must be trimmed: %+v", got)
	}
}

// fakeTmuxBackend builds a tmuxbackend.Backend pointed at a controllable
// /bin/sh fake of the tmux binary. The fake appends each invocation to
// $TMUX_LOG so callers can assert exactly which subcommands ran, and it can
// be told to emit canned stdout for list-sessions and capture-pane.
func fakeTmuxBackend(t *testing.T) (*Backend, string) {
	t.Helper()
	dir := t.TempDir()
	tmuxPath := filepath.Join(dir, "tmux")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *"list-sessions"*) echo "uam-claude-abc12345|1710000000|0|42|/tmp/work|claude" ;;
  *"capture-pane"*) printf 'line one\nline two\n' ;;
  *"has-session"*) exit 0 ;;
esac
exit 0
`
	if err := os.WriteFile(tmuxPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	c := tmux.New("uam-test")
	c.Executable = tmuxPath
	return New(c), logPath
}

func TestBackendSpawnUsesCreateSession(t *testing.T) {
	b, logPath := fakeTmuxBackend(t)
	handle, err := b.Spawn(context.Background(), mux.SpawnSpec{
		SessionName: "uam-claude-abc12345",
		Argv:        []string{"claude", "--yolo"},
		Env:         []string{"UAM_AGENT=claude", "UAM_ID=abc12345"},
		Cwd:         "/tmp/work",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if handle != mux.SessionHandle("uam-claude-abc12345") {
		t.Fatalf("Spawn handle = %q", handle)
	}
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "new-session") || !strings.Contains(string(data), "uam-claude-abc12345") {
		t.Fatalf("tmux log missing new-session for spawn: %s", data)
	}
	if !strings.Contains(string(data), "UAM_AGENT=claude") {
		t.Fatalf("env vars not threaded through to tmux: %s", data)
	}
}

func TestBackendHasListCapture(t *testing.T) {
	b, _ := fakeTmuxBackend(t)
	ctx := context.Background()

	ok, err := b.Has(ctx, "uam-claude-abc12345")
	if err != nil || !ok {
		t.Fatalf("Has = %v %v", ok, err)
	}

	infos, err := b.List(ctx, "uam-claude-")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 || infos[0].PanePID != 42 || infos[0].Cwd != "/tmp/work" || infos[0].PaneCmd != "claude" {
		t.Fatalf("List infos = %+v", infos)
	}

	// Empty prefix means "no filter": still returns the same row.
	all, err := b.List(ctx, "")
	if err != nil || len(all) != 1 {
		t.Fatalf("List(empty prefix) = %d err=%v", len(all), err)
	}

	// Prefix that doesn't match must filter the row out.
	none, err := b.List(ctx, "uam-codex-")
	if err != nil || len(none) != 0 {
		t.Fatalf("List(non-matching prefix) = %d err=%v", len(none), err)
	}

	cap, err := b.Capture(ctx, "uam-claude-abc12345", 100)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(cap.Lines) != 2 || cap.Lines[0] != "line one" {
		t.Fatalf("Capture lines = %+v", cap.Lines)
	}
}

func TestBackendWriteVariants(t *testing.T) {
	b, logPath := fakeTmuxBackend(t)
	ctx := context.Background()
	handle := mux.SessionHandle("uam-claude-abc12345")

	// Empty payload is a silent no-op.
	if err := b.Write(ctx, handle, nil); err != nil {
		t.Fatalf("Write(nil): %v", err)
	}
	data, _ := os.ReadFile(logPath)
	if strings.Contains(string(data), "send-keys") {
		t.Fatalf("empty Write should produce no send-keys calls: %s", data)
	}

	// Literal write without a trailing carriage return.
	if err := b.Write(ctx, handle, []byte("hello")); err != nil {
		t.Fatalf("Write literal: %v", err)
	}
	data, _ = os.ReadFile(logPath)
	if !strings.Contains(string(data), "send-keys") || !strings.Contains(string(data), "hello") {
		t.Fatalf("literal Write did not send-keys: %s", data)
	}

	// Carriage-return-terminated write -> literal body + Enter.
	if err := b.Write(ctx, handle, []byte("question\r")); err != nil {
		t.Fatalf("Write CR-terminated: %v", err)
	}
	data, _ = os.ReadFile(logPath)
	if !strings.Contains(string(data), "Enter") {
		t.Fatalf("CR-terminated Write must send Enter: %s", data)
	}

	// Bare CR -> just Enter, no literal body sent.
	if err := b.Write(ctx, handle, []byte("\r")); err != nil {
		t.Fatalf("Write bare CR: %v", err)
	}
}

func TestBackendResizeKillAttachSubscribe(t *testing.T) {
	b, logPath := fakeTmuxBackend(t)
	ctx := context.Background()
	handle := mux.SessionHandle("uam-claude-abc12345")

	if err := b.Resize(ctx, handle, 80, 24); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	if err := b.Kill(ctx, handle); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "kill-session") {
		t.Fatalf("Kill did not invoke kill-session: %s", data)
	}

	if _, err := b.Attach(ctx, handle); err == nil {
		t.Fatal("Attach must report unsupported for the tmux backend")
	}

	ch, err := b.Subscribe(ctx, handle)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if ch != nil {
		t.Fatalf("Subscribe should return a nil channel, got %v", ch)
	}
}

func TestBackendCaptureTime(t *testing.T) {
	b, _ := fakeTmuxBackend(t)
	cap, err := b.Capture(context.Background(), "uam-claude-abc12345", 0)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if cap.CapturedAt.IsZero() || time.Since(cap.CapturedAt) > time.Minute {
		t.Fatalf("CapturedAt should be near-now: %v", cap.CapturedAt)
	}
}
