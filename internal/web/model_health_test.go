package web

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// healthManager is a Manager that is not started, on a clock the test moves.
func healthManager(t *testing.T) (*Manager, *time.Time) {
	t.Helper()
	m := NewManager(openTestStore(t), nil)
	now := time.Unix(1_800_000_000, 0)
	m.now = func() time.Time { return now }
	t.Cleanup(m.counts.close)
	return m, &now
}

func calls(m *Manager, model, outcome string, n int) {
	for range n {
		m.recordModelCall(agentapi.ModelCall{Model: model, Outcome: outcome, DurationMS: 100})
	}
}

func TestModelHealthTripsOnConsecutiveFailuresInTheWindow(t *testing.T) {
	m, now := healthManager(t)
	unhealthy := func() bool { return m.health.unhealthy("a", *now) }
	calls(m, "a", "error", healthFails-1)
	if unhealthy() {
		t.Fatal("unhealthy below the failure count")
	}
	// A cancelled call neither counts as a failure nor breaks the run.
	calls(m, "a", "cancelled", 1)
	if unhealthy() {
		t.Fatal("a cancelled call counted as a failure")
	}
	calls(m, "a", "rejected", 1)
	if !unhealthy() {
		t.Fatalf("healthy after %d failed calls in a row", healthFails)
	}
	if m.health.unhealthy("b", *now) {
		t.Fatal("a model with no calls is unhealthy")
	}
}

func TestModelHealthRecoversAfterSuccessOrWindow(t *testing.T) {
	m, now := healthManager(t)
	calls(m, "a", "error", healthFails)
	calls(m, "a", "success", 1)
	if m.health.unhealthy("a", *now) {
		t.Fatal("unhealthy after a success")
	}
	// Failures before and after a success are not one run.
	calls(m, "a", "error", healthFails-1)
	if m.health.unhealthy("a", *now) {
		t.Fatal("failures across a success counted as consecutive")
	}
	calls(m, "a", "error", 1)
	*now = now.Add(healthWindow - time.Second)
	if !m.health.unhealthy("a", *now) {
		t.Fatal("healthy inside the window")
	}
	*now = now.Add(time.Second)
	if m.health.unhealthy("a", *now) {
		t.Fatal("unhealthy after the window")
	}
	// One more failure after the window does not trip it again: the run
	// must fit in one window.
	calls(m, "a", "error", 1)
	if m.health.unhealthy("a", *now) {
		t.Fatal("old failures counted with a new one")
	}
	calls(m, "a", "error", healthFails-1)
	if !m.health.unhealthy("a", *now) {
		t.Fatal("healthy after a new run of failures")
	}
}

func TestModelHealthMemoryIsBounded(t *testing.T) {
	m, now := healthManager(t)
	calls(m, "first", "error", healthKept*3)
	for i := range healthModels * 2 {
		*now = now.Add(time.Second)
		m.health.record(fmt.Sprintf("m%d", i), healthCall{at: *now})
	}
	m.health.mu.Lock()
	defer m.health.mu.Unlock()
	if len(m.health.calls) != healthModels {
		t.Fatalf("kept %d models, want %d", len(m.health.calls), healthModels)
	}
	if _, ok := m.health.calls["first"]; ok {
		t.Fatal("the model called longest ago was kept")
	}
	for id, kept := range m.health.calls {
		if len(kept) > healthKept {
			t.Fatalf("%s keeps %d calls", id, len(kept))
		}
	}
	if _, ok := m.health.calls[fmt.Sprintf("m%d", healthModels*2-1)]; !ok {
		t.Fatal("the latest model was dropped")
	}
}

func TestModelHealthKeepsOnlyRecentCalls(t *testing.T) {
	m, now := healthManager(t)
	calls(m, "a", "success", healthKept+5)
	m.health.mu.Lock()
	n := len(m.health.calls["a"])
	m.health.mu.Unlock()
	if n != healthKept || m.health.unhealthy("a", *now) {
		t.Fatalf("kept %d calls, want %d", n, healthKept)
	}
}

