package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// The payloads below were recorded from Copilot CLI 1.0.88 through SDK
// 1.0.14 on 2026-09-24 (#188), with IDs shortened and unused fields cut.

// recordedQuota is an account.getQuota result. resetDate was a few minutes
// before the call, not a future reset.
const recordedQuota = `{"quotaSnapshots":{
 "premium_interactions":{"entitlementRequests":1500,"isUnlimitedEntitlement":false,"overage":0,"overageAllowedWithExhaustedQuota":false,"remainingPercentage":88,"resetDate":"2026-09-24T17:11:32.998Z","usageAllowedWithExhaustedQuota":false,"usedRequests":180},
 "chat":{"entitlementRequests":0,"isUnlimitedEntitlement":true,"overage":0,"overageAllowedWithExhaustedQuota":false,"remainingPercentage":100,"resetDate":"2026-09-24T17:11:32.998Z","usageAllowedWithExhaustedQuota":false,"usedRequests":0},
 "completions":{"entitlementRequests":-1,"isUnlimitedEntitlement":false,"overage":0,"overageAllowedWithExhaustedQuota":false,"remainingPercentage":100,"usageAllowedWithExhaustedQuota":false,"usedRequests":0}}}`

// recordedModels is part of a models.list result.
const recordedModels = `[
 {"id":"auto","name":"Auto","capabilities":{},"billing":{"discountPercent":10}},
 {"id":"claude-sonnet-5","name":"Claude Sonnet 5","capabilities":{},"policy":{"state":"enabled","terms":""},"modelPickerPriceCategory":"medium"},
 {"id":"claude-haiku-4.5","name":"Claude Haiku 4.5","capabilities":{},"policy":{"state":"enabled","terms":""},"modelPickerPriceCategory":"low"},
 {"id":"future","name":"Future","capabilities":{},"modelPickerPriceCategory":"priceless","billing":{"discountPercent":400}}]`

// recordedTurns are the live events of two turns on claude-haiku-4.5 that
// carry usage. assistant.usage is ephemeral; the checkpoints are recorded.
const recordedTurns = `
{"type":"session.usage_info","ephemeral":true,"data":{"currentTokens":11006,"tokenLimit":128000,"messagesLength":2},"id":"e1","timestamp":"2026-09-24T17:22:54.000Z"}
{"type":"assistant.usage","ephemeral":true,"data":{"model":"claude-haiku-4.5","inputTokens":13336,"cacheReadTokens":0,"cacheWriteTokens":13326,"outputTokens":35,"copilotUsage":{"tokenDetails":[{"batchSize":1000000,"costPerBatch":100000000000,"tokenCount":10,"tokenType":"input"},{"batchSize":1000000,"costPerBatch":125000000000,"tokenCount":13326,"tokenType":"cache_write"},{"batchSize":1000000,"costPerBatch":500000000000,"tokenCount":35,"tokenType":"output"}],"totalNanoAiu":1684250000}},"id":"e2","timestamp":"2026-09-24T17:22:54.100Z"}
{"type":"session.usage_checkpoint","data":{"totalNanoAiu":1684250000,"totalPremiumRequests":0.33},"id":"e3","timestamp":"2026-09-24T17:22:54.200Z"}
{"type":"assistant.usage","ephemeral":true,"agentId":"agent-1","data":{"model":"claude-haiku-4.5","inputTokens":13385,"cacheReadTokens":13326,"cacheWriteTokens":49,"outputTokens":50,"copilotUsage":{"totalNanoAiu":165385000}},"id":"e4","timestamp":"2026-09-24T17:22:56.100Z"}
{"type":"session.usage_checkpoint","data":{"totalNanoAiu":1849635000,"totalPremiumRequests":0.66},"id":"e5","timestamp":"2026-09-24T17:22:56.200Z"}`

// recordedLog is an events.jsonl after the turn and a disconnect: the
// checkpoint and the shutdown carry the session total.
const recordedLog = `
{"type":"user.message","data":{"content":"Reply with the single word OK.","messageId":"u1","delivery":"idle"},"id":"e1","timestamp":"2026-09-24T17:20:25.000Z","parentId":null}
{"type":"assistant.message","data":{"messageId":"m1","content":"OK"},"id":"e2","timestamp":"2026-09-24T17:20:26.000Z","parentId":"e1"}
{"type":"session.usage_checkpoint","data":{"totalNanoAiu":4513400000,"totalPremiumRequests":1},"id":"e3","timestamp":"2026-09-24T17:20:26.100Z","parentId":"e2"}
{"type":"session.shutdown","data":{"shutdownType":"routine","totalNanoAiu":4513400000,"totalApiDurationMs":1200,"sessionStartTime":1790270424965,"codeChanges":{"linesAdded":0,"linesRemoved":0,"filesModified":[]},"modelMetrics":{}},"id":"e4","timestamp":"2026-09-24T17:20:27.000Z","parentId":"e3"}`

