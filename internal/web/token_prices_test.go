package web

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestTokenCostCacheOptionalAndZeroPrices(t *testing.T) {
	counts := TokenCounts{Input: 1_000_000, Output: 100_000, CacheRead: 600_000, CacheWrite: 100_000}
	for _, tc := range []struct {
		name  string
		rates store.WebTokenPrice
		want  float64
	}{
		{"no cache prices", store.WebTokenPrice{Input: new(2.0), Output: new(10.0)}, 3},
		{"cache discounts", store.WebTokenPrice{Input: new(2.0), Output: new(10.0), CacheRead: new(0.2), CacheWrite: new(2.5)}, 1.97},
		{"free cache reads", store.WebTokenPrice{Input: new(2.0), Output: new(10.0), CacheRead: new(0.0)}, 1.8},
		{"free model", store.WebTokenPrice{Input: new(0.0), Output: new(0.0)}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if cost := tokenCost(counts, &tc.rates); cost == nil || math.Abs(*cost-tc.want) > 1e-10 {
				t.Fatalf("cost = %v, want %v", cost, tc.want)
			}
		})
	}
	if tokenCost(counts, nil) != nil {
		t.Fatal("missing rates treated as free")
	}
}

func TestBundledTokenPricesAndOverrides(t *testing.T) {
	m, _, st := newTestManager(t)
	if got := m.tokenPriceLocked("copilot", "gpt-5"); got.Source != "bundled" || got.Rates == nil || !got.Rates.Valid() {
		t.Fatalf("bundled: %+v", got)
	}
	if got := m.tokenPriceLocked("copilot", "claude-sonnet-4.5"); got.Source != "bundled" {
		t.Fatalf("Claude alias: %+v", got)
	}
	if got := m.tokenPriceLocked("copilot", "private/gpt-5"); got.Source != "unpriced" {
		t.Fatalf("custom prefix matched unrelated rate: %+v", got)
	}
	for model, rates := range bundledPrices().Models {
		if !rates.Valid() {
			t.Fatalf("invalid bundled model %q", model)
		}
	}
	prices := map[string]map[string]store.WebTokenPrice{"fake": {"missing": {Input: new(2.0), Output: new(10.0)}}}
	if _, err := m.UpdateSettings(SettingsPatch{TokenPrices: &prices}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.recordTokensLocked("fake", agentapi.TokenUsage{Model: "missing", Input: 1_000_000, Output: 100_000, CacheRead: 600_000})
	m.recordTokensLocked("fake", agentapi.TokenUsage{Model: "unpriced", Input: 100})
	m.mu.Unlock()
	p := m.TokenUsage().Periods["today"]
	if p.CostUSD == nil || *p.CostUSD != 3 || p.UnpricedModels != 1 {
		t.Fatalf("partial total: %+v", p)
	}
	again := startManager(t, st)
	if got := again.tokenPriceLocked("fake", "missing"); got.Source != "manual" || *got.Rates.Input != 2 || got.Rates.CacheRead != nil {
		t.Fatalf("restart: %+v", got)
	}
	empty := map[string]map[string]store.WebTokenPrice{}
	if _, err := m.UpdateSettings(SettingsPatch{TokenPrices: &empty}); err != nil {
		t.Fatal(err)
	}
	if p := m.TokenUsage().Periods["today"]; p.CostUSD != nil || p.UnpricedModels != 2 {
		t.Fatalf("removed override: %+v", p)
	}
}

func TestTokenPricesAPIValidation(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := ts.login(t, "127.0.0.1:8260")
	for _, rates := range []string{`{"input":-1,"output":2}`, `{"output":2}`, `{"input":1}`, `{"input":1,"output":2,"cache_read":-1}`, `null`} {
		w := ts.do(http.MethodPatch, "/api/settings", `{"token_prices":{"fake":{"m":`+rates+`}}}`, auth)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("rates %s: status %d", rates, w.Code)
		}
	}
	w := ts.do(http.MethodPatch, "/api/settings", `{"token_prices":{"fake":{"missing":{"input":0,"output":2}}}}`, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("optional cache refused: %d %s", w.Code, w.Body.String())
	}
	w = ts.do(http.MethodGet, "/api/usage/prices", "", auth)
	if w.Code != http.StatusOK {
		t.Fatalf("pricing catalog: %d", w.Code)
	}
	var catalog struct {
		Models []ModelTokenPrice `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	for _, row := range catalog.Models {
		if row.Provider == "fake" && row.Model == "missing" && row.Source == "manual" && row.Rates != nil && *row.Rates.Input == 0 && row.Rates.CacheRead == nil {
			return
		}
	}
	t.Fatalf("manual price missing: %+v", catalog)
}

func TestTokenPricesCustomModelSelector(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: frameAssets()})
	auth := withCookie(ts)
	model := acme
	model.Name = strings.Repeat("p", store.MaxCustomModelNameBytes)
	model.ModelID = strings.Repeat("m", store.MaxHiddenModelBytes)
	body, err := json.Marshal(map[string]any{"custom_models": []store.WebCustomModel{model}})
	if err != nil {
		t.Fatal(err)
	}
	if w := ts.do(http.MethodPatch, "/api/settings", string(body), auth); w.Code != http.StatusOK {
		t.Fatalf("configure custom model: %d %s", w.Code, w.Body)
	}
	selector := model.Name + "/" + model.ModelID
	prices := func(provider, id string) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{"token_prices": map[string]any{provider: map[string]any{id: map[string]int{"input": 1, "output": 2}}}})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	if w := ts.do(http.MethodPatch, "/api/settings", prices("fake", selector), auth); w.Code != http.StatusOK {
		t.Fatalf("price valid custom selector of %d bytes: %d %s", len(selector), w.Code, w.Body)
	}
	if got := ts.m.tokenPriceLocked("fake", selector); got.Source != "manual" || got.Rates == nil || *got.Rates.Input != 1 {
		t.Fatalf("custom model price: %+v", got)
	}
	for _, bad := range []struct{ provider, model string }{
		{strings.Repeat("p", store.MaxHiddenModelBytes+1), selector},
		{"fake", strings.Repeat("m", store.MaxHiddenModelBytes+1)},
		{"fake", model.Name + "p/" + model.ModelID},
		{"fake", model.Name + "/" + model.ModelID + "m"},
		{"fake", model.Name + "/" + strings.Repeat("m", store.MaxHiddenModelBytes-1) + "\n"},
		{"fake", model.Name + "/" + strings.Repeat("m", store.MaxHiddenModelBytes-1) + " "},
	} {
		if w := ts.do(http.MethodPatch, "/api/settings", prices(bad.provider, bad.model), auth); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid pricing selector accepted: %d %s", w.Code, w.Body)
		}
	}
}
