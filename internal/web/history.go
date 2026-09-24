package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const (
	maxHistoryBytes       = 16 << 20
	maxCachedHistoryBytes = 64 << 20
	historySlotTimeout    = 5 * time.Second
)

// historyState is s's reported transcript state. Nothing asked for it yet
// means its conversation is opening, and the open reads the record.
func (s *webSession) historyState() string { return cmp.Or(s.history, HistoryLoading) }

// viewHistoryLocked records that a viewer asked for s's transcript and, when
// the conversation is neither open nor opening and the transcript is not in
// memory, starts reading it without opening the conversation. Nothing is
// sent and the Task stays as it is: a failed read only reports why. A failed
// read is tried again once historyRetry passed.
func (m *Manager) viewHistoryLocked(s *webSession) {
	now := m.now()
	s.historyUsed = now
	switch {
	case m.closed, s.removed, s.conv != nil, s.opening != nil, s.history == HistoryLoaded, s.history == HistoryLoading,
		s.history == HistoryUnavailable && now.Sub(s.historyFailed) < historyRetry:
		return
	}
	prov := m.providers[s.provider]
	reader, _ := prov.(agentapi.HistoryReader)
	switch {
	case prov == nil:
		m.historyUnavailableLocked(s, fmt.Sprintf("provider %q is not available in this service", s.provider))
		return
	case reader == nil:
		m.historyUnavailableLocked(s, "this provider cannot show the transcript without opening the conversation")
		return
	case s.convID == "":
		m.historyUnavailableLocked(s, "this task has no provider conversation")
		return
	}
	s.history, s.historyReason = HistoryLoading, ""
	convID, workdir := s.convID, s.workdir
	ctx, cancel := context.WithCancel(m.ctx)
	s.historyCancel = cancel
	s.historyGen++
	gen := s.historyGen
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		h, err := m.readHistory(ctx, reader, convID, workdir)
		if err == nil {
			m.mu.Lock()
			current := !s.removed && s.historyGen == gen && s.history == HistoryLoading
			m.mu.Unlock()
			if current {
				for i := range h.Items {
					if h.Items[i].Kind == agentapi.ItemTool {
						m.keepImages(s, &h.Items[i])
					}
				}
			}
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		// An open that finished meanwhile installed its own record.
		if s.removed || s.historyGen != gen || s.history != HistoryLoading {
			return
		}
		if err != nil {
			log.Warn("read web task transcript failed", "session", s.id, "error", err)
			m.historyUnavailableLocked(s, historyFailure(err, convID))
			return
		}
		m.installHistoryLocked(s, h)
	}()
}

func historyFailure(err error, convID string) string {
	if errors.Is(err, agentapi.ErrConversationNotFound) {
		return fmt.Sprintf("provider conversation %s no longer exists", convID)
	}
	return "could not read the recorded transcript: " + shortError(err)
}

// readHistory reads a conversation's record through reader, at most
// maxHistoryReads at a time.
func (m *Manager) readHistory(ctx context.Context, reader agentapi.HistoryReader, convID, workdir string) (agentapi.History, error) {
	waitCtx, waitCancel := context.WithTimeout(ctx, historySlotTimeout)
	defer waitCancel()
	select {
	case m.reads <- struct{}{}:
	case <-waitCtx.Done():
		return agentapi.History{}, newError(http.StatusServiceUnavailable, "history readers are busy or the request was cancelled; try again")
	}
	defer func() { <-m.reads }()
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	return reader.ReadHistory(ctx, agentapi.ReadRequest{ConversationID: convID, Workdir: workdir})
}

// installHistoryLocked installs a record read without opening the
// conversation and sends it to viewers in one history event. Nothing of a
// conversation that is not open runs, so a subagent the record leaves running
// shows as cancelled, as it does when a conversation closes.
func (m *Manager) installHistoryLocked(s *webSession, h agentapi.History) {
	before := m.summaryLocked(s)
	for i := range h.Subagents {
		switch h.Subagents[i].Status {
		case agentapi.SubagentRunning:
			h.Subagents[i].Status = agentapi.SubagentCancelled
		case agentapi.SubagentIdle:
			h.Subagents[i].Status = agentapi.SubagentCompleted
		}
	}
	m.applyHistoryLocked(s, h, false)
	if h.Truncated {
		s.truncated = true
	}
	s.history, s.historyReason, s.historyRead = HistoryLoaded, "", true
	m.publishHistoryLocked(s)
	if m.sessions[s.id] == s {
		m.enforceHistoryBudgetLocked(s)
	}
	m.changedLocked(s, before)
}

// historyUnavailableLocked records why s's transcript could not be read and
// tells viewers.
func (m *Manager) historyUnavailableLocked(s *webSession, reason string) {
	s.history, s.historyReason, s.historyFailed = HistoryUnavailable, reason, m.now()
	m.publishHistoryLocked(s)
}

