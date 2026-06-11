package claude

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
)

func TestNew(t *testing.T) {
	a := New(nil)
	if a == nil || a.Name() != "claude" || a.DisplayName() == "" {
		t.Fatalf("bad adapter: %#v", a)
	}
}

// TestYoloArgs locks in claude's full-access flag exactly. A drift here
// silently changes whether dispatched sessions run with permissions skipped.
func TestYoloArgs(t *testing.T) {
	ta, ok := New(nil).(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent")
	}
	if got, want := ta.YoloArgs, []string{"--dangerously-skip-permissions"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("YoloArgs = %v, want %v", got, want)
	}
}

// TestNewWiresSessionArgs asserts New installs the SessionArgs hook and
// SkipPromptOnResume. Without this wiring, picking "Resume" on a claude row
// would relaunch a fresh agent (no --continue) AND re-fire the original prompt.
func TestNewWiresSessionArgs(t *testing.T) {
	ta, ok := New(nil).(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent")
	}
	if ta.SessionArgs == nil {
		t.Fatal("expected SessionArgs to be wired")
	}
	if !ta.SkipPromptOnResume {
		t.Fatal("expected SkipPromptOnResume to be true")
	}
}

// TestResumeAppendsContinueAndDoesNotReplayPrompt: resuming an Exited claude
// row must use claude's --continue (resume last session) and must NOT replay
// the original prompt into the restored session, nor pass the uam UUID.
func TestResumeAppendsContinueAndDoesNotReplayPrompt(t *testing.T) {
	a, be := newTestClaudeAdapter(t)
	resumable, ok := a.(adapter.ResumableAdapter)
	if !ok {
		t.Fatal("claude adapter should be resumable")
	}
	_, err := resumable.Resume(context.Background(), adapter.ResumeRequest{ID: "abc12345-dead-beef-cafe-0123456789ab", Prompt: "fix parser", Cwd: "/tmp", Mode: "yolo", SessionName: "uam-claude-abc12345"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	argv := be.CommandLog()
	if !strings.Contains(argv, "claude --dangerously-skip-permissions --continue") {
		t.Fatalf("claude resume should append --continue: %s", argv)
	}
	// The uam UUID may appear in the UAM_ID env var, but must never be passed
	// as a flag argument to claude (no --resume <uuid> / --continue <uuid>).
	if strings.Contains(argv, "--continue abc12345-dead-beef-cafe-0123456789ab") ||
		strings.Contains(argv, "--resume") {
		t.Fatalf("claude resume must not pass the uam UUID as a flag arg: %s", argv)
	}
	if sends := be.CallsOf("send"); len(sends) != 0 {
		t.Fatalf("resume should not replay the original prompt: %+v", sends)
	}
}

// TestDispatchUnchanged_sendsPromptNoContinue: dispatch keeps its byte-identical
// argv (no --continue) and still sends the prompt.
func TestDispatchUnchanged_sendsPromptNoContinue(t *testing.T) {
	a, be := newTestClaudeAdapter(t)
	_, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Prompt: "fix parser", Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if argv := be.CommandLog(); strings.Contains(argv, "--continue") {
		t.Fatalf("dispatch must not append --continue: %s", argv)
	}
	sends := be.CallsOf("send")
	if len(sends) != 1 || sends[0].Text != "fix parser" {
		t.Fatalf("dispatch should send the prompt: %+v", sends)
	}
}

func newTestClaudeAdapter(t *testing.T) (adapter.AgentAdapter, *adaptertest.Backend) {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "claude"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	be := &adaptertest.Backend{}
	return New(be), be
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// newSeedingClaudeAdapter installs a fake claude whose --help advertises
// --session-id, enabling the exact-session seeding path.
func newSeedingClaudeAdapter(t *testing.T) (adapter.AgentAdapter, *adaptertest.Backend) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"--help\" ]; then echo '  --session-id <uuid>  Use a specific session ID'; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	be := &adaptertest.Backend{}
	return New(be), be
}

// Dispatch must seed claude's session id with the uam UUID when the installed
// claude supports --session-id, and record it as the provider session id so a
// later resume can target the exact conversation.
func TestDispatchSeedsSessionIDWhenSupported(t *testing.T) {
	a, be := newSeedingClaudeAdapter(t)
	sess, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	argv := be.CommandLog()
	if !strings.Contains(argv, "--session-id "+sess.ID) {
		t.Fatalf("dispatch should seed --session-id with the uam id: %s", argv)
	}
	if sess.ProviderSessionID != sess.ID {
		t.Fatalf("ProviderSessionID = %q, want the seeded uam id %q", sess.ProviderSessionID, sess.ID)
	}
}

// An older claude whose --help does not advertise --session-id must get the
// bare argv (no unknown flag that would kill the agent at startup).
func TestDispatchSkipsSessionIDWhenUnsupported(t *testing.T) {
	a, be := newTestClaudeAdapter(t)
	sess, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if argv := be.CommandLog(); strings.Contains(argv, "--session-id") {
		t.Fatalf("unsupported claude must not receive --session-id: %s", argv)
	}
	if sess.ProviderSessionID != "" {
		t.Fatalf("ProviderSessionID = %q, want empty without seeding", sess.ProviderSessionID)
	}
}

// A record carrying a seeded provider session id must resume that EXACT
// session (--resume <id>), not the cwd's most recent conversation
// (--continue) — two uam sessions in one directory must not collapse into the
// same claude conversation on resume.
func TestResumeTargetsExactSeededSession(t *testing.T) {
	a, be := newSeedingClaudeAdapter(t)
	resumable := a.(adapter.ResumableAdapter)
	_, err := resumable.Resume(context.Background(), adapter.ResumeRequest{
		ID: "abc12345-dead-beef-cafe-0123456789ab", Cwd: "/tmp", Mode: "yolo",
		SessionName: "uam-claude-abc12345", ProviderSessionID: "abc12345-dead-beef-cafe-0123456789ab",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	argv := be.CommandLog()
	if !strings.Contains(argv, "--resume abc12345-dead-beef-cafe-0123456789ab") {
		t.Fatalf("resume should target the seeded session id: %s", argv)
	}
	if strings.Contains(argv, "--continue") {
		t.Fatalf("exact resume must not fall back to --continue: %s", argv)
	}
}
