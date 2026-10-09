package web

import (
	"cmp"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// Per-session memory bounds. Provider output is untrusted and unbounded; the
// service keeps a bounded tail and says so (history_truncated).
const (
	maxItems            = 2000
	maxItemText         = 4 << 20
	maxToolText         = 256 << 10
	maxLabelText        = 4 << 10
	maxSessionBytes     = 64 << 20
	maxInteractions     = 200
	maxInteractionText  = 256 << 10
	maxAnswerBytes      = 64 << 10
	maxSubagents        = 200
	maxItemAttachments  = 50
	maxToolCallID       = 256
	maxDeclarationCards = 128
)

const truncatedMarker = "\n[truncated by uam]"

// clampText cuts s to at most limit bytes on a rune boundary and marks the
// cut.
func clampText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncatedMarker
}

// clampItem bounds an item's texts, marking it Clipped when it cuts one, and
// checks the rest as checkItem does.
func clampItem(it agentapi.Item, now time.Time) agentapi.Item {
	it = checkItem(it, now)
	clamp := func(s *string, limit int) {
		if len(*s) > limit {
			*s, it.Clipped = clampText(*s, limit), true
		}
	}
	clamp(&it.Text, maxItemText)
	if it.Tool != nil {
		clamp(&it.Tool.Name, maxLabelText)
		clamp(&it.Tool.Title, maxLabelText)
		clamp(&it.Tool.Input, maxToolText)
		clamp(&it.Tool.Output, maxToolText)
	}
	return it
}

// checkItem copies an item with its declaration, attachments and images
// checked, and its texts whole.
func checkItem(it agentapi.Item, now time.Time) agentapi.Item {
	if it.Kind == agentapi.ItemNotice {
		it.Completion = checkCompletion(it.Completion)
	} else {
		it.Completion = nil
	}
	it.Plan = clampPlanReview(it.Plan, false)
	if it.Tool != nil {
		tool := *it.Tool
		if !validDetailID(tool.EditEventID, false) || len(tool.EditEventID) > 256 {
			tool.EditEventID, tool.FileEdits, tool.FileEditsTruncated = "", nil, false
		} else {
			kept, bytes := make([]agentapi.FileEdit, 0, min(len(tool.FileEdits), agentapi.MaxFileEdits)), 0
			for _, edit := range tool.FileEdits {
				if len(kept) >= agentapi.MaxFileEdits || bytes+len(edit.Path) > agentapi.MaxFileEditPathsBytes ||
					!filepath.IsAbs(edit.Path) || !validDetailID(edit.Path, false) || len(edit.Path) > agentapi.MaxFileEditPathBytes ||
					!validDetailID(edit.Kind, false) || len(edit.Kind) > 32 || !validDetailID(edit.DiffStatus, false) || len(edit.DiffStatus) > 32 || edit.Additions < 0 || edit.Deletions < 0 {
					tool.FileEditsTruncated = true
					continue
				}
				if edit.DiffStatus != "available" {
					edit.Additions, edit.Deletions = 0, 0
				}
				kept = append(kept, edit)
				bytes += len(edit.Path)
			}
			tool.FileEdits = kept
		}
		if d := tool.Declaration; d != nil {
			if d.ArtifactID == "" || len(d.ArtifactID) > 64 || !utf8.ValidString(d.ArtifactID) || strings.ContainsFunc(d.ArtifactID, unicode.IsControl) ||
				!filepath.IsAbs(d.Path) || len(d.Path) > maxGrantPathBytes || !utf8.ValidString(d.Path) || strings.ContainsFunc(d.Path, unicode.IsControl) ||
				len(d.Title) > 128 || !utf8.ValidString(d.Title) || strings.ContainsFunc(d.Title, unicode.IsControl) ||
				len(d.TypeHint) > 32 || !utf8.ValidString(d.TypeHint) || strings.ContainsFunc(d.TypeHint, unicode.IsControl) {
				tool.Declaration = nil
			} else {
				declaration := *d
				tool.Declaration = &declaration
			}
		}
		it.Tool = &tool
	}
	if len(it.Attachments) > maxItemAttachments {
		it.Attachments = it.Attachments[:maxItemAttachments]
	}
	it.Attachments = slices.Clone(it.Attachments)
	for i := range it.Attachments {
		a := &it.Attachments[i]
		a.Name = clipRunes(displaytext.Sanitize(a.Name), maxNameRunes)
		a.MIME = clampText(displaytext.Sanitize(a.MIME), maxLabelText)
	}
	// Only a tool item carries images, only those keepImages stored, and
	// never their bytes.
	if it.Kind != agentapi.ItemTool {
		it.Images, it.ImagesNote = nil, ""
	}
	it.Images = slices.DeleteFunc(slices.Clone(it.Images), func(img agentapi.Image) bool { return img.ID == "" })
	for i := range it.Images {
		it.Images[i].Data = nil
	}
	if it.Time.IsZero() {
		it.Time = now
	}
	return it
}

