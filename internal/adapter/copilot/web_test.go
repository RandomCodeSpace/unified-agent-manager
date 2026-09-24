package copilot

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type fakeClient struct {
	mu        sync.Mutex
	started   int
	stopped   int
	forced    int
	startErr  error
	createErr error
	pingErr   error
	resumeErr error
	models    []rpc.Model
	modelsErr error
	sessions  []*fakeSession
	create    []*copilot.SessionConfig
	resume    []*copilot.ResumeSessionConfig
}

func (f *fakeClient) ListModels(context.Context) ([]rpc.Model, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.models, f.modelsErr
}

func (f *fakeClient) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	return f.startErr
}
func (f *fakeClient) Stop() error { f.mu.Lock(); f.stopped++; f.mu.Unlock(); return nil }
func (f *fakeClient) ForceStop()  { f.mu.Lock(); f.forced++; f.mu.Unlock() }

func (f *fakeClient) Ping(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pingErr
}

func (f *fakeClient) CreateSession(_ context.Context, cfg *copilot.SessionConfig) (sdkSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	s := &fakeSession{id: cfg.SessionID, onEvent: cfg.OnEvent, askUser: cfg.OnUserInputRequest, perm: cfg.OnPermissionRequest}
	f.create = append(f.create, cfg)
	f.sessions = append(f.sessions, s)
	return s, nil
}

func (f *fakeClient) ResumeSession(_ context.Context, id string, cfg *copilot.ResumeSessionConfig) (sdkSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resumeErr != nil {
		return nil, f.resumeErr
	}
	s := &fakeSession{id: id, onEvent: cfg.OnEvent, askUser: cfg.OnUserInputRequest, perm: cfg.OnPermissionRequest}
	f.resume = append(f.resume, cfg)
	f.sessions = append(f.sessions, s)
	return s, nil
}

func (f *fakeClient) counts() (started, forced int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.forced
}

type fakeSession struct {
	id      string
	onEvent copilot.SessionEventHandler
	askUser copilot.UserInputHandler
	perm    copilot.PermissionHandlerFunc

	mu      sync.Mutex
	sent    []string
	modes   []string
	sendErr error
	// beforeReturn runs with the assigned message ID before Send returns it,
	// as CLI events can be handled before the send response.
	beforeReturn      func(id string)
	models            []string
	modelErr          error
	modelRequests     []*rpc.ModelSwitchToRequest
	modelResult       *rpc.ModelSwitchToResult
	effortResets      int
	effortErr         error
	events            []copilot.SessionEvent
	answers           map[string]rpc.PermissionDecision
	notPending        map[string]bool
	disconnected      bool
	subCancels        []string
	subCancelErr      error
	subCancelRejected bool
	abortErr          error
	aborts            int
	respondHook       func(context.Context, string, rpc.PermissionDecision) (bool, error)
	eventsErr         error
	disconnectHook    func() error
}

func (s *fakeSession) CancelSubagent(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subCancels = append(s.subCancels, id)
	return !s.subCancelRejected, s.subCancelErr
}

func (s *fakeSession) ID() string { return s.id }
func (s *fakeSession) Abort(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aborts++
	return s.abortErr
}

func (s *fakeSession) Send(_ context.Context, prompt, mode string) (string, error) {
	s.mu.Lock()
	if s.sendErr != nil {
		defer s.mu.Unlock()
		return "", s.sendErr
	}
	s.sent = append(s.sent, prompt)
	s.modes = append(s.modes, mode)
	id := fmt.Sprintf("msg-%d", len(s.sent))
	hook := s.beforeReturn
	s.mu.Unlock()
	if hook != nil {
		hook(id)
	}
	return id, nil
}

func (s *fakeSession) SwitchModel(_ context.Context, req *rpc.ModelSwitchToRequest) (*rpc.ModelSwitchToResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelRequests = append(s.modelRequests, req)
	if s.modelErr != nil {
		return nil, s.modelErr
	}
	s.models = append(s.models, req.ModelID)
	if s.modelResult != nil {
		return s.modelResult, nil
	}
	status := "applied"
	return &rpc.ModelSwitchToResult{Status: &status, ModelID: &req.ModelID, ModelState: &rpc.CurrentModel{ModelID: &req.ModelID, ReasoningEffort: req.ReasoningEffort, ContextTier: req.ContextTier}}, nil
}

func (s *fakeSession) SetEffort(_ context.Context, effort string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if effort != "" {
		return fmt.Errorf("unexpected reset effort %q", effort)
	}
	s.effortResets++
	return s.effortErr
}

func (s *fakeSession) Events(context.Context) ([]copilot.SessionEvent, error) {
	return s.events, s.eventsErr
}

func (s *fakeSession) RespondPermission(ctx context.Context, id string, d rpc.PermissionDecision) (bool, error) {
	s.mu.Lock()
	if s.answers == nil {
		s.answers = map[string]rpc.PermissionDecision{}
	}
	s.answers[id] = d
	hook, applied := s.respondHook, !s.notPending[id]
	s.mu.Unlock()
	if hook != nil {
		return hook(ctx, id, d)
	}
	return applied, nil
}

func (s *fakeSession) Disconnect() error {
	s.mu.Lock()
	s.disconnected = true
	hook := s.disconnectHook
	s.mu.Unlock()
	if hook != nil {
		return hook()
	}
	return nil
}

func (s *fakeSession) answer(id string) rpc.PermissionDecision {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.answers[id]
}

type recSink struct {
	mu  sync.Mutex
	evs []agentapi.Event
}

func (r *recSink) Emit(e agentapi.Event) {
	r.mu.Lock()
	r.evs = append(r.evs, e)
	r.mu.Unlock()
}

func (r *recSink) all() []agentapi.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agentapi.Event(nil), r.evs...)
}

func (r *recSink) last() agentapi.Event {
	evs := r.all()
	if len(evs) == 0 {
		return agentapi.Event{}
	}
	return evs[len(evs)-1]
}

// interaction returns the latest emitted state of interaction id.
func (r *recSink) interaction(id string) *agentapi.Interaction {
	var got *agentapi.Interaction
	for _, e := range r.all() {
		if e.Kind == agentapi.EventInteraction && e.Interaction.ID == id {
			got = e.Interaction
		}
	}
	return got
}

func (r *recSink) question() *agentapi.Interaction {
	var got *agentapi.Interaction
	for _, e := range r.all() {
		if e.Kind == agentapi.EventInteraction && e.Interaction.Kind == agentapi.InteractionQuestion {
			got = e.Interaction
		}
	}
	return got
}

