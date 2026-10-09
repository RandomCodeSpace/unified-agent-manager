package copilot

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type elicitReply struct {
	res copilot.ElicitationResult
	err error
}

func elicitAsync(fs *fakeSession, req copilot.ElicitationContext) <-chan elicitReply {
	done := make(chan elicitReply, 1)
	go func() {
		res, err := fs.elicit(req)
		done <- elicitReply{res, err}
	}()
	return done
}

func strp(s string) *string { return &s }

func urlMode() *copilot.ElicitationRequestedMode {
	m := rpc.ElicitationRequestedModeURL
	return &m
}

// pendingElicitation waits for the newest elicitation to be pending.
func pendingElicitation(t *testing.T, h webHarness) *agentapi.Interaction {
	t.Helper()
	waitFor(t, "elicitation", func() bool {
		q := h.sink.question()
		return q != nil && q.Elicitation != nil && q.State == agentapi.InteractionPending
	})
	return h.sink.question()
}

func TestWebElicitationRegisteredOnCreateAndResume(t *testing.T) {
	h := openWeb(t)
	create := h.fc.create[0]
	if create.OnElicitationRequest == nil || create.AskUserVariant != copilot.AskUserVariantLegacy || create.OnUserInputRequest == nil {
		t.Fatalf("create: elicitation %v, ask_user variant %q", create.OnElicitationRequest != nil, create.AskUserVariant)
	}
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{ConversationID: "c-2", Workdir: "/w", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	resume := h.fc.resume[0]
	if resume.OnElicitationRequest == nil || resume.AskUserVariant != copilot.AskUserVariantLegacy || resume.OnUserInputRequest == nil {
		t.Fatalf("resume: elicitation %v, ask_user variant %q", resume.OnElicitationRequest != nil, resume.AskUserVariant)
	}
}

// A form shows each field as a typed question; the answer is checked
// against the fields, and sent as the SDK's accept with typed content.
func TestWebElicitationFormAccept(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	schema := &copilot.ElicitationSchema{
		Properties: map[string]any{
			"name":    map[string]any{"type": "string", "title": "Name", "minLength": 2.0},
			"count":   map[string]any{"type": "integer", "description": "How many", "minimum": 1.0, "maximum": 5.0},
			"channel": map[string]any{"type": "string", "oneOf": []any{map[string]any{"const": "stable", "title": "Stable"}, map[string]any{"const": "beta", "title": "Beta"}}},
			"ok":      map[string]any{"type": "boolean", "default": true},
			"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []any{"a", "b"}}, "maxItems": 2.0},
			"note":    map[string]any{"type": "string"},
		},
		Required: []string{"name", "ok"},
	}
	done := elicitAsync(h.fs, copilot.ElicitationContext{Message: "Configure the release", RequestedSchema: schema, ElicitationSource: strp("github")})
	q := pendingElicitation(t, h)
	var names []string
	for _, x := range q.Questions {
		names = append(names, x.Field.Name)
	}
	if !slices.Equal(names, []string{"channel", "count", "name", "note", "ok", "tags"}) || q.Title != "Form from github" || q.Detail != "Configure the release" || q.Elicitation.Mode != agentapi.ElicitationForm || q.Elicitation.Source != "github" {
		t.Fatalf("form = %+v (fields %v)", q, names)
	}
	if ch, count, ok, tags := q.Questions[0], q.Questions[1], q.Questions[4], q.Questions[5]; !slices.Equal(ch.Choices, []string{"Stable", "Beta"}) || ch.Custom ||
		count.Header != "count" || count.Text != "How many" || !count.Custom || !ok.Field.Required || !slices.Equal(ok.Choices, []string{"Yes", "No"}) || !tags.Multiple {
		t.Fatalf("questions = %+v", q.Questions)
	}

	if err := h.conv.Respond(ctx, q.ID, agentapi.Answer{Answers: [][]string{{}, {}, {"x"}, {}, {"Yes"}, {}}}); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("short name err = %v", err)
	}
	if err := h.conv.Respond(ctx, q.ID, agentapi.Answer{Answers: [][]string{{"Beta"}, {"3"}, {"v1"}, {}, {"No"}, {"a", "b"}}}); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	r := <-done
	want := map[string]copilot.ElicitationFieldValue{"channel": "beta", "count": 3.0, "name": "v1", "ok": false, "tags": []string{"a", "b"}}
	if r.err != nil || r.res.Action != copilot.ElicitationActionAccept || !reflect.DeepEqual(r.res.Content, want) {
		t.Fatalf("handler got %+v %v, want accept %v", r.res, r.err, want)
	}
	if got := h.sink.interaction(q.ID); got.State != agentapi.InteractionAnswered || got.Resolution != "answered" {
		t.Fatalf("answered form = %+v", got)
	}
	if err := h.conv.Respond(ctx, q.ID, agentapi.Answer{Reject: true}); !errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("second Respond err = %v", err)
	}
}

