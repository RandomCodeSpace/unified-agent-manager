package copilot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type autoResult struct {
	response copilot.AutoModeSwitchResponse
	err      error
}

func autoAsync(handler copilot.AutoModeSwitchRequestHandler, req copilot.AutoModeSwitchRequest, sessionID string) <-chan autoResult {
	done := make(chan autoResult, 1)
	go func() {
		r, err := handler(req, copilot.AutoModeSwitchInvocation{SessionID: sessionID})
		done <- autoResult{r, err}
	}()
	return done
}

func autoHandler(t *testing.T, h webHarness) copilot.AutoModeSwitchRequestHandler {
	t.Helper()
	handler := h.fc.create[0].OnAutoModeSwitchRequest
	if handler == nil {
		t.Fatal("create does not register Auto fallback handler")
	}
	return handler
}

func TestWebAutoCreatesAndResumesWithConfirmation(t *testing.T) {
	h := openWeb(t)
	_ = autoHandler(t, h)
	sink := &recSink{}
	conv, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-2", ConversationID: "conv-2", Workdir: "/work", Events: sink})
	if err != nil {
		t.Fatal(err)
	}
	if h.fc.resume[0].OnAutoModeSwitchRequest == nil {
		t.Fatal("resume does not register Auto fallback handler")
	}
	done := autoAsync(h.fc.resume[0].OnAutoModeSwitchRequest, copilot.AutoModeSwitchRequest{}, "conv-2")
	waitFor(t, "resumed Auto question", func() bool { return sink.question() != nil })
	if err := conv.Respond(context.Background(), sink.question().ID, agentapi.Answer{Answers: [][]string{{"No"}}}); err != nil {
		t.Fatal(err)
	}
	if r := <-done; string(r.response) != "no" || r.err != nil {
		t.Fatalf("resumed response = %+v", r)
	}
}

func TestWebAutoExplicitAnswerEveryRequest(t *testing.T) {
	h := openWeb(t)
	handler := autoHandler(t, h)
	var previous string
	for _, choice := range []string{"Yes", "No", "Yes"} {
		done := autoAsync(handler, copilot.AutoModeSwitchRequest{}, "s-1")
		waitFor(t, "fresh Auto question", func() bool {
			q := h.sink.question()
			return q != nil && q.State == agentapi.InteractionPending && q.ID != previous
		})
		q := h.sink.question()
		if q.ID == previous || q.ToolCallID != "" || q.AgentID != "" || len(q.Questions) != 1 {
			t.Fatalf("Auto question = %+v", q)
		}
		question := q.Questions[0]
		if question.Custom || question.Multiple || strings.Join(question.Choices, ",") != "No,Yes" || !strings.Contains(question.Text, "cost") {
			t.Fatalf("Auto choices/warning = %+v", question)
		}
		if err := h.conv.Respond(context.Background(), q.ID, agentapi.Answer{Answers: [][]string{{choice}}}); err != nil {
			t.Fatal(err)
		}
		r := <-done
		want := strings.ToLower(choice)
		if string(r.response) != want || r.err != nil {
			t.Fatalf("%s response = %+v", choice, r)
		}
		if err := h.conv.Respond(context.Background(), q.ID, agentapi.Answer{Answers: [][]string{{"Yes"}}}); !errors.Is(err, agentapi.ErrInteractionGone) {
			t.Fatalf("second answer = %v", err)
		}
		previous = q.ID
	}
	if len(h.fs.modelRequests) != 0 {
		t.Fatalf("consent called SwitchModel: %+v", h.fs.modelRequests)
	}
}

func TestWebAutoRejectsAutomaticAndInvalidAnswers(t *testing.T) {
	h := openWeb(t)
	done := autoAsync(autoHandler(t, h), copilot.AutoModeSwitchRequest{}, "s-1")
	waitFor(t, "Auto question", func() bool { return h.sink.question() != nil })
	q := h.sink.question()
	for _, ans := range []agentapi.Answer{
		{Answers: [][]string{{"Yes"}}, Auto: true},
		{Answers: [][]string{{"yes_always"}}},
		{Answers: [][]string{{"Yes", "No"}}},
		{},
		{Decision: "allow"},
	} {
		if err := h.conv.Respond(context.Background(), q.ID, ans); err == nil {
			t.Fatalf("invalid/automatic answer accepted: %+v", ans)
		}
	}
	if err := h.conv.Respond(context.Background(), q.ID, agentapi.Answer{Reject: true}); err != nil {
		t.Fatal(err)
	}
	if r := <-done; string(r.response) != "no" || r.err != nil {
		t.Fatalf("rejection = %+v", r)
	}
}

