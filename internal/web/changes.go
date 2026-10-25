package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

const (
	workspaceLabel = "Whole working tree compared with HEAD. It may include changes that were not made by this session."
	sessionLabel   = "File changes the provider recorded for this conversation."
	// closedSessionLabel heads the task scope served for a closed conversation.
	closedSessionLabel = "Git-based fallback while this Thread's conversation is closed: files its agent edited, compared with HEAD. Open the conversation to read its native changes."

	gitTimeout       = 10 * time.Second
	branchTimeout    = 2 * time.Second
	branchTTL        = 2 * time.Second
	maxStatusBytes   = 4 << 20
	maxDiffBytes     = 1 << 20
	maxChangedFiles  = 1000
	maxUntrackedRead = 256 << 10
	untrackedBudget  = 8 << 20

	// Git flags shared by the package's diffs and pathspec commands: exact
	// paths, no external diff or textconv programs, no colour.
	literalPathspecs = "--literal-pathspecs"
	noExtDiff        = "--no-ext-diff"
	noTextconv       = "--no-textconv"
	noColor          = "--no-color"
)

// gitBase is prepended to every git invocation: read-only, no fsmonitor, no
// hooks, no signature checks, and no external diff or textconv programs.
var gitBase = []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "log.showSignature=false"}

// Changes lists changed files for a session in the requested scope. It
// also re-reads the branch of the session's Project.
func (m *Manager) Changes(ctx context.Context, id, scope string) (Changes, error) {
	if s, err := m.lookup(id); err == nil {
		m.mu.Lock()
		project := s.projectID
		m.mu.Unlock()
		m.refreshBranches(ctx, true, project)
	}
	switch scope {
	case ScopeWorkspace, ScopeTask, ScopeTurn:
		return m.gitChanges(ctx, id, scope)
	case ScopeSession:
		files, out, err := m.sessionDiff(ctx, id)
		if err == nil && out.Scope == ScopeTask {
			out, err = m.gitChanges(ctx, id, ScopeTask)
			out.Label = closedSessionLabel
			return out, err
		}
		if err != nil || !out.Supported {
			return out, err
		}
		for _, f := range files {
			file := ChangedFile{Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions,
				OldPath: f.OldPath, CountsUnknown: f.CountsUnknown, Binary: f.Binary, Truncated: f.Truncated}
			if f.Patch != "" && !f.CountsUnknown {
				file.Digest = fmt.Sprintf("%x", sha256.Sum256([]byte(f.Patch)))
			}
			out.Files = append(out.Files, file)
		}
		return out, nil
	default:
		return Changes{}, errScope
	}
}

// FileChange returns one file's diff in the requested scope. The path must
// be one the scope's listing reports.
func (m *Manager) FileChange(ctx context.Context, id, scope, path string) (agentapi.FileDiff, error) {
	if path == "" {
		return agentapi.FileDiff{}, newError(http.StatusBadRequest, "path is required")
	}
	switch scope {
	case ScopeWorkspace:
		s, err := m.lookup(id)
		if err != nil {
			return agentapi.FileDiff{}, err
		}
		m.mu.Lock()
		workdir := s.workdir
		m.mu.Unlock()
		return workspaceFileDiff(ctx, workdir, path)
	case ScopeTask, ScopeTurn:
		return m.taskFileDiff(ctx, id, scope, path)
	case ScopeSession:
		files, out, err := m.sessionDiff(ctx, id)
		if err != nil {
			return agentapi.FileDiff{}, err
		}
		if out.Scope == ScopeTask {
			return m.taskFileDiff(ctx, id, ScopeTask, path)
		}
		if !out.Supported {
			return agentapi.FileDiff{}, newError(http.StatusConflict, "%s", out.Reason)
		}
		for _, f := range files {
			if f.Path == path {
				f.Before = clampText(f.Before, maxDiffBytes)
				f.After = clampText(f.After, maxDiffBytes)
				if len(f.Patch) > maxDiffBytes {
					f.Truncated = true
				}
				f.Patch = clampText(f.Patch, maxDiffBytes)
				return f, nil
			}
		}
		return agentapi.FileDiff{}, newError(http.StatusBadRequest, "path is not among this conversation's changes")
	default:
		return agentapi.FileDiff{}, errScope
	}
}

