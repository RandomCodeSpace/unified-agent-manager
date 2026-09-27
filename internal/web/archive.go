package web

import (
	"container/list"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// The service keeps a bounded part of each transcript and of each item's
// text. When the provider can page its record (agentapi.HistoryPager), the
// rest stays reachable: history pages past the oldest retained item, and
// bodies of items not retained or retained clipped, are read from the record
// a window at a time. Windows are served, never retained in the Task; the
// most recent ones stay in one cache all Tasks share, bounded by
// maxArchiveBytes and read again once evicted.
const (
	// archiveCursorPrefix marks a cursor naming a recorded item. It holds
	// only the item's ID, so it stays valid across restarts while the record
	// has the item, and it is not base64url, so it differs from every
	// retained item's cursor.
	archiveCursorPrefix = "a."
	maxArchiveBytes     = 32 << 20
	// archiveWindowItems is how many items one read keeps on the side a
	// transcript is paged towards, so the next pages need no read.
	archiveWindowItems = 1000
	// maxDetailReads bounds the record reads one detail stream starts.
	maxDetailReads = 3
	// subagentPageRecords is how many subagent records one list page holds;
	// subagentWindowRecords how many one read keeps before its boundary.
	subagentPageRecords   = 100
	subagentWindowRecords = 1000
)

func heldCursor(id string) string    { return base64.RawURLEncoding.EncodeToString([]byte(id)) }
func archiveCursor(id string) string { return archiveCursorPrefix + heldCursor(id) }

// decodeHistoryCursor returns the item a retained or archive cursor names.
func decodeHistoryCursor(cursor string) (string, error) {
	raw, _ := strings.CutPrefix(cursor, archiveCursorPrefix)
	id, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(id) == 0 || len(id) > 4096 {
		return "", newError(http.StatusBadRequest, "invalid history cursor")
	}
	return string(id), nil
}

// archiveWindow is a contiguous run of one agent's recorded items, whole,
// or of the recorded subagents. It never changes once cached.
type archiveWindow struct {
	key       string
	items     []agentapi.Item
	subagents []agentapi.Subagent
	// index holds the position of each item, or subagent, by ID.
	index map[string]int
	start bool   // items begins with the agent's first recorded item
	next  string // the item recorded after items, "" at the end of the record
	bytes int
}

type archiveCache struct {
	mu      sync.Mutex
	windows list.List // *archiveWindow, most recently used first
	bytes   int
}

func archiveKey(id, convID, agent string) string {
	return id + "\x00" + convID + "\x00" + agent
}

// holds reports where w holds id, the end of the record for "".
func (w *archiveWindow) holds(id string) (int, bool) {
	if id == "" {
		return len(w.items), w.next == ""
	}
	at, ok := w.index[id]
	return at, ok
}

// find returns fresh, else the most recently used cached window of key, that
// holds id where accept (nil accepts any) allows, and that position.
func (c *archiveCache) find(fresh *archiveWindow, key, id string, accept func(*archiveWindow, int) bool) (*archiveWindow, int) {
	if fresh != nil && fresh.key == key {
		if at, ok := fresh.holds(id); ok && (accept == nil || accept(fresh, at)) {
			return fresh, at
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for e := c.windows.Front(); e != nil; e = e.Next() {
		w := e.Value.(*archiveWindow)
		if at, ok := w.holds(id); ok && w.key == key && (accept == nil || accept(w, at)) {
			c.windows.MoveToFront(e)
			return w, at
		}
	}
	return nil, -1
}

func (c *archiveCache) add(w *archiveWindow) {
	if w.bytes > maxArchiveBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.windows.PushFront(w)
	c.bytes += w.bytes
	for c.bytes > maxArchiveBytes {
		c.bytes -= c.windows.Remove(c.windows.Back()).(*archiveWindow).bytes
	}
}

// forget drops the windows of Task id.
func (c *archiveCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for e := c.windows.Front(); e != nil; {
		next := e.Next()
		if w := e.Value.(*archiveWindow); strings.HasPrefix(w.key, id+"\x00") {
			c.bytes -= c.windows.Remove(e).(*archiveWindow).bytes
		}
		e = next
	}
}

// pagerLocked returns s's provider when it can page s's record.
func (m *Manager) pagerLocked(s *webSession) agentapi.HistoryPager {
	pager, _ := m.providers[s.provider].(agentapi.HistoryPager)
	if s.convID == "" || s.archiveGone {
		return nil
	}
	return pager
}

// archiveRead is a record read a request needs before it can be answered.
type archiveRead struct {
	s     *webSession
	pager agentapi.HistoryPager
	req   agentapi.WindowRequest
}

// key is the cache key of the window r reads; it needs no lock.
func (r *archiveRead) key() string {
	return archiveKey(r.s.id, r.req.ConversationID, r.req.AgentID)
}

func (m *Manager) windowReadLocked(s *webSession, agent, item string, before, after int) *archiveRead {
	return &archiveRead{s, m.pagerLocked(s), agentapi.WindowRequest{ReadRequest: agentapi.ReadRequest{ConversationID: s.convID, Workdir: s.workdir},
		AgentID: agent, ItemID: item, Before: before, After: after}}
}

// readArchive reads a window of a record without Manager.mu, in one of the
// history read slots, stores its tool images with the Task as a record read
// does, and caches it.
func (m *Manager) readArchive(r *archiveRead) (*archiveWindow, error) {
	if r.pager == nil {
		return nil, newError(http.StatusConflict, msgHistoryChanged)
	}
	release, err := m.readSlot(m.ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	defer cancel()
	h, err := r.pager.ReadHistoryWindow(ctx, r.req)
	if err != nil {
		return nil, m.archiveFailure(r, err)
	}
	w := &archiveWindow{key: r.key(), items: h.Items, index: make(map[string]int, len(h.Items)), start: h.Start, next: h.Next}
	for i := range w.items {
		it := &w.items[i]
		if it.Kind == agentapi.ItemTool {
			m.keepImages(r.s, it)
		}
		if _, dup := w.index[it.ID]; !dup {
			w.index[it.ID] = i
		}
		w.bytes += itemSize(*it)
	}
	m.archive.add(w)
	return w, nil
}

// archiveFailure records what a failed record read says about the Task and
// returns the request's error. A record that is gone, or has no item before
// which the retained main transcript begins, cannot reach its older part:
// the transcript then reports itself truncated again.
func (m *Manager) archiveFailure(r *archiveRead, err error) error {
	gone := errors.Is(err, agentapi.ErrConversationNotFound)
	if gone || errors.Is(err, agentapi.ErrItemNotFound) {
		m.mu.Lock()
		if v := m.viewLocked(r.s, r.req.AgentID, nil); gone || r.req.AgentID == "" && len(v.items) > 0 && !v.archived && v.items[0].ID == r.req.ItemID {
			r.s.archiveGone = true
		}
		m.mu.Unlock()
		return newError(http.StatusConflict, msgHistoryChanged)
	}
	var webErr *Error
	if errors.As(err, &webErr) {
		return err
	}
	log.Warn("read web task record window failed", "session", r.s.id, "error", err)
	return newError(http.StatusServiceUnavailable, "could not read the recorded transcript: %s", shortError(err))
}

// transcriptView is what pages of one agent's transcript serve from
// memory: its retained items, or the record's newest ones when none are.
type transcriptView struct {
	items []agentapi.Item
	// head is the cursor for the items before items[0], "" when none can
	// be reached.
	head string
	// archive is set when items older than those retained can be read
	// from the record; archived when items themselves were read from it.
	archive, archived bool
	// missing is set when the record's newest items must be read first.
	missing bool
}

// viewLocked returns agent's transcript view. fresh is a window just read,
// which the cache may already have evicted.
func (m *Manager) viewLocked(s *webSession, agent string, fresh *archiveWindow) transcriptView {
	v := transcriptView{items: s.agentItems(agent)}
	// A subagent that is not held may have items that are not either.
	if m.pagerLocked(s) == nil || !s.truncated && (agent == "" || !s.subagentsArchived && s.subIdx[agent] != nil) {
		return v
	}
	v.archive = true
	// A reasoning item's recorded ID can differ from the one it streamed
	// with, so the record is paged before the item it precedes.
	lead := 0
	for lead < len(v.items) && v.items[lead].Kind == agentapi.ItemReasoning {
		lead++
	}
	if lead < len(v.items) {
		v.items = v.items[lead:]
		v.head = archiveCursor(v.items[0].ID)
		return v
	}
	v.items = nil
	w, _ := m.archive.find(fresh, archiveKey(s.id, s.convID, agent), "", nil)
	if w == nil {
		v.missing = true
		return v
	}
	v.items, v.archived = m.archivedItemsLocked(s, w.items), true
	if !w.start && len(w.items) > 0 {
		v.head = archiveCursor(w.items[0].ID)
	}
	return v
}

// truncated reports whether older items than v's exist but cannot be read.
func (v transcriptView) truncated(s *webSession) bool {
	return s.truncated && (!v.archive || v.missing)
}

func (v transcriptView) cursor(id string) string {
	if v.archived {
		return archiveCursor(id)
	}
	return heldCursor(id)
}

// page is the page of v's items that ends before end.
func (v transcriptView) page(end int) compactHistoryPage {
	page := compactPage(v.items, end)
	start := end - len(page.Items)
	v.recursor(&page, start, end)
	return page
}

// forward is the page of v's items from start on.
func (v transcriptView) forward(start int) compactHistoryPage {
	page := compactForwardPage(v.items, start)
	v.recursor(&page, start, start+len(page.Items))
	return page
}

func (v transcriptView) recursor(page *compactHistoryPage, start, end int) {
	if len(page.Items) == 0 {
		return
	}
	page.Archive = v.archived
	page.Before = v.head
	if start > 0 {
		page.Before = v.cursor(v.items[start].ID)
	}
	page.After = ""
	if end < len(v.items) {
		page.After = v.cursor(v.items[end-1].ID)
	}
}

// archivedItemsLocked returns what the service would retain of recorded
// items.
func (m *Manager) archivedItemsLocked(s *webSession, items []agentapi.Item) []agentapi.Item {
	out := make([]agentapi.Item, len(items))
	for i, it := range items {
		out[i] = clampItem(it, m.now())
		s.linkUploadsLocked(&out[i])
	}
	return out
}

// windowPageLocked is the archive page of w's items that ends before end.
func (m *Manager) windowPageLocked(s *webSession, w *archiveWindow, end int) compactHistoryPage {
	low := max(0, end-historyPageItems)
	page := compactPage(m.archivedItemsLocked(s, w.items[low:end]), end-low)
	page.Archive, page.Before, page.After = true, "", ""
	if len(page.Items) > 0 {
		if start := end - len(page.Items); start > 0 || !w.start {
			page.Before = archiveCursor(w.items[start].ID)
		}
		page.After = archiveCursor(w.items[end-1].ID)
	}
	return page
}

// windowForwardLocked is the archive page of w's items from start on. It
// ends before the first retained item, whose page follows.
func (m *Manager) windowForwardLocked(s *webSession, w *archiveWindow, start int, held map[string]bool) compactHistoryPage {
	end := start
	for end < len(w.items) && end-start < historyPageItems && !held[w.items[end].ID] {
		end++
	}
	page := compactForwardPage(m.archivedItemsLocked(s, w.items[start:end]), 0)
	end = start + len(page.Items)
	page.Archive, page.Before, page.After = true, "", ""
	if len(page.Items) > 0 {
		if start > 0 || !w.start {
			page.Before = archiveCursor(w.items[start].ID)
		}
		if end < len(w.items) || w.next != "" || len(held) > 0 {
			page.After = archiveCursor(w.items[end-1].ID)
		}
	}
	return page
}

// historyPageLocked is the page of agent's transcript before (or, with
// forward, after) the item boundary, or the record read it needs first. A
// page of the record depends on the record alone, never on which window
// holds it: a window is used only when it holds the whole page.
func (m *Manager) historyPageLocked(s *webSession, agent, boundary string, forward bool, fresh *archiveWindow) (compactHistoryPage, *archiveRead, error) {
	v := m.viewLocked(s, agent, fresh)
	index := slices.IndexFunc(v.items, func(it agentapi.Item) bool { return it.ID == boundary })
	key := archiveKey(s.id, s.convID, agent)
	if !forward {
		switch {
		case !v.archived && (index > 0 || index == 0 && !v.archive):
			return v.page(index), nil, nil
		case !v.archive:
			return compactHistoryPage{}, nil, newError(http.StatusConflict, msgHistoryChanged)
		}
		w, at := m.archive.find(fresh, key, boundary, func(w *archiveWindow, at int) bool { return at >= historyPageItems || w.start })
		if w == nil {
			return compactHistoryPage{}, m.windowReadLocked(s, agent, boundary, archiveWindowItems, 0), nil
		}
		return m.windowPageLocked(s, w, at), nil, nil
	}
	if index >= 0 && !v.archived {
		return v.forward(index + 1), nil, nil
	}
	if !v.archive {
		return compactHistoryPage{}, nil, newError(http.StatusConflict, msgHistoryChanged)
	}
	held := map[string]bool{}
	if !v.archived {
		for _, it := range v.items {
			held[it.ID] = true
		}
	}
	// Newer items follow in the window up to the first retained one.
	w, at := m.archive.find(fresh, key, boundary, func(w *archiveWindow, at int) bool {
		after := w.items[at+1:]
		return len(after) >= historyPageItems || w.next == "" || slices.ContainsFunc(after, func(it agentapi.Item) bool { return held[it.ID] })
	})
	switch {
	case w == nil:
		return compactHistoryPage{}, m.windowReadLocked(s, agent, boundary, 0, archiveWindowItems), nil
	case at+1 < len(w.items) && held[w.items[at+1].ID]:
		next := w.items[at+1].ID
		return v.forward(slices.IndexFunc(v.items, func(it agentapi.Item) bool { return it.ID == next })), nil, nil
	case at+1 < len(w.items):
		return m.windowForwardLocked(s, w, at+1, held), nil, nil
	case len(held) > 0:
		// Retained items the record does not have yet follow its end.
		return v.forward(0), nil, nil
	}
	return compactHistoryPage{Archive: true}, nil, nil
}

// settled reports whether an item gets no further provider updates.
func settled(it agentapi.Item) bool {
	return it.Tool == nil || it.Tool.Status != agentapi.ToolRunning
}

// itemBodyLocked returns the body of one item: whole from the record when
// the retained copy was clipped or the item is not retained, else the
// retained copy. whole is false when the record was not used; read is the
// record read that would provide the whole body.
func (m *Manager) itemBodyLocked(s *webSession, agent, item string, fresh *archiveWindow) (it agentapi.Item, whole bool, read *archiveRead, err error) {
	key := archiveKey(s.id, s.convID, agent)
	if i, ok := s.itemIdx[itemKey(agent, item)]; ok {
		held := s.items[i]
		if !held.Clipped || !settled(held) || m.pagerLocked(s) == nil {
			return held, false, nil, nil
		}
		// A record not yet holding the settled item is not used.
		if w, at := m.archive.find(fresh, key, item, nil); w != nil && settled(w.items[at]) && (held.Tool == nil) == (w.items[at].Tool == nil) &&
			(held.Tool == nil || held.Tool.Status == w.items[at].Tool.Status) {
			return m.wholeItemLocked(s, w.items[at]), true, nil, nil
		}
		return held, false, m.windowReadLocked(s, agent, item, 0, 0), nil
	}
	if v := m.viewLocked(s, agent, nil); !v.archive {
		return agentapi.Item{}, false, nil, newError(http.StatusNotFound, "item is no longer retained")
	}
	if w, at := m.archive.find(fresh, key, item, nil); w != nil {
		return m.wholeItemLocked(s, w.items[at]), true, nil, nil
	}
	return agentapi.Item{}, false, m.windowReadLocked(s, agent, item, historyPageItems, historyPageItems), nil
}

func (m *Manager) wholeItemLocked(s *webSession, it agentapi.Item) agentapi.Item {
	it = checkItem(it, m.now())
	s.linkUploadsLocked(&it)
	return it
}

// warmDetailBodies reads from the record, before a detail stream's first
// frames, what they serve but memory lacks: the record of a subagent that is
// not held, a subagent's newest items when its transcript is not retained,
// and the whole bodies of the items named, in at most maxDetailReads reads
// of items.
func (m *Manager) warmDetailBodies(id, agent string, refs []bodyRef) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil || s.removed || m.pagerLocked(s) == nil {
		m.mu.Unlock()
		return
	}
	var reads []*archiveRead
	var record *subagentRead
	if agent != "" {
		_, read, ok := m.subagentLocked(s, agent, nil)
		if record = read; (ok || read != nil) && m.viewLocked(s, agent, nil).missing {
			reads = append(reads, m.windowReadLocked(s, agent, "", archiveWindowItems, 0))
		}
	}
	for _, ref := range refs {
		if _, _, read, _ := m.itemBodyLocked(s, ref[0], ref[1], nil); read != nil {
			reads = append(reads, read)
		}
	}
	m.mu.Unlock()
	if record != nil {
		if _, err := m.readSubagents(record); err != nil {
			return // the stream then reports the subagent missing
		}
	}
	for i, r := range reads {
		if i == maxDetailReads {
			return
		}
		// An earlier window may hold this item too.
		if r.req.ItemID != "" {
			if w, _ := m.archive.find(nil, r.key(), r.req.ItemID, nil); w != nil {
				continue
			}
		}
		_, _ = m.readArchive(r) // a failed read leaves the body unavailable
	}
}

// refreshBodyLocked sends body subscribers of a clipped item that just
// settled its whole body once the record has it.
func (m *Manager) refreshBodyLocked(s *webSession, it agentapi.Item) {
	read := m.windowReadLocked(s, it.AgentID, it.ID, 0, 0)
	if read.pager == nil || m.closed {
		return
	}
	key, changed := itemKey(it.AgentID, it.ID), s.itemSeq[itemKey(it.AgentID, it.ID)]
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		w, err := m.readArchive(read)
		if err != nil {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if s.removed || s.itemSeq[key] != changed {
			return
		}
		if body, whole, _, _ := m.itemBodyLocked(s, it.AgentID, it.ID, w); whole && itemSize(body) <= subscriberBytes/2 {
			m.broadcastFilteredLocked("body", s.id, bodySubscriber(it), func(seq uint64) any { return itemBody{detailBarrier{seq, m.epoch, s.id}, it.AgentID, body} })
		}
	}()
}

// subagentPagerLocked returns s's provider when it can page s's recorded
// subagents.
func (m *Manager) subagentPagerLocked(s *webSession) agentapi.SubagentPager {
	pager, _ := m.providers[s.provider].(agentapi.SubagentPager)
	if s.convID == "" || s.archiveGone {
		return nil
	}
	return pager
}

// subagentsBeforeLocked is the cursor of the subagents recorded before the
// list head that are not held, "" when there are none or they cannot be
// read.
func (m *Manager) subagentsBeforeLocked(s *webSession) string {
	if !s.subagentsOlder || s.subagentHead >= len(s.subagents) || m.subagentPagerLocked(s) == nil {
		return ""
	}
	return archiveCursor(s.subagents[s.subagentHead].ID)
}

// subagentRead is a read of recorded subagents a request needs first.
type subagentRead struct {
	s     *webSession
	pager agentapi.SubagentPager
	req   agentapi.SubagentRequest
}

func (r *subagentRead) key() string { return subagentsKey(r.s.id, r.req.ConversationID) }

// subagentsKey is the cache key of a Task's recorded subagents; the \x01
// sets it apart from every agent's items.
func subagentsKey(id, convID string) string { return id + "\x00" + convID + "\x01" }

func (m *Manager) subagentReadLocked(s *webSession, agent string, before int) *subagentRead {
	return &subagentRead{s, m.subagentPagerLocked(s), agentapi.SubagentRequest{ReadRequest: agentapi.ReadRequest{ConversationID: s.convID, Workdir: s.workdir},
		AgentID: agent, Before: before}}
}

// readSubagents reads recorded subagents as readArchive reads items: without
// Manager.mu, in a history read slot, into the shared cache.
func (m *Manager) readSubagents(r *subagentRead) (*archiveWindow, error) {
	if r.pager == nil {
		return nil, newError(http.StatusConflict, msgHistoryChanged)
	}
	release, err := m.readSlot(m.ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	defer cancel()
	h, err := r.pager.ReadSubagents(ctx, r.req)
	switch {
	case errors.Is(err, agentapi.ErrConversationNotFound):
		m.mu.Lock()
		r.s.archiveGone = true
		m.mu.Unlock()
		return nil, newError(http.StatusConflict, msgHistoryChanged)
	case errors.Is(err, agentapi.ErrItemNotFound):
		return nil, newError(http.StatusNotFound, msgSubagentNotFound)
	case err != nil:
		var webErr *Error
		if errors.As(err, &webErr) {
			return nil, err
		}
		log.Warn("read web task subagents failed", "session", r.s.id, "error", err)
		return nil, newError(http.StatusServiceUnavailable, "could not read the recorded subagents: %s", shortError(err))
	}
	w := &archiveWindow{key: r.key(), subagents: make([]agentapi.Subagent, len(h.Subagents)), index: make(map[string]int, len(h.Subagents)), start: h.Start}
	for i, sa := range h.Subagents {
		sa = clampSubagent(sa)
		w.subagents[i] = sa
		if _, dup := w.index[sa.ID]; !dup {
			w.index[sa.ID] = i
		}
		w.bytes += len(sa.ID) + len(sa.Name) + len(sa.Description) + len(sa.Model) + len(sa.Effort) + len(sa.Error) + len(sa.ParentToolCallID) + len(sa.Result)
	}
	m.archive.add(w)
	return w, nil
}

// subagentLocked returns agent's record: the held one, else one a cached
// read of the record holds. read is the record read that would find it.
func (m *Manager) subagentLocked(s *webSession, agent string, fresh *archiveWindow) (sa agentapi.Subagent, read *subagentRead, ok bool) {
	if held := s.subIdx[agent]; held != nil {
		return *held, nil, true
	}
	if agent == "" || m.subagentPagerLocked(s) == nil {
		return agentapi.Subagent{}, nil, false
	}
	read = m.subagentReadLocked(s, agent, 0)
	if w, at := m.archive.find(fresh, read.key(), agent, nil); w != nil {
		return m.recordedSubagentLocked(s, w.subagents[at]), nil, true
	}
	return agentapi.Subagent{}, read, false
}

// recordedSubagentLocked is what the list shows of a recorded subagent: its
// held record when it has one. Nothing of a conversation that is not open
// runs, as installHistoryLocked has it.
func (m *Manager) recordedSubagentLocked(s *webSession, sa agentapi.Subagent) agentapi.Subagent {
	if held := s.subIdx[sa.ID]; held != nil {
		return *held
	}
	if s.conv == nil {
		switch sa.Status {
		case agentapi.SubagentRunning:
			sa.Status = agentapi.SubagentCancelled
		case agentapi.SubagentIdle:
			sa.Status = agentapi.SubagentCompleted
		}
	}
	return sa
}

// compactSubagentPage is a page of recorded subagents, oldest first.
type compactSubagentPage struct {
	Seq            uint64            `json:"seq"`
	Epoch          string            `json:"epoch"`
	Representation string            `json:"representation"`
	Subagents      []compactSubagent `json:"subagents"`
	// Before is the cursor of the subagents recorded before these, "" at
	// the first.
	Before string `json:"before"`
}

// OlderSubagents returns the page of subagents recorded before the one an
// archive cursor names, from the record.
func (m *Manager) OlderSubagents(id, before string) (compactSubagentPage, error) {
	boundary, err := decodeHistoryCursor(before)
	if err != nil {
		return compactSubagentPage{}, err
	}
	var fresh *archiveWindow
	for reads := 0; ; reads++ {
		m.mu.Lock()
		s := m.sessions[id]
		if s == nil {
			m.mu.Unlock()
			return compactSubagentPage{}, newError(http.StatusNotFound, msgSessionNotFound)
		}
		s.historyUsed = m.now()
		read := m.subagentReadLocked(s, boundary, subagentWindowRecords)
		// A window read for this page serves it even when bounded short.
		w, at := m.archive.find(fresh, read.key(), boundary, func(w *archiveWindow, at int) bool {
			return w.start || at >= subagentPageRecords || w == fresh && at > 0
		})
		if w != nil {
			page := compactSubagentPage{Seq: m.seq, Epoch: m.epoch, Representation: compactRepresentation}
			low := max(0, at-subagentPageRecords)
			for _, sa := range w.subagents[low:at] {
				page.Subagents = append(page.Subagents, s.compactSubagent(m.recordedSubagentLocked(s, sa)))
			}
			if low > 0 || !w.start {
				page.Before = archiveCursor(w.subagents[low].ID)
			}
			m.mu.Unlock()
			if page.Subagents == nil {
				page.Subagents = []compactSubagent{}
			}
			return page, nil
		}
		m.mu.Unlock()
		if reads == 2 {
			return compactSubagentPage{}, newError(http.StatusServiceUnavailable, "the recorded subagents changed while they were read; try again")
		}
		if fresh, err = m.readSubagents(read); err != nil {
			if status, _ := errorStatus(err); status == http.StatusNotFound {
				return compactSubagentPage{}, newError(http.StatusConflict, msgHistoryChanged)
			}
			return compactSubagentPage{}, err
		}
	}
}

func (s *Server) handleSubagents(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()["before"]) != 1 {
		writeFailure(w, newError(http.StatusBadRequest, "provide one subagent cursor"))
		return
	}
	page, err := s.m.OlderSubagents(r.PathValue("id"), r.URL.Query().Get("before"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// subagentTail is what a subagent's list entry shows from a transcript that
// is not retained: its last message, the summary input bounded as summaries
// bound it, and its latest preview.
type subagentTail struct {
	lastAssistant agentapi.Item
	preview       string
}

// archiveSubagentItems returns the main agent's items and the list tails
// of the subagents whose items it leaves out.
func archiveSubagentItems(items []agentapi.Item) ([]agentapi.Item, map[string]subagentTail) {
	tails := map[string]subagentTail{}
	main := slices.DeleteFunc(items, func(it agentapi.Item) bool {
		if it.AgentID == "" {
			return false
		}
		tail := tails[it.AgentID]
		if it.Kind == agentapi.ItemAssistant {
			it.Text = clipRunesExact(it.Text, maxSummaryInputRunes)
			tail.lastAssistant = it
		}
		if p := itemPreview(it); p != "" {
			tail.preview = boundedPreview(p, 512)
		}
		tails[it.AgentID] = tail
		return true
	})
	return main, tails
}

// clipRunesExact cuts s to at most n runes, unmarked.
func clipRunesExact(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
