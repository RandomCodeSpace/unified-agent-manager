package web

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func budgetSession(t *testing.T, m *Manager, id string) *webSession {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func TestBudgetCreditsOnARunningThreadStopsItAndHoldsUntilChanged(t *testing.T) {
	m, prov := startUsageManager(t, nil, nil)
	sum, conv := createSession(t, m, prov)
	report := func(units float64) {
		conv.Emit(agentapi.Event{Kind: agentapi.EventUsage, Usage: &agentapi.Usage{AIUnits: units}})
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	report(5)

	got, err := m.SetBudget(sum.ID, 2, 0)
	if err != nil || got.Budget == nil || got.Budget.Credits != 2 || got.Budget.Spent != 0 || got.Budget.Reached {
		t.Fatalf("SetBudget = %+v, %v", got.Budget, err)
	}
	s := budgetSession(t, m, sum.ID)
	use := agentapi.ToolUse{Tool: "bash"}
	if v := m.preToolUse(t.Context(), s, use); v.Deny != "" || v.Context != "" {
		t.Fatalf("tool under budget = %+v", v)
	}
	report(6.7) // 1.7 of 2: past the warning share
	if v := m.preToolUse(t.Context(), s, use); v.Deny != "" || !strings.Contains(v.Context, "uam budget") {
		t.Fatalf("tool near budget = %+v", v)
	}
	if v := m.preToolUse(t.Context(), s, use); v.Context != "" {
		t.Fatalf("warning repeated: %+v", v)
	}
	report(7.1)
	waitUntil(t, "the budget stop", func() bool { return conv.Cancels() == 1 })
	d := detail(t, m, sum.ID)
	if d.Budget == nil || !d.Budget.Reached || d.Budget.Spent < 2 {
		t.Fatalf("budget after the stop = %+v", d.Budget)
	}
	if v := m.preToolUse(t.Context(), s, use); v.Deny == "" {
		t.Fatalf("tool after budget = %+v", v)
	}
	conv.EmitTurn(agentapi.TurnCancelled, "")
	if got := detail(t, m, sum.ID); got.StopReason != stopBudget || !strings.Contains(got.StateDetail, "2.1 of 2.0 AI credits") {
		t.Fatalf("stop = %q %q", got.StopReason, got.StateDetail)
	}
	_, err = m.Submit(sum.ID, PromptRequest{Text: "more", RequestID: mustUUID(t), Mode: ModeSend})
	var coded *Error
	if !errors.As(err, &coded) || coded.Status != http.StatusConflict || coded.Code != "budget_reached" {
		t.Fatalf("send after budget = %v", err)
	}

	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	if rec, _ := loadRecord(t, m.store, "fake", sum.ID); rec.Web == nil || rec.Web.Budget == nil || !rec.Web.Budget.Reached || rec.Web.Budget.From != 5 {
		t.Fatalf("stored budget = %+v", rec.Web)
	}

	// Raising it gives that many credits from now and lets work go on.
	if got, err := m.SetBudget(sum.ID, 3, 0); err != nil || got.Budget.Reached || got.Budget.Spent != 0 {
		t.Fatalf("raised budget = %+v, %v", got.Budget, err)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "more", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatalf("send after raising = %v", err)
	}
	if got, err := m.SetBudget(sum.ID, 0, 0); err != nil || got.Budget != nil {
		t.Fatalf("removed budget = %+v, %v", got.Budget, err)
	}
	if _, err := m.SetBudget(sum.ID, -1, 0); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("negative budget = %v", err)
	}
}

func TestBudgetQuotaPercentStopsAfterAQuotaRead(t *testing.T) {
	m, prov := startUsageManager(t, []agentapi.Quota{premium(600)}, nil) // 40% used
	sum, conv := createSession(t, m, prov)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	got, err := m.SetBudget(sum.ID, 0, 80)
	if err != nil || got.Budget == nil || got.Budget.Reached || got.Budget.QuotaUsed == nil || *got.Budget.QuotaUsed != 40 {
		t.Fatalf("SetBudget = %+v, %v", got.Budget, err)
	}
	prov.SetQuota([]agentapi.Quota{premium(1260)}, nil) // 84% used
	m.refreshQuota()
	waitUntil(t, "the quota budget stop", func() bool { return conv.Cancels() == 1 })
	if d := detail(t, m, sum.ID); d.Budget == nil || !d.Budget.Reached || !strings.Contains(d.StateDetail+budgetSession(t, m, sum.ID).stopReason, "account quota 84% used") {
		t.Fatalf("budget = %+v, detail %q", d.Budget, d.StateDetail)
	}
}

func TestBudgetCreditTextMatchesTheWebUI(t *testing.T) {
	for v, want := range map[float64]string{0: "0", 0.0001: "<0.01", 0.0196: "0.02", 2: "2.0", 2.14: "2.1", 84: "84", 1234.6: "1235"} {
		if got := creditText(v); got != want {
			t.Errorf("creditText(%v) = %q, want %q", v, got, want)
		}
	}
}
