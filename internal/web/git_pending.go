package web

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

// Unsaved git work (docs/web.md): for each repository holding a Task's
// directory, one cached read of how many tracked files have changes not
// committed (untracked files left out) and how many commits are on no
// remote. Tasks in one repository share it: it is the folder's indicator;
// Settle, Archive and the quiet-Task sweep check a Thread's own work
// instead (putAwayWork). It is read again with the
// Projects' branches: when a turn ends, after Commit, Pull and Init, when a
// page loads past the branch TTL, and after Push.

// GitPending is a repository's unsaved work: Uncommitted counts changed
// tracked files, Unpushed the commits HEAD has that no remote-tracking
// branch has. A repository without remotes has nowhere to push, so its
// commits do not count.
type GitPending struct {
	Uncommitted int `json:"uncommitted"`
	Unpushed    int `json:"unpushed"`
}

// maxPendingStatus bounds the status output a count reads; past it the
// count is of the files read.
const maxPendingStatus = 4 << 20

// pendingDirsLocked returns the directories whose unsaved work is read with
// the Projects ids: each Project's directory and the directory of each of
// its Tasks. The caller holds mu.
func (m *Manager) pendingDirsLocked(ids []string) []string {
	projects := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		projects[id] = struct{}{}
	}
	seen := map[string]bool{}
	var dirs []string
	add := func(dir string) {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	for id := range projects {
		if p := m.projects[id]; p != nil {
			add(p.Dir)
		}
	}
	for _, s := range m.sessions {
		if _, ok := projects[s.projectID]; ok && !s.removed {
			add(s.workdir)
		}
	}
	return dirs
}

// kickGitPendingLocked reads the unsaved work of the repositories holding
// dirs in the background. The caller holds mu.
func (m *Manager) kickGitPendingLocked(dirs []string) {
	if m.closed || len(dirs) == 0 {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.refreshGitPending(m.ctx, dirs...)
	}()
}

