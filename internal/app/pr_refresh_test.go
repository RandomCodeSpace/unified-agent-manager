package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/pr"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestLoadSessionsNeverRunsPRChecker(t *testing.T) {
	live := adapter.Session{ID: "aaaa1111", AgentType: "fake", DisplayName: "A", Cwd: "/tmp", SessionName: "uam-fake-aaaa1111", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now(), PR: &adapter.PRRef{URL: "https://github.com/o/r/pull/1", Number: 1, Status: adapter.PROpen}}
	svc, st, _ := newLoadService(t, []adapter.Session{live})
	var calls atomic.Int32
	svc.checkPR = func(context.Context, string) (pr.Status, error) {
		calls.Add(1)
		return pr.Open, nil
	}
	if _, _, err := svc.LoadSessions(context.Background()); err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("LoadSessions invoked PR checker %d times", calls.Load())
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec, ok := cfg.Sessions[store.Key("fake", "aaaa1111")]
	if !ok || rec.PR == nil {
		t.Fatalf("PR record not persisted: %+v", cfg.Sessions)
	}
	if !rec.PR.LastChecked.IsZero() {
		t.Fatalf("discovery should not claim a network check: %v", rec.PR.LastChecked)
	}
}

func TestRefreshPRStatusesUsesBoundedConcurrencyAndPersistsResults(t *testing.T) {
	sessions := make([]adapter.Session, 10)
	for i := range sessions {
		id := fmt.Sprintf("%08x", i+1)
		sessions[i] = adapter.Session{ID: id, AgentType: "fake", DisplayName: id, Cwd: "/tmp", SessionName: "uam-fake-" + id, State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now(), PR: &adapter.PRRef{URL: fmt.Sprintf("https://github.com/o/r/pull/%d", i+1), Number: i + 1, Status: adapter.PROpen}}
	}
	svc, st, _ := newLoadService(t, sessions)
	var active atomic.Int32
	var maximum atomic.Int32
	svc.checkPR = func(ctx context.Context, _ string) (pr.Status, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return pr.None, ctx.Err()
		case <-time.After(20 * time.Millisecond):
			return pr.Merged, nil
		}
	}
	if err := svc.RefreshPRStatuses(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := maximum.Load(); got < 2 || got > prRefreshWorkers {
		t.Fatalf("max concurrency = %d, want 2..%d", got, prRefreshWorkers)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	for key, rec := range cfg.Sessions {
		if rec.PR == nil || rec.PR.LastStatus != string(adapter.PRMerged) || rec.PR.LastChecked.IsZero() {
			t.Fatalf("record %s not refreshed: %+v", key, rec)
		}
	}
}

func TestRefreshPRStatusesDoesNotOverlapPasses(t *testing.T) {
	live := adapter.Session{ID: "bbbb2222", AgentType: "fake", DisplayName: "B", Cwd: "/tmp", SessionName: "uam-fake-bbbb2222", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now(), PR: &adapter.PRRef{URL: "https://github.com/o/r/pull/2", Number: 2, Status: adapter.PROpen}}
	svc, _, _ := newLoadService(t, []adapter.Session{live})
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	svc.checkPR = func(context.Context, string) (pr.Status, error) {
		once.Do(func() { close(entered) })
		<-release
		return pr.Open, nil
	}
	done := make(chan error, 1)
	go func() { done <- svc.RefreshPRStatuses(context.Background()) }()
	<-entered
	if err := svc.RefreshPRStatuses(context.Background()); err != nil {
		t.Fatalf("overlapping pass should skip cleanly: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRefreshPRStatusesPreservesLastKnownStatusOnCheckerError(t *testing.T) {
	live := adapter.Session{ID: "cccc3333", AgentType: "fake", DisplayName: "C", Cwd: "/tmp", SessionName: "uam-fake-cccc3333", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now(), PR: &adapter.PRRef{URL: "https://github.com/o/r/pull/3", Number: 3, Status: adapter.PROpen}}
	svc, st, _ := newLoadService(t, []adapter.Session{live})
	svc.checkPR = func(context.Context, string) (pr.Status, error) {
		return pr.None, errors.New("checker unavailable")
	}
	if err := svc.RefreshPRStatuses(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := cfg.Sessions[store.Key("fake", live.ID)]
	if rec.PR == nil || rec.PR.LastStatus != string(adapter.PROpen) || rec.PR.LastChecked.IsZero() {
		t.Fatalf("last known PR state was not preserved: %+v", rec.PR)
	}
}

func TestRefreshPRStatusesSkipsFreshRecords(t *testing.T) {
	live := adapter.Session{ID: "dddd4444", AgentType: "fake", DisplayName: "D", Cwd: "/tmp", SessionName: "uam-fake-dddd4444", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now(), PR: &adapter.PRRef{URL: "https://github.com/o/r/pull/4", Number: 4, Status: adapter.PROpen}}
	svc, st, _ := newLoadService(t, []adapter.Session{live})
	if _, _, err := svc.LoadSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		rec := cfg.Sessions[store.Key("fake", live.ID)]
		rec.PR.LastChecked = time.Now()
		cfg.Sessions[store.Key("fake", live.ID)] = rec
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	svc.checkPR = func(context.Context, string) (pr.Status, error) {
		calls.Add(1)
		return pr.Closed, nil
	}
	if err := svc.RefreshPRStatuses(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("fresh PR record triggered %d checks", calls.Load())
	}
}

func TestAdapterStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		in   pr.Status
		want adapter.PRStatus
	}{
		{pr.Open, adapter.PROpen},
		{pr.Merged, adapter.PRMerged},
		{pr.Closed, adapter.PRClosed},
		{pr.Draft, adapter.PRDraft},
		{pr.None, adapter.PRNone},
	} {
		if got := adapterStatus(tc.in); got != tc.want {
			t.Fatalf("adapterStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func BenchmarkRefreshPRStatuses(b *testing.B) {
	sessions := make([]adapter.Session, 20)
	for i := range sessions {
		id := fmt.Sprintf("%08x", i+1)
		sessions[i] = adapter.Session{
			ID: id, AgentType: "fake", DisplayName: id, Cwd: "/tmp",
			SessionName: "uam-fake-" + id, State: adapter.Active, ProcAlive: adapter.Alive,
			CreatedAt: time.Now(), PR: &adapter.PRRef{URL: fmt.Sprintf("https://github.com/o/r/pull/%d", i+1), Number: i + 1, Status: adapter.PROpen},
		}
	}
	st, err := store.Open(filepath.Join(b.TempDir(), "sessions.json"))
	if err != nil {
		b.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: sessions}
	svc := NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	svc.checkPR = func(context.Context, string) (pr.Status, error) { return pr.Open, nil }
	if _, _, err := svc.LoadSessions(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		b.StopTimer()
		if err := st.Update(func(cfg *store.Config) error {
			for key, rec := range cfg.Sessions {
				if rec.PR != nil {
					rec.PR.LastChecked = time.Time{}
					cfg.Sessions[key] = rec
				}
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := svc.RefreshPRStatuses(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
