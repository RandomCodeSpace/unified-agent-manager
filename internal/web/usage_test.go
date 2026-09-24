package web

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

var usageT0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// startUsageManager starts a manager whose provider has the usage
// capability, with the clock at usageT0 and the usage loop's timer out of
// the way, and waits for the start-up quota read.
func startUsageManager(t *testing.T, quotas []agentapi.Quota, err error) (*Manager, *agenttest.Provider) {
	t.Helper()
	caps := allCaps
	caps.Usage = true
	prov := agenttest.NewProvider("fake", caps)
	prov.SetQuota(quotas, err)
	m := NewManager(openTestStore(t), []agentapi.Provider{prov})
	m.now = func() time.Time { return usageT0 }
	m.quotaTick = time.Hour
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	waitUntil(t, "the start-up quota read", func() bool {
		u := m.AccountUsage()
		return !u.UpdatedAt.IsZero() || u.Stale
	})
	return m, prov
}

func premium(used int64) agentapi.Quota {
	return agentapi.Quota{Type: "premium_interactions", Used: used, Entitlement: 1500, RemainingPercent: float64(1500-used) / 15, ResetAt: usageT0.Add(24 * time.Hour)}
}

func noFrame(t *testing.T, sub *Subscriber, what string) {
	t.Helper()
	select {
	case raw := <-sub.Frames():
		t.Fatalf("%s sent a frame: %s", what, raw)
	default:
	}
}

func usageJSON(t *testing.T, u AccountUsage) string {
	t.Helper()
	data, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAccountUsageCleanedCachedAndInSnapshot(t *testing.T) {
	m, prov := startUsageManager(t, []agentapi.Quota{
		premium(180),
		{Type: "chat", Unlimited: true, Entitlement: -1, RemainingPercent: 100, ResetAt: usageT0.Add(-10 * time.Minute)},
		{Type: " odd\x1b[31m ", Used: -3, RemainingPercent: math.NaN(), Overage: math.Inf(1)},
		{Type: "wide", RemainingPercent: 250, Overage: -2},
		{Type: " "},
	}, nil)
	// The past reset time is dropped; the type is sanitized; counts and
	// percentages are kept within range; an entry without a type is dropped.
	want := `{"quotas":[` +
		`{"provider":"fake","type":"chat","used":0,"entitlement":0,"unlimited":true,"remaining_percent":100,"overage":0},` +
		`{"provider":"fake","type":"odd","used":0,"entitlement":0,"unlimited":false,"remaining_percent":0,"overage":0},` +
		`{"provider":"fake","type":"premium_interactions","used":180,"entitlement":1500,"unlimited":false,"remaining_percent":88,"overage":0,"reset_at":"2026-09-25T12:00:00Z"},` +
		`{"provider":"fake","type":"wide","used":0,"entitlement":0,"unlimited":false,"remaining_percent":100,"overage":0}],` +
		`"stale":false,"updated_at":"2026-09-24T12:00:00Z"}`
	if got := usageJSON(t, m.AccountUsage()); got != want {
		t.Fatalf("usage =\n%s\nwant\n%s", got, want)
	}
	// Reading the cache never calls the provider.
	for range 3 {
		m.AccountUsage()
	}
	_, snap, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(parseFrame(t, snap).data["usage"]); got != want {
		t.Fatalf("snapshot usage = %s", got)
	}
	if prov.QuotaCalls() != 1 {
		t.Fatalf("quota calls = %d, want the start-up read only", prov.QuotaCalls())
	}
	// Once the reset time passes it is no longer shown.
	setNow(m, usageT0.Add(25*time.Hour))
	if strings.Contains(usageJSON(t, m.AccountUsage()), "reset_at") {
		t.Fatalf("a past reset time is shown: %s", usageJSON(t, m.AccountUsage()))
	}
}

func TestAccountUsageRefreshCadence(t *testing.T) {
	m, prov := startUsageManager(t, []agentapi.Quota{premium(180)}, nil)
	calls := func(want int, what string) {
		t.Helper()
		if got := prov.QuotaCalls(); got != want {
			t.Fatalf("%s: quota calls = %d, want %d", what, got, want)
		}
	}
	calls(1, "start")

	// Nobody is watching: no periodic read.
	setNow(m, usageT0.Add(10*time.Minute))
	m.pollQuota()
	calls(1, "no browser")

	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	m.pollQuota()
	calls(2, "browser connected, last read ten minutes ago")
	noFrame(t, sub, "a read that changed nothing")
	setNow(m, usageT0.Add(10*time.Minute+59*time.Second))
	m.pollQuota()
	calls(2, "within the minute")

	prov.SetQuota([]agentapi.Quota{premium(181)}, nil)
	setNow(m, usageT0.Add(11*time.Minute))
	m.pollQuota()
	calls(3, "a minute later")
	var u AccountUsage
	decodeField(t, frameOf(t, sub, "usage"), "usage", &u)
	if len(u.Quotas) != 1 || u.Quotas[0].Used != 181 || u.Stale || !u.UpdatedAt.Equal(usageT0.Add(11*time.Minute)) {
		t.Fatalf("usage frame = %+v", u)
	}

	// A turn that ends asks for a read at once; one that starts does not.
	sum, conv := createSession(t, m, prov)
	conv.EmitTurn(agentapi.TurnWorking, "")
	calls(3, "turn started")
	prov.SetQuota([]agentapi.Quota{premium(182)}, nil)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "the read after the turn", func() bool { return prov.QuotaCalls() == 4 })
	waitUntil(t, "the new quota", func() bool { return m.AccountUsage().Quotas[0].Used == 182 })
	conv.EmitTurn(agentapi.TurnFailed, "boom")
	waitUntil(t, "the read after a failed turn", func() bool { return prov.QuotaCalls() == 5 })
	if _, err := m.Detail(sum.ID); err != nil {
		t.Fatal(err)
	}

	m.Unsubscribe(sub)
	setNow(m, usageT0.Add(time.Hour))
	m.pollQuota()
	calls(5, "browser gone")
}

