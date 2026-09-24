package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func option[T any](v T) *T { return &v }

func TestWebEffortContextCatalogAndCreate(t *testing.T) {
	fc := &fakeClient{models: []rpc.Model{
		{ID: "auto", SupportedReasoningEfforts: []string{"high"}},
		{ID: "tiered", SupportedReasoningEfforts: []string{"low", "high"},
			Capabilities: rpc.ModelCapabilities{Limits: &rpc.ModelCapabilitiesLimits{MaxPromptTokens: option(int64(999))}},
			Billing:      &rpc.ModelBilling{TokenPrices: &rpc.ModelBillingTokenPrices{MaxPromptTokens: option(int64(272)), LongContext: &rpc.ModelBillingTokenPricesLongContext{MaxPromptTokens: option(int64(922))}}}},
		{ID: "unknown", SupportedContextTiers: []string{"long_context"}},
	}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !p.Capabilities().ContextSize || len(models[0].Efforts) != 0 || len(models[0].ContextSizes) != 0 || len(models[2].ContextSizes) != 0 {
		t.Fatalf("catalog = %+v", models)
	}
	mo := models[1]
	if strings.Join(mo.Efforts, ",") != "low,high" || len(mo.ContextSizes) != 2 || mo.ContextSizes[0].Tokens != 272 || mo.ContextSizes[1].Tokens != 922 {
		t.Fatalf("tiered = %+v", mo)
	}
	ctx := context.Background()
	if _, err := p.Open(ctx, agentapi.OpenRequest{SessionID: "new", Model: "tiered", Effort: "high", ContextSize: "long_context", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	if cfg := fc.create[0]; cfg.ReasoningEffort != "high" || cfg.ContextTier != "long_context" {
		t.Fatalf("create = %+v", cfg)
	}
	if _, err := p.Open(ctx, agentapi.OpenRequest{ConversationID: "old", Model: "ignored", Effort: "ignored", ContextSize: "ignored", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	if cfg := fc.resume[0]; cfg.Model != "" || cfg.ReasoningEffort != "" || cfg.ContextTier != "" {
		t.Fatalf("resume overrides selection: %+v", cfg)
	}
}

func TestWebEffortContextSwitchResults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		result     *rpc.ModelSwitchToResult
		resetErr   error
		effort     string
		size       string
		wantErr    bool
		wantClosed bool
	}{
		{name: "effort and tier", effort: "high", size: "long_context"},
		{name: "reset default", size: "default"},
		{name: "cancelled", size: "default", result: &rpc.ModelSwitchToResult{Status: option("cancelled")}, wantErr: true},
		{name: "needs consent", size: "default", result: &rpc.ModelSwitchToResult{Status: option("confirmation_required")}, wantErr: true},
		{name: "deferred", size: "default", result: &rpc.ModelSwitchToResult{Status: option("applied"), Deferred: option(true)}, wantErr: true, wantClosed: true},
		{name: "unknown status", size: "default", result: &rpc.ModelSwitchToResult{Status: option("future")}, wantErr: true, wantClosed: true},
		{name: "wrong tier", effort: "high", size: "long_context", result: &rpc.ModelSwitchToResult{Status: option("applied"), ModelState: &rpc.CurrentModel{ReasoningEffort: option("high"), ContextTier: option(rpc.ContextTier("default"))}}, wantErr: true, wantClosed: true},
		{name: "missing tier confirmation", size: "long_context", result: &rpc.ModelSwitchToResult{Status: option("applied")}, wantErr: true, wantClosed: true},
		{name: "partial reset", size: "default", resetErr: errors.New("reset failed"), wantErr: true, wantClosed: true},
		{name: "persistence failure", size: "default", result: &rpc.ModelSwitchToResult{Status: option("applied"), PersistenceError: option("disk full")}, wantErr: true, wantClosed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := openWeb(t)
			h.fs.modelResult, h.fs.effortErr = tc.result, tc.resetErr
			err := h.conv.SetModel(context.Background(), "target", tc.effort, tc.size)
			if (err != nil) != tc.wantErr {
				t.Fatalf("switch = %v", err)
			}
			req := h.fs.modelRequests[0]
			if req.ModelID != "target" || req.ContextTier == nil || string(*req.ContextTier) != tc.size || req.RunCompactionPreflight == nil || !*req.RunCompactionPreflight || req.CompactionDecision != nil {
				t.Fatalf("switch request = %+v", req)
			}
			if tc.effort != "" && (req.ReasoningEffort == nil || *req.ReasoningEffort != tc.effort) {
				t.Fatalf("effort omitted: %+v", req)
			}
			if tc.effort == "" && req.ReasoningEffort != nil {
				t.Fatal("empty effort must use the reset RPC")
			}
			if !tc.wantErr && tc.effort == "" && h.fs.effortResets != 1 {
				t.Fatal("default did not explicitly reset effort")
			}
			if h.fs.disconnected != tc.wantClosed {
				t.Fatalf("disconnected = %v", h.fs.disconnected)
			}
			if tc.wantClosed {
				if err := h.conv.Send(context.Background(), "must not send"); !errors.Is(err, agentapi.ErrClosed) {
					t.Fatalf("send after uncertain selection = %v", err)
				}
				found := false
				for _, ev := range h.sink.all() {
					found = found || ev.Kind == agentapi.EventExit
				}
				if !found {
					t.Fatal("uncertain selection did not notify manager")
				}
			}
		})
	}
}

func TestWebContextAndCompactionEvents(t *testing.T) {
	h := openWeb(t)
	h.fs.onEvent(agentEv("sub", "child", &rpc.SessionUsageInfoData{CurrentTokens: 8, TokenLimit: 20}))
	h.fs.onEvent(ev("invalid", &rpc.SessionUsageInfoData{CurrentTokens: -1, TokenLimit: 20}))
	if len(h.sink.all()) != 0 {
		t.Fatal("subagent or invalid context entered main meter")
	}
	h.fs.onEvent(ev("usage", &rpc.SessionUsageInfoData{CurrentTokens: 25, TokenLimit: 20}))
	if e := h.sink.last(); e.Kind != agentapi.EventContext || e.Context.Used != 25 || e.Context.Limit != 20 {
		t.Fatalf("context event = %+v", e)
	}
	events := []copilot.SessionEvent{
		ev("compact", &rpc.SessionCompactionCompleteData{Success: true}),
		ev("failed", &rpc.SessionCompactionCompleteData{Success: false, Error: option("provider failed")}),
		ev("truncate", &rpc.SessionTruncationData{MessagesRemovedDuringTruncation: 2, TokensRemovedDuringTruncation: 600}),
	}
	for _, e := range events {
		h.fs.onEvent(e)
	}
	notice := notices(h.sink.all())
	if len(notice) != 3 || !strings.Contains(notice[0], "compacted") || !strings.Contains(notice[1], "failed") || !strings.Contains(notice[2], "600") {
		t.Fatalf("notices = %q", notice)
	}
	h.fs.events = events
	hist, err := h.conv.History(context.Background())
	if err != nil || len(hist.Items) != 3 {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	for _, it := range hist.Items {
		if it.Kind != agentapi.ItemNotice {
			t.Fatalf("history item = %+v", it)
		}
	}
}

func TestWebSubagentModelEffortEventsAndHistory(t *testing.T) {
	h := openWeb(t)
	events := []copilot.SessionEvent{
		agentEv("configured", "child", &rpc.SubagentConfiguredData{Model: "resolved", ReasoningEffort: option("high")}),
		agentEv("started", "child", &rpc.SubagentStartedData{ToolCallID: "tool", AgentName: "explore", Model: option("initial")}),
		agentEv("done", "child", &rpc.SubagentCompletedData{ToolCallID: "tool"}),
		agentEv("late", "child", &rpc.SubagentConfiguredData{Model: "late"}),
	}
	for _, e := range events {
		h.fs.onEvent(e)
	}
	var sa *agentapi.Subagent
	for _, e := range h.sink.all() {
		if e.Kind == agentapi.EventSubagent {
			sa = e.Subagent
		}
	}
	if sa == nil || sa.Model != "resolved" || sa.Effort != "high" || sa.Name != "explore" || sa.Status != agentapi.SubagentCompleted {
		t.Fatalf("subagent = %+v", sa)
	}
	h.fs.events = events
	hist, err := h.conv.History(context.Background())
	if err != nil || len(hist.Subagents) != 1 || hist.Subagents[0].Model != "resolved" || hist.Subagents[0].Effort != "high" {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	h.fs.onEvent(agentEv("start2", "child2", &rpc.SubagentStartedData{ToolCallID: "tool2", Model: option("second")}))
	h.fs.onEvent(agentEv("cfg2", "child2", &rpc.SubagentConfiguredData{Model: "second", ReasoningEffort: option("low")}))
	h.fs.onEvent(agentEv("reset2", "child2", &rpc.SubagentConfiguredData{Model: "second"}))
	if sa := h.sink.last().Subagent; sa.Model != "second" || sa.Effort != "" {
		t.Fatalf("reset subagent = %+v", sa)
	}
}
