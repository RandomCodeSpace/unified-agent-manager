package copilot

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestWebPermissionDetailsAndSessionScope(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		prompt      rpc.PermissionPromptRequest
		details     []string
		approval    rpc.PermissionDecisionApproveForSessionApproval
	}{
		{"shell escalation", "Run shell command", &rpc.PermissionPromptRequestCommands{FullCommandText: "build release", Warning: option("untrusted script"), RequestSandboxBypass: option(true), RequestSandboxBypassReason: option("needs network")}, []string{"build release", "Warning: untrusted script", "outside the sandbox", "needs network"}, nil},
		{"write", "Write file", &rpc.PermissionPromptRequestWrite{FileName: "app.go", Diff: "-old\n+new", CanOfferSessionApproval: true}, []string{"app.go", "-old\n+new"}, &rpc.PermissionDecisionApproveForSessionApprovalWrite{}},
		{"write without session approval", "Write file", &rpc.PermissionPromptRequestWrite{FileName: "app.go", Diff: "+new"}, []string{"app.go", "+new"}, nil},
		{"read", "Read file", &rpc.PermissionPromptRequestRead{Path: "/work/config"}, []string{"/work/config"}, &rpc.PermissionDecisionApproveForSessionApprovalRead{}},
		{"external paths", "Access paths outside the workspace", &rpc.PermissionPromptRequestPath{Paths: []string{"/outside/a", "/outside/b"}}, []string{"/outside/a\n/outside/b"}, nil},
		{"URL escalation", "Fetch URL", &rpc.PermissionPromptRequestURL{URL: "https://example.com", RequestSandboxBypass: option(true)}, []string{"https://example.com", "outside the sandbox"}, nil},
		{"MCP tool", "Run MCP tool project/search", &rpc.PermissionPromptRequestMCP{ServerName: "project", ToolName: "search", Args: map[string]string{"query": "needle"}}, []string{"query", "needle"}, &rpc.PermissionDecisionApproveForSessionApprovalMCP{ServerName: "project", ToolName: option("search")}},
		{"custom tool", "Run tool deploy", &rpc.PermissionPromptRequestCustomTool{ToolName: "deploy", Args: map[string]string{"environment": "staging"}}, []string{"environment", "staging"}, &rpc.PermissionDecisionApproveForSessionApprovalCustomTool{ToolName: "deploy"}},
		{"memory", "Store memory", &rpc.PermissionPromptRequestMemory{Fact: "use Go 1.26.5"}, []string{"use Go 1.26.5"}, &rpc.PermissionDecisionApproveForSessionApprovalMemory{}},
		{"hook", "Confirm deploy", &rpc.PermissionPromptRequestHook{ToolName: "deploy", HookMessage: option("Requires review"), ToolArgs: map[string]string{"target": "staging"}}, []string{"Requires review", "target", "staging"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := openWeb(t)
			req := shellRequest("permission")
			req.PromptRequest = tc.prompt
			h.fs.onEvent(ev("request", req))
			ix := h.sink.interaction(req.RequestID)
			if ix == nil || ix.Title != tc.title || ix.State != agentapi.InteractionPending {
				t.Fatalf("permission = %+v", ix)
			}
			for _, detail := range tc.details {
				if !strings.Contains(ix.Detail, detail) {
					t.Errorf("permission detail %q omits %q", ix.Detail, detail)
				}
			}
			hasSessionOption := false
			for _, opt := range ix.Options {
				if opt.ID == "approve_session" {
					hasSessionOption = true
					if opt.AllowOnce {
						t.Fatalf("session option marked for automatic approval: %+v", opt)
					}
				}
			}
			if hasSessionOption != (tc.approval != nil) {
				t.Fatalf("session option offered=%v, supported=%v", hasSessionOption, tc.approval != nil)
			}
			err := h.conv.Respond(context.Background(), req.RequestID, agentapi.Answer{Decision: "approve_session"})
			if tc.approval == nil {
				if err == nil || h.fs.answer(req.RequestID) != nil || h.sink.interaction(req.RequestID).State != agentapi.InteractionPending {
					t.Fatalf("unsupported session approval reached provider: %v, %v", err, h.fs.answer(req.RequestID))
				}
				return
			}
			decision, ok := h.fs.answer(req.RequestID).(*rpc.PermissionDecisionApproveForSession)
			if err != nil || !ok || !reflect.DeepEqual(decision.Approval, tc.approval) {
				t.Fatalf("session scope = %#v, err %v; want %#v", decision, err, tc.approval)
			}
		})
	}
}

