package web

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"time"
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

func (m *Manager) publishItemLocked(s *webSession, it agentapi.Item) {
	m.broadcastLocked("item", s.id, func(seq uint64) any {
		return itemEvent{Seq: seq, SessionID: s.id, AgentID: it.AgentID, Item: it}
	})
}

// upsertItemLocked replaces the item with the same agent and ID or appends
// it.
func (m *Manager) upsertItemLocked(s *webSession, it agentapi.Item, publish bool) {
	if i, ok := s.itemIdx[itemKey(it.AgentID, it.ID)]; ok {
		s.itemBytes -= itemSize(s.items[i])
		s.items[i] = it
	} else {
		s.itemIdx[itemKey(it.AgentID, it.ID)] = len(s.items)
		s.items = append(s.items, it)
	}
	s.itemBytes += itemSize(it)
	s.trimItems()
	if publish {
		m.publishItemLocked(s, it)
	}
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
	s.itemBytes += len(add)
	s.trimItems()
	m.broadcastLocked("delta", s.id, func(seq uint64) any {
		return deltaEvent{Seq: seq, SessionID: s.id, AgentID: it.AgentID, ItemID: d.ItemID, Kind: it.Kind, Text: add}
	})
}

// applyHistoryLocked installs the provider's record of a reopened
// conversation. History defines order; items already known but absent from
// it (for example streamed while it was read) are kept after it.
func (m *Manager) applyHistoryLocked(s *webSession, history agentapi.History) {
	for _, sa := range history.Subagents {
		if sa.ID != "" {
			m.upsertSubagentLocked(s, sa)
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
	s.itemBytes = 0
	for _, it := range s.items {
		s.itemBytes += itemSize(it)
	}
	s.rebuildIndex()
	s.trimItems()
	for _, it := range items[:fromHistory] {
		if _, kept := s.itemIdx[itemKey(it.AgentID, it.ID)]; !kept {
			continue
		}
		m.publishItemLocked(s, it)
	}
}

func (s *webSession) rebuildIndex() {
	s.itemIdx = make(map[string]int, len(s.items))
	for i, it := range s.items {
		s.itemIdx[itemKey(it.AgentID, it.ID)] = i
	}
}

// trimItems drops the oldest items once the count or byte budget is
// exceeded. It trims with slack so steady streaming does not reindex on
// every event.
func (s *webSession) trimItems() {
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
		return
	}
	for i := range drop {
		s.itemBytes -= itemSize(s.items[i])
	}
	s.items = append([]agentapi.Item(nil), s.items[drop:]...)
	s.rebuildIndex()
	s.truncated = true
}

func clampInteraction(ix agentapi.Interaction, now time.Time) agentapi.Interaction {
	ix.Title = clampText(ix.Title, maxLabelText)
	ix.Detail = clampText(ix.Detail, maxInteractionText)
	ix.Resolution = clampText(ix.Resolution, maxLabelText)
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
	m.publishInteractionLocked(s, cur)
	m.autoAllowLocked(s, cur)
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
	snapshot := ix.Interaction
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

// upsertSubagentLocked records a subagent update. A failed or cancelled
// subagent never changes again, and a completed one only becomes idle when
// the provider reports that it takes a follow-up: providers may report the
// end more than once (Copilot sends a second, cancelled completion when a
// client disconnects).
func (m *Manager) upsertSubagentLocked(s *webSession, in agentapi.Subagent) {
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
	m.publishSubagentLocked(s, cur)
}

func (m *Manager) publishSubagentLocked(s *webSession, sa *agentapi.Subagent) {
	snapshot := *sa
	m.broadcastLocked("subagent", s.id, func(seq uint64) any {
		return subagentEvent{Seq: seq, SessionID: s.id, Subagent: snapshot}
	})
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
