package web

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// changedLocked publishes s's queue when it changed and its summary when it
// changed since before, and schedules a sessions.json write when its durable
// part or its queue changed.
func (m *Manager) changedLocked(s *webSession, before SessionSummary) {
	m.updateTurnTimingPauseLocked(s)
	m.updatePhaseLocked(s)
	after := m.summaryLocked(s)
	after.UpdatedAt = before.UpdatedAt
	key := s.key()
	durable := key != s.persisted
	queue := s.queueChanged
	s.queueChanged = false
	if after == before && !durable && !queue {
		return
	}
	// A Task that stops working may be the last one a pending CLI or uam
	// restart waits for.
	select {
	case m.cliKick <- struct{}{}:
	default:
	}
	if m.binary != nil {
		m.binary.nudge()
	}
	// updated_at is Task activity, which is what the durable part records:
	// turn state (including waiting for input), detail, name, title, model
	// and the last submission. Queue changes are activity too. A viewer
	// opening the conversation (open, starting) and provider capability or
	// catalog changes are not, nor is the outcome line, which only follows
	// the turn state.
	worded := key
	worded.outcome = s.persisted.outcome
	worded.place = s.persisted.place // the branch taking the title is not activity either
	if durable && worded != s.persisted || queue {
		s.updatedAt = m.now()
	}
	if m.sessions[s.id] != s {
		// Not yet (or no longer) listed; Create publishes it once registered.
		return
	}
	if queue {
		m.broadcastLocked("queue", s.id, func(seq uint64) any {
			return queueEvent{Seq: seq, SessionID: s.id, Queue: s.queueSnapshot(), Paused: s.queuePaused}
		})
	}
	summary := m.summaryLocked(s)
	m.broadcastLocked("session", "", func(seq uint64) any { return sessionEvent{Seq: seq, Session: summary} })
	m.noticeLocked(s, before, summary)
	m.childEndLocked(s, before, summary)
	if durable || queue {
		if durable {
			s.persisted = key
		}
		m.dirty[s.id] = struct{}{}
		select {
		case m.wake <- struct{}{}:
		default:
		}
	}
}

// lazyFlushDelay bounds how long a model call's token counts wait in memory:
// the next flush writes them, or one this long after the first unsaved count.
// Model calls come too often to rewrite sessions.json and the token ledger for
// each; Shutdown's flush writes what is left.
var lazyFlushDelay = 30 * time.Second

// flushLaterLocked schedules a flush lazyFlushDelay after the first change
// since the last one it scheduled.
func (m *Manager) flushLaterLocked() {
	if m.lazyFlush {
		return
	}
	m.lazyFlush = true
	time.AfterFunc(lazyFlushDelay, func() {
		m.mu.Lock()
		m.lazyFlush = false
		m.mu.Unlock()
		select {
		case m.wake <- struct{}{}:
		default:
		}
	})
}

func (m *Manager) persistLoop() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
			if err := m.flush(); err != nil {
				log.Warn("persist web sessions failed", "error", err)
			}
		}
	}
}

type recordPatch struct {
	id, provider, name, convID string
	mode                       store.Mode
	updated                    time.Time
	web                        store.WebState
	// queue becomes web.Queue outside mu.
	queue []QueuedPrompt
}

