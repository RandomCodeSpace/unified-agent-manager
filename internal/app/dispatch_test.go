package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestEnterIgnoresRemovedComposerAndAttaches(t *testing.T) {
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
	m.input = "@fake new task"

	_, cmd := m.handleActionKey("enter")
	if cmd == nil {
		t.Fatal("expected an attach command")
	}
	msg := cmd()
	if _, ok := msg.(attachSpecMsg); !ok {
		t.Fatalf("expected attachSpecMsg, got %T", msg)
	}
}