// refreshGitPending reads the unsaved work of the repositories holding dirs,
// each repository once, and publishes the Tasks whose count changed. Git
// runs outside mu.
func (m *Manager) refreshGitPending(ctx context.Context, dirs ...string) {
	git, err := execpath.Resolve("git")
	if err != nil {
		return
	}
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	tops := make(map[string]string, len(dirs))
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for _, dir := range dirs {
		wg.Go(func() {
			top := repoTop(ctx, git, dir)
			mu.Lock()
			tops[dir] = top
			mu.Unlock()
		})
	}
	wg.Wait()
	var unique []string
	for _, top := range tops {
		if top != "" && !slices.Contains(unique, top) {
			unique = append(unique, top)
		}
	}
	reads := make(map[string]*GitPending, len(unique))
	for _, top := range unique {
		wg.Go(func() {
			p, _ := readGitPending(ctx, git, top, 0)
			mu.Lock()
			reads[top] = p
			mu.Unlock()
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return // the caller is gone; this says nothing about the work
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingTop == nil {
		m.pendingTop, m.gitPending = map[string]string{}, map[string]*GitPending{}
	}
	changed := map[string]bool{}
	for dir, top := range tops {
		if m.pendingTop[dir] != top {
			changed[dir] = true
		}
	}
	for top, p := range reads {
		if p == nil {
			continue // git failed; the last read stands
		}
		if old := m.gitPending[top]; old == nil || *old != *p {
			changed[top] = true
		}
	}
	if len(changed) == 0 {
		return
	}
	var touched []*webSession
	var befores []SessionSummary
	for _, s := range m.sessions {
		if !s.removed && (changed[s.workdir] || changed[m.pendingTop[s.workdir]] || changed[tops[s.workdir]]) {
			touched = append(touched, s)
			befores = append(befores, m.summaryLocked(s))
		}
	}
	for dir, top := range tops {
		m.pendingTop[dir] = top
	}
	for top, p := range reads {
		if p != nil && changed[top] {
			m.gitPending[top] = p
		}
	}
	for i, s := range touched {
		m.changedLocked(s, befores[i])
	}
}

// repoTop is the real path of the git work tree holding dir, "" when there
// is none or git cannot tell.
func repoTop(ctx context.Context, git, dir string) string {
	out, code, _, err := runGit(ctx, git, dir, 4096, "rev-parse", "--show-toplevel")
	if err != nil || code != 0 {
		return ""
	}
	if top := strings.TrimSpace(string(out)); top != "" {
		return realPath(top)
	}
	return ""
}

// readGitPending counts top's unsaved work and keeps the paths of the
// first keep changed files, nil when git fails.
func readGitPending(ctx context.Context, git, top string, keep int) (*GitPending, []string) {
	status, code, _, err := runGit(ctx, git, top, maxPendingStatus, "status", "--porcelain=v2", "-z", "--untracked-files=no")
	if err != nil || code != 0 {
		return nil, nil
	}
	n, paths := trackedChanges(status, keep)
	p := &GitPending{Uncommitted: n}
	remotes, code, _, err := runGit(ctx, git, top, 64<<10, "remote")
	if err != nil || code != 0 {
		return nil, nil
	}
	if len(strings.Fields(string(remotes))) == 0 {
		return p, paths
	}
	// Before the first commit there is no HEAD and nothing to push.
	count, code, _, err := runGit(ctx, git, top, 4096, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return nil, nil
	}
	if code == 0 {
		p.Unpushed, _ = strconv.Atoi(strings.TrimSpace(string(count)))
	}
	return p, paths
}

// trackedChanges counts the changed tracked files in git status
// --porcelain=v2 -z output, ordinary, renamed or copied, and unmerged
// entries, and returns the paths of the first keep, made safe to show. A
// rename's or copy's source path is its own NUL-ended field.
func trackedChanges(status []byte, keep int) (int, []string) {
	n := 0
	var paths []string
	fields := strings.Split(string(status), "\x00")
	for i := 0; i < len(fields); i++ {
		// The fields before the path: 8 for an ordinary entry, 9 for a
		// rename or copy, 10 for an unmerged one.
		var before int
		switch {
		case strings.HasPrefix(fields[i], "1 "):
			before = 8
		case strings.HasPrefix(fields[i], "2 "):
			before = 9
		case strings.HasPrefix(fields[i], "u "):
			before = 10
		default:
			continue
		}
		n++
		if parts := strings.SplitN(fields[i], " ", before+1); len(paths) < keep && len(parts) == before+1 {
			paths = append(paths, clipRunes(displaytext.Sanitize(parts[before]), maxNameRunes))
		}
		if before == 9 {
			i++ // the source path
		}
	}
	return n, paths
}

// maxUnsavedPaths is how many changed files a refusal over unsaved work
// names.
const maxUnsavedPaths = 3

// codeUnsavedWork classifies a refusal to settle or archive a Task that has
// unsaved work (putAwayWork).
const codeUnsavedWork = "unsaved_work"

// checkUnsaved refuses putting away the Task id while it has unsaved work
// (putAwayWork), read now: the refusal carries the counts and the first
// changed files. The folder's cached count is read again too.
func (m *Manager) checkUnsaved(ctx context.Context, id string) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.kickGitPendingLocked([]string{s.workdir})
	m.mu.Unlock()
	p, paths, own := m.putAwayWork(ctx, s)
	if p == nil {
		return nil
	}
	var parts []string
	if p.Uncommitted > 0 {
		parts = append(parts, fmt.Sprintf("%d not committed", p.Uncommitted))
	}
	if p.Unpushed > 0 {
		parts = append(parts, fmt.Sprintf("%d not pushed", p.Unpushed))
	}
	message := "This Thread's own changes (" + strings.Join(parts, ", ") + ") are not saved anywhere else."
	if own {
		message = "This Thread's worktree has unsaved git work (" + strings.Join(parts, ", ") + ") that is not saved anywhere else."
	}
	return &Error{Status: http.StatusConflict, Code: codeUnsavedWork, Pending: p, Refs: paths, Message: message}
}

// putAwayWork is the unsaved git work that keeps s from being settled or
// archived without force, read now, with its first changed files; nil when
// there is none or git cannot tell. own reports that s runs in its own
// worktree: all the work there is its own, so the whole worktree counts,
// changed tracked files and commits on no remote. In a folder Threads share
// only s's own files count: those its native session diff lists, where the
// provider keeps one, and those its edit tools touched, still changed
// against HEAD, new untracked files included. Commits there are no one
// Thread's and do not count.
func (m *Manager) putAwayWork(ctx context.Context, s *webSession) (p *GitPending, paths []string, own bool) {
	m.mu.Lock()
	dir := s.workdir
	own = s.place != nil && s.place.Worktree != ""
	m.mu.Unlock()
	if own {
		git, err := execpath.Resolve("git")
		if err != nil {
			return nil, nil, own
		}
		top := repoTop(ctx, git, dir)
		if top == "" {
			return nil, nil, own
		}
		if p, paths = readGitPending(ctx, git, top, maxUnsavedPaths); p == nil || *p == (GitPending{}) {
			return nil, nil, own
		}
		return p, paths, own
	}
	mine := m.threadPaths(ctx, s)
	if len(mine) == 0 {
		return nil, nil, own
	}
	repo, _, err := openRepo(ctx, dir)
	if err != nil || repo == nil {
		return nil, nil, own
	}
	entries, _, err := repo.status(ctx)
	if err != nil {
		return nil, nil, own
	}
	resolve := repoPaths(repo.top, dir)
	set := make(map[string]bool, len(mine))
	for _, path := range mine {
		if rel, ok := resolve(path); ok {
			set[rel] = true
		}
	}
	n := 0
	for _, e := range entries {
		if set[e.path] || e.orig != "" && set[e.orig] {
			n++
			if len(paths) < maxUnsavedPaths {
				paths = append(paths, clipRunes(displaytext.Sanitize(e.path), maxNameRunes))
			}
		}
	}
	if n == 0 {
		return nil, nil, own
	}
	return &GitPending{Uncommitted: n}, paths, own
}

// threadPaths lists the files s changed, as given: those its edit tools
// touched and, for a native Task, those its native session diff lists, read
// now while the conversation is open, else as last read. A closed
// conversation is not resumed for it.
func (m *Manager) threadPaths(ctx context.Context, s *webSession) []string {
	m.mu.Lock()
	paths := slices.Collect(maps.Keys(s.edits))
	native, conv, gen, revision := s.nativeChanges, s.conv, s.gen, s.nativeDiffRevision
	m.mu.Unlock()
	if native && conv != nil {
		if files, err := m.readNativeDiff(ctx, s, conv, gen, revision); err == nil {
			for _, f := range files {
				paths = append(paths, f.Path)
				if f.OldPath != "" {
					paths = append(paths, f.OldPath)
				}
			}
			return paths
		}
	}
	m.mu.Lock()
	paths = append(paths, s.nativePaths...)
	m.mu.Unlock()
	return paths
}