func ev(id string, data rpc.SessionEventData) copilot.SessionEvent {
	return copilot.SessionEvent{ID: id, Timestamp: time.Unix(100, 0), Data: data}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

type webHarness struct {
	p    *webProvider
	fc   *fakeClient
	fs   *fakeSession
	conv agentapi.Conversation
	sink *recSink
}

func openWeb(t *testing.T) webHarness {
	t.Helper()
	fc := &fakeClient{}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	sink := &recSink{}
	conv, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-1", Workdir: "/work", Events: sink})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return webHarness{p: p, fc: fc, fs: fc.sessions[0], conv: conv, sink: sink}
}

func TestWebOpenCreatesStreamingSessionWithoutApproveAll(t *testing.T) {
	h := openWeb(t)
	cfg := h.fc.create[0]
	if h.conv.ID() != "s-1" || cfg.SessionID != "s-1" || cfg.WorkingDirectory != "/work" || cfg.Streaming == nil || !*cfg.Streaming {
		t.Fatalf("create config = %+v, id %q", cfg, h.conv.ID())
	}
	if cfg.Model != "" || cfg.OnUserInputRequest == nil {
		t.Fatalf("model %q / question handler %v", cfg.Model, cfg.OnUserInputRequest != nil)
	}
	d, err := cfg.OnPermissionRequest(&rpc.PermissionRequestShell{FullCommandText: "rm -rf /"}, copilot.PermissionInvocation{})
	if _, ok := d.(*rpc.PermissionDecisionNoResult); !ok || err != nil {
		t.Fatalf("SDK permission callback decided %T %v, want no result", d, err)
	}
	if caps := h.p.Capabilities(); !caps.Cancel || !caps.Permissions || !caps.Questions || !caps.History || caps.SessionDiff {
		t.Fatalf("capabilities = %+v", caps)
	}
	if _, err := h.conv.Diff(context.Background()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("Diff err = %v", err)
	}
}

func TestWebDeltasAndFinalMessageShareOneItem(t *testing.T) {
	h := openWeb(t)
	h.fs.onEvent(ev("e1", &rpc.AssistantMessageDeltaData{MessageID: "m1", DeltaContent: "Hel"}))
	h.fs.onEvent(ev("e2", &rpc.AssistantMessageDeltaData{MessageID: "m1", DeltaContent: "lo"}))
	h.fs.onEvent(ev("e3", &rpc.AssistantMessageData{MessageID: "m1", Content: "Hello"}))
	evs := h.sink.all()
	if len(evs) != 3 {
		t.Fatalf("events = %+v", evs)
	}
	for i, text := range []string{"Hel", "lo"} {
		if d := evs[i].Delta; evs[i].Kind != agentapi.EventDelta || d.ItemID != "m1" || d.Kind != agentapi.ItemAssistant || d.Text != text {
			t.Fatalf("delta %d = %+v", i, evs[i])
		}
	}
	if it := evs[2].Item; evs[2].Kind != agentapi.EventItem || it.ID != "m1" || it.Kind != agentapi.ItemAssistant || it.Text != "Hello" {
		t.Fatalf("final = %+v", evs[2])
	}
}

func TestWebToolEventsUpsertOneItem(t *testing.T) {
	h := openWeb(t)
	h.fs.onEvent(ev("e1", &rpc.ToolExecutionStartData{ToolCallID: "t1", ToolName: "bash", Arguments: map[string]any{"command": "ls"}}))
	h.fs.onEvent(ev("e2", &rpc.ToolExecutionPartialResultData{ToolCallID: "t1", PartialOutput: "a"}))
	h.fs.onEvent(ev("e3", &rpc.ToolExecutionPartialResultData{ToolCallID: "t1", PartialOutput: strings.Repeat("b", maxToolText)}))
	h.fs.onEvent(ev("e4", &rpc.ToolExecutionCompleteData{ToolCallID: "t1", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: "done"}}))
	h.fs.onEvent(ev("e5", &rpc.ToolExecutionStartData{ToolCallID: "t2", ToolName: "view"}))
	h.fs.onEvent(ev("e6", &rpc.ToolExecutionCompleteData{ToolCallID: "t2", Error: &rpc.ToolExecutionCompleteError{Message: "no such file"}}))
	evs := h.sink.all()
	if len(evs) != 6 {
		t.Fatalf("events = %d", len(evs))
	}
	want := []agentapi.ToolCall{
		{Name: "bash", Status: agentapi.ToolRunning, Input: `{"command":"ls"}`},
		{Name: "bash", Status: agentapi.ToolRunning, Input: `{"command":"ls"}`, Output: "a"},
		{Name: "bash", Status: agentapi.ToolRunning, Input: `{"command":"ls"}`, Output: "a" + strings.Repeat("b", maxToolText-1)},
		{Name: "bash", Status: agentapi.ToolCompleted, Input: `{"command":"ls"}`, Output: "done"},
		{Name: "view", Status: agentapi.ToolRunning},
		{Name: "view", Status: agentapi.ToolFailed, Output: "no such file"},
	}
	for i, w := range want {
		it := evs[i].Item
		id := "t1"
		if i >= 4 {
			id = "t2"
		}
		if evs[i].Kind != agentapi.EventItem || it.ID != id || it.Kind != agentapi.ItemTool || *it.Tool != w {
			t.Fatalf("event %d = %+v tool %+v, want %+v", i, it, it.Tool, w)
		}
	}
}

func TestWebTurnStates(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	turn := func() agentapi.Turn {
		t.Helper()
		e := h.sink.last()
		if e.Kind != agentapi.EventTurn {
			t.Fatalf("last event = %+v, want turn", e)
		}
		return *e.Turn
	}
	if err := h.conv.Send(ctx, "hi"); err != nil || turn().State != agentapi.TurnWorking {
		t.Fatalf("Send err %v", err)
	}
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	if turn().State != agentapi.TurnCancelled {
		t.Fatalf("aborted idle = %+v", turn())
	}
	_ = h.conv.Send(ctx, "again")
	h.fs.onEvent(ev("err1", &rpc.SessionErrorData{ErrorType: "rate_limit", Message: "rate\x1b[31m limited\nretry later"}))
	notice := h.sink.last()
	if notice.Kind != agentapi.EventItem || notice.Item.ID != "err1" || notice.Item.Kind != agentapi.ItemNotice {
		t.Fatalf("error notice = %+v", notice)
	}
	h.fs.onEvent(ev("i2", &rpc.SessionIdleData{}))
	if got := turn(); got.State != agentapi.TurnFailed || got.Error != "rate limited retry later" {
		t.Fatalf("failed turn = %+v", got)
	}
	_ = h.conv.Send(ctx, "third")
	h.fs.onEvent(ev("i3", &rpc.SessionIdleData{}))
	if got := turn(); got.State != agentapi.TurnCompleted || got.Error != "" {
		t.Fatalf("completed turn = %+v", got)
	}
	if strings.Join(h.fs.sent, ",") != "hi,again,third" {
		t.Fatalf("sent = %v", h.fs.sent)
	}
}

