package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

var importCaps = agentapi.Capabilities{Cancel: true, Permissions: true, Questions: true, History: true, Import: true}

// importManager starts a manager whose provider can import, with one
// Project, the offered models and Task defaults in Settings.
func importManager(t *testing.T) (*Manager, *agenttest.Provider, *store.Store, Project) {
	t.Helper()
	prov := agenttest.NewProvider("fake", importCaps)
	prov.SetModels([]agentapi.Model{{ID: "m-recorded", Name: "Recorded"}, {ID: "m-default", Name: "Default", Efforts: []string{"high"}}}, nil)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	p, err := m.AddProject(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSettings(SettingsPatch{TaskDefaults: &TaskDefaults{Provider: "fake", Model: "m-default", Effort: "high", Mode: "yolo"}}); err != nil {
		t.Fatal(err)
	}
	return m, prov, st, p
}

const (
	prevA = "e5cc1da2-d847-4205-8e3c-c27a133f0127"
	prevB = "5036d6d8-b2dd-4964-b423-72ef7baf4884"
	prevC = "14a7bdcc-9341-4823-b2ad-88d8b367335b"
)

func TestPreviousListsUnlinkedSessionsNewestFirst(t *testing.T) {
	m, prov, _, p := importManager(t)
	linked, _ := createSession(t, m, prov)
	base := time.Date(2026, 9, 24, 17, 0, 0, 0, time.UTC)
	prov.SetPrevious([]agentapi.PreviousConversation{
		{ID: prevA, Title: "old \x1b[1mone\x07", CreatedAt: base, UpdatedAt: base.Add(time.Minute)},
		{ID: prevB, Title: "newest", CreatedAt: base, UpdatedAt: base.Add(time.Hour)},
		{ID: linked.ConversationID, Title: "already a task", UpdatedAt: base.Add(2 * time.Hour)},
		{ID: "bad id with spaces", Title: "unusable", UpdatedAt: base.Add(3 * time.Hour)},
	}, nil)
	prov.SetInUse([]string{prevA}, nil)
	got, err := m.Previous(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []PreviousConversation{
		{Provider: "fake", ConversationID: prevB, Title: "newest", CreatedAt: base, UpdatedAt: base.Add(time.Hour)},
		{Provider: "fake", ConversationID: prevA, Title: "old one", CreatedAt: base, UpdatedAt: base.Add(time.Minute), InUse: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("previous =\n%+v\nwant\n%+v", got, want)
	}
	if dirs := prov.PreviousDirs(); !slices.Equal(dirs, []string{p.Dir}) {
		t.Fatalf("listed %v", dirs)
	}
	if checks := prov.InUseChecks(); len(checks) != 1 || !slices.Equal(checks[0], []string{prevB, prevA}) {
		t.Fatalf("in-use checks = %v", checks)
	}

	var many []agentapi.PreviousConversation
	for i := range maxPrevious + 5 {
		many = append(many, agentapi.PreviousConversation{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), UpdatedAt: base.Add(time.Duration(i) * time.Second)})
	}
	prov.SetPrevious(many, nil)
	if got, err := m.Previous(p.ID); err != nil || len(got) != maxPrevious || got[0].ConversationID != many[len(many)-1].ID {
		t.Fatalf("capped list = %d entries from %v, %v", len(got), got[0].ConversationID, err)
	}
	prov.SetInUse(nil, errors.New("method not found"))
	if _, err := m.Previous(p.ID); statusOf(err) != http.StatusBadGateway {
		t.Fatalf("failed in-use check = %v", err)
	}
	if _, err := m.Previous("nope"); statusOf(err) != http.StatusNotFound {
		t.Fatalf("unknown project = %v", err)
	}
}

func TestProvidersWithoutImportListNothing(t *testing.T) {
	m, prov, _ := newTestManager(t)
	p := addProject(t, m, t.TempDir())
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	if got, err := m.Previous(p); err != nil || len(got) != 0 || len(prov.PreviousDirs()) != 0 {
		t.Fatalf("previous = %v, %v; listed %v", got, err, prov.PreviousDirs())
	}
	if _, err := m.Import(context.Background(), p, prevA); statusOf(err) != http.StatusNotFound {
		t.Fatalf("import = %v", err)
	}
	if info := m.Providers()[0]; info.Capabilities.Import {
		t.Fatal("capability import advertised")
	}
}

func TestImportCreatesAClosedTaskWithTheRecordedTranscript(t *testing.T) {
	m, prov, st, p := importManager(t)
	base := time.Date(2026, 9, 24, 17, 0, 0, 0, time.UTC)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA, Title: "Fix \x1b[31mthe build", CreatedAt: base, UpdatedAt: base}, {ID: prevB, Title: "other"}}, nil)
	prov.SetHistory(prevA, agentapi.History{
		Items: []agentapi.Item{{ID: "u1", Kind: agentapi.ItemUser, Text: "fix it"}, {ID: "a1", Kind: agentapi.ItemAssistant, Text: "fixed"}},
		Model: "m-recorded",
	})
	prov.SetHistory(prevB, agentapi.History{Model: "m-gone"})

	sum, err := m.Import(context.Background(), p.ID, prevA)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Stage != StageActive || sum.State != StateClosed || sum.Open || sum.ConversationID != prevA || sum.ProjectID != p.ID ||
		sum.Model != "m-recorded" || sum.Effort != "" || sum.Mode != "yolo" || sum.Title != "Fix the build" || sum.Name != "" {
		t.Fatalf("imported = %+v", sum)
	}
	d := detail(t, m, sum.ID)
	if d.History != HistoryLoaded || len(d.Items) != 2 || d.Items[1].Text != "fixed" {
		t.Fatalf("detail = %+v", d)
	}
	if len(prov.Opens()) != 0 || len(prov.Reads()) != 1 {
		t.Fatalf("import opened %d and read %d conversations", len(prov.Opens()), len(prov.Reads()))
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := cfg.Sessions[store.Key("fake", sum.ID)]
	if rec.Surface != store.SurfaceWeb || rec.ProviderSessionID != prevA || rec.Web.Turn != StateClosed || rec.Web.Model != "m-recorded" || rec.Web.ProjectID != p.ID || !rec.Web.Imported {
		t.Fatalf("record = %+v %+v", rec, rec.Web)
	}
	// Linked now: neither listed nor imported again.
	if got, err := m.Previous(p.ID); err != nil || len(got) != 1 || got[0].ConversationID != prevB {
		t.Fatalf("previous after import = %+v, %v", got, err)
	}
	if _, err := m.Import(context.Background(), p.ID, prevA); statusOf(err) != http.StatusConflict {
		t.Fatalf("second import = %v", err)
	}
	// A model the provider no longer offers gives way to the Task defaults
	// of Settings.
	other, err := m.Import(context.Background(), p.ID, prevB)
	if err != nil || other.Model != "m-default" || other.Effort != "high" {
		t.Fatalf("import with an unoffered model = %+v, %v", other, err)
	}
	for _, bad := range []struct {
		project, conv string
		status        int
	}{{p.ID, prevC, http.StatusNotFound}, {"nope", prevC, http.StatusNotFound}, {p.ID, "../etc", http.StatusBadRequest}} {
		if _, err := m.Import(context.Background(), bad.project, bad.conv); statusOf(err) != bad.status {
			t.Fatalf("import %s/%s = %v, want %d", bad.project, bad.conv, err, bad.status)
		}
	}
}

