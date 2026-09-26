package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// archiveRecord is a record of n main-agent items, item-0000 first, and of
// subagent sa1, whose task tool call and 120 items come ten items before the
// end.
func archiveRecord(n int) agentapi.History {
	var items []agentapi.Item
	for i := range n {
		items = append(items, agentapi.Item{ID: fmt.Sprintf("item-%04d", i), Kind: agentapi.ItemAssistant, Text: fmt.Sprintf("answer %d", i)})
		if i == n-10 {
			items = append(items, agentapi.Item{ID: "task-1", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted, Input: `{"description":"explore"}`, Output: "found three files"}})
			for j := range 120 {
				items = append(items, agentapi.Item{ID: fmt.Sprintf("s-%03d", j), Kind: agentapi.ItemAssistant, Text: fmt.Sprintf("sub %d", j), AgentID: "sa1"})
			}
		}
	}
	return agentapi.History{Items: items, Subagents: []agentapi.Subagent{{ID: "sa1", Name: "explore", Status: agentapi.SubagentCompleted, ParentToolCallID: "task-1"}}}
}

func recordIDs(h agentapi.History, agent string) []string {
	var out []string
	for _, it := range h.Items {
		if it.AgentID == agent {
			out = append(out, it.ID)
		}
	}
	return out
}

// archivedTask settles a Task in a first run and starts a second one whose
// provider prov (a Pager or a plain Provider) has record as the Task's
// conversation, so viewing the Task reads the record without opening it.
func archivedTask(t *testing.T, record agentapi.History, pager bool) (*Manager, *agenttest.Pager, *store.Store, SessionSummary) {
	t.Helper()
	st := openTestStore(t)
	first := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	sum, err := first.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, first, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov := agenttest.NewPager("fake", allCaps)
	prov.SetHistory(sum.ConversationID, record)
	var m *Manager
	if pager {
		m = startManager(t, st, prov)
	} else {
		m = startManager(t, st, prov.Provider)
	}
	waitHistory(t, m, sum.ID, HistoryLoaded)
	return m, prov, st, sum
}

// walkBack pages agent's transcript back from before to its first item and
// returns every item ID, oldest first, the cursors used and how many pages
// came from the record.
func walkBack(t *testing.T, m *Manager, id, agent string, items []compactItem, before string) (ids, cursors []string, archived int) {
	t.Helper()
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	for before != "" {
		cursors = append(cursors, before)
		page, err := m.CompactHistoryPage(id, agent, before, "")
		if err != nil {
			t.Fatalf("page before %q: %v", before, err)
		}
		if len(page.Items) == 0 || len(page.Items) > historyPageItems || page.Epoch != m.epoch || page.Representation != compactRepresentation {
			t.Fatalf("page before %q: %d items, epoch %q", before, len(page.Items), page.Epoch)
		}
		if page.Archive {
			archived++
			if page.Before != "" && !strings.HasPrefix(page.Before, archiveCursorPrefix) {
				t.Fatalf("archive page cursor %q", page.Before)
			}
		}
		var older []string
		for _, it := range page.Items {
			older = append(older, it.ID)
		}
		ids = append(older, ids...)
		before = page.Before
	}
	return ids, cursors, archived
}

func (m *Manager) heldItems(id string) (items []agentapi.Item, truncated bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	return slices.Clone(s.items), s.truncated
}