func itemSize(it agentapi.Item) int {
	n := len(it.ID) + len(it.Text)
	if c := it.Completion; c != nil {
		n += len(c.Decision) + len(c.UserItemID) + len(c.Summary) + len(c.Reason)
		if c.Blocker != nil {
			n += len(c.Blocker.Kind) + len(c.Blocker.Reason)
		}
	}
	if it.Plan != nil {
		n += len(it.Plan.RequestID) + len(it.Plan.Summary) + len(it.Plan.Recommended)
		for _, action := range it.Plan.Actions {
			n += len(action)
		}
	}
	if it.Tool != nil {
		n += len(it.Tool.Name) + len(it.Tool.Title) + len(it.Tool.Input) + len(it.Tool.Output) + len(it.Tool.EditEventID)
		for _, edit := range it.Tool.FileEdits {
			n += len(edit.Path) + len(edit.Kind) + len(edit.DiffStatus) + 16
		}
		if d := it.Tool.Declaration; d != nil {
			n += len(d.ArtifactID) + len(d.Path) + len(d.Title) + len(d.TypeHint)
		}
		for _, line := range it.Tool.Tail {
			n += len(line.Text)
		}
	}
	return n
}

func checkCompletion(in *agentapi.TaskCompletion) *agentapi.TaskCompletion {
	if in == nil {
		return nil
	}
	c := *in
	switch c.Decision {
	case agentapi.CompletionAccepted, agentapi.CompletionRejected, agentapi.CompletionBlocked, agentapi.CompletionUnknown:
	default:
		c.Decision = agentapi.CompletionUnknown
	}
	if len(c.UserItemID) > 256 || !utf8.ValidString(c.UserItemID) || strings.ContainsFunc(c.UserItemID, unicode.IsControl) {
		c.UserItemID = ""
	}
	text := func(s string, limit int) string {
		s = strings.TrimSpace(displaytext.Sanitize(s))
		if len(s) > limit {
			for limit > 0 && !utf8.RuneStart(s[limit]) {
				limit--
			}
			s = s[:limit]
		}
		return strings.Clone(s)
	}
	c.Summary, c.Reason = text(c.Summary, 512), text(c.Reason, 256)
	if c.Blocker != nil {
		b := *c.Blocker
		b.Kind, b.Reason = text(b.Kind, 64), text(b.Reason, 64)
		c.Blocker = &b
	}
	return &c
}

// itemKey indexes an item: item IDs are unique per agent only. The main
// agent and every subagent share one retained list and its bounds.
func itemKey(agentID, id string) string {
	if agentID == "" {
		return id
	}
	return agentID + "\x00" + id
}

// row publishes the compact list row too; a caller omits it when the row
// shows nothing new.
func (m *Manager) publishItemLocked(s *webSession, it agentapi.Item, appendItem, row bool) {
	m.broadcastFilteredLocked("item", s.id, legacySubscriber, func(seq uint64) any {
		return itemEvent{Seq: seq, SessionID: s.id, AgentID: it.AgentID, Item: it, Append: appendItem}
	})
	if row {
		m.publishCompactItemLocked(s, it, appendItem)
	}
	m.publishBodyLocked(s, it)
	m.itemPreviewLocked(s, it)
}

// Send evictions after the item/delta that triggered them, including when an
// update to an old item evicts that very item. A browser must not recreate it.
func (m *Manager) publishItemsTrimmedLocked(s *webSession, removed []trimmedItem) {
	if len(removed) == 0 {
		return
	}
	m.broadcastFilteredLocked("items_trimmed", s.id, nil, func(seq uint64) any {
		return itemsTrimmedEvent{Seq: seq, SessionID: s.id, Items: removed}
	})
	for sub := range m.subs {
		if sub.detail && sub.session == s.id {
			for _, it := range removed {
				delete(sub.bodies, itemKey(it.AgentID, it.ID))
			}
		}
	}
}

// Item mutation barriers are bounded to retained IDs and precede every publication.
func (m *Manager) markItemMutationLocked(s *webSession, it agentapi.Item) {
	m.seq++
	if s.itemSeq == nil {
		s.itemSeq = map[string]uint64{}
	}
	s.itemSeq[itemKey(it.AgentID, it.ID)] = m.seq
}

func confirmedSteerReceipt(previous, next agentapi.Item) bool {
	return previous.Kind == agentapi.ItemUser && previous.Delivery == agentapi.DeliverySteer && previous.SteerStatus != "" && next.Kind == agentapi.ItemUser && next.SteerStatus == ""
}

