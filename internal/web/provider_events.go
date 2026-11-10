package web

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// sink routes one conversation's events to its session. gen pins it to that
// conversation so a closed or replaced one cannot change the session.
type sink struct {
	m   *Manager
	s   *webSession
	gen uint64
}

// Emit applies the event at once. A tool item's images are stored by the
// image goroutine, since storing writes files and Emit must not wait: the
// item goes out without them now and again with them once they are stored.
func (k sink) Emit(ev agentapi.Event) {
	if ev.Kind == agentapi.EventItem && ev.Item != nil && ev.Item.Kind == agentapi.ItemTool && len(ev.Item.Images) > 0 {
		it := *ev.Item
		images := it.Images
		it.Images, it.ImagesNote = nil, ""
		ev.Item = &it
		k.m.handleEvent(k.s, k.gen, ev)
		k.m.queueImages(imageJob{s: k.s, gen: k.gen, agentID: it.AgentID, itemID: it.ID, images: images})
		return
	}
	k.m.handleEvent(k.s, k.gen, ev)
}

// handleEvent applies one provider event. It only takes Manager.mu and never
// waits on browsers: subscriber queues are fed without blocking.
func (m *Manager) handleEvent(s *webSession, gen uint64, ev agentapi.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.gen != gen {
		return
	}
	s.activeAt = m.now()
	before := m.summaryLocked(s)
	// After before: a new minute publishes the summary, a further event in
	// the same minute does not.
	s.eventAt = m.now().Truncate(time.Minute)
	switch ev.Kind {
	case agentapi.EventItem:
		if ev.Item != nil && ev.Item.ID != "" {
			it := clampItem(*ev.Item, m.now())
			if startsTurn(it) || it.Tool != nil && (it.Tool.Status == agentapi.ToolCompleted || it.Tool.Status == agentapi.ToolFailed) {
				s.invalidateNativeDiff()
				if s.activeTiming < 0 {
					s.idleToolEdits++
				}
			}
			s.linkUploadsLocked(&it)
			m.notePlanAliasLocked(s, it)
			m.linkTurnTimingLocked(s, it)
			m.upsertItemLocked(s, it, true)
			m.wrapItemLocked(s, it)
			if s.noteEdits(it) {
				m.kickDiffLocked(s)
			}
		}
	case agentapi.EventDelta:
		if ev.Delta != nil && ev.Delta.ItemID != "" {
			m.applyDeltaLocked(s, *ev.Delta)
		}
	case agentapi.EventTurn:
		if ev.Turn != nil {
			m.countTurn(*ev.Turn)
			m.applyTurnLocked(s, *ev.Turn)
		}
	case agentapi.EventInteraction:
		if ev.Interaction != nil && ev.Interaction.ID != "" {
			m.upsertInteractionLocked(s, *ev.Interaction)
		}
	case agentapi.EventPlanPath:
		path := s.planPath
		m.notePlanPathLocked(s, ev.PlanPath)
		if ev.PlanVersion != 0 && ev.PlanVersion != s.planVersion || s.planPath != path {
			if ev.PlanVersion != 0 {
				s.planVersion = ev.PlanVersion
			}
			m.broadcastLocked("plan_version", s.id, func(seq uint64) any {
				return struct {
					Seq         uint64 `json:"seq"`
					SessionID   string `json:"session_id"`
					PlanVersion uint64 `json:"plan_version"`
					PlanPath    string `json:"plan_path,omitempty"`
				}{seq, s.id, s.planVersion, s.planPath}
			})
		}
	case agentapi.EventSubagent:
		if ev.Subagent != nil && ev.Subagent.ID != "" {
			fresh := s.subIdx[ev.Subagent.ID] == nil
			m.upsertSubagentLocked(s, *ev.Subagent, true)
			if fresh && s.subIdx[ev.Subagent.ID] != nil {
				m.kickSubagentLabelLocked(s, *ev.Subagent)
			}
		}
	case agentapi.EventExecution:
		if ev.Execution != nil {
			state := *ev.Execution
			s.execution = &state
		}
	case agentapi.EventBackgroundTasks:
		if ev.BackgroundTasks != nil {
			m.backgroundTasksLocked(s, *ev.BackgroundTasks)
		}
	case agentapi.EventSchedules:
		if ev.Schedules != nil {
			m.schedulesLocked(s, ev.Schedules)
		}
	case agentapi.EventMCPStatus:
		if ev.MCPStatus != nil {
			m.mcpStatusLocked(s, *ev.MCPStatus)
		}
	case agentapi.EventSkill:
		if ev.Skill != nil {
			m.noteSkillsLocked(s, []agentapi.SkillLoad{*ev.Skill})
		}
	case agentapi.EventContext:
		if ev.Context != nil && ev.Context.Used >= 0 && ev.Context.Limit > 0 {
			usage := *ev.Context
			if usage.Cached < 0 || usage.Cached > usage.Prompt {
				usage.Prompt, usage.Cached = 0, 0
			}
			if s.context == nil || *s.context != usage {
				s.context = &usage
				// Not part of persistKey: without this the report is saved only
				// if something else dirties the session before the next flush.
				m.dirty[s.id] = struct{}{}
			}
		}
	case agentapi.EventModelSelection:
		if selection := ev.ModelSelection; selection != nil && validModelSelectionID(selection.Model, maxNameRunes) {
			s.model = strings.Clone(selection.Model)
			if selection.Model == "auto" {
				// Auto forbids authored per-model options. Normalize the
				// selection for reopen; retain native context usage/capacity.
				s.effort, s.contextSize = "", "default"
			} else {
				if selection.Effort != nil && (*selection.Effort == "" || validModelSelectionID(*selection.Effort, 64)) && m.validateSelectionLocked(s.provider, selection.Model, *selection.Effort, "default") == nil {
					s.effort = strings.Clone(*selection.Effort)
				}
				if selection.ContextSize != nil && validModelSelectionID(*selection.ContextSize, 64) && m.validateSelectionLocked(s.provider, selection.Model, "", *selection.ContextSize) == nil {
					s.contextSize = strings.Clone(*selection.ContextSize)
				}
			}
		}
	case agentapi.EventCompaction:
		started := (ev.Compacting || ev.CompactingBackground) && !s.compacting && !s.compactingBackground
		s.compacting, s.compactingBackground = ev.Compacting, ev.CompactingBackground
		if started {
			m.kickMemoryNotesLocked(s)
		}
		if ev.Compacted {
			m.kickMemoryPinLocked(s, ev.CompactionSummary)
		}
	case agentapi.EventActivity:
		if ev.Activity != nil {
			m.applyActivityLocked(s, *ev.Activity)
		}
	case agentapi.EventTodos:
		if ev.Todos != nil {
			m.applyTodosLocked(s, *ev.Todos)
		}
	case agentapi.EventUsage:
		m.applyUsageLocked(s, ev.Usage)
	case agentapi.EventTokens:
		if ev.Tokens != nil {
			m.recordTokensLocked(s.provider, *ev.Tokens)
			m.countTurnTokensLocked(s, *ev.Tokens)
			m.wrapCallLocked(s)
		}
	case agentapi.EventTitle:
		if title := cleanTitle(ev.Title); title != "" {
			s.title, s.titleFallback = title, false
		}
	case agentapi.EventExit:
		// The conversation is gone. Report it when it ran or waited for
		// anything; never replay a prompt or open a replacement on the
		// user's behalf. A shared runtime ends idle conversations too: those
		// keep their state, and the next prompt reopens, as after an idle
		// close. So does a conversation the provider detached after a stop
		// it could not confirm: it ended the turn as cancelled first.
		lost := !ev.Detached && s.runsOrWaitsLocked()
		s.conv = nil
		s.gen++
		m.expirePendingLocked(s, "the provider runtime exited")
		m.endSubagentsLocked(s)
		m.forgetBackgroundTaskStateLocked(s)
		m.pauseQueueLocked(s)
		if s.wrap != nil {
			m.finishWrapLocked(s, "", "the conversation ended")
		}
		if !lost {
			log.Info("idle web provider conversation exited", "session", s.id, "provider", s.provider)
			break
		}
		detail := "the provider runtime exited"
		if ev.Error != "" {
			detail = "the provider runtime exited: " + clipRunes(displaytext.Sanitize(ev.Error), maxDetailRunes)
		}
		s.setBase(StateFailed, detail)
		log.Warn("web provider conversation exited", "session", s.id, "provider", s.provider)
	}
	m.changedLocked(s, before)
}