// Decline and cancel are the SDK's own actions, without content.
func TestWebElicitationDeclineAndCancel(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	schema := &copilot.ElicitationSchema{Properties: map[string]any{"name": map[string]any{"type": "string"}}}
	for _, tc := range []struct {
		answer     agentapi.Answer
		action     copilot.ElicitationAction
		resolution string
	}{
		{agentapi.Answer{Reject: true}, copilot.ElicitationActionDecline, "declined"},
		{agentapi.Answer{Cancel: true}, copilot.ElicitationActionCancel, "cancelled"},
	} {
		done := elicitAsync(h.fs, copilot.ElicitationContext{Message: "Name?", RequestedSchema: schema})
		q := pendingElicitation(t, h)
		if err := h.conv.Respond(ctx, q.ID, agentapi.Answer{Cancel: true, Answers: [][]string{{"x"}}}); err == nil {
			t.Fatal("cancel with answers accepted")
		}
		if err := h.conv.Respond(ctx, q.ID, tc.answer); err != nil {
			t.Fatal(err)
		}
		if r := <-done; r.err != nil || r.res.Action != tc.action || r.res.Content != nil {
			t.Fatalf("%s: handler got %+v %v", tc.resolution, r.res, r.err)
		}
		if got := h.sink.interaction(q.ID); got.State != agentapi.InteractionRejected || got.Resolution != tc.resolution {
			t.Fatalf("%s: interaction = %+v", tc.resolution, got)
		}
	}
	// ask_user questions keep their own answers: no cancel.
	done := askAsync(h.fs, copilot.UserInputRequest{Question: "Continue?"})
	waitFor(t, "question", func() bool {
		q := h.sink.question()
		return q.Elicitation == nil && q.State == agentapi.InteractionPending
	})
	if err := h.conv.Respond(ctx, h.sink.question().ID, agentapi.Answer{Cancel: true}); err == nil {
		t.Fatal("cancelled an ask_user question")
	}
	_ = h.conv.Respond(ctx, h.sink.question().ID, agentapi.Answer{Reject: true})
	<-done
}

// A form UAM cannot show whole is declined at once, and shown as declined.
func TestWebElicitationUnsupportedFormsAreDeclined(t *testing.T) {
	h := openWeb(t)
	many := map[string]any{}
	for i := range maxFormFields + 1 {
		many[string(rune('a'+i))] = map[string]any{"type": "string"}
	}
	for name, schema := range map[string]*copilot.ElicitationSchema{
		"pattern":      {Properties: map[string]any{"code": map[string]any{"type": "string", "pattern": "^[0-9]+$"}}},
		"object":       {Properties: map[string]any{"who": map[string]any{"type": "object"}}},
		"format":       {Properties: map[string]any{"when": map[string]any{"type": "string", "format": "time"}}},
		"no type":      {Properties: map[string]any{"x": map[string]any{"title": "X"}}},
		"min above":    {Properties: map[string]any{"n": map[string]any{"type": "number", "minimum": 5.0, "maximum": 1.0}}},
		"bad required": {Properties: map[string]any{"x": map[string]any{"type": "string"}}, Required: []string{"y"}},
		"repeat":       {Properties: map[string]any{"x": map[string]any{"type": "string", "enum": []any{"a", "a"}}}},
		"free items":   {Properties: map[string]any{"x": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}},
		"too many":     {Properties: many},
		"no schema":    nil,
	} {
		r, err := h.fs.elicit(copilot.ElicitationContext{Message: name, RequestedSchema: schema})
		if err != nil || r.Action != copilot.ElicitationActionDecline || r.Content != nil {
			t.Fatalf("%s: = %+v %v, want decline", name, r, err)
		}
		q := h.sink.question()
		if q.Detail != name || q.State != agentapi.InteractionRejected || !strings.HasPrefix(q.Resolution, "declined: ") {
			t.Fatalf("%s: interaction = %+v", name, q)
		}
	}
}

