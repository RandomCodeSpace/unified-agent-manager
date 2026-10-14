package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Settle, Archive and Delete remove a Thread's worktree and keep its branch;
// git refuses a worktree with changes, which then stays until the owner
// removes it in Settings → Worktrees. Using the Thread again recreates it
// from the branch.
const (
	// removedLogFile keeps the last worktrees removed on settling.
	removedLogFile = "web-worktrees-removed.json"
	maxRemovedLog  = 20
	// A worktree's size is measured in the background, at most this often,
	// reading at most sizeEntries entries for at most sizeTime.
	sizeTTL     = 10 * time.Minute
	sizeEntries = 1 << 20
	sizeTime    = 30 * time.Second
	// lowDiskShare is the share of free space below which uam warns.
	lowDiskShare        = 0.10
	codeWorktreeUnsaved = "worktree_unsaved"
)

// DiskInfo is the filesystem holding the folder new worktrees go in. Low is
// set below lowDiskShare free.
type DiskInfo struct {
	FreeBytes  uint64 `json:"free_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
	Low        bool   `json:"low"`
}

// WorktreeEntry is one folder <worktrees folder>/<project>/uam-… and the
// Thread it was made for, if one still has it.
type WorktreeEntry struct {
	Path      string `json:"path"`
	Branch    string `json:"branch,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Thread    string `json:"thread,omitempty"`
	Stage     string `json:"stage,omitempty"`
	// SizeBytes is omitted until measured; SizePartial says the walk
	// stopped early, so the size is at least that.
	SizeBytes   *int64 `json:"size_bytes,omitempty"`
	SizePartial bool   `json:"size_partial,omitempty"`
	// Uncommitted counts changed files, untracked ones included, which a
	// removal loses; Unpushed counts commits on no remote.
	Uncommitted int `json:"uncommitted"`
	Unpushed    int `json:"unpushed"`
}

