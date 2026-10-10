package copilot

import (
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestWebTokenUsageIncludesSubagentsAndDeduplicates(t *testing.T) {
	h := openWeb(t)
	events := decodeEvents(t, recordedTurns)
	for _, ev := range events {
		h.fs.onEvent(ev)
	}
	for _, ev := range events {
		h.fs.onEvent(ev)
	}
	var got []agentapi.TokenUsage
	for _, ev := range h.sink.all() {
		if ev.Kind == agentapi.EventTokens {
			got = append(got, *ev.Tokens)
		}
	}
	if len(got) != 2 {
		t.Fatalf("token calls: %+v", got)
	}
	if got[0].Model != "claude-haiku-4.5" || got[0].Input != 13336 || got[0].Output != 35 || got[0].CacheRead != 0 || got[0].CacheWrite != 13326 || got[0].Time.IsZero() {
		t.Fatalf("main call: %+v", got[0])
	}
	if got[1].Input != 13385 || got[1].Output != 50 || got[1].CacheRead != 13326 || got[1].CacheWrite != 49 {
		t.Fatalf("subagent call: %+v", got[1])
	}
	if got[0].NanoAIU != 1684250000 || got[1].NanoAIU != 165385000 {
		t.Fatalf("deduplicated call charges: %+v", got)
	}
}

func TestModelTokensCarriesPostage(t *testing.T) {
	call := ev("priced", &rpc.AssistantUsageData{Model: "gpt-6-luna", Cost: new(0.5), CopilotUsage: &rpc.AssistantUsageCopilotUsage{TotalNanoAiu: 25230000}})
	got := modelTokens(call, call.Data.(*rpc.AssistantUsageData))
	if got == nil || got.NanoAIU != 25230000 || got.Cost != 0.5 {
		t.Fatalf("priced usage=%+v", got)
	}
	data := &rpc.AssistantUsageData{Model: "deepseek-v4.1-flash", InputTokens: new(int64(100)), OutputTokens: new(int64(20))}
	got = modelTokens(ev("unpriced", data), data)
	if got == nil || got.NanoAIU != 0 || got.Cost != 0 {
		t.Fatalf("unpriced usage=%+v", got)
	}
}

func TestUtilityTokenUsageKeepsCustomModelAndCache(t *testing.T) {
	h := openWeb(t)
	h.p.SetCustomModels(testCustom)
	u := utilityUsage{provider: h.p, selected: "acme/coder"}
	call := ev("usage", &rpc.AssistantUsageData{Model: "coder", IsByok: new(true), InputTokens: new(int64(100)), OutputTokens: new(int64(20)), CacheReadTokens: new(int64(70)), CacheWriteTokens: new(int64(10))})
	u.add(call)
	u.add(call)
	u.report(func(got agentapi.UtilityUsage) {
		if len(got.Tokens) != 1 || got.InputTokens != 100 || got.OutputTokens != 20 {
			t.Fatalf("duplicate utility usage: %+v", got)
		}
		if tokens := got.Tokens[0]; tokens.Model != "acme/coder" || tokens.CacheRead != 70 || tokens.CacheWrite != 10 {
			t.Fatalf("custom utility usage: %+v", tokens)
		}
	})
	h.fs.onEvent(call)
	for _, event := range h.sink.all() {
		if event.Kind == agentapi.EventTokens && event.Tokens.Model == "acme/coder" {
			return
		}
	}
	t.Fatal("main BYOK usage lost its custom model ID")
}
