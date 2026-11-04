package web

import (
	"cmp"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// lacksProject reports whether a web record has no stored Project: no
// project_id, or one naming a Project that is not in web_projects (written
// by a run whose migration could not be saved, or dropped as invalid).
func lacksProject(cfg *store.Config, rec store.SessionRecord) bool {
	if rec.Surface != store.SurfaceWeb || rec.ID == "" || rec.Workdir == "" {
		return false
	}
	if rec.Web == nil || rec.Web.ProjectID == "" {
		return true
	}
	_, ok := cfg.WebProjects[rec.Web.ProjectID]
	return !ok
}

// needsProject reports whether a web record still lacks a Project.
func needsProject(cfg store.Config) bool {
	for _, rec := range cfg.Sessions {
		if lacksProject(&cfg, rec) {
			return true
		}
	}
	return false
}

// assignProjects gives every web record that lacks a Project the Project for
// its workdir, creating one named after the directory when needed. Only such
// records are touched, so running it again changes nothing. A removed
// Project does not come back: removing it deleted its Tasks in the same
// update. The directory need not exist any more.
func assignProjects(cfg *store.Config, now time.Time) error {
	byDir := map[string]string{}
	for id, p := range cfg.WebProjects {
		byDir[p.Dir] = id
	}
	for key, rec := range cfg.Sessions {
		if !lacksProject(cfg, rec) {
			continue
		}
		id, ok := byDir[rec.Workdir]
		if !ok {
			var err error
			if id, err = newUUID(); err != nil {
				return fmt.Errorf("generate project id: %w", err)
			}
			if cfg.WebProjects == nil {
				cfg.WebProjects = map[string]store.WebProject{}
			}
			cfg.WebProjects[id] = store.WebProject{ID: id, Name: loadedName("", rec.Workdir), Dir: rec.Workdir, CreatedAt: now}
			byDir[rec.Workdir] = id
		}
		web := store.WebState{}
		if rec.Web != nil {
			web = *rec.Web
		}
		web.ProjectID = id
		rec.Web = &web
		cfg.Sessions[key] = rec
	}
	return nil
}

// loadedName is a stored Project name made safe to show, or the directory's
// base name when it has none.
func loadedName(name, dir string) string {
	if clean, err := cleanName(name, filepath.Base(dir)); err == nil {
		return clean
	}
	return clipRunes(displaytext.Sanitize(dir), maxNameRunes)
}

func sessionFromRecord(rec store.SessionRecord) *webSession {
	s := newSession(rec.ID, rec.Agent, rec.Name, rec.Workdir, rec.ProviderSessionID, rec.CreatedAt)
	s.updatedAt = rec.LastSeenAt
	if rec.Mode == store.ModeYolo || rec.Mode == store.ModeAssisted {
		s.mode = rec.Mode
	}
	if web := rec.Web; web != nil {
		s.turnTimings = store.CloneTurnTimings(web.TurnTimings)
		if len(s.turnTimings) > maxTurnTimings {
			s.turnTimings = s.turnTimings[len(s.turnTimings)-maxTurnTimings:]
		}
		for i := range s.turnTimings {
			if s.turnTimings[i].State == StateWorking {
				s.turnTimings[i].State = "unknown"
			}
		}
		_ = json.Unmarshal(web.CommandSubmissions, &s.commandSubmissions)
		s.commandLedger = string(web.CommandSubmissions)
		if knownStates[web.Turn] {
			s.base = web.Turn
		}
		s.detail, s.stopCode = web.Detail, web.StopReason
		if web.FailureKind != "" && s.base == StateFailed {
			s.failKind = agentapi.ErrorKindOf(web.FailureKind)
		}
		s.projectID, s.model, s.title = web.ProjectID, web.Model, cleanTitle(web.Title)
		s.terminalID = web.TerminalSession
		s.imported = web.Imported
		s.nativeChanges = web.NativeChanges
		s.spawnedBy = web.SpawnedBy
		s.routineID = web.RoutineID
		s.rerunOf, s.outcome, s.suggestions = web.RerunOf, clipRunes(displaytext.Sanitize(web.Outcome), maxOutcomeRunes), loadSuggestions(web.Suggestions)
		s.fork = web.Fork
		s.place = loadedPlace(web.Place)
		s.rewind = loadedRewind(web.Rewind)
		s.unseenEnd, s.interruptedAt = web.UnseenEnd, web.InterruptedAt
		s.effort, s.contextSize = web.Effort, cmp.Or(web.ContextSize, "default")
		if web.Context != nil && web.Context.Limit > 0 && web.Context.Used >= 0 {
			usage := agentapi.Context(*web.Context)
			s.context = &usage
		}
		s.agent, s.style, s.skillChoice = web.Agent, web.OutputStyle, web.Skills
		if web.Budget != nil {
			s.budget = *web.Budget
		}
		s.handoff = web.Handoff
		s.memoryNotes = loadMemoryNotes(web.MemoryNotes)
		s.subagentLabels = loadSubagentLabels(web.SubagentLabels)
		s.messageOutcomes = loadMessageOutcomes(web.MessageOutcomes)
		if web.NativePaths != nil {
			s.nativePaths, s.nativePathsAt = web.NativePaths.Paths, web.NativePaths.At
		}
		// An unknown stage loads as active, as an unknown turn state is ignored.
		if web.Stage == StageSettled || web.Stage == StageArchived {
			s.stage, s.settledAt, s.archivedAt = web.Stage, web.SettledAt, web.ArchivedAt
			if web.SettledBy == settledByAuto || web.SettledBy == settledByMerged || web.SettledBy == settledByReviewed {
				s.settledBy = web.SettledBy
			}
		}
		if !web.UpdatedAt.IsZero() {
			s.updatedAt = web.UpdatedAt
		}
		if s.stage == StageActive {
			// Nothing is sent again without the owner seeing it: the queue
			// waits, paused, until they resume or clear it.
			s.queue = loadedQueue(web.Queue)
			s.queuePaused = len(s.queue) > 0
		}
		if web.RequestID != "" {
			sub := Submission{RequestID: web.RequestID, Status: web.RequestStatus, Time: web.UpdatedAt}
			if len(web.CommandResult) > 0 {
				_ = json.Unmarshal(web.CommandResult, &sub.CommandResult)
			}
			s.submissions = []Submission{sub}
			s.last = &sub
		}
	}
	if s.updatedAt.IsZero() {
		s.updatedAt = s.createdAt
	}
	return s
}