// upsertItemLocked replaces the item with the same agent and ID or appends
// it. A replacement keeps the earliest start: a tool's completion event is
// stamped when it finished, not when it began.
func (m *Manager) upsertItemLocked(s *webSession, it agentapi.Item, publish bool) {
	var previous agentapi.Item
	delete(s.textBuffers, itemKey(it.AgentID, it.ID))
	// Keep only the newest declaration cards. The ordinary tool rows remain
	// available and the provider journal remains the replay source.
	if it.Tool != nil && it.Tool.Declaration != nil {
		oldIndex, count := -1, 0
		for i, existing := range s.items {
			if existing.Tool != nil && existing.Tool.Declaration != nil && itemKey(existing.AgentID, existing.ID) != itemKey(it.AgentID, it.ID) {
				count++
				if oldIndex < 0 {
					oldIndex = i
				}
			}
		}
		if count >= maxDeclarationCards && oldIndex >= 0 {
			old := s.items[oldIndex]
			stripped := *old.Tool
			stripped.Declaration = nil
			old.Tool = &stripped
			s.itemBytes -= itemSize(s.items[oldIndex])
			s.items[oldIndex] = old
			s.itemBytes += itemSize(old)
			m.markItemMutationLocked(s, old)
			if publish {
				m.publishItemLocked(s, old, false, true)
			}
		}
	}
	moveIdleSteer := false
	if i, ok := s.itemIdx[itemKey(it.AgentID, it.ID)]; ok {
		previous = s.items[i]
		confirmedSteer := confirmedSteerReceipt(previous, it)
		moveIdleSteer = confirmedSteer && it.Delivery == ""
		if confirmedSteer && len(it.Attachments) == 0 {
			it.Attachments = slices.Clone(previous.Attachments)
		}
		if prev := previous.Time; !moveIdleSteer && !prev.IsZero() && prev.Before(it.Time) {
			it.Time = prev
		}
		s.itemBytes -= itemSize(s.items[i])
		if moveIdleSteer {
			s.items = append(s.items[:i], s.items[i+1:]...)
			s.items = append(s.items, it)
			s.rebuildIndex()
		} else {
			s.items[i] = it
		}
	} else {
		s.itemIdx[itemKey(it.AgentID, it.ID)] = len(s.items)
		s.items = append(s.items, it)
	}
	if previous.ID == "" || !reflect.DeepEqual(previous, it) {
		m.markItemMutationLocked(s, it)
	}
	s.itemBytes += itemSize(it)
	removed := s.trimItems()
	if publish {
		defer m.publishItemsTrimmedLocked(s, removed)
		if m.paceToolOutputLocked(s, previous, it) {
			return
		}
		// The browser's row is the previous item's projection.
		sameRow := previous.ID != "" && !moveIdleSteer && sameCompactRow(previous, it)
		m.publishItemLocked(s, it, previous.ID == "" || moveIdleSteer, !sameRow)
	}
}

// toolOutputOnly reports whether next is previous, still running, with only
// its live output changed: its output and tail.
func toolOutputOnly(previous, next agentapi.Item) bool {
	if previous.Tool == nil || next.Tool == nil || next.Kind != agentapi.ItemTool || next.Tool.Status != agentapi.ToolRunning {
		return false
	}
	tool := *previous.Tool
	tool.Output, tool.Tail = next.Tool.Output, next.Tool.Tail
	previous.Tool = &tool
	return reflect.DeepEqual(previous, next)
}

// toolOutputState paces the publications of one running tool: at is the
// last whole one, rowAt the last of its row. A pending timer publishes the
// latest item, whole when the browser's copy can no longer follow it by
// deltas, its row when that changed.
type toolOutputState struct {
	at, rowAt  time.Time
	timer      *time.Timer
	whole, row bool
}

// paceToolOutputLocked publishes a change to a running tool's live output
// and reports whether it took the change; any other change it leaves to the
// caller to publish at once, ending the pacing. Growing output goes out at
// once as deltas. Output that replaces rather than extends the previous one,
// as a CLI's sliding tail window does, is published whole, and the row,
// which shows the tail, as it changes; each at most once per
// previewInterval, the latest on a trailing timer.
func (m *Manager) paceToolOutputLocked(s *webSession, previous, it agentapi.Item) bool {
	key := itemKey(it.AgentID, it.ID)
	state := s.outputs[key]
	if !toolOutputOnly(previous, it) {
		if state != nil {
			if state.timer != nil {
				state.timer.Stop()
			}
			delete(s.outputs, key)
		}
		return false
	}
	if state == nil {
		if s.outputs == nil {
			s.outputs = map[string]*toolOutputState{}
		}
		state = &toolOutputState{}
		s.outputs[key] = state
	}
	now := m.now()
	state.row = state.row || !sameCompactRow(previous, it)
	// While a whole publication is pending the browser's copy is older than
	// previous, so no delta applies to it.
	if state.whole || !strings.HasPrefix(it.Tool.Output, previous.Tool.Output) {
		if state.whole {
			return true
		}
		if state.timer != nil {
			state.timer.Stop()
		}
		if wait := previewInterval - now.Sub(state.at); !state.at.IsZero() && wait > 0 {
			state.whole = true
			m.toolOutputTimerLocked(s, key, state, wait)
			return true
		}
		m.publishToolOutputLocked(s, state, it)
		return true
	}
	suffix := it.Tool.Output[len(previous.Tool.Output):]
	if suffix != "" {
		m.broadcastFilteredLocked("tool_output", s.id, func(sub *Subscriber) bool { return legacySubscriber(sub) && sub.toolDeltas }, func(seq uint64) any {
			return toolOutputEvent{Seq: seq, SessionID: s.id, AgentID: it.AgentID, ItemID: it.ID, Text: suffix}
		})
		m.broadcastFilteredLocked("item", s.id, func(sub *Subscriber) bool { return legacySubscriber(sub) && !sub.toolDeltas }, func(seq uint64) any {
			return itemEvent{Seq: seq, SessionID: s.id, AgentID: it.AgentID, Item: it}
		})
	}
	// The row goes first: the browser holds a body current once a frame
	// reaches the row's sequence.
	if state.row && state.timer == nil {
		if wait := previewInterval - now.Sub(state.rowAt); !state.rowAt.IsZero() && wait > 0 {
			m.toolOutputTimerLocked(s, key, state, wait)
		} else {
			state.rowAt, state.row = now, false
			m.publishCompactItemLocked(s, it, false)
			if suffix == "" {
				// No delta follows to keep the browser's body current, as
				// when the output reached its cap and only the tail moves.
				m.publishBodyCurrentLocked(s, it)
			}
		}
	}
	if suffix != "" {
		m.publishBodyDeltaLocked(s, it, suffix, true)
		m.itemPreviewLocked(s, it)
	}
	return true
}