func TestImportedGuardSurvivesRestart(t *testing.T) {
	m, prov, st, p := importManager(t)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	prov.SetHistory(prevA, agentapi.History{})
	sum, err := m.Import(context.Background(), p.ID, prevA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Rename(sum.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov2 := agenttest.NewProvider("fake", importCaps)
	prov2.SetInUse([]string{prevA}, nil)
	m2 := startManager(t, st, prov2)
	if _, err := m2.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t)}); !errors.Is(err, errHeldElsewhere) {
		t.Fatalf("send after restart = %v", err)
	}
}

func TestTerminalLinkedTaskChecksForAnotherHolder(t *testing.T) {
	m, prov, _, _ := importManager(t)
	sum, conv := createSession(t, m, prov)
	m.mu.Lock()
	m.sessions[sum.ID].terminalID = "linked"
	m.mu.Unlock()
	prov.SetInUse([]string{sum.ConversationID}, nil)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t)}); !errors.Is(err, errHeldElsewhere) || len(conv.Sends()) != 0 {
		t.Fatalf("terminal-linked send = %v", err)
	}
}

func TestHolderCheckDoesNotBlockClose(t *testing.T) {
	m, prov, _, p := importManager(t)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	prov.SetHistory(prevA, agentapi.History{})
	sum, err := m.Import(context.Background(), p.ID, prevA)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	prov.SetInUseHook(func(ctx context.Context, _ []string) ([]string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > holderTimeout {
			t.Error("holder check has no short deadline")
		}
		close(started)
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	result := make(chan error, 1)
	rid := mustUUID(t)
	go func() { _, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: rid}); result <- err }()
	<-started
	closed := make(chan error, 1)
	go func() { _, err := m.Archive(sum.ID); closed <- err }()
	select {
	case err := <-closed:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("archive waited on the holder RPC")
	}
	close(release)
	if err := <-result; statusOf(err) != http.StatusConflict {
		t.Fatalf("changed task send = %v", err)
	}
	if len(prov.Opens()) != 0 {
		t.Fatal("send opened an archived Task")
	}
}

