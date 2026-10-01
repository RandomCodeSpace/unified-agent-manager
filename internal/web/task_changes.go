package web

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// The task and turn scopes of Changes, and the Task's cached total
// (SessionSummary.Diff).
//
// A Task's edits are the files its edit tools touched (editedPaths: create,
// edit and apply_patch, main agent and subagents, failed calls left out),
// collected as items arrive live or from the provider's record and kept when
// the transcript is trimmed or evicted. Its latest turn starts at the latest
// ordinary prompt to the main agent: a main-agent user item that is neither a
// steer nor an automatic continuation, so steers join the running turn. The
// turn's edits are those touched at or after that prompt's time. Each scope
// lists the files of the working-tree listing (compared with HEAD) among
// those edits: a file someone else also changed shows those changes too, and
// one changed back or committed drops out. Files written by commands (a
// shell, a script) are not edit tools and are not attributed.

const (
	taskLabel    = "Files this task's agent edited, compared with HEAD. Changes made to them by anything else show too."
	turnLabel    = "Files the agent edited in its latest turn, compared with HEAD."
	partialEdits = "edits from before this task's history was read may be missing"
)

var errScope = newError(http.StatusBadRequest, "scope must be %q, %q, %q or %q", ScopeTask, ScopeTurn, ScopeWorkspace, ScopeSession)

// diffDelay batches a burst of edits into one recount of the Task's total.
var diffDelay = time.Second

// noteEdits records it in s's edits and turn start and reports whether the
// edits changed or an edit completed, so the total needs a recount.
func (s *webSession) noteEdits(it agentapi.Item) bool {
	if it.AgentID == "" && it.Kind == agentapi.ItemUser && it.Delivery == "" {
		if it.Time.After(s.turnStart) {
			s.turnStart = it.Time
		}
		return false
	}
	paths := editedPaths(it.Tool)
	changed := len(paths) > 0 && it.Tool.Status == agentapi.ToolCompleted
	for _, p := range paths {
		if at, ok := s.edits[p]; !ok || it.Time.After(at) {
			if s.edits == nil {
				s.edits = map[string]time.Time{}
			}
			s.edits[p] = it.Time
			changed = true
		}
	}
	return changed
}

// noteHistoryLocked records a provider record's edits; whole says the
// record was read, which makes the edits complete unless it was truncated.
func (m *Manager) noteHistoryLocked(s *webSession, h agentapi.History, whole bool) {
	changed := false
	for _, it := range h.Items {
		changed = s.noteEdits(it) || changed
	}
	if whole && !h.Truncated {
		s.editsKnown = true
	}
	if changed {
		m.kickDiffLocked(s)
	}
}

// kickDiffLocked recounts s's total in the background, after diffDelay,
// once more if it is kicked again meanwhile.
func (m *Manager) kickDiffLocked(s *webSession) {
	if m.closed || s.removed || len(s.edits) == 0 && s.diff == nil {
		return
	}
	if s.diffRunning {
		s.diffDirty = true
		return
	}
	s.diffRunning = true
	m.wg.Add(1)
	go m.recountDiff(s)
}