func (m *Manager) sessionDiff(ctx context.Context, id string) ([]agentapi.FileDiff, Changes, error) {
	out := Changes{Scope: ScopeSession, Label: sessionLabel, Files: []ChangedFile{}}
	s, err := m.lookup(id)
	if err != nil {
		return nil, out, err
	}
	m.mu.Lock()
	info := m.infos[s.provider]
	m.mu.Unlock()
	if !info.Capabilities.SessionDiff {
		out.Reason = fmt.Sprintf("%s does not record file changes per conversation; use the workspace view", firstNonEmpty(info.DisplayName, s.provider))
		return nil, out, nil
	}
	if err := m.View(ctx, id); err != nil {
		return nil, out, err
	}
	m.mu.Lock()
	conv, gen, revision := s.conv, s.gen, s.nativeDiffRevision
	m.mu.Unlock()
	if conv == nil {
		// Closed: the callers serve the Git-attributed task scope instead,
		// without resuming the conversation.
		out.Scope = ScopeTask
		return nil, out, nil
	}
	files, err := m.readNativeDiff(ctx, s, conv, gen, revision)
	if errors.Is(err, agentapi.ErrUnsupported) {
		out.Reason = "the provider does not record file changes for this conversation"
		if err != agentapi.ErrUnsupported {
			out.Reason = shortError(err)
		}
		return nil, out, nil
	}
	if errors.Is(err, agentapi.ErrBusy) {
		out.Reason = "native changes are available when this Thread settles"
		return nil, out, nil
	}
	if err != nil {
		return nil, out, newError(http.StatusBadGateway, "could not read the provider's changes: %s", shortError(err))
	}
	out.Supported = true
	if len(files) > maxChangedFiles {
		files = files[:maxChangedFiles]
		out.Reason = fmt.Sprintf("showing the first %d changed files", maxChangedFiles)
	}
	return files, out, nil
}

// readNativeDiff reads conv's native session diff with each file's line
// counts. While s still has conv, gen and revision, the read becomes its
// native paths; the Task's total is its task scope, not this read. The
// caller holds no lock.
func (m *Manager) readNativeDiff(ctx context.Context, s *webSession, conv agentapi.Conversation, gen, revision uint64) ([]agentapi.FileDiff, error) {
	diffCtx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	files, err := conv.Diff(diffCtx)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for i := range files {
		if files[i].Patch != "" && !files[i].CountsUnknown {
			files[i].Additions, files[i].Deletions = countPatch(files[i].Patch)
		}
		paths = append(paths, files[i].Path)
		if files[i].OldPath != "" {
			paths = append(paths, files[i].OldPath)
		}
	}
	m.mu.Lock()
	if s.nativeChanges && !s.removed && s.conv == conv && s.gen == gen && s.nativeDiffRevision == revision {
		s.nativePaths = paths
	}
	m.mu.Unlock()
	return files, nil
}

// statusEntry is a changed path: its index (x) and work tree (y) status
// letters and, for a rename staged in the index, the path it came from.
type statusEntry struct {
	path      string
	orig      string
	status    string
	x, y      byte
	untracked bool
}

type gitRepo struct {
	git     string
	top     string
	hasHead bool
}

// openRepo resolves the repository containing workdir. A nil repo with a
// reason means the workspace view is unsupported there.
func openRepo(ctx context.Context, workdir string) (*gitRepo, string, error) {
	git, err := execpath.Resolve("git")
	if err != nil {
		return nil, "git is not installed in a standard location", nil
	}
	out, code, stderr, err := runGit(ctx, git, workdir, 4096, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, "", err
	}
	if code != 0 {
		if strings.Contains(stderr, "not a git repository") {
			return nil, "this directory is not in a Git working tree", nil
		}
		return nil, "git could not read this directory: " + gitMessage(stderr), nil
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return nil, "this directory is not in a Git working tree", nil
	}
	_, code, _, err = runGit(ctx, git, top, 4096, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return nil, "", err
	}
	return &gitRepo{git: git, top: top, hasHead: code == 0}, "", nil
}