// flush writes the durable state of every dirty session.
func (m *Manager) flush() (err error) {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	defer func() { m.noteSave(err) }()
	defer func() { err = errors.Join(err, m.flushTokenLedger()) }()
	m.mu.Lock()
	patches := make([]recordPatch, 0, len(m.dirty))
	var kept []keptTodos
	var keptChanges []keptTurnChanges
	var keptPlans []keptPlanReviews
	for id := range m.dirty {
		s := m.sessions[id]
		if s == nil {
			continue
		}
		timings := store.CloneTurnTimings(s.turnTimings)
		if len(s.turnTodos) > 0 {
			kept = append(kept, keptTodos{s: s, records: slices.Clone(s.turnTodos), timings: timings, live: s.turnActivity.Todos})
		}
		if len(s.turnChanges) > 0 {
			keptChanges = append(keptChanges, keptTurnChanges{s: s, records: cloneTurnChanges(s.turnChanges), timings: timings})
		}
		if len(s.planRecords) > 0 {
			keptPlans = append(keptPlans, keptPlanReviews{s: s, records: slices.Clone(s.planRecords)})
		}
		key := s.key()
		var commandResult json.RawMessage
		if key.commandResult != nil {
			commandResult, _ = json.Marshal(key.commandResult)
		}
		patches = append(patches, recordPatch{
			id: s.id, provider: s.provider, name: s.name, convID: s.convID, mode: s.mode, updated: s.updatedAt, queue: s.savedQueue(),
			web: store.WebState{
				Turn: key.turn, TurnTimings: timings, RequestID: key.reqID, RequestStatus: key.reqStatus, CommandResult: commandResult, CommandSubmissions: json.RawMessage(key.commandLedger), UpdatedAt: s.updatedAt, Detail: s.detail,
				ProjectID: key.projectID, Model: key.model, Effort: key.effort, ContextSize: key.contextSize, Agent: key.agent, OutputStyle: key.style, Skills: keySkills(key.skills), Title: key.title,
				Stage: key.stage, SettledAt: key.settledAt, SettledBy: key.settledBy, ArchivedAt: key.archivedAt, TerminalSession: s.terminalID, Imported: s.imported,
				SpawnedBy: s.spawnedBy, RoutineID: s.routineID, RerunOf: s.rerunOf, Fork: s.fork, Rewind: s.rewind.Clone(), Outcome: s.outcome, Suggestions: s.suggestions, UnseenEnd: s.unseenEnd, InterruptedAt: s.interruptedAt,
				StopReason: key.stopReason, FailureKind: string(key.failKind), NativeChanges: s.nativeChanges, Place: s.clonedPlace(), Budget: budgetRecord(key.budget), Handoff: handoffRecord(key.handoff), Context: (*store.WebContext)(s.context),
				MemoryNotes: slices.Clone(s.memoryNotes), SubagentLabels: maps.Clone(s.subagentLabels), NativePaths: nativePathsRecord(s), MessageOutcomes: slices.Clone(s.messageOutcomes),
			},
		})
	}
	m.dirty = map[string]struct{}{}
	writePairs := m.pairsDirty
	var pairs map[string]store.WebMessagePair
	if writePairs {
		pairs = make(map[string]store.WebMessagePair, len(m.pairs))
		for id, p := range m.pairs {
			p.Messages = slices.Clone(p.Messages)
			pairs[id] = p
		}
		if len(pairs) == 0 {
			pairs = nil
		}
		m.pairsDirty = false
	}
	m.mu.Unlock()
	if len(patches) == 0 && !writePairs {
		return nil
	}
	for i := range patches {
		if len(patches[i].queue) > 0 {
			patches[i].web.Queue, _ = json.Marshal(patches[i].queue)
		}
	}
	// Turn todo snapshots land before the counts that point at them.
	for _, k := range kept {
		m.appendTurnTodos(k)
	}
	for _, k := range keptChanges {
		m.appendTurnChanges(k)
	}
	for _, k := range keptPlans {
		m.appendPlanReviews(k)
	}
	err = m.store.Update(func(cfg *store.Config) error {
		for _, p := range patches {
			key := store.Key(p.provider, p.id)
			rec, ok := cfg.Sessions[key]
			// Never recreate a record removed behind the service's back, and
			// never touch a record this surface does not own.
			if !ok || rec.ID != p.id || rec.Surface != store.SurfaceWeb {
				continue
			}
			rec.Name = p.name
			rec.Mode = p.mode
			rec.ProviderSessionID = p.convID
			rec.LastSeenAt = p.updated
			// Update in place: fields a newer uam wrote must survive.
			web := store.WebState{}
			if rec.Web != nil {
				web = *rec.Web
			}
			web.Update(p.web)
			rec.Web = &web
			cfg.Sessions[key] = rec
		}
		if writePairs {
			cfg.WebMessagePairs = pairs
		}
		return nil
	})
	if err != nil {
		// Retry with the next change rather than spinning on a store that
		// refuses writes (for example a read-only newer schema).
		m.mu.Lock()
		for _, p := range patches {
			m.dirty[p.id] = struct{}{}
		}
		m.pairsDirty = m.pairsDirty || writePairs
		m.mu.Unlock()
		return err
	}
	return nil
}

// noteSave keeps the latest flush's failure, or clears it, and tells the
// browsers when that changes: a failed save is retried with the next change,
// and until one succeeds the owner's latest changes are not on disk.
func (m *Manager) noteSave(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg == m.saveError {
		return
	}
	m.saveError = msg
	m.broadcastLocked("save_status", "", func(seq uint64) any { return saveStatusEvent{Seq: seq, SaveError: msg} })
}
