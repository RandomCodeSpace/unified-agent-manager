package web

import (
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// A model is unhealthy while its last healthFails calls all failed (the
// provider's "error" or "rejected") and the oldest of them is younger than
// healthWindow. One success restores it at once, and so does the window
// passing: healthFails new failures are needed again. A cancelled call says
// nothing about the model. The rule is deliberately slow to trip, because a
// failure can be the request's own fault and not the model's.
const (
	healthFails  = 3
	healthWindow = 10 * time.Minute
	// healthKept is how many recent calls are kept for each model, and
	// healthModels how many models; the model called longest ago goes first.
	healthKept   = 20
	healthModels = 64
)

type healthCall struct {
	at     time.Time
	failed bool
	ms     int64
}

// modelHealth is each model's recent calls, oldest first, in memory only.
// shortcut: keyed by model ID alone, so two providers serving one ID share
// its health; key by provider too when a second provider has subagents.
type modelHealth struct {
	mu    sync.Mutex
	calls map[string][]healthCall
}

func (h *modelHealth) record(model string, call healthCall) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.calls == nil {
		h.calls = map[string][]healthCall{}
	}
	if _, ok := h.calls[model]; !ok && len(h.calls) >= healthModels {
		oldest, at := "", time.Time{}
		for id, calls := range h.calls {
			if last := calls[len(calls)-1].at; oldest == "" || last.Before(at) {
				oldest, at = id, last
			}
		}
		delete(h.calls, oldest)
	}
	calls := append(h.calls[model], call)
	h.calls[model] = calls[max(len(calls)-healthKept, 0):]
}

func (h *modelHealth) unhealthy(model string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return unhealthyCalls(h.calls[model], now)
}

func unhealthyCalls(calls []healthCall, now time.Time) bool {
	if len(calls) < healthFails {
		return false
	}
	last := calls[len(calls)-healthFails:]
	for _, c := range last {
		if !c.failed {
			return false
		}
	}
	return now.Sub(last[0].at) < healthWindow
}

// stats adds each model's mean duration over its kept calls, and a mark on
// the models unhealthy now, to counts.
func (h *modelHealth) stats(now time.Time, counts map[string]int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for model, calls := range h.calls {
		var ms int64
		for _, c := range calls {
			ms += c.ms
		}
		counts["model_recent_ms_"+model] = int(ms / int64(len(calls)))
		if unhealthyCalls(calls, now) {
			counts["model_unhealthy_"+model] = 1
		}
	}
}

// recordModelCall counts a finished model call by model and outcome, adds its
// duration to the model's total, and keeps it for the model's health.
func (m *Manager) recordModelCall(c agentapi.ModelCall) {
	if c.Model == "" {
		return
	}
	failed := c.Outcome == "error" || c.Outcome == "rejected"
	outcome := c.Outcome
	if !failed && outcome != "success" && outcome != "cancelled" {
		outcome = "other"
	}
	m.count("model_call_" + c.Model + "_" + outcome)
	m.countN("model_call_"+c.Model+"_ms", int(c.DurationMS))
	if failed || outcome == "success" {
		m.health.record(c.Model, healthCall{at: m.now(), failed: failed, ms: c.DurationMS})
	}
}

// subagentModel moves a subagent about to start on an unhealthy model to the
// first healthy one of usable, the allowed models in the owner's order. With
// none it keeps picked: a model that may work beats no subagent.
func (m *Manager) subagentModel(picked string, usable []string) string {
	now := m.now()
	if !m.health.unhealthy(picked, now) {
		return picked
	}
	for _, id := range usable {
		if !m.health.unhealthy(id, now) {
			log.Info("subagent moved off an unhealthy model", "from", picked, "to", id)
			m.count("subagent_model_health_moves")
			return id
		}
	}
	return picked
}
