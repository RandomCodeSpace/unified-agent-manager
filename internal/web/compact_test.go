package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func compactFrame(t *testing.T, part initialDetailFrame) frame {
	t.Helper()
	raw, err := encodeFrame(part.event, part.payload)
	if err != nil {
		t.Fatal(err)
	}
	return parseFrame(t, raw)
}
func TestCompactProjectionPreservesDisplayAndSemanticContent(t *testing.T) {
	path := strings.Repeat("nested/", 100) + "a.go"
	input, _ := json.Marshal(map[string]string{"path": path, "content": "hidden source"})
	it := agentapi.Item{ID: "edit", Kind: agentapi.ItemTool, Text: "hidden diagnostic", Tool: &agentapi.ToolCall{Name: "edit", Status: agentapi.ToolCompleted, Input: string(input), Output: "hidden result"}, Images: []agentapi.Image{{ID: "stored"}}}
	got := projectItem(it)
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "hidden") || got.Tool.Path != path || len(got.Tool.DisplayArg) > 512 || !got.Tool.HasInput || !got.Tool.HasOutput || !got.Compact.HasText || len(got.Images) != 1 {
		t.Fatalf("compact projection lost semantics or leaked bodies: %s", raw)
	}
	if it.Tool.Input != string(input) || it.Text != "hidden diagnostic" {
		t.Fatal("projection mutated retained body")
	}
	ask := projectItem(agentapi.Item{Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "ask_user", Input: `{"question":"Choose?","choices":["A","B"]}`, Output: "A", Status: agentapi.ToolCompleted}})
	if ask.Tool.Input == "" || ask.Tool.Output != "A" {
		t.Fatal("recorded question/answer lost")
	}
	missingTool := projectItem(agentapi.Item{Kind: agentapi.ItemTool, Text: "hidden diagnostic"})
	if missingTool.Text != "" || !missingTool.Compact.HasText {
		t.Fatal("tool text without metadata leaked")
	}
	reasoning := projectItem(agentapi.Item{Kind: agentapi.ItemReasoning, Text: "secret thought"})
	if reasoning.Text != "" || !reasoning.Compact.HasReasoning {
		t.Fatal("reasoning was not deferred")
	}
}

func TestCompactPagesAndLegacyOptIn(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, _ := createSession(t, ts.m, ts.prov)
	ts.m.mu.Lock()
	s := ts.m.sessions[sum.ID]
	for i := range 120 {
		ts.m.upsertItemLocked(s, agentapi.Item{ID: fmt.Sprintf("tool-%03d", i), Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Input: `{"command":"pwd"}`, Output: strings.Repeat("private-output", 5000), Status: agentapi.ToolCompleted}}, false)
	}
	ts.m.upsertSubagentLocked(s, agentapi.Subagent{ID: "helper", Name: "Helper", Status: agentapi.SubagentRunning}, false)
	for i := range 60 {
		ts.m.upsertItemLocked(s, agentapi.Item{ID: fmt.Sprintf("child-%03d", i), AgentID: "helper", Kind: agentapi.ItemAssistant, Text: "hi"}, false)
	}
	ts.m.mu.Unlock()
	path := "/api/sessions/" + sum.ID
	for _, q := range []string{"", "?view=unknown"} {
		w := ts.do("GET", path+q, "", withCookie(ts))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "private-output") || strings.Contains(w.Body.String(), `"representation"`) {
			t.Fatalf("legacy changed %d", w.Code)
		}
	}
	w := ts.do("GET", path+"?view=compact-v1", "", withCookie(ts))
	var d compactSessionDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || d.Epoch == "" || d.Representation != compactRepresentation || !d.DetailStream || len(d.Items) != 50 || strings.Contains(w.Body.String(), "private-output") {
		t.Fatalf("compact response %d %d items", w.Code, len(d.Items))
	}
	ids := map[string]bool{}
	for _, it := range d.Items {
		ids[it.ID] = true
	}
	before := *d.HistoryBefore
	for before != "" {
		w = ts.do("GET", path+"/history?view=compact-v1&before="+url.QueryEscape(before), "", withCookie(ts))
		var page compactHistoryPage
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		if w.Code != 200 || len(page.Items) == 0 || page.Epoch != d.Epoch || strings.Contains(w.Body.String(), "private-output") {
			t.Fatal("bad compact page")
		}
		for _, it := range page.Items {
			if ids[it.ID] {
				t.Fatal("duplicate compact item")
			}
			ids[it.ID] = true
		}
		before = page.Before
	}
	if len(ids) != 120 {
		t.Fatalf("lost compact items: %d", len(ids))
	}
	w = ts.do("GET", path+"/subagents/helper?view=compact-v1", "", withCookie(ts))
	var child compactSubagentDetail
	_ = json.Unmarshal(w.Body.Bytes(), &child)
	if len(child.Items) != 50 || child.Before == "" || child.Epoch != d.Epoch {
		t.Fatal("child recent page")
	}
	w = ts.do("GET", path+"/subagents/helper/history?view=compact-v1&before="+url.QueryEscape(child.Before), "", withCookie(ts))
	var older compactHistoryPage
	_ = json.Unmarshal(w.Body.Bytes(), &older)
	if w.Code != 200 || len(older.Items) != 10 || older.Before != "" {
		t.Fatal("child older page")
	}
	_, raw, err := ts.m.subscribeView(sum.ID, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	f := parseFrame(t, raw)
	var epoch, repr string
	decodeField(t, f, "epoch", &epoch)
	decodeField(t, f, "representation", &repr)
	if epoch != d.Epoch || repr != compactRepresentation {
		t.Fatal("snapshot capability missing")
	}
	_, empty, err := ts.m.subscribeView("", true, true, true)
	if err != nil || !strings.Contains(string(empty), `"detail_stream":true`) {
		t.Fatal("empty-task capability missing")
	}
}

