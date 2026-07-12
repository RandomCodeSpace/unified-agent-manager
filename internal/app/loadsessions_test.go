package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// newLoadService builds a Service backed by a tempdir store and a single fake
// adapter whose live session set is taken from sessions.
func newLoadService(t *testing.T, sessions []adapter.Session) (*Service, *store.Store, *svcFakeAdapter) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: sessions}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	return svc, st, fake
}

// F01 — a refresh that backfills a record must persist via an atomic re-read,
// never a whole-config Save that clobbers a concurrent TogglePin on a *different*
// session. Deterministic: prime a pinned record B, then run a refresh that owns
// only key A. After the fix the refresh re-reads inside the lock and writes only
// A, so B's pin survives.
func TestLoadSessionsRefreshDoesNotClobberConcurrentPin(t *testing.T) {
	// Live session A has no store record -> refresh must backfill it (a write).
	liveA := adapter.Session{ID: "aaaa1111", AgentType: "fake", DisplayName: "A", Cwd: "/tmp", SessionName: "uam-fake-aaaa1111", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{liveA})

	// Record B belongs to a different session and is already persisted, unpinned.
	keyB := store.Key("fake", "bbbb2222")
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[keyB] = store.SessionRecord{ID: "bbbb2222", Agent: "fake", Name: "B", SessionName: "uam-fake-bbbb2222"}
		return nil
	}); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	// Simulate the lost-update window: an in-flight refresh has already loaded a
	// stale cfg (B unpinned). Concurrently the user pins B. The refresh must not
	// clobber that pin when it persists its own backfill of A.
	cfgStale, err := st.Load()
	if err != nil {
		t.Fatalf("Load stale: %v", err)
	}
	if err := svc.TogglePin(context.Background(), "bbbb2222"); err != nil {
		t.Fatalf("TogglePin B: %v", err)
	}

	// Drive the refresh persistence with the stale snapshot. With the buggy
	// whole-config Save this writes B unpinned and clobbers the pin; with the
	// atomic re-read it touches only A.
	live := map[string]adapter.Session{store.Key("fake", liveA.ID): liveA}
	svc.mergeStoredSessions(live, cfgStale, time.Now())
	updates := svc.refreshSessionRecords(context.Background(), live, &cfgStale)
	svc.persistRefresh(updates)

	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load final: %v", err)
	}
	if !cfg.Sessions[keyB].Pinned {
		t.Fatalf("concurrent pin on B was clobbered by the refresh save: %+v", cfg.Sessions[keyB])
	}
	if _, ok := cfg.Sessions[store.Key("fake", "aaaa1111")]; !ok {
		t.Fatalf("refresh did not persist the backfilled record A: %+v", cfg.Sessions)
	}
}

