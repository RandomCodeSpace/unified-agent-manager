package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func historyPageFixture(n, size int) []agentapi.Item {
	items := make([]agentapi.Item, n)
	for i := range items {
		items[i] = agentapi.Item{ID: fmt.Sprintf("item-%04d", i), Kind: agentapi.ItemAssistant, Text: strings.Repeat("x", size)}
	}
	return items
}

func TestHistoryPagesStayBoundedAndWalkEveryItem(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	items := historyPageFixture(240, 1700)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.applyHistoryLocked(s, agentapi.History{Items: items}, false)
	m.mu.Unlock()
	_, raw, err := m.subscribeHistory(sum.ID, true, true)
	if err != nil {
		t.Fatal(err)
	}
	var first SessionDetail
	if err := json.Unmarshal(parseFrame(t, raw).data["session"], &first); err != nil {
		t.Fatal(err)
	}
	if first.HistoryBefore == nil || *first.HistoryBefore == "" || len(first.Items) >= len(items) || len(raw) > 80<<10 {
		t.Fatalf("recent snapshot: %d items, %d bytes, cursor %v", len(first.Items), len(raw), first.HistoryBefore)
	}
	// Appending between requests must not shift any older page's boundary.
	m.mu.Lock()
	m.upsertItemLocked(s, agentapi.Item{ID: "new", Kind: agentapi.ItemAssistant}, true)
	m.mu.Unlock()
	got := first.Items
	before := *first.HistoryBefore
	for before != "" {
		page, err := m.OlderHistory(sum.ID, before)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(page.Items)
		if len(page.Items) == 0 || len(page.Items) > historyPageItems || len(encoded) > historyPageBytes+2 {
			t.Fatalf("unbounded/non-progressing page: %d items, %d bytes", len(page.Items), len(encoded))
		}
		got = append(page.Items, got...)
		before = page.Before
	}
	ids := func(items []agentapi.Item) []string {
		out := make([]string, len(items))
		for i, item := range items {
			out[i] = item.ID
		}
		return out
	}
	if !slices.Equal(ids(got), ids(items)) {
		t.Fatalf("paging lost, repeated or reordered items: got %d", len(got))
	}
}

func TestHistoryPageOversizedItemStillMakesProgress(t *testing.T) {
	items := historyPageFixture(3, historyPageBytes+1)
	page := historyPage(items, len(items))
	if len(page.Items) != 1 || page.Items[0].Text != items[2].Text || page.Before == "" {
		t.Fatalf("oversized item lost: %+v", page)
	}
	if len(historyPage(nil, 0).Items) != 0 {
		t.Fatal("empty history")
	}
}

func TestHistoryPageRoutesAndLegacyCompatibility(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, _ := createSession(t, ts.m, ts.prov)
	ts.m.mu.Lock()
	ts.m.applyHistoryLocked(ts.m.sessions[sum.ID], agentapi.History{Items: historyPageFixture(120, 20)}, false)
	ts.m.mu.Unlock()
	path := "/api/sessions/" + sum.ID
	if w := ts.do(http.MethodGet, path+"/history?before=anything", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("auth = %d", w.Code)
	}
	for _, tc := range []struct {
		query string
		code  int
	}{{"", 400}, {"?before=%%%", 400}, {"?before=" + base64.RawURLEncoding.EncodeToString([]byte("gone")), 409}} {
		if w := ts.do(http.MethodGet, path+"/history"+tc.query, "", withCookie(ts)); w.Code != tc.code {
			t.Fatalf("cursor %q = %d, want %d", tc.query, w.Code, tc.code)
		}
	}
	for _, recent := range []bool{false, true} {
		query := ""
		want := 120
		if recent {
			query, want = "?history=recent", historyPageItems
		}
		w := ts.do(http.MethodGet, path+query, "", withCookie(ts))
		var d SessionDetail
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || len(d.Items) != want || (d.HistoryBefore != nil) != recent {
			t.Fatalf("detail recent=%v: %d items, status %d", recent, len(d.Items), w.Code)
		}
		if recent {
			w = ts.do(http.MethodGet, path+"/history?before="+*d.HistoryBefore, "", withCookie(ts))
			var page HistoryPage
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || len(page.Items) != historyPageItems || page.Items[0].ID != "item-0020" {
				t.Fatalf("older page = %d, %+v", w.Code, page)
			}
		}
	}
}

func TestHistoryFramesRespectRecentNegotiation(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	legacy, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	recent, _, err := m.subscribeHistory(sum.ID, true, true)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.applyHistoryLocked(s, agentapi.History{Items: historyPageFixture(120, 2000)}, false)
	m.publishHistoryLocked(s)
	m.mu.Unlock()
	for _, sub := range []*Subscriber{legacy, recent} {
		frame := frameOf(t, sub, "history")
		var items []agentapi.Item
		if err := json.Unmarshal(frame.data["items"], &items); err != nil {
			t.Fatal(err)
		}
		if sub == legacy && len(items) != 120 {
			t.Fatal("legacy transcript changed")
		}
		if sub == recent && (len(items) >= 50 || frame.data["history_before"] == nil) {
			t.Fatal("history load bypassed pagination")
		}
	}
	// A paged client must be able to ignore an update to an unfetched item,
	// while still appending genuinely new streamed items.
	for _, tc := range []struct {
		id     string
		append bool
	}{{"item-0000", false}, {"brand-new", true}} {
		m.mu.Lock()
		m.upsertItemLocked(s, agentapi.Item{ID: tc.id, Kind: agentapi.ItemAssistant, Text: "updated"}, true)
		m.mu.Unlock()
		frame := frameOf(t, recent, "item")
		var appended bool
		if raw := frame.data["append"]; raw != nil {
			if err := json.Unmarshal(raw, &appended); err != nil {
				t.Fatal(err)
			}
		}
		if appended != tc.append {
			t.Fatalf("item %s append = %v", tc.id, appended)
		}
	}
}
