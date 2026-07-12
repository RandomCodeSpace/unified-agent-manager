package app

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

func TestGroupToggleWaitsForPendingReorderBeforeReload(t *testing.T) {
	persistStarted := make(chan struct{})
	allowPersist := make(chan struct{})
	reloadStarted := make(chan struct{})
	m := NewWithDeps(nil, nil)
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "fake", Cwd: "/tmp/a", ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "b", AgentType: "fake", Cwd: "/tmp/a", ProcAlive: adapter.Alive, SortIndex: 1},
	}
	m.persistSortIndices = func([]adapter.Session) error {
		close(persistStarted)
		<-allowPersist
		return nil
	}
	m.reloadSessions = func() sessionsLoadedMsg {
		close(reloadStarted)
		return sessionsLoadedMsg{sessions: m.sessions, groupByDir: true}
	}
	m.selected = 0
	_ = m.moveSession(1)

	model, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("pending reorder toggle needs a sequenced command")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case <-persistStarted:
	case <-time.After(time.Second):
		t.Fatal("reorder persistence did not start")
	}
	select {
	case <-reloadStarted:
		t.Fatal("reload began before reorder persistence completed")
	case <-time.After(30 * time.Millisecond):
	}
	close(allowPersist)
	select {
	case <-reloadStarted:
	case <-time.After(time.Second):
		t.Fatal("reload did not begin after reorder persistence completed")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sequenced toggle command did not complete")
	}
}

func TestGroupedFlushDoesNotOverwriteConcurrentUnrelatedProviderRecord(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := []adapter.Session{
		{ID: "a1", AgentType: "fake", Cwd: filepath.Join(dir, "a"), ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "a2", AgentType: "fake", Cwd: filepath.Join(dir, "a"), ProcAlive: adapter.Alive, SortIndex: 1},
		{ID: "a1", AgentType: "other", Cwd: filepath.Join(dir, "other"), ProcAlive: adapter.Alive, SortIndex: 2},
	}
	svc := NewService(st, nil)
	if err := svc.UpdateSortIndices(sessions); err != nil {
		t.Fatal(err)
	}
	m := NewWithDeps(st, nil)
	m.sessions = sessions
	m.groupByDir = true
	m.selected = 0
	_ = m.moveSession(1)
	if err := st.Update(func(cfg *store.Config) error {
		rec := cfg.Sessions[store.Key("other", "a1")]
		rec.SortIndex = 99
		cfg.Sessions[store.Key("other", "a1")] = rec
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	drainCmd(m.flushReorder())

	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Sessions[store.Key("other", "a1")].SortIndex; got != 99 {
		t.Fatalf("stale grouped flush overwrote unrelated provider record: got %d want 99", got)
	}
}

func TestGroupedReorderNormalizesCollidingIndicesWithinAllowedGroup(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewWithDeps(st, nil)
	m.groupByDir = true
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "fake", Cwd: dir, ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "b", AgentType: "fake", Cwd: dir, ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "c", AgentType: "fake", Cwd: dir, ProcAlive: adapter.Alive, SortIndex: 0},
	}
	m.selected = 0
	_ = m.moveSession(1)
	if got := sessionIDs(m.sessions); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Fatalf("requested adjacent pair did not swap: %v", got)
	}
	drainCmd(m.flushReorder())
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	reloaded := append([]adapter.Session(nil), m.sessions...)
	for i := range reloaded {
		reloaded[i].SortIndex = cfg.Sessions[store.Key(reloaded[i].AgentType, reloaded[i].ID)].SortIndex
	}
	SortSessions(reloaded)
	if got := sessionIDs(reloaded); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Fatalf("colliding indices changed a third row after persist/reload: %v", got)
	}
	for i, sess := range reloaded {
		if sess.SortIndex != i {
			t.Fatalf("normalized group index for %s = %d, want %d", sess.ID, sess.SortIndex, i)
		}
	}
}

func TestFailedSequencedReorderDoesNotPersistGroupSetting(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewWithDeps(st, nil)
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "fake", Cwd: dir, ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "b", AgentType: "fake", Cwd: dir, ProcAlive: adapter.Alive, SortIndex: 1},
	}
	m.persistSortIndices = func([]adapter.Session) error { return errTestBoom }
	m.selected = 0
	_ = m.moveSession(1)
	model, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	msg := cmd()
	model, _ = m.Update(msg)
	m = model.(Model)
	if m.groupByDir || m.message == "" {
		t.Fatalf("failed reorder persistence must revert grouping with feedback: grouped=%v message=%q", m.groupByDir, m.message)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.GroupByDir {
		t.Fatal("view setting was persisted even though prerequisite reorder write failed")
	}
}

func TestRapidGroupToggleLastGenerationWinsModelAndStore(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	firstPersistStarted := make(chan struct{})
	allowFirstPersist := make(chan struct{})
	m := NewWithDeps(st, nil)
	m.sessions = []adapter.Session{
		{ID: "a", AgentType: "fake", Cwd: dir, ProcAlive: adapter.Alive, SortIndex: 0},
		{ID: "b", AgentType: "fake", Cwd: dir, ProcAlive: adapter.Alive, SortIndex: 1},
	}
	m.persistSortIndices = func(sessions []adapter.Session) error {
		if len(sessions) > 0 {
			close(firstPersistStarted)
			<-allowFirstPersist
		}
		return nil
	}
	m.reloadSessions = func() sessionsLoadedMsg {
		cfg, err := st.Load()
		return sessionsLoadedMsg{sessions: append([]adapter.Session(nil), m.sessions...), groupByDir: cfg.UI.GroupByDir, err: err}
	}
	m.selected = 0
	_ = m.moveSession(1)

	model, firstCmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	firstDone := make(chan tea.Msg, 1)
	go func() { firstDone <- firstCmd() }()
	select {
	case <-firstPersistStarted:
	case <-time.After(time.Second):
		t.Fatal("first toggle did not block in reorder persistence")
	}

	model, secondCmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	secondMsg := secondCmd()
	model, _ = m.Update(secondMsg)
	m = model.(Model)
	if m.groupByDir {
		t.Fatal("second toggle should leave model ungrouped")
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.GroupByDir {
		t.Fatal("second toggle should persist ungrouped setting")
	}

	close(allowFirstPersist)
	var firstMsg tea.Msg
	select {
	case firstMsg = <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first toggle did not finish after release")
	}
	model, _ = m.Update(firstMsg)
	m = model.(Model)
	if m.groupByDir {
		t.Fatal("stale first completion overwrote latest model toggle")
	}
	cfg, err = st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.GroupByDir {
		t.Fatal("stale first command overwrote latest stored toggle")
	}
}