func TestRefreshDoesNotClobberConcurrentMutationOnSameSession(t *testing.T) {
	now := time.Now()
	live := adapter.Session{ID: "aaaa1111", AgentType: "fake", DisplayName: "before", Cwd: "/tmp", SessionName: "uam-fake-aaaa1111", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: now}
	svc, st, _ := newLoadService(t, []adapter.Session{live})
	key := store.Key("fake", live.ID)
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[key] = store.SessionRecord{ID: live.ID, Agent: "fake", Name: "before", Workdir: "/tmp", SessionName: live.SessionName, Status: store.StatusActive, LastSeenAt: now.Add(-2 * lastSeenRefresh)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	stale, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	liveMap := map[string]adapter.Session{key: live}
	svc.mergeStoredSessions(liveMap, stale, now)
	updates := svc.refreshSessionRecords(context.Background(), liveMap, &stale)
	if err := svc.Rename(context.Background(), live.ID, "after"); err != nil {
		t.Fatal(err)
	}
	if err := svc.TogglePin(context.Background(), live.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.persistRefresh(updates); err != nil {
		t.Fatal(err)
	}

	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := cfg.Sessions[key]
	if rec.Name != "after" || !rec.Pinned {
		t.Fatalf("refresh clobbered same-session mutation: %+v", rec)
	}
	if rec.LastSeenAt.Before(now) {
		t.Fatalf("refresh-owned field was not persisted: %+v", rec)
	}
}

func TestPersistRefreshReturnsReadOnlyStoreError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	data := []byte(`{"schema_version":999,"default_agent":"opencode","sessions":{},"ui":{}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, nil)
	status := store.StatusActive
	err = svc.persistRefresh(map[string]refreshPatch{
		"fake:aaaa1111": {status: &status},
	})
	if !errors.Is(err, store.ErrReadOnly) {
		t.Fatalf("persistRefresh error = %v, want ErrReadOnly", err)
	}
}

// F01 (-race) — concurrent LoadSessions refreshes and TogglePin calls must not
// race and must not lose the final pin state.
func TestLoadSessionsConcurrentWithTogglePin_Race(t *testing.T) {
	liveA := adapter.Session{ID: "aaaa1111", AgentType: "fake", DisplayName: "A", Cwd: "/tmp", SessionName: "uam-fake-aaaa1111", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{liveA})

	keyB := store.Key("fake", "bbbb2222")
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[keyB] = store.SessionRecord{ID: "bbbb2222", Agent: "fake", Name: "B", SessionName: "uam-fake-bbbb2222", Pinned: true}
		return nil
	}); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := svc.LoadSessions(context.Background()); err != nil {
				t.Errorf("LoadSessions: %v", err)
			}
		}()
	}
	wg.Wait()

	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// B was never toggled by the refresh path; its pin must survive unchanged.
	if !cfg.Sessions[keyB].Pinned {
		t.Fatalf("B lost its pin under concurrent refresh: %+v", cfg.Sessions[keyB])
	}
}

// C1-1 — a live session lacking a store record must be backfilled and persisted.
func TestRefreshBackfillsOrphanRecord(t *testing.T) {
	live := adapter.Session{ID: "cccc3333", AgentType: "fake", DisplayName: "orphan", Cwd: "/tmp", SessionName: "uam-fake-cccc3333", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{live})

	if _, _, err := svc.LoadSessions(context.Background()); err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec, ok := cfg.Sessions[store.Key("fake", "cccc3333")]
	if !ok {
		t.Fatalf("orphan live session was not backfilled into the store: %+v", cfg.Sessions)
	}
	if rec.ID != "cccc3333" || rec.SessionName != "uam-fake-cccc3333" {
		t.Fatalf("backfilled record is malformed: %+v", rec)
	}
}

// C1-1 — once a record exists, repeated refreshes with no real change must not
// rewrite the store (no write-storm of no-op saves).
func TestRefreshIsIdempotentNoRedundantSave(t *testing.T) {
	live := adapter.Session{ID: "dddd4444", AgentType: "fake", DisplayName: "d", Cwd: "/tmp", SessionName: "uam-fake-dddd4444", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{live})

	// First load backfills.
	if _, _, err := svc.LoadSessions(context.Background()); err != nil {
		t.Fatalf("LoadSessions 1: %v", err)
	}

	// Subsequent loads must report no owned changes -> empty update set.
	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	liveMap := map[string]adapter.Session{store.Key("fake", live.ID): live}
	svc.mergeStoredSessions(liveMap, cfg, time.Now())
	updates := svc.refreshSessionRecords(context.Background(), liveMap, &cfg)
	if len(updates) != 0 {
		t.Fatalf("idempotent refresh produced %d updates, want 0: %+v", len(updates), updates)
	}
}

// C1-1 fix-trap — a live session with an empty ID must never be persisted as an
// un-killable phantom record.
func TestRefreshDoesNotPersistEmptyIDRecord(t *testing.T) {
	live := adapter.Session{ID: "", AgentType: "fake", DisplayName: "phantom", Cwd: "/tmp", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{live})

	if _, _, err := svc.LoadSessions(context.Background()); err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for key, rec := range cfg.Sessions {
		if rec.ID == "" {
			t.Fatalf("empty-ID record persisted under key %q: %+v", key, rec)
		}
	}
}

// F18 — a session the user closed but whose pane is still alive must reconcile
// to Active (it renders under ACTIVE, not CLOSED, because Closed => Exited).
func TestLoadSessionsReconcilesLiveUserClosedSessionToActive(t *testing.T) {
	live := adapter.Session{ID: "eeee5555", AgentType: "fake", DisplayName: "e", Cwd: "/tmp", SessionName: "uam-fake-eeee5555", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{live})

	// Persist a record flagged closed-by-user even though the pane is alive.
	key := store.Key("fake", "eeee5555")
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[key] = store.SessionRecord{ID: "eeee5555", Agent: "fake", Name: "e", SessionName: "uam-fake-eeee5555", Status: store.StatusClosedByUser}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sessions, _, err := svc.LoadSessions(context.Background())
	if err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	var found bool
	for _, s := range sessions {
		if s.ID == "eeee5555" {
			found = true
			if s.Closed {
				t.Fatalf("live user-closed session must not render Closed (Closed=>Exited): %+v", s)
			}
		}
	}
	if !found {
		t.Fatalf("session not present: %+v", sessions)
	}
}

// F18 anti-flap — the persisted Status of a closed record whose pane survived
// the close must be reset to Active so it does not flap back to Closed.
func TestLoadSessionsResetsPersistedStatusWhenLivePaneSurvivesClose(t *testing.T) {
	live := adapter.Session{ID: "ffff6666", AgentType: "fake", DisplayName: "f", Cwd: "/tmp", SessionName: "uam-fake-ffff6666", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{live})

	key := store.Key("fake", "ffff6666")
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[key] = store.SessionRecord{ID: "ffff6666", Agent: "fake", Name: "f", SessionName: "uam-fake-ffff6666", Status: store.StatusClosedByUser}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, _, err := svc.LoadSessions(context.Background()); err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Sessions[key].Status; got != store.StatusActive {
		t.Fatalf("persisted status = %q, want %q (anti-flap reset)", got, store.StatusActive)
	}
}