func TestAccountUsageFailedReadKeepsLastValueStale(t *testing.T) {
	m, prov := startUsageManager(t, []agentapi.Quota{premium(180)}, nil)
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	prov.SetQuota(nil, errors.New("not signed in"))
	setNow(m, usageT0.Add(time.Minute))
	m.pollQuota()
	var u AccountUsage
	decodeField(t, frameOf(t, sub, "usage"), "usage", &u)
	if !u.Stale || len(u.Quotas) != 1 || u.Quotas[0].Used != 180 || !u.UpdatedAt.Equal(usageT0) {
		t.Fatalf("usage after a failed read = %+v", u)
	}
	// Failing again changes nothing a browser sees.
	setNow(m, usageT0.Add(2*time.Minute))
	m.pollQuota()
	noFrame(t, sub, "a second failed read")
	if got := m.AccountUsage(); !got.Stale || got.Quotas[0].Used != 180 {
		t.Fatalf("usage = %+v", got)
	}
	prov.SetQuota([]agentapi.Quota{premium(180)}, nil)
	setNow(m, usageT0.Add(3*time.Minute))
	m.pollQuota()
	decodeField(t, frameOf(t, sub, "usage"), "usage", &u)
	if u.Stale || !u.UpdatedAt.Equal(usageT0.Add(3*time.Minute)) {
		t.Fatalf("usage after recovery = %+v", u)
	}
}

func TestAccountUsageFirstReadFailing(t *testing.T) {
	m, _ := startUsageManager(t, nil, errors.New("offline"))
	if got := usageJSON(t, m.AccountUsage()); got != `{"quotas":[],"stale":true}` {
		t.Fatalf("usage = %s", got)
	}
}

