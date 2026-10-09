package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type earlyPlanClient struct {
	*fakeClient
	t    *testing.T
	sink *recSink
	done chan copilot.ExitPlanModeResult
	fail bool
}

func (f *earlyPlanClient) review(handler copilot.ExitPlanModeRequestHandler) {
	go func() {
		result, _ := handler(copilot.ExitPlanModeRequest{PlanContent: "# Early plan", Actions: []string{"interactive"}}, copilot.ExitPlanModeInvocation{})
		f.done <- result
	}()
	waitFor(f.t, "early callback before Open returns", func() bool { return pendingPlan(f.sink) != nil })
}

func (f *earlyPlanClient) CreateSession(ctx context.Context, cfg *copilot.SessionConfig) (sdkSession, error) {
	f.review(cfg.OnExitPlanModeRequest)
	if f.fail {
		return nil, errors.New("create failed after callback registration")
	}
	return f.fakeClient.CreateSession(ctx, cfg)
}

func (f *earlyPlanClient) ResumeSession(ctx context.Context, id string, cfg *copilot.ResumeSessionConfig) (sdkSession, error) {
	f.review(cfg.OnExitPlanModeRequest)
	if f.fail {
		return nil, errors.New("resume failed after callback registration")
	}
	return f.fakeClient.ResumeSession(ctx, id, cfg)
}

func TestWebPlanOpenFailureReleasesEarlyReview(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		for _, usageFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("resume=%t/usage=%t", resumed, usageFailure), func(t *testing.T) {
				sink := &recSink{}
				fc := &earlyPlanClient{fakeClient: &fakeClient{}, t: t, sink: sink, done: make(chan copilot.ExitPlanModeResult, 1), fail: !usageFailure}
				p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
				t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
				if usageFailure {
					p.SetUsageSessionRecorder(func(string, bool) error { return errors.New("ownership failed") })
				}
				req := agentapi.OpenRequest{SessionID: "early", Workdir: "/work", Events: sink}
				if resumed {
					req.ConversationID = "existing"
				}
				if conv, err := p.Open(t.Context(), req); err == nil || conv != nil {
					t.Fatalf("Open unexpectedly succeeded: %v %v", conv, err)
				}
				select {
				case result := <-fc.done:
					if result.Approved || !strings.Contains(result.Feedback, "cancelled") {
						t.Fatalf("failed Open approved: %+v", result)
					}
				case <-time.After(time.Second):
					t.Fatal("failed Open left review callback blocked")
				}
			})
		}
	}
}

func TestWebPlanEarlyResumeReviewUsesExistingNativeRevisions(t *testing.T) {
	sink := &recSink{}
	fc := &earlyPlanClient{fakeClient: &fakeClient{journal: []copilot.SessionEvent{
		ev("prior", &rpc.ExitPlanModeRequestedData{RequestID: "prior", PlanContent: "# Prior"}),
		ev("current", &rpc.ExitPlanModeRequestedData{RequestID: "current", PlanContent: "# Early plan"}),
	}}, t: t, sink: sink, done: make(chan copilot.ExitPlanModeResult, 1)}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	conv, err := p.Open(t.Context(), agentapi.OpenRequest{ConversationID: "existing", Workdir: "/work", Events: sink})
	if err != nil {
		t.Fatal(err)
	}
	ix := pendingPlan(sink)
	if ix.Plan.Revision != 2 || ix.Plan.Previous != "# Prior" {
		t.Fatalf("early resumed review = %+v", ix.Plan)
	}
	if err := conv.Respond(t.Context(), ix.ID, agentapi.Answer{Plan: &agentapi.PlanAnswer{Feedback: "Revise"}}); err != nil {
		t.Fatal(err)
	}
	<-fc.done
}

