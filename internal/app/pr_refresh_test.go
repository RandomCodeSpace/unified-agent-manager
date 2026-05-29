package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// writeFakeGH installs a fake `gh` binary at UAM_GH_BIN/PATH whose script body
// is given, returning nothing. Used to make pr.Check behavior deterministic.
func writeFakeGH(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UAM_GH_BIN", gh)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// F02 — when pr.Check times out (hung gh), updatePRRecord must still stamp
// LastChecked so the 60s guard arms and the next tick does NOT re-launch gh.
func TestUpdatePRRecordWritesLastCheckedOnTimeout(t *testing.T) {
	// gh sleeps long enough to blow the per-check timeout.
	writeFakeGH(t, "#!/bin/sh\nsleep 30\n")

	live := adapter.Session{ID: "aaaa1111", AgentType: "fake", DisplayName: "A", Cwd: "/tmp", TmuxSession: "uam-fake-aaaa1111", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now(), PR: &adapter.PRRef{URL: "https://github.com/o/r/pull/1", Number: 1, Status: adapter.PROpen}}
	svc, st, _ := newLoadService(t, []adapter.Session{live})

	before := time.Now()
	if _, _, err := svc.LoadSessions(context.Background()); err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec, ok := cfg.Sessions[store.Key("fake", "aaaa1111")]
	if !ok || rec.PR == nil {
		t.Fatalf("PR record not persisted: %+v", cfg.Sessions)
	}
	if !rec.PR.LastChecked.After(before.Add(-time.Second)) {
		t.Fatalf("LastChecked not stamped on the timeout path: %v", rec.PR.LastChecked)
	}
}

// F02 — concurrent LoadSessions must not stack overlapping pr.Check subprocesses.
// The fake gh uses an atomic mkdir lock to detect overlap: if a second gh runs
// while the first is still in flight, the lock fails and writes a sentinel.
func TestRefreshDoesNotStackConcurrentLoads(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, "lock")
	overlap := filepath.Join(dir, "overlap")
	gh := filepath.Join(dir, "gh")
	// mkdir is atomic: only one process can hold the lock at a time. If a second
	// gh runs concurrently it can't create the dir, so it records overlap.
	body := "#!/bin/sh\n" +
		"if ! mkdir '" + lockDir + "' 2>/dev/null; then touch '" + overlap + "'; fi\n" +
		"sleep 0.2\n" +
		"rmdir '" + lockDir + "' 2>/dev/null\n" +
		"echo '{\"state\":\"OPEN\",\"isDraft\":false,\"mergedAt\":null}'\n"
	if err := os.WriteFile(gh, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UAM_GH_BIN", gh)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	live := adapter.Session{ID: "bbbb2222", AgentType: "fake", DisplayName: "B", Cwd: "/tmp", TmuxSession: "uam-fake-bbbb2222", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now(), PR: &adapter.PRRef{URL: "https://github.com/o/r/pull/2", Number: 2, Status: adapter.PROpen}}
	svc, _, _ := newLoadService(t, []adapter.Session{live})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := svc.LoadSessions(context.Background()); err != nil {
				t.Errorf("LoadSessions: %v", err)
			}
		}()
	}
	wg.Wait()

	if _, err := os.Stat(overlap); err == nil {
		t.Fatal("overlapping pr.Check subprocesses ran concurrently — in-flight guard missing")
	}
}