func TestSubagentModelMovesOffAnUnhealthyModel(t *testing.T) {
	m, now := healthManager(t)
	moves := func() bool {
		return slices.ContainsFunc(m.uamStats(), func(s SessionStat) bool { return s.Key == "uam.subagent_model_health_moves" })
	}
	allowed := []string{"a", "b", "c"}
	if got := m.subagentModel("a", allowed); got != "a" || moves() {
		t.Fatalf("healthy model moved to %q", got)
	}
	calls(m, "a", "error", healthFails)
	calls(m, "outside", "success", 1)
	if got := m.subagentModel("a", allowed); got != "b" {
		t.Fatalf("moved to %q, want the next allowed healthy model b", got)
	}
	if !slices.Contains(m.uamStats(), SessionStat{"uam.subagent_model_health_moves", "1"}) {
		t.Fatalf("the move was not counted: %v", m.uamStats())
	}
	// The next healthy one in the owner's order, never one outside the list.
	calls(m, "b", "rejected", healthFails)
	if got := m.subagentModel("a", allowed); got != "c" {
		t.Fatalf("moved to %q, want c", got)
	}
	if got := m.subagentModel("c", allowed); got != "c" {
		t.Fatalf("healthy pick moved to %q", got)
	}
	// With no healthy alternative the pick stays, and nothing is counted.
	calls(m, "c", "error", healthFails)
	if got := m.subagentModel("a", allowed); got != "a" {
		t.Fatalf("no healthy alternative: moved to %q", got)
	}
	if got := m.subagentModel("a", nil); got != "a" {
		t.Fatalf("nothing usable: moved to %q", got)
	}
	if !slices.Contains(m.uamStats(), SessionStat{"uam.subagent_model_health_moves", "2"}) {
		t.Fatalf("moves counted wrong: %v", m.uamStats())
	}
	*now = now.Add(healthWindow)
	if got := m.subagentModel("a", allowed); got != "a" {
		t.Fatalf("after the window: moved to %q", got)
	}
}

func TestModelCallCountsAndLatencyInStats(t *testing.T) {
	m, _ := healthManager(t)
	m.recordModelCall(agentapi.ModelCall{Model: "a", Outcome: "success", DurationMS: 300})
	m.recordModelCall(agentapi.ModelCall{Model: "a", Outcome: "error", DurationMS: 100})
	m.recordModelCall(agentapi.ModelCall{Model: "a", Outcome: "cancelled", DurationMS: 50})
	m.recordModelCall(agentapi.ModelCall{Model: "a", Outcome: "later-sdk-outcome"})
	m.recordModelCall(agentapi.ModelCall{Outcome: "error", DurationMS: 100})
	calls(m, "b", "rejected", healthFails)
	stats := m.uamStats()
	for _, want := range []SessionStat{
		{"uam.model_call_a_success", "1"}, {"uam.model_call_a_error", "1"}, {"uam.model_call_a_cancelled", "1"}, {"uam.model_call_a_other", "1"},
		{"uam.model_call_a_ms", "450"}, {"uam.model_recent_ms_a", "200"},
		{"uam.model_call_b_rejected", "3"}, {"uam.model_call_b_ms", "300"}, {"uam.model_recent_ms_b", "100"}, {"uam.model_unhealthy_b", "1"},
	} {
		if !slices.Contains(stats, want) {
			t.Errorf("stats lack %v", want)
		}
	}
	if slices.ContainsFunc(stats, func(s SessionStat) bool { return s.Key == "uam.model_unhealthy_a" || s.Key == "uam.model_call__error" }) {
		t.Errorf("stats = %v", stats)
	}
}

// A provider's model call events reach the health the subagent hook asks.
func TestModelCallEventsSteerTheSubagentHook(t *testing.T) {
	m, prov, _ := newTestManager(t)
	_, conv := createSession(t, m, prov)
	if conv.Request().Hooks.SubagentModel == nil {
		t.Fatal("the conversation was opened without the subagent model hook")
	}
	for range healthFails {
		conv.Emit(agentapi.Event{Kind: agentapi.EventModelCall, ModelCall: &agentapi.ModelCall{Model: "a", Outcome: "error", DurationMS: 10}})
	}
	if got := conv.Request().Hooks.SubagentModel("a", []string{"a", "b"}); got != "b" {
		t.Fatalf("hook kept %q", got)
	}
	conv.Emit(agentapi.Event{Kind: agentapi.EventModelCall, ModelCall: &agentapi.ModelCall{Model: "a", Outcome: "success"}})
	if got := conv.Request().Hooks.SubagentModel("a", []string{"a", "b"}); got != "a" {
		t.Fatalf("after a success the hook moved to %q", got)
	}
}
