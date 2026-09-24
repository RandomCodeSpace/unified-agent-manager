package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// setNow pins the manager clock. Callers make sure no drain or answer
// goroutine is running.
func setNow(m *Manager, at time.Time) {
	m.mu.Lock()
	m.now = func() time.Time { return at }
	m.mu.Unlock()
}

// nextSession returns the next session frame's summary.
func nextSession(t *testing.T, sub *Subscriber) SessionSummary {
	t.Helper()
	var s SessionSummary
	decodeField(t, frameOf(t, sub, "session"), "session", &s)
	return s
}

func wantConflict(t *testing.T, what string, err error, message string) {
	t.Helper()
	if statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), message) {
		t.Fatalf("%s = %v, want 409 %q", what, err, message)
	}
}

func TestSettleReopenAndArchive(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}

	settledAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	setNow(m, settledAt)
	settled, err := m.Settle(sum.ID)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if settled.Stage != StageSettled || !settled.SettledAt.Equal(settledAt) || !settled.ArchivedAt.IsZero() ||
		settled.Open || settled.State != StateClosed || conv.Closes() != 1 {
		t.Fatalf("settled = %+v, closes %d", settled, conv.Closes())
	}
	if f := nextSession(t, sub); f.Stage != StageSettled || !f.SettledAt.Equal(settledAt) || !f.UpdatedAt.Equal(settledAt) {
		t.Fatalf("settle frame = %+v", f)
	}
	if rec, _ := loadRecord(t, st, "fake", sum.ID); rec.Web.Stage != StageSettled || !rec.Web.SettledAt.Equal(settledAt) || !rec.Web.UpdatedAt.Equal(settledAt) {
		t.Fatalf("stored settle = %+v", rec.Web)
	}
	wantConflict(t, "settle a settled task", errOf(m.Settle(sum.ID)), "a task that is settled cannot be settled")

	reopenedAt := settledAt.Add(time.Hour)
	setNow(m, reopenedAt)
	reopened, err := m.Reopen(sum.ID)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if reopened.Stage != StageActive || !reopened.SettledAt.IsZero() || reopened.Open || len(prov.Opens()) != 1 {
		t.Fatalf("reopened = %+v, opens %d", reopened, len(prov.Opens()))
	}
	if f := nextSession(t, sub); f.Stage != StageActive || !f.SettledAt.IsZero() || !f.UpdatedAt.Equal(reopenedAt) {
		t.Fatalf("reopen frame = %+v", f)
	}
	if rec, _ := loadRecord(t, st, "fake", sum.ID); rec.Web.Stage != "" || !rec.Web.SettledAt.IsZero() {
		t.Fatalf("stored reopen = %+v", rec.Web)
	}
	wantConflict(t, "reopen an active task", errOf(m.Reopen(sum.ID)), "a task that is active cannot be reopened")

	// The next prompt reopens the same conversation.
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "more", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatalf("prompt after reopen: %v", err)
	}
	opens := prov.Opens()
	if len(opens) != 2 || opens[1].ConversationID != sum.ConversationID || len(prov.Last().Sends()) != 1 {
		t.Fatalf("opens after reopen = %+v", opens)
	}
	prov.Last().EmitTurn(agentapi.TurnCompleted, "")

	if _, err := m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	archivedAt := reopenedAt.Add(time.Hour)
	setNow(m, archivedAt)
	archived, err := m.Archive(sum.ID)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived.Stage != StageArchived || !archived.SettledAt.Equal(reopenedAt) || !archived.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("archived = %+v", archived)
	}
	f := nextSession(t, sub)
	for f.Stage != StageArchived {
		f = nextSession(t, sub)
	}
	if !f.ArchivedAt.Equal(archivedAt) || !f.UpdatedAt.Equal(archivedAt) {
		t.Fatalf("archive frame = %+v", f)
	}
	for name, move := range map[string]func(string) (SessionSummary, error){
		"settled": m.Settle, "reopened": m.Reopen, "archived": m.Archive,
	} {
		wantConflict(t, name+" an archived task", errOf(move(sum.ID)), "a task that is archived cannot be "+name)
	}
	for _, move := range []func(string) (SessionSummary, error){m.Settle, m.Reopen, m.Archive} {
		if _, err := move("missing"); statusOf(err) != http.StatusNotFound {
			t.Fatalf("stage change of an unknown task = %v, want 404", err)
		}
	}
}

