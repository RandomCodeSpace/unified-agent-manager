package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// A nil provider is skipped, and a provider whose name is taken is ignored:
// the first one keeps the name.
func TestManagerKeepsTheFirstProviderOfAName(t *testing.T) {
	first, second := agenttest.NewProvider("fake", allCaps), agenttest.NewProvider("fake", allCaps)
	m := startManager(t, openTestStore(t), nil, first, second)
	if infos := m.Providers(); len(infos) != 1 || infos[0].Name != "fake" {
		t.Fatalf("providers = %+v", infos)
	}
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, t.TempDir())}); err != nil {
		t.Fatal(err)
	}
	if len(first.Opens()) != 1 || len(second.Opens()) != 0 {
		t.Fatalf("opens = %d, %d", len(first.Opens()), len(second.Opens()))
	}
}

// A directory whose name is blank gives no default Project name: adding it
// needs a name, and a stored Project without one shows the whole path.
func TestProjectInABlankNamedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "   ")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st := openTestStore(t)
	m := startManager(t, st)
	if _, err := m.AddProject(dir, ""); statusOf(err) != http.StatusBadRequest || err.Error() != "name is required" {
		t.Fatalf("add without a name = %v", err)
	}
	p, err := m.AddProject(dir, "spaces")
	if err != nil || p.Name != "spaces" {
		t.Fatalf("add with a name = %+v, %v", p, err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		stored := cfg.WebProjects[p.ID]
		stored.Name = ""
		cfg.WebProjects[p.ID] = stored
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := startManager(t, st).Projects(); len(got) != 1 || got[0].ID != p.ID || got[0].Name != p.Dir {
		t.Fatalf("loaded projects = %+v, want the name %q", got, p.Dir)
	}
}

// A record carrying more turn timings than a Task keeps loads the latest.
func TestRecordWithTooManyTurnTimingsLoadsTheLatest(t *testing.T) {
	st := openTestStore(t)
	id := mustUUID(t)
	seedWebRecord(t, st, id, "conv_a", StateCompleted)
	start := time.Now().UTC().Add(-time.Hour)
	if err := st.Update(func(cfg *store.Config) error {
		key := store.Key("fake", id)
		rec := cfg.Sessions[key]
		for i := range maxTurnTimings + 1 {
			at := start.Add(time.Duration(i) * time.Second)
			rec.Web.TurnTimings = append(rec.Web.TurnTimings, store.TurnTiming{ID: fmt.Sprintf("t%d", i), StartedAt: at, EndedAt: at.Add(time.Millisecond), State: StateCompleted})
		}
		cfg.Sessions[key] = rec
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	got := detail(t, m, id).TurnTimings
	if len(got) != maxTurnTimings || got[0].ID != "t1" || got[len(got)-1].ID != fmt.Sprintf("t%d", maxTurnTimings) {
		t.Fatalf("loaded %d timings, first %q", len(got), got[0].ID)
	}
}

// Tasks created at the same instant list in a stable order, by ID.
func TestTasksCreatedTogetherListByID(t *testing.T) {
	m, _, _ := newTestManager(t)
	setNow(m, time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	project := addProject(t, m, t.TempDir())
	var want []string
	for range 3 {
		sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, sum.ID)
	}
	slices.Sort(want)
	var got []string
	for _, s := range m.List() {
		got = append(got, s.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("list = %v, want %v", got, want)
	}
}