func TestWebSendFailureClassification(t *testing.T) {
	h := openWeb(t)
	h.fs.sendErr = rejectedError{errors.New("JSON-RPC Error -32603: invalid")}
	err := h.conv.Send(context.Background(), "p")
	if err == nil || errors.Is(err, agentapi.ErrSubmissionUncertain) {
		t.Fatalf("rejected send err = %v, want definite", err)
	}
	h.fs.sendErr = errors.New("CLI process exited: signal: killed")
	if err := h.conv.Send(context.Background(), "p"); !errors.Is(err, agentapi.ErrSubmissionUncertain) {
		t.Fatalf("send after CLI exit err = %v, want uncertain", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.conv.Send(ctx, "p"); !errors.Is(err, context.Canceled) || errors.Is(err, agentapi.ErrSubmissionUncertain) {
		t.Fatalf("cancelled send err = %v", err)
	}
	for _, e := range h.sink.all() {
		if e.Kind == agentapi.EventTurn {
			t.Fatalf("failed sends reported a turn: %+v", e.Turn)
		}
	}
}

func shellRequest(id string) *rpc.PermissionRequestedData {
	return &rpc.PermissionRequestedData{
		RequestID:         id,
		PermissionRequest: &rpc.PermissionRequestShell{FullCommandText: "rm -rf build"},
		PromptRequest:     &rpc.PermissionPromptRequestCommands{FullCommandText: "rm -rf build", CanOfferSessionApproval: true, CommandIdentifiers: []string{"rm"}},
	}
}

// Only approve_once is marked for yolo, and only when no managed policy says
// a person must decide.
func TestWebAllowOnceMarkerFollowsManagedPolicy(t *testing.T) {
	h := openWeb(t)
	h.fs.onEvent(ev("e1", shellRequest("plain")))
	managed := shellRequest("managed")
	managed.PermissionRequest = &rpc.PermissionRequestShell{FullCommandText: "rm -rf build", ManagedApprovalRequired: copilot.Bool(true)}
	h.fs.onEvent(ev("e2", managed))
	unreadable := shellRequest("unreadable")
	unreadable.PermissionRequest = nil
	h.fs.onEvent(ev("e3", unreadable))
	unknown := shellRequest("unknown")
	unknown.PermissionRequest = &rpc.RawPermissionRequest{Discriminator: "future-kind", Raw: []byte(`{"kind":"future-kind"}`)}
	h.fs.onEvent(ev("e4", unknown))
	for id, want := range map[string]string{"plain": "approve_once", "managed": "", "unreadable": "", "unknown": ""} {
		var marked []string
		for _, o := range h.sink.interaction(id).Options {
			if o.AllowOnce {
				marked = append(marked, o.ID)
			}
		}
		if strings.Join(marked, ",") != want {
			t.Fatalf("%s: options marked allow-once = %v, want %q", id, marked, want)
		}
	}
}

// Only a person's approve_once is reported as approved interactively; an
// automatic (yolo) approval leaves the flag off.
func TestWebApproveOnceReportsWhetherAPersonApproved(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	h.fs.onEvent(ev("e1", shellRequest("human")))
	h.fs.onEvent(ev("e2", shellRequest("auto")))
	if err := h.conv.Respond(ctx, "human", agentapi.Answer{Decision: "approve_once"}); err != nil {
		t.Fatalf("Respond human: %v", err)
	}
	if err := h.conv.Respond(ctx, "auto", agentapi.Answer{Decision: "approve_once", Auto: true}); err != nil {
		t.Fatalf("Respond auto: %v", err)
	}
	if d, ok := h.fs.answer("human").(*rpc.PermissionDecisionApproveOnce); !ok || d.ApprovedInteractively == nil || !*d.ApprovedInteractively {
		t.Fatalf("human decision = %#v", h.fs.answer("human"))
	}
	if d, ok := h.fs.answer("auto").(*rpc.PermissionDecisionApproveOnce); !ok || d.ApprovedInteractively != nil {
		t.Fatalf("auto decision = %#v", h.fs.answer("auto"))
	}
}

func TestWebPermissionWaitsForRespond(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	h.fs.onEvent(ev("e1", shellRequest("p1")))
	in := h.sink.interaction("p1")
	if in == nil || in.Kind != agentapi.InteractionPermission || in.State != agentapi.InteractionPending || !strings.Contains(in.Detail, "rm -rf build") {
		t.Fatalf("interaction = %+v", in)
	}
	var ids []string
	for _, o := range in.Options {
		ids = append(ids, o.ID)
	}
	if strings.Join(ids, ",") != "approve_once,approve_session,reject" || !in.Options[2].Reject {
		t.Fatalf("options = %+v", in.Options)
	}
	if h.fs.answer("p1") != nil {
		t.Fatal("permission answered before Respond")
	}
	if err := h.conv.Respond(ctx, "p1", agentapi.Answer{Decision: "bogus"}); err == nil || errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("unknown decision err = %v", err)
	}
	if err := h.conv.Respond(ctx, "p1", agentapi.Answer{Decision: "approve_session"}); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	d, ok := h.fs.answer("p1").(*rpc.PermissionDecisionApproveForSession)
	if !ok {
		t.Fatalf("decision = %T", h.fs.answer("p1"))
	}
	if a, ok := d.Approval.(*rpc.PermissionDecisionApproveForSessionApprovalCommands); !ok || strings.Join(a.CommandIdentifiers, ",") != "rm" {
		t.Fatalf("session approval = %+v", d.Approval)
	}
	if in := h.sink.interaction("p1"); in.State != agentapi.InteractionAnswered || in.Resolution != "Allow for this session" {
		t.Fatalf("answered interaction = %+v", in)
	}
	if err := h.conv.Respond(ctx, "p1", agentapi.Answer{Decision: "approve_once"}); !errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("second Respond err = %v", err)
	}

	// The CLI no longer waits: Respond reports gone and the interaction expires.
	h.fs.onEvent(ev("e2", shellRequest("p2")))
	h.fs.notPending = map[string]bool{"p2": true}
	if err := h.conv.Respond(ctx, "p2", agentapi.Answer{Decision: "reject"}); !errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("stale Respond err = %v", err)
	}
	if in := h.sink.interaction("p2"); in.State != agentapi.InteractionExpired {
		t.Fatalf("stale interaction = %+v", in)
	}

	// Resolved elsewhere (a hook or policy) ends the interaction here too.
	h.fs.onEvent(ev("e3", shellRequest("p3")))
	h.fs.onEvent(ev("e4", &rpc.PermissionCompletedData{RequestID: "p3", Result: &rpc.PermissionDeniedByRules{}}))
	if in := h.sink.interaction("p3"); in.State != agentapi.InteractionRejected || in.Resolution != "Denied by rules" {
		t.Fatalf("completed elsewhere = %+v", in)
	}
	if err := h.conv.Respond(ctx, "p3", agentapi.Answer{Decision: "approve_once"}); !errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("Respond after completion err = %v", err)
	}

	// Hook-resolved requests never reach the user; URL prompts get no session option.
	h.fs.onEvent(ev("e5", &rpc.PermissionRequestedData{RequestID: "p4", ResolvedByHook: copilot.Bool(true), PromptRequest: &rpc.PermissionPromptRequestRead{Path: "/x"}}))
	h.fs.onEvent(ev("e6", &rpc.PermissionRequestedData{RequestID: "p5", PromptRequest: &rpc.PermissionPromptRequestURL{URL: "https://example.com"}}))
	if h.sink.interaction("p4") != nil || len(h.sink.interaction("p5").Options) != 2 {
		t.Fatalf("hook/url interactions = %+v %+v", h.sink.interaction("p4"), h.sink.interaction("p5"))
	}
}

func askAsync(fs *fakeSession, req copilot.UserInputRequest) <-chan userReply {
	done := make(chan userReply, 1)
	go func() {
		resp, err := fs.askUser(req, copilot.UserInputInvocation{})
		done <- userReply{resp: resp, err: err}
	}()
	return done
}