// workspaceListing is the working tree's changes compared with HEAD, with
// the repository it read, nil when the workspace view is unsupported.
func workspaceListing(ctx context.Context, workdir string) (Changes, *gitRepo, error) {
	repo, reason, err := openRepo(ctx, workdir)
	if err != nil {
		return Changes{Scope: ScopeWorkspace, Label: workspaceLabel, Files: []ChangedFile{}}, nil, err
	}
	if repo == nil {
		return Changes{Scope: ScopeWorkspace, Label: workspaceLabel, Files: []ChangedFile{}, Reason: reason}, nil, nil
	}
	out, err := repo.listing(ctx)
	if err != nil {
		return out, nil, err
	}
	return out, repo, nil
}

// listing is the repository's changed files compared with HEAD, with their
// line counts.
func (r *gitRepo) listing(ctx context.Context) (Changes, error) {
	out := Changes{Scope: ScopeWorkspace, Label: workspaceLabel, Files: []ChangedFile{}}
	entries, truncated, err := r.status(ctx)
	if err != nil {
		return out, err
	}
	out.Supported = true
	if truncated || len(entries) > maxChangedFiles {
		out.Reason = fmt.Sprintf("showing the first %d changed files", min(len(entries), maxChangedFiles))
		entries = entries[:min(len(entries), maxChangedFiles)]
	}
	root, err := os.OpenRoot(r.top)
	if err != nil {
		return out, newError(http.StatusBadGateway, "could not open working tree: %s", shortError(err))
	}
	defer func() { _ = root.Close() }()
	stats, binary := r.numstat(ctx)
	renames := r.renameStats(ctx, entries)
	budget := untrackedBudget
	for _, e := range entries {
		file := ChangedFile{Path: e.path, Status: e.status, OldPath: e.orig, Binary: binary[e.path]}
		if e.untracked || !r.hasHead {
			file.Additions, budget, file.Binary = countLines(root, e.path, budget)
		} else if st, ok := renames[e.path]; ok && st.orig == e.orig {
			file.Additions, file.Deletions = st.additions, st.deletions
		} else if st, ok := stats[e.path]; ok {
			file.Additions, file.Deletions = st[0], st[1]
		}
		out.Files = append(out.Files, file)
	}
	return out, nil
}

// renameStat is a renamed path's line counts against the path it came from.
type renameStat struct {
	orig                 string
	additions, deletions int
}

// renameStats returns the line counts of the renames staged in entries
// compared with their old paths. git pairs the paths on their content, so a
// rename whose content changed almost wholly is left out and counts as a
// new file, as numstat has it.
func (r *gitRepo) renameStats(ctx context.Context, entries []statusEntry) map[string]renameStat {
	stats := map[string]renameStat{}
	var paths []string
	for _, e := range entries {
		if e.orig != "" {
			paths = append(paths, e.path, e.orig)
		}
	}
	if len(paths) == 0 || !r.hasHead {
		return stats
	}
	args := append([]string{literalPathspecs, "diff", noExtDiff, noTextconv, noColor, "--find-renames", "--numstat", "-z", "HEAD", "--"}, paths...)
	out, code, _, err := runGit(ctx, r.git, r.top, maxStatusBytes, args...)
	if err != nil || code != 0 {
		return stats
	}
	// A rename record is "added\tdeleted\t" followed by the old and the new
	// path as fields of their own.
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+2 < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 || parts[2] != "" {
			continue
		}
		add, errA := strconv.Atoi(parts[0])
		del, errD := strconv.Atoi(parts[1])
		if errA == nil && errD == nil {
			stats[fields[i+2]] = renameStat{orig: fields[i+1], additions: add, deletions: del}
		}
		i += 2
	}
	return stats
}