func TestWebPlanStoredTextDoesNotRetainOversizedBacking(t *testing.T) {
	for _, label := range []bool{false, true} {
		t.Run(fmt.Sprint(label), func(t *testing.T) {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			versions, tr := func() (planVersions, *transcript) {
				text := strings.Repeat("x", 4<<20)
				var v planVersions
				tr := newTranscript()
				if label {
					tr.items(ev("review", &rpc.ExitPlanModeRequestedData{RequestID: "r", Summary: text}))
				} else {
					v.observe(text)
				}
				return v, tr
			}()
			runtime.GC()
			runtime.ReadMemStats(&after)
			runtime.KeepAlive(versions)
			runtime.KeepAlive(tr)
			if retained := int64(after.HeapAlloc) - int64(before.HeapAlloc); retained > 1<<20 {
				t.Fatalf("bounded plan text retained %d bytes", retained)
			}
		})
	}
}

func TestReadPlanReviewMarksTruncatedPreviousRevision(t *testing.T) {
	fc := &fakeClient{journal: []copilot.SessionEvent{
		ev("old", &rpc.ExitPlanModeRequestedData{RequestID: "old", PlanContent: strings.Repeat("x", maxPlanContent+1)}),
		ev("new", &rpc.ExitPlanModeRequestedData{RequestID: "new", PlanContent: "# Current"}),
	}}
	p := readerProvider(fc)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	plan, err := p.ReadPlanReview(t.Context(), agentapi.ReadRequest{ConversationID: "s"}, "new")
	if err != nil || plan.Truncated || !plan.PreviousTruncated || len(plan.Previous) > maxPlanContent {
		t.Fatalf("previous truncation = %+v %v", plan, err)
	}
}

func TestWebPlanSettledBodiesReloadFromTheNativeJournal(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	h.fc.journal = []copilot.SessionEvent{
		ev("prior", &rpc.ExitPlanModeRequestedData{RequestID: "prior", PlanContent: "# First"}),
		// The runtime may persist the new requested snapshot before it invokes
		// the blocking callback. Rehydration still needs the prior boundary.
		ev("new", &rpc.ExitPlanModeRequestedData{RequestID: "new", PlanContent: "# Second"}),
	}
	c.mu.Lock()
	c.plans.observe("# First")
	c.expirePlansLocked()
	retained := c.plans.current != "" || c.plans.previous != ""
	c.mu.Unlock()
	if retained {
		t.Fatal("settled conversation retains full plan bodies")
	}
	done := make(chan copilot.ExitPlanModeResult, 1)
	go func() {
		result, _ := h.fc.create[0].OnExitPlanModeRequest(copilot.ExitPlanModeRequest{PlanContent: "# Second", Actions: []string{"interactive"}}, copilot.ExitPlanModeInvocation{})
		done <- result
	}()
	waitFor(t, "rehydrated review", func() bool { return pendingPlan(h.sink) != nil })
	ix := pendingPlan(h.sink)
	if ix.Plan.Revision != 2 || ix.Plan.Previous != "# First" {
		t.Fatalf("prior native revision = %+v", ix.Plan)
	}
	if err := h.conv.Respond(t.Context(), ix.ID, agentapi.Answer{Plan: &agentapi.PlanAnswer{Feedback: "Revise"}}); err != nil {
		t.Fatal(err)
	}
	<-done
	h.fs.onEvent(ev("idle", &rpc.SessionIdleData{}))
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.plans.current != "" || c.plans.previous != "" {
		t.Fatal("turn settlement kept reader bodies")
	}
}

type blockedPlanJournalClient struct {
	*fakeClient
	entered, release chan struct{}
	once             sync.Once
}

type blockedPlanHistorySession struct {
	sdkSession
	entered, release chan struct{}
	events           []copilot.SessionEvent
}

func (s blockedPlanHistorySession) Events(context.Context) ([]copilot.SessionEvent, error) {
	close(s.entered)
	<-s.release
	return s.events, nil
}

