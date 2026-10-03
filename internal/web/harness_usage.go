package web

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/aiusage-core/adapter"
	"github.com/RandomCodeSpace/aiusage-core/adapter/all"
	"github.com/RandomCodeSpace/aiusage-core/collect"
	coremodel "github.com/RandomCodeSpace/aiusage-core/model"
	corestore "github.com/RandomCodeSpace/aiusage-core/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// HarnessCollection describes the last local-file collection, independently
// of the live SDK counters. A successful scan does not imply complete history.
type HarnessCollection struct {
	Status       string    `json:"status"`
	UpdatedAt    time.Time `json:"updated_at,omitzero"`
	CopilotSince time.Time `json:"copilot_since"`
}

type copilotOwnership struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end,omitzero"`
}

type harnessUsage struct {
	collection HarnessCollection
	today      string
	periods    map[string][]corestore.Bucket
}

func (m *Manager) prepareHarnessUsage() (*corestore.Ledger, error) {
	if m.usageHome == "" {
		return nil, nil
	}
	m.harness = &harnessUsage{collection: HarnessCollection{Status: "starting", CopilotSince: m.now()}}
	if m.tokens.CopilotSince.IsZero() {
		// Legacy daily totals have no call identities. Import external Copilot
		// only after this durable boundary, while preserving those totals.
		m.tokens.CopilotSince = m.now()
		m.tokens.Version = 2
		m.tokens.revision++
	}
	if m.tokens.CopilotSessions == nil {
		m.tokens.CopilotSessions = map[string][]copilotOwnership{}
	}
	// A crash leaves no trustworthy release time. Keep that interval bounded
	// at recovery and report incomplete coverage instead of inventing counts.
	for id, intervals := range m.tokens.CopilotSessions {
		for i := range intervals {
			if intervals[i].End.IsZero() {
				intervals[i].End = m.now()
				m.tokens.CopilotUnattributed = true
				m.tokens.revision++
			}
		}
		m.tokens.CopilotSessions[id] = intervals
	}
	m.harness.collection.CopilotSince = m.tokens.CopilotSince
	m.persistMu.Lock()
	err := m.flushTokenLedger()
	m.persistMu.Unlock()
	if err != nil {
		return nil, err
	}
	// Ownership is recorded before inference, including utility sessions, and
	// retained after a Task is deleted. SDK and OTEL never own the same call.
	if p, ok := m.providers["copilot"].(agentapi.UsageSessionRecorder); ok {
		p.SetUsageSessionRecorder(m.recordCopilotUsageSession)
	}
	ledger, err := corestore.Open(filepath.Join(filepath.Dir(m.store.Path()), "web-token-usage.db"))
	if err != nil {
		m.harness.collection.Status = "unavailable"
		log.Warn("open harness usage failed", "error", err)
		return nil, nil // SDK usage and ownership remain available for recovery.
	}
	return ledger, nil
}

func (m *Manager) recordCopilotUsageSession(id string, active bool) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("record Copilot usage ownership: empty session ID")
	}
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.Lock()
	intervals := m.tokens.CopilotSessions[id]
	previous := slices.Clone(intervals)
	open := len(intervals) > 0 && intervals[len(intervals)-1].End.IsZero()
	if active && !open {
		m.tokens.CopilotSessions[id] = append(intervals, copilotOwnership{Start: m.now()})
		m.tokens.revision++
	} else if !active && open {
		intervals[len(intervals)-1].End = m.now()
		m.tokens.revision++
	}
	m.mu.Unlock()
	err := m.flushTokenLedger()
	if err != nil && active && !open {
		// Inference is refused when activation cannot be saved. Do not let
		// the regular flush later persist an owner that never started.
		m.mu.Lock()
		if len(previous) == 0 {
			delete(m.tokens.CopilotSessions, id)
		} else {
			m.tokens.CopilotSessions[id] = previous
		}
		m.tokens.revision++
		m.mu.Unlock()
	}
	return err
}

// usageOnlyStore retains core's transaction/checkpoint machinery while omitting
// activity and content unrelated to usage. Copilot sessions owned by UAM keep
// their SDK counts, including BYOK selection IDs and background calls.
type usageOnlyStore struct {
	*corestore.Ledger
	m *Manager
}

func (s usageOnlyStore) ApplyBatch(ctx context.Context, b corestore.ObservationBatch) (corestore.Applied, error) {
	// An activation is provisional until its file write succeeds. Serialize
	// classification with that write/rollback, using the persistence lock order.
	s.m.persistMu.Lock()
	s.m.mu.Lock()
	events := b.Events[:0]
	for _, event := range b.Events {
		if event.Tool == coremodel.ToolCopilot {
			if event.EventTime.Before(s.m.tokens.CopilotSince) {
				continue
			}
			if s.m.copilotOwnedAtLocked(event.SessionID, event.EventTime) {
				continue
			}
			// The adapter may substitute a trace ID when no conversation ID
			// exists. Such a record cannot safely be classified as external.
			_, traceErr := hex.DecodeString(event.SessionID)
			if event.SessionID == "" || event.SessionID == "unknown-session" || (len(event.SessionID) == 32 && traceErr == nil) {
				if !s.m.tokens.CopilotUnattributed {
					s.m.tokens.CopilotUnattributed = true
					s.m.tokens.revision++
				}
				continue
			}
		}
		event.Raw = ""
		event.Project = ""
		events = append(events, event)
	}
	s.m.mu.Unlock()
	// Save an incomplete-coverage marker before advancing its source checkpoint.
	err := s.m.flushTokenLedger()
	s.m.persistMu.Unlock()
	if err != nil {
		return corestore.Applied{}, err
	}
	b.Events, b.Activity, b.TurnContexts, b.CodeChanges = events, nil, nil, nil
	return s.Ledger.ApplyBatch(ctx, b)
}