func TestImportCommandAndSubagentCheckHolder(t *testing.T) {
	m, prov, _, p := importManager(t)
	prov.SetCommands([]agentapi.Command{{Name: "review"}}, nil)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	prov.SetHistory(prevA, agentapi.History{})
	sum, err := m.Import(context.Background(), p.ID, prevA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t)}); err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.EmitSubagent(agentapi.Subagent{ID: "sub", Status: agentapi.SubagentIdle})
	prov.SetInUse([]string{prevA}, nil)
	if _, err := m.Command(sum.ID, CommandRequest{Name: "review", RequestID: mustUUID(t)}); !errors.Is(err, errHeldElsewhere) {
		t.Fatalf("command = %v", err)
	}
	if _, err := m.PromptSubagent(sum.ID, "sub", "go", mustUUID(t)); !errors.Is(err, errHeldElsewhere) {
		t.Fatalf("subagent = %v", err)
	}
	if len(conv.CommandRuns()) != 0 || len(conv.SubagentPrompts()) != 0 {
		t.Fatal("held conversation received a write")
	}
}

func TestConcurrentImportsCreateOnlyOneTask(t *testing.T) {
	m, prov, _, p := importManager(t)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	started, release := make(chan struct{}, 2), make(chan struct{})
	prov.SetReadHook(func(ctx context.Context, _ agentapi.ReadRequest) (agentapi.History, error) {
		started <- struct{}{}
		select {
		case <-release:
			return agentapi.History{}, nil
		case <-ctx.Done():
			return agentapi.History{}, ctx.Err()
		}
	})
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := m.Import(context.Background(), p.ID, prevA); results <- err }()
	}
	<-started
	<-started
	close(release)
	first, second := <-results, <-results
	if (first == nil) == (second == nil) || (first != nil && !errors.Is(first, errAlreadyTask)) || (second != nil && !errors.Is(second, errAlreadyTask)) || len(m.List()) != 1 {
		t.Fatalf("imports = %v, %v; Tasks %d", first, second, len(m.List()))
	}
}

func TestPreviousCountsAndImportKeepFolderBoundaries(t *testing.T) {
	m, prov, _, p := importManager(t)
	otherDir := t.TempDir()
	other := addProject(t, m, otherDir)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA, Workdir: p.Dir}, {ID: prevB, Workdir: otherDir}}, nil)
	counts, err := m.PreviousCounts(context.Background())
	if err != nil || counts[p.ID] != 1 || counts[other] != 1 || len(prov.PreviousDirs()) != 1 || prov.PreviousDirs()[0] != "" || len(prov.InUseChecks()) != 0 {
		t.Fatalf("counts = %v, %v", counts, err)
	}
	if _, err := m.Import(context.Background(), p.ID, prevB); statusOf(err) != http.StatusNotFound {
		t.Fatalf("other folder import = %v", err)
	}
}

