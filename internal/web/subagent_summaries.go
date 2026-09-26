package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const (
	maxSummaryInputRunes = 4000
	maxSummaryRunes      = 160
	maxSummaryBytes      = 512
)

// A run exists only after a live Running event, never from a history read.
// Replacing its pointer on a follow-up invalidates queued and late replies.
type subagentSummaryRun struct {
	followup                bool
	lastAssistantID         string // observed complete live item; replay never updates it
	resultID, resultAgentID string
	attempted               subagentResultIdentity
	cancel                  context.CancelFunc
}

// Complete items, not tokens or status repeats, identify new candidates.
// Providers do not expose an authoritative follow-up-final fence: a later
// complete item can supersede an already-started candidate.
type subagentResultIdentity struct {
	itemID, agentID, digest string
}

type subagentSummaryJob struct {
	sessionID string
	agentID   string
	run       *subagentSummaryRun
	ctx       context.Context
	cancel    context.CancelFunc
	identity  subagentResultIdentity
	provider  agentapi.SubagentSummarizer
	request   agentapi.SubagentSummaryRequest
	record    store.SubagentSummary
}

func summaryResult(text string) string {
	return strings.TrimSpace(displaytext.Sanitize(clipRunes(text, maxSummaryInputRunes-1)))
}

func summaryDigest(text string) string {
	digest := sha256.Sum256([]byte(summaryResult(text)))
	return hex.EncodeToString(digest[:])
}

func cleanGeneratedSummary(reply string) string {
	reply = thinkRE.ReplaceAllString(clipRunes(reply, maxSummaryInputRunes), "")
	// Clipping can remove a closing tag. Unfinished reasoning is not a result.
	if strings.Contains(strings.ToLower(reply), "<think>") {
		return ""
	}
	for line := range strings.Lines(reply) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimSpace(strings.Trim(line, "\"'`*_ "))
		line = spacesRE.ReplaceAllString(displaytext.Sanitize(line), " ")
		return boundedPreview(clipRunes(line, maxSummaryRunes-1), maxSummaryBytes)
	}
	return ""
}

func (m *Manager) subagentSummaryStatusLocked(s *webSession, id string, previous agentapi.SubagentStatus) {
	sa := s.subIdx[id]
	if sa == nil {
		return
	}
	if sa.Status == agentapi.SubagentRunning && previous != agentapi.SubagentRunning {
		if old := s.summaryRuns[id]; old != nil && old.cancel != nil {
			old.cancel()
		}
		// Retain at most the existing child limit, including runs whose history
		// was trimmed. Providers can temporarily exceed that limit with live agents.
		for key, run := range s.summaryRuns {
			if s.subIdx[key] == nil {
				if run.cancel != nil {
					run.cancel()
				}
				delete(s.summaryRuns, key)
			}
		}
		_, hadSummary := s.subagentSummaries[id]
		if hadSummary {
			delete(s.subagentSummaries, id)
			s.summaryRevision++
		}
		if len(s.summaryRuns) < maxSubagents || s.summaryRuns[id] != nil {
			s.summaryRuns[id] = &subagentSummaryRun{followup: previous != "" || hadSummary}
		}
		return
	}
	if sa.Status == agentapi.SubagentFailed || sa.Status == agentapi.SubagentCancelled {
		if run := s.summaryRuns[id]; run != nil && run.cancel != nil {
			run.cancel()
		}
		return
	}
	m.queueSubagentSummaryLocked(s, sa)
}

// Completion and the final parent task-tool output can arrive in either
// order. Only complete item events supply input; previews and deltas do not.
func (m *Manager) subagentSummaryItemLocked(s *webSession, it agentapi.Item) {
	if it.AgentID != "" {
		run := s.summaryRuns[it.AgentID]
		sa := s.subIdx[it.AgentID]
		if run == nil || sa == nil || it.Kind != agentapi.ItemAssistant {
			return
		}
		// Repeated complete items keep their earlier transcript position.
		if it.ID == s.lastSubagentAssistant(it.AgentID) {
			run.lastAssistantID = it.ID
		}
		if run.followup || sa.ParentToolCallID == "" {
			run.resultID, run.resultAgentID = it.ID, it.AgentID
		} else if record, ok := s.subagentSummaries[sa.ID]; ok && completedResult(sa) {
			// The initial parent result is authoritative. A late child item only
			// advances its matching metadata, without generating the same input twice.
			if updated := s.syncInitialSummaryRecord(run, record); updated != record {
				before := m.summaryLocked(s)
				s.subagentSummaries[sa.ID] = updated
				s.summaryRevision++
				m.changedLocked(s, before)
				m.queueSubagentPreviewLocked(s, sa.ID, true)
			}
		}
		m.queueSubagentSummaryLocked(s, sa)
		return
	}
	if it.Tool == nil || it.Tool.Status != agentapi.ToolCompleted {
		return
	}
	for _, sa := range s.subagents {
		run := s.summaryRuns[sa.ID]
		if run != nil && !run.followup && sa.ParentToolCallID == it.ID {
			run.resultID, run.resultAgentID = it.ID, ""
			m.queueSubagentSummaryLocked(s, sa)
		}
	}
}

