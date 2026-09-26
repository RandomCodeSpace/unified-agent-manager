package web

import (
	"cmp"
	"net/http"
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
	maxItems           = 2000
	maxItemText        = 4 << 20
	maxToolText        = 256 << 10
	maxLabelText       = 4 << 10
	maxSessionBytes    = 64 << 20
	maxInteractions    = 200
	maxInteractionText = 256 << 10
	maxAnswerBytes     = 64 << 10
	maxSubagents       = 200
	maxItemAttachments = 50
	maxToolCallID      = 256
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

func clampItem(it agentapi.Item, now time.Time) agentapi.Item {
	it.Text = clampText(it.Text, maxItemText)
	if it.Tool != nil {
		tool := *it.Tool
		tool.Name = clampText(tool.Name, maxLabelText)
		tool.Title = clampText(tool.Title, maxLabelText)
		tool.Input = clampText(tool.Input, maxToolText)
		tool.Output = clampText(tool.Output, maxToolText)
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
	if it.Tool != nil {
		n += len(it.Tool.Name) + len(it.Tool.Title) + len(it.Tool.Input) + len(it.Tool.Output)
	}
	return n
}

// itemKey indexes an item: item IDs are unique per agent only. The main
// agent and every subagent share one retained list and its bounds.
func itemKey(agentID, id string) string {
	if agentID == "" {
		return id
	}
	return agentID + "\x00" + id
}

func (m *Manager) publishItemLocked(s *webSession, it agentapi.Item, appendItem bool) {
	m.broadcastFilteredLocked("item", s.id, legacySubscriber, func(seq uint64) any {
		return itemEvent{Seq: seq, SessionID: s.id, AgentID: it.AgentID, Item: it, Append: appendItem}
	})
	m.publishCompactItemLocked(s, it, appendItem)
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

// upsertItemLocked replaces the item with the same agent and ID or appends
// it. A replacement keeps the earliest start: a tool's completion event is
// stamped when it finished, not when it began.
func (m *Manager) upsertItemLocked(s *webSession, it agentapi.Item, publish bool) {
	var previous agentapi.Item
	if i, ok := s.itemIdx[itemKey(it.AgentID, it.ID)]; ok {
		previous = s.items[i]
		if prev := s.items[i].Time; !prev.IsZero() && prev.Before(it.Time) {
			it.Time = prev
		}
		s.itemBytes -= itemSize(s.items[i])
		s.items[i] = it
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
		if suffix, ok := toolOutputSuffix(previous, it); ok {
			if suffix == "" {
				return
			}
			m.broadcastFilteredLocked("tool_output", s.id, func(sub *Subscriber) bool { return legacySubscriber(sub) && sub.toolDeltas }, func(seq uint64) any {
				return toolOutputEvent{Seq: seq, SessionID: s.id, AgentID: it.AgentID, ItemID: it.ID, Text: suffix}
			})
			m.broadcastFilteredLocked("item", s.id, func(sub *Subscriber) bool { return legacySubscriber(sub) && !sub.toolDeltas }, func(seq uint64) any {
				return itemEvent{Seq: seq, SessionID: s.id, AgentID: it.AgentID, Item: it}
			})
			if previous.Tool.Output == "" {
				m.publishCompactItemLocked(s, it, false)
			}
			m.publishBodyDeltaLocked(s, it, suffix, true)
			m.itemPreviewLocked(s, it)
			return
		}
		m.publishItemLocked(s, it, previous.ID == "")
	}
}

// Only append-only output with otherwise identical metadata can be a delta.
// Starts, rewrites, status changes, images and completion remain full items.
func toolOutputSuffix(previous, next agentapi.Item) (string, bool) {
	if previous.Tool == nil || next.Tool == nil || next.Kind != agentapi.ItemTool || next.Tool.Status != agentapi.ToolRunning || !strings.HasPrefix(next.Tool.Output, previous.Tool.Output) {
		return "", false
	}
	output := previous.Tool.Output
	tool := *previous.Tool
	tool.Output = next.Tool.Output
	previous.Tool = &tool
	if !reflect.DeepEqual(previous, next) {
		return "", false
	}
	return next.Tool.Output[len(output):], true
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
	i, ok := s.itemIdx[itemKey(d.AgentID, d.ItemID)]
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
	it.Text += add
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
		m.publishItemLocked(s, it, false)
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
	ix.Title = clampText(ix.Title, maxLabelText)
	ix.Detail = clampText(ix.Detail, maxInteractionText)
	ix.Resolution = clampText(ix.Resolution, maxLabelText)
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
			ix.Resolution = yoloResolution
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
// completed one only becomes idle when the provider reports that it takes a
// follow-up: providers may report the end more than once (Copilot sends a
// second, cancelled completion when a client disconnects).
func (m *Manager) upsertSubagentLocked(s *webSession, in agentapi.Subagent, publish bool) {
	if in.Status == "" {
		in.Status = agentapi.SubagentRunning
	}
	if in.Status != agentapi.SubagentRunning && in.Status != agentapi.SubagentIdle && !in.Status.Terminal() {
		return
	}
	in.Name = clampText(displaytext.Sanitize(in.Name), maxLabelText)
	in.Description = clampText(displaytext.Sanitize(in.Description), maxLabelText)
	in.Model = clampText(displaytext.Sanitize(in.Model), maxLabelText)
	in.Effort = clampText(displaytext.Sanitize(in.Effort), maxLabelText)
	in.Error = clipRunes(displaytext.Sanitize(in.Error), maxDetailRunes)
	in.ParentToolCallID = clampText(in.ParentToolCallID, maxLabelText)
	cur := s.subIdx[in.ID]
	if cur == nil {
		cur = &agentapi.Subagent{}
		s.subagents = append(s.subagents, cur)
		s.subIdx[in.ID] = cur
	} else if cur.Status.Terminal() && (cur.Status != agentapi.SubagentCompleted || in.Status != agentapi.SubagentIdle) {
		return
	} else {
		// An end event may omit what the start event said.
		in.ParentToolCallID = cmp.Or(in.ParentToolCallID, cur.ParentToolCallID)
		in.Name = cmp.Or(in.Name, cur.Name)
		in.Description = cmp.Or(in.Description, cur.Description)
		in.Model = cmp.Or(in.Model, cur.Model)
		if in.StartedAt.IsZero() {
			in.StartedAt = cur.StartedAt
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

func (m *Manager) forgetBackgroundTaskStateLocked(s *webSession) {
	m.forgetTurnTimingLocked(s)
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

// trimSubagents forgets the oldest finished subagents beyond the cap.
func (s *webSession) trimSubagents() {
	excess := len(s.subagents) - maxSubagents
	if excess <= 0 {
		return
	}
	kept := s.subagents[:0]
	for _, sa := range s.subagents {
		if excess > 0 && sa.Status.Terminal() {
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
}

// validateAnswer checks an answer against what the provider offered, so an
// invalid answer never reaches the provider.
func validateAnswer(ix agentapi.Interaction, a agentapi.Answer) error {
	switch ix.Kind {
	case agentapi.InteractionPermission:
		if a.Reject || len(a.Answers) > 0 {
			return newError(http.StatusBadRequest, "a permission request takes a decision only")
		}
		for _, opt := range ix.Options {
			if opt.ID == a.Decision {
				return nil
			}
		}
		return newError(http.StatusBadRequest, "decision must be one of the offered options")
	case agentapi.InteractionQuestion:
		if a.Decision != "" {
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