func TestImportCancellationAndBusyReaderQueueCreateNoTasks(t *testing.T) {
	m, prov, _, p := importManager(t)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}, {ID: prevB}, {ID: prevC}}, nil)
	started := make(chan struct{}, 2)
	prov.SetReadHook(func(ctx context.Context, _ agentapi.ReadRequest) (agentapi.History, error) {
		started <- struct{}{}
		<-ctx.Done()
		return agentapi.History{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, 2)
	for _, id := range []string{prevA, prevB} {
		go func() { _, err := m.Import(ctx, p.ID, id); results <- err }()
	}
	<-started
	<-started
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, err := m.Import(short, p.ID, prevC); statusOf(err) != http.StatusServiceUnavailable {
		t.Fatalf("busy import = %v", err)
	}
	cancel()
	if first, second := <-results, <-results; first == nil || second == nil || len(m.List()) != 0 {
		t.Fatalf("cancelled imports = %v, %v; Tasks %d", first, second, len(m.List()))
	}
}

func TestImportIsRefusedWhileAnotherClientHoldsTheSession(t *testing.T) {
	m, prov, _, p := importManager(t)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	prov.SetHistory(prevA, agentapi.History{})
	prov.SetInUse([]string{prevA}, nil)
	if _, err := m.Import(context.Background(), p.ID, prevA); !errors.Is(err, errHeldElsewhere) {
		t.Fatalf("import while held = %v", err)
	}
	prov.SetInUse(nil, errors.New("boom"))
	if _, err := m.Import(context.Background(), p.ID, prevA); statusOf(err) != http.StatusBadGateway {
		t.Fatalf("import with a failed check = %v", err)
	}
	if len(m.List()) != 0 || len(prov.Reads()) != 0 {
		t.Fatalf("a refused import created %d tasks and read %d", len(m.List()), len(prov.Reads()))
	}
}

func TestEveryWriteChecksForAnotherHolder(t *testing.T) {
	m, prov, _, p := importManager(t)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	prov.SetHistory(prevA, agentapi.History{Items: []agentapi.Item{{ID: "u1", Kind: agentapi.ItemUser, Text: "earlier"}}})
	sum, err := m.Import(context.Background(), p.ID, prevA)
	if err != nil {
		t.Fatal(err)
	}
	prov.SetInUse([]string{prevA}, nil)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t), Mode: ModeSend}); !errors.Is(err, errHeldElsewhere) || statusOf(err) != http.StatusConflict {
		t.Fatalf("send while held = %v", err)
	}
	if len(prov.Opens()) != 0 {
		t.Fatal("a refused send opened the conversation")
	}
	prov.SetInUse(nil, errors.New("method not found"))
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t), Mode: ModeSend}); !errors.Is(err, errHolderUnknown) {
		t.Fatalf("send with a failed check = %v", err)
	}

	prov.SetInUse(nil, nil)
	if sub, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t), Mode: ModeSend}); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("send = %+v, %v", sub, err)
	}
	conv := prov.Last()
	if opens := prov.Opens(); len(opens) != 1 || opens[0].ConversationID != prevA || !slices.Equal(conv.Sends(), []string{"go"}) {
		t.Fatalf("opens = %+v, sends = %v", opens, conv.Sends())
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	queued := mustUUID(t)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: queued, Mode: ModeQueue}); err != nil {
		t.Fatal(err)
	}
	prov.SetInUse([]string{prevA}, nil)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "steer", RequestID: mustUUID(t), Mode: ModeSteer}); !errors.Is(err, errHeldElsewhere) || len(conv.Steers()) != 0 {
		t.Fatalf("steer while held = %v, steers %v", err, conv.Steers())
	}
	// The queued prompt is refused when the turn ends: it stays, paused.
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "paused queue", func() bool { d := detail(t, m, sum.ID); return d.QueuePaused && len(d.Queue) == 1 })
	if !slices.Equal(conv.Sends(), []string{"go"}) {
		t.Fatalf("sends = %v", conv.Sends())
	}
	checks := prov.InUseChecks()
	for _, c := range checks {
		if !slices.Equal(c, []string{prevA}) {
			t.Fatalf("checks = %v", checks)
		}
	}
}