func workspaceFileDiff(ctx context.Context, workdir, path string) (agentapi.FileDiff, error) {
	repo, reason, err := openRepo(ctx, workdir)
	if err != nil {
		return agentapi.FileDiff{}, err
	}
	if repo == nil {
		return agentapi.FileDiff{}, newError(http.StatusConflict, "%s", reason)
	}
	entries, _, err := repo.status(ctx)
	if err != nil {
		return agentapi.FileDiff{}, err
	}
	var entry *statusEntry
	for i := range entries {
		if entries[i].path == path {
			entry = &entries[i]
			break
		}
	}
	// Only paths git itself reports are diffed, so the request cannot name
	// arbitrary files.
	if entry == nil {
		return agentapi.FileDiff{}, newError(http.StatusBadRequest, "path is not a changed file in this working tree")
	}
	args := []string{"diff", noExtDiff, noTextconv, noColor}
	if entry.untracked || !repo.hasHead {
		args = append(args, "--no-index", "--", "/dev/null", entry.path)
	} else {
		args = append(args, "HEAD", "--", entry.path)
	}
	patch, code, stderr, err := runGit(ctx, repo.git, repo.top, maxDiffBytes+1, args...)
	// A staged rename is diffed against the path it came from, as the
	// listing counts it; without the pair, git shows a whole new file.
	if err == nil && entry.orig != "" {
		args = []string{literalPathspecs, "diff", noExtDiff, noTextconv, noColor, "--find-renames", "HEAD", "--", entry.path, entry.orig}
		if paired, pairedCode, _, pairedErr := runGit(ctx, repo.git, repo.top, maxDiffBytes+1, args...); pairedErr == nil && pairedCode == 0 && strings.Contains(string(paired), "\nrename from ") {
			patch, code = paired, pairedCode
		}
	}
	if err != nil {
		return agentapi.FileDiff{}, err
	}
	// diff --no-index exits 1 when the files differ.
	if code != 0 && code != 1 {
		return agentapi.FileDiff{}, newError(http.StatusBadGateway, "git diff failed: %s", gitMessage(stderr))
	}
	diff := agentapi.FileDiff{Path: entry.path, Status: entry.status, OldPath: entry.orig, Patch: clampText(string(patch), maxDiffBytes)}
	diff.Additions, diff.Deletions = countPatch(diff.Patch)
	// Without --binary, git names a binary change in one line and no hunk.
	diff.Binary = binaryPatch(diff.Patch)
	return diff, nil
}

func (r *gitRepo) status(ctx context.Context) ([]statusEntry, bool, error) {
	out, code, stderr, err := runGit(ctx, r.git, r.top, maxStatusBytes+1, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, false, err
	}
	if code != 0 {
		return nil, false, newError(http.StatusBadGateway, "git status failed: %s", gitMessage(stderr))
	}
	truncated := len(out) > maxStatusBytes
	if truncated {
		// The last record may be cut short: keep only whole ones.
		out = out[:bytes.LastIndexByte(out[:maxStatusBytes], 0)+1]
	}
	return parseStatus(out), truncated, nil
}

// parseStatus reads `git status --porcelain=v1 -z`: "XY path\0", with the
// original path following as its own field for renames and copies.
func parseStatus(out []byte) []statusEntry {
	var entries []statusEntry
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 || f[2] != ' ' {
			continue
		}
		x, y := f[0], f[1]
		e := statusEntry{path: f[3:], status: statusName(x, y), x: x, y: y, untracked: x == '?' && y == '?'}
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++ // the original path
			if x == 'R' && i < len(fields) {
				e.orig = fields[i]
			}
		}
		entries = append(entries, e)
	}
	return entries
}

func statusName(x, y byte) string {
	switch {
	case x == '?' && y == '?':
		return "untracked"
	case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
		return "conflicted"
	case x == 'R' || y == 'R':
		return "renamed"
	case x == 'C' || y == 'C':
		return "copied"
	case x == 'A':
		return "added"
	case x == 'D' || y == 'D':
		return "deleted"
	default:
		return "modified"
	}
}