func errOf(_ SessionSummary, err error) error { return err }

// Any Task can be archived: an active one is archived without being settled.
func TestArchiveActiveTaskLeavesSettledAtEmpty(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	setNow(m, at)
	archived, err := m.Archive(sum.ID)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived.Stage != StageArchived || !archived.SettledAt.IsZero() || !archived.ArchivedAt.Equal(at) || archived.Open || conv.Closes() != 1 {
		t.Fatalf("archived = %+v, closes %d", archived, conv.Closes())
	}
	rec, _ := loadRecord(t, st, "fake", sum.ID)
	if rec.Web.Stage != StageArchived || !rec.Web.SettledAt.IsZero() || !rec.Web.ArchivedAt.Equal(at) {
		t.Fatalf("stored = %+v", rec.Web)
	}
}

// Settling, and archiving an active Task, wait for the turn, the queue and
// every interaction.
func TestSettleAndArchivePreconditions(t *testing.T) {
	for name, move := range map[string]func(*Manager, string) (SessionSummary, error){
		"settle": (*Manager).Settle, "archive": (*Manager).Archive,
	} {
		t.Run(name, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			refused := func(what, id, message string) {
				t.Helper()
				wantConflict(t, name+" "+what, errOf(move(m, id)), message)
				if s, _ := m.Summary(id); s.Stage != StageActive {
					t.Fatalf("%s %s changed the stage to %q", name, what, s.Stage)
				}
			}
			sum, conv := createSession(t, m, prov)
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
				t.Fatal(err)
			}
			refused("while working", sum.ID, "stop the turn first")
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "later", RequestID: mustUUID(t), Mode: ModeQueue}); err != nil {
				t.Fatal(err)
			}
			conv.EmitTurn(agentapi.TurnCancelled, "") // pauses the queue
			refused("with queued prompts", sum.ID, "queued prompts")
			if err := m.ClearQueue(sum.ID); err != nil {
				t.Fatal(err)
			}
			conv.EmitInteraction(permissionRequest("p1"))
			refused("awaiting permission", sum.ID, "stop the turn first")
			if _, err := m.Answer(sum.ID, "p1", agentapi.Answer{Decision: "deny"}); err != nil {
				t.Fatal(err)
			}
			conv.EmitInteraction(question("q1"))
			refused("awaiting an answer", sum.ID, "stop the turn first")
			if conv.Closes() != 0 {
				t.Fatalf("a refused %s closed the conversation", name)
			}
			if _, err := m.Answer(sum.ID, "q1", agentapi.Answer{Reject: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := move(m, sum.ID); err != nil {
				t.Fatalf("%s once idle: %v", name, err)
			}
			if conv.Closes() != 1 {
				t.Fatalf("closes = %d", conv.Closes())
			}

			// A yolo approval on its way to the provider is still pending.
			yolo, yconv := createTask(t, m, prov, "yolo")
			release := make(chan struct{})
			yconv.SetRespondHook(func(context.Context, string, agentapi.Answer) error { <-release; return nil })
			yconv.EmitTurn(agentapi.TurnWorking, "")
			yconv.EmitInteraction(onceRequest("p2", ""))
			yconv.EmitTurn(agentapi.TurnCompleted, "")
			refused("while a yolo approval is sent", yolo.ID, "still being answered")
			close(release)
			waitAllowed(t, m, yolo.ID, "p2")
			if _, err := move(m, yolo.ID); err != nil {
				t.Fatalf("%s after the approval: %v", name, err)
			}
		})
	}
}