func TestProvidersWithoutImportAreNotChecked(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	prov.SetInUse([]string{sum.ConversationID}, nil)
	if sub, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t), Mode: ModeSend}); err != nil || sub.Status != SubmissionAccepted || len(conv.Sends()) != 1 {
		t.Fatalf("send = %+v, %v", sub, err)
	}
	if n := len(prov.InUseChecks()); n != 0 {
		t.Fatalf("%d in-use checks for a provider that cannot tell", n)
	}
}

func TestWebCreatedTasksDoNotCheckForAnotherHolder(t *testing.T) {
	m, prov, _, _ := importManager(t)
	sum, conv := createSession(t, m, prov)
	prov.SetInUse(nil, errors.New("method not found"))
	if sub, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t), Mode: ModeSend}); err != nil || sub.Status != SubmissionAccepted || len(conv.Sends()) != 1 {
		t.Fatalf("send = %+v, %v", sub, err)
	}
	if n := len(prov.InUseChecks()); n != 0 {
		t.Fatalf("%d in-use checks for a web-created Task", n)
	}
}

func TestImportedTaskPreservesLegacyTerminalRecord(t *testing.T) {
	m, prov, st, p := importManager(t)
	const termID = "0da22111-aaaa-4bbb-8ccc-dddddddddddd"
	now := time.Now().UTC()
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions[store.Key("fake", termID)] = store.SessionRecord{ID: termID, Agent: "fake", Name: "fix \x1b[31mbuild", Mode: store.ModeSafe, Workdir: p.Dir,
			SessionName: "uam-fake-0da22111", CreatedAt: now, LastSeenAt: now, Status: store.StatusActive, ProviderSessionID: prevA}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	prov.SetHistory(prevA, agentapi.History{})
	sum, err := m.Import(context.Background(), p.ID, prevA)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.sessions[sum.ID].terminalID; got != termID {
		t.Fatalf("legacy terminal link = %q, want %q", got, termID)
	}

	// The link survives a restart.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov2 := agenttest.NewProvider("fake", importCaps)
	prov2.SetHistory(prevA, agentapi.History{})
	m2 := NewManager(st, []agentapi.Provider{prov2})
	if err := m2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m2.Shutdown(context.Background()) })
	if got := m2.sessions[sum.ID].terminalID; got != termID {
		t.Fatalf("legacy terminal link after restart = %q, want %q", got, termID)
	}
	prov2.SetInUse([]string{prevA}, nil)
	if _, err := m2.Submit(sum.ID, PromptRequest{Text: "do not send", RequestID: mustUUID(t), Mode: ModeSend}); !errors.Is(err, errHeldElsewhere) {
		t.Fatalf("retired terminal link bypassed provider holder check: %v", err)
	}

	// Deleting the Task leaves the terminal's record alone.
	if _, err := m2.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := m2.Delete(sum.ID); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := cfg.Sessions[store.Key("fake", termID)]; !ok || rec.ProviderSessionID != prevA || rec.Surface != "" {
		t.Fatalf("terminal record after delete = %+v, %v", rec, ok)
	}
}