func TestWebPlanBlockedHistoryCannotRestoreBodiesAfterExpiry(t *testing.T) {
	for _, action := range []string{"stop-new-turn", "close", "settle"} {
		t.Run(action, func(t *testing.T) {
			h := openWeb(t)
			c := h.conv.(*conversation)
			blocked := blockedPlanHistorySession{sdkSession: c.sess, entered: make(chan struct{}), release: make(chan struct{}), events: []copilot.SessionEvent{
				ev("prior", &rpc.ExitPlanModeRequestedData{RequestID: "prior", PlanContent: "# Prior"}),
			}}
			c.mu.Lock()
			c.sess = blocked
			c.startTurnLocked()
			c.mu.Unlock()
			done := make(chan struct{})
			go func() { _, _ = c.History(t.Context()); close(done) }()
			select {
			case <-blocked.entered:
			case <-time.After(time.Second):
				t.Fatal("History did not begin")
			}
			switch action {
			case "stop-new-turn":
				if err := c.Cancel(t.Context()); err != nil {
					t.Fatal(err)
				}
				h.fs.onEvent(ev("stopped", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
				h.fs.onEvent(ev("new-user", userMessage("new-user", rpc.UserMessageDeliveryIdle, "new turn")))
			case "close":
				if err := c.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "settle":
				h.fs.onEvent(ev("settled", &rpc.SessionIdleData{}))
			}
			close(blocked.release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("History did not return")
			}
			c.mu.Lock()
			retained, revision := c.plans.current != "" || c.plans.previous != "", c.plans.revision
			c.mu.Unlock()
			if retained || revision != 0 {
				t.Fatalf("expired read restored plan state: retained=%t revision=%d", retained, revision)
			}
		})
	}
}

func (f *blockedPlanJournalClient) ReadEvents(ctx context.Context, req *rpc.SessionsReadPersistedEventsRequest) (*rpc.EventsReadResult, error) {
	f.once.Do(func() {
		close(f.entered)
		select {
		case <-f.release:
		case <-ctx.Done():
		}
	})
	return f.fakeClient.ReadEvents(ctx, req)
}

func TestWebPlanBlockedRehydrationCannotSurviveExpiryOrANewTurn(t *testing.T) {
	for _, dropped := range []bool{false, true} {
		for _, action := range []string{"stop-new-turn", "close", "settle", "new-turn"} {
			t.Run(fmt.Sprintf("dropped=%t/%s", dropped, action), func(t *testing.T) {
				fc := &blockedPlanJournalClient{fakeClient: &fakeClient{journal: []copilot.SessionEvent{
					ev("prior", &rpc.ExitPlanModeRequestedData{RequestID: "prior", PlanContent: "# Prior"}),
				}}, entered: make(chan struct{}), release: make(chan struct{})}
				p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
				t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
				sink := &recSink{}
				conv, err := p.Open(t.Context(), agentapi.OpenRequest{SessionID: "blocked-plan", Workdir: "/work", Events: sink})
				if err != nil {
					t.Fatal(err)
				}
				c := conv.(*conversation)
				if dropped {
					c.mu.Lock()
					c.plans.observe("# Prior")
					c.plans.dropBodies()
					c.mu.Unlock()
				}
				type callbackResult struct {
					result copilot.ExitPlanModeResult
					err    error
				}
				done := make(chan callbackResult, 1)
				go func() {
					result, err := fc.create[0].OnExitPlanModeRequest(copilot.ExitPlanModeRequest{PlanContent: "# Next", Actions: []string{"interactive"}}, copilot.ExitPlanModeInvocation{})
					done <- callbackResult{result, err}
				}()
				select {
				case <-fc.entered:
				case <-time.After(time.Second):
					t.Fatal("review did not start its journal read")
				}
				switch action {
				case "stop-new-turn":
					if err := conv.Cancel(t.Context()); err != nil {
						t.Fatal(err)
					}
					fc.sessions[0].onEvent(ev("stopped", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
					fc.sessions[0].onEvent(ev("new-user", userMessage("new-user", rpc.UserMessageDeliveryIdle, "new turn")))
				case "close":
					if err := conv.Close(t.Context()); err != nil {
						t.Fatal(err)
					}
				case "settle":
					fc.sessions[0].onEvent(ev("settled", &rpc.SessionIdleData{}))
				case "new-turn":
					fc.sessions[0].onEvent(ev("new-user", userMessage("new-user", rpc.UserMessageDeliveryIdle, "new turn")))
				}
				close(fc.release)
				var returned callbackResult
				blocked := false
				select {
				case returned = <-done:
				case <-time.After(time.Second):
					blocked = true
				}
				c.mu.Lock()
				retained := c.plans.current != "" || c.plans.previous != ""
				pending := len(c.pending)
				if blocked {
					c.expirePlansLocked()
				}
				c.mu.Unlock()
				if blocked {
					returned = <-done
				}
				if blocked || pending != 0 || retained || returned.result.Approved || returned.result.SelectedAction != "" || returned.err == nil {
					t.Fatalf("stale review: blocked=%t pending=%d retained=%t result=%+v err=%v", blocked, pending, retained, returned.result, returned.err)
				}
				if pendingPlan(sink) != nil {
					t.Fatal("old review became actionable")
				}
			})
		}
	}
}

func pendingPlan(s *recSink) *agentapi.Interaction {
	events := s.all()
	seen := map[string]bool{}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Interaction == nil || seen[ev.Interaction.ID] {
			continue
		}
		seen[ev.Interaction.ID] = true
		if ev.Interaction != nil && ev.Interaction.Kind == agentapi.InteractionPlanReview && ev.Interaction.State == agentapi.InteractionPending {
			return ev.Interaction
		}
	}
	return nil
}

func planJournal(t *testing.T) []copilot.SessionEvent {
	t.Helper()
	action := rpc.ExitPlanModeActionInteractive
	records := []copilot.SessionEvent{
		ev("request-one", &rpc.ExitPlanModeRequestedData{RequestID: "r1", Summary: "Keep data", PlanContent: "# Plan\n1. Preserve data.", Actions: []rpc.ExitPlanModeAction{action}, RecommendedAction: action}),
		ev("decision-one", &rpc.HumanResponseRecordedData{RequestID: "r1", Response: rpc.HumanResponseRecordedResponseExitPlanMode{Summary: "Keep data", PlanContent: "# Plan\n1. Preserve data.", Approved: false, Feedback: copilot.String("Add the validation step")}}),
		ev("request-two", &rpc.ExitPlanModeRequestedData{RequestID: "r2", Summary: "Keep and validate data", PlanContent: "# Plan\n1. Preserve data.\n2. Validate it.", Actions: []rpc.ExitPlanModeAction{action}, RecommendedAction: action}),
		ev("completed-two", &rpc.ExitPlanModeCompletedData{RequestID: "r2", Approved: copilot.Bool(true), SelectedAction: &action}),
		ev("decision-two", &rpc.HumanResponseRecordedData{RequestID: "r2", Response: rpc.HumanResponseRecordedResponseExitPlanMode{Summary: "Keep and validate data", PlanContent: "# Plan\n1. Preserve data.\n2. Validate it.", Approved: true, SelectedAction: &action}}),
	}
	for i := range records {
		records[i].Timestamp = records[i].Timestamp.Add(time.Duration(i) * time.Second)
	}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestReadPlanReviewUsesTheExactHistoricalSnapshot(t *testing.T) {
	fc := &fakeClient{journal: planJournal(t), pageSize: 1}
	p := readerProvider(fc)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	req := agentapi.ReadRequest{ConversationID: "plans"}
	plan, err := p.ReadPlanReview(context.Background(), req, "r2")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Content != "# Plan\n1. Preserve data.\n2. Validate it." || plan.Previous != "# Plan\n1. Preserve data." || plan.Revision != 2 {
		t.Fatalf("historical snapshot = %+v", plan)
	}
	first, err := p.ReadPlanReview(context.Background(), req, "r1")
	if err != nil || first.Content != "# Plan\n1. Preserve data." || first.Previous != "" || first.Revision != 1 {
		t.Fatalf("first snapshot = %+v, %v", first, err)
	}
	if _, err := p.ReadPlanReview(context.Background(), req, "missing"); !errors.Is(err, agentapi.ErrItemNotFound) {
		t.Fatalf("missing review = %v", err)
	}
	h, err := p.ReadHistory(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Items) != 2 || h.Items[0].ID != "plan-r1" || h.Items[1].ID != "plan-r2" {
		t.Fatalf("review records = %+v", h.Items)
	}
	for _, item := range h.Items {
		if item.Plan == nil || item.Plan.Content != "" || item.Plan.Previous != "" {
			t.Fatalf("history retained a body: %+v", item)
		}
	}
	w, err := p.ReadHistoryWindow(context.Background(), agentapi.WindowRequest{ReadRequest: req, ItemID: "plan-r2"})
	if err != nil || len(w.Items) != 1 || !reflect.DeepEqual(w.Items[0], h.Items[1]) {
		t.Fatalf("cold window = %+v, %v", w, err)
	}
	if len(fc.create) != 0 || len(fc.resume) != 0 {
		t.Fatal("historical read opened the conversation")
	}
}

func TestWebPlanReviewWaitsForAnExplicitAnswer(t *testing.T) {
	for _, answer := range []agentapi.PlanAnswer{
		{Action: agentapi.PlanAutopilot}, {Action: agentapi.PlanAutopilotFleet},
		{Action: agentapi.PlanInteractive}, {Action: agentapi.PlanExitOnly},
		{Feedback: "Keep the existing data; revise step two."},
	} {
		t.Run(string(answer.Action)+answer.Feedback, func(t *testing.T) {
			h := openWeb(t)
			callback := h.fc.create[0].OnExitPlanModeRequest
			results := make(chan copilot.ExitPlanModeResult, 1)
			go func() {
				result, err := callback(copilot.ExitPlanModeRequest{Summary: "A safe migration", PlanContent: "# Plan\n1. Preserve data.", Actions: []string{"autopilot", "autopilot_fleet", "interactive", "exit_only"}, RecommendedAction: "interactive"}, copilot.ExitPlanModeInvocation{})
				if err != nil {
					results <- copilot.ExitPlanModeResult{Feedback: err.Error()}
					return
				}
				results <- result
			}()
			waitFor(t, "pending native plan review", func() bool { return pendingPlan(h.sink) != nil })
			select {
			case got := <-results:
				t.Fatalf("review answered before owner: %+v", got)
			default:
			}
			ix := pendingPlan(h.sink)
			if ix.Plan == nil || ix.Plan.Recommended != agentapi.PlanInteractive || ix.Plan.Content != "# Plan\n1. Preserve data." || ix.Plan.Revision != 1 {
				t.Fatalf("review = %+v", ix)
			}
			if err := h.conv.Respond(context.Background(), ix.ID, agentapi.Answer{Plan: &answer}); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-results:
				if got.Approved != (answer.Action != "") || got.SelectedAction != string(answer.Action) || got.Feedback != answer.Feedback {
					t.Fatalf("native result = %+v", got)
				}
			case <-time.After(time.Second):
				t.Fatal("plan callback stayed blocked after answer")
			}
			if err := h.conv.Respond(context.Background(), ix.ID, agentapi.Answer{Plan: &answer}); !errors.Is(err, agentapi.ErrInteractionGone) {
				t.Fatalf("second response = %v", err)
			}
			if len(h.fs.msgs) != 0 {
				t.Fatalf("review started another prompt: %+v", h.fs.msgs)
			}
			settled := h.sink.interaction(ix.ID)
			if settled.Plan != nil && (settled.Plan.Content != "" || settled.Plan.Previous != "") {
				t.Fatal("resolved interaction retained full plan bodies")
			}
		})
	}
}

func TestWebPlanReviewStopAndCloseRefuseImplementation(t *testing.T) {
	for _, closeTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "close"}[closeTask], func(t *testing.T) {
			h := openWeb(t)
			results := make(chan copilot.ExitPlanModeResult, 1)
			go func() {
				result, _ := h.fc.create[0].OnExitPlanModeRequest(copilot.ExitPlanModeRequest{PlanContent: "# Plan", Actions: []string{"interactive"}}, copilot.ExitPlanModeInvocation{})
				results <- result
			}()
			waitFor(t, "pending review", func() bool { return pendingPlan(h.sink) != nil })
			id := pendingPlan(h.sink).ID
			var err error
			if closeTask {
				err = h.conv.Close(context.Background())
			} else {
				err = h.conv.Cancel(context.Background())
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-results:
				if result.Approved || result.SelectedAction != "" || result.Feedback == "" {
					t.Fatalf("withdrawal result = %+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("withdrawal left callback blocked")
			}
			if ix := h.sink.interaction(id); ix.State != agentapi.InteractionExpired || ix.Plan.Content != "" || ix.Plan.Previous != "" {
				t.Fatalf("withdrawn review = %+v", ix)
			}
			if err := h.conv.Respond(context.Background(), id, agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive}}); err == nil {
				t.Fatal("late approval succeeded")
			}
			if result, err := h.fc.create[0].OnExitPlanModeRequest(copilot.ExitPlanModeRequest{PlanContent: "Late callback", Actions: []string{"interactive"}}, copilot.ExitPlanModeInvocation{}); err == nil || result.Approved {
				t.Fatalf("late review after withdrawal = %+v, %v", result, err)
			}
		})
	}
}

