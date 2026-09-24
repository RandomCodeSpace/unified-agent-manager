package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type typedConversation struct {
	agentapi.Conversation
	execute func(context.Context, string, agentapi.Prompt) (*agentapi.CommandResult, error)
	shell   func(context.Context, string) (agentapi.BackgroundTasks, error)
}

func (c *typedConversation) ExecuteCommand(ctx context.Context, name string, prompt agentapi.Prompt) (*agentapi.CommandResult, error) {
	return c.execute(ctx, name, prompt)
}
func (c *typedConversation) CancelBackgroundTask(ctx context.Context, id string) (agentapi.BackgroundTasks, error) {
	return c.shell(ctx, id)
}
func commandManager(t *testing.T) (*Manager, *agenttest.Provider, *store.Store, SessionSummary, *agenttest.Conversation, *typedConversation) {
	t.Helper()
	caps := allCaps
	caps.Import = true
	prov := agenttest.NewProvider(agentapi.ProviderCopilot, caps)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	sum, conv := createSession(t, m, prov)
	wrapped := &typedConversation{Conversation: conv}
	m.mu.Lock()
	m.sessions[sum.ID].conv = wrapped
	m.mu.Unlock()
	prov.SetCommands([]agentapi.Command{{Name: "autopilot", Aliases: []string{"goal"}, AllowDuringTurn: true}, {Name: "allow-all", Aliases: []string{"yolo"}, AllowDuringTurn: true}, {Name: "review"}, {Name: "model", AllowDuringTurn: true}, {Name: "plan", DisabledReason: "Plan exit approval unavailable"}}, nil)
	return m, prov, st, sum, conv, wrapped
}

func TestCommandRuntimeDurableReservationReplayAndNoTurn(t *testing.T) {
	m, prov, st, sum, conv, wrapped := commandManager(t)
	var calls atomic.Int32
	wrapped.execute = func(_ context.Context, name string, _ agentapi.Prompt) (*agentapi.CommandResult, error) {
		calls.Add(1)
		if name != "autopilot" {
			t.Errorf("alias was not normalized: %s", name)
		}
		cfg, err := st.Load()
		if err != nil {
			t.Fatal(err)
		}
		web := cfg.Sessions[store.Key(prov.Name(), sum.ID)].Web
		if web.RequestStatus != SubmissionUncertain || len(web.CommandSubmissions) == 0 {
			t.Fatalf("invoked before durable reservation: %+v", web)
		}
		return &agentapi.CommandResult{Kind: "text", Text: "autopilot status"}, nil
	}
	first := CommandRequest{RequestID: mustUUID(t), Name: "goal"}
	sub, err := m.Command(sum.ID, first)
	if err != nil || sub.Status != SubmissionAccepted || sub.CommandResult == nil {
		t.Fatalf("command=%+v %v", sub, err)
	}
	if state := detail(t, m, sum.ID).State; state != StateIdle {
		t.Fatalf("nonprompt Working: %s", state)
	}
	second := CommandRequest{RequestID: mustUUID(t), Name: "autopilot"}
	if _, err = m.Command(sum.ID, second); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Command(sum.ID, first); err != nil || calls.Load() != 2 {
		t.Fatalf("duplicate invoked: %v calls=%d", err, calls.Load())
	}
	if len(conv.Sends()) != 0 {
		t.Fatal("nonprompt command sent prompt")
	}
	if err = m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := startManager(t, st, prov)
	replay, err := restarted.Command(sum.ID, first)
	if err != nil || replay.Status != SubmissionAccepted || replay.CommandResult == nil || replay.CommandResult.Text != "autopilot status" || calls.Load() != 2 {
		t.Fatalf("restart replay=%+v %v calls=%d", replay, err, calls.Load())
	}
}

