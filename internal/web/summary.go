package web

import (
	"cmp"
	"net/http"
	"slices"
	"sort"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func (m *Manager) lookup(id string) (*webSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil, newError(http.StatusNotFound, msgSessionNotFound)
	}
	return s, nil
}

func (m *Manager) summaryLocked(s *webSession) SessionSummary {
	permissions, questions := s.pendingKinds()
	capabilities := m.infos[s.provider].Capabilities
	if capabilities.SessionDiffNeedsTracking && !s.nativeChanges {
		capabilities.SessionDiff = false
	}
	forkOf, forkUser := "", ""
	if s.fork != nil {
		forkOf, forkUser = s.fork.SourceTaskID, s.fork.UserItemID
	}
	out := SessionSummary{
		ID: s.id, ProjectID: s.projectID, Provider: s.provider, Model: s.model, Name: s.name, Title: s.title,
		Effort: s.effort, ContextSize: cmp.Or(s.contextSize, "default"), Agent: s.agent, OutputStyle: s.style, Context: s.context, Usage: s.usage, Budget: m.budgetViewLocked(s), Handoff: s.handoff,
		LastModel: s.lastModel, SubagentsRunning: s.runningSubagents(), BackgroundTasksRunning: s.runningBackgroundTasks(), Workdir: s.workdir, RealWorkdir: s.realWorkdir, ConversationID: s.convID,
		Execution: s.execution, State: s.state(), StateDetail: s.detail, InterruptedAt: s.interruptedAt, Open: s.conv != nil, Pending: permissions + questions,
		CreatedAt: s.createdAt, UpdatedAt: s.updatedAt, Capabilities: capabilities, Queued: len(s.queue),
		Mode: string(s.mode), ModeUnknown: s.modeUnknown, Stage: s.stage, SettledAt: s.settledAt, SettledBy: s.settledBy, ArchivedAt: s.archivedAt, SpawnedBy: s.spawnedBy, RoutineID: s.routineID,
		Ask: s.pendingAsk(), EventAt: s.eventAt, Compacting: s.compacting && s.conv != nil, CompactingBackground: s.compactingBackground && s.conv != nil, CompactThreshold: s.openCompactAt(),
		Diff:       s.diff,
		GitPending: m.gitPending[m.pendingTop[s.workdir]],
		RerunOf:    s.rerunOf, Outcome: s.outcome, StopReason: s.shownStopReason(), FailureKind: string(s.failKind),
		ForkOf: forkOf, ForkUserItemID: forkUser, NotStarted: s.fork != nil && s.base == StateClosed && len(s.turnTimings) == 0, Rewind: s.rewindSummary(),
		Branch:       s.branch,
		TitlePending: s.titling || s.titleWait != nil,
		MessagesHeld: m.heldMessagesLocked(s.id),
	}
	out.BranchLinkURL, out.BranchLinkBranch = m.branchLinkLocked(s)
	if s.place != nil && s.place.Worktree != "" {
		out.Worktree = WorktreeInfo{Path: s.place.Worktree, Branch: s.place.Branch, SizeBytes: m.worktrees.sizes[s.place.Worktree].bytes}
	}
	return out
}

// detailLocked builds the retained web transcript and controls.
func (m *Manager) detailLocked(s *webSession) SessionDetail {
	timings := store.CloneTurnTimings(s.turnTimings)
	if timings == nil {
		timings = []TurnTiming{}
	}
	d := SessionDetail{
		PlanVersion:      s.planVersion,
		PlanPath:         s.planPath,
		SessionSummary:   m.summaryLocked(s),
		Seq:              m.seq,
		TurnTimings:      timings,
		Items:            s.agentItems(""),
		Interactions:     make([]agentapi.Interaction, 0, len(s.interactions)),
		Subagents:        s.subagentList(),
		HistoryTruncated: s.truncated,
		Queue:            s.queueSnapshot(),
		QueuePaused:      s.queuePaused,
		History:          s.historyState(),
		HistoryReason:    s.historyReason,
		Drawings:         m.drawingSummaries(s.id),
	}
	for _, ix := range s.interactions {
		d.Interactions = append(d.Interactions, ix.public())
	}
	if s.backgroundTasks != nil {
		snapshot := *s.backgroundTasks
		snapshot.Tasks = slices.Clone(snapshot.Tasks)
		d.BackgroundTasks = &snapshot
	}
	if s.schedules != nil {
		snapshot := *s.schedules
		snapshot.Entries = slices.Clone(snapshot.Entries)
		d.Schedules = &snapshot
	}
	if s.mcpStatus != nil {
		snapshot := *s.mcpStatus
		snapshot.Servers = slices.Clone(snapshot.Servers)
		d.MCPStatus = &snapshot
	}
	d.SkillsLoaded = slices.Clone(s.skills)
	activity := s.turnActivity
	d.TurnActivity = &activity
	if s.last != nil {
		last := *s.last
		d.LastSubmission = &last
	}
	return d
}

func (m *Manager) summariesLocked() []SessionSummary {
	out := make([]SessionSummary, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, m.summaryLocked(s))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// List returns every web session, newest first.
func (m *Manager) List() []SessionSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.summariesLocked()
}

// Summary returns one session's summary.
func (m *Manager) Summary(id string) (SessionSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	return m.summaryLocked(s), nil
}

// Detail returns one session with its retained transcript. Asking for it
// starts a read-only load of the recorded transcript when the conversation
// is not open and the transcript is not in memory.
func (m *Manager) Detail(id string) (SessionDetail, error) {
	s, err := m.lookup(id)
	if err != nil {
		return SessionDetail{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.removed {
		return SessionDetail{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	m.viewHistoryLocked(s)
	return m.detailLocked(s), nil
}