func TestWebAutoRetryOnlyReportsValidProviderData(t *testing.T) {
	h := openWeb(t)
	handler := autoHandler(t, h)
	previous := ""
	for _, value := range []float64{-1, math.NaN(), math.Inf(1), 60, 0.5} {
		done := autoAsync(handler, copilot.AutoModeSwitchRequest{RetryAfterSeconds: &value}, "s-1")
		waitFor(t, "fresh retry question", func() bool { return h.sink.question() != nil && h.sink.question().ID != previous })
		q := h.sink.question()
		invalid := value < 0 || math.IsNaN(value) || math.IsInf(value, 0)
		if strings.Contains(q.Questions[0].Text, "reset") == invalid {
			t.Fatalf("retry fact for %v: %q", value, q.Questions[0].Text)
		}
		if !invalid && !strings.Contains(q.Questions[0].Text, fmt.Sprintf("%g seconds", value)) {
			t.Fatalf("retry value changed: %q", q.Questions[0].Text)
		}
		if err := h.conv.Respond(context.Background(), q.ID, agentapi.Answer{Answers: [][]string{{"No"}}}); err != nil {
			t.Fatal(err)
		}
		if r := <-done; string(r.response) != "no" || r.err != nil {
			t.Fatalf("retry answer = %+v", r)
		}
		previous = q.ID
	}
}

func TestWebAutoIdenticalCallbacksKeepIndependentReplies(t *testing.T) {
	h := openWeb(t)
	handler := autoHandler(t, h)
	first := autoAsync(handler, copilot.AutoModeSwitchRequest{}, "s-1")
	waitFor(t, "first question", func() bool { return h.sink.question() != nil })
	firstID := h.sink.question().ID
	second := autoAsync(handler, copilot.AutoModeSwitchRequest{}, "s-1")
	waitFor(t, "second question", func() bool { return h.sink.question().ID != firstID })
	secondID := h.sink.question().ID
	h.fs.onEvent(ev("same-data-request", &rpc.AutoModeSwitchRequestedData{RequestID: "unprovable"}))
	h.fs.onEvent(ev("same-data-completion", &rpc.AutoModeSwitchCompletedData{RequestID: "unprovable", Response: rpc.AutoModeSwitchResponseYesAlways}))
	for _, id := range []string{firstID, secondID} {
		if q := h.sink.interaction(id); q.State != agentapi.InteractionPending {
			t.Fatalf("native notification dismissed private question: %+v", q)
		}
	}
	if err := h.conv.Respond(context.Background(), firstID, agentapi.Answer{Answers: [][]string{{"Yes"}}}); err != nil {
		t.Fatal(err)
	}
	if r := <-first; string(r.response) != "yes" || r.err != nil {
		t.Fatalf("first callback = %+v", r)
	}
	if q := h.sink.interaction(secondID); q.State != agentapi.InteractionPending {
		t.Fatalf("first reply settled second: %+v", q)
	}
	if err := h.conv.Respond(context.Background(), secondID, agentapi.Answer{Answers: [][]string{{"No"}}}); err != nil {
		t.Fatal(err)
	}
	if r := <-second; string(r.response) != "no" || r.err != nil {
		t.Fatalf("second callback = %+v", r)
	}
}

