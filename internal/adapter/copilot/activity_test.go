package copilot

import (
	"context"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// activities returns the activity events emitted after the first n events.
func activities(h webHarness, n int) []agentapi.Activity {
	var out []agentapi.Activity
	for _, e := range h.sink.all()[n:] {
		if e.Kind == agentapi.EventActivity {
			out = append(out, *e.Activity)
		}
	}
	return out
}

func TestWebTurnReportsTheAbortReasonOnce(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "hi"})
	// A subagent's abort is not the turn's.
	h.fs.onEvent(agentEv("a0", "child", &rpc.AbortData{Reason: rpc.AbortReasonUserInitiated}))
	h.fs.onEvent(ev("a1", &rpc.AbortData{Reason: rpc.AbortReasonAutopilotCreditLimit}))
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	if e := h.sink.last(); e.Kind != agentapi.EventTurn || e.Turn.State != agentapi.TurnCancelled || e.Turn.Reason != "autopilot_credit_limit" {
		t.Fatalf("aborted turn = %+v", e.Turn)
	}
	// The next turn starts without it: an abort with no reason reports none.
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "again"})
	h.fs.onEvent(ev("i2", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	if e := h.sink.last(); e.Turn.State != agentapi.TurnCancelled || e.Turn.Reason != "" {
		t.Fatalf("second aborted turn = %+v", e.Turn)
	}
	// An abort reason left by a turn that completed does not reach a later one.
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "third"})
	h.fs.onEvent(ev("a3", &rpc.AbortData{Reason: rpc.AbortReasonRemoteCommand}))
	h.fs.onEvent(ev("i3", &rpc.SessionIdleData{}))
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "fourth"})
	h.fs.onEvent(ev("i4", &rpc.SessionIdleData{Aborted: copilot.Bool(true)}))
	if e := h.sink.last(); e.Turn.Reason != "" {
		t.Fatalf("a stale abort reason reached the next turn: %+v", e.Turn)
	}
}

func TestWebActivityIntentIsTheMainAgentsAndPerTurn(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "hi"})
	n := len(h.sink.all())
	h.fs.onEvent(ev("n1", &rpc.AssistantIntentData{Intent: "  Building the \x1b[31mindex page "}))
	h.fs.onEvent(ev("n2", &rpc.AssistantIntentData{Intent: "Building the index page"}))
	h.fs.onEvent(agentEv("n3", "child", &rpc.AssistantIntentData{Intent: "Child work"}))
	// The CLI starts the main agent's turn again for each model call; the
	// turn keeps its intent.
	h.fs.onEvent(ev("ts", &rpc.AssistantTurnStartData{TurnID: "2"}))
	h.fs.onEvent(ev("r0", &rpc.AssistantTurnRetryData{TurnID: "2"}))
	h.fs.onEvent(ev("n4", &rpc.AssistantIntentData{Intent: strings.Repeat("é", 200)}))
	got := activities(h, n)
	if len(got) != 3 || got[0].Intent != "Building the index page" || got[1].Intent != "Building the index page" || got[1].Retry == nil || got[2].Intent != strings.Repeat("é", maxIntentRunes) {
		t.Fatalf("activities = %+v", got)
	}
	for _, e := range h.sink.all()[n:] {
		if e.Kind != agentapi.EventActivity && e.Kind != agentapi.EventTurn {
			t.Fatalf("intent events emitted %+v", e)
		}
	}
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{}))
	// A new turn starts without the old intent.
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "again"})
	n = len(h.sink.all())
	h.fs.onEvent(ev("r1", &rpc.AssistantTurnRetryData{TurnID: "1"}))
	if got := activities(h, n); len(got) != 1 || got[0].Intent != "" || got[0].Retry == nil {
		t.Fatalf("new turn activity = %+v", got)
	}
}