func TestSettledAndArchivedTasksAreReadOnly(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	rid := mustUUID(t)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: rid, Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if _, err := m.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	// A retried request keeps its recorded outcome.
	if sub, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: rid, Mode: ModeSend}); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("retry of a recorded prompt = %+v, %v", sub, err)
	}

	checks := func(stage, message string) {
		t.Helper()
		for _, mode := range []string{ModeSend, ModeQueue, ModeSteer} {
			wantConflict(t, stage+" prompt "+mode, errOf2(m.Submit(sum.ID, PromptRequest{Text: "x", RequestID: mustUUID(t), Mode: mode})), message)
		}
		wantConflict(t, stage+" clear queue", m.ClearQueue(sum.ID), message)
		wantConflict(t, stage+" resume queue", m.ResumeQueue(sum.ID), message)
		wantConflict(t, stage+" cancel queued", m.CancelQueued(sum.ID, mustUUID(t)), message)
		wantConflict(t, stage+" model", errOf(m.SetModel(sum.ID, setting("b"), nil, nil)), message)
		wantConflict(t, stage+" mode", errOf(m.SetMode(sum.ID, "yolo")), message)
		if err := m.View(context.Background(), sum.ID); err != nil {
			t.Fatal(err)
		}
		if d := detail(t, m, sum.ID); d.Open || d.Mode != string(store.ModeSafe) || len(prov.Opens()) != 1 || len(conv.Sends()) != 1 || len(conv.Steers()) != 0 {
			t.Fatalf("%s task reached the provider: open %v, opens %d, sends %d", stage, d.Open, len(prov.Opens()), len(conv.Sends()))
		}
	}
	checks("settled", "the task is settled; reopen it first")
	if renamed, err := m.Rename(sum.ID, "done"); err != nil || renamed.Name != "done" {
		t.Fatalf("rename a settled task = %+v, %v", renamed, err)
	}
	if _, err := m.Archive(sum.ID); err != nil {
		t.Fatal(err)
	}
	checks("archived", "the task is archived")
	wantConflict(t, "rename an archived task", errOf(m.Rename(sum.ID, "again")), "the task is archived")
}

func errOf2(_ Submission, err error) error { return err }

