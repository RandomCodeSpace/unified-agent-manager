package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestTokenUsagePeriodsAndPersistence(t *testing.T) {
	st := openTestStore(t)
	m := NewManager(st, nil)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	m.now = func() time.Time { return now }
	if err := m.loadTokenLedger(); err != nil {
		t.Fatal(err)
	}
	for _, days := range []int{0, 1, 6, 7, 29, 30} {
		m.recordTokensLocked("copilot", agentapi.TokenUsage{Model: "model-a", Time: now.AddDate(0, 0, -days), Input: 100, Output: 20, CacheRead: 70, CacheWrite: 10})
	}
	m.recordTokensLocked("other", agentapi.TokenUsage{Model: "model-a", Time: now, Input: 10, Output: 2})
	m.recordTokensLocked("copilot", agentapi.TokenUsage{Model: "model-b", Time: now, Input: 20, Output: 3})
	got := m.TokenUsage()
	for period, calls := range map[string]int64{"today": 1, "7d": 3, "30d": 5, "lifetime": 6} {
		p := got.Periods[period]
		want := TokenCounts{Input: calls*100 + 30, Output: calls*20 + 5, CacheRead: calls * 70, CacheWrite: calls * 10, Total: calls*120 + 35}
		if p.Total != want || len(p.Models) != 3 || p.Models[0].Provider != "copilot" || p.Models[0].Model != "model-a" {
			t.Fatalf("%s = %+v, want %+v with 3 separate models", period, p, want)
		}
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(m.tokenLedgerPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("ledger permissions: %v, %v", info, err)
	}
	again := NewManager(st, nil)
	again.now = m.now
	if err := again.loadTokenLedger(); err != nil {
		t.Fatal(err)
	}
	if actual := again.TokenUsage(); !actual.Since.Equal(got.Since) || !reflect.DeepEqual(actual.Periods, got.Periods) {
		t.Fatalf("restart: %+v != %+v", actual, got)
	}
}

func TestTokenUsageSinkAndUtility(t *testing.T) {
	m, prov, _ := newTestManager(t)
	_, conv := createSession(t, m, prov)
	conv.Emit(agentapi.Event{Kind: agentapi.EventTokens, Tokens: &agentapi.TokenUsage{Model: "actual-model", Input: 100, Output: 20, CacheRead: 60, CacheWrite: 10}})
	_, err := m.runUtility(context.Background(), UtilityCall{Provider: "fake", Model: "auto"}, "test", func(_ context.Context, report func(agentapi.UtilityUsage)) (string, error) {
		report(agentapi.UtilityUsage{InputTokens: 50, OutputTokens: 3, Tokens: []agentapi.TokenUsage{{Model: "actual-model", Input: 50, Output: 3, CacheRead: 30}}})
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Estimates from a provider with no usage report never enter these totals.
	_, _ = m.runUtility(context.Background(), UtilityCall{Provider: "fake", Model: "auto"}, "test", func(context.Context, func(agentapi.UtilityUsage)) (string, error) { return "ok", nil })
	p := m.TokenUsage().Periods["today"]
	if len(p.Models) != 1 || p.Models[0].Model != "actual-model" || p.Total != (TokenCounts{Input: 150, Output: 23, CacheRead: 90, CacheWrite: 10, Total: 173}) {
		t.Fatalf("usage: %+v", p)
	}
}

func TestTokenUsageWriteFailureAndInvalidLedger(t *testing.T) {
	st := openTestStore(t)
	m := NewManager(st, nil)
	if err := m.loadTokenLedger(); err != nil {
		t.Fatal(err)
	}
	path := m.tokenLedgerPath()
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	m.recordTokensLocked("p", agentapi.TokenUsage{Model: "m", Input: 1})
	if err := m.flush(); err == nil {
		t.Fatal("write failure hidden")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	again := NewManager(st, nil)
	if err := again.loadTokenLedger(); err != nil {
		t.Fatal(err)
	}
	if again.TokenUsage().Periods["lifetime"].Total.Input != 1 {
		t.Fatal("failed write lost tokens")
	}
	if err := os.WriteFile(filepath.Clean(path), []byte("{bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := NewManager(st, nil).loadTokenLedger(); err == nil {
		t.Fatal("invalid ledger accepted")
	}
}

func TestTokenUsageAPI(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	w := ts.do(http.MethodGet, "/api/usage/tokens", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", w.Code)
	}
	w = ts.do(http.MethodGet, "/api/usage/tokens", "", ts.login(t, "127.0.0.1:8260"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var report TokenUsageReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Periods) != 4 || report.Since.IsZero() || report.Periods["today"].Models == nil {
		t.Fatalf("empty report: %+v", report)
	}
}

func TestTokenUsageKeepsExactModelIDs(t *testing.T) {
	m, _, _ := newTestManager(t)
	prefix := strings.Repeat("x", 130)
	m.mu.Lock()
	m.recordTokensLocked("fake", agentapi.TokenUsage{Model: prefix + "-one", Input: 10})
	m.recordTokensLocked("fake", agentapi.TokenUsage{Model: prefix + "-two", Input: 20})
	m.mu.Unlock()
	models := m.TokenUsage().Periods["today"].Models
	if len(models) != 2 || models[0].Model != prefix+"-two" || models[1].Model != prefix+"-one" {
		t.Fatalf("model IDs were truncated or merged: %+v", models)
	}
}