func validModelSelectionID(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func (m *Manager) applyTurnLocked(s *webSession, turn agentapi.Turn) {
	s.invalidateNativeDiff()
	if model := clipRunes(strings.TrimSpace(displaytext.Sanitize(turn.Model)), maxNameRunes); model != "" {
		s.lastModel = model
	}
	m.turnActivityTurnLocked(s, turn.State)
	finalizedTiming := m.observeTurnTimingLocked(s, turn.State)
	if turn.State != agentapi.TurnWorking {
		m.kickDiffLocked(s) // the turn may have changed files without an edit tool
	}
	m.outcomeTurnLocked(s, turn.State, turn.Completion)
	switch turn.State {
	case agentapi.TurnWorking:
		s.setBase(StateWorking, "")
		if s.wrap != nil && s.wrap.prompted {
			s.wrap.started = true
		}
		if s.wrap == nil && !s.budget.Reached {
			// The handoff stays only until the Thread works again.
			s.handoff = nil
		}
	case agentapi.TurnCompleted:
		s.setBase(StateCompleted, "")
		// A stop that lost the race to the turn's end must not explain a
		// later one.
		s.stopReason, s.stopBy = "", ""
		// The whole turn is over, steers included: the queue may go on.
		m.kickDrainLocked(s)
	case agentapi.TurnCancelled:
		code, detail := s.stopCause(turn.Reason)
		if detail == "" && turn.Error != "" {
			// Why the provider ended the turn itself, as after a stop it
			// could not confirm.
			detail = clipRunes(displaytext.Sanitize(turn.Error), maxDetailRunes)
		}
		s.setBase(StateCancelled, detail)
		s.stopReason, s.stopBy, s.stopCode = "", "", code
		m.pauseQueueLocked(s)
	case agentapi.TurnFailed:
		detail := "the provider reported that the turn failed"
		if turn.Error != "" {
			detail = clipRunes(displaytext.Sanitize(turn.Error), maxDetailRunes)
		}
		s.setBase(StateFailed, detail)
		if turn.Failure != nil {
			s.failKind = agentapi.ErrorKindOf(string(turn.Failure.Kind))
		}
		s.stopReason, s.stopBy = "", ""
		m.pauseQueueLocked(s)
		m.kickSignedOutLocked(s, detail)
	}
	if finalizedTiming != nil {
		m.kickTurnChangesLocked(s, *finalizedTiming)
	}
	if turn.State != agentapi.TurnWorking {
		m.kickSessionPathsLocked(s)
		// The agent may have switched branches during the turn.
		m.kickBranchLocked(s.projectID)
		m.kickWorktreeSizeLocked(s)
		m.kickQuotaLocked(s.provider)
		if s.routineID != "" {
			m.kickRoutines() // its run ends now, not at the next tick
		}
		if s.titleWait != nil {
			m.titleAfterReplyLocked(s)
		}
		m.replyFallbackLocked(s)
		m.wrapTurnEndLocked(s, turn.State)
		m.peerTurnEndLocked(s, turn.State)
	}
}