func (m *Manager) recountDiff(s *webSession) {
	defer m.wg.Done()
	timer := time.NewTimer(diffDelay)
	defer timer.Stop()
	for {
		select {
		case <-m.ctx.Done():
			m.mu.Lock()
			s.diffRunning = false
			m.mu.Unlock()
			return
		case <-timer.C:
		}
		m.mu.Lock()
		s.diffDirty = false
		workdir, edits, start := s.workdir, maps.Clone(s.edits), s.turnStart
		m.mu.Unlock()
		l, err := listTaskChanges(m.ctx, workdir, edits, start)
		m.mu.Lock()
		if err == nil {
			m.setDiffLocked(s, l.stat())
		}
		if !s.diffDirty || m.closed || s.removed {
			s.diffRunning = false
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		timer.Reset(diffDelay)
	}
}

// setDiffLocked caches s's total and publishes the summary when it changed.
func (m *Manager) setDiffLocked(s *webSession, d *DiffStat) {
	if s.removed || (d == nil) == (s.diff == nil) && (d == nil || *d == *s.diff) {
		return
	}
	before := m.summaryLocked(s)
	s.diff = d
	m.changedLocked(s, before)
}

// taskListing is the working-tree listing with the task and turn subsets.
type taskListing struct {
	all        Changes
	repo       *gitRepo
	task, turn []ChangedFile
}

func listTaskChanges(ctx context.Context, workdir string, edits map[string]time.Time, turnStart time.Time) (taskListing, error) {
	all, repo, err := workspaceListing(ctx, workdir)
	l := taskListing{all: all, repo: repo, task: []ChangedFile{}, turn: []ChangedFile{}}
	if err != nil || repo == nil {
		return l, err
	}
	resolve := repoPaths(repo.top, workdir)
	inTask, inTurn := map[string]bool{}, map[string]bool{}
	for p, at := range edits {
		if rel, ok := resolve(p); ok {
			inTask[rel] = true
			inTurn[rel] = inTurn[rel] || !at.Before(turnStart)
		}
	}
	for _, f := range all.Files {
		if inTask[f.Path] {
			l.task = append(l.task, f)
		}
		if inTurn[f.Path] {
			l.turn = append(l.turn, f)
		}
	}
	return l, nil
}

// stat totals the task subset; nil when it is empty or unknown.
func (l taskListing) stat() *DiffStat {
	if l.repo == nil || len(l.task) == 0 {
		return nil
	}
	d := &DiffStat{Files: len(l.task)}
	for _, f := range l.task {
		d.Additions += f.Additions
		d.Deletions += f.Deletions
	}
	return d
}

// gitChanges lists the workspace, task or turn scope, with the counts of
// all three, and refreshes the Task's cached total on the way.
func (m *Manager) gitChanges(ctx context.Context, id, scope string) (Changes, error) {
	s, err := m.lookup(id)
	if err != nil {
		return Changes{}, err
	}
	m.mu.Lock()
	workdir, edits, start, known := s.workdir, maps.Clone(s.edits), s.turnStart, s.editsKnown
	m.mu.Unlock()
	l, err := listTaskChanges(ctx, workdir, edits, start)
	out := l.all
	if err != nil {
		return out, err
	}
	if scope != ScopeWorkspace {
		out.Scope, out.Label = scope, taskLabel
		if scope == ScopeTurn {
			out.Label = turnLabel
		}
	}
	if l.repo == nil {
		return out, nil
	}
	m.mu.Lock()
	m.setDiffLocked(s, l.stat())
	m.mu.Unlock()
	out.Counts = &ScopeCounts{Task: len(l.task), Turn: len(l.turn), Workspace: len(l.all.Files)}
	if scope != ScopeWorkspace {
		out.Files = l.task
		if scope == ScopeTurn {
			out.Files = l.turn
		}
		if !known {
			out.Reason = joinReasons(out.Reason, partialEdits)
		}
	}
	digestFiles(l.repo.top, out.Files)
	return out, nil
}

// taskFileDiff is one file of the task or turn scope compared with HEAD.
func (m *Manager) taskFileDiff(ctx context.Context, id, scope, path string) (agentapi.FileDiff, error) {
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.FileDiff{}, err
	}
	m.mu.Lock()
	workdir, edits, start := s.workdir, maps.Clone(s.edits), s.turnStart
	m.mu.Unlock()
	repo, reason, err := openRepo(ctx, workdir)
	if err != nil {
		return agentapi.FileDiff{}, err
	}
	if repo == nil {
		return agentapi.FileDiff{}, newError(http.StatusConflict, "%s", reason)
	}
	resolve := repoPaths(repo.top, workdir)
	for p, at := range edits {
		if rel, ok := resolve(p); ok && rel == path && (scope == ScopeTask || !at.Before(start)) {
			return workspaceFileDiff(ctx, workdir, path)
		}
	}
	return agentapi.FileDiff{}, newError(http.StatusBadRequest, "path is not among the files this task edited")
}

// digestFiles sets each file's Digest from its size and modification time,
// read without following a final symlink: cheap enough for every listing,
// and it changes whenever the content does. A file that is gone gets "gone".
func digestFiles(top string, files []ChangedFile) {
	root, err := os.OpenRoot(top)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	for i := range files {
		info, err := root.Lstat(files[i].Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			files[i].Digest = "gone"
		case err == nil:
			files[i].Digest = strconv.FormatInt(info.Size(), 36) + "-" + strconv.FormatInt(info.ModTime().UnixNano(), 36)
		}
	}
}

func joinReasons(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