func TestWebUnreadablePermissionRemainsActionableWithoutSessionGrant(t *testing.T) {
	for _, request := range []rpc.PermissionRequest{nil, &rpc.RawPermissionRequest{Discriminator: "future-kind", Raw: []byte(`{"kind":"future-kind","operation":"publish"}`)}} {
		h := openWeb(t)
		h.fs.onEvent(ev("future", &rpc.PermissionRequestedData{RequestID: "unknown", PermissionRequest: request}))
		ix := h.sink.interaction("unknown")
		if ix == nil || !strings.HasPrefix(ix.Title, "Permission request:") || len(ix.Options) != 2 {
			t.Fatalf("unreadable permission = %+v", ix)
		}
		if request != nil && (!strings.Contains(ix.Title, "future-kind") || !strings.Contains(ix.Detail, "publish")) {
			t.Fatalf("unknown permission lost provider details: %+v", ix)
		}
		if err := h.conv.Respond(context.Background(), "unknown", agentapi.Answer{Decision: "reject"}); err != nil {
			t.Fatal(err)
		}
		if _, ok := h.fs.answer("unknown").(*rpc.PermissionDecisionReject); !ok || h.sink.interaction("unknown").State != agentapi.InteractionRejected {
			t.Fatalf("unreadable request could not be denied: %+v", h.sink.interaction("unknown"))
		}
	}
}

func TestWebPermissionReplyFailureAllowsExplicitRetry(t *testing.T) {
	h := openWeb(t)
	h.fs.onEvent(ev("request", shellRequest("p")))
	h.fs.respondHook = func(context.Context, string, rpc.PermissionDecision) (bool, error) {
		return false, errors.New("transport unavailable")
	}
	if err := h.conv.Respond(context.Background(), "p", agentapi.Answer{Decision: "approve_once"}); err == nil || !strings.Contains(err.Error(), "transport unavailable") {
		t.Fatalf("provider failure = %v", err)
	}
	if ix := h.sink.interaction("p"); ix.State != agentapi.InteractionPending {
		t.Fatalf("failed reply resolved the permission: %+v", ix)
	}
	h.fs.respondHook = nil
	if err := h.conv.Respond(context.Background(), "p", agentapi.Answer{Decision: "reject"}); err != nil {
		t.Fatalf("explicit retry: %v", err)
	}
	if ix := h.sink.interaction("p"); ix.State != agentapi.InteractionRejected || ix.Resolution != "Deny" {
		t.Fatalf("retry = %+v", ix)
	}
}

