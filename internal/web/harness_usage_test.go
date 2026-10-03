package web

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/RandomCodeSpace/aiusage-core/adapter"
	"github.com/RandomCodeSpace/aiusage-core/adapter/claudecode"
	"github.com/RandomCodeSpace/aiusage-core/adapter/codex"
	"github.com/RandomCodeSpace/aiusage-core/collect"
	coremodel "github.com/RandomCodeSpace/aiusage-core/model"
	corestore "github.com/RandomCodeSpace/aiusage-core/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func harnessTestManager(t *testing.T, now time.Time) (*Manager, *corestore.Ledger) {
	t.Helper()
	m := NewManager(openTestStore(t), nil)
	m.now = func() time.Time { return now }
	m.usageHome = t.TempDir()
	if err := m.loadTokenLedger(); err != nil {
		t.Fatal(err)
	}
	ledger, err := m.prepareHarnessUsage()
	if err != nil || ledger == nil {
		t.Fatalf("prepare: %v", err)
	}
	t.Cleanup(func() { _ = ledger.Close(); m.cancel() })
	return m, ledger
}

func harnessTestEvent(tool, session, key string, at time.Time, input, output int64) coremodel.UsageEvent {
	return coremodel.UsageEvent{Tool: tool, Model: "test-model", SessionID: session, DedupKey: key,
		EventTime: at, ObservedTime: at, InputTokens: input, OutputTokens: output, TotalTokens: input + output, Kind: coremodel.KindUsage}
}

