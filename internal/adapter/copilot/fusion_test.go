package copilot

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// fusionCatalog is what CLI 1.0.112 lists beside the models: Max is off for
// the plan and Fast is not for pickers.
func fusionCatalog() *rpc.AutoTierMetadata {
	tier := func(id, level string, enabled, visible bool) rpc.AutoTierDescriptor {
		d := rpc.AutoTierDescriptor{ID: id, Status: rpc.AutoTierStatus{Enabled: enabled, PickerVisible: &visible}}
		if level != "" {
			d.FusionTier = &level
		}
		return d
	}
	return &rpc.AutoTierMetadata{DefaultTier: "balance", Tiers: []rpc.AutoTierDescriptor{
		tier("balance", "standard", true, true), tier("hydrafusion", "high", true, true),
		tier("hydrafusion-max", "max", false, true), tier("fast", "", true, false),
	}}
}

func TestFusionCatalogAndCreate(t *testing.T) {
	fc := &fakeClient{fusion: fusionCatalog(), models: []rpc.Model{{ID: "auto", Name: "Auto", SupportedReasoningEfforts: []string{"high"}}}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	ctx := context.Background()
	models, err := p.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mo := models[0]; mo.Name != "Fusion" || strings.Join(mo.Efforts, ",") != "fusion-standard,fusion-high" {
		t.Fatalf("Fusion = %+v, want the two levels the account can select", mo)
	}
	if _, err := p.Open(ctx, agentapi.OpenRequest{SessionID: "new", Model: "auto", Effort: "fusion-high", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	if cfg := fc.create[0]; cfg.ReasoningEffort != "" || cfg.Capi == nil || cfg.Capi.FusionTier != rpc.FusionTierHigh {
		t.Fatalf("create = effort %q, capi %+v; want the level apart from the effort", cfg.ReasoningEffort, cfg.Capi)
	}
	if _, err := p.Open(ctx, agentapi.OpenRequest{SessionID: "default", Model: "auto", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	if cfg := fc.create[1]; cfg.ReasoningEffort != "" || cfg.Capi != nil {
		t.Fatalf("create with no level = effort %q, capi %+v", cfg.ReasoningEffort, cfg.Capi)
	}
	// "high" is another model's reasoning effort, which Fusion rejects.
	if _, err := p.Open(ctx, agentapi.OpenRequest{SessionID: "plain", Model: "auto", Effort: "high", Events: &recSink{}}); err == nil || len(fc.create) != 2 {
		t.Fatalf("a reasoning effort on Fusion = %v after %d creates, want it refused", err, len(fc.create))
	}
}

func TestFusionSwitch(t *testing.T) {
	staged := func(tier rpc.AutoTier) *rpc.ModelSwitchToResult {
		return &rpc.ModelSwitchToResult{Status: option("applied"), ModelID: option("auto"), ModelState: &rpc.CurrentModel{ModelID: option("auto"), PendingAutoTier: &tier}}
	}
	t.Run("level", func(t *testing.T) {
		h := openWeb(t)
		h.fs.modelResult = staged("hydrafusion")
		if err := h.conv.SetModel(context.Background(), "auto", "fusion-high", "default"); err != nil {
			t.Fatal(err)
		}
		req := h.fs.modelRequests[0]
		if req.FusionTier == nil || *req.FusionTier != rpc.FusionTierHigh || req.ReasoningEffort != nil || req.AutoTier != nil {
			t.Fatalf("switch request = %+v, want only the level", req)
		}
		if h.fs.effortResets != 0 {
			t.Fatal("Fusion rejects the effort reset while a level is staged")
		}
	})
	t.Run("level not confirmed", func(t *testing.T) {
		h := openWeb(t)
		if err := h.conv.SetModel(context.Background(), "auto", "fusion-high", "default"); err == nil || !h.fs.disconnected {
			t.Fatalf("switch = %v, disconnected %v; a result that stages no level is uncertain", err, h.fs.disconnected)
		}
	})
	t.Run("default", func(t *testing.T) {
		h := openWeb(t)
		if err := h.conv.SetModel(context.Background(), "auto", "", "default"); err != nil {
			t.Fatal(err)
		}
		req := h.fs.modelRequests[0]
		if req.AutoTier == nil || *req.AutoTier != nil || req.FusionTier != nil || req.ReasoningEffort != nil {
			t.Fatalf("switch request = %+v, want an explicit return to the provider's default level", req)
		}
		if h.fs.effortResets != 1 {
			t.Fatal("default did not reset the effort")
		}
	})
	t.Run("reasoning effort", func(t *testing.T) {
		h := openWeb(t)
		if err := h.conv.SetModel(context.Background(), "auto", "high", "default"); err == nil || len(h.fs.modelRequests) != 0 {
			t.Fatalf("switch = %v after %d requests, want it refused unsent", err, len(h.fs.modelRequests))
		}
	})
}

// Fusion delivers a turn's steps live and records them again at its commit,
// stamped with the commit's time: a recorded step stays finished and keeps
// the time it ran.
func TestFusionCommitKeepsLiveSteps(t *testing.T) {
	tr := newTranscript()
	ran := time.Date(2026, 11, 17, 11, 16, 22, 0, time.UTC)
	tag := &rpc.FusionAttribution{}
	at := func(e copilot.SessionEvent, when time.Time, live bool) copilot.SessionEvent {
		e.Timestamp, e.Ephemeral = when, &live
		return e
	}
	start := ev("s", &rpc.ToolExecutionStartData{ToolCallID: "call", ToolName: "view", Fusion: tag})
	end := ev("e", &rpc.ToolExecutionCompleteData{ToolCallID: "call", Success: true, Fusion: tag})
	tr.items(at(start, ran, true))
	tr.items(at(end, ran.Add(time.Second), true))
	commit := ran.Add(time.Minute)
	if got := tr.items(at(start, commit, false)); len(got) != 0 {
		t.Fatalf("recorded start = %+v, want nothing: the step already finished", got)
	}
	got := tr.items(at(end, commit, false))
	if len(got) != 1 || got[0].Tool.Name != "view" || got[0].Tool.Status != agentapi.ToolCompleted || !got[0].EndedAt.Equal(ran.Add(time.Second)) {
		t.Fatalf("recorded end = %+v, want the finished view ending when it ran", got)
	}
	// A record with no live copy, as history reads it, is taken as it is.
	if got := newTranscript().items(at(start, commit, false)); len(got) != 1 || !got[0].Time.Equal(commit) {
		t.Fatalf("history start = %+v", got)
	}
}

// A Critique turn as CLI 1.0.112 reports it: the route before the turn, the
// draft, a critic that approves, and the revision it then skips.
func TestFusionRouteInActivity(t *testing.T) {
	h := openWeb(t)
	_ = h.conv.Send(context.Background(), agentapi.Prompt{Text: "hi"})
	n := len(h.sink.all())
	hint, luna, effort := "Using Critique", "gpt-6-luna", "high"
	h.fs.onEvent(ev("rs", &rpc.SessionFusionRouteStartedData{}))
	h.fs.onEvent(ev("rr", &rpc.SessionFusionResolvedData{SyntheticModel: "hydrafusion", Pattern: rpc.FusionPatternCritique, Hint: &hint, PrimaryModel: "claude-opus-5.5", SecondaryModel: &luna, PhasePlan: []rpc.FusionPhasePlanStep{
		{Kind: rpc.FusionPhaseKindDraft, Role: "solver"}, {Kind: rpc.FusionPhaseKindCritic, Role: "critic"}, {Kind: rpc.FusionPhaseKindRevision, Role: "solver", Conditional: true},
	}}))
	h.fs.onEvent(ev("d0", &rpc.AssistantFusionPhaseStartedData{PhaseID: "f:phase:0", PhaseKind: rpc.FusionPhaseKindDraft, Role: "solver", Model: "claude-opus-5.5", ConversationScope: rpc.FusionConversationScopeRoot}))
	h.fs.onEvent(ev("da", &rpc.AssistantFusionPhaseActivityData{PhaseID: "f:phase:0"}))
	h.fs.onEvent(ev("d1", &rpc.AssistantFusionPhaseCompletedData{PhaseID: "f:phase:0", PhaseKind: rpc.FusionPhaseKindDraft, Model: "claude-opus-5.5", Status: rpc.FusionPhaseStatusSucceeded, Content: "The whole reply", ConversationScope: rpc.FusionConversationScopeRoot, Usage: rpc.FusionPhaseUsage{InputTokens: 100, OutputTokens: 20, TotalNanoAiu: 5e9}}))
	h.fs.onEvent(ev("c0", &rpc.AssistantFusionPhaseStartedData{PhaseID: "f:critic", PhaseKind: rpc.FusionPhaseKindCritic, Role: "critic", Model: luna, ReasoningEffort: &effort, ConversationScope: rpc.FusionConversationScopeReview}))
	h.fs.onEvent(ev("c1", &rpc.AssistantFusionPhaseCompletedData{PhaseID: "f:critic", PhaseKind: rpc.FusionPhaseKindCritic, Model: luna, Status: rpc.FusionPhaseStatusSucceeded, Content: `{"assessment":"approve","feedback":"No defect.","defect":null}`, ConversationScope: rpc.FusionConversationScopeReview}))
	got := activities(h, n)
	if len(got) != 6 {
		t.Fatalf("activities = %d, want one per route and phase event, none for its progress", len(got))
	}
	f := got[len(got)-1].Fusion
	if f == nil || f.Level != "high" || f.Pattern != "critique" || f.Hint != hint || !slices.Equal(f.Models, []string{"claude-opus-5.5", luna}) {
		t.Fatalf("route = %+v", f)
	}
	draft, critic, revision := f.Phases[0], f.Phases[1], f.Phases[2]
	if draft.Status != "succeeded" || draft.Tokens != 120 || draft.NanoAIU != 5e9 || draft.Note != "" || draft.StartedAt.IsZero() {
		t.Fatalf("draft = %+v; the draft's reply stays in the conversation", draft)
	}
	if critic.Model != luna || critic.Effort != "high" || critic.Verdict != "approve" || critic.Note != "No defect." {
		t.Fatalf("critic = %+v", critic)
	}
	if revision.Status != "planned" || !revision.Conditional || revision.ID != "" {
		t.Fatalf("revision = %+v", revision)
	}
	// An earlier activity is not changed by a later event.
	if got[1].Fusion.Phases[0].Status != "planned" {
		t.Fatalf("first activity's draft = %+v, want it as it was then", got[1].Fusion.Phases[0])
	}
	h.fs.onEvent(ev("fc", &rpc.SessionFusionCompletedData{}))
	if last := activities(h, n); last[len(last)-1].Fusion != nil {
		t.Fatal("the route stayed after Fusion completed")
	}
}

// Fusion Standard reports only the model it picked for the turn and why.
func TestStandardRouteInActivity(t *testing.T) {
	h := openWeb(t)
	_ = h.conv.Send(context.Background(), agentapi.Prompt{Text: "hi"})
	n := len(h.sink.all())
	tier, why := rpc.AutoTier("balance"), "Copilot Fusion selected GPT-6 Luna since the task has medium complexity."
	h.fs.onEvent(ev("am", &rpc.SessionAutoModeResolvedData{AutoTier: &tier, ChosenModel: "gpt-6-luna", SelectionReason: &why}))
	got := activities(h, n)
	f := got[len(got)-1].Fusion
	if f == nil || f.Level != "standard" || f.Pattern != "single" || f.Hint != why || !slices.Equal(f.Models, []string{"gpt-6-luna"}) || len(f.Phases) != 0 {
		t.Fatalf("route = %+v", f)
	}
	// Standard picks once; the next turn shows the same pick.
	h.fs.onEvent(ev("i1", &rpc.SessionIdleData{}))
	n = len(h.sink.all())
	h.fs.onEvent(ev("t2", &rpc.AssistantTurnStartData{TurnID: "2"}))
	if got := activities(h, n); len(got) == 0 || got[len(got)-1].Fusion == nil || got[len(got)-1].Fusion.Hint != why {
		t.Fatalf("the next turn lost Standard's pick: %d activities", len(got))
	}
	// A model change ends the pick.
	h.fs.onEvent(ev("i2", &rpc.SessionIdleData{}))
	h.fs.onEvent(ev("mc", &rpc.SessionModelChangeData{NewModel: "gpt-6-luna"}))
	n = len(h.sink.all())
	h.fs.onEvent(ev("t3", &rpc.AssistantTurnStartData{TurnID: "3"}))
	for _, a := range activities(h, n) {
		if a.Fusion != nil {
			t.Fatal("the pick outlived a model change")
		}
	}
}

// The journal keeps a Critique turn's route and how its phases ended, not
// their starts or the planned revision's skip.
func TestReadFusionFromTheJournal(t *testing.T) {
	at := time.Date(2026, 11, 17, 11, 16, 0, 0, time.UTC)
	luna, high := "gpt-6-luna", "high"
	stamp := func(e copilot.SessionEvent, s int) copilot.SessionEvent {
		e.Timestamp = at.Add(time.Duration(s) * time.Second)
		return e
	}
	journal := []copilot.SessionEvent{
		stamp(ev("u0", &rpc.UserMessageData{Content: "plain turn"}), 0),
		stamp(ev("r1", &rpc.SessionFusionResolvedData{FusionID: "f1", SyntheticModel: "hydrafusion-max", Pattern: rpc.FusionPatternCritique, PrimaryModel: "claude-opus-5.5", SecondaryModel: &luna, PhasePlan: []rpc.FusionPhasePlanStep{
			{Kind: rpc.FusionPhaseKindDraft, Role: "solver"}, {Kind: rpc.FusionPhaseKindCritic, Role: "critic"}, {Kind: rpc.FusionPhaseKindRevision, Role: "solver", Conditional: true},
		}}), 10),
		stamp(ev("p1", &rpc.AssistantFusionPhaseCompletedData{FusionID: "f1", PhaseID: "f1:phase:0", PhaseKind: rpc.FusionPhaseKindDraft, Role: "solver", Model: "claude-opus-5.5", Status: rpc.FusionPhaseStatusSucceeded, DurationMs: 90000, ConversationScope: rpc.FusionConversationScopeRoot, Usage: rpc.FusionPhaseUsage{InputTokens: 7, OutputTokens: 3}}), 100),
		stamp(ev("p2", &rpc.AssistantFusionPhaseCompletedData{FusionID: "f1", PhaseID: "f1:critic", PhaseKind: rpc.FusionPhaseKindCritic, Role: "critic", Model: luna, ReasoningEffort: &high, Status: rpc.FusionPhaseStatusSucceeded, DurationMs: 20000, ConversationScope: rpc.FusionConversationScopeReview,
			Content: `{"assessment":"revise","feedback":"Close races Hit.","defect":"Hit after Close is counted"}`}), 120),
		// A phase of a route the journal never resolved is left out.
		stamp(ev("p3", &rpc.AssistantFusionPhaseCompletedData{FusionID: "lost", PhaseID: "x", PhaseKind: rpc.FusionPhaseKindDraft}), 130),
	}
	p := readerProvider(&fakeClient{journal: journal, pageSize: 2})
	turns, err := p.ReadFusion(context.Background(), agentapi.ReadRequest{ConversationID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].ID != "f1" || turns[0].Level != "max" || !turns[0].StartedAt.Equal(at.Add(10*time.Second)) || len(turns[0].Phases) != 3 {
		t.Fatalf("turns = %+v", turns)
	}
	draft, critic, revision := turns[0].Phases[0], turns[0].Phases[1], turns[0].Phases[2]
	if !draft.StartedAt.Equal(at.Add(10*time.Second)) || draft.Tokens != 10 || draft.Status != "succeeded" {
		t.Fatalf("draft = %+v, want its start as its end less its duration", draft)
	}
	if critic.Verdict != "revise" || critic.Note != "Close races Hit." || critic.Defect != "Hit after Close is counted" || critic.Effort != "high" {
		t.Fatalf("critic = %+v", critic)
	}
	if revision.Status != "planned" {
		t.Fatalf("revision = %+v", revision)
	}
}
