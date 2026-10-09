package copilot

import (
	"context"
	"encoding/json"
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

type fakeUsageMetricsSession struct {
	*fakeSession
	result *rpc.UsageGetMetricsResult
	err    error
	calls  int
}

func (f *fakeUsageMetricsSession) UsageMetrics(context.Context) (*rpc.UsageGetMetricsResult, error) {
	f.calls++
	return f.result, f.err
}

func usageMetricsFixture() *rpc.UsageGetMetricsResult {
	aiu, reasoning := 2e9, int64(7)
	model := rpc.UsageMetricsModelMetric{Requests: rpc.UsageMetricsModelMetricRequests{Count: 3, Cost: .5}, TotalNanoAiu: &aiu, Usage: rpc.UsageMetricsModelMetricUsage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 80, CacheWriteTokens: 10, ReasoningTokens: &reasoning}, TokenDetails: map[string]rpc.UsageMetricsModelMetricTokenDetail{"future": {TokenCount: 11}}}
	return &rpc.UsageGetMetricsResult{SessionStartTime: time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC), ModelMetrics: map[string]rpc.UsageMetricsModelMetric{"model-a": model}, AgentMetrics: map[string]rpc.UsageMetricsAgentMetric{"main": {ModelMetrics: map[string]rpc.UsageMetricsModelMetric{"model-a": model}, TotalNanoAiu: 2e9, TotalAPIDurationMs: 700}}, TotalNanoAiu: &aiu, TotalUserRequests: 1, TotalPremiumRequestCost: .5, TotalAPIDurationMs: 700, LastCallInputTokens: 100, LastCallOutputTokens: 20, TokenDetails: map[string]rpc.UsageMetricsTokenDetail{"future": {TokenCount: 11}}, CodeChanges: rpc.UsageMetricsCodeChanges{FilesModified: []string{"/private/path"}, FilesModifiedCount: 1, LinesAdded: 5, LinesRemoved: 2}}
}

func TestUsageMetricsReaderPreservesOverlappingNativeProjectionsWithoutSideEffects(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	f := &fakeUsageMetricsSession{fakeSession: h.fs, result: usageMetricsFixture()}
	c.sess = f
	if !h.p.Capabilities().UsageMetrics {
		t.Fatal("native usage metrics capability absent")
	}
	got, err := c.UsageMetrics(context.Background())
	if err != nil || got == nil || got.UserRequests != 1 || got.APIDurationMS != 700 || got.PremiumRequestCost != .5 || got.AIUnits == nil || *got.AIUnits != 2 || len(got.Models) != 1 || len(got.Agents) != 1 {
		t.Fatalf("metrics=%+v, %v", got, err)
	}
	model := got.Models[0]
	if model.Input != 100 || model.Output != 20 || model.CacheRead != 80 || model.CacheWrite != 10 || model.Reasoning == nil || *model.Reasoning != 7 || model.Requests != 3 || got.Agents[0].Models[0].Input != 100 || got.TokenDetails[0].Tokens != 11 {
		t.Fatalf("native counts changed or combined: %+v", got)
	}
	wire, _ := json.Marshal(got)
	if strings.Contains(string(wire), "/private/path") || len(h.fs.sent) != 0 || len(h.fc.resume) != 0 || len(h.sink.all()) != 0 || f.calls != 1 {
		t.Fatal("read leaked paths, ran/resumed a model or emitted metadata")
	}
	*f.result.TotalNanoAiu = 9e9
	source := f.result.ModelMetrics["model-a"]
	*source.Usage.ReasoningTokens = 99
	if *got.AIUnits != 2 || *got.Models[0].Reasoning != 7 {
		t.Fatal("snapshot retained mutable SDK pointers")
	}
	source.TotalNanoAiu = nil
	source.Usage.ReasoningTokens = nil
	f.result.ModelMetrics["model-a"] = source
	f.result.TotalNanoAiu = nil
	next, err := c.UsageMetrics(context.Background())
	if err != nil || next.AIUnits != nil || next.Models[0].AIUnits != nil || next.Models[0].Reasoning != nil || next.UserRequests != 1 {
		t.Fatalf("optional unknown/absolute replacement: %+v %v", next, err)
	}
}