func TestWebAutoNativeEventsDoNotCreateOrReplayConsent(t *testing.T) {
	h := openWeb(t)
	for i := 0; i < 64; i++ {
		h.fs.onEvent(ev(fmt.Sprintf("e%d", i), &rpc.AutoModeSwitchRequestedData{RequestID: fmt.Sprintf("r%d", i)}))
	}
	if h.sink.question() != nil {
		t.Fatal("native requests created a question without a callback")
	}
	h.fs.events = []copilot.SessionEvent{ev("historical-request", &rpc.AutoModeSwitchRequestedData{RequestID: "old"}), ev("historical-completed", &rpc.AutoModeSwitchCompletedData{RequestID: "old", Response: rpc.AutoModeSwitchResponseYesAlways})}
	if _, err := h.conv.History(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.sink.question() != nil {
		t.Fatal("historical replay created consent question")
	}
}

func TestWebAutoCloseAndWrongSessionRefuse(t *testing.T) {
	h := openWeb(t)
	handler := autoHandler(t, h)
	if r, err := handler(copilot.AutoModeSwitchRequest{}, copilot.AutoModeSwitchInvocation{SessionID: "other"}); string(r) != "no" || err != nil {
		t.Fatalf("wrong session = %q, %v", r, err)
	}
	if h.sink.question() != nil {
		t.Fatal("wrong-session callback prompted owner")
	}
	done := autoAsync(handler, copilot.AutoModeSwitchRequest{}, "s-1")
	waitFor(t, "Auto question", func() bool { return h.sink.question() != nil })
	if err := h.conv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := <-done; string(r.response) != "no" || r.err != nil {
		t.Fatalf("close = %+v", r)
	}
	if h.sink.question().State != agentapi.InteractionExpired {
		t.Fatalf("close question = %+v", h.sink.question())
	}
	if r, err := handler(copilot.AutoModeSwitchRequest{}, copilot.AutoModeSwitchInvocation{SessionID: "s-1"}); string(r) != "no" || err != nil {
		t.Fatalf("closed callback = %q, %v", r, err)
	}
}

func TestWebAutoDelayedNativeNotificationsCannotDismissNewQuestion(t *testing.T) {
	h := openWeb(t)
	handler := autoHandler(t, h)
	first := autoAsync(handler, copilot.AutoModeSwitchRequest{}, "s-1")
	waitFor(t, "first Auto question", func() bool { return h.sink.question() != nil })
	firstID := h.sink.question().ID
	if err := h.conv.Respond(context.Background(), firstID, agentapi.Answer{Answers: [][]string{{"No"}}}); err != nil {
		t.Fatal(err)
	}
	<-first
	second := autoAsync(handler, copilot.AutoModeSwitchRequest{}, "s-1")
	waitFor(t, "second Auto question", func() bool { return h.sink.question().ID != firstID })
	secondID := h.sink.question().ID
	// Identical data is not an identity, including notifications arriving long
	// after an earlier callback has returned and a new one is waiting.
	h.fs.onEvent(ev("late-request", &rpc.AutoModeSwitchRequestedData{RequestID: "old"}))
	h.fs.onEvent(ev("late-completion", &rpc.AutoModeSwitchCompletedData{RequestID: "old", Response: rpc.AutoModeSwitchResponseYesAlways}))
	if q := h.sink.interaction(secondID); q.State != agentapi.InteractionPending {
		t.Fatalf("old native notification dismissed new question: %+v", q)
	}
	if err := h.conv.Respond(context.Background(), secondID, agentapi.Answer{Answers: [][]string{{"No"}}}); err != nil {
		t.Fatal(err)
	}
	if r := <-second; string(r.response) != "no" || r.err != nil {
		t.Fatalf("native response forged affirmative consent: %+v", r)
	}
}

func TestWebAutoStopRefusesPendingQuestion(t *testing.T) {
	h := openWeb(t)
	done := autoAsync(autoHandler(t, h), copilot.AutoModeSwitchRequest{}, "s-1")
	waitFor(t, "Auto question", func() bool { return h.sink.question() != nil })
	if err := h.conv.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stopped question", func() bool { return h.sink.question().State == agentapi.InteractionExpired })
	if r := <-done; string(r.response) != "no" || r.err != nil {
		t.Fatalf("stop = %+v", r)
	}
}

type blockedAutoAbortSession struct {
	*fakeSession
	entered, release chan struct{}
}

func (s *blockedAutoAbortSession) Abort(ctx context.Context) error {
	close(s.entered)
	select {
	case <-s.release:
		return s.fakeSession.Abort(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestWebAutoStopWithdrawsBeforeBlockedAbortReturns(t *testing.T) {
	h := openWeb(t)
	done := autoAsync(autoHandler(t, h), copilot.AutoModeSwitchRequest{}, "s-1")
	waitFor(t, "Auto question", func() bool { return h.sink.question() != nil })
	id := h.sink.question().ID
	blocked := &blockedAutoAbortSession{fakeSession: h.fs, entered: make(chan struct{}), release: make(chan struct{})}
	c := h.conv.(*conversation)
	c.mu.Lock()
	c.sess = blocked
	c.mu.Unlock()
	stopDone := make(chan error, 1)
	go func() { stopDone <- h.conv.Cancel(context.Background()) }()
	defer func() {
		close(blocked.release)
		select {
		case err := <-stopDone:
			if err != nil {
				t.Errorf("Stop after Abort release: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Stop did not finish after Abort release")
		}
	}()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		t.Fatal("Stop did not reach Abort")
	}
	if q := h.sink.interaction(id); q.State != agentapi.InteractionExpired {
		t.Fatalf("Auto remains answerable while Abort blocks: %+v", q)
	}
	if err := h.conv.Respond(context.Background(), id, agentapi.Answer{Answers: [][]string{{"Yes"}}}); !errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("Yes after Stop claim = %v", err)
	}
	select {
	case r := <-done:
		if string(r.response) != "no" || r.err != nil {
			t.Fatalf("callback before Abort release = %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("Auto callback remained blocked until Abort returned")
	}
	select {
	case err := <-stopDone:
		t.Fatalf("Stop returned before Abort release: %v", err)
	default:
	}
}

func TestWebAutoExpiryRefusesItsPendingCallback(t *testing.T) {
	h := openWeb(t)
	done := autoAsync(autoHandler(t, h), copilot.AutoModeSwitchRequest{}, "s-1")
	waitFor(t, "Auto question", func() bool { return h.sink.question() != nil })
	c := h.conv.(*conversation)
	c.mu.Lock()
	c.expireLocked()
	c.mu.Unlock()
	if r := <-done; string(r.response) != "no" || r.err != nil {
		t.Fatalf("expired callback = %+v", r)
	}
}

func TestWebAutoNativeResolutionCannotReplaceExplicitAnswer(t *testing.T) {
	h := openWeb(t)
	done := autoAsync(autoHandler(t, h), copilot.AutoModeSwitchRequest{}, "s-1")
	waitFor(t, "Auto question", func() bool { return h.sink.question() != nil })
	h.fs.onEvent(ev("native-request", &rpc.AutoModeSwitchRequestedData{RequestID: "not-a-callback-id"}))
	h.fs.onEvent(ev("native-complete", &rpc.AutoModeSwitchCompletedData{RequestID: "not-a-callback-id", Response: rpc.AutoModeSwitchResponseNo}))
	if q := h.sink.question(); q.State != agentapi.InteractionPending {
		t.Fatalf("native resolution changed private callback: %+v", q)
	}
	if err := h.conv.Respond(context.Background(), h.sink.question().ID, agentapi.Answer{Answers: [][]string{{"Yes"}}}); err != nil {
		t.Fatal(err)
	}
	if r := <-done; string(r.response) != "yes" || r.err != nil {
		t.Fatalf("native resolution replaced owner choice: %+v", r)
	}
}

func TestWebAutoModelSelectionComesOnlyFromMainConfirmedChange(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.mu.Lock()
	c.selected = "old"
	c.mu.Unlock()
	cause, wrong, old, low := "rate_limit_auto_switch", "refusal_fallback", "old", "low"
	tier := rpc.ContextTier("default")
	count := func() int {
		n := 0
		for _, e := range h.sink.all() {
			if e.Kind == agentapi.EventModelSelection {
				n++
			}
		}
		return n
	}
	child := "child"
	h.fs.onEvent(copilot.SessionEvent{ID: "child", AgentID: &child, Data: &rpc.SessionModelChangeData{NewModel: "auto", Cause: &cause}})
	h.fs.onEvent(ev("other-cause", &rpc.SessionModelChangeData{NewModel: "auto", Cause: &wrong}))
	h.fs.onEvent(ev("bad-model", &rpc.SessionModelChangeData{NewModel: strings.Repeat("x", 1000), Cause: &cause}))
	if n := count(); n != 0 {
		t.Fatalf("non-authoritative changes published %d selections", n)
	}
	h.fs.onEvent(ev("actual-usage", &rpc.AssistantUsageData{Model: "routed-actual"}))
	h.fs.onEvent(ev("actual", &rpc.SessionModelChangeData{NewModel: "auto", PreviousModel: &old, Cause: &cause, ReasoningEffort: &low, ContextTier: &tier}))
	if n := count(); n != 1 {
		t.Fatalf("confirmed selection count = %d", n)
	}
	var got *agentapi.ModelSelection
	for _, e := range h.sink.all() {
		if e.Kind == agentapi.EventModelSelection {
			got = e.ModelSelection
		}
	}
	if got.Model != "auto" || got.Effort == nil || *got.Effort != low || got.ContextSize == nil || *got.ContextSize != string(tier) {
		t.Fatalf("confirmed facts = %+v", got)
	}
	// Optional provider fields stay unknown; they are not invented defaults.
	auto := "auto"
	h.fs.onEvent(ev("next", &rpc.SessionModelChangeData{NewModel: "next", PreviousModel: &auto, Cause: &cause}))
	for _, e := range h.sink.all() {
		if e.Kind == agentapi.EventModelSelection {
			got = e.ModelSelection
		}
	}
	if got.Model != "next" || got.Effort != nil || got.ContextSize != nil {
		t.Fatalf("unknown optional facts = %+v", got)
	}
	// A delayed change from before a manual selection cannot overwrite it.
	h.fs.onEvent(ev("stale", &rpc.SessionModelChangeData{NewModel: "stale", PreviousModel: &old, Cause: &cause}))
	if n := count(); n != 2 {
		t.Fatalf("stale previous-model change published: %d", n)
	}
	c.mu.Lock()
	actualTurnModel := c.turnModel
	c.mu.Unlock()
	if actualTurnModel != "routed-actual" {
		t.Fatalf("selection overwrote actual usage provenance: %q", actualTurnModel)
	}
	if len(h.fs.modelRequests) != 0 {
		t.Fatal("confirmed event invoked SwitchModel")
	}
}