// toolOutputTimerLocked publishes what state holds back after wait.
func (m *Manager) toolOutputTimerLocked(s *webSession, key string, state *toolOutputState, wait time.Duration) {
	var timer *time.Timer
	timer = time.AfterFunc(wait, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed || m.sessions[s.id] != s || s.outputs[key] != state || state.timer != timer {
			return
		}
		state.timer = nil
		i, ok := s.itemIdx[key]
		if !ok {
			delete(s.outputs, key)
			return
		}
		if state.whole {
			m.publishToolOutputLocked(s, state, s.items[i])
			return
		}
		// The deltas kept the browser's body current; the row alone would
		// raise the sequence it waits for.
		state.rowAt, state.row = m.now(), false
		m.publishCompactItemLocked(s, s.items[i], false)
		m.publishBodyCurrentLocked(s, s.items[i])
	})
	state.timer = timer
}

// publishToolOutputLocked publishes a running tool whole, with its row when
// that changed.
func (m *Manager) publishToolOutputLocked(s *webSession, state *toolOutputState, it agentapi.Item) {
	row := state.row
	state.at, state.timer, state.whole, state.row = m.now(), nil, false, false
	if row {
		state.rowAt = state.at
	}
	m.publishItemLocked(s, it, false, row)
}

// agentItems returns the retained items of one agent ("" for the main
// agent), oldest first.
func (s *webSession) agentItems(agentID string) []agentapi.Item {
	out := []agentapi.Item{}
	for _, it := range s.items {
		if it.AgentID == agentID {
			out = append(out, it)
		}
	}
	return out
}

// applyDeltaLocked appends streamed text, creating the item when absent.
func (m *Manager) applyDeltaLocked(s *webSession, d agentapi.Delta) {
	key := itemKey(d.AgentID, d.ItemID)
	i, ok := s.itemIdx[key]
	if !ok {
		m.upsertItemLocked(s, clampItem(agentapi.Item{ID: d.ItemID, Kind: d.Kind, Text: d.Text, AgentID: d.AgentID}, m.now()), true)
		return
	}
	it := s.items[i]
	room := maxItemText - len(it.Text)
	if room <= 0 || d.Text == "" {
		return
	}
	add := d.Text
	if len(add) > room {
		add = clampText(add, room)
	}
	buffer := s.textBuffers[key]
	if buffer == nil {
		buffer = &strings.Builder{}
		buffer.Grow(len(it.Text) + len(add))
		buffer.WriteString(it.Text)
		if s.textBuffers == nil {
			s.textBuffers = map[string]*strings.Builder{}
		}
		s.textBuffers[key] = buffer
	}
	buffer.WriteString(add)
	it.Text = buffer.String()
	if len(it.Text) >= maxItemText {
		// No more deltas fit. Keep the bounded string without spare builder capacity.
		it.Text = strings.Clone(it.Text)
		delete(s.textBuffers, key)
	}
	s.items[i] = it
	m.markItemMutationLocked(s, it)
	s.itemBytes += len(add)
	removed := s.trimItems()
	m.broadcastFilteredLocked("delta", s.id, func(sub *Subscriber) bool {
		return legacySubscriber(sub) || (it.Kind != agentapi.ItemReasoning && it.Kind != agentapi.ItemTool && compactAgent(it.AgentID)(sub))
	}, func(seq uint64) any {
		return deltaEvent{Seq: seq, SessionID: s.id, AgentID: it.AgentID, ItemID: d.ItemID, Kind: it.Kind, Text: add}
	})
	if it.Kind == agentapi.ItemReasoning || it.Kind == agentapi.ItemTool {
		if len(it.Text) == len(add) {
			m.publishCompactItemLocked(s, it, false)
		}
		m.publishBodyDeltaLocked(s, it, add, false)
	}
	m.itemPreviewLocked(s, it)
	m.publishItemsTrimmedLocked(s, removed)
}

