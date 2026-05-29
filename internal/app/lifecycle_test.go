package app

import (
	"context"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// F20 Stage 1 — a refresh must bump LastSeenAt for every live (pane-alive)
// session so the staleness-based PruneOld never deletes a session that is still
// running. Without this, an old CreatedAt + never-updated LastSeenAt makes a
// long-running live session look prunable.
func TestLastSeenAtBumpedForLiveSessions(t *testing.T) {
	live := adapter.Session{ID: "aaaa1111", AgentType: "fake", DisplayName: "A", Cwd: "/tmp", TmuxSession: "uam-fake-aaaa1111", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now().Add(-30 * 24 * time.Hour)}
	svc, st, _ := newLoadService(t, []adapter.Session{live})

	// Seed a stale record for the same live session: LastSeenAt far in the past.
	key := store.Key("fake", "aaaa1111")
	stale := time.Now().Add(-30 * 24 * time.Hour)
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[key] = store.SessionRecord{ID: "aaaa1111", Agent: "fake", Name: "A", TmuxSession: "uam-fake-aaaa1111", Status: store.StatusActive, LastSeenAt: stale}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	before := time.Now()
	if _, _, err := svc.LoadSessions(context.Background()); err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Sessions[key].LastSeenAt
	if !got.After(before.Add(-time.Second)) {
		t.Fatalf("LastSeenAt for live session not bumped: got %v, stale was %v", got, stale)
	}
}

// F20 Stage 2 — startup pruning must be server-down-safe: when no live sessions
// are visible (server down or empty, indistinguishable) it must NOT delete the
// persisted records, or a transient tmux-down would wipe the whole store.
func TestStartupPruneSkipsWhenServerDown(t *testing.T) {
	// No live sessions: the fake reports an empty set, which is what a down
	// server looks like to the service.
	svc, st, _ := newLoadService(t, nil)

	key := store.Key("fake", "bbbb2222")
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[key] = store.SessionRecord{ID: "bbbb2222", Agent: "fake", Name: "B", TmuxSession: "uam-fake-bbbb2222", Status: store.StatusActive, LastSeenAt: old}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.PruneStartup(context.Background()); err != nil {
		t.Fatalf("PruneStartup: %v", err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Sessions[key]; !ok {
		t.Fatalf("server-down startup prune deleted a record: %+v", cfg.Sessions)
	}
}

// F20 Stage 2 — when the server is up (at least one live session proves it),
// startup pruning removes a stale, dead-pane record but keeps the live one.
func TestStartupPruneRemovesStaleDeadRecordWhenServerUp(t *testing.T) {
	live := adapter.Session{ID: "cccc3333", AgentType: "fake", DisplayName: "C", Cwd: "/tmp", TmuxSession: "uam-fake-cccc3333", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{live})

	liveKey := store.Key("fake", "cccc3333")
	deadKey := store.Key("fake", "dddd4444")
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[liveKey] = store.SessionRecord{ID: "cccc3333", Agent: "fake", Name: "C", TmuxSession: "uam-fake-cccc3333", Status: store.StatusActive, LastSeenAt: time.Now()}
		cfg.Sessions[deadKey] = store.SessionRecord{ID: "dddd4444", Agent: "fake", Name: "D", TmuxSession: "uam-fake-dddd4444", Status: store.StatusActive, LastSeenAt: old}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.PruneStartup(context.Background()); err != nil {
		t.Fatalf("PruneStartup: %v", err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Sessions[liveKey]; !ok {
		t.Fatalf("startup prune removed the live session: %+v", cfg.Sessions)
	}
	if _, ok := cfg.Sessions[deadKey]; ok {
		t.Fatalf("startup prune kept a stale dead record: %+v", cfg.Sessions)
	}
}