func (m *Manager) copilotOwnedAtLocked(id string, at time.Time) bool {
	for _, interval := range m.tokens.CopilotSessions[id] {
		if !at.Before(interval.Start) && (interval.End.IsZero() || at.Before(interval.End)) {
			return true
		}
	}
	return false
}

func (m *Manager) harnessUsageLoop(ledger *corestore.Ledger) {
	defer m.wg.Done()
	// A timed-out Shutdown must not close an active reader.
	defer func() {
		if err := ledger.Close(); err != nil {
			log.Warn("close harness usage ledger failed", "error", err)
		}
	}()
	err := collect.Run(m.ctx, time.Minute, all.Default(), usageOnlyStore{Ledger: ledger, m: m}, adapter.DiscoverConfig{Home: m.usageHome},
		collect.WithoutRaw(), collect.WithCycleCallback(func(stats collect.CycleStats, cycleErr error) {
			if m.ctx.Err() != nil {
				return
			}
			if err := m.refreshHarnessUsage(m.ctx, ledger.Reader, len(stats.Errors) > 0 || cycleErr != nil); err != nil {
				m.mu.Lock()
				m.harness.collection.Status = "partial"
				m.mu.Unlock()
				log.Warn("summarize harness usage failed", "error", err)
			}
			if cycleErr != nil || len(stats.Errors) > 0 {
				// Source errors may contain local paths. Keep the browser's
				// status small and never copy source payloads into its response.
				log.Warn("harness usage collection incomplete", "sources", stats.Sources, "failed", stats.SourcesFailed, "errors", len(stats.Errors))
			}
		}))
	if err != nil {
		m.mu.Lock()
		m.harness.collection.Status = "unavailable"
		m.mu.Unlock()
		log.Warn("harness usage collector stopped", "error", err)
	}
}

func (m *Manager) refreshHarnessUsage(ctx context.Context, reader *corestore.Reader, partial bool) error {
	now := m.now().Local()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	periods := make(map[string][]corestore.Bucket, 4)
	for period, since := range map[string]time.Time{"today": midnight, "7d": midnight.AddDate(0, 0, -6), "30d": midnight.AddDate(0, 0, -29), "lifetime": {}} {
		summary, err := reader.Summarize(ctx, corestore.Filter{Since: since, Until: midnight.AddDate(0, 0, 1), GroupBy: []string{"tool", "model"}})
		if err != nil {
			return err
		}
		periods[period] = summary.Buckets
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.harness.periods, m.harness.today = periods, dayOf(now)
	m.harness.collection.UpdatedAt = now
	m.harness.collection.Status = "ready"
	if partial || m.tokens.CopilotUnattributed {
		m.harness.collection.Status = "partial"
	}
	return nil
}

func harnessModel(value string) string {
	if value = strings.TrimSpace(displaytext.Sanitize(value)); value != "" {
		return value
	}
	return "Unknown model"
}

type harnessCost struct {
	counts   TokenCounts
	reported float64
	priced   int64
	unpriced int64
	costOnly float64
}

func (m *Manager) mergeHarnessTokensLocked(period, today string, models map[string]ModelTokens, p *TokenPeriod) map[string]harnessCost {
	costs := map[string]harnessCost{}
	if m.harness == nil || (m.harness.today != today && period != "lifetime") {
		return costs // A previous-day cache must not masquerade as Today.
	}
	for _, b := range m.harness.periods[period] {
		harness, name := b.Keys["tool"], harnessModel(b.Keys["model"])
		counts := TokenCounts{Input: b.Input + b.CacheRead + b.CacheCreation, Output: b.Output, CacheRead: b.CacheRead, CacheWrite: b.CacheCreation, Total: b.Total}
		if coremodel.ReasoningModeFor(harness) == coremodel.ReasoningAdditive {
			counts.Output += b.Reasoning
		}
		key := harness + "\x00" + name
		row := models[key]
		row.Provider, row.Model = harness, name
		row.add(counts)
		models[key] = row
		p.Total.add(counts)
		c := costs[key]
		c.counts.add(counts)
		c.reported += float64(b.CostMicroUSD) / 1e6
		c.priced += b.Events - b.UnpricedEvents
		c.unpriced += b.UnpricedEvents
		if counts.Total == 0 && counts.Input == 0 && counts.Output == 0 {
			c.costOnly += float64(b.CostMicroUSD) / 1e6
		}
		costs[key] = c
	}
	return costs
}

func (m *Manager) modelTokenCostLocked(row ModelTokens, core harnessCost) (*float64, bool) {
	if rates := m.tokenPriceLocked(row.Provider, row.Model).Rates; rates != nil && row.Total > 0 {
		cost := tokenCost(row.TokenCounts, rates)
		*cost += core.costOnly
		return cost, false
	}
	// Preserve source-reported costs, including cost-only harnesses. A missing
	// model price is unknown, never a claim that its requests were free.
	partial := core.unpriced > 0 || row.Total != core.counts.Total
	if core.priced > 0 {
		cost := core.reported
		return &cost, partial
	}
	return nil, false
}