// applyHistoryLocked installs the provider's record of a conversation.
// History defines order; items already known but absent from it (for example
// streamed while it was read) are kept after it. publish sends each item and
// subagent to viewers; without it the caller publishes the result.
func (m *Manager) applyHistoryLocked(s *webSession, history agentapi.History, publish bool) {
	s.subagentsArchived, s.subagentTails = false, nil
	m.applyUsageLocked(s, history.Usage)
	for _, sa := range history.Subagents {
		if sa.ID != "" {
			m.upsertSubagentLocked(s, sa, publish)
		}
	}
	if len(history.Items) == 0 {
		return
	}
	now := m.now()
	items := make([]agentapi.Item, 0, len(history.Items)+len(s.items))
	seen := make(map[string]bool, len(history.Items))
	for _, it := range history.Items {
		key := itemKey(it.AgentID, it.ID)
		if it.ID == "" || seen[key] {
			continue
		}
		seen[key] = true
		delete(s.textBuffers, key)
		if i, ok := s.itemIdx[key]; ok && confirmedSteerReceipt(s.items[i], it) && len(it.Attachments) == 0 {
			it.Attachments = slices.Clone(s.items[i].Attachments)
		}
		it = clampItem(it, now)
		s.linkUploadsLocked(&it)
		items = append(items, it)
	}
	fromHistory := len(items)
	for _, it := range s.items {
		if !seen[itemKey(it.AgentID, it.ID)] {
			items = append(items, it)
		}
	}
	declarations := 0
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Tool == nil || items[i].Tool.Declaration == nil {
			continue
		}
		declarations++
		if declarations > maxDeclarationCards {
			stripped := *items[i].Tool
			stripped.Declaration = nil
			items[i].Tool = &stripped
		}
	}
	s.items = items
	for _, it := range items {
		m.markItemMutationLocked(s, it)
	}
	s.itemBytes = 0
	for _, it := range s.items {
		s.itemBytes += itemSize(it)
	}
	s.rebuildIndex()
	removed := s.trimItems()
	if !publish {
		return
	}
	for _, it := range items[:fromHistory] {
		if _, kept := s.itemIdx[itemKey(it.AgentID, it.ID)]; !kept {
			continue
		}
		m.publishItemLocked(s, it, false, true)
	}
	m.publishItemsTrimmedLocked(s, removed)
}

func (s *webSession) rebuildIndex() {
	s.itemIdx = make(map[string]int, len(s.items))
	for i, it := range s.items {
		s.itemIdx[itemKey(it.AgentID, it.ID)] = i
	}
	for key := range s.itemSeq {
		if _, ok := s.itemIdx[key]; !ok {
			delete(s.itemSeq, key)
		}
	}
	for key := range s.textBuffers {
		if _, ok := s.itemIdx[key]; !ok {
			delete(s.textBuffers, key)
		}
	}
}

// trimItems drops the oldest items once the count or byte budget is
// exceeded. It trims with slack so steady streaming does not reindex on
// every event.
func (s *webSession) trimItems() []trimmedItem {
	drop := 0
	if len(s.items) > maxItems {
		drop = len(s.items) - maxItems*9/10
	}
	if s.itemBytes > maxSessionBytes {
		remaining := s.itemBytes
		for i := range drop {
			remaining -= itemSize(s.items[i])
		}
		for drop < len(s.items)-1 && remaining > maxSessionBytes*9/10 {
			remaining -= itemSize(s.items[drop])
			drop++
		}
	}
	if drop == 0 {
		return nil
	}
	removed := make([]trimmedItem, drop)
	for i := range drop {
		removed[i] = trimmedItem{ID: s.items[i].ID, AgentID: s.items[i].AgentID}
		s.itemBytes -= itemSize(s.items[i])
	}
	s.items = append([]agentapi.Item(nil), s.items[drop:]...)
	s.rebuildIndex()
	s.truncated = true
	return removed
}

func clampInteraction(ix agentapi.Interaction, now time.Time) agentapi.Interaction {
	ix.Plan = clampPlanReview(ix.Plan, ix.State == agentapi.InteractionPending || ix.State == "")
	ix.Title = clampText(ix.Title, maxLabelText)
	ix.Detail = clampText(ix.Detail, maxInteractionText)
	ix.Resolution = clampText(ix.Resolution, maxLabelText)
	ix.Assisted.Recommendation = clampText(ix.Assisted.Recommendation, maxLabelText)
	ix.Assisted.Model = clampText(ix.Assisted.Model, maxLabelText)
	ix.Assisted.Reason = clampText(ix.Assisted.Reason, maxInteractionText)
	if !validToolCallID(ix.ToolCallID) {
		ix.ToolCallID = ""
	}
	ix.Options = slices.Clone(ix.Options)
	ix.Questions = slices.Clone(ix.Questions)
	for i := range ix.Questions {
		ix.Questions[i].Choices = slices.Clone(ix.Questions[i].Choices)
	}
	if ix.State == "" {
		ix.State = agentapi.InteractionPending
	}
	if ix.Time.IsZero() {
		ix.Time = now
	}
	return ix
}