func TestWebQuestionAnswers(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	cases := []struct {
		name     string
		freeform *bool
		answer   string
		want     copilot.UserInputResponse
	}{
		{"choice", nil, "blue", copilot.UserInputResponse{Answer: "blue", WasFreeform: false}},
		{"custom", copilot.Bool(true), "teal", copilot.UserInputResponse{Answer: "teal", WasFreeform: true}},
	}
	for _, tc := range cases {
		done := askAsync(h.fs, copilot.UserInputRequest{Question: "Colour?", Choices: []string{"red", "blue"}, AllowFreeform: tc.freeform})
		waitFor(t, "question", func() bool { q := h.sink.question(); return q != nil && q.State == agentapi.InteractionPending })
		q := h.sink.question()
		if q.Questions[0].Text != "Colour?" || !q.Questions[0].Custom || len(q.Questions[0].Choices) != 2 {
			t.Fatalf("%s: question = %+v", tc.name, q)
		}
		if err := h.conv.Respond(ctx, q.ID, agentapi.Answer{Answers: [][]string{{tc.answer}}}); err != nil {
			t.Fatalf("%s: Respond: %v", tc.name, err)
		}
		if r := <-done; r.err != nil || r.resp != tc.want {
			t.Fatalf("%s: handler got %+v %v, want %+v", tc.name, r.resp, r.err, tc.want)
		}
		if err := h.conv.Respond(ctx, q.ID, agentapi.Answer{Answers: [][]string{{"red"}}}); !errors.Is(err, agentapi.ErrInteractionGone) {
			t.Fatalf("%s: second Respond err = %v", tc.name, err)
		}
	}

	done := askAsync(h.fs, copilot.UserInputRequest{Question: "Pick", Choices: []string{"a"}, AllowFreeform: copilot.Bool(false)})
	waitFor(t, "strict question", func() bool { q := h.sink.question(); return q.Questions[0].Text == "Pick" })
	q := h.sink.question()
	if err := h.conv.Respond(ctx, q.ID, agentapi.Answer{Answers: [][]string{{"zzz"}}}); err == nil {
		t.Fatal("free-form answer accepted for a choice-only question")
	}
	if err := h.conv.Respond(ctx, q.ID, agentapi.Answer{Reject: true}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if r := <-done; r.err == nil || r.resp.Answer != "" {
		t.Fatalf("rejected question handler got %+v %v, want error", r.resp, r.err)
	}
	if h.sink.question().State != agentapi.InteractionRejected {
		t.Fatalf("rejected question = %+v", h.sink.question())
	}
}

func TestWebCloseReleasesPendingInteractions(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	h.fs.onEvent(ev("e1", shellRequest("p1")))
	done := askAsync(h.fs, copilot.UserInputRequest{Question: "Continue?"})
	waitFor(t, "question", func() bool { return h.sink.question() != nil })

	if err := h.conv.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if r := <-done; r.err == nil || r.resp.Answer != "" {
		t.Fatalf("question handler got %+v %v, want error", r.resp, r.err)
	}
	if _, ok := h.fs.answer("p1").(*rpc.PermissionDecisionUserNotAvailable); !ok {
		t.Fatalf("permission decision on close = %T", h.fs.answer("p1"))
	}
	if h.sink.interaction("p1").State != agentapi.InteractionExpired || h.sink.question().State != agentapi.InteractionExpired {
		t.Fatalf("interactions after close = %+v %+v", h.sink.interaction("p1"), h.sink.question())
	}
	if !h.fs.disconnected {
		t.Fatal("session not disconnected")
	}
	n := len(h.sink.all())
	h.fs.onEvent(ev("late", &rpc.AssistantMessageDeltaData{MessageID: "m", DeltaContent: "x"}))
	if len(h.sink.all()) != n {
		t.Fatal("event emitted after Close")
	}
	if err := h.conv.Send(ctx, "p"); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("Send after Close err = %v", err)
	}
	if err := h.conv.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestWebWatchdogFailureEndsConversationsAndResetsClient(t *testing.T) {
	var mu sync.Mutex
	var clients []*fakeClient
	p := newWebProvider(func() (sdkClient, error) {
		mu.Lock()
		defer mu.Unlock()
		c := &fakeClient{}
		clients = append(clients, c)
		return c, nil
	}, 5*time.Millisecond)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	ctx := context.Background()
	s1, s2 := &recSink{}, &recSink{}
	c1, err := p.Open(ctx, agentapi.OpenRequest{SessionID: "a", Events: s1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Open(ctx, agentapi.OpenRequest{ConversationID: "b", Events: s2}); err != nil {
		t.Fatal(err)
	}
	first := clients[0]
	fs1 := first.sessions[0]
	fs1.onEvent(ev("e1", shellRequest("p1")))
	done := askAsync(fs1, copilot.UserInputRequest{Question: "Q"})
	waitFor(t, "question", func() bool { return s1.question() != nil })

	first.mu.Lock()
	first.pingErr = errors.New("CLI process exited: exit status 1\nstderr: \x1b[31mboom")
	first.mu.Unlock()

	for _, s := range []*recSink{s1, s2} {
		waitFor(t, "exit event", func() bool { return s.last().Kind == agentapi.EventExit })
		if msg := s.last().Error; !strings.Contains(msg, "Copilot CLI stopped") || strings.ContainsAny(msg, "\x1b\n") {
			t.Fatalf("exit reason = %q", msg)
		}
	}
	if r := <-done; r.err == nil {
		t.Fatalf("question released with answer %+v", r.resp)
	}
	if s1.interaction("p1").State != agentapi.InteractionExpired || fs1.answer("p1") != nil {
		t.Fatalf("permission after failure = %+v decision %v", s1.interaction("p1"), fs1.answer("p1"))
	}
	waitFor(t, "force stop", func() bool { _, forced := first.counts(); return forced == 1 })
	if err := c1.Send(ctx, "p"); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("Send after failure err = %v", err)
	}

	if _, err := p.Open(ctx, agentapi.OpenRequest{SessionID: "c", Events: &recSink{}}); err != nil {
		t.Fatalf("Open after failure: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(clients) != 2 || clients[1].started != 1 || len(first.sessions) != 2 {
		t.Fatalf("clients %d, second started %d, first sessions %d", len(clients), clients[1].started, len(first.sessions))
	}
	for _, s := range append(first.sessions, clients[1].sessions...) {
		if len(s.sent) != 0 {
			t.Fatalf("prompt resent: %v", s.sent)
		}
	}
}

func TestWebReopen(t *testing.T) {
	fc := &fakeClient{resumeErr: errors.New("failed to resume session: JSON-RPC Error -32603: Request session.resume failed with message: Failed to load session events: Session not found: gone")}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	ctx := context.Background()
	if _, err := p.Open(ctx, agentapi.OpenRequest{ConversationID: "gone", Events: &recSink{}}); !errors.Is(err, agentapi.ErrConversationNotFound) {
		t.Fatalf("missing conversation err = %v", err)
	}
	if len(fc.create) != 0 {
		t.Fatal("missing conversation was replaced by a new one")
	}
	fc.resumeErr = nil
	conv, err := p.Open(ctx, agentapi.OpenRequest{ConversationID: "c-7", Workdir: "/w", Events: &recSink{}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := fc.resume[0]
	if conv.ID() != "c-7" || cfg.WorkingDirectory != "/w" || cfg.ContinuePendingWork == nil || *cfg.ContinuePendingWork || cfg.Streaming == nil || !*cfg.Streaming || cfg.Model != "" {
		t.Fatalf("resume config = %+v", cfg)
	}
	if cfg.OnPermissionRequest == nil || cfg.OnUserInputRequest == nil || cfg.OnEvent == nil {
		t.Fatal("resume lost its handlers")
	}
}

func TestWebHistory(t *testing.T) {
	h := openWeb(t)
	eph := func(e copilot.SessionEvent) copilot.SessionEvent { e.Ephemeral = copilot.Bool(true); return e }
	msgID := "u1"
	h.fs.events = []copilot.SessionEvent{
		ev("e1", &rpc.UserMessageData{Content: "fix it", MessageID: &msgID}),
		ev("e2", &rpc.AssistantReasoningData{ReasoningID: "r1", Content: "thinking"}),
		eph(ev("e3", &rpc.AssistantMessageDeltaData{MessageID: "m1", DeltaContent: "Do"})),
		ev("e4", &rpc.AssistantMessageData{MessageID: "m1", Content: "Done."}),
		ev("e5", &rpc.ToolExecutionStartData{ToolCallID: "t1", ToolName: "edit"}),
		eph(ev("e6", &rpc.ToolExecutionPartialResultData{ToolCallID: "t1", PartialOutput: "…"})),
		ev("e7", &rpc.ToolExecutionCompleteData{ToolCallID: "t1", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: "ok"}}),
		ev("e8", &rpc.AssistantMessageData{MessageID: "m2"}),
		ev("e9", &rpc.SessionErrorData{Message: "quota"}),
		ev("e10", &rpc.SessionIdleData{}),
	}
	recorded, err := h.conv.History(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range recorded.Items {
		s := string(it.Kind) + ":" + it.ID + ":" + it.Text
		if it.Tool != nil {
			s += string(it.Tool.Status) + "/" + it.Tool.Output
		}
		got = append(got, s)
	}
	want := "user:u1:fix it|reasoning:reasoning:r1:thinking|assistant:m1:Done.|tool:t1:completed/ok|notice:e9:Error: quota"
	if strings.Join(got, "|") != want {
		t.Fatalf("history =\n%s\nwant\n%s", strings.Join(got, "|"), want)
	}
	if len(h.sink.all()) != 0 {
		t.Fatal("History emitted events")
	}
}

func userMessage(id string, delivery rpc.UserMessageDelivery, text string) *rpc.UserMessageData {
	return &rpc.UserMessageData{Content: text, MessageID: &id, Delivery: &delivery}
}

// notices returns the texts of the notice items in evs.
func notices(evs []agentapi.Event) []string {
	var out []string
	for _, e := range evs {
		if e.Kind == agentapi.EventItem && e.Item.Kind == agentapi.ItemNotice {
			out = append(out, e.Item.ID+"|"+e.Item.Text)
		}
	}
	return out
}

func TestWebSendEnqueuesAndSteerInterjects(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	if err := h.conv.Send(ctx, "start"); err != nil {
		t.Fatal(err)
	}
	before := len(h.sink.all())
	if err := h.conv.Steer(ctx, "also this"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.fs.modes, ","); got != "enqueue,immediate" || strings.Join(h.fs.sent, ",") != "start,also this" {
		t.Fatalf("modes = %s, sent = %v", got, h.fs.sent)
	}
	if evs := h.sink.all()[before:]; len(evs) != 0 {
		t.Fatalf("a steer reported %+v; the running turn reports its own end", evs)
	}
	h.fs.sendErr = rejectedError{errors.New("JSON-RPC Error -32603: invalid")}
	if err := h.conv.Steer(ctx, "p"); err == nil || errors.Is(err, agentapi.ErrSubmissionUncertain) {
		t.Fatalf("rejected steer err = %v, want definite", err)
	}
	h.fs.sendErr = errors.New("CLI process exited: signal: killed")
	if err := h.conv.Steer(ctx, "p"); !errors.Is(err, agentapi.ErrSubmissionUncertain) {
		t.Fatalf("steer after CLI exit err = %v, want uncertain", err)
	}
}

func TestWebSteerDeliveredIsMarked(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, "start") // msg-1
	h.fs.onEvent(ev("u1", userMessage("msg-1", rpc.UserMessageDeliveryIdle, "start")))
	if it := h.sink.last().Item; it == nil || it.ID != "msg-1" || it.Delivery != "" {
		t.Fatalf("prompt item = %+v", it)
	}
	if err := h.conv.Steer(ctx, "use tabs"); err != nil { // msg-2
		t.Fatal(err)
	}
	h.fs.onEvent(ev("u2", userMessage("msg-2", rpc.UserMessageDeliverySteering, "use tabs")))
	if it := h.sink.last().Item; it == nil || it.ID != "msg-2" || it.Kind != agentapi.ItemUser || it.Delivery != agentapi.DeliverySteer {
		t.Fatalf("steer item = %+v", it)
	}
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	if got := notices(h.sink.all()); len(got) != 0 {
		t.Fatalf("a delivered steer was reported as not delivered: %q", got)
	}
}

func TestWebSteerTheTurnDidNotUseIsReportedOnce(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, "start")
	_ = h.conv.Steer(ctx, "too late")      // msg-2
	_ = h.conv.Steer(ctx, "line one\ntwo") // msg-3
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	evs := h.sink.all()
	want := []string{
		"steer-undelivered:msg-2|Steer not delivered: the turn was stopped\n\n> too late",
		"steer-undelivered:msg-3|Steer not delivered: the turn was stopped\n\n> line one\n> two",
	}
	if got := notices(evs); strings.Join(got, "#") != strings.Join(want, "#") {
		t.Fatalf("notices = %q\nwant %q", got, want)
	}
	if last := evs[len(evs)-1]; last.Kind != agentapi.EventTurn || last.Turn.State != agentapi.TurnCancelled {
		t.Fatalf("the notices must come before the turn ends: last event %+v", last)
	}
	_ = h.conv.Send(ctx, "again")
	h.fs.onEvent(ev("i2", &rpc.SessionIdleData{}))
	if got := notices(h.sink.all()); len(got) != 2 {
		t.Fatalf("notices after the next turn = %q", got)
	}
}

// A steer moves a running shell to the background: the call completes, and
// the shell's later output arrives as partial results under the same ID.
func TestWebBackgroundedShellOutputKeepsTheCallCompleted(t *testing.T) {
	h := openWeb(t)
	h.fs.onEvent(ev("e1", &rpc.ToolExecutionStartData{ToolCallID: "t1", ToolName: "bash"}))
	h.fs.onEvent(ev("e2", &rpc.ToolExecutionCompleteData{ToolCallID: "t1", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: "moved to background"}}))
	h.fs.onEvent(ev("e3", &rpc.ToolExecutionPartialResultData{ToolCallID: "t1", PartialOutput: "first-done\n"}))
	evs := h.sink.all()
	if last := evs[len(evs)-1].Item; len(evs) != 2 || last.Tool.Name != "bash" || last.Tool.Status != agentapi.ToolCompleted {
		t.Fatalf("events = %d, last tool %+v", len(evs), last.Tool)
	}
}

// A steer that reaches the CLI after its idle starts a new turn there, which
// reports the steer's user message with delivery "idle".
func TestWebSteerDeliveredAfterIdleStartsATurn(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, "start")                     // msg-1
	if err := h.conv.Steer(ctx, "late"); err != nil { // msg-2
		t.Fatal(err)
	}
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{}))
	if got := notices(h.sink.all()); len(got) != 0 {
		t.Fatalf("a completed turn reported its steer as not delivered: %q", got)
	}
	before := len(h.sink.all())
	h.fs.onEvent(ev("u2", userMessage("msg-2", rpc.UserMessageDeliveryIdle, "late")))
	var turns []agentapi.TurnState
	for _, e := range h.sink.all()[before:] {
		if e.Kind == agentapi.EventTurn {
			turns = append(turns, e.Turn.State)
		}
	}
	if len(turns) != 1 || turns[0] != agentapi.TurnWorking {
		t.Fatalf("turns after the idle-delivered steer = %v, want [working]", turns)
	}
	if it := h.sink.last().Item; it == nil || it.ID != "msg-2" || it.Kind != agentapi.ItemUser {
		t.Fatalf("steer item = %+v", it)
	}
	h.fs.onEvent(ev("i2", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	if got := notices(h.sink.all()); len(got) != 0 {
		t.Fatalf("a used steer was reported as not delivered: %q", got)
	}
}

func TestWebToolStartReusingAnEndedIDStreamsItsOutput(t *testing.T) {
	h := openWeb(t)
	h.fs.onEvent(ev("e1", &rpc.ToolExecutionStartData{ToolCallID: "t1", ToolName: "bash"}))
	h.fs.onEvent(ev("e2", &rpc.ToolExecutionCompleteData{ToolCallID: "t1", Success: true}))
	h.fs.onEvent(ev("e3", &rpc.ToolExecutionStartData{ToolCallID: "t1", ToolName: "bash"}))
	h.fs.onEvent(ev("e4", &rpc.ToolExecutionPartialResultData{ToolCallID: "t1", PartialOutput: "second\n"}))
	last := h.sink.last().Item
	if last == nil || last.Tool == nil || last.Tool.Status != agentapi.ToolRunning || last.Tool.Output != "second\n" {
		t.Fatalf("last item = %+v", last)
	}
}

// The CLI can use a steer before the Send that carried it returns its ID.
func TestWebSteerUsedBeforeSendReturns(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, "start")
	h.fs.beforeReturn = func(id string) {
		h.fs.onEvent(ev("u2", userMessage(id, rpc.UserMessageDeliverySteering, "quick")))
	}
	if err := h.conv.Steer(ctx, "quick"); err != nil {
		t.Fatal(err)
	}
	h.fs.beforeReturn = nil
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	if got := notices(h.sink.all()); len(got) != 0 {
		t.Fatalf("a used steer was reported as not delivered: %q", got)
	}
}