func TestCompactDetailBodiesOrderingCompletionAndIdentity(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	started := time.Now().UTC()
	makeItem := func(agent, output string, status agentapi.ToolStatus) agentapi.Item {
		return agentapi.Item{ID: "same", AgentID: agent, Kind: agentapi.ItemTool, Time: started, Tool: &agentapi.ToolCall{Name: "bash", Input: "pwd", Output: output, Status: status}}
	}
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.upsertSubagentLocked(s, agentapi.Subagent{ID: "helper", Name: "Helper", Status: agentapi.SubagentRunning}, false)
	m.upsertItemLocked(s, makeItem("", "", agentapi.ToolRunning), false)
	m.upsertItemLocked(s, makeItem("helper", "child", agentapi.ToolCompleted), false)
	m.mu.Unlock()
	main, _, err := m.subscribeView(sum.ID, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	detail, initial, err := m.subscribeDetail(sum.ID, "helper", []bodyRef{{"", "same"}, {"helper", "same"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(initial) != 4 {
		t.Fatalf("initial resources %d", len(initial))
	}
	barrier := compactFrame(t, initial[0]).seq
	for _, part := range initial {
		if compactFrame(t, part).seq != barrier {
			t.Fatal("non-atomic initial resources")
		}
	}
	child := compactFrame(t, initial[2])
	var item agentapi.Item
	decodeField(t, child, "item", &item)
	if item.AgentID != "helper" || item.Tool.Output != "child" {
		t.Fatal("body identity collision")
	}
	// Hidden suffixes do not carry full bodies or output-length churn on main.
	m.mu.Lock()
	m.upsertItemLocked(s, makeItem("", "a", agentapi.ToolRunning), true)
	m.mu.Unlock()
	mutation := frameOf(t, main, "item")
	delta := frameOf(t, detail, "body_output")
	if mutation.seq >= delta.seq {
		t.Fatal("body preceded compact mutation")
	}
	m.mu.Lock()
	m.upsertItemLocked(s, makeItem("", "ab", agentapi.ToolRunning), true)
	m.mu.Unlock()
	frameOf(t, detail, "body_output")
	noFrame(t, main, "hidden output suffix")
	// A rewrite, final suffix, and correction after completion are authoritative.
	for _, tc := range []struct {
		output string
		status agentapi.ToolStatus
	}{{"rewrite", agentapi.ToolRunning}, {"rewrite FINAL", agentapi.ToolCompleted}, {"corrected", agentapi.ToolCompleted}} {
		m.mu.Lock()
		m.upsertItemLocked(s, makeItem("", tc.output, tc.status), true)
		m.mu.Unlock()
		summary := frameOf(t, main, "item")
		body := frameOf(t, detail, "body")
		decodeField(t, body, "item", &item)
		if summary.seq >= body.seq || item.Tool.Output != tc.output || item.Tool.Status != tc.status {
			t.Fatal("authoritative body lost/reordered")
		}
	}
	// Final-only output never depends on a prior delta.
	m.mu.Lock()
	m.upsertItemLocked(s, makeItem("helper", "only final", agentapi.ToolCompleted), true)
	m.mu.Unlock()
	frameOf(t, detail, "item")
	body := frameOf(t, detail, "body")
	decodeField(t, body, "item", &item)
	if item.Tool.Output != "only final" {
		t.Fatal("final-only body lost")
	}
	// The body copied for the initial barrier is immutable after replacements.
	original := compactFrame(t, initial[1])
	item = agentapi.Item{}
	decodeField(t, original, "item", &item)
	if item.Tool.Output != "" {
		t.Fatal("initial body mutated")
	}
	m.Unsubscribe(detail)
	select {
	case <-detail.Gone():
	default:
		t.Fatal("detail not cleaned")
	}
}

func TestCompactReasoningClosedAgentAndPreview(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.upsertSubagentLocked(s, agentapi.Subagent{ID: "helper", Name: "Helper", Status: agentapi.SubagentRunning}, false)
	m.upsertItemLocked(s, agentapi.Item{ID: "thought", Kind: agentapi.ItemReasoning, Text: "first"}, false)
	m.mu.Unlock()
	main, _, _ := m.subscribeView(sum.ID, true, true, true)
	detail, _, _ := m.subscribeDetail(sum.ID, "", []bodyRef{{"", "thought"}})
	m.mu.Lock()
	m.applyDeltaLocked(s, agentapi.Delta{ItemID: "thought", Kind: agentapi.ItemReasoning, Text: " second"})
	m.applyDeltaLocked(s, agentapi.Delta{ItemID: "chat", AgentID: "helper", Kind: agentapi.ItemAssistant, Text: strings.Repeat("界", 300)})
	m.mu.Unlock()
	delta := frameOf(t, detail, "body_delta")
	var text string
	decodeField(t, delta, "text", &text)
	if text != " second" {
		t.Fatal("live reasoning missing")
	}
	preview := frameOf(t, main, "subagent")
	var sa compactSubagent
	decodeField(t, preview, "subagent", &sa)
	if len(sa.Preview) > 512 || !utf8.ValidString(sa.Preview) || sa.Preview == "" {
		t.Fatal("preview bound")
	}
	noFrame(t, main, "closed subagent transcript/reasoning")
	m.mu.Lock()
	m.applyDeltaLocked(s, agentapi.Delta{ItemID: "chat", AgentID: "helper", Kind: agentapi.ItemAssistant, Text: "suffix"})
	m.upsertSubagentLocked(s, agentapi.Subagent{ID: "helper", Status: agentapi.SubagentCompleted}, true)
	m.mu.Unlock()
	final := frameOf(t, main, "subagent")
	decodeField(t, final, "subagent", &sa)
	if sa.Status != agentapi.SubagentCompleted {
		t.Fatal("status did not flush")
	}
	m.mu.Lock()
	if state := s.previews["helper"]; state.timer != nil {
		t.Fatal("final left preview timer")
	}
	m.publishHistoryLocked(s)
	m.mu.Unlock()
	frameOf(t, main, "history")
	frameOf(t, detail, "detail_reset")
}

func TestCompactReadAuthMissingAndInterestValidation(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, _ := createSession(t, ts.m, ts.prov)
	ts.m.mu.Lock()
	ts.m.upsertItemLocked(ts.m.sessions[sum.ID], agentapi.Item{ID: "body", Kind: agentapi.ItemAssistant, Text: "retained"}, false)
	ts.m.mu.Unlock()
	path := "/api/sessions/" + sum.ID + "/items/body"
	if w := ts.do("GET", path, " "); w.Code != 401 {
		t.Fatalf("body auth %d", w.Code)
	}
	if w := ts.do("GET", "/api/events/detail?session="+sum.ID+"&item=%5B%22%22,%22body%22%5D", ""); w.Code != 401 {
		t.Fatalf("stream auth %d", w.Code)
	}
	w := ts.do("GET", path, "", withCookie(ts))
	var body itemBody
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 200 || body.Epoch == "" || body.Item.Text != "retained" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("body response %d %+v", w.Code, body)
	}
	for _, suffix := range []string{"missing/items/body", sum.ID + "/items/missing", sum.ID + "/items/body?agent_id=wrong"} {
		if w := ts.do("GET", "/api/sessions/"+suffix, "", withCookie(ts)); w.Code != 404 {
			t.Fatalf("missing read %d", w.Code)
		}
	}
	valid := "?session=" + sum.ID + "&item=" + url.QueryEscape(`["","body"]`)
	for _, q := range []string{"?session=" + sum.ID, valid + "&item=" + url.QueryEscape(`["","body"]`), "?session=" + sum.ID + "&item=bad", valid + strings.Repeat("&item="+url.QueryEscape(`["","other"]`), 8)} {
		req := httptest.NewRequest(http.MethodGet, "/api/events/detail"+q, nil)
		if _, _, _, err := parseDetailInterest(req); statusOf(err) != 400 {
			t.Fatalf("accepted invalid interest %s", q)
		}
	}
	sub, initial, err := ts.m.subscribeDetail(sum.ID, "", []bodyRef{{"", "missing"}, {"", "body"}})
	if err != nil || initial[1].event != "body_unavailable" || initial[2].event != "body" {
		t.Fatal("missing member broke valid body")
	}
	ts.m.Unsubscribe(sub)
}

func TestCompactReconnectCoversLoadedAgentRangeAndTrims(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.upsertSubagentLocked(s, agentapi.Subagent{ID: "helper", Status: agentapi.SubagentRunning}, false)
	for i := range 125 {
		m.upsertItemLocked(s, agentapi.Item{ID: fmt.Sprintf("old-%03d", i), AgentID: "helper", Kind: agentapi.ItemAssistant, Text: "before"}, false)
	}
	m.upsertItemLocked(s, agentapi.Item{ID: "old-010", AgentID: "helper", Kind: agentapi.ItemAssistant, Text: "corrected during reconnect"}, true)
	m.mu.Unlock()
	before := base64.RawURLEncoding.EncodeToString([]byte("old-000"))
	sub, initial, err := m.subscribeDetailRange(sum.ID, "helper", nil, before)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	corrected := false
	barrier := compactFrame(t, initial[0]).seq
	for _, part := range initial {
		f := compactFrame(t, part)
		if f.seq != barrier {
			t.Fatal("range not atomic")
		}
		if part.event != "detail_snapshot" && part.event != "detail_page" {
			continue
		}
		var items []compactItem
		decodeField(t, f, "items", &items)
		count += len(items)
		for _, it := range items {
			if it.ID == "old-010" {
				corrected = it.Text == "corrected during reconnect"
			}
		}
	}
	if count != 125 || !corrected {
		t.Fatalf("loaded older extent lost: count%d corrected%v", count, corrected)
	}
	m.Unsubscribe(sub)
	// If the old boundary disappeared, include every still-retained row and mark it.
	m.mu.Lock()
	s.items = s.items[20:]
	s.rebuildIndex()
	m.mu.Unlock()
	sub, initial, err = m.subscribeDetailRange(sum.ID, "helper", nil, before)
	if err != nil {
		t.Fatal(err)
	}
	var truncated bool
	decodeField(t, compactFrame(t, initial[0]), "history_truncated", &truncated)
	if !truncated {
		t.Fatal("missing boundary not reported")
	}
	count = 0
	for _, part := range initial {
		if part.event == "detail_snapshot" || part.event == "detail_page" {
			var items []compactItem
			decodeField(t, compactFrame(t, part), "items", &items)
			count += len(items)
		}
	}
	if count != 105 {
		t.Fatalf("retained range %d", count)
	}
	m.Unsubscribe(sub)
	if _, _, err = m.subscribeDetailRange(sum.ID, "helper", nil, "%"); statusOf(err) != 400 {
		t.Fatal("malformed agent boundary accepted")
	}
}

func TestCompactPreviewEvictionAndBodyTrimCleanup(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.upsertSubagentLocked(s, agentapi.Subagent{ID: "helper", Status: agentapi.SubagentRunning}, false)
	m.upsertItemLocked(s, agentapi.Item{ID: "chat", AgentID: "helper", Kind: agentapi.ItemAssistant, Text: "one"}, true)
	m.applyDeltaLocked(s, agentapi.Delta{ItemID: "chat", AgentID: "helper", Kind: agentapi.ItemAssistant, Text: " two"})
	state := s.previews["helper"]
	if state == nil || state.timer == nil {
		t.Fatal("preview fixture has no trailing timer")
	}
	m.dropHistoryLocked(s)
	if len(s.previews) != 0 || state.timer.Stop() {
		t.Fatal("history eviction retained preview state/timer")
	}
	m.upsertItemLocked(s, agentapi.Item{ID: "tool", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash"}}, false)
	m.mu.Unlock()
	sub, _, err := m.subscribeDetail(sum.ID, "", []bodyRef{{"", "tool"}})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.publishItemsTrimmedLocked(s, []trimmedItem{{ID: "tool"}})
	if len(sub.bodies) != 0 {
		t.Fatal("trim kept stale body interest")
	}
	m.mu.Unlock()
	frameOf(t, sub, "items_trimmed")
	m.Unsubscribe(sub)
}

func TestCompactDetailSlowSubscriberBound(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.upsertItemLocked(s, agentapi.Item{ID: "thought", Kind: agentapi.ItemReasoning, Text: "start"}, false)
	m.mu.Unlock()
	sub, _, err := m.subscribeDetail(sum.ID, "", []bodyRef{{"", "thought"}})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	for range subscriberQueue + 1 {
		m.applyDeltaLocked(s, agentapi.Delta{ItemID: "thought", Kind: agentapi.ItemReasoning, Text: "x"})
	}
	_, retained := m.subs[sub]
	m.mu.Unlock()
	select {
	case <-sub.Gone():
	default:
		t.Fatal("slow detail subscriber not dropped")
	}
	if retained || len(sub.ch) > subscriberQueue || sub.queued.Load() > subscriberBytes {
		t.Fatal("detail queue exceeded bounds")
	}
}

func TestCompactBodyResumeCoversHiddenMutationsAndHistory(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	it := agentapi.Item{ID: "thought", Kind: agentapi.ItemReasoning, Text: "retained"}
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.upsertItemLocked(s, it, false)
	m.mu.Unlock()
	first, err := m.ItemBody(sum.ID, "", "thought")
	if err != nil {
		t.Fatal(err)
	}
	ref := bodyRef{"", "thought"}
	check := func(epoch string, seq uint64, want string) {
		t.Helper()
		sub, initial, err := m.subscribeDetailKnown(sum.ID, "", []bodyRef{ref}, "", epoch, map[bodyRef]uint64{ref: seq})
		if err != nil {
			t.Fatal(err)
		}
		defer m.Unsubscribe(sub)
		if len(initial) != 3 || initial[1].event != want {
			t.Fatalf("resume epoch%q seq%d got%s want%s", epoch, seq, initial[1].event, want)
		}
	}
	check(first.Epoch, first.Seq, "body_current")
	// Other resources must not force an unchanged large body to be retransmitted.
	m.mu.Lock()
	m.upsertItemLocked(s, agentapi.Item{ID: "other", Kind: agentapi.ItemAssistant, Text: "unrelated"}, true)
	m.mu.Unlock()
	check(first.Epoch, first.Seq, "body_current")
	check("old service", first.Seq, "body")
	check(first.Epoch, ^uint64(0), "body")
	// A hidden delta has no main transcript frame but must invalidate the proof.
	m.mu.Lock()
	m.applyDeltaLocked(s, agentapi.Delta{ItemID: "thought", Kind: agentapi.ItemReasoning, Text: " hidden"})
	m.mu.Unlock()
	check(first.Epoch, first.Seq, "body")
	current, _ := m.ItemBody(sum.ID, "", "thought")
	check(current.Epoch, current.Seq, "body_current")
	// History replacement without item publication still invalidates old snapshots.
	m.mu.Lock()
	m.applyHistoryLocked(s, agentapi.History{Items: []agentapi.Item{{ID: "thought", Kind: agentapi.ItemReasoning, Text: "replacement"}}}, false)
	m.mu.Unlock()
	check(current.Epoch, current.Seq, "body")
	// Mutation bookkeeping is removed by retention and eviction.
	m.mu.Lock()
	for i := range maxItems + 1 {
		m.upsertItemLocked(s, agentapi.Item{ID: fmt.Sprintf("retained-%d", i), Kind: agentapi.ItemAssistant}, false)
	}
	if len(s.itemSeq) != len(s.items) {
		t.Fatalf("mutation map leaked: %d keys/%d items", len(s.itemSeq), len(s.items))
	}
	m.dropHistoryLocked(s)
	if len(s.itemSeq) != 0 {
		t.Fatal("eviction retained mutation map")
	}
	m.mu.Unlock()
}

func TestCompactDetailCoveredSequenceParsing(t *testing.T) {
	for _, tuple := range []string{`["","body",2]`, `["","body"]`} {
		req := httptest.NewRequest("GET", "/api/events/detail?session=task&item="+url.QueryEscape(tuple), nil)
		_, _, refs, err := parseDetailInterest(req)
		if err != nil || len(refs) != 1 {
			t.Fatalf("valid covered tuple rejected %s", tuple)
		}
	}
	for _, tuple := range []string{`["","body",-1]`, `["","body",1.2]`, `["","body","2"]`, `["","body",null]`, `["","body",9007199254740992]`} {
		req := httptest.NewRequest("GET", "/api/events/detail?session=task&item="+url.QueryEscape(tuple), nil)
		_, _, _, err := parseDetailInterest(req)
		if statusOf(err) != 400 {
			t.Fatalf("invalid covered tuple accepted %s", tuple)
		}
	}
}

func TestCompactSubagentResultSummaryKeepsMarkdownLines(t *testing.T) {
	prefix := "## Summary\n\n```text\ndiagnostic details\n```\n\nImplemented **the fix**.\n"
	report := prefix + strings.Repeat("界", 300)
	parent := agentapi.Item{ID: "parent", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted, Output: report}}
	s := &webSession{items: []agentapi.Item{parent}, itemIdx: map[string]int{"parent": 0}}
	got := s.compactSubagent(agentapi.Subagent{ID: "helper", ParentToolCallID: "parent", Status: agentapi.SubagentCompleted})
	if !strings.HasPrefix(got.ResultSummary, prefix) || len(got.ResultSummary) > 512 || !utf8.ValidString(got.ResultSummary) {
		t.Fatalf("result summary lost Markdown lines or exceeded bound: %q", got.ResultSummary)
	}
	if s.items[0].Tool.Output != report {
		t.Fatal("summary changed the retained report")
	}
	if short := boundedResultSummary(prefix); short != prefix {
		t.Fatal("short report was changed")
	}
}

func TestCompactForwardHistoryWalkAndLimits(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	items := historyPageFixture(240, 1600)
	m.applyHistoryLocked(s, agentapi.History{Items: items}, false)
	m.mu.Unlock()
	recent, err := m.CompactDetail(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := *recent.HistoryBefore
	var oldest compactHistoryPage
	for before != "" {
		oldest, err = m.CompactOlderHistory(sum.ID, "", before)
		if err != nil {
			t.Fatal(err)
		}
		before = oldest.Before
	}
	got := append([]compactItem{}, oldest.Items...)
	after := oldest.After
	// Concurrent tail append does not shift the named forward boundary.
	m.mu.Lock()
	m.upsertItemLocked(s, agentapi.Item{ID: "new-tail", Kind: agentapi.ItemAssistant, Text: "new"}, true)
	m.mu.Unlock()
	for after != "" {
		page, err := m.CompactHistoryPage(sum.ID, "", "", after)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(page.Items)
		if len(page.Items) == 0 || len(page.Items) > historyPageItems || len(raw) > historyPageBytes+2 || page.Epoch != recent.Epoch {
			t.Fatal("invalid forward page bounds")
		}
		got = append(got, page.Items...)
		after = page.After
	}
	if len(got) != 241 {
		t.Fatalf("forward traversal lost/duplicated records: %d", len(got))
	}
	for i, it := range got[:240] {
		if it.ID != items[i].ID {
			t.Fatalf("forward order %d=%s", i, it.ID)
		}
	}
	if got[240].ID != "new-tail" {
		t.Fatal("concurrent tail append lost")
	}
	head := base64.RawURLEncoding.EncodeToString([]byte(items[0].ID))
	tail := base64.RawURLEncoding.EncodeToString([]byte("new-tail"))
	empty, err := m.CompactHistoryPage(sum.ID, "", head, "")
	if err != nil || len(empty.Items) != 0 || empty.Before != "" || empty.After != head {
		t.Fatal("empty head boundary")
	}
	empty, err = m.CompactHistoryPage(sum.ID, "", "", tail)
	if err != nil || len(empty.Items) != 0 || empty.After != "" || empty.Before != tail {
		t.Fatal("empty tail boundary")
	}
	oversized := historyPageFixture(3, historyPageBytes+1)
	for _, page := range []compactHistoryPage{compactPage(oversized, 3), compactForwardPage(oversized, 0)} {
		if len(page.Items) != 1 || len(page.Items[0].Text) != historyPageBytes+1 {
			t.Fatal("oversized compact item lost")
		}
	}
	m.mu.Lock()
	s.items = s.items[10:]
	s.rebuildIndex()
	m.mu.Unlock()
	if _, err = m.CompactHistoryPage(sum.ID, "", "", head); statusOf(err) != 409 {
		t.Fatal("trimmed forward boundary accepted")
	}
}

func TestCompactHistoryDirectionRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, _ := createSession(t, ts.m, ts.prov)
	ts.m.mu.Lock()
	s := ts.m.sessions[sum.ID]
	ts.m.applyHistoryLocked(s, agentapi.History{Items: historyPageFixture(120, 20)}, false)
	ts.m.upsertSubagentLocked(s, agentapi.Subagent{ID: "helper", Status: agentapi.SubagentRunning}, false)
	for _, it := range historyPageFixture(120, 20) {
		it.AgentID = "helper"
		ts.m.upsertItemLocked(s, it, false)
	}
	ts.m.mu.Unlock()
	cursor := base64.RawURLEncoding.EncodeToString([]byte("item-0000"))
	for _, suffix := range []string{"/history", "/subagents/helper/history"} {
		path := "/api/sessions/" + sum.ID + suffix
		if w := ts.do("GET", path+"?view=compact-v1&after="+cursor, ""); w.Code != 401 {
			t.Fatal("forward route lacks auth")
		}
		w := ts.do("GET", path+"?view=compact-v1&after="+cursor, "", withCookie(ts))
		var page compactHistoryPage
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		if w.Code != 200 || len(page.Items) != 50 || page.Items[0].ID != "item-0001" || page.Before == "" || page.After == "" || page.Seq == 0 || page.Epoch == "" {
			t.Fatalf("forward route: %d %+v", w.Code, page)
		}
		for _, query := range []string{"", "&after=", "&after=%25", "&before=" + cursor + "&after=" + cursor, "&after=" + cursor + "&after=" + cursor, "&before=&after=" + cursor} {
			if w := ts.do("GET", path+"?view=compact-v1"+query, "", withCookie(ts)); w.Code != 400 {
				t.Fatalf("invalid direction %q accepted: %d", query, w.Code)
			}
		}
	}
	// Legacy requests retain their original shape, even with unknown extra params.
	path := "/api/sessions/" + sum.ID + "/history?before=" + base64.RawURLEncoding.EncodeToString([]byte("item-0100"))
	w := ts.do("GET", path+"&after=ignored", "", withCookie(ts))
	if w.Code != 200 || strings.Contains(w.Body.String(), `"after"`) || strings.Contains(w.Body.String(), `"representation"`) {
		t.Fatal("legacy history contract changed")
	}
	if w := ts.do("GET", "/api/sessions/"+sum.ID+"/subagents/missing/history?view=compact-v1&after="+cursor, "", withCookie(ts)); w.Code != 404 {
		t.Fatal("unknown forward agent accepted")
	}
}

func TestCompactDetailWindowSkipsEvictedGap(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.upsertSubagentLocked(s, agentapi.Subagent{ID: "helper", Status: agentapi.SubagentRunning}, false)
	for i := range 1800 {
		m.upsertItemLocked(s, agentapi.Item{ID: fmt.Sprintf("row-%04d", i), AgentID: "helper", Kind: agentapi.ItemAssistant, Text: "retained"}, false)
	}
	m.upsertItemLocked(s, agentapi.Item{ID: "row-0450", AgentID: "helper", Kind: agentapi.ItemAssistant, Text: "corrected during reconnect"}, true)
	m.mu.Unlock()
	cursor := func(id string) string { return base64.RawURLEncoding.EncodeToString([]byte(id)) }
	sub, initial, err := m.subscribeDetailWindow(sum.ID, "helper", nil, cursor("row-0400"), cursor("row-0549"), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	snapshot := compactFrame(t, initial[0])
	var rangeMode bool
	decodeField(t, snapshot, "range", &rangeMode)
	var tail []compactItem
	decodeField(t, snapshot, "items", &tail)
	if !rangeMode || len(tail) != 50 || tail[0].ID != "row-1750" {
		t.Fatal("recent tail must stay bounded and separate")
	}
	count := 0
	seen := map[string]bool{}
	corrected := false
	for _, part := range initial {
		f := compactFrame(t, part)
		if f.seq != snapshot.seq {
			t.Fatal("window barrier differs")
		}
		if part.event != "detail_page" {
			continue
		}
		var scope, before, after string
		decodeField(t, f, "scope", &scope)
		decodeField(t, f, "before", &before)
		decodeField(t, f, "after", &after)
		var page []compactItem
		decodeField(t, f, "items", &page)
		if scope != "window" || before == "" || after == "" || len(page) > 50 {
			t.Fatal("window frame contract")
		}
		for _, it := range page {
			if it.ID < "row-0400" || it.ID > "row-0549" || seen[it.ID] {
				t.Fatal("window included evicted gap/duplicate")
			}
			seen[it.ID] = true
			count++
			if it.ID == "row-0450" {
				corrected = it.Text == "corrected during reconnect"
			}
		}
	}
	if count != 150 || !corrected {
		t.Fatalf("bounded range incomplete: count%d correction%v", count, corrected)
	}
	// Missing, reversed, and oversized requests cannot silently hydrate the gap.
	for _, tc := range []struct {
		from, to string
		want     int
		reset    bool
	}{{"gone", "row-0549", 0, true}, {"row-0400", "gone", 0, true}, {"row-0549", "row-0400", 400, false}, {"row-0000", "row-1500", 0, true}} {
		stream, parts, err := m.subscribeDetailWindow(sum.ID, "helper", nil, cursor(tc.from), cursor(tc.to), "", nil)
		if tc.want != 0 {
			if statusOf(err) != tc.want {
				t.Fatal("reversed range accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var reset, truncated bool
		f := compactFrame(t, parts[0])
		decodeField(t, f, "range_reset", &reset)
		decodeField(t, f, "history_truncated", &truncated)
		if !reset || !truncated || len(parts) != 2 {
			t.Fatal("range reset replayed unbounded history")
		}
		m.Unsubscribe(stream)
	}
	if _, _, err = m.subscribeDetailWindow(sum.ID, "helper", nil, "", cursor("row-0400"), "", nil); statusOf(err) != 400 {
		t.Fatal("upper bound without lower accepted")
	}
	if _, _, err = m.subscribeDetailWindow(sum.ID, "helper", nil, "%", cursor("row-0400"), "", nil); statusOf(err) != 400 {
		t.Fatal("malformed window accepted")
	}
}

func TestCompactDetailWindowByteAccounting(t *testing.T) {
	items := historyPageFixture(2, maxDetailWindowBytes/4)
	if compactWindowFits(items) {
		t.Fatal("two large items exceeded window byte target")
	}
	if !compactWindowFits(items[:1]) {
		t.Fatal("single oversized item cannot make progress")
	}
	if compactWindowFits(historyPageFixture(maxDetailWindowItems+1, 1)) {
		t.Fatal("window item cap not enforced")
	}
	if !compactWindowFits(historyPageFixture(maxDetailWindowItems, 20)) {
		t.Fatal("ordinary compact window rejected")
	}
}

func TestCompactWindowAccountingMatchesBrowser(t *testing.T) {
	// The reference applies historyWindow.ts's charges to the actual decoded
	// wire shape, including omission rules and Unicode UTF-16 code units.
	var account func(any) int
	account = func(value any) int {
		switch v := value.(type) {
		case string:
			units := 0
			for _, r := range v {
				units++
				if r > 0xffff {
					units++
				}
			}
			return 24 + 2*units
		case float64:
			return 8
		case bool, nil:
			return 4
		case []any:
			n := 32
			for _, entry := range v {
				n += 8 + account(entry)
			}
			return n
		case map[string]any:
			n := 64
			for key, entry := range v {
				n += 32 + 2*len(key) + account(entry)
			}
			return n
		default:
			t.Fatalf("unexpected JSON value %T", value)
			return 0
		}
	}
	now := time.Date(2026, 9, 25, 12, 34, 56, 123456789, time.FixedZone("zone", 3600))
	for _, it := range []agentapi.Item{
		{ID: "empty", Kind: agentapi.ItemAssistant},
		{ID: "chat界", Kind: agentapi.ItemAssistant, Text: "line\n<界😀>&", Time: now, EndedAt: now, AgentID: "agent", Delivery: "steer", Attachments: []agentapi.Attachment{{ID: "upload", Name: "文😀", MIME: "text/plain", Size: 20, NotNative: true}, {Name: "empty", MIME: ""}}},
		{ID: "tool", Kind: agentapi.ItemTool, Text: "hidden", Time: now, Tool: &agentapi.ToolCall{Name: "edit", Title: "Edit file", Status: agentapi.ToolCompleted, Input: `{"path":"文😀.go","content":"hidden"}`, Output: "hidden"}, Images: []agentapi.Image{{ID: "image", MIME: "image/png", Name: "文", Size: 123}, {ID: "zero"}}, ImagesNote: "note"},
		{ID: "ask", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "ask_user", Input: "<\n界", Output: "😀", Status: agentapi.ToolCompleted}},
		{ID: "thought", Kind: agentapi.ItemReasoning, Text: "hidden"},
	} {
		projected := projectItem(it)
		raw, err := json.Marshal(projected)
		if err != nil {
			t.Fatal(err)
		}
		var wire any
		if err = json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if got, want := compactItemBytes(projected), account(wire); got != want {
			t.Fatalf("%s account=%d want%d wire%s", it.ID, got, want, raw)
		}
	}
	for _, text := range []string{strings.Repeat("界", 350000), strings.Repeat("<", 180000), strings.Repeat("😀", 200000)} {
		items := []agentapi.Item{{ID: "a", Kind: agentapi.ItemAssistant, Text: text}, {ID: "b", Kind: agentapi.ItemAssistant, Text: text}}
		if !compactWindowFits(items) {
			t.Fatal("browser-admissible Unicode/escaped window would reset")
		}
	}
}
