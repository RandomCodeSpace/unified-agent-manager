package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// The task and turn scopes of Changes, and the Task's cached total
// (SessionSummary.Diff).
//
// A Task's edits are the files its edit tools touched (editedPaths: create,
// write, edit and apply_patch, main agent and subagents, calls that did not
// complete left out), collected as items arrive live or from the provider's
// record and kept when the transcript is trimmed or evicted. Its latest turn
// starts at the latest ordinary prompt to the main agent (startsTurn), so
// steers join the running turn. The turn's edits are those touched at or
// after that prompt's time. Each scope
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

// invalidateNativeDiff leaves the header total unknown until another
// on-demand native read. Even a failed tool can have committed partial edits.
// The caller holds Manager.mu and publishes its event's changed summary.
func (s *webSession) invalidateNativeDiff() {
	if s.nativeChanges {
		s.diff = nil
		s.nativeDiffRevision++
	}
}

// noteEdits records it in s's evidence, edits and turn start and reports
// whether an edit completed, so the total needs a recount. An edit counts
// once it completed, at when it ended: one still running may yet fail.
func (s *webSession) noteEdits(it agentapi.Item) bool {
	s.noteActivity(it)
	if startsTurn(it) {
		if it.Time.After(s.turnStart) {
			s.turnStart = it.Time
		}
		return false
	}
	if it.Tool == nil || it.Tool.Status != agentapi.ToolCompleted {
		return false
	}
	paths := editedPaths(it.Tool)
	at := it.Time
	if it.EndedAt.After(at) {
		at = it.EndedAt
	}
	projectEdit := false
	for _, p := range paths {
		if s.isPlanPath(p) {
			continue
		}
		projectEdit = true
		if last, ok := s.edits[p]; !ok || at.After(last) {
			if s.edits == nil {
				s.edits = map[string]time.Time{}
			}
			s.edits[p] = at
		}
	}
	return projectEdit
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
	if m.closed || s.removed || s.nativeChanges || len(s.edits) == 0 && s.diff == nil {
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
	if !s.nativeChanges {
		m.setDiffLocked(s, l.stat())
	}
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

// editedPaths lists the files one edit tool call (edit, create, write or
// apply_patch, in any case) touched; none for a call that only reads or
// failed. The paths come from the input's headers or path field alone, so an
// input clipped at its size limit, or framed loosely, still names the files
// before the cut; the paths are only matched against git's listing. This is
// the one reading of which files a call changed: Changes, the Task's total,
// the finish card and the outcome line all use it.
func editedPaths(tool *agentapi.ToolCall) []string {
	if tool == nil || tool.Status == agentapi.ToolFailed {
		return nil
	}
	switch strings.ToLower(tool.Name) {
	case "apply_patch":
		return patchHeaderPaths(tool.Input)
	case "edit", "create", "write":
		if p := inputPath(tool.Input); p != "" {
			return []string{p}
		}
	}
	return nil
}

// inputPathRE finds a path field in JSON clipped before it closes.
var inputPathRE = regexp.MustCompile(`"(?:path|file_path|filePath)"\s*:\s*("(?:[^"\\]|\\.)*")`)

// inputPath is the file an edit tool's JSON input names in its path,
// file_path or filePath field; "" when none, or when they disagree.
func inputPath(input string) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &obj) != nil {
		// Clipped: the first path field before the cut.
		var p string
		if m := inputPathRE.FindStringSubmatch(input); m == nil || json.Unmarshal([]byte(m[1]), &p) != nil || !validResolvePath(p) {
			return ""
		}
		return p
	}
	path := ""
	for _, key := range []string{"path", "file_path", "filePath"} {
		if raw, ok := obj[key]; ok {
			var value string
			if json.Unmarshal(raw, &value) != nil || !validResolvePath(value) || (path != "" && path != value) {
				return ""
			}
			path = value
		}
	}
	return path
}

// patchHeaderPaths lists the files a patch's Add, Update, Delete and Move to
// headers name, in order. The input may be the patch itself, the patch as a
// JSON string, or a JSON object holding it as a string field. JSON clipped at
// the size limit has its escaped line breaks split and its cut last line
// dropped.
func patchHeaderPaths(input string) []string {
	patch := strings.TrimSpace(input)
	switch {
	case strings.HasPrefix(patch, `"`):
		var value string
		if json.Unmarshal([]byte(patch), &value) == nil {
			patch = value
		} else {
			patch = clippedLines(patch)
		}
	case strings.HasPrefix(patch, "{"):
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(patch), &obj) != nil {
			patch = clippedLines(patch)
			break
		}
		for _, raw := range obj {
			var value string
			if json.Unmarshal(raw, &value) == nil && strings.Contains(value, "*** ") {
				patch = value
				break
			}
		}
	}
	var paths []string
	seen := map[string]bool{}
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			p, ok := strings.CutPrefix(line, prefix)
			if p = strings.TrimSpace(p); ok && validResolvePath(p) && !seen[p] && len(paths) < maxChangedFiles {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// clippedLines splits clipped JSON text at its escaped line breaks and drops
// the last line, which the clip may have cut.
func clippedLines(text string) string {
	text = strings.ReplaceAll(text, `\n`, "\n")
	return text[:strings.LastIndexByte(text, '\n')+1]
}