func TestWebPermissionReplyRacingChildCancellationDoesNotReviveRequest(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider withdrew", true: "provider accepted"}[applied], func(t *testing.T) {
			h := openWeb(t)
			h.fs.onEvent(agentEv("start", "child", &rpc.SubagentStartedData{ToolCallID: "call"}))
			h.fs.onEvent(agentEv("request", "child", shellRequest("p")))
			h.fs.respondHook = func(context.Context, string, rpc.PermissionDecision) (bool, error) {
				if err := h.conv.Respond(context.Background(), "p", agentapi.Answer{Decision: "reject"}); !errors.Is(err, agentapi.ErrInteractionGone) {
					t.Fatalf("second answer won while the first was in flight: %v", err)
				}
				before := len(h.sink.all())
				h.fs.onEvent(agentEv("replayed", "child", shellRequest("p")))
				h.fs.onEvent(ev("completion", &rpc.PermissionCompletedData{RequestID: "p", Result: &rpc.PermissionApproved{}}))
				if len(h.sink.all()) != before {
					t.Fatal("provider replay replaced the in-flight answer")
				}
				h.fs.onEvent(agentEv("cancelled", "child", &rpc.SubagentCompletedData{ToolCallID: "call", Cancelled: option(true)}))
				return applied, nil
			}
			err := h.conv.Respond(context.Background(), "p", agentapi.Answer{Decision: "approve_once"})
			if applied && err != nil || !applied && !errors.Is(err, agentapi.ErrInteractionGone) {
				t.Fatalf("reply applied=%v returned %v", applied, err)
			}
			if ix := h.sink.interaction("p"); ix.State != agentapi.InteractionExpired {
				t.Fatalf("late reply revived cancelled permission: %+v", ix)
			}
		})
	}
}

func TestWebCancelFailureDoesNotPretendTheTurnEnded(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	if err := h.conv.Send(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	h.fs.abortErr = errors.New("abort unavailable")
	before := len(h.sink.all())
	if err := h.conv.Cancel(ctx); err == nil || !strings.Contains(err.Error(), "abort unavailable") {
		t.Fatalf("failed Cancel = %v", err)
	}
	if len(h.sink.all()) != before {
		t.Fatal("failed Cancel reported a turn transition")
	}
	h.fs.abortErr = nil
	if err := h.conv.Cancel(ctx); err != nil || h.fs.aborts != 2 || len(h.sink.all()) != before {
		t.Fatalf("Cancel retry = %v, aborts %d", err, h.fs.aborts)
	}
	h.fs.onEvent(ev("idle", &rpc.SessionIdleData{Aborted: option(true)}))
	if got := h.sink.last().Turn; got == nil || got.State != agentapi.TurnCancelled {
		t.Fatalf("provider cancellation = %+v", got)
	}
	if err := h.conv.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.conv.Cancel(ctx); !errors.Is(err, agentapi.ErrClosed) || h.fs.aborts != 2 {
		t.Fatalf("closed Cancel = %v, aborts %d", err, h.fs.aborts)
	}
}

func TestWebShutdownFailureExpiresRequestsAndKeepsOtherSessionsOpen(t *testing.T) {
	for _, reason := range []*string{nil, option("backend\x1b[31m stopped\nretry later")} {
		h := openWeb(t)
		otherSink := &recSink{}
		other, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "other", Events: otherSink})
		if err != nil {
			t.Fatal(err)
		}
		h.fs.onEvent(ev("request", shellRequest("p")))
		done := askAsync(h.fs, copilot.UserInputRequest{Question: "Continue?"})
		waitFor(t, "question", func() bool { return h.sink.question() != nil })
		h.fs.onEvent(ev("shutdown", &rpc.SessionShutdownData{ShutdownType: rpc.ShutdownTypeError, ErrorReason: reason}))
		if got := h.sink.last(); got.Kind != agentapi.EventExit || !strings.Contains(got.Error, "session shut down") || strings.ContainsAny(got.Error, "\x1b\n") {
			t.Fatalf("shutdown = %+v", got)
		}
		if reply := <-done; reply.err == nil || reply.resp.Answer != "" {
			t.Fatalf("shutdown fabricated question answer: %+v", reply)
		}
		if h.sink.interaction("p").State != agentapi.InteractionExpired || h.fs.answer("p") != nil {
			t.Fatal("shutdown left a permission pending or approved it")
		}
		if err := h.conv.Respond(context.Background(), "p", agentapi.Answer{Decision: "approve_once"}); !errors.Is(err, agentapi.ErrClosed) {
			t.Fatalf("reply after shutdown = %v", err)
		}
		if _, err := h.fs.askUser(copilot.UserInputRequest{Question: "Late?"}, copilot.UserInputInvocation{}); err == nil {
			t.Fatal("closed conversation accepted a new question")
		}
		if err := other.Send(context.Background(), "continue"); err != nil {
			t.Fatalf("one session shutdown closed another: %v", err)
		}
	}
}