func TestUsageMetricsReaderBoundsAndSanitizesWithoutChangingSessionTotals(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	res := usageMetricsFixture()
	name := "\x1b[31m" + strings.Repeat("x", 700)
	for i := range maxUsageAgents + 2 {
		res.AgentMetrics[fmt.Sprintf("agent-%03d", i)] = rpc.UsageMetricsAgentMetric{AgentDisplayName: &name, ModelMetrics: res.ModelMetrics}
	}
	for i := range maxUsageModels + 2 {
		res.ModelMetrics[fmt.Sprintf("model-%03d", i)] = rpc.UsageMetricsModelMetric{}
	}
	f := &fakeUsageMetricsSession{fakeSession: h.fs, result: res}
	c.sess = f
	got, err := c.UsageMetrics(context.Background())
	if err != nil || got == nil || !got.Truncated || len(got.Agents) > maxUsageAgents || len(got.Models) > maxUsageModels || got.UserRequests != 1 || *got.AIUnits != 2 {
		t.Fatalf("bounds=%+v %v", got, err)
	}
	rows := len(got.Models)
	for _, agent := range got.Agents {
		rows += len(agent.Models)
		if len(agent.DisplayName) > maxUsageLabel || strings.Contains(agent.DisplayName, "\x1b") {
			t.Fatal("unsanitized/unbounded agent label")
		}
	}
	if rows > maxUsageRows {
		t.Fatalf("nested models exceeded shared bound: %d", rows)
	}
}

func TestUsageMetricsReaderUnavailableInvalidAndClosedAreDistinct(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	if _, err := c.UsageMetrics(context.Background()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("absent seam=%v", err)
	}
	f := &fakeUsageMetricsSession{fakeSession: h.fs, result: usageMetricsFixture()}
	c.sess = f
	for _, bad := range []string{"nil", "uninitialized", "negative", "nan", "infinite", "model"} {
		f.result = usageMetricsFixture()
		switch bad {
		case "nil":
			f.result = nil
		case "uninitialized":
			f.result = &rpc.UsageGetMetricsResult{}
		case "negative":
			f.result.TotalUserRequests = -1
		case "nan":
			f.result.TotalPremiumRequestCost = math.NaN()
		case "infinite":
			f.result.TotalPremiumRequestCost = math.Inf(1)
		case "model":
			m := f.result.ModelMetrics["model-a"]
			m.Usage.CacheReadTokens = -1
			f.result.ModelMetrics["model-a"] = m
		}
		if got, err := c.UsageMetrics(context.Background()); got != nil || err == nil {
			t.Fatalf("%s became valid zero snapshot: %+v %v", bad, got, err)
		}
	}
	f.err = &copilot.RPCError{Code: -32601, Message: "missing"}
	if _, err := c.UsageMetrics(context.Background()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("typed unsupported=%v", err)
	}
	f.err = errors.New("method not found inside partial native data")
	if _, err := c.UsageMetrics(context.Background()); err == nil || errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("partial failure=%v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := f.calls
	if _, err := c.UsageMetrics(context.Background()); !errors.Is(err, agentapi.ErrClosed) || f.calls != before {
		t.Fatalf("closed read=%v calls=%d", err, f.calls)
	}
}

func TestUsageMetricsSDKGetterUsesOnlyReadonlySessionRPC(t *testing.T) {
	rt := startFakeRuntime(t)
	a := openFakeSDKSession(t, rt)
	wire, err := json.Marshal(usageMetricsFixture())
	if err != nil {
		t.Fatal(err)
	}
	rt.set("session.usage.getMetrics", string(wire))
	got, err := a.UsageMetrics(context.Background())
	if err != nil || got.TotalUserRequests != 1 {
		t.Fatalf("SDK getter=%+v %v", got, err)
	}
	params := rt.last("session.usage.getMetrics")
	if len(params) != 1 || params["sessionId"] != "s-1" || rt.last("session.send") != nil || rt.last("session.resume") != nil {
		t.Fatalf("unexpected SDK request: %+v", params)
	}
}

func TestUsageMetricsReaderInitializedZeroRemainsDistinctFromMissingOptionalCounters(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.sess = &fakeUsageMetricsSession{fakeSession: h.fs, result: &rpc.UsageGetMetricsResult{SessionStartTime: time.Now(), ModelMetrics: map[string]rpc.UsageMetricsModelMetric{}}}
	got, err := c.UsageMetrics(context.Background())
	if err != nil || got == nil || got.UserRequests != 0 || got.AIUnits != nil || got.Models == nil || got.Agents == nil || got.TokenDetails == nil {
		t.Fatalf("initialized zero=%+v %v", got, err)
	}
}