func decodeEvents(t *testing.T, lines string) []copilot.SessionEvent {
	t.Helper()
	var out []copilot.SessionEvent
	for line := range strings.Lines(strings.TrimSpace(lines)) {
		var ev copilot.SessionEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode %s: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

func usages(evs []agentapi.Event) []float64 {
	var out []float64
	for _, e := range evs {
		if e.Kind == agentapi.EventUsage {
			out = append(out, e.Usage.AIUnits)
		}
	}
	return out
}

func TestWebQuotaFromAccountGetQuota(t *testing.T) {
	var res rpc.AccountGetQuotaResult
	if err := json.Unmarshal([]byte(recordedQuota), &res); err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{quota: res.QuotaSnapshots}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	if !p.Capabilities().Usage {
		t.Fatal("Copilot does not advertise the usage capability")
	}
	got, err := p.Quota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Date(2026, 9, 24, 17, 11, 32, 998_000_000, time.UTC)
	want := []agentapi.Quota{
		{Type: "chat", Unlimited: true, RemainingPercent: 100, ResetAt: reset},
		{Type: "completions", Unlimited: true, RemainingPercent: 100},
		{Type: "premium_interactions", Used: 180, Entitlement: 1500, RemainingPercent: 88, ResetAt: reset},
	}
	if len(got) != len(want) {
		t.Fatalf("quotas = %+v", got)
	}
	for i := range want {
		if g, w := got[i], want[i]; g.Type != w.Type || g.Used != w.Used || g.Entitlement != w.Entitlement || g.Unlimited != w.Unlimited ||
			g.RemainingPercent != w.RemainingPercent || g.Overage != w.Overage || !g.ResetAt.Equal(w.ResetAt) {
			t.Fatalf("quota %d = %+v, want %+v", i, g, w)
		}
	}

	fc.mu.Lock()
	fc.quotaErr = errors.New("not signed in")
	fc.mu.Unlock()
	if _, err := p.Quota(context.Background()); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("quota error = %v", err)
	}
}

func TestWebModelsCostTierAndDiscount(t *testing.T) {
	var models []rpc.Model
	if err := json.Unmarshal([]byte(recordedModels), &models); err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{models: models}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	got, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var summary []string
	for _, mo := range got {
		summary = append(summary, mo.ID+":"+mo.CostTier+":"+strings.Repeat("%", mo.DiscountPercent/10))
	}
	// Discounts and tiers outside the SDK's ranges are dropped.
	if want := "auto::%|claude-sonnet-5:medium:|claude-haiku-4.5:low:|future::"; strings.Join(summary, "|") != want {
		t.Fatalf("models = %s, want %s", strings.Join(summary, "|"), want)
	}
	if got[0].DiscountPercent != 10 {
		t.Fatalf("auto discount = %d", got[0].DiscountPercent)
	}
}

func TestWebUsageSumsCallsAndFollowsCheckpoints(t *testing.T) {
	h := openWeb(t)
	for _, e := range decodeEvents(t, recordedTurns) {
		h.fs.onEvent(e)
	}
	// A call without a cost changes nothing.
	h.fs.onEvent(ev("e6", &rpc.AssistantUsageData{Model: "claude-haiku-4.5"}))
	// The subagent's call counts; a checkpoint equal to the sum is not
	// reported again.
	if got := usages(h.sink.all()); len(got) != 2 || got[0] != 1.68425 || got[1] != 1.849635 {
		t.Fatalf("usage events = %v", got)
	}
	// The CLI's total wins over the sum of calls it had not seen.
	h.fs.onEvent(ev("e7", &rpc.AssistantUsageData{Model: "m", CopilotUsage: &rpc.AssistantUsageCopilotUsage{TotalNanoAiu: 1e9}}))
	h.fs.onEvent(ev("e8", &rpc.SessionUsageCheckpointData{TotalNanoAiu: 2.5e9}))
	if got := usages(h.sink.all()); len(got) != 4 || got[2] != 2.849635 || got[3] != 2.5 {
		t.Fatalf("usage events = %v", got)
	}
}

func TestWebHistoryUsageFromRecordedTotal(t *testing.T) {
	if got := history(decodeEvents(t, recordedLog)).Usage; got == nil || got.AIUnits != 4.5134 {
		t.Fatalf("recorded usage = %+v", got)
	}
	// assistant.usage is ephemeral: a log with only it records no total.
	if got := history(decodeEvents(t, recordedTurns)[:2]).Usage; got != nil {
		t.Fatalf("usage from ephemeral calls = %+v", got)
	}

	h := openWeb(t)
	h.fs.events = decodeEvents(t, recordedLog)
	recorded, err := h.conv.History(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Usage == nil || recorded.Usage.AIUnits != 4.5134 {
		t.Fatalf("History usage = %+v", recorded.Usage)
	}
	if got := usages(h.sink.all()); len(got) != 0 {
		t.Fatalf("History emitted usage %v", got)
	}
	// The next call continues from the recorded total.
	h.fs.onEvent(ev("e9", &rpc.AssistantUsageData{Model: "m", CopilotUsage: &rpc.AssistantUsageCopilotUsage{TotalNanoAiu: 1e9}}))
	if got := usages(h.sink.all()); len(got) != 1 || got[0] != 5.5134 {
		t.Fatalf("usage after reopen = %v", got)
	}
}

// recordedPrices is part of a models.list result with its token prices;
// "legacy" only has the deprecated cachePrice and contextMax.
const recordedPrices = `[
 {"id":"auto","name":"Auto","capabilities":{},"billing":{"discountPercent":10}},
 {"id":"claude-sonnet-5","name":"Claude Sonnet 5","capabilities":{},"billing":{"tokenPrices":{"batchSize":1000000,"cachePrice":20,"cacheReadPrice":20,"cacheWrite1hPrice":400,"cacheWritePrice":250,"contextMax":200000,"inputPrice":200,"longContext":{"cachePrice":20,"cacheReadPrice":20,"cacheWrite1hPrice":400,"cacheWritePrice":250,"contextMax":936000,"inputPrice":200,"maxPromptTokens":936000,"outputPrice":1000},"maxPromptTokens":200000,"outputPrice":1000}}},
 {"id":"gpt-5-mini","name":"GPT-5 mini","capabilities":{},"billing":{"tokenPrices":{"batchSize":1000000,"cachePrice":2.5,"cacheReadPrice":2.5,"cacheWritePrice":0,"inputPrice":25,"outputPrice":200}}},
 {"id":"legacy","name":"Legacy","capabilities":{},"billing":{"tokenPrices":{"cachePrice":3,"contextMax":64000,"inputPrice":30}}}]`

func TestWebModelPrices(t *testing.T) {
	var models []rpc.Model
	if err := json.Unmarshal([]byte(recordedPrices), &models); err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{models: models}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	got, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"id":"auto","name":"Auto","efforts":[],"context_sizes":[],"discount_percent":10}`,
		`{"id":"claude-sonnet-5","name":"Claude Sonnet 5","efforts":[],"context_sizes":[{"id":"default","tokens":200000},{"id":"long_context","tokens":936000}],` +
			`"prices":{"batch_size":1000000,"input":200,"output":1000,"cache_read":20,"cache_write":250,"max_prompt_tokens":200000,` +
			`"long_context":{"input":200,"output":1000,"cache_read":20,"cache_write":250,"max_prompt_tokens":936000}}}`,
		`{"id":"gpt-5-mini","name":"GPT-5 mini","efforts":[],"context_sizes":[],"prices":{"batch_size":1000000,"input":25,"output":200,"cache_read":2.5,"cache_write":0}}`,
		`{"id":"legacy","name":"Legacy","efforts":[],"context_sizes":[],"prices":{"input":30,"cache_read":3,"max_prompt_tokens":64000}}`,
	}
	for i, mo := range got {
		data, err := json.Marshal(mo)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want[i] {
			t.Fatalf("model %d =\n%s\nwant\n%s", i, data, want[i])
		}
	}
}

func TestWebContextCarriesTheCachedShare(t *testing.T) {
	h := openWeb(t)
	contexts := func() []agentapi.Context {
		var out []agentapi.Context
		for _, e := range h.sink.all() {
			if e.Kind == agentapi.EventContext {
				out = append(out, *e.Context)
			}
		}
		return out
	}
	// A call before any context report sends nothing; the next report
	// carries it.
	h.fs.onEvent(ev("early", &rpc.AssistantUsageData{Model: "m", InputTokens: option(int64(9))}))
	for _, e := range decodeEvents(t, recordedTurns) {
		h.fs.onEvent(e)
	}
	// The subagent's call does not describe the main context.
	want := []agentapi.Context{{Used: 11006, Limit: 128000, Prompt: 9}, {Used: 11006, Limit: 128000, Prompt: 13336}}
	if got := contexts(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("contexts = %+v", got)
	}
	h.fs.onEvent(ev("i2", &rpc.SessionUsageInfoData{CurrentTokens: 11084, TokenLimit: 128000}))
	h.fs.onEvent(ev("u2", &rpc.AssistantUsageData{Model: "m", InputTokens: option(int64(13385)), CacheReadTokens: option(int64(13326)), CacheWriteTokens: option(int64(49))}))
	if got := contexts(); len(got) != 4 || got[2] != (agentapi.Context{Used: 11084, Limit: 128000, Prompt: 13336}) ||
		got[3] != (agentapi.Context{Used: 11084, Limit: 128000, Prompt: 13385, Cached: 13326}) {
		t.Fatalf("contexts = %+v", got)
	}
	// After a switch the old limit is not sent again with a new call.
	if err := h.conv.SetModel(context.Background(), "other", "", "default"); err != nil {
		t.Fatal(err)
	}
	h.fs.onEvent(ev("u3", &rpc.AssistantUsageData{Model: "other", InputTokens: option(int64(100))}))
	h.fs.onEvent(ev("i3", &rpc.SessionUsageInfoData{CurrentTokens: 90, TokenLimit: 64000}))
	if got := contexts(); len(got) != 5 || got[4] != (agentapi.Context{Used: 90, Limit: 64000, Prompt: 100}) {
		t.Fatalf("contexts after a switch = %+v", got)
	}
}