func TestWebPlanReviewRevisionsAndOfferedActions(t *testing.T) {
	h := openWeb(t)
	for i, content := range []string{"# First", "# Second", "# Second"} {
		results := make(chan copilot.ExitPlanModeResult, 1)
		go func() {
			result, _ := h.fc.create[0].OnExitPlanModeRequest(copilot.ExitPlanModeRequest{PlanContent: content, Actions: []string{"interactive", "unknown", "interactive"}, RecommendedAction: "autopilot"}, copilot.ExitPlanModeInvocation{})
			results <- result
		}()
		waitFor(t, "new review", func() bool { return pendingPlan(h.sink) != nil })
		ix := pendingPlan(h.sink)
		wantRevision := i + 1
		if i == 2 {
			wantRevision = 2
		}
		if ix.Plan.Revision != wantRevision || ix.Plan.Recommended != "" || !reflect.DeepEqual(ix.Plan.Actions, []agentapi.PlanAction{agentapi.PlanInteractive}) {
			t.Fatalf("review = %+v", ix.Plan)
		}
		if i > 0 && ix.Plan.Previous != "# First" {
			t.Fatalf("previous = %q", ix.Plan.Previous)
		}
		if err := h.conv.Respond(context.Background(), ix.ID, agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanAutopilot}}); err == nil {
			t.Fatal("unoffered action accepted")
		}
		if err := h.conv.Respond(context.Background(), ix.ID, agentapi.Answer{Plan: &agentapi.PlanAnswer{Feedback: "Please revise"}}); err != nil {
			t.Fatal(err)
		}
		if result := <-results; result.Approved || result.Feedback != "Please revise" {
			t.Fatalf("feedback = %+v", result)
		}
	}
}

