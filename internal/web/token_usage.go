package web

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// TokenCounts includes cache reads and writes in Input. Total preserves the
// harness-authoritative total; optional reasoning counters are not added twice.
type TokenCounts struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Total      int64 `json:"total"`
}

func (t *TokenCounts) add(v TokenCounts) {
	t.Input += v.Input
	t.Output += v.Output
	t.CacheRead += v.CacheRead
	t.CacheWrite += v.CacheWrite
	t.Total += v.Total
}

type ModelTokens struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	TokenCounts
	CostUSD     *float64 `json:"cost_usd"`
	CostPartial bool     `json:"cost_partial,omitempty"`
}

type TokenPeriod struct {
	Models         []ModelTokens `json:"models"`
	Total          TokenCounts   `json:"total"`
	CostUSD        *float64      `json:"cost_usd"`
	UnpricedModels int           `json:"unpriced_models"`
}

type TokenUsageReport struct {
	Since      time.Time              `json:"since"`
	Today      string                 `json:"today"`
	Periods    map[string]TokenPeriod `json:"periods"`
	Collection *HarnessCollection     `json:"collection,omitempty"`
}

type tokenDay struct {
	Day string `json:"day"`
	ModelTokens
}

// Daily aggregates have no transcript or task dependency, so removing a
// task cannot subtract its usage. There is no retention cutoff for Lifetime.
// CopilotPruned is the time before which Copilot ownership was dropped
// (pruneCopilotOwnership); zero while none was.
type tokenLedger struct {
	Version             int                           `json:"version"`
	Since               time.Time                     `json:"since"`
	Days                map[string]tokenDay           `json:"days"`
	CopilotSince        time.Time                     `json:"copilot_since,omitzero"`
	CopilotSessions     map[string][]copilotOwnership `json:"copilot_sessions,omitempty"`
	CopilotPruned       time.Time                     `json:"copilot_pruned,omitzero"`
	CopilotUnattributed bool                          `json:"copilot_unattributed,omitempty"`
	revision, saved     uint64
}

func (m *Manager) tokenLedgerPath() string {
	return filepath.Join(filepath.Dir(m.store.Path()), "web-token-usage.json")
}

// loadTokenLedger reads the saved totals. A file that cannot be read or is not
// a ledger this version knows is set aside under a .bak name and a fresh
// ledger starts: statistics never keep the service from starting.
func (m *Manager) loadTokenLedger() {
	path := m.tokenLedgerPath()
	fresh := tokenLedger{Version: 1, Since: m.now(), Days: map[string]tokenDay{}, revision: 1}
	data, err := os.ReadFile(path) // #nosec G304 -- uam's own file beside the store.
	if errors.Is(err, os.ErrNotExist) {
		m.tokens = fresh
		return
	}
	var loaded tokenLedger
	if err == nil {
		err = json.Unmarshal(data, &loaded)
	}
	known := loaded.Version == 1 || (loaded.Version == 2 && !loaded.CopilotSince.IsZero())
	if err == nil && known && !loaded.Since.IsZero() && loaded.Days != nil {
		m.tokens = loaded
		return
	}
	// The decode error can quote the file, so only the paths are logged. If
	// the move fails, the next save replaces the file with the fresh ledger.
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().UnixNano())
	if err := os.Rename(path, backup); err != nil {
		backup = ""
	}
	log.Error("token usage file could not be read; started a fresh ledger", "path", path, "backup", backup)
	m.tokens = fresh
}

// Called under m.mu. Disk writes stay in the existing persistence worker.
func (m *Manager) recordTokensLocked(provider string, usage agentapi.TokenUsage) {
	model := strings.TrimSpace(displaytext.Sanitize(usage.Model))
	if model == "" {
		model = "Unknown model"
	}
	counts := TokenCounts{Input: max(usage.Input, 0), Output: max(usage.Output, 0), CacheRead: max(usage.CacheRead, 0), CacheWrite: max(usage.CacheWrite, 0)}
	if counts == (TokenCounts{}) {
		return
	}
	counts.Total = counts.Input + counts.Output
	at := usage.Time
	if at.IsZero() || at.After(m.now()) {
		at = m.now()
	}
	day := dayOf(at)
	key := day + "\x00" + provider + "\x00" + model
	d := m.tokens.Days[key]
	d.Day, d.Provider, d.Model = day, provider, model
	d.add(counts)
	m.tokens.Days[key] = d
	m.tokens.revision++
	m.flushLaterLocked()
}

// The caller holds persistMu; only the snapshot is taken under mu.
func (m *Manager) flushTokenLedger() error {
	m.mu.Lock()
	revision := m.tokens.revision
	if revision == m.tokens.saved {
		m.mu.Unlock()
		return nil
	}
	data, err := json.Marshal(m.tokens)
	m.mu.Unlock()
	if err == nil {
		err = writeFileAtomic(m.tokenLedgerPath(), ".token-usage-*", data)
	}
	if err != nil {
		return fmt.Errorf("persist token usage: %w", err)
	}
	m.mu.Lock()
	m.tokens.saved = revision
	m.mu.Unlock()
	return nil
}

func (m *Manager) TokenUsage() TokenUsageReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().Local()
	today := dayOf(now)
	out := TokenUsageReport{Since: m.tokens.Since, Today: today, Periods: map[string]TokenPeriod{}}
	for period, start := range map[string]string{"today": today, "7d": dayOf(now.AddDate(0, 0, -6)), "30d": dayOf(now.AddDate(0, 0, -29)), "lifetime": ""} {
		models := map[string]ModelTokens{}
		p := TokenPeriod{Models: []ModelTokens{}}
		for _, d := range m.tokens.Days {
			if d.Day < start || d.Day > today {
				continue
			}
			key := d.Provider + "\x00" + d.Model
			model := models[key]
			model.Provider, model.Model = d.Provider, d.Model
			model.add(d.TokenCounts)
			models[key] = model
			p.Total.add(d.TokenCounts)
		}
		coreCosts := m.mergeHarnessTokensLocked(period, today, models, &p)
		cost, priced := 0.0, 0
		for _, model := range models {
			model.CostUSD, model.CostPartial = m.modelTokenCostLocked(model, coreCosts[model.Provider+"\x00"+model.Model])
			if model.CostUSD == nil || model.CostPartial {
				p.UnpricedModels++
			}
			if model.CostUSD != nil {
				cost += *model.CostUSD
				priced++
			}
			p.Models = append(p.Models, model)
		}
		if priced > 0 || len(models) == 0 {
			p.CostUSD = &cost
		}
		slices.SortFunc(p.Models, func(a, b ModelTokens) int {
			return cmp.Or(cmp.Compare(b.Total, a.Total), strings.Compare(a.Provider, b.Provider), strings.Compare(a.Model, b.Model))
		})
		out.Periods[period] = p
	}
	if m.harness != nil {
		status := m.harness.collection
		if m.harness.today != "" && m.harness.today != today {
			status.Status = "partial"
		}
		out.Collection = &status
	}
	return out
}

func (s *Server) handleTokenUsage(w http.ResponseWriter, _ *http.Request) {
	s.m.requestHarnessUsage()
	writeJSON(w, http.StatusOK, s.m.TokenUsage())
}