// validToolCallID reports whether a provider's tool call ID may reach a
// browser. The browser matches it to a tool item's ID, so an unfit one is
// dropped, not cleaned: a changed ID would name no tool call.
func validToolCallID(id string) bool {
	return len(id) <= maxToolCallID && utf8.ValidString(id) &&
		!strings.ContainsFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

// upsertInteractionLocked records a provider interaction. A resolved
// interaction never returns to pending.
func (m *Manager) upsertInteractionLocked(s *webSession, in agentapi.Interaction) {
	ix := clampInteraction(in, m.now())
	if sa := s.subIdx[ix.AgentID]; ix.AgentID != "" && ix.State == agentapi.InteractionPending &&
		(s.stoppedSubagents[ix.AgentID] || sa != nil && sa.Status == agentapi.SubagentCancelled) {
		ix.State, ix.Resolution = agentapi.InteractionExpired, "the subagent was stopped"
	}
	cur := s.ixIdx[ix.ID]
	if cur != nil {
		if cur.State != agentapi.InteractionPending && ix.State == agentapi.InteractionPending {
			return
		}
		if cur.yolo && ix.State == agentapi.InteractionAnswered {
			ix.Resolution = autoResolution(cur)
		}
		cur.Interaction = ix
	} else {
		cur = &interaction{Interaction: ix}
		s.interactions = append(s.interactions, cur)
		s.ixIdx[ix.ID] = cur
		s.trimInteractions()
	}
	// Claimed first, so the browser never sees a yolo request wait for the user.
	m.autoAllowLocked(s, cur)
	m.publishInteractionLocked(s, cur)
}

// trimInteractions forgets the oldest resolved interactions beyond the cap.
// Pending ones are kept: forgetting them would hide a request the provider
// still waits on.
func (s *webSession) trimInteractions() {
	excess := len(s.interactions) - maxInteractions
	if excess <= 0 {
		return
	}
	kept := s.interactions[:0]
	for _, ix := range s.interactions {
		if excess > 0 && ix.State != agentapi.InteractionPending {
			delete(s.ixIdx, ix.ID)
			excess--
			continue
		}
		kept = append(kept, ix)
	}
	s.interactions = kept
}

func (m *Manager) publishInteractionLocked(s *webSession, ix *interaction) {
	snapshot := ix.public()
	m.broadcastLocked("interaction", s.id, func(seq uint64) any {
		return interactionEvent{Seq: seq, SessionID: s.id, Interaction: snapshot}
	})
}

func (m *Manager) expireLocked(s *webSession, ix *interaction, reason string) {
	ix.State = agentapi.InteractionExpired
	ix.Plan = clampPlanReview(ix.Plan, false)
	ix.Resolution = reason
	ix.answering = false
	m.publishInteractionLocked(s, ix)
}

// expirePendingLocked ends every pending interaction without an answer.
func (m *Manager) expirePendingLocked(s *webSession, reason string) {
	for _, ix := range s.interactions {
		if ix.State == agentapi.InteractionPending {
			m.expireLocked(s, ix, reason)
		}
	}
}

// expireSubagentLocked ends only this subagent's pending interactions.
func (m *Manager) expireSubagentLocked(s *webSession, agentID string) {
	for _, ix := range s.interactions {
		if ix.AgentID == agentID && ix.State == agentapi.InteractionPending {
			m.expireLocked(s, ix, "the subagent was stopped")
		}
	}
}

// upsertSubagentLocked records a subagent update and, with publish, sends it
// to viewers. A failed or cancelled subagent never changes again, and a
// completed one becomes idle when it takes follow-ups or running when the
// provider reports a new start after its previous end. Duplicate end events
// and stale starts stay ignored (Copilot sends a cancelled end on disconnect).
func (m *Manager) upsertSubagentLocked(s *webSession, in agentapi.Subagent, publish bool) {
	if in.Status == "" {
		in.Status = agentapi.SubagentRunning
	}
	if in.Status != agentapi.SubagentRunning && in.Status != agentapi.SubagentIdle && !in.Status.Terminal() {
		return
	}
	in = clampSubagent(in)
	cur := s.subIdx[in.ID]
	if cur == nil {
		cur = &agentapi.Subagent{}
		s.subagents = append(s.subagents, cur)
		s.subIdx[in.ID] = cur
	} else if cur.Status.Terminal() && (cur.Status != agentapi.SubagentCompleted ||
		in.Status != agentapi.SubagentIdle && (in.Status != agentapi.SubagentRunning || !in.StartedAt.After(cur.EndedAt))) {
		return
	} else {
		// An end event may omit what the start event said.
		in.ParentToolCallID = cmp.Or(in.ParentToolCallID, cur.ParentToolCallID)
		in.ParentAgentID = cmp.Or(in.ParentAgentID, cur.ParentAgentID)
		in.Name = cmp.Or(in.Name, cur.Name)
		in.Description = cmp.Or(in.Description, cur.Description)
		in.Model = cmp.Or(in.Model, cur.Model)
		in.Result = cmp.Or(in.Result, cur.Result)
		in.Background = in.Background || cur.Background
		// The provider's totals replace the live counts; a record rebuilt
		// from recorded events may not know them.
		in.Tokens, in.ToolCalls = cmp.Or(in.Tokens, cur.Tokens), cmp.Or(in.ToolCalls, cur.ToolCalls)
		if in.StartedAt.IsZero() {
			in.StartedAt = cur.StartedAt
		}
		if len(in.Runs) == 0 {
			in.Runs = cur.Runs
			in = in.Snapshot()
		} else {
			in.Runs = mergeRuns(cur.Runs, in.Runs)
		}
	}
	*cur = in
	if cur.Status == agentapi.SubagentCancelled {
		m.expireSubagentLocked(s, cur.ID)
	}
	s.trimSubagents()
	if publish {
		m.publishSubagentLocked(s, cur)
	}
}

// mergeRuns joins the known runs and an update's by start time. The update
// may know fewer, as a record rebuilt from recorded events does; its version
// of a run both know is the newer one.
func mergeRuns(known, in []agentapi.SubagentRun) []agentapi.SubagentRun {
	if len(known) == 0 {
		return in
	}
	out := make([]agentapi.SubagentRun, 0, len(known)+len(in))
	for i, j := 0, 0; i < len(known) || j < len(in); {
		switch {
		case j == len(in) || i < len(known) && known[i].StartedAt.Before(in[j].StartedAt):
			out = append(out, known[i])
			i++
		case i == len(known) || in[j].StartedAt.Before(known[i].StartedAt):
			out = append(out, in[j])
			j++
		default:
			out = append(out, in[j])
			i, j = i+1, j+1
		}
	}
	return agentapi.CapSubagentRuns(out)
}

// clampSubagent bounds and sanitizes a provider's subagent record.
func clampSubagent(in agentapi.Subagent) agentapi.Subagent {
	in.Name = clampText(displaytext.Sanitize(in.Name), maxLabelText)
	in.Description = clampText(displaytext.Sanitize(in.Description), maxLabelText)
	in.Model = clampText(displaytext.Sanitize(in.Model), maxLabelText)
	in.Effort = clampText(displaytext.Sanitize(in.Effort), maxLabelText)
	in.Error = clipRunes(displaytext.Sanitize(in.Error), maxDetailRunes)
	in.ParentToolCallID = clampText(in.ParentToolCallID, maxLabelText)
	in.ParentAgentID = clampText(in.ParentAgentID, maxLabelText)
	in.Result = boundedResultSummary(in.Result)
	in.Runs = agentapi.CapSubagentRuns(in.Runs)
	// A retry is news only while it runs.
	if in.Retry != nil && in.Status == agentapi.SubagentRunning {
		r := cleanRetry(*in.Retry)
		in.Retry = &r
	} else {
		in.Retry = nil
	}
	return in
}

// subagentList returns a copy of s's subagent records.
func (s *webSession) subagentList() []agentapi.Subagent {
	out := make([]agentapi.Subagent, 0, len(s.subagents))
	for _, sa := range s.subagents {
		out = append(out, *sa)
	}
	return out
}

func (m *Manager) publishSubagentLocked(s *webSession, sa *agentapi.Subagent) {
	snapshot := *sa
	m.broadcastFilteredLocked("subagent", s.id, legacySubscriber, func(seq uint64) any {
		return subagentEvent{Seq: seq, SessionID: s.id, Subagent: snapshot}
	})
	m.queueSubagentPreviewLocked(s, sa.ID, true)
}

// endSubagentsLocked marks every running subagent cancelled and every idle
// one completed when its conversation ends: nothing of it runs once the
// conversation is closed, and only a live provider says one takes follow-ups.
func (m *Manager) endSubagentsLocked(s *webSession) {
	for _, sa := range s.subagents {
		switch sa.Status {
		case agentapi.SubagentRunning:
			sa.Status, sa.EndedAt = agentapi.SubagentCancelled, m.now()
		case agentapi.SubagentIdle:
			sa.Status = agentapi.SubagentCompleted
		default:
			continue
		}
		*sa = sa.Snapshot()
		m.publishSubagentLocked(s, sa)
	}
}

func (s *webSession) runningSubagents() int {
	n := 0
	for _, sa := range s.subagents {
		if sa.Status == agentapi.SubagentRunning {
			n++
		}
	}
	return n
}

// runningBackgroundTasks counts the background shells of s's open
// conversation last reported as not finished. A closed conversation runs
// none, whatever its last snapshot said.
func (s *webSession) runningBackgroundTasks() int {
	if s.conv == nil || s.backgroundTasks == nil {
		return 0
	}
	n := 0
	for _, task := range s.backgroundTasks.Tasks {
		if task.Status != "completed" && task.Status != "failed" && task.Status != "cancelled" {
			n++
		}
	}
	return n
}

func (m *Manager) backgroundTasksLocked(s *webSession, snapshot agentapi.BackgroundTasks) {
	snapshot.Tasks = slices.Clone(snapshot.Tasks)
	for i := range snapshot.Tasks {
		task := &snapshot.Tasks[i]
		task.Command = clampText(displaytext.Sanitize(task.Command), maxToolText)
		task.Description = clampText(displaytext.Sanitize(task.Description), maxLabelText)
	}
	s.backgroundTasks = &snapshot
	m.broadcastLocked("background_tasks", s.id, func(seq uint64) any {
		return backgroundTasksEvent{Seq: seq, SessionID: s.id, BackgroundTasks: snapshot}
	})
}

// schedulesLocked replaces s's native schedule snapshot with a bounded,
// sanitized copy of snapshot, or releases it (nil), and publishes it.
func (m *Manager) schedulesLocked(s *webSession, snapshot *agentapi.ScheduleSnapshot) {
	if snapshot != nil {
		own := *snapshot
		own.Reason = clampText(displaytext.Sanitize(own.Reason), maxLabelText)
		if len(own.Entries) > agentapi.MaxSchedules {
			own.Known, own.Truncated = false, true
		}
		own.Entries = slices.Clone(own.Entries[:min(len(own.Entries), agentapi.MaxSchedules)])
		if own.Entries == nil {
			own.Entries = []agentapi.ScheduleEntry{}
		}
		for i := range own.Entries {
			entry := &own.Entries[i]
			entry.ID = clampText(displaytext.Sanitize(entry.ID), maxLabelText)
			entry.Cron = clampText(displaytext.Sanitize(entry.Cron), maxLabelText)
			entry.Timezone = clampText(displaytext.Sanitize(entry.Timezone), maxLabelText)
		}
		snapshot = &own
	}
	s.schedules = snapshot
	m.broadcastLocked("schedules", s.id, func(seq uint64) any {
		return schedulesEvent{Seq: seq, SessionID: s.id, Schedules: snapshot}
	})
}

func (m *Manager) forgetBackgroundTaskStateLocked(s *webSession) {
	m.forgetMCPStatusLocked(s)
	m.forgetTurnTimingLocked(s)
	m.forgetTodosLocked(s)
	if s.schedules != nil {
		m.schedulesLocked(s, nil)
	}
	if s.execution != nil {
		state := *s.execution
		state.Known = false
		s.execution = &state
	}
	if s.backgroundTasks != nil && s.backgroundTasks.Known {
		snapshot := *s.backgroundTasks
		snapshot.Known = false
		m.backgroundTasksLocked(s, snapshot)
	}
}

// trimSubagents forgets the oldest finished subagents beyond the cap. The
// newest record stays, so every forgotten one is followed by a held one:
// the list head, before which the record is paged.
func (s *webSession) trimSubagents() {
	excess := len(s.subagents) - maxSubagents
	if excess <= 0 {
		return
	}
	head, dropped := s.subagentHead, 0
	kept := s.subagents[:0]
	for i, sa := range s.subagents {
		if excess > 0 && sa.Status.Terminal() && i < len(s.subagents)-1 {
			head, dropped = max(head, i+1), dropped+1
			delete(s.subIdx, sa.ID)
			if state := s.previews[sa.ID]; state != nil {
				if state.timer != nil {
					state.timer.Stop()
				}
				delete(s.previews, sa.ID)
			}
			excess--
			continue
		}
		kept = append(kept, sa)
	}
	s.subagents = kept
	if dropped > 0 {
		// Every forgotten record came before head.
		s.subagentHead, s.subagentsOlder = head-dropped, true
	}
}

// validateAnswer checks an answer against what the provider offered, so an
// invalid answer never reaches the provider.
func validateAnswer(ix agentapi.Interaction, a agentapi.Answer) error {
	switch ix.Kind {
	case agentapi.InteractionPermission:
		if a.Reject || len(a.Answers) > 0 || a.Plan != nil {
			return newError(http.StatusBadRequest, "a permission request takes a decision only")
		}
		for _, opt := range ix.Options {
			if opt.ID == a.Decision {
				return nil
			}
		}
		return newError(http.StatusBadRequest, "decision must be one of the offered options")
	case agentapi.InteractionQuestion:
		if a.Decision != "" || a.Plan != nil {
			return newError(http.StatusBadRequest, "a question takes answers, not a decision")
		}
		if a.Reject {
			if len(a.Answers) > 0 {
				return newError(http.StatusBadRequest, "a rejected question takes no answers")
			}
			return nil
		}
		if len(a.Answers) != len(ix.Questions) {
			return newError(http.StatusBadRequest, "answers must have one entry per question (%d)", len(ix.Questions))
		}
		for i, q := range ix.Questions {
			if err := validateQuestionAnswer(i, q, a.Answers[i]); err != nil {
				return err
			}
		}
		return nil
	case agentapi.InteractionPlanReview:
		if a.Plan == nil || ix.Plan == nil || a.Decision != "" || len(a.Answers) > 0 || a.Reject || a.Auto {
			return newError(http.StatusBadRequest, "a plan review takes an explicit plan answer only")
		}
		plan := a.Plan
		if plan.Action != "" && plan.Feedback != "" || plan.Action == "" && strings.TrimSpace(plan.Feedback) == "" || len(plan.Feedback) > agentapi.MaxPlanFeedbackBytes {
			return newError(http.StatusBadRequest, "a plan review takes one action or bounded feedback")
		}
		if plan.Action != "" && (!slices.Contains(ix.Plan.Actions, plan.Action) || ix.Plan.Truncated) {
			return newError(http.StatusBadRequest, "plan action must be offered and the reviewed snapshot complete")
		}
		return nil
	default:
		return newError(http.StatusBadRequest, "unknown interaction kind")
	}
}

func validateQuestionAnswer(i int, q agentapi.Question, values []string) error {
	if len(values) == 0 {
		return newError(http.StatusBadRequest, "question %d needs an answer", i+1)
	}
	if !q.Multiple && len(values) > 1 {
		return newError(http.StatusBadRequest, "question %d accepts one answer", i+1)
	}
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return newError(http.StatusBadRequest, "question %d has an empty answer", i+1)
		}
		if len(v) > maxAnswerBytes {
			return newError(http.StatusBadRequest, "question %d answer is too long", i+1)
		}
		if !q.Custom && !slices.Contains(q.Choices, v) {
			return newError(http.StatusBadRequest, "question %d only accepts the listed choices", i+1)
		}
	}
	return nil
}

func resolution(ix agentapi.Interaction, a agentapi.Answer) (agentapi.InteractionState, string) {
	if ix.Kind == agentapi.InteractionPlanReview && a.Plan != nil {
		if a.Plan.Action != "" {
			return agentapi.InteractionAnswered, string(a.Plan.Action)
		}
		return agentapi.InteractionAnswered, "feedback sent"
	}
	if ix.Kind == agentapi.InteractionPermission {
		for _, opt := range ix.Options {
			if opt.ID == a.Decision {
				if opt.Reject {
					return agentapi.InteractionRejected, opt.Label
				}
				return agentapi.InteractionAnswered, opt.Label
			}
		}
	}
	if a.Reject {
		return agentapi.InteractionRejected, "declined"
	}
	return agentapi.InteractionAnswered, "answered"
}