// openedHistoryLocked records the transcript state once s's conversation
// opened: the open read the provider's record, or could not, in which case a
// record already in memory still counts. The items are live from now on and
// are no longer dropped when idle.
func (m *Manager) openedHistoryLocked(s *webSession, withHistory bool, err error) {
	switch {
	case withHistory && err == nil:
		s.history, s.historyReason = HistoryLoaded, ""
	case s.history == HistoryLoaded:
	case withHistory:
		s.history, s.historyReason = HistoryUnavailable, "could not read the recorded transcript: "+shortError(err)
	default:
		s.history, s.historyReason = HistoryUnavailable, "this provider does not report the recorded transcript"
	}
	s.historyRead = false
	m.publishHistoryLocked(s)
}

// publishHistoryLocked sends s's transcript state, with its main-agent items
// and its subagents, to the viewers of s.
func (m *Manager) publishHistoryLocked(s *webSession) {
	m.boundHistoryLocked(s)
	m.broadcastLocked("history", s.id, func(seq uint64) any {
		return historyEvent{Seq: seq, SessionID: s.id, History: s.historyState(), HistoryReason: s.historyReason,
			Truncated: s.truncated, Items: s.agentItems(""), Subagents: s.subagentList()}
	})
}

// evictHistories drops the transcripts read without opening their
// conversation that no viewer asked for within historyIdle and no event
// stream watches. The next view reads them again.
func (m *Manager) evictHistories() {
	m.mu.Lock()
	defer m.mu.Unlock()
	watched := map[string]bool{}
	for sub := range m.subs {
		watched[sub.session] = true
	}
	cutoff := m.now().Add(-historyIdle)
	for _, s := range m.sessions {
		if !s.historyRead || s.conv != nil || s.opening != nil || watched[s.id] || s.historyUsed.After(cutoff) {
			continue
		}
		m.dropHistoryLocked(s)
	}
}

// Bound encoded items, including JSON escaping, so a history event stays well
// below the subscriber's 32 MiB queue cap. Keep the newest complete items.
func (m *Manager) boundHistoryLocked(s *webSession) {
	bytesKept, subStart := 0, len(s.subagents)
	for subStart > 0 {
		encoded, err := json.Marshal(s.subagents[subStart-1])
		if err != nil || bytesKept+len(encoded)+1 > maxHistoryBytes/4 {
			break
		}
		bytesKept += len(encoded) + 1
		subStart--
	}
	if subStart > 0 {
		s.subagents = append([]*agentapi.Subagent(nil), s.subagents[subStart:]...)
		s.subIdx = make(map[string]*agentapi.Subagent, len(s.subagents))
		for _, sa := range s.subagents {
			s.subIdx[sa.ID] = sa
		}
		s.truncated = true
	}
	start := len(s.items)
	for start > 0 {
		encoded, err := json.Marshal(s.items[start-1])
		if err != nil || bytesKept+len(encoded)+1 > maxHistoryBytes {
			break
		}
		bytesKept += len(encoded) + 1
		start--
	}
	s.historyBytes = bytesKept
	if start == 0 {
		return
	}
	s.items = append([]agentapi.Item(nil), s.items[start:]...)
	s.itemBytes = 0
	for _, it := range s.items {
		s.itemBytes += itemSize(it)
	}
	s.rebuildIndex()
	s.truncated = true
}

func (m *Manager) cancelHistoryLocked(s *webSession) {
	if s.historyCancel != nil {
		s.historyCancel()
		s.historyCancel = nil
		s.historyGen++
		if s.history == HistoryLoading {
			s.history = ""
		}
	}
}

func (m *Manager) dropHistoryLocked(s *webSession) {
	s.items, s.itemIdx, s.itemBytes, s.truncated = nil, map[string]int{}, 0, false
	s.subagents, s.subIdx = nil, map[string]*agentapi.Subagent{}
	s.history, s.historyReason, s.historyRead, s.historyBytes = "", "", false, 0
}

// Drop the least recently viewed cached histories until the total fits. Open
// conversations are live state and are governed by their own existing limit.
func (m *Manager) enforceHistoryBudgetLocked(current *webSession) {
	for {
		total := current.historyBytes
		var oldest *webSession
		for _, s := range m.sessions {
			if s == current || !s.historyRead || s.conv != nil || s.opening != nil {
				continue
			}
			total += s.historyBytes
			if oldest == nil || s.historyUsed.Before(oldest.historyUsed) {
				oldest = s
			}
		}
		if total <= maxCachedHistoryBytes || oldest == nil {
			return
		}
		m.dropHistoryLocked(oldest)
	}
}