func TestAccountUsageWithoutTheCapability(t *testing.T) {
	m, prov, _ := newTestManager(t)
	prov.SetQuota([]agentapi.Quota{premium(1)}, nil)
	if _, _, err := m.Subscribe(""); err != nil {
		t.Fatal(err)
	}
	m.refreshQuota()
	if got := usageJSON(t, m.AccountUsage()); got != `{"quotas":[],"stale":false}` || prov.QuotaCalls() != 0 {
		t.Fatalf("usage = %s after %d calls", got, prov.QuotaCalls())
	}
}

func TestUsageRoute(t *testing.T) {
	m, prov := startUsageManager(t, []agentapi.Quota{premium(180)}, nil)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ts := &testServer{srv: srv, m: m, prov: prov}
	if w := ts.do(http.MethodGet, "/api/usage", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET without sign-in = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/usage", "", withCookie(ts), withHost("evil.example")); w.Code/100 != 4 {
		t.Fatalf("GET from a foreign host = %d", w.Code)
	}
	w := ts.do(http.MethodGet, "/api/usage", "", withCookie(ts))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != usageJSON(t, m.AccountUsage()) {
		t.Fatalf("GET /api/usage = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/usage", "{}", withCookie(ts)); w.Code != http.StatusMethodNotAllowed && w.Code != http.StatusNotFound {
		t.Fatalf("POST /api/usage = %d", w.Code)
	}
	if prov.QuotaCalls() != 1 {
		t.Fatalf("the route read the provider: %d calls", prov.QuotaCalls())
	}
}

func TestTaskUsageIsTheConversationTotal(t *testing.T) {
	m, prov := startUsageManager(t, nil, nil)
	sum, conv := createSession(t, m, prov)
	if sum.Usage != nil {
		t.Fatalf("usage before any report = %+v", sum.Usage)
	}
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	report := func(units float64) {
		conv.Emit(agentapi.Event{Kind: agentapi.EventUsage, Usage: &agentapi.Usage{AIUnits: units}})
	}
	report(1.68425)
	if got := nextSession(t, sub); got.Usage == nil || got.Usage.AIUnits != 1.68425 {
		t.Fatalf("session frame usage = %+v", got.Usage)
	}
	// Each report is the total so far: it replaces, never adds.
	report(1.849635)
	report(1.849635)
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		report(bad)
	}
	if got := detail(t, m, sum.ID).Usage; got == nil || got.AIUnits != 1.849635 {
		t.Fatalf("detail usage = %+v", got)
	}
	if data, _ := json.Marshal(detail(t, m, sum.ID)); !strings.Contains(string(data), `"usage":{"ai_units":1.849635}`) {
		t.Fatalf("detail JSON = %s", data)
	}

	// Reopening takes the total the provider recorded.
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	prov.SetHistoryUsage(sum.ConversationID, agentapi.Usage{AIUnits: 4.5})
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "again", RequestID: mustUUID(t)}); err != nil {
		t.Fatal(err)
	}
	if got := detail(t, m, sum.ID).Usage; got == nil || got.AIUnits != 4.5 {
		t.Fatalf("usage after reopening = %+v", got)
	}
}

func TestTaskUsageNeedsTheCapability(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.Emit(agentapi.Event{Kind: agentapi.EventUsage, Usage: &agentapi.Usage{AIUnits: 2}})
	if got := detail(t, m, sum.ID).Usage; got != nil {
		t.Fatalf("usage without the capability = %+v", got)
	}
}