func TestWebPlanCommandAppliesTheNativeMode(t *testing.T) {
	h, runtime := runtimeHarness(t)
	h.fs.commands = append(h.fs.commands, rpc.SlashCommandInfo{Name: "plan", Kind: rpc.SlashCommandKindBuiltin})
	mode := rpc.SessionModePlan
	h.fs.invoke = &rpc.SlashCommandAgentPromptResult{Prompt: "Produce the native plan", Mode: &mode}
	result, err := h.conv.(agentapi.CommandExecutor).ExecuteCommand(context.Background(), "plan", agentapi.Prompt{Text: "preserve data"})
	if err != nil || result != nil || !reflect.DeepEqual(runtime.sets, []rpc.SessionMode{mode}) || len(h.fs.msgs) != 1 {
		t.Fatalf("plan command = %+v, %v, modes %v, prompts %v", result, err, runtime.sets, h.fs.msgs)
	}
}

func TestSDKNativePlanModeRequiresReadbackAndNoModelHandoff(t *testing.T) {
	runtime := startFakeRuntime(t)
	session := openFakeSDKSession(t, runtime)
	runtime.set("session.mode.get", `"plan"`)
	runtime.set("session.mode.set", `{"modelChanged":false,"status":"ok"}`)
	if err := session.SetExecutionMode(t.Context(), rpc.SessionModePlan); err != nil || runtime.last("session.mode.set")["mode"] != "plan" {
		t.Fatalf("native plan mode = %v, %+v", err, runtime.last("session.mode.set"))
	}
	for _, response := range []string{`{"modelChanged":true,"status":"ok"}`, `{"modelChanged":false,"status":"ok","deferImplementation":true}`} {
		runtime.set("session.mode.set", response)
		if err := session.SetExecutionMode(t.Context(), rpc.SessionModePlan); err == nil {
			t.Fatal("plan mode accepted an unsupported model handoff or deferred implementation")
		}
	}
}