// numstat returns additions/deletions per tracked path versus HEAD, and the
// paths git counts as binary ("-" for both). Missing entries (failures)
// simply show no counts.
func (r *gitRepo) numstat(ctx context.Context) (map[string][2]int, map[string]bool) {
	stats, binary := map[string][2]int{}, map[string]bool{}
	if !r.hasHead {
		return stats, binary
	}
	out, code, _, err := runGit(ctx, r.git, r.top, maxStatusBytes, "diff", noExtDiff, noTextconv, noColor, "--no-renames", "--numstat", "-z", "HEAD", "--")
	if err != nil || code != 0 {
		return stats, binary
	}
	for _, rec := range strings.Split(string(out), "\x00") {
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "-" && parts[1] == "-" {
			binary[parts[2]] = true
			continue
		}
		add, errA := strconv.Atoi(parts[0])
		del, errD := strconv.Atoi(parts[1])
		if errA != nil || errD != nil {
			continue
		}
		stats[parts[2]] = [2]int{add, del}
	}
	return stats, binary
}

// countLines counts lines of a new file within a shared read budget. A file
// whose read part holds a NUL byte is binary, as git has it, with no lines.
func countLines(root *os.Root, path string, budget int) (lines, left int, binary bool) {
	if budget <= 0 {
		return 0, budget, false
	}
	// Root confines parent-directory resolution even when a directory changes
	// to a symlink after git lists it. Reject final symlinks and special files:
	// a FIFO could block, and an outside link could disclose its target's size.
	if info, err := root.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return 0, budget, false
	}
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0, budget, false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, int64(min(budget, maxUntrackedRead))))
	if err != nil {
		return 0, budget - len(data), false
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return 0, budget - len(data), true
	}
	lines = bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	return lines, budget - len(data), false
}

// binaryPatch reports whether git's patch says the file is binary: a
// "Binary files … differ" line outside any hunk.
func binaryPatch(patch string) bool {
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			return false
		}
		if strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ") {
			return true
		}
	}
	return false
}

func countPatch(patch string) (additions, deletions int) {
	inHunk := false
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inHunk = false
		case strings.HasPrefix(line, "@@ "):
			inHunk = true
		case inHunk && strings.HasPrefix(line, "+"):
			additions++
		case inHunk && strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}

// Why a Project has no git views (Project.NoGit).
const (
	noGitInstalled  = "not_installed"
	noGitRepository = "not_repository"
)

