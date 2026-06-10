package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func replyTestModel(t *testing.T) (Model, *svcFakeAdapter) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := []adapter.Session{{ID: "abc12345", AgentType: "fake", DisplayName: "live", Cwd: "/tmp", SessionName: "uam-fake-abc12345", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}}
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: sessions}
	m := NewWithDeps(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.sessions = append([]adapter.Session(nil), sessions...)
	m.defaultAgent = "fake"
	return m, fake
}

// F36 — when the peek panel is open, typing text and pressing Enter must send a
// reply to the peeked session via Service.Reply, not dispatch a new agent. The
// check must come before the dispatch/attach branch.
func TestReplyFromPeekSendsToService(t *testing.T) {
	m, fake := replyTestModel(t)
	m.peekOpen = true
	m.peekTargetID = "abc12345"
	m.input = "please continue"

	handled, cmd := m.handleActionKey("enter")
	if !handled {
		t.Fatal("enter should be handled while peek is open")
	}
	if cmd == nil {
		t.Fatal("expected a reply command")
	}
	cmd() // run the reply against the service
	if fake.replied != "please continue" {
		t.Fatalf("reply text = %q, want %q", fake.replied, "please continue")
	}
}

// F36 — sending a reply clears the input so the composer is ready for the next
// line, and the peek panel stays open (re-peeked) to show the agent's response.
func TestReplyFromPeekClearsInputAndKeepsPeekOpen(t *testing.T) {
	m, _ := replyTestModel(t)
	m.peekOpen = true
	m.peekTargetID = "abc12345"
	m.input = "hi"

	_, cmd := m.handleActionKey("enter")
	if cmd == nil {
		t.Fatal("expected a reply command")
	}
	cmd()
	// handleActionKey returns a *copy* via value receiver semantics on Model; the
	// model mutated in-place because handleActionKey has a pointer receiver.
	if m.input != "" {
		t.Fatalf("reply should clear the input, got %q", m.input)
	}
	if !m.peekOpen {
		t.Fatal("peek panel should stay open after sending a reply")
	}
}

// F36 — Esc while peek is open with a half-typed reply closes the panel WITHOUT
// sending. No reply must reach the service.
func TestReplyEscCancelsWithoutSending(t *testing.T) {
	m, fake := replyTestModel(t)
	m.peekOpen = true
	m.peekTargetID = "abc12345"
	m.input = "unsent draft"

	handled, _ := m.handleActionKey("esc")
	if !handled {
		t.Fatal("esc should be handled")
	}
	if m.peekOpen {
		t.Fatal("esc should close the peek panel")
	}
	if fake.replied != "" {
		t.Fatalf("esc must not send a reply, got %q", fake.replied)
	}
}

// F36 — pressing Enter with an empty input while peek is open must not send an
// empty reply; it falls through to the normal Enter behavior.
func TestReplyEmptyInputDoesNotSend(t *testing.T) {
	m, fake := replyTestModel(t)
	m.peekOpen = true
	m.peekTargetID = "abc12345"
	m.input = ""

	_, cmd := m.handleActionKey("enter")
	if cmd != nil {
		// An attach command may be returned; just make sure nothing replied.
		cmd()
	}
	if fake.replied != "" {
		t.Fatalf("empty input must not send a reply, got %q", fake.replied)
	}
}

// F36 — when the peek panel is open the prompt line presents itself as a reply
// composer so the sub-mode is discoverable, not a silent overload of the
// dispatch line.
func TestPeekPromptShowsReplyComposer(t *testing.T) {
	m, _ := replyTestModel(t)
	m.width = 90
	m.peekOpen = true
	m.peekTargetID = "abc12345"

	out := m.renderPrompt()
	if !strings.Contains(out, "reply") {
		t.Fatalf("peek-open prompt should label itself a reply composer: %q", out)
	}
}

// F36 — with the peek panel CLOSED, typed-input + Enter must still dispatch a new
// session, not reply (the existing behavior must be preserved).
func TestEnterWithoutPeekStillDispatches(t *testing.T) {
	m, fake := replyTestModel(t)
	m.peekOpen = false
	m.input = "@fake new task"

	_, cmd := m.handleActionKey("enter")
	if cmd == nil {
		t.Fatal("expected a dispatch command")
	}
	msg := cmd()
	if _, ok := msg.(dispatchedMsg); !ok {
		t.Fatalf("expected dispatchedMsg, got %T", msg)
	}
	if fake.replied != "" {
		t.Fatalf("dispatch path must not reply, got %q", fake.replied)
	}
}
