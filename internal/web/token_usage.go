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
)

// TokenCounts keeps cache separate for inspection. Total is input + output:
// the provider's input already includes cache reads and writes.
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
	t.Total = t.Input + t.Output
}

type ModelTokens struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	TokenCounts
	CostUSD *float64 `json:"cost_usd"`
}

type TokenPeriod struct {
	Models         []ModelTokens `json:"models"`
	Total          TokenCounts   `json:"total"`
	CostUSD        *float64      `json:"cost_usd"`
	UnpricedModels int           `json:"unpriced_models"`
}

type TokenUsageReport struct {
	Since   time.Time              `json:"since"`
	Today   string                 `json:"today"`
	Periods map[string]TokenPeriod `json:"periods"`
}

type tokenDay struct {
	Day string `json:"day"`
	ModelTokens
}

// Daily aggregates have no transcript or task dependency, so removing a
// task cannot subtract its usage. There is no retention cutoff for Lifetime.
type tokenLedger struct {
	Version         int                 `json:"version"`
	Since           time.Time           `json:"since"`
	Days            map[string]tokenDay `json:"days"`
	revision, saved uint64
}

func (m *Manager) tokenLedgerPath() string {
	return filepath.Join(filepath.Dir(m.store.Path()), "web-token-usage.json")
}

func (m *Manager) loadTokenLedger() error {
	data, err := os.ReadFile(m.tokenLedgerPath())
	if errors.Is(err, os.ErrNotExist) {
		m.tokens = tokenLedger{Version: 1, Since: m.now(), Days: map[string]tokenDay{}, revision: 1}
		return nil
	}
	if err == nil {
		err = json.Unmarshal(data, &m.tokens)
	}
	if err != nil {
		return fmt.Errorf("load token usage: %w", err)
	}
	if m.tokens.Version != 1 || m.tokens.Since.IsZero() || m.tokens.Days == nil {
		return fmt.Errorf("load token usage: unsupported or incomplete ledger")
	}
	return nil
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
	at := usage.Time
	if at.IsZero() || at.After(m.now()) {
		at = m.now()
	}
	day := dayOf(at)
	key := day + "\x00" + provider + "\x00" + model
	d := m.tokens.Days[key]
	d.Day, d.Provider, d.Model = day, provider, model
	d.TokenCounts.add(counts)
	m.tokens.Days[key] = d
	m.tokens.revision++
	select {
	case m.wake <- struct{}{}:
	default:
	}
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
			model.TokenCounts.add(d.TokenCounts)
			models[key] = model
			p.Total.add(d.TokenCounts)
		}
		cost, priced := 0.0, 0
		for _, model := range models {
			model.CostUSD = tokenCost(model.TokenCounts, m.tokenPriceLocked(model.Provider, model.Model).Rates)
			if model.CostUSD == nil {
				p.UnpricedModels++
			} else {
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
	return out
}

func (s *Server) handleTokenUsage(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.m.TokenUsage())
}