func TestCommandRuntimeAliasesPermissionBoundaryAndBusy(t *testing.T) {
	m, _, _, sum, conv, wrapped := commandManager(t)
	var calls atomic.Int32
	wrapped.execute = func(context.Context, string, agentapi.Prompt) (*agentapi.CommandResult, error) {
		calls.Add(1)
		return &agentapi.CommandResult{Kind: "completed"}, nil
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(agentapi.Event{Kind: agentapi.EventExecution, Execution: &agentapi.ExecutionState{Known: true, Mode: "autopilot"}})
	ix := onceRequest("pending", "")
	conv.EmitInteraction(ix)
	if _, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "goal", Arguments: "on"}); err != nil {
		t.Fatal(err)
	}
	current := detail(t, m, sum.ID)
	if current.Mode != "safe" || current.Pending != 1 || current.Execution == nil || current.Execution.Mode != "autopilot" {
		t.Fatalf("autopilot changed permissions: %+v", current)
	}
	for _, name := range []string{"review", "plan", "unknown"} {
		if _, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: name}); err == nil {
			t.Fatalf("unsupported/busy %s accepted", name)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("invalid command invoked provider")
	}
	show, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "yolo", Arguments: "show"})
	if err != nil || show.CommandResult.Text != "Permission mode: safe" {
		t.Fatalf("permission show=%+v %v", show, err)
	}
	off, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "yolo", Arguments: "off"})
	if err != nil || off.Status != SubmissionAccepted || detail(t, m, sum.ID).Pending != 1 {
		t.Fatalf("permission off affected pending request: %+v %v", off, err)
	}
	if _, err = m.Cancel(sum.ID); err != nil {
		t.Fatal(err)
	}
	if conv.Cancels() != 1 || detail(t, m, sum.ID).Pending != 1 {
		t.Fatal("Stop approved a permission")
	}
}

func TestCommandRuntimeUncertainOutcomeIsNeverReinvoked(t *testing.T) {
	m, _, _, sum, _, wrapped := commandManager(t)
	var calls atomic.Int32
	wrapped.execute = func(context.Context, string, agentapi.Prompt) (*agentapi.CommandResult, error) {
		calls.Add(1)
		return nil, errors.Join(agentapi.ErrSubmissionUncertain, errors.New("connection lost"))
	}
	req := CommandRequest{RequestID: mustUUID(t), Name: "autopilot"}
	for i := 0; i < 2; i++ {
		sub, err := m.Command(sum.ID, req)
		if err != nil || sub.Status != SubmissionUncertain {
			t.Fatalf("uncertain=%+v %v", sub, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("ambiguous invocation repeated %d times", calls.Load())
	}
}

func TestCommandRuntimeClosedCatalogOpensExactConversation(t *testing.T) {
	m, prov, _, sum, _, _ := commandManager(t)
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	before := len(prov.Opens())
	if _, err := m.Commands(context.Background(), sum.ID); err != nil {
		t.Fatal(err)
	}
	opens := prov.Opens()
	if len(opens) != before+1 || opens[len(opens)-1].ConversationID != sum.ConversationID || len(prov.Last().Sends()) != 0 {
		t.Fatalf("catalog reopened wrong conversation: %+v", opens)
	}
}

func TestBackgroundShellCancelRouteIsolatesTargetAndTruthfulSnapshot(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	sum, conv := createSession(t, ts.m, ts.prov)
	auth := withCookie(ts)
	snapshot := agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "server", Status: "running", Command: "server"}, {ID: "other", Status: "running", Command: "other"}}}
	conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &snapshot})
	conv.EmitTurn(agentapi.TurnWorking, "")
	var calls atomic.Int32
	wrapped := &typedConversation{Conversation: conv, shell: func(_ context.Context, id string) (agentapi.BackgroundTasks, error) {
		calls.Add(1)
		if id != "server" {
			t.Fatalf("wrong shell: %s", id)
		}
		return snapshot, nil
	}}
	ts.m.mu.Lock()
	ts.m.sessions[sum.ID].conv = wrapped
	ts.m.mu.Unlock()
	path := "/api/sessions/" + sum.ID + "/background-tasks/server/cancel"
	if w := ts.do(http.MethodPost, path, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", w.Code)
	}
	if w := ts.do(http.MethodPost, path, "", auth, withHeader("Sec-Fetch-Site", "cross-site")); w.Code != http.StatusForbidden {
		t.Fatalf("crosssite=%d", w.Code)
	}
	w := ts.do(http.MethodPost, path, "", auth)
	var result BackgroundTaskCancellation
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || !result.Accepted || result.BackgroundTasks.Tasks[0].Status != "running" {
		t.Fatalf("pending cancel=%d %s %v", w.Code, w.Body, err)
	}
	if conv.Cancels() != 0 || detail(t, ts.m, sum.ID).State != StateWorking {
		t.Fatal("shell stop aborted foreground")
	}
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/background-tasks/agent/cancel", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("non-shell=%d", w.Code)
	}
	snapshot.Known = false
	conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &snapshot})
	if w := ts.do(http.MethodPost, path, "", auth); w.Code != http.StatusConflict || calls.Load() != 1 {
		t.Fatalf("unknown state=%d calls=%d", w.Code, calls.Load())
	}
}

