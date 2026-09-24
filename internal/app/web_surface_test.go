package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const webRecordID = "9f8e7d6c-1111-4222-8333-444455556666"

// seedWebRecord stores a web-owned record next to an ordinary stopped
// terminal record so each test can check that only the terminal one is seen.
func seedWebRecord(t *testing.T, st *store.Store) {
	t.Helper()
	now := time.Now()
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("fake", webRecordID)] = store.SessionRecord{
			ID: webRecordID, Agent: "fake", Name: "web task", Mode: store.ModeSafe, Workdir: "/tmp",
			CreatedAt: now, LastSeenAt: now, Status: store.StatusActive, Surface: store.SurfaceWeb,
			ProviderSessionID: "conv_1", Web: &store.WebState{Turn: "working", UpdatedAt: now},
		}
		cfg.Sessions[store.Key("fake", "dead0001")] = store.SessionRecord{
			ID: "dead0001", Agent: "fake", Name: "terminal", Mode: store.ModeYolo, Workdir: "/tmp",
			SessionName: "uam-fake-dead0001", CreatedAt: now, LastSeenAt: now, Status: store.StatusActive,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSessionsAndListSkipWebRecords(t *testing.T) {
	svc, st, _ := newLoadService(t, nil)
	seedWebRecord(t, st)
	sessions, cfg, err := svc.LoadSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "dead0001" {
		t.Fatalf("terminal view = %+v, want only the terminal record", sessions)
	}
	if _, ok := cfg.Sessions[store.Key("fake", webRecordID)]; !ok {
		t.Fatal("the web record must stay in the stored config")
	}
	out := captureStdout(t, func() {
		if err := svc.PrintList(context.Background(), false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, webRecordID) || strings.Contains(out, "web task") {
		t.Fatalf("uam ls printed a web record: %q", out)
	}
}

func TestFindExplainsWebRecordsAndTerminalActionsRefuseThem(t *testing.T) {
	svc, st, fake := newLoadService(t, nil)
	seedWebRecord(t, st)
	for _, id := range []string{webRecordID, webRecordID[:6]} {
		_, _, err := svc.Find(context.Background(), id)
		if err == nil || !strings.Contains(err.Error(), "managed by the web interface") || !strings.Contains(err.Error(), `"uam web"`) {
			t.Fatalf("Find(%q) error = %v", id, err)
		}
	}
	if err := svc.Stop(context.Background(), webRecordID, true); err == nil {
		t.Fatal("stop/rm must refuse a web record")
	}
	if _, err := svc.AttachSpec(context.Background(), webRecordID); err == nil {
		t.Fatal("attach must refuse a web record")
	}
	if err := svc.Restart(context.Background(), webRecordID); err == nil {
		t.Fatal("restart must refuse a web record")
	}
	if fake.stopped || fake.resumed != nil || fake.attachCount != 0 {
		t.Fatalf("terminal adapter touched a web record: stopped=%v resumed=%v attach=%d", fake.stopped, fake.resumed, fake.attachCount)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := cfg.Sessions[store.Key("fake", webRecordID)]
	if !ok || rec.Surface != store.SurfaceWeb || rec.Web == nil || rec.Web.Turn != "working" {
		t.Fatalf("web record changed by terminal actions: %+v", rec)
	}
	// The terminal record is still found normally.
	if found, _, err := svc.Find(context.Background(), "dead0001"); err != nil || found.ID != "dead0001" {
		t.Fatalf("Find terminal record = %+v, %v", found, err)
	}
}

func TestPruneStartupKeepsWebRecords(t *testing.T) {
	live := adapter.Session{ID: "live0001", AgentType: "fake", SessionName: "uam-fake-live0001", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	svc, st, _ := newLoadService(t, []adapter.Session{live})
	old := time.Now().Add(-2 * pruneMaxAge)
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("fake", webRecordID)] = store.SessionRecord{ID: webRecordID, Agent: "fake", Surface: store.SurfaceWeb, LastSeenAt: old}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PruneStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Sessions[store.Key("fake", webRecordID)]; !ok {
		t.Fatal("startup prune deleted a web record")
	}
}