func TestWebRetryGoesToTheMainTurnOrTheSubagent(t *testing.T) {
	h := openWeb(t)
	ctx := context.Background()
	_ = h.conv.Send(ctx, agentapi.Prompt{Text: "hi"})
	h.fs.onEvent(agentEv("s1", "child", &rpc.SubagentStartedData{ToolCallID: "task-1", AgentName: "explore"}))
	n := len(h.sink.all())
	// A failed call qualifies the retry that follows it, once.
	h.fs.onEvent(ev("f1", &rpc.ModelCallFailureData{StatusCode: option(int32(429)), FailureKind: option(rpc.ModelCallFailureKindAPI), Source: rpc.ModelCallFailureSourceTopLevel}))
	h.fs.onEvent(ev("r1", &rpc.AssistantTurnRetryData{TurnID: "1", Reason: option("rate_limited")}))
	h.fs.onEvent(ev("r2", &rpc.AssistantTurnRetryData{TurnID: "1"}))
	got := activities(h, n)
	if len(got) != 2 {
		t.Fatalf("activities = %+v", got)
	}
	if r := got[0].Retry; r == nil || r.Count != 1 || r.Status != 429 || r.Network || r.Reason != "rate_limited" || !r.At.Equal(ev("", nil).Timestamp) {
		t.Fatalf("first retry = %+v", r)
	}
	if r := got[1].Retry; r == nil || r.Count != 2 || r.Status != 0 || r.Reason != "" {
		t.Fatalf("second retry = %+v", r)
	}
	// The subagent's retry is its own; the turn's activity does not change.
	n = len(h.sink.all())
	h.fs.onEvent(agentEv("f2", "child", &rpc.ModelCallFailureData{FailureKind: option(rpc.ModelCallFailureKindTransport), Source: rpc.ModelCallFailureSourceSubagent}))
	h.fs.onEvent(agentEv("r3", "child", &rpc.AssistantTurnRetryData{TurnID: "c1", Reason: option("network_error")}))
	evs := h.sink.all()[n:]
	if len(evs) != 1 || evs[0].Kind != agentapi.EventSubagent || evs[0].Subagent.ID != "child" {
		t.Fatalf("subagent retry events = %+v", evs)
	}
	if r := evs[0].Subagent.Retry; r == nil || r.Count != 1 || !r.Network || r.Status != 0 || r.Reason != "network_error" {
		t.Fatalf("subagent retry = %+v", r)
	}
	// A call that goes through ends the retry: the main turn's at once, the
	// subagent's with its usage report.
	n = len(h.sink.all())
	h.fs.onEvent(ev("u1", &rpc.AssistantUsageData{Model: "m", InputTokens: option(int64(10))}))
	h.fs.onEvent(agentEv("u2", "child", &rpc.AssistantUsageData{Model: "m", InputTokens: option(int64(10))}))
	if got := activities(h, n); len(got) != 1 || got[0].Retry != nil {
		t.Fatalf("activity after a call went through = %+v", got)
	}
	var sub *agentapi.Subagent
	for _, e := range h.sink.all()[n:] {
		if e.Kind == agentapi.EventSubagent {
			sub = e.Subagent
		}
	}
	if sub == nil || sub.Retry != nil || sub.Tokens != 10 {
		t.Fatalf("subagent after a call went through = %+v", sub)
	}
}

func TestWebNoticesFromInfoAndWarnings(t *testing.T) {
	events := []copilot.SessionEvent{
		ev("w1", &rpc.SessionWarningData{WarningType: "subscription", Message: "Your token expired", Remediation: option(rpc.RemediationActionSignIn), URL: option("https://docs.github.com/copilot (setup)")}),
		ev("w2", &rpc.SessionWarningData{WarningType: "policy", Message: "Blocked by policy.", URL: option("http://example.com/policy")}),
		ev("w3", &rpc.SessionWarningData{WarningType: "policy", Message: "Odd link", URL: option("https://user@evil.example/")}),
		agentEv("w4", "child", &rpc.SessionWarningData{WarningType: "mcp", Message: "MCP server slow"}),
		ev("i1", &rpc.SessionInfoData{InfoType: "mcp", Message: "MCP server ready", Tip: option("Run /mcp to list it")}),
		ev("i2", &rpc.SessionInfoData{InfoType: "timing", Message: "Took 3s"}),
		ev("i3", &rpc.SessionInfoData{InfoType: "tip", Message: "Press Ctrl+C"}),
		ev("i4", &rpc.SessionInfoData{InfoType: "notification", Message: ""}),
	}
	want := []string{
		"|w1|Warning: Your token expired. Sign in to GitHub Copilot again in Settings. [Learn more](https://docs.github.com/copilot%20%28setup%29)",
		"|w2|Warning: Blocked by policy.",
		"|w3|Warning: Odd link",
		"child|w4|Warning: MCP server slow",
		"|i1|MCP server ready. Run /mcp to list it",
	}
	notices := func(items []agentapi.Item) string {
		var got []string
		for _, it := range items {
			if it.Kind == agentapi.ItemNotice {
				got = append(got, it.AgentID+"|"+it.ID+"|"+it.Text)
			}
		}
		return strings.Join(got, "\n")
	}

	h := openWeb(t)
	var live []agentapi.Item
	for _, e := range events {
		n := len(h.sink.all())
		h.fs.onEvent(e)
		for _, emitted := range h.sink.all()[n:] {
			if emitted.Kind == agentapi.EventItem {
				live = append(live, *emitted.Item)
			}
		}
	}
	if got := notices(live); got != strings.Join(want, "\n") {
		t.Fatalf("live notices =\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}

	h.fs.events = events
	recorded, err := h.conv.History(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := notices(recorded.Items); got != strings.Join(want, "\n") {
		t.Fatalf("history notices =\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
}