func TestWebSteerIdleBeforeSendReturns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failed bool
		idle   *rpc.SessionIdleData
		reason string
	}{
		{name: "aborted", idle: &rpc.SessionIdleData{Aborted: copilot.Bool(true)}, reason: "the turn was stopped"},
		{name: "failed", failed: true, idle: &rpc.SessionIdleData{}, reason: "the turn failed"},
		{name: "completed", idle: &rpc.SessionIdleData{}},
	} {
		for _, used := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/used=%t", tc.name, used), func(t *testing.T) {
				h := openWeb(t)
				ctx := context.Background()
				if err := h.conv.Send(ctx, "start"); err != nil {
					t.Fatal(err)
				}
				if tc.name == "aborted" {
					h.fs.onEvent(ev("permission", shellRequest("p1")))
				}
				h.fs.beforeReturn = func(id string) {
					if used {
						h.fs.onEvent(ev("u2", userMessage(id, rpc.UserMessageDeliverySteering, "line one\ntwo")))
					}
					if tc.failed {
						h.fs.onEvent(ev("error", &rpc.SessionErrorData{Message: "failed"}))
					}
					h.fs.onEvent(ev("idle", tc.idle))
				}
				if err := h.conv.Steer(ctx, "line one\ntwo"); err != nil {
					t.Fatal(err)
				}
				h.fs.beforeReturn = nil
				if tc.name == "aborted" && h.sink.interaction("p1").State != agentapi.InteractionExpired {
					t.Fatal("aborted idle did not expire the pending permission")
				}
				var want []string
				if tc.failed {
					want = append(want, "error|Error: failed")
				}
				if tc.reason != "" && !used {
					want = append(want, "steer-undelivered:msg-2|Steer not delivered: "+tc.reason+"\n\n> line one\n> two")
				}
				if got := notices(h.sink.all()); strings.Join(got, "#") != strings.Join(want, "#") {
					t.Fatalf("notices after Send returned = %q, want %q", got, want)
				}
				if err := h.conv.Send(ctx, "again"); err != nil {
					t.Fatal(err)
				}
				h.fs.onEvent(ev("later-idle", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
				if got := notices(h.sink.all()); strings.Join(got, "#") != strings.Join(want, "#") {
					t.Fatalf("notices after a later abort = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestWebSteerConcurrentSendsBeforeAbortedIdle(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	if err := h.conv.Send(ctx, "start"); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 2)
	release := map[string]chan struct{}{
		"msg-2": make(chan struct{}, 1),
		"msg-3": make(chan struct{}, 1),
	}
	defer close(release["msg-2"])
	defer close(release["msg-3"])
	h.fs.beforeReturn = func(id string) {
		started <- id
		<-release[id]
	}
	done := make(chan error, 2)
	go func() { done <- h.conv.Steer(ctx, "used") }()
	if id := <-started; id != "msg-2" {
		t.Fatalf("first steer ID = %q", id)
	}
	go func() { done <- h.conv.Steer(ctx, "unused") }()
	if id := <-started; id != "msg-3" {
		t.Fatalf("second steer ID = %q", id)
	}
	h.fs.onEvent(ev("used", userMessage("msg-2", rpc.UserMessageDeliverySteering, "used")))
	h.fs.onEvent(ev("idle", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	// Return the unused call first; the used call must retain its delivery
	// evidence until it receives its own response.
	release["msg-3"] <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.fs.onEvent(ev("later-idle", &rpc.SessionIdleData{}))
	release["msg-2"] <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.fs.onEvent(ev("later-abort", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	want := "steer-undelivered:msg-3|Steer not delivered: the turn was stopped\n\n> unused"
	if got := notices(h.sink.all()); len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %q, want [%q]", got, want)
	}
}

func TestWebHistoryMarksSteers(t *testing.T) {
	h := openWeb(t)
	h.fs.events = []copilot.SessionEvent{
		ev("e1", userMessage("u1", rpc.UserMessageDeliveryIdle, "start")),
		ev("e2", userMessage("u2", rpc.UserMessageDeliverySteering, "steer")),
		ev("e3", userMessage("u3", rpc.UserMessageDeliveryQueued, "queued")),
	}
	recorded, err := h.conv.History(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range recorded.Items {
		got = append(got, it.ID+":"+it.Delivery)
	}
	if strings.Join(got, ",") != "u1:,u2:steer,u3:" {
		t.Fatalf("history deliveries = %v", got)
	}
}

func TestWebModelsKeepOnlySelectableEntries(t *testing.T) {
	fc := &fakeClient{models: []rpc.Model{
		{ID: "auto", Name: "Auto"},
		{ID: "claude-haiku-4.5", Name: "Claude Haiku 4.5", Policy: &rpc.ModelPolicy{State: rpc.ModelPolicyStateEnabled}},
		{ID: "gpt-locked", Name: "Locked", Policy: &rpc.ModelPolicy{State: rpc.ModelPolicyStateDisabled}},
		{ID: "gpt-unset", Name: "Unconfigured", Policy: &rpc.ModelPolicy{State: rpc.ModelPolicyStateUnconfigured}},
	}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []agentapi.Model{{ID: "auto", Name: "Auto"}, {ID: "claude-haiku-4.5", Name: "Claude Haiku 4.5"}}
	if fmt.Sprint(models) != fmt.Sprint(want) {
		t.Fatalf("models = %+v, want %+v", models, want)
	}
	fc.mu.Lock()
	fc.models, fc.modelsErr = nil, errors.New("not signed in")
	fc.mu.Unlock()
	if _, err := p.Models(context.Background()); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("models error = %v", err)
	}
}

func TestWebModelOnCreateOnlyAndSetModel(t *testing.T) {
	fc := &fakeClient{}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	ctx := context.Background()
	conv, err := p.Open(ctx, agentapi.OpenRequest{SessionID: "s-1", Workdir: "/w", Model: "gpt-5-mini", Events: &recSink{}})
	if err != nil {
		t.Fatal(err)
	}
	if fc.create[0].Model != "gpt-5-mini" {
		t.Fatalf("create model = %q", fc.create[0].Model)
	}
	if _, err := p.Open(ctx, agentapi.OpenRequest{ConversationID: "c-1", Model: "ignored", Events: &recSink{}}); err != nil || fc.resume[0].Model != "" {
		t.Fatalf("resume model = %q, %v", fc.resume[0].Model, err)
	}
	if err := conv.SetModel(ctx, "claude-haiku-4.5", "", "default"); err != nil {
		t.Fatal(err)
	}
	fs := fc.sessions[0]
	if strings.Join(fs.models, ",") != "claude-haiku-4.5" {
		t.Fatalf("switched models = %q", fs.models)
	}
	fs.modelErr = errors.New("JSON-RPC Error: bad")
	if err := conv.SetModel(ctx, "x", "", "default"); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("refused switch err = %v", err)
	}
	if err := conv.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := conv.SetModel(ctx, "x", "", "default"); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("SetModel after Close err = %v", err)
	}
}

func agentEv(id, agentID string, data rpc.SessionEventData) copilot.SessionEvent {
	e := ev(id, data)
	e.AgentID = &agentID
	return e
}

func TestWebTitleAndTurnModel(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	h.fs.onEvent(ev("t1", &rpc.SessionTitleChangedData{Title: "Fix the build"}))
	if e := h.sink.last(); e.Kind != agentapi.EventTitle || e.Title != "Fix the build" {
		t.Fatalf("title event = %+v", e)
	}
	_ = h.conv.Send(ctx, "hi")
	h.fs.onEvent(ev("u1", &rpc.AssistantUsageData{Model: "gpt-5-mini"}))
	h.fs.onEvent(agentEv("u2", "agent-1", &rpc.AssistantUsageData{Model: "sub-model"}))
	h.fs.onEvent(ev("u3", &rpc.AssistantUsageData{Model: "claude-haiku-4.5"}))
	h.fs.onEvent(agentEv("u4", "agent-1", &rpc.AssistantUsageData{Model: "sub-model"}))
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{}))
	if e := h.sink.last(); e.Kind != agentapi.EventTurn || e.Turn.State != agentapi.TurnCompleted || e.Turn.Model != "claude-haiku-4.5" {
		t.Fatalf("turn = %+v", e.Turn)
	}
	_ = h.conv.Send(ctx, "again")
	h.fs.onEvent(ev("i2", &rpc.SessionIdleData{}))
	if e := h.sink.last(); e.Turn.Model != "" {
		t.Fatalf("second turn reused the first turn's model: %+v", e.Turn)
	}
}

func TestWebSubagentEventsNeverEnterTheMainTranscript(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, "delegate")
	subPrompt := "sub-prompt"
	h.fs.onEvent(ev("e1", &rpc.ToolExecutionStartData{ToolCallID: "call_1", ToolName: "task"}))
	h.fs.onEvent(agentEv("e2", "agent-1", &rpc.SubagentStartedData{ToolCallID: "call_1", AgentName: "general-purpose", AgentDisplayName: "General purpose", AgentDescription: "Does things"}))
	h.fs.onEvent(agentEv("e3", "agent-1", &rpc.UserMessageData{Content: "delegated prompt", MessageID: &subPrompt}))
	h.fs.onEvent(agentEv("e4", "agent-1", &rpc.AssistantMessageDeltaData{MessageID: "m1", DeltaContent: "part"}))
	h.fs.onEvent(agentEv("e5", "agent-1", &rpc.AssistantMessageData{MessageID: "m1", Content: "sub answer"}))
	h.fs.onEvent(agentEv("e6", "agent-1", &rpc.SessionErrorData{Message: "sub hiccup"}))
	h.fs.onEvent(agentEv("e7", "agent-1", &rpc.SessionIdleData{}))
	h.fs.onEvent(agentEv("e8", "agent-1", shellRequest("p1")))
	h.fs.onEvent(agentEv("e9", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1", AgentName: "general-purpose"}))
	// Copilot repeats the completion, cancelled, when the client disconnects.
	h.fs.onEvent(agentEv("e10", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1", Cancelled: copilot.Bool(true)}))
	// A failure without an envelope agent is matched by its tool call.
	h.fs.onEvent(agentEv("e11", "agent-2", &rpc.SubagentStartedData{ToolCallID: "call_2", AgentName: "explore"}))
	h.fs.onEvent(ev("e12", &rpc.SubagentFailedData{ToolCallID: "call_2", Error: "boom\x1b[31m"}))
	h.fs.onEvent(ev("e13", &rpc.SubagentFailedData{ToolCallID: "unknown", Error: "who"}))

	var subs []agentapi.Subagent
	for _, e := range h.sink.all() {
		switch e.Kind {
		case agentapi.EventItem:
			if e.Item.ID != "call_1" && e.Item.AgentID != "agent-1" {
				t.Fatalf("subagent item without its agent: %+v", e.Item)
			}
			if e.Item.ID == "call_1" && e.Item.AgentID != "" {
				t.Fatalf("main tool item tagged: %+v", e.Item)
			}
		case agentapi.EventDelta:
			if e.Delta.AgentID != "agent-1" {
				t.Fatalf("subagent delta without its agent: %+v", e.Delta)
			}
		case agentapi.EventTurn:
			if e.Turn.State != agentapi.TurnWorking {
				t.Fatalf("a subagent event ended the main turn: %+v", e.Turn)
			}
		case agentapi.EventInteraction:
			if e.Interaction.AgentID != "agent-1" {
				t.Fatalf("subagent permission without its agent: %+v", e.Interaction)
			}
		case agentapi.EventSubagent:
			subs = append(subs, *e.Subagent)
		}
	}
	if len(subs) != 4 {
		t.Fatalf("subagent events = %+v", subs)
	}
	if s := subs[0]; s.ID != "agent-1" || s.Status != agentapi.SubagentRunning || s.ParentToolCallID != "call_1" || s.Name != "General purpose" || s.Description != "Does things" || s.StartedAt.IsZero() {
		t.Fatalf("started = %+v", s)
	}
	if s := subs[1]; s.ID != "agent-1" || s.Status != agentapi.SubagentCompleted || s.EndedAt.IsZero() {
		t.Fatalf("completed = %+v", s)
	}
	if s := subs[3]; s.ID != "agent-2" || s.Status != agentapi.SubagentFailed || s.Error != "boom" {
		t.Fatalf("failed = %+v", s)
	}
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{}))
	if e := h.sink.last(); e.Kind != agentapi.EventTurn || e.Turn.State != agentapi.TurnCompleted {
		t.Fatalf("main turn after a subagent error = %+v", e.Turn)
	}
}

func TestWebHistorySeparatesSubagents(t *testing.T) {
	h := openWeb(t)
	msgID, subID := "u1", "u2"
	h.fs.events = []copilot.SessionEvent{
		ev("e1", &rpc.UserMessageData{Content: "fix it", MessageID: &msgID}),
		ev("e2", &rpc.ToolExecutionStartData{ToolCallID: "call_1", ToolName: "task"}),
		agentEv("e3", "agent-1", &rpc.SubagentStartedData{ToolCallID: "call_1", AgentName: "general-purpose"}),
		agentEv("e4", "agent-1", &rpc.UserMessageData{Content: "delegated", MessageID: &subID}),
		agentEv("e5", "agent-1", &rpc.AssistantMessageData{MessageID: "m1", Content: "sub answer"}),
		agentEv("e6", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1"}),
		agentEv("e7", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "call_1", Cancelled: copilot.Bool(true)}),
		ev("e8", &rpc.ToolExecutionCompleteData{ToolCallID: "call_1", Success: true, Result: &rpc.ToolExecutionCompleteResult{Content: "sub answer"}}),
		ev("e9", &rpc.AssistantMessageData{MessageID: "m2", Content: "Done."}),
	}
	recorded, err := h.conv.History(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var main, sub []string
	for _, it := range recorded.Items {
		if it.AgentID == "" {
			main = append(main, it.ID)
		} else {
			sub = append(sub, it.AgentID+"/"+it.ID)
		}
	}
	if strings.Join(main, ",") != "u1,call_1,m2" || strings.Join(sub, ",") != "agent-1/u2,agent-1/m1" {
		t.Fatalf("history main %q sub %q", main, sub)
	}
	if len(recorded.Subagents) != 1 || recorded.Subagents[0].Status != agentapi.SubagentCompleted || recorded.Subagents[0].ParentToolCallID != "call_1" {
		t.Fatalf("history subagents = %+v", recorded.Subagents)
	}
}

func TestWebCheck(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	p := newWebProvider(nil, time.Hour)
	ctx := context.Background()
	if err := p.Check(ctx); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("missing CLI err = %v", err)
	}
	script := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "copilot"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("#!/bin/sh\necho 'GitHub Copilot CLI 1.0.88.'\n")
	if err := p.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	}
	script("#!/bin/sh\necho 'something else'\n")
	if err := p.Check(ctx); err == nil {
		t.Fatal("Check accepted output without a version")
	}
	script("#!/bin/sh\necho 'bad flag' >&2; exit 2\n")
	if err := p.Check(ctx); err == nil || !strings.Contains(err.Error(), "bad flag") {
		t.Fatalf("failing CLI err = %v", err)
	}
	script("#!/usr/bin/env node\n")
	if err := p.Check(ctx); err == nil || !strings.Contains(err.Error(), "node is not on PATH") {
		t.Fatalf("node shim without node err = %v", err)
	}
}

// TestWebRealCopilotCreateWithoutPrompt starts the installed CLI and opens a
// conversation without sending anything, so no model call is made.
func TestWebRealCopilotCreateWithoutPrompt(t *testing.T) {
	if os.Getenv("UAM_WEB_REAL_COPILOT") != "1" {
		t.Skip("set UAM_WEB_REAL_COPILOT=1 to start the installed copilot CLI")
	}
	// A private COPILOT_HOME keeps the test's session out of ~/.copilot.
	t.Setenv("COPILOT_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p := NewWebProvider()
	if err := p.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	}
	id := testUUID(t)
	sink := &recSink{}
	conv, err := p.Open(ctx, agentapi.OpenRequest{SessionID: id, Workdir: t.TempDir(), Title: "uam web test", Events: sink})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if conv.ID() != id {
		t.Fatalf("conversation id %q, want %q", conv.ID(), id)
	}
	if _, err := p.Open(ctx, agentapi.OpenRequest{ConversationID: testUUID(t), Workdir: t.TempDir(), Events: &recSink{}}); !errors.Is(err, agentapi.ErrConversationNotFound) {
		t.Fatalf("reopen of unknown id err = %v", err)
	}
	procs := cliProcesses(t)
	if len(procs) != 2 { // npm node shim and the native CLI it runs
		t.Logf("CLI processes before shutdown: %v", procs)
	}
	if err := conv.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	for _, pid := range procs {
		waitFor(t, fmt.Sprintf("CLI process %d to exit", pid), func() bool { return syscall.Kill(pid, 0) != nil })
	}
	for _, e := range sink.all() {
		if e.Kind == agentapi.EventExit {
			t.Fatalf("unexpected exit event: %+v", e)
		}
	}
}

func testUUID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// cliProcesses lists the headless CLI processes this test process started,
// including the native binary the npm shim runs as its child.
func cliProcesses(t *testing.T) []int {
	t.Helper()
	out, err := exec.Command("ps", "-eo", "pid=,ppid=,args=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	parents := map[int]bool{os.Getpid(): true}
	var pids []int
	for range 2 {
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 3 || !strings.Contains(line, "--headless --no-auto-update --stdio") {
				continue
			}
			pid, _ := strconv.Atoi(f[0])
			ppid, _ := strconv.Atoi(f[1])
			if parents[ppid] && !parents[pid] {
				parents[pid] = true
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

func TestExitTextDropsWrapperStderr(t *testing.T) {
	err := errors.New("CLI process exited: signal: killed\nstderr: Error: no platform package found. Reinstall")
	if got := exitText(err); got != "CLI process exited: signal: killed" {
		t.Fatalf("exitText = %q", got)
	}
}

func TestWebCancelSubagentTargetsExactAgent(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	h.fs.onEvent(agentEv("s1", "agent-1", &rpc.SubagentStartedData{ToolCallID: "parent-call-1"}))
	h.fs.onEvent(agentEv("s2", "agent-2", &rpc.SubagentStartedData{ToolCallID: "parent-call-2"}))
	if err := h.conv.CancelSubagent(ctx, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if len(h.fs.subCancels) != 1 || h.fs.subCancels[0] != "agent-1" {
		t.Fatalf("cancel calls = %v", h.fs.subCancels)
	}
	for _, e := range h.sink.all() {
		if e.Kind == agentapi.EventTurn || e.Kind == agentapi.EventSubagent && e.Subagent.Status != agentapi.SubagentRunning {
			t.Fatalf("cancel changed state before provider event: %+v", e)
		}
	}
	h.fs.onEvent(agentEv("done", "agent-1", &rpc.SubagentCompletedData{ToolCallID: "parent-call-1", Cancelled: copilot.Bool(true)}))
	if e := h.sink.last(); e.Subagent == nil || e.Subagent.Status != agentapi.SubagentCancelled {
		t.Fatalf("cancel event = %+v", e)
	}
	if err := h.conv.CancelSubagent(ctx, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if len(h.fs.subCancels) != 1 || len(h.fs.sent) != 0 {
		t.Fatalf("repeat resent or prompted: cancels %v sends %v", h.fs.subCancels, h.fs.sent)
	}
}

func TestWebCancelledSubagentPermissionsStayExpired(t *testing.T) {
	for _, source := range []string{"response", "event", "history"} {
		t.Run(source, func(t *testing.T) {
			h := openWeb(t)
			ctx := context.Background()
			started := agentEv("start", "target", &rpc.SubagentStartedData{ToolCallID: "call-target"})
			ended := agentEv("end", "target", &rpc.SubagentCompletedData{ToolCallID: "call-target", Cancelled: copilot.Bool(true)})
			h.fs.onEvent(started)
			h.fs.onEvent(agentEv("p1", "target", shellRequest("target-permission")))
			h.fs.onEvent(agentEv("p2", "sibling", shellRequest("sibling-permission")))
			h.fs.onEvent(ev("p3", shellRequest("parent-permission")))
			switch source {
			case "response":
				if err := h.conv.CancelSubagent(ctx, "target"); err != nil {
					t.Fatal(err)
				}
			case "event":
				h.fs.onEvent(ended)
			case "history":
				h.fs.events = []copilot.SessionEvent{started, ended}
				if _, err := h.conv.History(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if ix := h.sink.interaction("target-permission"); ix.State != agentapi.InteractionExpired {
				t.Fatalf("target permission = %+v", ix)
			}
			// The provider can replay a stale pending permission after cancellation.
			h.fs.onEvent(agentEv("late", "target", shellRequest("target-permission")))
			h.fs.onEvent(agentEv("new-late", "target", shellRequest("new-target-permission")))
			for _, id := range []string{"target-permission", "new-target-permission"} {
				if err := h.conv.Respond(ctx, id, agentapi.Answer{Decision: "approve_once"}); !errors.Is(err, agentapi.ErrInteractionGone) {
					t.Fatalf("Respond %s = %v", id, err)
				}
			}
			for _, id := range []string{"sibling-permission", "parent-permission"} {
				if ix := h.sink.interaction(id); ix.State != agentapi.InteractionPending {
					t.Fatalf("unrelated permission = %+v", ix)
				}
				if err := h.conv.Respond(ctx, id, agentapi.Answer{Decision: "approve_once"}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestWebCancelSubagentFailureKeepsPermissionForRetry(t *testing.T) {
	for _, transport := range []bool{false, true} {
		t.Run(strconv.FormatBool(transport), func(t *testing.T) {
			h := openWeb(t)
			h.fs.onEvent(agentEv("start", "target", &rpc.SubagentStartedData{ToolCallID: "call"}))
			h.fs.onEvent(agentEv("permission", "target", shellRequest("pending")))
			h.fs.subCancelRejected = true
			if transport {
				h.fs.subCancelErr = errors.New("transport failed")
			}
			if err := h.conv.CancelSubagent(context.Background(), "target"); err == nil {
				t.Fatal("failed stop reported success")
			}
			if len(h.fs.subCancels) != 1 || h.sink.interaction("pending").State != agentapi.InteractionPending {
				t.Fatalf("failure retried or expired request: %v %+v", h.fs.subCancels, h.sink.interaction("pending"))
			}
			h.fs.subCancelRejected, h.fs.subCancelErr = false, nil
			if err := h.conv.CancelSubagent(context.Background(), "target"); err != nil {
				t.Fatal(err)
			}
			if err := h.conv.CancelSubagent(context.Background(), "target"); err != nil {
				t.Fatal(err)
			}
			if len(h.fs.subCancels) != 2 || h.sink.interaction("pending").State != agentapi.InteractionExpired {
				t.Fatalf("retry = %v %+v", h.fs.subCancels, h.sink.interaction("pending"))
			}
		})
	}
}
