package web

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Prices are bundled at compile time, never fetched by the running service.
// Refresh with scripts/update-model-prices.py <LiteLLM commit SHA>.
//
//go:embed model-prices.json
var bundledPriceJSON []byte

type priceSnapshot struct {
	Commit string                         `json:"commit"`
	Models map[string]store.WebTokenPrice `json:"models"`
}

var bundledPrices = sync.OnceValue(func() priceSnapshot {
	var prices priceSnapshot
	if err := json.Unmarshal(bundledPriceJSON, &prices); err != nil {
		panic(err)
	}
	return prices
})

type ModelTokenPrice struct {
	Provider string               `json:"provider"`
	Model    string               `json:"model"`
	Rates    *store.WebTokenPrice `json:"rates"`
	Source   string               `json:"source"` // manual, bundled, or unpriced
}

// Match exact registry IDs first. Copilot spells Claude versions with a dot;
// Anthropic's registry IDs spell that same version with a hyphen. Arbitrary
// provider prefixes are never stripped, so custom endpoints need their own rate.
func (m *Manager) tokenPriceLocked(provider, model string) ModelTokenPrice {
	p := ModelTokenPrice{Provider: provider, Model: model, Source: "unpriced"}
	if rates, ok := m.settings.TokenPrices[provider][model]; ok && rates.Valid() {
		p.Rates, p.Source = &rates, "manual"
		return p
	}
	ids := []string{provider + "/" + model, model}
	if provider == "copilot" && strings.HasPrefix(model, "claude-") {
		ids = append(ids, strings.ReplaceAll(model, ".", "-"))
	}
	for _, id := range ids {
		if rates, ok := bundledPrices().Models[id]; ok && rates.Valid() {
			p.Rates, p.Source = &rates, "bundled"
			break
		}
	}
	return p
}

// Estimates deliberately use the current base rates for every period. These
// are comparable token costs, not historical invoices or Copilot credits.
func tokenCost(counts TokenCounts, rates *store.WebTokenPrice) *float64 {
	if rates == nil {
		return nil
	}
	read := min(counts.CacheRead, counts.Input)
	write := min(counts.CacheWrite, counts.Input-read)
	r, w := *rates.Input, *rates.Input
	if rates.CacheRead != nil {
		r = *rates.CacheRead
	}
	if rates.CacheWrite != nil {
		w = *rates.CacheWrite
	}
	cost := (float64(counts.Input-read-write)**rates.Input + float64(read)*r + float64(write)*w + float64(counts.Output)**rates.Output) / 1e6
	return &cost
}

func (s *Server) handleTokenPrices(w http.ResponseWriter, _ *http.Request) {
	s.m.mu.Lock()
	models := map[string]ModelTokenPrice{}
	add := func(provider, model string) { models[provider+"\x00"+model] = s.m.tokenPriceLocked(provider, model) }
	for provider, info := range s.m.infos {
		for _, model := range info.Models {
			add(provider, model.ID)
		}
	}
	for provider, rates := range s.m.settings.TokenPrices {
		for model := range rates {
			add(provider, model)
		}
	}
	for _, day := range s.m.tokens.Days {
		add(day.Provider, day.Model)
	}
	rows := make([]ModelTokenPrice, 0, len(models))
	for _, row := range models {
		rows = append(rows, row)
	}
	s.m.mu.Unlock()
	slices.SortFunc(rows, func(a, b ModelTokenPrice) int {
		return cmp.Or(strings.Compare(a.Provider, b.Provider), strings.Compare(a.Model, b.Model))
	})
	writeJSON(w, http.StatusOK, struct {
		Commit string            `json:"commit"`
		Models []ModelTokenPrice `json:"models"`
	}{bundledPrices().Commit, rows})
}