func TestPreviousAndImportRoutes(t *testing.T) {
	m, prov, _, p := importManager(t)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test", Assets: fstest.MapFS{"index.html": {Data: []byte("app")}}})
	if err != nil {
		t.Fatal(err)
	}
	ts := &testServer{srv: srv, m: m, prov: prov}
	base := time.Date(2026, 9, 24, 17, 0, 0, 0, time.UTC)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA, Title: "one", Workdir: p.Dir, CreatedAt: base, UpdatedAt: base}}, nil)
	prov.SetHistory(prevA, agentapi.History{})
	prov.SetInUse([]string{prevA}, nil)

	counts := ts.do(http.MethodGet, "/api/previous/counts", "", withCookie(ts))
	if counts.Code != http.StatusOK || strings.TrimSpace(counts.Body.String()) != `{"`+p.ID+`":1}` {
		t.Fatalf("counts = %d %s", counts.Code, counts.Body)
	}
	w := ts.do(http.MethodGet, "/api/projects/"+p.ID+"/previous", "", withCookie(ts))
	want := `[{"provider":"fake","conversation_id":"` + prevA + `","title":"one","created_at":"2026-09-24T17:00:00Z","updated_at":"2026-09-24T17:00:00Z","in_use":true}]`
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != want {
		t.Fatalf("GET previous = %d %s", w.Code, w.Body)
	}
	target := "/api/projects/" + p.ID + "/previous/" + prevA + "/import"
	if w := ts.do(http.MethodPost, target, "", withCookie(ts)); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "another client has this conversation open") {
		t.Fatalf("import while held = %d %s", w.Code, w.Body)
	}
	prov.SetInUse(nil, nil)
	w = ts.do(http.MethodPost, target, "{}", withCookie(ts))
	var sum SessionSummary
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &sum) != nil || sum.ConversationID != prevA || sum.State != StateClosed {
		t.Fatalf("import = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodGet, "/api/sessions/"+sum.ID, "", withCookie(ts))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"history":"loaded"`) {
		t.Fatalf("detail = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, "/api/meta", "", withCookie(ts)); !strings.Contains(w.Body.String(), `"import":true`) {
		t.Fatalf("meta = %s", w.Body)
	}
}

func TestImportedSubagentConcurrentSameRequestSendsOnce(t *testing.T) {
	m, prov, _, p := importManager(t)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	prov.SetHistory(prevA, agentapi.History{})
	sum, err := m.Import(context.Background(), p.ID, prevA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t)}); err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.EmitSubagent(agentapi.Subagent{ID: "sub", Status: agentapi.SubagentIdle})
	started, release := make(chan struct{}, 2), make(chan struct{})
	prov.SetInUseHook(func(ctx context.Context, _ []string) ([]string, error) {
		started <- struct{}{}
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	type result struct {
		sub Submission
		err error
	}
	results := make(chan result, 2)
	rid := mustUUID(t)
	for range 2 {
		go func() { sub, err := m.PromptSubagent(sum.ID, "sub", "follow up", rid); results <- result{sub, err} }()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("holder calls did not overlap")
		}
	}
	close(release)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("repeat errors: %v, %v", first.err, second.err)
	}
	if n := len(conv.SubagentPrompts()); n != 1 {
		t.Fatalf("same request sent %d provider follow-ups; expected 1", n)
	}
}