func (s *webSession) subagentResult(itemID, agentID string) string {
	index, ok := s.itemIdx[itemKey(agentID, itemID)]
	if !ok {
		return ""
	}
	it := s.items[index]
	if agentID != "" && it.Kind == agentapi.ItemAssistant {
		return it.Text
	}
	if agentID == "" && it.Tool != nil && it.Tool.Status == agentapi.ToolCompleted {
		return it.Tool.Output
	}
	return ""
}

func completedResult(sa *agentapi.Subagent) bool {
	return sa != nil && (sa.Status == agentapi.SubagentCompleted || sa.Status == agentapi.SubagentIdle)
}

func (m *Manager) queueSubagentSummaryLocked(s *webSession, sa *agentapi.Subagent) {
	run := s.summaryRuns[sa.ID]
	if m.closed || s.removed || run == nil || !completedResult(sa) || run.resultID == "" {
		return
	}
	result := summaryResult(s.subagentResult(run.resultID, run.resultAgentID))
	if result == "" {
		return
	}
	latestAssistant := s.lastSubagentAssistant(sa.ID)
	if run.resultAgentID != "" && run.resultID != latestAssistant {
		return
	}
	identity := subagentResultIdentity{run.resultID, run.resultAgentID, summaryDigest(result)}
	// No retry for duplicate complete items, opt-out or failure.
	if run.attempted == identity {
		return
	}
	if run.cancel != nil {
		run.cancel()
	}
	run.attempted = identity
	if len(sa.ID) > maxToolCallID || len(run.resultID) > maxToolCallID || len(run.resultAgentID) > maxToolCallID {
		return
	}
	model := m.utilityModelLocked(s.provider)
	provider, ok := m.providers[s.provider].(agentapi.SubagentSummarizer)
	if model == "" || !ok || !m.infos[s.provider].Available {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	run.cancel = cancel
	job := subagentSummaryJob{sessionID: s.id, agentID: sa.ID, run: run, ctx: ctx, cancel: cancel, identity: identity, provider: provider,
		request: agentapi.SubagentSummaryRequest{Model: model, Workdir: s.workdir, Description: strings.TrimSpace(displaytext.Sanitize(clipRunes(sa.Description, 255))), Result: result},
		record:  store.SubagentSummary{AgentID: sa.ID, ItemID: run.resultID, ItemAgentID: run.resultAgentID, Digest: identity.digest, LastAssistantID: latestAssistant},
	}
	if len(job.record.LastAssistantID) > maxToolCallID {
		cancel()
		return
	}
	select {
	case m.summaryJobs <- job:
	default:
		cancel() // bounded overload falls back to the provider's report
	}
}

func (m *Manager) subagentSummaryLoop() {
	defer m.summaryWorkers.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case job := <-m.summaryJobs:
			m.generateSubagentSummary(job)
		}
	}
}

func (m *Manager) generateSubagentSummary(job subagentSummaryJob) {
	defer job.cancel()
	if job.ctx.Err() != nil {
		return
	}
	select {
	case m.titleSlots <- struct{}{}:
		defer func() { <-m.titleSlots }()
	case <-job.ctx.Done():
		return
	}
	m.mu.Lock()
	current := m.summaryJobSessionLocked(&job) != nil
	m.mu.Unlock()
	if !current {
		return
	}
	ctx, cancel := context.WithTimeout(job.ctx, titleTimeout)
	reply, err := job.provider.SummarizeSubagent(ctx, job.request)
	timedOut := ctx.Err() != nil
	cancel()
	if err != nil || timedOut {
		return
	}
	job.record.Text = cleanGeneratedSummary(reply)
	if job.record.Text == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.summaryJobSessionLocked(&job)
	if s == nil {
		return
	}
	before := m.summaryLocked(s)
	_, replacing := s.subagentSummaries[job.agentID]
	if !replacing && len(s.subagentSummaries) >= maxSubagents {
		// Transcript order is oldest first; drop the oldest retained result.
		for _, sa := range s.subagents {
			if _, ok := s.subagentSummaries[sa.ID]; ok {
				delete(s.subagentSummaries, sa.ID)
				break
			}
		}
		// An idle history eviction may have removed all those child records.
		if len(s.subagentSummaries) >= maxSubagents {
			for id := range s.subagentSummaries {
				delete(s.subagentSummaries, id)
				break
			}
		}
	}
	s.subagentSummaries[job.agentID] = job.record
	s.summaryRevision++
	m.changedLocked(s, before)
	m.queueSubagentPreviewLocked(s, job.agentID, true)
}