// readBranch returns the branch checked out in the git work tree containing
// dir, made safe to show, and why dir has no git views, if it has none. The
// branch is empty when dir is not in a work tree, HEAD is detached, or git
// cannot tell within branchTimeout; noGit is set only when git is missing or
// says dir is in no work tree.
func readBranch(ctx context.Context, dir string) (branch, noGit string) {
	git, err := execpath.Resolve("git")
	if err != nil {
		return "", noGitInstalled
	}
	ctx, cancel := context.WithTimeout(ctx, branchTimeout)
	defer cancel()
	out, code, stderr, err := runGit(ctx, git, dir, 4096, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || code != 0 {
		if err == nil && strings.Contains(stderr, "not a git repository") {
			return "", noGitRepository
		}
		return "", ""
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "refs/heads/")
	if !ok {
		return "", ""
	}
	return clipRunes(displaytext.Sanitize(branch), maxNameRunes), ""
}

// refreshBranches re-reads the branch of each Project in ids, or of every
// Project when there are none, and publishes each Project whose branch, or
// whether it has git views, changed; the Threads of those Projects get the
// branch of their own folder, a worktree's included. Unless force, a branch
// read within branchTTL is kept. Git runs outside mu, for all folders at
// once, so a listing waits at most about branchTimeout however many there
// are.
func (m *Manager) refreshBranches(ctx context.Context, force bool, ids ...string) {
	m.branchMu.Lock()
	defer m.branchMu.Unlock()
	m.mu.Lock()
	if len(ids) == 0 {
		for id := range m.projects {
			ids = append(ids, id)
		}
	}
	dirs := map[string]string{}
	folders := map[string]bool{}
	for _, id := range ids {
		if p := m.projects[id]; p != nil && (force || time.Since(m.branchAt[id]) >= branchTTL) {
			dirs[id] = p.Dir
			folders[p.Dir] = true
		}
	}
	for _, s := range m.sessions {
		if _, ok := dirs[s.projectID]; ok && !s.removed {
			folders[s.workdir] = true
		}
	}
	m.mu.Unlock()
	type read struct {
		branch string
		noGit  string
	}
	branches := make(map[string]read, len(folders))
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for dir := range folders {
		wg.Go(func() {
			branch, noGit := readBranch(ctx, dir)
			mu.Lock()
			branches[dir] = read{branch, noGit}
			mu.Unlock()
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return // the caller is gone; this says nothing about the branches
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, dir := range dirs {
		p := m.projects[id]
		if p == nil {
			continue // removed while git ran
		}
		r := branches[dir]
		m.branchAt[id] = time.Now()
		if p.Branch != r.branch || p.NoGit != r.noGit {
			p.Branch, p.NoGit = r.branch, r.noGit
			m.publishProjectLocked(*p)
		}
	}
	// The unsaved work of the same Projects' repositories is read with
	// their branches.
	m.kickGitPendingLocked(m.pendingDirsLocked(slices.Collect(maps.Keys(dirs))))
	for _, s := range m.sessions {
		r, read := branches[s.workdir]
		if r.branch == "" && s.place != nil && s.place.Worktree != "" {
			r.branch = s.place.Branch // its worktree was removed; the branch is kept
		}
		if _, refreshed := dirs[s.projectID]; !refreshed || !read || s.removed || s.branch == r.branch {
			continue
		}
		before := m.summaryLocked(s)
		s.branch = r.branch
		m.changedLocked(s, before)
	}
}

// kickBranchLocked re-reads a Project's branch in the background.
func (m *Manager) kickBranchLocked(projectID string) {
	if m.closed {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.refreshBranches(m.ctx, true, projectID)
	}()
}

// runGit runs one read-only git command in dir with structured arguments
// (no shell) and a timeout, keeping at most limit bytes of output.
func runGit(ctx context.Context, git, dir string, limit int, args ...string) ([]byte, int, string, error) {
	return runGitInput(ctx, git, dir, nil, limit, args...)
}

// runGitInput is runGit with stdin as git's standard input.
func runGitInput(ctx context.Context, git, dir string, stdin io.Reader, limit int, args ...string) ([]byte, int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	argv := append([]string{"-C", dir}, gitBase...)
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, git, argv...) // #nosec G204 G702 -- resolved git binary, fixed commands, user paths after --, no shell.
	cmd.Stdin = stdin
	// Optional locks off keeps `git status` from rewriting the index.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_PAGER=cat")
	stdout := &cappedBuffer{limit: limit}
	stderr := &cappedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, 0, "", newError(http.StatusGatewayTimeout, "git did not finish in time")
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.buf.Bytes(), exitErr.ExitCode(), stderr.buf.String(), nil
	}
	if err != nil {
		return nil, 0, "", newError(http.StatusBadGateway, "git failed: %s", shortError(err))
	}
	return stdout.buf.Bytes(), 0, stderr.buf.String(), nil
}

// cappedBuffer keeps the first limit bytes and discards the rest while still
// draining the writer, so a huge output cannot exhaust memory.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// gitMessage is git's first stderr line made safe and short enough to show.
func gitMessage(stderr string) string {
	return clipRunes(displaytext.Sanitize(firstLine(stderr)), maxDetailRunes)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// repoPaths returns a function mapping a tool's file path, relative to dir
// or absolute, to its slash-separated path in the work tree at top. dir may
// reach the work tree through a symbolic link, as a Task's tools see it.
func repoPaths(top, dir string) func(string) (string, bool) {
	dir = filepath.Clean(dir)
	// Relative paths count only from a dir inside the work tree: another
	// repository's README.md is not this one's.
	roots := []struct{ dir, prefix string }{{top, ""}}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		if rel, err := filepath.Rel(top, real); err == nil && filepath.IsLocal(rel) {
			roots = append([]struct{ dir, prefix string }{{dir, rel}}, roots...)
		}
	}
	return func(p string) (string, bool) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		for _, root := range roots {
			if rel, err := filepath.Rel(root.dir, p); err == nil && filepath.IsLocal(rel) && rel != "." {
				return filepath.ToSlash(filepath.Join(root.prefix, rel)), true
			}
		}
		return "", false
	}
}