func TestCommandRuntimeStopPausesQueuedFollowup(t *testing.T) {
	m, _, _, sum, conv, _ := commandManager(t)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.Emit(agentapi.Event{Kind: agentapi.EventExecution, Execution: &agentapi.ExecutionState{Known: true, Mode: "autopilot"}})
	mustSubmit(t, m, sum.ID, "followup", mustUUID(t), ModeQueue, SubmissionQueued)
	if _, err := m.Cancel(sum.ID); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	current := detail(t, m, sum.ID)
	if !current.QueuePaused || len(current.Queue) != 1 || len(conv.Sends()) != 0 {
		t.Fatalf("Stop drained queued followup: %+v sends=%v", current.Queue, conv.Sends())
	}
}

func TestCommandRuntimeFailedReservationNeverInvokes(t *testing.T) {
	m, _, st, sum, _, wrapped := commandManager(t)
	var calls atomic.Int32
	wrapped.execute = func(context.Context, string, agentapi.Prompt) (*agentapi.CommandResult, error) {
		calls.Add(1)
		return &agentapi.CommandResult{Kind: "completed"}, nil
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	backup := st.Path() + ".saved"
	if err := os.Rename(st.Path(), backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(st.Path(), 0700); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(st.Path())
		if err := os.Rename(backup, st.Path()); err != nil {
			t.Error(err)
		}
	}()
	if _, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "autopilot"}); err == nil || calls.Load() != 0 {
		t.Fatalf("failed persistence invoked: %v calls=%d", err, calls.Load())
	}
}

func TestCommandRuntimeHeldImportedConversationNeverInvokesOrReopens(t *testing.T) {
	m, prov, _, sum, _, wrapped := commandManager(t)
	var calls atomic.Int32
	wrapped.execute = func(context.Context, string, agentapi.Prompt) (*agentapi.CommandResult, error) {
		calls.Add(1)
		return &agentapi.CommandResult{Kind: "completed"}, nil
	}
	m.mu.Lock()
	m.sessions[sum.ID].imported = true
	m.mu.Unlock()
	prov.SetInUse([]string{sum.ConversationID}, nil)
	if _, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "autopilot"}); !errors.Is(err, errHeldElsewhere) || calls.Load() != 0 {
		t.Fatalf("held invoke=%v calls=%d", err, calls.Load())
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	opens := len(prov.Opens())
	if _, err := m.Commands(context.Background(), sum.ID); !errors.Is(err, errHeldElsewhere) || len(prov.Opens()) != opens {
		t.Fatalf("held catalog=%v opens=%d want=%d", err, len(prov.Opens()), opens)
	}
}

func TestCommandRuntimeStopInvalidatesInFlightHolderCheck(t *testing.T) {
	m, prov, _, sum, conv, wrapped := commandManager(t)
	var calls atomic.Int32
	wrapped.execute = func(context.Context, string, agentapi.Prompt) (*agentapi.CommandResult, error) {
		calls.Add(1)
		return &agentapi.CommandResult{Kind: "completed"}, nil
	}
	m.mu.Lock()
	m.sessions[sum.ID].imported = true
	m.mu.Unlock()
	conv.EmitTurn(agentapi.TurnWorking, "")
	started, release := make(chan struct{}), make(chan struct{})
	prov.SetInUseHook(func(context.Context, []string) ([]string, error) { close(started); <-release; return nil, nil })
	result := make(chan error, 1)
	go func() {
		_, err := m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "autopilot", Arguments: "on"})
		result <- err
	}()
	<-started
	if _, err := m.Cancel(sum.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; statusOf(err) != http.StatusConflict || calls.Load() != 0 || conv.Cancels() != 1 {
		t.Fatalf("stale command=%v calls=%d cancels=%d", err, calls.Load(), conv.Cancels())
	}
}

func TestCommandRuntimeCloseMakesExecutionObservationUnknown(t *testing.T) {
	m, _, _, sum, conv, _ := commandManager(t)
	conv.Emit(agentapi.Event{Kind: agentapi.EventExecution, Execution: &agentapi.ExecutionState{Known: true, Mode: "autopilot", Objective: &agentapi.AutopilotObjective{ID: 1, Objective: "objective", Status: "paused", TurnCount: 2}}})
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	current := detail(t, m, sum.ID)
	if current.Execution == nil || current.Execution.Known || current.Execution.Objective.Status != "paused" || current.Mode != "safe" {
		t.Fatalf("close fabricated runtime or permission state: %+v", current.Execution)
	}
}
