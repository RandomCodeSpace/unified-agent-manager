package web

import (
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestModelSelectionUsesConfirmedFactsAndPreservesUnknowns(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	s.model, s.effort, s.contextSize = "a", "high", "long_context"
	info := m.infos[prov.Name()]
	info.Models = selectionModels()
	m.infos[prov.Name()] = info
	s.context = &agentapi.Context{Used: 12, Limit: 256000}
	m.mu.Unlock()
	conv.Emit(agentapi.Event{Kind: agentapi.EventModelSelection, ModelSelection: &agentapi.ModelSelection{Model: "b"}})
	d := detail(t, m, sum.ID)
	if d.Model != "b" || d.Effort != "high" || d.ContextSize != "long_context" || d.Context == nil || d.Context.Limit != 256000 {
		t.Fatalf("confirmed model/unknown options = %+v", d.SessionSummary)
	}
	low, size := "low", "default"
	conv.Emit(agentapi.Event{Kind: agentapi.EventModelSelection, ModelSelection: &agentapi.ModelSelection{Model: "c", Effort: &low, ContextSize: &size}})
	d = detail(t, m, sum.ID)
	if d.Model != "c" || d.Effort != low || d.ContextSize != size {
		t.Fatalf("confirmed settings = %+v", d.SessionSummary)
	}
	unsupportedEffort, unsupportedSize := "high", "long_context"
	conv.Emit(agentapi.Event{Kind: agentapi.EventModelSelection, ModelSelection: &agentapi.ModelSelection{Model: "c", Effort: &unsupportedEffort, ContextSize: &unsupportedSize}})
	d = detail(t, m, sum.ID)
	if d.Effort != low || d.ContextSize != size {
		t.Fatalf("unsupported supplied options overwritten: %+v", d.SessionSummary)
	}
	if len(conv.ModelSets()) != 0 {
		t.Fatalf("confirmation switched model: %+v", conv.ModelSets())
	}
	// Persist the adopted selection through the same existing state path.
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	record, ok := cfg.Sessions[store.Key(prov.Name(), sum.ID)]
	if !ok || record.Web.Model != "c" || record.Web.Effort != low || record.Web.ContextSize != size {
		t.Fatalf("stored selection = %+v", record)
	}
}

func TestModelSelectionAutoReopensAndSendsWithCompatibleAuthoredOptions(t *testing.T) {
	caps := allCaps
	caps.ContextSize = true
	prov := agenttest.NewProvider("fake", caps)
	prov.SetModels(selectionModels(), nil)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, m, t.TempDir()), Model: "a", Effort: "high", ContextSize: "long_context"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	m.mu.Lock()
	m.sessions[sum.ID].context = &agentapi.Context{Used: 12, Limit: 256000}
	m.mu.Unlock()
	conv.Emit(agentapi.Event{Kind: agentapi.EventModelSelection, ModelSelection: &agentapi.ModelSelection{Model: "auto"}})
	d := detail(t, m, sum.ID)
	if d.Model != "auto" || d.Effort != "" || d.ContextSize != "default" || d.Context == nil || d.Context.Limit != 256000 || d.Context.Used != 12 {
		t.Fatalf("Auto authored options/context facts = %+v", d.SessionSummary)
	}
	if len(conv.ModelSets()) != 0 {
		t.Fatal("native Auto confirmation called SwitchModel")
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "continue", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "reopened send", func() bool { return prov.Last() != conv && len(prov.Last().Sends()) == 1 })
	opened := prov.Last()
	settings := opened.ModelSettings()
	if len(settings) != 1 || settings[0].Model != "auto" || settings[0].Effort != "" || settings[0].ContextSize != "default" {
		t.Fatalf("reopen reapplied incompatible options: %+v", settings)
	}
}

func TestModelSelectionRejectsStaleAndUnboundedFacts(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	s.model = "kept"
	gen := s.gen
	m.mu.Unlock()
	for _, model := range []string{"", strings.Repeat("m", maxNameRunes+1), "bad\nmodel"} {
		conv.Emit(agentapi.Event{Kind: agentapi.EventModelSelection, ModelSelection: &agentapi.ModelSelection{Model: model}})
		if d := detail(t, m, sum.ID); d.Model != "kept" {
			t.Fatalf("invalid model %q adopted: %q", model, d.Model)
		}
	}
	m.handleEvent(s, gen-1, agentapi.Event{Kind: agentapi.EventModelSelection, ModelSelection: &agentapi.ModelSelection{Model: "stale"}})
	if d := detail(t, m, sum.ID); d.Model != "kept" {
		t.Fatalf("old generation adopted: %q", d.Model)
	}
}

func TestAutoFallbackQuestionNeverUsesYoloPermissionConsent(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	m.mu.Lock()
	m.sessions[sum.ID].mode = store.ModeYolo
	m.mu.Unlock()
	conv.EmitInteraction(agentapi.Interaction{ID: "auto-first", Kind: agentapi.InteractionQuestion, Title: "Auto model fallback", Questions: []agentapi.Question{{Text: "Switch to Auto? Usage and cost may differ.", Choices: []string{"Yes", "No"}}}})
	if d := detail(t, m, sum.ID); d.State != StateAwaitingAnswer || len(conv.Responds()) != 0 {
		t.Fatalf("Yolo automatically answered question: state=%s, responses=%+v", d.State, conv.Responds())
	}
	if _, err := m.Answer(sum.ID, "auto-first", agentapi.Answer{Answers: [][]string{{"Yes"}}}); err != nil {
		t.Fatal(err)
	}
	conv.EmitInteraction(agentapi.Interaction{ID: "auto-second", Kind: agentapi.InteractionQuestion, Questions: []agentapi.Question{{Text: "Switch again?", Choices: []string{"Yes", "No"}}}})
	if d := detail(t, m, sum.ID); d.State != StateAwaitingAnswer || len(conv.Responds()) != 1 {
		t.Fatalf("first choice carried to second question: %+v", conv.Responds())
	}
}