func TestArchivePagingReachesTheRecordStartAcrossRestarts(t *testing.T) {
	record := archiveRecord(2500)
	m, pager, st, sum := archivedTask(t, record, true)
	held, truncated := m.heldItems(sum.ID)
	// 2,501 main-agent items: the newest 1,800 are kept, and no subagent's.
	if len(held) != 1800 || !truncated || held[0].ID != "item-0701" || slices.ContainsFunc(held, func(it agentapi.Item) bool { return it.AgentID != "" }) {
		t.Fatalf("held %d items from %s, truncated %v", len(held), held[0].ID, truncated)
	}
	walk := func(m *Manager) ([]string, []string) {
		d, err := m.CompactDetail(sum.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.HistoryTruncated || d.HistoryBefore == nil || *d.HistoryBefore == "" {
			t.Fatalf("reachable history reported truncated %v, before %v", d.HistoryTruncated, d.HistoryBefore)
		}
		ids, cursors, archived := walkBack(t, m, sum.ID, "", d.Items, *d.HistoryBefore)
		if !slices.Equal(ids, recordIDs(record, "")) || archived != 15 {
			t.Fatalf("walked %d items (%d archive pages), want %d without gaps or repeats", len(ids), archived, len(recordIDs(record, "")))
		}
		if held, _ := m.heldItems(sum.ID); len(held) != 1800 {
			t.Fatalf("paging retained %d items", len(held))
		}
		return ids, cursors
	}
	_, cursors := walk(m)
	// One read of the record served all 701 older items.
	if reads := pager.WindowReads(); len(reads) != 1 || reads[0].ItemID != "item-0701" || reads[0].AgentID != "" {
		t.Fatalf("window reads = %+v", reads)
	}
	// Only the main agent's items were read for it.
	m.archive.mu.Lock()
	for e := m.archive.windows.Front(); e != nil; e = e.Next() {
		if w := e.Value.(*archiveWindow); slices.ContainsFunc(w.items, func(it agentapi.Item) bool { return it.AgentID != "" }) {
			t.Fatal("a main archive read kept subagent items")
		}
	}
	m.archive.mu.Unlock()

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := startManager(t, st, pager)
	waitHistory(t, restarted, sum.ID, HistoryLoaded)
	if _, again := walk(restarted); !slices.Equal(again, cursors) {
		t.Fatal("cursors changed across a restart")
	}
}

func TestArchiveCursorsBodiesAndFailures(t *testing.T) {
	record := archiveRecord(2500)
	m, pager, _, sum := archivedTask(t, record, true)
	for _, cursor := range []string{"a.%%%", "%%%", "a."} {
		if _, err := m.CompactHistoryPage(sum.ID, "", cursor, ""); statusOf(err) != 400 {
			t.Fatalf("cursor %q = %v", cursor, err)
		}
	}
	ids := func(page compactHistoryPage) string {
		return page.Items[0].ID + ".." + page.Items[len(page.Items)-1].ID
	}
	// The browser's own cursors name items only the record has, both ways.
	older, err := m.CompactHistoryPage(sum.ID, "", heldCursor("item-0300"), "")
	if err != nil || !older.Archive || ids(older) != "item-0250..item-0299" || older.Before != archiveCursor("item-0250") {
		t.Fatalf("older = %v, %+v", err, older)
	}
	m.archive.forget(sum.ID)
	newer, err := m.CompactHistoryPage(sum.ID, "", "", heldCursor("item-0300"))
	if err != nil || !newer.Archive || ids(newer) != "item-0301..item-0350" || newer.After != archiveCursor("item-0350") {
		t.Fatalf("newer = %v, %+v", err, newer)
	}
	// A page is the same whichever cached window holds its boundary.
	m.archive.forget(sum.ID)
	cold, err := m.CompactHistoryPage(sum.ID, "", archiveCursor("item-0660"), "")
	if err != nil || ids(cold) != "item-0610..item-0659" {
		t.Fatalf("cold page = %v, %+v", err, cold)
	}
	m.archive.forget(sum.ID)
	if _, err := m.CompactHistoryPage(sum.ID, "", "", archiveCursor("item-0620")); err != nil {
		t.Fatal(err)
	}
	if warm, err := m.CompactHistoryPage(sum.ID, "", archiveCursor("item-0660"), ""); err != nil || ids(warm) != ids(cold) || warm.Before != cold.Before {
		t.Fatalf("page after another window = %v, %+v", err, warm)
	}
	seam, err := m.CompactHistoryPage(sum.ID, "", "", archiveCursor("item-0700"))
	if err != nil || seam.Archive || ids(seam) != "item-0701..item-0750" || seam.Before != archiveCursor("item-0701") {
		t.Fatalf("into retained items = %v, %+v", err, seam)
	}

	// Bodies of items only the record has, read again once evicted.
	body, err := m.ItemBody(sum.ID, "", "item-0005")
	if err != nil || body.Item.Text != "answer 5" {
		t.Fatalf("body = %v, %+v", err, body)
	}
	m.archive.forget(sum.ID)
	reads := len(pager.WindowReads())
	if body, err = m.ItemBody(sum.ID, "", "item-0005"); err != nil || body.Item.Text != "answer 5" || len(pager.WindowReads()) != reads+1 {
		t.Fatalf("body after eviction = %v, %+v", err, body)
	}
	m.archive.forget(sum.ID)
	refs := []bodyRef{{"", "item-0006"}, {"", "item-2400"}}
	m.warmDetailBodies(sum.ID, "", refs)
	sub, frames, err := m.subscribeDetail(sum.ID, "", refs)
	if err != nil {
		t.Fatal(err)
	}
	m.Unsubscribe(sub)
	var texts []string
	for _, part := range frames {
		if part.event == "body" {
			texts = append(texts, part.payload.(itemBody).Item.Text)
		}
	}
	if !slices.Equal(texts, []string{"answer 6", "answer 2400"}) {
		t.Fatalf("detail bodies = %v", texts)
	}

	// A boundary the record no longer has.
	changed := record
	changed.Items = slices.DeleteFunc(slices.Clone(record.Items), func(it agentapi.Item) bool { return it.ID == "item-0150" })
	pager.SetHistory(sum.ConversationID, changed)
	m.archive.forget(sum.ID)
	if _, err := m.CompactHistoryPage(sum.ID, "", archiveCursor("item-0150"), ""); statusOf(err) != 409 {
		t.Fatalf("vanished boundary = %v", err)
	}
	if d, _ := m.CompactDetail(sum.ID); d.HistoryTruncated {
		t.Fatal("one vanished item made the transcript unreachable")
	}
	// A record that is gone: the older part cannot be reached any more.
	pager.ForgetConversation(sum.ConversationID)
	m.archive.forget(sum.ID)
	if _, err := m.CompactHistoryPage(sum.ID, "", archiveCursor("item-0500"), ""); statusOf(err) != 409 {
		t.Fatalf("gone record = %v", err)
	}
	d, err := m.CompactDetail(sum.ID)
	if err != nil || !d.HistoryTruncated {
		t.Fatalf("gone record detail = %v, truncated %v", err, d.HistoryTruncated)
	}
	if first, _ := m.CompactHistoryPage(sum.ID, "", heldCursor("item-0751"), ""); first.Before != "" || first.Archive {
		t.Fatalf("first retained page still leads into the record: %+v", first.Before)
	}
	if _, err := m.CompactHistoryPage(sum.ID, "", heldCursor("item-0300"), ""); statusOf(err) != 409 {
		t.Fatalf("unreachable item = %v", err)
	}

	// Without paging the transcript stays truncated.
	plain, _, _, other := archivedTask(t, record, false)
	if d, _ := plain.CompactDetail(other.ID); !d.HistoryTruncated {
		t.Fatal("a provider that cannot page reports reachable history")
	}
}

func TestSubagentTranscriptsAreReadWhenOpened(t *testing.T) {
	record := archiveRecord(2500)
	m, pager, _, sum := archivedTask(t, record, true)
	plain, _, _, other := archivedTask(t, record, false)
	d, err := m.CompactDetail(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	want, err := plain.CompactDetail(other.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The list entry is unchanged although no subagent item is retained.
	if len(d.Subagents) != 1 || fmt.Sprint(d.Subagents) != fmt.Sprint(want.Subagents) || d.Subagents[0].Preview != "found three files" {
		t.Fatalf("subagents = %+v, want %+v", d.Subagents, want.Subagents)
	}
	if reads := pager.WindowReads(); len(reads) != 0 {
		t.Fatalf("listing read transcripts: %+v", reads)
	}

	sa, err := m.CompactSubagent(sum.ID, "sa1")
	if err != nil {
		t.Fatal(err)
	}
	if !sa.Archive || len(sa.Items) != 50 || sa.Items[49].ID != "s-119" || !strings.HasPrefix(sa.Before, archiveCursorPrefix) {
		t.Fatalf("subagent detail: archive %v, %d items, before %q", sa.Archive, len(sa.Items), sa.Before)
	}
	ids, _, archived := walkBack(t, m, sum.ID, "sa1", sa.Items, sa.Before)
	if !slices.Equal(ids, recordIDs(record, "sa1")) || archived != 2 {
		t.Fatalf("subagent walk = %d items, %d archive pages", len(ids), archived)
	}
	if reads := pager.WindowReads(); len(reads) != 1 || reads[0].AgentID != "sa1" || reads[0].ItemID != "" {
		t.Fatalf("window reads = %+v", reads)
	}
	if held, _ := m.heldItems(sum.ID); slices.ContainsFunc(held, func(it agentapi.Item) bool { return it.AgentID != "" }) {
		t.Fatal("opening a subagent retained its items")
	}
	m.archive.forget(sum.ID)
	newer, err := m.CompactHistoryPage(sum.ID, "sa1", "", heldCursor("s-010"))
	if err != nil || !newer.Archive || newer.Items[0].ID != "s-011" || newer.Items[49].ID != "s-060" {
		t.Fatalf("newer subagent page = %v, %+v", err, newer)
	}
	if body, err := m.ItemBody(sum.ID, "sa1", "s-100"); err != nil || body.Item.Text != "sub 100" {
		t.Fatalf("subagent body = %v, %+v", err, body)
	}

	m.archive.forget(sum.ID)
	m.warmDetailBodies(sum.ID, "sa1", nil)
	sub, frames, err := m.subscribeDetail(sum.ID, "sa1", nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Unsubscribe(sub)
	snap := frames[0].payload.(detailSnapshot)
	if !snap.Archive || snap.HistoryTruncated || len(snap.Items) != 50 || snap.Items[0].ID != "s-070" || !strings.HasPrefix(*snap.Before, archiveCursorPrefix) {
		t.Fatalf("subagent snapshot: archive %v, truncated %v, %d items", snap.Archive, snap.HistoryTruncated, len(snap.Items))
	}
}

func TestClippedItemsHaveWholeBodies(t *testing.T) {
	record := archiveRecord(100)
	long := strings.Repeat("é", 3<<20) // 6 MiB
	output := strings.Repeat("z", 300<<10)
	adapterClipped := strings.Repeat("q", 100<<10)
	record.Items = append(record.Items,
		agentapi.Item{ID: "long", Kind: agentapi.ItemAssistant, Text: long},
		agentapi.Item{ID: "big", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Output: output}},
		agentapi.Item{ID: "clipped", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Output: adapterClipped}})
	st := openTestStore(t)
	first := startManager(t, st, agenttest.NewProvider("fake", allCaps))
	sum, err := first.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, first, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Settle(sum.ID); err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	pager := agenttest.NewPager("fake", allCaps)
	pager.SetHistory(sum.ConversationID, record)
	// The adapter's read keeps 64 KiB of this output, as Copilot's does.
	pager.SetReadHook(func(context.Context, agentapi.ReadRequest) (agentapi.History, error) {
		h := record
		h.Items = slices.Clone(record.Items)
		tool := *h.Items[len(h.Items)-1].Tool
		tool.Output = adapterClipped[:64<<10]
		h.Items[len(h.Items)-1].Tool, h.Items[len(h.Items)-1].Clipped = &tool, true
		return h, nil
	})
	m := startManager(t, st, pager)
	waitHistory(t, m, sum.ID, HistoryLoaded)
	d, err := m.CompactDetail(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The long message fills a page of its own.
	older, err := m.CompactHistoryPage(sum.ID, "", *d.HistoryBefore, "")
	if err != nil {
		t.Fatal(err)
	}
	clipped := map[string]bool{}
	for _, it := range append(older.Items, d.Items...) {
		clipped[it.ID] = it.Clipped
	}
	if !clipped["long"] || !clipped["big"] || !clipped["clipped"] || clipped["item-0099"] {
		t.Fatalf("clipped = %v", clipped)
	}
	whole := map[string]func(agentapi.Item) bool{
		"long":    func(it agentapi.Item) bool { return it.Text == long },
		"big":     func(it agentapi.Item) bool { return it.Tool.Output == output },
		"clipped": func(it agentapi.Item) bool { return it.Tool.Output == adapterClipped },
	}
	for id, ok := range whole {
		body, err := m.ItemBody(sum.ID, "", id)
		if err != nil || !ok(body.Item) {
			t.Fatalf("body of %s = %v, whole %v", id, err, err == nil && ok(body.Item))
		}
	}
	m.archive.forget(sum.ID)
	refs := []bodyRef{{"", "long"}, {"", "big"}, {"", "clipped"}}
	m.warmDetailBodies(sum.ID, "", refs)
	sub, frames, err := m.subscribeDetail(sum.ID, "", refs)
	if err != nil {
		t.Fatal(err)
	}
	m.Unsubscribe(sub)
	n := 0
	for _, part := range frames {
		if part.event == "body" {
			if body := part.payload.(itemBody); !whole[body.Item.ID](body.Item) {
				t.Fatalf("detail body of %s is not whole", body.Item.ID)
			}
			n++
		}
	}
	if n != 3 {
		t.Fatalf("%d whole detail bodies", n)
	}
}

func TestSettledClippedToolIsSentWhole(t *testing.T) {
	pager := agenttest.NewPager("fake", allCaps)
	m := startManager(t, openTestStore(t), pager)
	sum, conv := createSession(t, m, pager.Provider)
	running := agentapi.Item{ID: "tool", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolRunning}}
	conv.EmitItem(running)
	sub, _, err := m.subscribeDetail(sum.ID, "", []bodyRef{{"", "tool"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	output := strings.Repeat("o", 100<<10)
	whole := running
	whole.Tool = &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Output: output}
	pager.SetHistory(sum.ConversationID, agentapi.History{Items: []agentapi.Item{whole}})
	// The adapter keeps 64 KiB of the live output.
	clipped := whole
	clipped.Tool, clipped.Clipped = &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Output: output[:64<<10]}, true
	conv.EmitItem(clipped)
	var sizes []int
	for len(sizes) < 2 {
		var it agentapi.Item
		decodeField(t, frameOf(t, sub, "body"), "item", &it)
		sizes = append(sizes, len(it.Tool.Output))
	}
	if !slices.Equal(sizes, []int{64 << 10, 100 << 10}) {
		t.Fatalf("body outputs = %v", sizes)
	}
}

func TestArchivePagingFollowsAnOpenConversation(t *testing.T) {
	st := openTestStore(t)
	pager := agenttest.NewPager("fake", allCaps)
	m := startManager(t, st, pager)
	sum, conv := createSession(t, m, pager.Provider)
	var record []agentapi.Item
	emit := func(n int) {
		for range n {
			it := agentapi.Item{ID: fmt.Sprintf("live-%04d", len(record)), Kind: agentapi.ItemAssistant, Text: "streamed"}
			record = append(record, it)
			conv.EmitItem(it)
		}
		pager.SetHistory(sum.ConversationID, agentapi.History{Items: record})
		if held, _ := m.heldItems(sum.ID); len(held) > maxItems {
			t.Fatalf("%d items retained", len(held))
		}
	}
	emit(2100)
	d, err := m.CompactDetail(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.HistoryTruncated || !d.Open {
		t.Fatalf("open conversation: truncated %v, open %v", d.HistoryTruncated, d.Open)
	}
	want := slices.Clone(record)
	ids := []string{}
	for _, it := range d.Items {
		ids = append(ids, it.ID)
	}
	// The record grows while it is paged, and trimming moves retained
	// items the browser already named into the record.
	before, archived, trimmed := *d.HistoryBefore, 0, 0
	for pages := 0; before != ""; pages++ {
		n := 40
		if pages == 3 {
			n = 1900 // enough to trim past the item the cursor names
		}
		emit(n)
		if held, _ := m.heldItems(sum.ID); !strings.HasPrefix(before, archiveCursorPrefix) && !slices.ContainsFunc(held, func(it agentapi.Item) bool { return heldCursor(it.ID) == before }) {
			trimmed++
		}
		page, err := m.CompactHistoryPage(sum.ID, "", before, "")
		if err != nil {
			t.Fatalf("page before %q: %v", before, err)
		}
		if page.Archive {
			archived++
		}
		var older []string
		for _, it := range page.Items {
			older = append(older, it.ID)
		}
		ids = append(older, ids...)
		before = page.Before
	}
	var wantIDs []string
	for _, it := range want {
		wantIDs = append(wantIDs, it.ID)
	}
	if !slices.Equal(ids, wantIDs) || archived == 0 || trimmed == 0 {
		t.Fatalf("walked %d items, want %d; %d archive pages, %d cursors of trimmed items", len(ids), len(wantIDs), archived, trimmed)
	}
}

// subagentRecord is a record of n completed subagents, sa-000 first, each
// spawned by a task tool call of the main agent and with three items; the
// record leaves sa-010 running, and sa-005 has 60 items.
func subagentRecord(n int) agentapi.History {
	var h agentapi.History
	for i := range n {
		id, call := fmt.Sprintf("sa-%03d", i), fmt.Sprintf("task-%03d", i)
		status := agentapi.SubagentCompleted
		if i == 10 {
			status = agentapi.SubagentRunning
		}
		h.Subagents = append(h.Subagents, agentapi.Subagent{ID: id, Name: "explore " + id, Status: status, ParentToolCallID: call})
		h.Items = append(h.Items, agentapi.Item{ID: call, Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "task", Status: agentapi.ToolCompleted, Output: "result " + id}})
		items := 3
		if i == 5 {
			items = 60
		}
		for j := range items {
			h.Items = append(h.Items, agentapi.Item{ID: fmt.Sprintf("%s-%02d", id, j), Kind: agentapi.ItemAssistant, Text: "sub", AgentID: id})
		}
	}
	return h
}

func compactIDs(subs []compactSubagent) []string {
	out := make([]string, len(subs))
	for i, sa := range subs {
		out[i] = sa.ID
	}
	return out
}

// walkSubagents lists a Task's subagents as a browser does: the held ones,
// then every older page. It returns their IDs, oldest first, and the
// cursors used.
func walkSubagents(t *testing.T, m *Manager, id string) (ids, cursors []string) {
	t.Helper()
	d, err := m.CompactDetail(id)
	if err != nil {
		t.Fatal(err)
	}
	ids = compactIDs(d.Subagents)
	for before := d.SubagentsBefore; before != ""; {
		cursors = append(cursors, before)
		page, err := m.OlderSubagents(id, before)
		if err != nil {
			t.Fatalf("subagents before %q: %v", before, err)
		}
		if len(page.Subagents) == 0 || len(page.Subagents) > subagentPageRecords || page.Epoch != m.epoch || page.Representation != compactRepresentation {
			t.Fatalf("subagents before %q: %d records, epoch %q", before, len(page.Subagents), page.Epoch)
		}
		ids = append(compactIDs(page.Subagents), ids...)
		before = page.Before
	}
	return ids, cursors
}

func TestSubagentListPagesToTheFirstRecordedOneAcrossRestarts(t *testing.T) {
	record := subagentRecord(450)
	m, pager, st, sum := archivedTask(t, record, true)
	var all []string
	for _, sa := range record.Subagents {
		all = append(all, sa.ID)
	}
	d, err := m.CompactDetail(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The newest 200 are held; the list says older ones can be read.
	if len(d.Subagents) != maxSubagents || d.Subagents[0].ID != "sa-250" || d.SubagentsBefore != archiveCursor("sa-250") {
		t.Fatalf("held %d subagents from %s, before %q", len(d.Subagents), d.Subagents[0].ID, d.SubagentsBefore)
	}
	if reads := pager.SubagentReads(); len(reads) != 0 {
		t.Fatalf("listing read the record: %+v", reads)
	}
	ids, cursors := walkSubagents(t, m, sum.ID)
	if !slices.Equal(ids, all) || len(cursors) != 3 {
		t.Fatalf("walked %d subagents in %d pages, want %d without gaps or repeats", len(ids), len(cursors), len(all))
	}
	// One read of the record served all 250 older records.
	if reads := pager.SubagentReads(); len(reads) != 1 || reads[0].AgentID != "sa-250" || reads[0].Before != subagentWindowRecords {
		t.Fatalf("subagent reads = %+v", reads)
	}
	// A subagent the record leaves running shows as the listed ones do.
	page, err := m.OlderSubagents(sum.ID, archiveCursor("sa-011"))
	if err != nil || len(page.Subagents) != 11 || page.Before != "" {
		t.Fatalf("first page = %v, %+v", err, page)
	}
	if sa := page.Subagents[10]; sa.ID != "sa-010" || sa.Status != agentapi.SubagentCancelled || sa.Name != "explore sa-010" || sa.ResultSummary != "result sa-010" {
		t.Fatalf("recorded subagent = %+v", sa)
	}
	if held, _ := m.heldItems(sum.ID); slices.ContainsFunc(held, func(it agentapi.Item) bool { return it.AgentID != "" }) {
		t.Fatal("paging the list read subagent items")
	}
	for _, cursor := range []string{"%%%", "a.", ""} {
		if _, err := m.OlderSubagents(sum.ID, cursor); statusOf(err) != 400 {
			t.Fatalf("cursor %q = %v", cursor, err)
		}
	}
	if _, err := m.OlderSubagents(sum.ID, archiveCursor("nobody")); statusOf(err) != 409 {
		t.Fatalf("unrecorded boundary = %v", err)
	}

	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test", Assets: fstest.MapFS{"index.html": {Data: []byte("app")}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m}
	path := "/api/sessions/" + sum.ID + "/subagents"
	if w := ts.do("GET", path+"?before="+url.QueryEscape(cursors[0]), ""); w.Code != 401 {
		t.Fatalf("unauthenticated page = %d", w.Code)
	}
	w := ts.do("GET", path+"?before="+url.QueryEscape(cursors[0]), "", withCookie(ts))
	var got compactSubagentPage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || len(got.Subagents) != subagentPageRecords || got.Subagents[0].ID != "sa-150" || got.Before != archiveCursor("sa-150") {
		t.Fatalf("page route = %d %v %s", w.Code, err, w.Body)
	}
	for _, query := range []string{"", "?before=" + url.QueryEscape(cursors[0]) + "&before=" + url.QueryEscape(cursors[0])} {
		if w := ts.do("GET", path+query, "", withCookie(ts)); w.Code != 400 {
			t.Fatalf("query %q = %d", query, w.Code)
		}
	}

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := startManager(t, st, pager)
	waitHistory(t, restarted, sum.ID, HistoryLoaded)
	if again, cursorsAgain := walkSubagents(t, restarted, sum.ID); !slices.Equal(again, all) || !slices.Equal(cursorsAgain, cursors) {
		t.Fatal("subagent cursors changed across a restart")
	}
}

func TestSubagentOnlyTheRecordListsOpens(t *testing.T) {
	record := subagentRecord(250)
	m, pager, _, sum := archivedTask(t, record, true)
	if d, _ := m.CompactDetail(sum.ID); slices.Contains(compactIDs(d.Subagents), "sa-005") {
		t.Fatal("sa-005 is held")
	}
	sa, err := m.CompactSubagent(sum.ID, "sa-005")
	if err != nil {
		t.Fatal(err)
	}
	if sa.Subagent.ID != "sa-005" || sa.Subagent.Status != agentapi.SubagentCompleted || sa.Subagent.ResultSummary != "result sa-005" || !sa.Archive || len(sa.Items) != 50 || sa.Items[49].ID != "sa-005-59" {
		t.Fatalf("subagent detail: %+v, archive %v, %d items", sa.Subagent, sa.Archive, len(sa.Items))
	}
	if reads := pager.SubagentReads(); len(reads) != 1 || reads[0].AgentID != "sa-005" || reads[0].Before != 0 {
		t.Fatalf("subagent reads = %+v", reads)
	}
	ids, _, _ := walkBack(t, m, sum.ID, "sa-005", sa.Items, sa.Before)
	if !slices.Equal(ids, recordIDs(record, "sa-005")) {
		t.Fatalf("walked %v", ids)
	}
	if body, err := m.ItemBody(sum.ID, "sa-005", "sa-005-01"); err != nil || body.Item.Text != "sub" {
		t.Fatalf("body = %v, %+v", err, body)
	}
	if d, _ := m.CompactDetail(sum.ID); len(d.Subagents) != maxSubagents || slices.Contains(compactIDs(d.Subagents), "sa-005") {
		t.Fatal("opening a subagent the record lists held it")
	}

	// The detail stream, once the cache forgot both reads.
	m.archive.forget(sum.ID)
	m.warmDetailBodies(sum.ID, "sa-005", nil)
	sub, frames, err := m.subscribeDetail(sum.ID, "sa-005", nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Unsubscribe(sub)
	snap := frames[0].payload.(detailSnapshot)
	if snap.Subagent == nil || snap.Subagent.ID != "sa-005" || !snap.Archive || snap.HistoryTruncated || len(snap.Items) != 50 || snap.Items[0].ID != "sa-005-10" {
		t.Fatalf("subagent snapshot: %+v, archive %v, truncated %v, %d items", snap.Subagent, snap.Archive, snap.HistoryTruncated, len(snap.Items))
	}

	if _, err := m.CompactSubagent(sum.ID, "nobody"); statusOf(err) != 404 {
		t.Fatalf("unknown subagent = %v", err)
	}
	m.warmDetailBodies(sum.ID, "nobody", nil)
	if _, _, err := m.subscribeDetail(sum.ID, "nobody", nil); statusOf(err) != 404 {
		t.Fatalf("unknown subagent stream = %v", err)
	}
}

func TestSubagentListPagesAnOpenConversation(t *testing.T) {
	pager := agenttest.NewPager("fake", allCaps)
	m := startManager(t, openTestStore(t), pager)
	sum, conv := createSession(t, m, pager.Provider)
	var record agentapi.History
	emit := func(id string, status agentapi.SubagentStatus) {
		sa := agentapi.Subagent{ID: id, Name: id, Status: status}
		record.Subagents = append(record.Subagents, sa)
		conv.EmitSubagent(sa)
	}
	// A running subagent is never forgotten; the held list skips the
	// finished ones after it.
	emit("sa-000", agentapi.SubagentRunning)
	for i := 1; i <= 250; i++ {
		emit(fmt.Sprintf("sa-%03d", i), agentapi.SubagentCompleted)
	}
	pager.SetHistory(sum.ConversationID, record)
	d, err := m.CompactDetail(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Subagents) != maxSubagents || d.Subagents[0].ID != "sa-000" || d.Subagents[1].ID != "sa-052" || d.SubagentsBefore != archiveCursor("sa-052") {
		t.Fatalf("held %v.., before %q", compactIDs(d.Subagents[:2]), d.SubagentsBefore)
	}
	// The page repeats the held running one as it is held.
	page, err := m.OlderSubagents(sum.ID, d.SubagentsBefore)
	if err != nil || len(page.Subagents) != 52 || page.Before != "" || page.Subagents[0].Status != agentapi.SubagentRunning || page.Subagents[51].ID != "sa-051" {
		t.Fatalf("page = %v, %d records, before %q", err, len(page.Subagents), page.Before)
	}
	// A provider that cannot page its subagents offers no cursor.
	plain := startManager(t, openTestStore(t), agenttest.NewProvider("fake", allCaps))
	other, plainConv := createSession(t, plain, plain.providers["fake"].(*agenttest.Provider))
	for i := range maxSubagents + 1 {
		plainConv.EmitSubagent(agentapi.Subagent{ID: fmt.Sprint(i), Status: agentapi.SubagentCompleted})
	}
	if d, _ := plain.CompactDetail(other.ID); d.SubagentsBefore != "" {
		t.Fatalf("cursor %q without a pager", d.SubagentsBefore)
	}
}