// A link is only shown: an https URL the user opens; accepting it sends
// accept without content. Any other link is declined.
func TestWebElicitationURL(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	link := "https://example.com/device?code=ABCD"
	done := elicitAsync(h.fs, copilot.ElicitationContext{Message: "Sign in", Mode: urlMode(), URL: &link, ElicitationSource: strp("github")})
	q := pendingElicitation(t, h)
	if q.Title != "Link from github" || q.Elicitation.Mode != agentapi.ElicitationURL || q.Elicitation.URL != link || len(q.Questions) != 0 {
		t.Fatalf("link = %+v", q)
	}
	if err := h.conv.Respond(ctx, q.ID, agentapi.Answer{}); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.err != nil || r.res.Action != copilot.ElicitationActionAccept || r.res.Content != nil {
		t.Fatalf("handler got %+v %v", r.res, r.err)
	}
	if got := h.sink.interaction(q.ID); got.State != agentapi.InteractionAnswered || got.Resolution != "accepted" {
		t.Fatalf("accepted link = %+v", got)
	}
	for _, bad := range []string{"", "http://example.com", "javascript:alert(1)", "https://user:pw@example.com", "https:///path", "https://example.com/a b", "https://example.com/" + strings.Repeat("a", maxElicitURL)} {
		r, err := h.fs.elicit(copilot.ElicitationContext{Message: "Open", Mode: urlMode(), URL: &bad})
		if err != nil || r.Action != copilot.ElicitationActionDecline {
			t.Fatalf("link %q = %+v %v, want decline", bad, r, err)
		}
		if q := h.sink.question(); q.State != agentapi.InteractionRejected || q.Elicitation.URL != "" {
			t.Fatalf("link %q interaction = %+v", bad, q)
		}
	}
}

// The CLI's events name the request: they link the elicitation to its tool
// call and agent, either side first, and one resolved without this client
// ends here too.
func TestWebElicitationLinksAndCompletesElsewhere(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	schema := &copilot.ElicitationSchema{Properties: map[string]any{"name": map[string]any{"type": "string"}}}
	sub := "agent-1"

	done := elicitAsync(h.fs, copilot.ElicitationContext{Message: "First", RequestedSchema: schema})
	first := pendingElicitation(t, h)
	h.fs.onEvent(copilot.SessionEvent{ID: "e1", AgentID: &sub, Data: &rpc.ElicitationRequestedData{RequestID: "r1", Message: "First", ToolCallID: strp("call-1"), RequestedSchema: &rpc.ElicitationRequestedSchema{}}})
	if got := h.sink.interaction(first.ID); got.ToolCallID != "call-1" || got.AgentID != sub || got.State != agentapi.InteractionPending {
		t.Fatalf("linked after the callback = %+v", got)
	}
	cancel := rpc.ElicitationCompletedActionCancel
	h.fs.onEvent(ev("e2", &rpc.ElicitationCompletedData{RequestID: "r1", Action: &cancel}))
	if r := <-done; r.err == nil {
		t.Fatalf("handler resolved elsewhere got %+v, want an error", r.res)
	}
	if got := h.sink.interaction(first.ID); got.State != agentapi.InteractionExpired || got.Resolution != "cancelled" {
		t.Fatalf("resolved elsewhere = %+v", got)
	}
	if err := h.conv.Respond(ctx, first.ID, agentapi.Answer{Cancel: true}); !errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("Respond after completion err = %v", err)
	}

	// The event first: the elicitation arrives linked. Its own answer's
	// completion event changes nothing.
	h.fs.onEvent(ev("e3", &rpc.ElicitationRequestedData{RequestID: "r2", Message: "Second", ToolCallID: strp("call-2")}))
	done = elicitAsync(h.fs, copilot.ElicitationContext{Message: "Second", RequestedSchema: schema})
	second := pendingElicitation(t, h)
	if second.ToolCallID != "call-2" {
		t.Fatalf("linked before the callback = %+v", second)
	}
	if err := h.conv.Respond(ctx, second.ID, agentapi.Answer{Reject: true}); err != nil {
		t.Fatal(err)
	}
	<-done
	accept := rpc.ElicitationCompletedActionAccept
	h.fs.onEvent(ev("e4", &rpc.ElicitationCompletedData{RequestID: "r2", Action: &accept}))
	if got := h.sink.interaction(second.ID); got.State != agentapi.InteractionRejected || got.Resolution != "declined" {
		t.Fatalf("own answer = %+v", got)
	}
}

// Close (and every other end of the conversation) releases a waiting
// elicitation with an error, which the SDK sends as cancel.
func TestWebCloseReleasesPendingElicitations(t *testing.T) {
	h := openWeb(t)
	link := "https://example.com"
	done := elicitAsync(h.fs, copilot.ElicitationContext{Message: "Open", Mode: urlMode(), URL: &link})
	q := pendingElicitation(t, h)
	if err := h.conv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.err == nil {
		t.Fatalf("handler after Close got %+v, want an error", r.res)
	}
	if got := h.sink.interaction(q.ID); got.State != agentapi.InteractionExpired {
		t.Fatalf("after Close = %+v", got)
	}
	if r, err := h.fs.elicit(copilot.ElicitationContext{Message: "Late", Mode: urlMode(), URL: &link}); err == nil {
		t.Fatalf("elicitation after Close = %+v, want an error", r)
	}
}