func TestStagesSurviveRestart(t *testing.T) {
	st := openTestStore(t)
	prov := agenttest.NewProvider("fake", allCaps)
	m := startManager(t, st, prov)
	settled, _ := createSession(t, m, prov)
	archived, _ := createSession(t, m, prov)
	active, _ := createSession(t, m, prov)
	if _, err := m.Settle(settled.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Archive(archived.ID); err != nil {
		t.Fatal(err)
	}
	want := map[string]SessionSummary{}
	for _, s := range m.List() {
		want[s.ID] = s
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Records written before stages existed, or with a stage this version
	// does not know, are active.
	legacy, unknown := mustUUID(t), mustUUID(t)
	now := time.Now().UTC()
	seedLegacyWebRecord(t, st, legacy, t.TempDir(), &store.WebState{Turn: StateCompleted, UpdatedAt: now})
	seedLegacyWebRecord(t, st, unknown, t.TempDir(), &store.WebState{Turn: StateCompleted, UpdatedAt: now, Stage: "frozen", SettledAt: now})

	prov2 := agenttest.NewProvider("fake", allCaps)
	for _, s := range want {
		prov2.AddConversation(s.ConversationID, nil)
	}
	m2 := startManager(t, st, prov2)
	for id, w := range want {
		got := detail(t, m2, id)
		if got.Stage != w.Stage || !got.SettledAt.Equal(w.SettledAt) || !got.ArchivedAt.Equal(w.ArchivedAt) || !got.UpdatedAt.Equal(w.UpdatedAt) {
			t.Fatalf("after restart %s = stage %q settled %s archived %s updated %s, want %+v", id, got.Stage, got.SettledAt, got.ArchivedAt, got.UpdatedAt, w)
		}
	}
	if s := detail(t, m2, settled.ID); s.Stage != StageSettled || s.SettledAt.IsZero() {
		t.Fatalf("settled after restart = %+v", s.SessionSummary)
	}
	if s := detail(t, m2, archived.ID); s.Stage != StageArchived || s.ArchivedAt.IsZero() || !s.SettledAt.IsZero() {
		t.Fatalf("archived after restart = %+v", s.SessionSummary)
	}
	for _, id := range []string{legacy, unknown, active.ID} {
		if s := detail(t, m2, id); s.Stage != StageActive || !s.SettledAt.IsZero() {
			t.Fatalf("task %s after restart = stage %q settled %s, want active", id, s.Stage, s.SettledAt)
		}
	}
	// Viewing a settled or archived Task leaves its conversation closed, also
	// for one settled while its conversation was not open.
	if s, err := m2.Settle(active.ID); err != nil || s.State == StateClosed {
		t.Fatalf("settle a task that is not open = %+v, %v", s, err)
	}
	for _, id := range []string{settled.ID, archived.ID, active.ID} {
		if err := m2.View(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if len(prov2.Opens()) != 0 {
		t.Fatalf("viewing opened %d conversations", len(prov2.Opens()))
	}
	if _, err := m2.Reopen(settled.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Submit(settled.ID, PromptRequest{Text: "again", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	if opens := prov2.Opens(); len(opens) != 1 || opens[0].ConversationID != settled.ConversationID {
		t.Fatalf("opens = %+v", opens)
	}
	if err := m2.Delete(archived.ID); err != nil {
		t.Fatalf("delete an archived task after restart: %v", err)
	}
}

func TestRemoveEmptyProject(t *testing.T) {
	m, _, _ := newTestManager(t)
	project := addProject(t, m, t.TempDir())
	if err := m.RemoveProject(project); err != nil {
		t.Fatalf("remove a project without tasks: %v", err)
	}
}

func TestLifecycleRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, conv := createSession(t, ts.m, ts.prov)
	base := "/api/sessions/" + sum.ID
	post := func(path string) (int, string) {
		w := ts.do(http.MethodPost, base+path, "", auth)
		return w.Code, w.Body.String()
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if code, body := post("/settle"); code != http.StatusConflict || !strings.Contains(body, "stop the turn first") {
		t.Fatalf("settle while working = %d %s", code, body)
	}
	if code, body := post("/archive"); code != http.StatusConflict || !strings.Contains(body, "stop the turn first") {
		t.Fatalf("archive while working = %d %s", code, body)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if code, _ := post("/reopen"); code != http.StatusConflict {
		t.Fatalf("reopen an active task = %d, want 409", code)
	}
	if code, body := post("/settle"); code != http.StatusOK || !strings.Contains(body, `"stage":"settled"`) || !strings.Contains(body, `"settled_at":`) || strings.Contains(body, `"archived_at"`) {
		t.Fatalf("settle = %d %s", code, body)
	}
	prompt := `{"text":"x","request_id":"` + mustUUID(t) + `","mode":"queue"}`
	if w := ts.do(http.MethodPost, base+"/prompt", prompt, auth); w.Code != http.StatusConflict {
		t.Fatalf("prompt a settled task = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, base+"/queue/clear", "", auth); w.Code != http.StatusConflict {
		t.Fatalf("clear the queue of a settled task = %d", w.Code)
	}
	for body, want := range map[string]int{`{"name":"done"}`: http.StatusOK, `{"mode":"yolo"}`: http.StatusConflict} {
		if w := ts.do(http.MethodPatch, base, body, auth); w.Code != want {
			t.Fatalf("PATCH settled %s = %d, want %d", body, w.Code, want)
		}
	}
	if code, body := post("/reopen"); code != http.StatusOK || strings.Contains(body, `"stage"`) || strings.Contains(body, `"settled_at"`) {
		t.Fatalf("reopen = %d %s", code, body)
	}
	if code, body := post("/archive"); code != http.StatusOK || !strings.Contains(body, `"stage":"archived"`) || strings.Contains(body, `"settled_at"`) {
		t.Fatalf("archive an active task = %d %s", code, body)
	}
	for _, path := range []string{"/settle", "/reopen", "/archive"} {
		if code, _ := post(path); code != http.StatusConflict {
			t.Fatalf("%s an archived task = %d, want 409", path, code)
		}
		if w := ts.do(http.MethodPost, "/api/sessions/missing"+path, "", auth); w.Code != http.StatusNotFound {
			t.Fatalf("%s an unknown task = %d, want 404", path, w.Code)
		}
	}
	if w := ts.do(http.MethodPatch, base, `{"name":"x"}`, auth); w.Code != http.StatusConflict {
		t.Fatalf("rename an archived task = %d", w.Code)
	}
	if w := ts.do(http.MethodDelete, base, "", auth); w.Code != http.StatusNoContent {
		t.Fatalf("delete an archived task = %d %s", w.Code, w.Body)
	}
}

// An open conversation left idle and unviewed for idleClose is closed without
// changing the Task; the next prompt reopens it as after a restart.
func TestIdleConversationClosesAndReopensOnPrompt(t *testing.T) {
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels([]agentapi.Model{{ID: "a", Name: "A"}}, nil)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, t.TempDir()), Model: "a"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	if _, err := m.SetMode(sum.ID, "yolo"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	reply := agentapi.Item{ID: "a1", Kind: agentapi.ItemAssistant, Text: "done"}
	conv.EmitItem(reply)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	prov.SetHistory(conv.ID(), agentapi.History{Items: []agentapi.Item{reply}})
	viewer, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	left := time.Now().Add(time.Hour)
	setNow(m, left.Add(-time.Hour+idleClose))
	m.closeIdleConversations()
	setNow(m, left)
	m.Unsubscribe(viewer)
	setNow(m, left.Add(idleClose-time.Second))
	m.closeIdleConversations()
	if conv.Closes() != 0 {
		t.Fatal("closed a viewed conversation or one idle for less than idleClose")
	}

	setNow(m, left.Add(idleClose))
	m.closeIdleConversations()
	d := detail(t, m, sum.ID)
	if conv.Closes() != 1 || d.Open || d.State != StateCompleted || d.Model != "a" || d.Mode != "yolo" {
		t.Fatalf("after idle close: closes %d, detail %+v", conv.Closes(), d.SessionSummary)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	if rec, _ := loadRecord(t, st, "fake", sum.ID); rec.Web.Turn != StateCompleted {
		t.Fatalf("stored state after idle close = %q", rec.Web.Turn)
	}

	if _, err := m.Submit(sum.ID, PromptRequest{Text: "more", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	next := prov.Last()
	opens := prov.Opens()
	if next == conv || len(opens) != 2 || opens[1].ConversationID != sum.ConversationID || len(next.Sends()) != 1 || strings.Join(next.ModelSets(), ",") != "a" {
		t.Fatalf("reopen: opens %+v, sends %v, models %v", opens, next.Sends(), next.ModelSets())
	}
	d = detail(t, m, sum.ID)
	if !d.Open || d.Mode != "yolo" || d.Model != "a" || len(d.Items) == 0 || d.Items[0].ID != "a1" {
		t.Fatalf("reopened detail = %+v", d)
	}
}

// Work, a pending answer or a viewer keeps an open conversation open however
// long it has been.
func TestBusyOrWatchedConversationStaysOpen(t *testing.T) {
	for name, keep := range map[string]func(*Manager, string, *agenttest.Conversation){
		"turn": func(_ *Manager, _ string, c *agenttest.Conversation) { c.EmitTurn(agentapi.TurnWorking, "") },
		"queue": func(m *Manager, id string, _ *agenttest.Conversation) {
			m.mu.Lock()
			m.sessions[id].queue = []QueuedPrompt{{RequestID: "q"}}
			m.mu.Unlock()
		},
		"permission": func(_ *Manager, _ string, c *agenttest.Conversation) { c.EmitInteraction(permissionRequest("p1")) },
		"subagent": func(_ *Manager, _ string, c *agenttest.Conversation) {
			c.EmitSubagent(agentapi.Subagent{ID: "sa", Status: agentapi.SubagentRunning})
		},
		"shell": func(_ *Manager, _ string, c *agenttest.Conversation) {
			c.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "t", Status: "running"}}}})
		},
		"unknown shells": func(_ *Manager, _ string, c *agenttest.Conversation) {
			c.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{}})
		},
		"objective": func(_ *Manager, _ string, c *agenttest.Conversation) {
			c.Emit(agentapi.Event{Kind: agentapi.EventExecution, Execution: &agentapi.ExecutionState{Known: true, Mode: "autopilot", Objective: &agentapi.AutopilotObjective{Status: "active"}}})
		},
		"viewer": func(m *Manager, id string, _ *agenttest.Conversation) {
			if _, _, err := m.Subscribe(id); err != nil {
				panic(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			sum, conv := createSession(t, m, prov)
			m.closeIdleConversations() // first seen now
			keep(m, sum.ID, conv)
			setNow(m, time.Now().Add(24*time.Hour))
			m.closeIdleConversations()
			if s, _ := m.Summary(sum.ID); conv.Closes() != 0 || !s.Open {
				t.Fatalf("closed a busy or watched conversation: %+v", s)
			}
		})
	}
}
