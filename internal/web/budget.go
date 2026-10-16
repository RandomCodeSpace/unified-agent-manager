package web

import (
	"cmp"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const (
	// maxBudgetCredits bounds a Thread's credit budget.
	maxBudgetCredits = 1_000_000
	// budgetWarnShare of the credits, or budgetWarnPoints short of the quota
	// limit, the agent is told to wrap up.
	budgetWarnShare  = 0.8
	budgetWarnPoints = 5
)

var errBudgetReached = &Error{Status: http.StatusConflict, Code: "budget_reached", Message: "This Thread reached its budget. Raise or remove the budget to send more."}

// BudgetView is SessionSummary.Budget. Spent is the AI units the Thread's
// conversation reported since the budget was set; QuotaUsed is the highest
// share, in percent, used of the provider's limited account quotas, omitted
// while none is known.
type BudgetView struct {
	Credits      float64  `json:"credits,omitempty"`
	Spent        float64  `json:"spent"`
	QuotaPercent float64  `json:"quota_percent,omitempty"`
	QuotaUsed    *float64 `json:"quota_used,omitempty"`
	Reached      bool     `json:"reached,omitempty"`
}

func budgetRecord(b store.Budget) *store.Budget {
	if b == (store.Budget{}) {
		return nil
	}
	return &b
}

func (s *webSession) usedUnits() float64 {
	if s.usage == nil {
		return 0
	}
	return s.usage.AIUnits
}

func (s *webSession) budgetSpent() float64 {
	return max(0, s.usedUnits()-s.budget.From)
}

// quotaUsedLocked is the highest share used of provider's limited quotas.
func (m *Manager) quotaUsedLocked(provider string) (float64, bool) {
	c := m.quota[provider]
	if c == nil {
		return 0, false
	}
	used, ok := 0.0, false
	for _, q := range c.quotas {
		if !q.Unlimited {
			used, ok = max(used, 100-q.RemainingPercent), true
		}
	}
	return used, ok
}

func (m *Manager) budgetViewLocked(s *webSession) *BudgetView {
	if s.budget.Credits == 0 && s.budget.QuotaPercent == 0 {
		return nil
	}
	v := &BudgetView{Credits: s.budget.Credits, Spent: s.budgetSpent(), QuotaPercent: s.budget.QuotaPercent, Reached: s.budget.Reached}
	if used, ok := m.quotaUsedLocked(s.provider); ok {
		v.QuotaUsed = &used
	}
	return v
}

// budgetOverLocked says why s's budget is spent; "" while it is not.
func (m *Manager) budgetOverLocked(s *webSession) string {
	b := s.budget
	if b.Credits > 0 && s.budgetSpent() >= b.Credits {
		return fmt.Sprintf("Stopped at its budget: %s of %s AI units", creditText(s.budgetSpent()), creditText(b.Credits))
	}
	if used, ok := m.quotaUsedLocked(s.provider); ok && b.QuotaPercent > 0 && used >= b.QuotaPercent {
		return fmt.Sprintf("Stopped at its budget: account quota %s%% used, limit %s%%", creditText(used), creditText(b.QuotaPercent))
	}
	return ""
}

// checkBudgetLocked marks s's budget reached once it is spent, and has uam
// stop a running turn of a Thread whose budget is reached. The caller
// reports the change (changedLocked).
func (m *Manager) checkBudgetLocked(s *webSession) {
	why := m.budgetOverLocked(s)
	if why != "" {
		s.budget.Reached = true
	}
	if m.closed || !s.budget.Reached || s.budgetStopping || s.conv == nil || (!busy(s.state()) && (s.execution == nil || s.execution.Mode != "autopilot")) {
		return
	}
	why = cmp.Or(why, "Stopped at its budget")
	s.budgetStopping = true
	id := s.id
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		_, _ = m.cancelBecause(id, stopBudget, why)
		m.mu.Lock()
		s.budgetStopping = false
		m.mu.Unlock()
	}()
}

// checkQuotaBudgetsLocked checks the Threads with a quota budget after the
// account quotas were read.
func (m *Manager) checkQuotaBudgetsLocked() {
	for _, s := range m.sessions {
		if s.removed || s.budget.QuotaPercent == 0 {
			continue
		}
		before := m.summaryLocked(s)
		m.checkBudgetLocked(s)
		m.changedLocked(s, before)
	}
}

// budgetToolLocked is the budget's part of a tool call: deny once it is
// reached, and a note to wrap up, once, as it nears.
func (m *Manager) budgetToolLocked(s *webSession) (deny, note string) {
	b := s.budget
	if b.Reached {
		return "uam budget: this Thread reached the spending limit its owner set. Stop working, and report what is done and what is left.", ""
	}
	if s.budgetWarned {
		return "", ""
	}
	near := b.Credits > 0 && s.budgetSpent() >= b.Credits*budgetWarnShare
	if used, ok := m.quotaUsedLocked(s.provider); ok && b.QuotaPercent > 0 && used >= b.QuotaPercent-budgetWarnPoints {
		near = true
	}
	if !near {
		return "", ""
	}
	s.budgetWarned = true
	return "", "uam budget: this Thread is close to the spending limit its owner set; uam stops it there. Wrap up: finish the current step and report."
}

// SetBudget sets the Thread's budget, running or not: credits more AI
// units from now and the account quota used up to quotaPercent; zero is no
// limit, both zero remove it. Setting it again starts its credits afresh.
func (m *Manager) SetBudget(id string, credits, quotaPercent float64) (SessionSummary, error) {
	if !(credits >= 0 && credits <= maxBudgetCredits) || !(quotaPercent >= 0 && quotaPercent <= 100) {
		return SessionSummary{}, newError(http.StatusBadRequest, "budget credits must be 0 to %d and quota_percent 0 to 100", maxBudgetCredits)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.removed {
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	if s.stage == StageArchived {
		return SessionSummary{}, s.readOnlyLocked()
	}
	before := m.summaryLocked(s)
	s.budget, s.budgetWarned = store.Budget{}, false
	if credits > 0 || quotaPercent > 0 {
		s.budget = store.Budget{Credits: credits, From: s.usedUnits(), QuotaPercent: quotaPercent}
		m.checkBudgetLocked(s)
	}
	m.changedLocked(s, before)
	if !s.budget.Reached {
		m.kickDrainLocked(s)
	}
	return m.summaryLocked(s), nil
}

// creditText writes AI units as the web UI does (formatCredits): "<0.01"
// for a tiny non-zero amount, two decimals below 1, one below 10.
func creditText(v float64) string {
	switch {
	case v == 0:
		return "0"
	case v < 0.005:
		return "<0.01"
	case v < 1:
		return strconv.FormatFloat(v, 'f', 2, 64)
	case v < 10:
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
}