func TestMetaModelCostTierAndDiscount(t *testing.T) {
	caps := allCaps
	caps.Usage = true
	prov := agenttest.NewProvider("fake", caps)
	prov.SetModels([]agentapi.Model{
		{ID: "auto", Name: "Auto", DiscountPercent: 10},
		{ID: "cheap", Name: "Cheap", CostTier: agentapi.CostLow},
		{ID: "odd", Name: "Odd", CostTier: "priceless", DiscountPercent: 400},
	}, nil)
	m := startManager(t, openTestStore(t), prov)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ts := &testServer{srv: srv, m: m, prov: prov}
	w := ts.do(http.MethodGet, "/api/meta", "", withCookie(ts))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/meta = %d %s", w.Code, w.Body)
	}
	var meta struct {
		Providers []struct {
			Capabilities map[string]bool   `json:"capabilities"`
			Models       []json.RawMessage `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	p := meta.Providers[0]
	if !p.Capabilities["usage"] || len(p.Models) != 3 {
		t.Fatalf("provider = %s", w.Body)
	}
	for i, want := range []string{`"discount_percent":10`, `"cost_tier":"low"`, ``} {
		got := string(p.Models[i])
		if !strings.Contains(got, want) || (want == "" && (strings.Contains(got, "cost_tier") || strings.Contains(got, "discount_percent"))) {
			t.Fatalf("model %d = %s, want %s", i, got, want)
		}
	}
}

func TestMetaModelPrices(t *testing.T) {
	price := func(v float64) *float64 { return &v }
	prov := agenttest.NewProvider("fake", allCaps)
	prov.SetModels([]agentapi.Model{
		{ID: "priced", Name: "Priced", Prices: &agentapi.Prices{BatchSize: 1_000_000,
			TierPrices:  agentapi.TierPrices{Input: price(200), Output: price(1000), CacheRead: price(20), CacheWrite: price(0), MaxPromptTokens: 200_000},
			LongContext: &agentapi.TierPrices{Input: price(400), Output: price(math.Inf(1)), CacheRead: price(-1), MaxPromptTokens: 936_000}}},
		{ID: "broken", Name: "Broken", Prices: &agentapi.Prices{BatchSize: -5, TierPrices: agentapi.TierPrices{Input: price(math.NaN())}, LongContext: &agentapi.TierPrices{}}},
		{ID: "plain", Name: "Plain"},
	}, nil)
	m := startManager(t, openTestStore(t), prov)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ts := &testServer{srv: srv, m: m, prov: prov}
	w := ts.do(http.MethodGet, "/api/meta", "", withCookie(ts))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/meta = %d %s", w.Code, w.Body)
	}
	var meta struct {
		Providers []struct {
			Models []json.RawMessage `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	// Negative and non-finite prices are dropped; nothing left means no prices.
	want := []string{
		`"prices":{"batch_size":1000000,"input":200,"output":1000,"cache_read":20,"cache_write":0,"max_prompt_tokens":200000,"long_context":{"input":400,"max_prompt_tokens":936000}}`,
		``, ``,
	}
	for i, got := range meta.Providers[0].Models {
		if !strings.Contains(string(got), want[i]) || (want[i] == "" && strings.Contains(string(got), "prices")) {
			t.Fatalf("model %d = %s, want %s", i, got, want[i])
		}
	}
}

func TestTaskContextCachedShare(t *testing.T) {
	m, p, _ := newTestManager(t)
	sum, c := createSession(t, m, p)
	c.Emit(agentapi.Event{Kind: agentapi.EventContext, Context: &agentapi.Context{Used: 11084, Limit: 128000, Prompt: 13385, Cached: 13326}})
	if got := detail(t, m, sum.ID).Context; got == nil || *got != (agentapi.Context{Used: 11084, Limit: 128000, Prompt: 13385, Cached: 13326}) {
		t.Fatalf("context = %+v", got)
	}
	if data, _ := json.Marshal(detail(t, m, sum.ID)); !strings.Contains(string(data), `"context":{"used":11084,"limit":128000,"prompt":13385,"cached":13326}`) {
		t.Fatalf("detail JSON = %s", data)
	}
	// An impossible cache report keeps the usage and drops the cache figures.
	for _, bad := range []agentapi.Context{{Used: 5, Limit: 10, Prompt: 3, Cached: 4}, {Used: 5, Limit: 10, Prompt: 3, Cached: -1}} {
		c.Emit(agentapi.Event{Kind: agentapi.EventContext, Context: &bad})
		if got := detail(t, m, sum.ID).Context; got == nil || *got != (agentapi.Context{Used: 5, Limit: 10}) {
			t.Fatalf("context after %+v = %+v", bad, got)
		}
	}
}
