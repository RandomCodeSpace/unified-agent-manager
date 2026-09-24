package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

const (
	workspaceLabel = "Whole working tree compared with HEAD. It may include changes that were not made by this session."
	sessionLabel   = "File changes the provider recorded for this conversation."

	gitTimeout       = 10 * time.Second
	maxStatusBytes   = 4 << 20
	maxDiffBytes     = 1 << 20
	maxChangedFiles  = 1000
	maxUntrackedRead = 256 << 10
	untrackedBudget  = 8 << 20
)

// gitBase is prepended to every git invocation: read-only, no fsmonitor, no
// hooks, and no external diff or textconv programs.
var gitBase = []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}

// Changes lists changed files for a session in the requested scope.
func (m *Manager) Changes(ctx context.Context, id, scope string) (Changes, error) {
	switch scope {
	case ScopeWorkspace:
		s, err := m.lookup(id)
		if err != nil {
			return Changes{}, err
		}
		m.mu.Lock()
		workdir := s.workdir
		m.mu.Unlock()
		return workspaceChanges(ctx, workdir)
	case ScopeSession:
		files, out, err := m.sessionDiff(ctx, id)
		if err != nil || !out.Supported {
			return out, err
		}
		for _, f := range files {
			out.Files = append(out.Files, ChangedFile{Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions})
		}
		return out, nil
	default:
		return Changes{}, newError(http.StatusBadRequest, "scope must be %q or %q", ScopeSession, ScopeWorkspace)
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
	case ScopeSession:
		files, out, err := m.sessionDiff(ctx, id)
		if err != nil {
			return agentapi.FileDiff{}, err
		}
		if !out.Supported {
			return agentapi.FileDiff{}, newError(http.StatusConflict, "%s", out.Reason)
		}
		for _, f := range files {
			if f.Path == path {
				f.Before = clampText(f.Before, maxDiffBytes)
				f.After = clampText(f.After, maxDiffBytes)
				f.Patch = clampText(f.Patch, maxDiffBytes)
				return f, nil
			}
		}
		return agentapi.FileDiff{}, newError(http.StatusBadRequest, "path is not among this conversation's changes")
	default:
		return agentapi.FileDiff{}, newError(http.StatusBadRequest, "scope must be %q or %q", ScopeSession, ScopeWorkspace)
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
	conv := s.conv
	m.mu.Unlock()
	if conv == nil {
		return nil, out, newError(http.StatusConflict, "the provider conversation is not open")
	}
	diffCtx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	files, err := conv.Diff(diffCtx)
	if errors.Is(err, agentapi.ErrUnsupported) {
		out.Reason = "the provider does not record file changes for this conversation"
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

type statusEntry struct {
	path      string
	status    string
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

func workspaceChanges(ctx context.Context, workdir string) (Changes, error) {
	out := Changes{Scope: ScopeWorkspace, Label: workspaceLabel, Files: []ChangedFile{}}
	repo, reason, err := openRepo(ctx, workdir)
	if err != nil {
		return out, err
	}
	if repo == nil {
		out.Reason = reason
		return out, nil
	}
	entries, truncated, err := repo.status(ctx)
	if err != nil {
		return out, err
	}
	out.Supported = true
	if truncated || len(entries) > maxChangedFiles {
		out.Reason = fmt.Sprintf("showing the first %d changed files", min(len(entries), maxChangedFiles))
		entries = entries[:min(len(entries), maxChangedFiles)]
	}
	root, err := os.OpenRoot(repo.top)
	if err != nil {
		return out, newError(http.StatusBadGateway, "could not open working tree: %s", shortError(err))
	}
	defer func() { _ = root.Close() }()
	stats := repo.numstat(ctx)
	budget := untrackedBudget
	for _, e := range entries {
		file := ChangedFile{Path: e.path, Status: e.status}
		if e.untracked || !repo.hasHead {
			file.Additions, budget = countLines(root, e.path, budget)
		} else if st, ok := stats[e.path]; ok {
			file.Additions, file.Deletions = st[0], st[1]
		}
		out.Files = append(out.Files, file)
	}
	return out, nil
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
	var args []string
	if entry.untracked || !repo.hasHead {
		args = []string{"diff", "--no-index", "--no-ext-diff", "--no-textconv", "--no-color", "--", "/dev/null", entry.path}
	} else {
		args = []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "HEAD", "--", entry.path}
	}
	patch, code, stderr, err := runGit(ctx, repo.git, repo.top, maxDiffBytes+1, args...)
	if err != nil {
		return agentapi.FileDiff{}, err
	}
	// diff --no-index exits 1 when the files differ.
	if code != 0 && code != 1 {
		return agentapi.FileDiff{}, newError(http.StatusBadGateway, "git diff failed: %s", gitMessage(stderr))
	}
	diff := agentapi.FileDiff{Path: entry.path, Status: entry.status, Patch: clampText(string(patch), maxDiffBytes)}
	diff.Additions, diff.Deletions = countPatch(diff.Patch)
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
		entries = append(entries, statusEntry{path: f[3:], status: statusName(x, y), untracked: x == '?' && y == '?'})
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++ // skip the original path
		}
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

// numstat returns additions/deletions per tracked path versus HEAD. Missing
// entries (binary files, failures) simply show no counts.
func (r *gitRepo) numstat(ctx context.Context) map[string][2]int {
	stats := map[string][2]int{}
	if !r.hasHead {
		return stats
	}
	out, code, _, err := runGit(ctx, r.git, r.top, maxStatusBytes, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--numstat", "-z", "HEAD", "--")
	if err != nil || code != 0 {
		return stats
	}
	for _, rec := range strings.Split(string(out), "\x00") {
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		add, errA := strconv.Atoi(parts[0])
		del, errD := strconv.Atoi(parts[1])
		if errA != nil || errD != nil {
			continue
		}
		stats[parts[2]] = [2]int{add, del}
	}
	return stats
}

// countLines counts lines of a new file within a shared read budget.
func countLines(root *os.Root, path string, budget int) (int, int) {
	if budget <= 0 {
		return 0, budget
	}
	// Root confines parent-directory resolution even when a directory changes
	// to a symlink after git lists it. Reject final symlinks and special files:
	// a FIFO could block, and an outside link could disclose its target's size.
	if info, err := root.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return 0, budget
	}
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0, budget
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, int64(min(budget, maxUntrackedRead))))
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return 0, budget - len(data)
	}
	lines := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	return lines, budget - len(data)
}

func countPatch(patch string) (additions, deletions int) {
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}

// runGit runs one read-only git command in dir with structured arguments
// (no shell) and a timeout, keeping at most limit bytes of output.
func runGit(ctx context.Context, git, dir string, limit int, args ...string) ([]byte, int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	argv := append([]string{"-C", dir}, gitBase...)
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, git, argv...) // #nosec G204 G702 -- resolved git binary, fixed commands, user paths after --, no shell.
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