// The same check protects the provider call and publication. A history
// replacement can invalidate source text without the live cancellation hook.
func (m *Manager) summaryJobSessionLocked(job *subagentSummaryJob) *webSession {
	s := m.sessions[job.sessionID]
	if m.closed || s == nil || s.removed || s.summaryRuns[job.agentID] != job.run || job.run.attempted != job.identity || job.ctx.Err() != nil || !completedResult(s.subIdx[job.agentID]) {
		return nil
	}
	job.record = s.syncInitialSummaryRecord(job.run, job.record)
	if !s.matchesSubagentSummary(job.record) {
		return nil
	}
	return s
}

// Only a complete live item in the same initial run may refresh this
// metadata. A history-only change must still fail the saved identity check.
func (s *webSession) syncInitialSummaryRecord(run *subagentSummaryRun, record store.SubagentSummary) store.SubagentSummary {
	if run.followup || record.ItemAgentID != "" || run.lastAssistantID == "" || len(run.lastAssistantID) > maxToolCallID || run.lastAssistantID != s.lastSubagentAssistant(record.AgentID) || (subagentResultIdentity{record.ItemID, record.ItemAgentID, record.Digest}) != run.attempted || summaryDigest(s.subagentResult(record.ItemID, record.ItemAgentID)) != record.Digest {
		return record
	}
	record.LastAssistantID = run.lastAssistantID
	return record
}

// A replay containing a newer child answer must not project a saved line
// merely because the initial parent task-tool output is still present.
func (s *webSession) lastSubagentAssistant(id string) string {
	for i := len(s.items) - 1; i >= 0; i-- {
		if it := s.items[i]; it.AgentID == id && it.Kind == agentapi.ItemAssistant {
			return it.ID
		}
	}
	return ""
}

func (s *webSession) matchesSubagentSummary(record store.SubagentSummary) bool {
	return s.lastSubagentAssistant(record.AgentID) == record.LastAssistantID && summaryDigest(s.subagentResult(record.ItemID, record.ItemAgentID)) == record.Digest
}

func (s *webSession) generatedSubagentSummary(sa agentapi.Subagent) string {
	if !completedResult(&sa) {
		return ""
	}
	record, ok := s.subagentSummaries[sa.ID]
	if !ok || !s.matchesSubagentSummary(record) {
		return ""
	}
	return record.Text
}

func (s *webSession) cancelSubagentSummaries() {
	for _, run := range s.summaryRuns {
		if run.cancel != nil {
			run.cancel()
		}
	}
	clear(s.summaryRuns)
}

func (m *Manager) discardSubagentSummaryJobs() {
	for {
		select {
		case job := <-m.summaryJobs:
			job.cancel()
		default:
			return
		}
	}
}

func (s *webSession) loadSubagentSummaries(records []store.SubagentSummary) {
	for _, record := range records {
		if len(s.subagentSummaries) == maxSubagents {
			break
		}
		if record.AgentID == "" || record.ItemID == "" || len(record.AgentID) > maxToolCallID || len(record.ItemID) > maxToolCallID || len(record.ItemAgentID) > maxToolCallID || len(record.LastAssistantID) > maxToolCallID || len(record.Digest) != sha256.Size*2 {
			continue
		}
		record.Text = cleanGeneratedSummary(record.Text)
		if record.Text != "" {
			s.subagentSummaries[record.AgentID] = record
		}
	}
}

func (s *webSession) savedSubagentSummaries() []store.SubagentSummary {
	records := make([]store.SubagentSummary, 0, len(s.subagentSummaries))
	for _, record := range s.subagentSummaries {
		records = append(records, record)
	}
	slices.SortFunc(records, func(a, b store.SubagentSummary) int { return strings.Compare(a.AgentID, b.AgentID) })
	return records
}