// The native request ID of a live plan receipt is not the review's ID: the
// receipt names the review its answer decided, so the reader finds the
// snapshot UAM kept for it. Another decision is not attributed to it.
func TestLivePlanReceiptNamesTheReviewItDecided(t *testing.T) {
	h := openWeb(t)
	callback := h.fc.create[0].OnExitPlanModeRequest
	done := make(chan struct{})
	go func() {
		_, _ = callback(copilot.ExitPlanModeRequest{Summary: "Migrate", PlanContent: "# Plan", Actions: []string{"interactive"}}, copilot.ExitPlanModeInvocation{})
		close(done)
	}()
	waitFor(t, "pending native plan review", func() bool { return pendingPlan(h.sink) != nil })
	ix := pendingPlan(h.sink)
	if !strings.HasPrefix(ix.ID, "plan-") {
		t.Fatalf("review ID = %q", ix.ID)
	}
	if err := h.conv.Respond(context.Background(), ix.ID, agentapi.Answer{Plan: &agentapi.PlanAnswer{Action: agentapi.PlanInteractive}}); err != nil {
		t.Fatal(err)
	}
	<-done
	receipt := func(native string, approved bool, action string) *agentapi.Item {
		d := &rpc.ExitPlanModeCompletedData{RequestID: native, Approved: copilot.Bool(approved)}
		if action != "" {
			a := rpc.ExitPlanModeAction(action)
			d.SelectedAction = &a
		}
		h.fs.onEvent(ev("completed-"+native, d))
		for _, e := range slices.Backward(h.sink.all()) {
			if e.Kind == agentapi.EventItem && e.Item.ID == "plan-"+native {
				return e.Item
			}
		}
		t.Fatalf("no receipt for %s", native)
		return nil
	}
	const native = "5b0c9b0e-6f1d-4d0a-9c39-0c7f3b1f2a11"
	if it := receipt("aaaaaaaa-0000-4000-8000-000000000000", false, ""); it.Plan == nil || it.Plan.RequestID != "aaaaaaaa-0000-4000-8000-000000000000" {
		t.Fatalf("unmatched decision = %+v", it.Plan)
	}
	if it := receipt(native, true, "interactive"); it.Plan == nil || it.Plan.RequestID != ix.ID {
		t.Fatalf("decided receipt = %+v", it.Plan)
	}
}