func TestImportedSubagentRevalidatesStateAfterHolderCheck(t *testing.T) {
	m, prov, _, p := importManager(t)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	prov.SetHistory(prevA, agentapi.History{})
	sum, err := m.Import(context.Background(), p.ID, prevA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t)}); err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	conv.EmitTurn(agentapi.TurnCompleted, "")
	conv.EmitSubagent(agentapi.Subagent{ID: "sub", Status: agentapi.SubagentIdle})
	started, release := make(chan struct{}), make(chan struct{})
	prov.SetInUseHook(func(ctx context.Context, _ []string) ([]string, error) {
		close(started)
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	result := make(chan error, 1)
	rid := mustUUID(t)
	go func() { _, err := m.PromptSubagent(sum.ID, "sub", "follow up", rid); result <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("holder call did not start")
	}
	conv.EmitSubagent(agentapi.Subagent{ID: "sub", Status: agentapi.SubagentRunning})
	close(release)
	err = <-result
	if statusOf(err) != http.StatusConflict || len(conv.SubagentPrompts()) != 0 {
		t.Fatalf("follow-up after subagent became running: err=%v, provider writes=%d; want 409 and zero writes", err, len(conv.SubagentPrompts()))
	}
}

func TestConcurrentImportsRespectAggregateHistoryBudget(t *testing.T) {
	m, prov, _, p := importManager(t)
	listed := make([]agentapi.PreviousConversation, 6)
	h := agentapi.History{}
	for i := range 3 {
		h.Items = append(h.Items, agentapi.Item{ID: fmt.Sprint(i), Kind: agentapi.ItemAssistant, Text: strings.Repeat("x", 4<<20)})
	}
	for i := range listed {
		listed[i].ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		prov.SetHistory(listed[i].ID, h)
	}
	prov.SetPrevious(listed, nil)
	m.projectMu.Lock()
	m.mu.Lock()
	beforeSeq := m.seq
	m.mu.Unlock()
	results := make(chan error, len(listed))
	for _, c := range listed {
		go func() { _, err := m.Import(context.Background(), p.ID, c.ID); results <- err }()
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		m.mu.Lock()
		seq := m.seq
		m.mu.Unlock()
		if seq >= beforeSeq+uint64(len(listed)) {
			break
		}
		if time.Now().After(deadline) {
			m.projectMu.Unlock()
			t.Fatal("concurrent imports did not finish history installation")
		}
		time.Sleep(time.Millisecond)
	}
	m.projectMu.Unlock()
	for range listed {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for _, s := range m.sessions {
		if s.historyRead {
			total += s.historyBytes
		}
	}
	if total > maxCachedHistoryBytes {
		t.Fatalf("concurrent imports cached %d bytes, budget %d", total, maxCachedHistoryBytes)
	}
}

func TestQueuedImportedPromptsKeepOrderDuringHolderCheck(t *testing.T) {
	m, prov, _, p := importManager(t)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	prov.SetHistory(prevA, agentapi.History{})
	sum, err := m.Import(context.Background(), p.ID, prevA)
	if err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, m, sum.ID, "first", mustUUID(t), ModeSend, SubmissionAccepted)
	conv := prov.Last()
	conv.EmitTurn(agentapi.TurnWorking, "")
	mustSubmit(t, m, sum.ID, "second", mustUUID(t), ModeQueue, SubmissionQueued)
	mustSubmit(t, m, sum.ID, "third", mustUUID(t), ModeQueue, SubmissionQueued)
	started, release := make(chan struct{}, 3), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	prov.SetInUseHook(func(ctx context.Context, _ []string) ([]string, error) {
		started <- struct{}{}
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	<-started
	s, err := m.lookup(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.drain(s); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second drain competed with an in-flight queue head")
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "overtake", RequestID: mustUUID(t)}); statusOf(err) != http.StatusConflict {
		t.Fatalf("send past queued head = %v", err)
	}
	finish()
	waitUntil(t, "second queued prompt", func() bool { return len(conv.Sends()) == 2 })
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "third queued prompt", func() bool { return len(conv.Sends()) == 3 })
	if !slices.Equal(conv.Sends(), []string{"first", "second", "third"}) {
		t.Fatalf("send order = %v", conv.Sends())
	}
}

func TestImportedPromptConcurrentSameRequestSendsOnce(t *testing.T) {
	m, prov, _, p := importManager(t)
	prov.SetPrevious([]agentapi.PreviousConversation{{ID: prevA}}, nil)
	prov.SetHistory(prevA, agentapi.History{})
	sum, err := m.Import(context.Background(), p.ID, prevA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "go", RequestID: mustUUID(t)}); err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	conv.EmitTurn(agentapi.TurnCompleted, "")
	started, release := make(chan struct{}, 2), make(chan struct{})
	prov.SetInUseHook(func(ctx context.Context, _ []string) ([]string, error) {
		started <- struct{}{}
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	type result struct {
		sub Submission
		err error
	}
	results := make(chan result, 2)
	rid := mustUUID(t)
	for range 2 {
		go func() {
			sub, err := m.Submit(sum.ID, PromptRequest{Text: "follow up", RequestID: rid})
			results <- result{sub, err}
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("holder calls did not overlap")
		}
	}
	close(release)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("repeat errors: %v, %v", first.err, second.err)
	}
	if n := len(conv.Sends()); n != 2 {
		t.Fatalf("initial prompt and repeated request sent %d provider prompts; expected 2", n)
	}
}