// RemovedWorktree is a worktree removed on its own when its Thread settled,
// was archived or deleted.
type RemovedWorktree struct {
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	ProjectID string    `json:"project_id,omitempty"`
	TaskID    string    `json:"task_id,omitempty"`
	Thread    string    `json:"thread,omitempty"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
	At        time.Time `json:"at"`
}

// Worktrees answers GET /api/worktrees, biggest first. Root is the folder
// new worktrees go in, DefaultRoot the one used without a setting; the list
// covers every folder worktrees went in.
type Worktrees struct {
	Root        string            `json:"root"`
	DefaultRoot string            `json:"default_root,omitempty"`
	Disk        *DiskInfo         `json:"disk,omitempty"`
	Worktrees   []WorktreeEntry   `json:"worktrees"`
	Removed     []RemovedWorktree `json:"removed"`
}

type worktreeSize struct {
	bytes   int64
	partial bool
	at      time.Time
}

// worktreeState is the Manager's worktree bookkeeping: ops serialises
// removing and recreating; the rest is under mu.
type worktreeState struct {
	ops       sync.Mutex
	sizes     map[string]worktreeSize
	queue     []string
	measuring bool
	removed   []RemovedWorktree
	loaded    bool
	// keep counts the Threads being made in each worktree (Run again);
	// removal on settling leaves those.
	keep map[string]int
}

// diskInfo reads the filesystem holding dir, or its nearest existing
// parent; nil when it cannot.
func diskInfo(dir string) *DiskInfo {
	for {
		var st unix.Statfs_t
		if err := unix.Statfs(dir, &st); err == nil {
			bsize := uint64(st.Bsize) // #nosec G115 -- a block size is never negative; its type differs per platform
			total, free := st.Blocks*bsize, st.Bavail*bsize
			if total == 0 {
				return nil
			}
			return &DiskInfo{FreeBytes: free, TotalBytes: total, Low: float64(free) < lowDiskShare*float64(total)}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// Disk is the filesystem holding the worktrees, for /api/meta.
func (m *Manager) Disk() *DiskInfo {
	root, err := m.worktreesDir()
	if err != nil {
		return nil
	}
	return diskInfo(root)
}

// ListWorktrees lists the worktree folders in every worktrees folder
// (worktreeRoots) with the Thread each belongs to, what is not saved in it
// and its size, measuring unknown or old sizes in the background.
func (m *Manager) ListWorktrees(ctx context.Context) (Worktrees, error) {
	root, err := m.worktreesDir()
	if err != nil {
		return Worktrees{}, err
	}
	out := Worktrees{Root: root, Disk: diskInfo(root), Worktrees: []WorktreeEntry{}, Removed: m.removedLog()}
	out.DefaultRoot, _ = m.defaultWorktreesDir()
	for _, dir := range m.allWorktreeDirs() {
		out.Worktrees = append(out.Worktrees, WorktreeEntry{Path: dir})
	}
	// A worktree recreated since it was removed on settling is listed once,
	// as it is now.
	out.Removed = slices.DeleteFunc(out.Removed, func(r RemovedWorktree) bool {
		return slices.ContainsFunc(out.Worktrees, func(e WorktreeEntry) bool { return e.Path == r.Path })
	})
	var wg sync.WaitGroup
	for i := range out.Worktrees {
		e := &out.Worktrees[i]
		wg.Go(func() { e.Branch, e.Uncommitted, e.Unpushed = worktreePending(ctx, e.Path) })
	}
	wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	threads := m.worktreeThreadsLocked()
	var stale []string
	for i := range out.Worktrees {
		e := &out.Worktrees[i]
		if users := threads[e.Path]; len(users) > 0 {
			s := users[0]
			e.TaskID, e.ProjectID, e.Stage = s.id, s.projectID, stageName(s.stage)
			e.Thread = clipRunes(displaytext.Sanitize(firstNonEmpty(s.name, s.title)), maxNameRunes)
		}
		size, ok := m.worktrees.sizes[e.Path]
		if ok {
			e.SizeBytes, e.SizePartial = &size.bytes, size.partial
		}
		if !ok || time.Since(size.at) > sizeTTL {
			stale = append(stale, e.Path)
		}
	}
	m.measureLocked(stale)
	slices.SortStableFunc(out.Worktrees, func(a, b WorktreeEntry) int {
		var x, y int64
		if a.SizeBytes != nil {
			x = *a.SizeBytes
		}
		if b.SizeBytes != nil {
			y = *b.SizeBytes
		}
		if x != y {
			return int(min(max(y-x, -1), 1))
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out, nil
}

// allWorktreeDirs are the worktree folders of every worktrees folder.
func (m *Manager) allWorktreeDirs() []string {
	var out []string
	for _, root := range m.worktreeRoots() {
		out = append(out, worktreeDirs(root)...)
	}
	return out
}

// worktreeDirs are the folders uam-… two levels under root, links left out:
// the names uam gives worktrees (makeWorktree), so a folder the owner chose
// for worktrees lists none of its other folders.
func worktreeDirs(root string) []string {
	var out []string
	projects, _ := os.ReadDir(root)
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		trees, _ := os.ReadDir(filepath.Join(root, p.Name()))
		for _, t := range trees {
			if t.IsDir() && strings.HasPrefix(t.Name(), strings.ReplaceAll(branchPrefix, "/", "-")) {
				out = append(out, filepath.Join(root, p.Name(), t.Name()))
			}
		}
	}
	return out
}

// worktreeThreadsLocked maps each worktree path to the Threads in it: the
// one made for it and those Run again made there, active ones first, then
// the newest.
func (m *Manager) worktreeThreadsLocked() map[string][]*webSession {
	out := map[string][]*webSession{}
	for _, s := range m.sessions {
		if !s.removed && s.place != nil && s.place.Worktree != "" {
			out[s.place.Worktree] = append(out[s.place.Worktree], s)
		}
	}
	for _, users := range out {
		slices.SortFunc(users, func(a, b *webSession) int {
			if x, y := a.stage == StageActive, b.stage == StageActive; x != y {
				if x {
					return -1
				}
				return 1
			}
			return b.createdAt.Compare(a.createdAt)
		})
	}
	return out
}

// worktreeInUseLocked says whether a Thread is active, or being made, in
// the worktree at path.
func (m *Manager) worktreeInUseLocked(path string) bool {
	users := m.worktreeThreadsLocked()[path]
	return m.worktrees.keep[path] > 0 || len(users) > 0 && users[0].stage == StageActive
}

// worktreePending reads the branch of the worktree at dir, its changed
// files (untracked included) and its commits on no remote.
func worktreePending(ctx context.Context, dir string) (branch string, uncommitted, unpushed int) {
	repo, _, err := openRepo(ctx, dir)
	if err != nil || repo == nil {
		return "", 0, 0
	}
	branch, _ = readBranch(ctx, dir)
	if entries, _, err := repo.status(ctx); err == nil {
		uncommitted = len(entries)
	}
	if out, code, _, err := runGit(ctx, repo.git, repo.top, 64, "rev-list", "--count", "HEAD", "--not", "--remotes"); err == nil && code == 0 {
		unpushed, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	return branch, uncommitted, unpushed
}

// measureLocked queues paths for the background size walk.
func (m *Manager) measureLocked(paths []string) {
	for _, p := range paths {
		if !slices.Contains(m.worktrees.queue, p) {
			m.worktrees.queue = append(m.worktrees.queue, p)
		}
	}
	if m.worktrees.measuring || len(m.worktrees.queue) == 0 || m.closed {
		return
	}
	m.worktrees.measuring = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			m.mu.Lock()
			if len(m.worktrees.queue) == 0 || m.ctx.Err() != nil {
				m.worktrees.measuring = false
				m.mu.Unlock()
				return
			}
			p := m.worktrees.queue[0]
			m.worktrees.queue = m.worktrees.queue[1:]
			m.mu.Unlock()
			bytes, partial := measureDir(m.ctx, p)
			m.mu.Lock()
			if m.worktrees.sizes == nil {
				m.worktrees.sizes = map[string]worktreeSize{}
			}
			// The Threads' summaries carry the size for their branch chips.
			users := m.worktreeThreadsLocked()[p]
			befores := make([]SessionSummary, len(users))
			for i, s := range users {
				befores[i] = m.summaryLocked(s)
			}
			m.worktrees.sizes[p] = worktreeSize{bytes: bytes, partial: partial, at: time.Now()}
			for i, s := range users {
				m.changedLocked(s, befores[i])
			}
			m.mu.Unlock()
		}
	}()
}

// kickWorktreeSizeLocked measures the worktree of s again once its size is
// older than sizeTTL, as a turn of s ended.
func (m *Manager) kickWorktreeSizeLocked(s *webSession) {
	if s.place == nil || s.place.Worktree == "" {
		return
	}
	if size, ok := m.worktrees.sizes[s.place.Worktree]; !ok || time.Since(size.at) > sizeTTL {
		m.measureLocked([]string{s.place.Worktree})
	}
}

// measureDir adds up the regular files under dir, links not followed, and
// says whether it stopped early.
func measureDir(ctx context.Context, dir string) (int64, bool) {
	deadline := time.Now().Add(sizeTime)
	var total int64
	entries := 0
	stopped := errors.New("stopped")
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable parts are left out
		}
		if entries++; entries > sizeEntries || ctx.Err() != nil || entries%1024 == 0 && time.Now().After(deadline) {
			return stopped
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, errors.Is(err, stopped)
}

// removeWorktree removes the worktree at path and keeps its branch. Without
// force git refuses one with changed or untracked files.
func removeWorktree(ctx context.Context, path string, force bool) error {
	git, err := lookGit()
	if err != nil {
		return err
	}
	common, code, stderr, err := runGit(ctx, git, path, 4096, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	if code != 0 {
		return newError(http.StatusConflict, "%s is not a git worktree: %s", path, gitMessage(stderr))
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	if _, err := runGitWrite(ctx, strings.TrimSpace(string(common)), nil, append(args, path)...); err != nil {
		return gitFailed("Could not remove the worktree", err)
	}
	return nil
}

// kickWorktreeRemoveLocked removes s's worktree in the background, as its
// Thread settled, was archived or deleted. One with changes is kept.
func (m *Manager) kickWorktreeRemoveLocked(s *webSession) {
	if m.closed || s.place == nil || s.place.Worktree == "" {
		return
	}
	path, branch, project, id := s.place.Worktree, s.place.Branch, s.projectID, s.id
	thread := clipRunes(displaytext.Sanitize(firstNonEmpty(s.name, s.title)), maxNameRunes)
	size, known := m.worktrees.sizes[path]
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.worktrees.ops.Lock()
		defer m.worktrees.ops.Unlock()
		if info, err := os.Lstat(path); err != nil || !info.IsDir() {
			return
		}
		m.mu.Lock()
		inUse := m.worktreeInUseLocked(path)
		m.mu.Unlock()
		if inUse {
			return // reopened meanwhile, or another Thread runs there
		}
		if !known {
			size.bytes, _ = measureDir(m.ctx, path)
		}
		if err := removeWorktree(m.ctx, path, false); err != nil {
			log.Info("web thread worktree kept", "session", id, "path", path, "error", err)
			return
		}
		log.Info("web thread worktree removed", "session", id, "path", path)
		m.mu.Lock()
		delete(m.worktrees.sizes, path)
		m.mu.Unlock()
		m.logRemoved(RemovedWorktree{Path: path, Branch: branch, ProjectID: project, TaskID: id, Thread: thread, SizeBytes: size.bytes, At: m.now()})
	}()
}

// restoreWorktree recreates the worktree of an active Thread from its
// branch when it was removed, before its conversation opens there. The
// caller holds s.op.
func (m *Manager) restoreWorktree(ctx context.Context, s *webSession) {
	m.mu.Lock()
	var path, branch, dir string
	if s.place != nil && s.place.Worktree != "" && s.stage == StageActive {
		path, branch = s.place.Worktree, s.place.Branch
		if p := m.projects[s.projectID]; p != nil {
			dir = p.Dir
		}
	}
	m.mu.Unlock()
	if path == "" || dir == "" {
		return
	}
	if err := m.recreateWorktree(ctx, dir, path, branch); err != nil {
		log.Warn("web thread worktree not recreated", "session", s.id, "path", path, "error", err)
	}
}

// recreateWorktree adds the worktree at path back on branch, in the
// repository holding dir, when it was removed.
func (m *Manager) recreateWorktree(ctx context.Context, dir, path, branch string) error {
	m.worktrees.ops.Lock()
	defer m.worktrees.ops.Unlock()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	repo, reason, err := openRepo(ctx, dir)
	if err != nil {
		return err
	}
	if repo == nil {
		return newError(http.StatusConflict, "%s", reason)
	}
	// A folder deleted by hand is still registered until pruned.
	_, _ = runGitWrite(ctx, repo.top, nil, "worktree", "prune")
	if _, err := runGitWrite(ctx, repo.top, nil, "worktree", "add", path, branch); err != nil {
		return gitFailed("Could not recreate the worktree", err)
	}
	log.Info("web thread worktree recreated", "path", path, "branch", branch)
	return nil
}

// keepWorktree recreates the worktree of place, in the repository holding
// dir, when it was removed, and keeps it from removal on settling until
// release runs, as a Thread is made there.
func (m *Manager) keepWorktree(ctx context.Context, dir string, place *store.WebPlace) (release func(), err error) {
	path := place.Worktree
	m.mu.Lock()
	if m.worktrees.keep == nil {
		m.worktrees.keep = map[string]int{}
	}
	m.worktrees.keep[path]++
	m.mu.Unlock()
	release = func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.worktrees.keep[path]--; m.worktrees.keep[path] <= 0 {
			delete(m.worktrees.keep, path)
		}
	}
	if err := m.recreateWorktree(ctx, dir, path, place.Branch); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// RemoveWorktree removes a listed worktree on the owner's word. One with
// changes needs force; one whose Thread is working is refused; the open
// conversation of its Thread is closed first, and its next use recreates
// the worktree from the branch.
func (m *Manager) RemoveWorktree(ctx context.Context, path string, force bool) error {
	if !slices.Contains(m.allWorktreeDirs(), path) {
		return newError(http.StatusNotFound, "worktree not found")
	}
	m.mu.Lock()
	users := slices.Clone(m.worktreeThreadsLocked()[path])
	m.mu.Unlock()
	// Held in ID order, so two removals cannot wait on each other.
	slices.SortFunc(users, func(a, b *webSession) int { return strings.Compare(a.id, b.id) })
	for _, s := range users {
		s.op.Lock()
		defer s.op.Unlock()
	}
	m.mu.Lock()
	for _, s := range users {
		if err := s.settleableLocked(); err != nil {
			m.mu.Unlock()
			return newError(http.StatusConflict, "“%s” is working in this worktree. Wait for it to finish.", clipRunes(displaytext.Sanitize(firstNonEmpty(s.name, s.title, "The Thread")), maxNameRunes))
		}
	}
	var convs []agentapi.Conversation
	for _, s := range users {
		if s.conv != nil {
			before := m.summaryLocked(s)
			convs = append(convs, m.suspendLocked(s, false))
			m.changedLocked(s, before)
		}
	}
	m.mu.Unlock()
	for _, conv := range convs {
		if conv != nil {
			m.closeConversation(conv)
		}
	}
	m.worktrees.ops.Lock()
	defer m.worktrees.ops.Unlock()
	if !force {
		if _, uncommitted, _ := worktreePending(ctx, path); uncommitted > 0 {
			return &Error{Status: http.StatusConflict, Code: codeWorktreeUnsaved, Refs: []string{strconv.Itoa(uncommitted)},
				Message: strconv.Itoa(uncommitted) + " changed " + plural(uncommitted, "file is", "files are") + " not committed and would be lost."}
		}
	}
	if err := removeWorktree(ctx, path, force); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.worktrees.sizes, path)
	m.mu.Unlock()
	log.Info("web worktree removed by the owner", "path", path)
	return nil
}

// removedLog is the worktrees removed on settling, newest first; empty, never
// nil, so the list serves [] rather than null.
func (m *Manager) removedLog() []RemovedWorktree {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadRemovedLocked()
	return append([]RemovedWorktree{}, m.worktrees.removed...)
}

func (m *Manager) removedLogPath() string {
	return filepath.Join(filepath.Dir(m.store.Path()), removedLogFile)
}

func (m *Manager) loadRemovedLocked() {
	if m.worktrees.loaded {
		return
	}
	m.worktrees.loaded = true
	data, err := os.ReadFile(m.removedLogPath())
	if err != nil {
		return
	}
	var list []RemovedWorktree
	if json.Unmarshal(data, &list) == nil {
		m.worktrees.removed = list[:min(len(list), maxRemovedLog)]
	}
	if m.worktrees.removed == nil {
		m.worktrees.removed = []RemovedWorktree{}
	}
}

// logRemoved records a removal on settling, keeping the newest
// maxRemovedLog.
func (m *Manager) logRemoved(r RemovedWorktree) {
	m.mu.Lock()
	m.loadRemovedLocked()
	list := append([]RemovedWorktree{r}, m.worktrees.removed...)
	m.worktrees.removed = list[:min(len(list), maxRemovedLog)]
	list = slices.Clone(m.worktrees.removed)
	m.mu.Unlock()
	data, err := json.Marshal(list)
	if err == nil {
		err = writeFileAtomic(m.removedLogPath(), ".worktrees-*", data)
	}
	if err != nil {
		log.Warn("save the removed worktrees failed", "error", err)
	}
}

func (s *Server) handleWorktrees(w http.ResponseWriter, r *http.Request) {
	list, err := s.m.ListWorktrees(r.Context())
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRemoveWorktree(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path  string `json:"path"`
		Force bool   `json:"force"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.m.RemoveWorktree(r.Context(), body.Path, body.Force); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