func TestWebMalformedQuestionAnswerDoesNotReleaseQuestion(t *testing.T) {
	h := openWeb(t)
	done := askAsync(h.fs, copilot.UserInputRequest{Question: "Name?"})
	waitFor(t, "question", func() bool { return h.sink.question() != nil })
	id := h.sink.question().ID
	for _, answers := range [][][]string{nil, {{""}}, {{"one", "two"}}, {{"one"}, {"two"}}} {
		if err := h.conv.Respond(context.Background(), id, agentapi.Answer{Answers: answers}); err == nil {
			t.Fatalf("malformed answer accepted: %q", answers)
		}
		select {
		case reply := <-done:
			t.Fatalf("malformed answer released question: %+v", reply)
		default:
		}
	}
	if err := h.conv.Respond(context.Background(), id, agentapi.Answer{Answers: [][]string{{"Ada"}}}); err != nil {
		t.Fatal(err)
	}
	if reply := <-done; reply.err != nil || reply.resp.Answer != "Ada" {
		t.Fatalf("corrected answer = %+v", reply)
	}
}

func TestWebStartupFailureCanBeRetriedWithoutCreatingAConversation(t *testing.T) {
	fc := &fakeClient{startErr: errors.New("CLI unavailable")}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	req := agentapi.OpenRequest{SessionID: "same-task", Events: &recSink{}}
	if conv, err := p.Open(context.Background(), req); err == nil || conv != nil || !strings.Contains(err.Error(), "CLI unavailable") {
		t.Fatalf("failed startup = %v, %v", conv, err)
	}
	if started, forced := fc.counts(); started != 1 || forced != 1 || len(fc.sessions) != 0 {
		t.Fatalf("failed startup: started=%d forced=%d sessions=%d", started, forced, len(fc.sessions))
	}
	fc.startErr = nil
	fc.createErr = errors.New("workspace refused")
	if conv, err := p.Open(context.Background(), req); err == nil || conv != nil || !strings.Contains(err.Error(), "workspace refused") {
		t.Fatalf("failed creation = %v, %v", conv, err)
	}
	fc.createErr = nil
	conv, err := p.Open(context.Background(), req)
	if err != nil || conv.ID() != req.SessionID || len(fc.sessions) != 1 || len(fc.sessions[0].sent) != 0 {
		t.Fatalf("retry = %v, %v; sessions=%d", conv, err, len(fc.sessions))
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Open(context.Background(), req); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("Open after shutdown = %v", err)
	}
}

func TestWebHistoryFailureDoesNotInventOrEndConversation(t *testing.T) {
	h := openWeb(t)
	h.fs.eventsErr = errors.New("history unavailable")
	if hist, err := h.conv.History(context.Background()); err == nil || !strings.Contains(err.Error(), "history unavailable") || len(hist.Items) != 0 {
		t.Fatalf("failed history = %+v, %v", hist, err)
	}
	if len(h.sink.all()) != 0 {
		t.Fatal("history failure changed the live conversation")
	}
	if err := h.conv.Send(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if err := h.conv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.conv.History(context.Background()); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("history after close = %v", err)
	}
}

func TestWebDisconnectFailureStillClosesConversation(t *testing.T) {
	h := openWeb(t)
	h.fs.disconnectHook = func() error { return errors.New("disconnect refused") }
	if err := h.conv.Close(context.Background()); err == nil || !strings.Contains(err.Error(), "disconnect refused") {
		t.Fatalf("Close = %v", err)
	}
	if err := h.conv.Send(context.Background(), "must not send"); !errors.Is(err, agentapi.ErrClosed) || len(h.fs.sent) != 0 {
		t.Fatalf("send after disconnect failure = %v, sent %v", err, h.fs.sent)
	}
}