func TestHarnessUsageOwnershipPersistenceAndPrivacy(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	m, ledger := harnessTestManager(t, now)
	m.recordTokensLocked("copilot", agentapi.TokenUsage{Model: "test-model", Time: now, Input: 100, Output: 20})
	if err := m.recordCopilotUsageSession("uam-session", true); err != nil {
		t.Fatal(err)
	}
	// Before adoption, this was an external session: that history must survive.
	m.now = func() time.Time { return now.Add(time.Minute) }
	if err := m.recordCopilotUsageSession("adopted-session", true); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return now.Add(2 * time.Minute) }
	events := []coremodel.UsageEvent{
		harnessTestEvent("copilot", "uam-session", "owned", now.Add(time.Second), 100, 20),
		harnessTestEvent("copilot", "old-session", "legacy", now.Add(-time.Second), 100, 20),
		harnessTestEvent("copilot", "external-session", "external", now.Add(time.Second), 10, 2),
		harnessTestEvent("copilot", "adopted-session", "before-adoption", now.Add(30*time.Second), 10, 2),
		harnessTestEvent("copilot", "adopted-session", "after-adoption", now.Add(90*time.Second), 100, 20),
		harnessTestEvent("copilot", "unknown-session", "ambiguous", now.Add(time.Second), 100, 20),
	}
	events[2].Raw = `{"prompt":"PRIVATE-CANARY"}`
	usageStore := usageOnlyStore{Ledger: ledger, m: m}
	for range 2 {
		_, err := usageStore.ApplyBatch(context.Background(), corestore.ObservationBatch{
			Events:       slices.Clone(events),
			Activity:     []coremodel.ActivityEvent{{Tool: "copilot", Name: "PRIVATE-CANARY"}},
			TurnContexts: []coremodel.TurnContext{{}}, CodeChanges: []coremodel.CodeChange{{}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := m.refreshHarnessUsage(context.Background(), ledger.Reader, false); err != nil {
		t.Fatal(err)
	}
	got := m.TokenUsage()
	if got.Periods["today"].Total.Total != 144 || got.Collection.Status != "partial" {
		t.Fatalf("SDK + external only: %+v", got)
	}
	rows, err := ledger.ListEvents(context.Background(), corestore.Filter{}, corestore.WithRaw())
	if err != nil || len(rows) != 2 {
		t.Fatalf("stored rows: %d, %v", len(rows), err)
	}
	for _, row := range rows {
		if row.Raw != "" {
			t.Fatal("usage stored raw content")
		}
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	again := NewManager(m.store, nil)
	again.now = m.now
	again.usageHome = m.usageHome
	if err := again.loadTokenLedger(); err != nil {
		t.Fatal(err)
	}
	if len(again.tokens.CopilotSessions) != 2 || !again.tokens.CopilotSince.Equal(now) {
		t.Fatal("ownership or cutoff lost")
	}
	next, err := again.prepareHarnessUsage()
	if err != nil || next == nil {
		t.Fatalf("reopen: %v", err)
	}
	defer next.Close()
	if err := again.refreshHarnessUsage(context.Background(), next.Reader, false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Periods, again.TokenUsage().Periods) {
		t.Fatal("restart changed counts")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(filepath.Dir(m.store.Path()), "web-token-usage.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"activity_events", "usage_turn_context", "code_changes"} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d, %v", table, n, err)
		}
	}
}

func TestHarnessUsageCostsAndCache(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	m, ledger := harnessTestManager(t, now)
	claude := harnessTestEvent("claude-code", "s", "claude", now, 400_000, 100_000)
	claude.CacheReadTokens, claude.CacheCreationTokens, claude.TotalTokens = 500_000, 100_000, 1_100_000
	open := harnessTestEvent("opencode", "s", "open", now, 10, 2)
	open.ReasoningTokens, open.TotalTokens = 3, 15
	crush := harnessTestEvent("crush", "s", "cost-only", now, 0, 0)
	crush.Model, crush.CostMicroUSD = "", new(int64(500_000))
	if _, err := (usageOnlyStore{Ledger: ledger, m: m}).ApplyBatch(context.Background(), corestore.ObservationBatch{Events: []coremodel.UsageEvent{claude, open, crush}}); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshHarnessUsage(context.Background(), ledger.Reader, false); err != nil {
		t.Fatal(err)
	}
	m.settings.TokenPrices = map[string]map[string]store.WebTokenPrice{"claude-code": {"test-model": {Input: new(2.0), Output: new(10.0), CacheRead: new(0.0)}}}
	p := m.TokenUsage().Periods["today"]
	if p.Total.Total != 1_100_015 || p.Total.Input != 1_000_010 || p.Total.Output != 100_005 || p.CostUSD == nil || *p.CostUSD != 2.5 || p.UnpricedModels != 1 {
		t.Fatalf("normalized costs: %+v", p)
	}
	if p.Models[0].CostUSD == nil || *p.Models[0].CostUSD != 2 {
		t.Fatalf("free cache: %+v", p.Models[0])
	}
	delete(m.settings.TokenPrices, "claude-code")
	p = m.TokenUsage().Periods["today"]
	if p.CostUSD == nil || *p.CostUSD != 0.5 || p.UnpricedModels != 2 {
		t.Fatalf("removed override and retained source cost: %+v", p)
	}
}

func TestHarnessUsageTerminalHandoff(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	m, ledger := harnessTestManager(t, now)
	for _, step := range []struct {
		minute int
		active bool
	}{{0, true}, {2, false}, {4, true}, {6, false}} {
		m.now = func() time.Time { return now.Add(time.Duration(step.minute) * time.Minute) }
		if err := m.recordCopilotUsageSession("shared-session", step.active); err != nil {
			t.Fatal(err)
		}
	}
	// Parent and subagent SDK usage is already included in the daily ledger.
	m.recordTokensLocked("copilot", agentapi.TokenUsage{Model: "test-model", Input: 20})
	var events []coremodel.UsageEvent
	for _, minute := range []int{1, 3, 5, 7} {
		events = append(events, harnessTestEvent("copilot", "shared-session", fmt.Sprint(minute), now.Add(time.Duration(minute)*time.Minute), 10, 0))
	}
	for range 2 {
		if _, err := (usageOnlyStore{Ledger: ledger, m: m}).ApplyBatch(context.Background(), corestore.ObservationBatch{Events: slices.Clone(events)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.refreshHarnessUsage(context.Background(), ledger.Reader, false); err != nil {
		t.Fatal(err)
	}
	if got := m.TokenUsage(); got.Periods["today"].Total.Total != 40 || got.Collection.Status != "ready" {
		t.Fatalf("terminal handoff: %+v", got)
	}
	again := NewManager(m.store, nil)
	if err := again.loadTokenLedger(); err != nil {
		t.Fatal(err)
	}
	if len(again.tokens.CopilotSessions["shared-session"]) != 2 || again.copilotOwnedAtLocked("shared-session", now.Add(7*time.Minute)) || !again.copilotOwnedAtLocked("shared-session", now.Add(time.Minute)) {
		t.Fatal("ownership intervals did not survive restart")
	}
}

func TestHarnessUsageFailedOwnershipDoesNotHideTerminalUsage(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	m, ledger := harnessTestManager(t, now)
	path := m.tokenLedgerPath()
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.recordCopilotUsageSession("failed-session", true); err == nil {
		t.Fatal("ownership accepted failed persistence")
	}
	if m.copilotOwnedAtLocked("failed-session", now.Add(time.Second)) {
		t.Fatal("failed start retained ownership")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".saved", path); err != nil {
		t.Fatal(err)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := (usageOnlyStore{Ledger: ledger, m: m}).ApplyBatch(context.Background(), corestore.ObservationBatch{Events: []coremodel.UsageEvent{harnessTestEvent("copilot", "failed-session", "terminal", now.Add(time.Second), 10, 0)}}); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshHarnessUsage(context.Background(), ledger.Reader, false); err != nil {
		t.Fatal(err)
	}
	if got := m.TokenUsage().Periods["today"].Total.Total; got != 10 {
		t.Fatalf("terminal tokens after failed ownership: %d", got)
	}
	again := NewManager(m.store, nil)
	if err := again.loadTokenLedger(); err != nil {
		t.Fatal(err)
	}
	if len(again.tokens.CopilotSessions) != 0 {
		t.Fatal("failed ownership persisted after recovery")
	}
}

// Discovery is pinned to fixture sources so tests never consult real HOME or
// configured harness roots. Parsing/checkpoints remain the library's code.
type fixtureHarness struct {
	adapter.Adapter
	source adapter.Source
}

func (f fixtureHarness) Discover(context.Context, adapter.DiscoverConfig) ([]adapter.Source, error) {
	return []adapter.Source{f.source}, nil
}

func TestHarnessUsageCollectsMultipleHarnesses(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	m, ledger := harnessTestManager(t, now)
	var fixtures []adapter.Adapter
	for _, f := range []struct {
		a    adapter.Adapter
		text string
	}{
		{codex.New(), `{"type":"turn_context","payload":{"model":"shared-model"}}` + "\n" + fmt.Sprintf(`{"type":"event_msg","timestamp":%q,"payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":100,"total_tokens":1100}}}}`, now.Format(time.RFC3339))},
		{claudecode.New(), fmt.Sprintf(`{"timestamp":%q,"sessionId":"s","requestId":"r1","message":{"id":"m1","model":"shared-model","usage":{"input_tokens":400,"output_tokens":100,"cache_read_input_tokens":600}}}`, now.Format(time.RFC3339))},
	} {
		root := t.TempDir()
		path := filepath.Join(root, "fixture.jsonl")
		if f.a.ID() == "claude-code" {
			if err := os.Mkdir(filepath.Join(root, "projects"), 0700); err != nil {
				t.Fatal(err)
			}
			path = filepath.Join(root, "projects", "fixture.jsonl")
		}
		if err := os.WriteFile(path, []byte(f.text+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		sourcePath := path
		if f.a.ID() == "claude-code" {
			sourcePath = root
		}
		fixtures = append(fixtures, fixtureHarness{f.a, adapter.Source{Tool: f.a.ID(), Class: coremodel.EventLevel, Path: sourcePath}})
	}
	for range 2 {
		stats, err := collect.RunOnce(context.Background(), adapter.NewRegistry(fixtures...), usageOnlyStore{Ledger: ledger, m: m}, adapter.DiscoverConfig{}, collect.WithoutRaw())
		if err != nil || len(stats.Errors) != 0 {
			t.Fatalf("collect: %+v, %v", stats, err)
		}
	}
	if err := m.refreshHarnessUsage(context.Background(), ledger.Reader, false); err != nil {
		t.Fatal(err)
	}
	p := m.TokenUsage().Periods["today"]
	if len(p.Models) != 2 || p.Total.Total != 2200 || p.Total.Input != 2000 || p.Total.CacheRead != 1200 {
		t.Fatalf("parsed and deduplicated: %+v", p)
	}
}

func TestHarnessUsageCalendarWindows(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, loc) // 25-hour DST fallback day.
	m, ledger := harnessTestManager(t, now)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	var events []coremodel.UsageEvent
	for i, at := range []time.Time{start.Add(-time.Nanosecond), start, start.Add(24 * time.Hour), start.AddDate(0, 0, 1), start.AddDate(0, 0, -6), start.AddDate(0, 0, -7), start.AddDate(0, 0, -29), start.AddDate(0, 0, -30)} {
		events = append(events, harnessTestEvent("codex", "s", fmt.Sprint(i), at, 1, 0))
	}
	if _, err := ledger.InsertEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshHarnessUsage(context.Background(), ledger.Reader, false); err != nil {
		t.Fatal(err)
	}
	for period, want := range map[string]int64{"today": 2, "7d": 4, "30d": 6, "lifetime": 7} {
		if got := m.TokenUsage().Periods[period].Total.Total; got != want {
			t.Fatalf("%s: %d != %d", period, got, want)
		}
	}
	m.now = func() time.Time { return now.AddDate(0, 0, 1) }
	if got := m.TokenUsage(); got.Collection.Status != "partial" || got.Periods["today"].Total.Total != 0 {
		t.Fatal("stale yesterday cache claimed to be today's data")
	}
}
