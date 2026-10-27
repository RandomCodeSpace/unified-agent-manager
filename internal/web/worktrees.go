package web

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Where a new Thread runs (PlaceRequest.Mode).
const (
	placeCurrent  = "current"
	placeBranch   = "branch"
	placeWorktree = "worktree"
	// placeSwitch checks out an existing branch in the Project's folder.
	placeSwitch = "switch"
	// branchPrefix starts every branch uam makes for a Thread.
	branchPrefix = "uam/"
	// maxSlugBytes caps the title part of a branch name and a folder name.
	maxSlugBytes = 48
	// maxBranchRefs caps GET /api/projects/{id}/branches.
	maxBranchRefs   = 500
	codeBranchBusy  = "branch_busy"
	codeBranchDirty = "branch_dirty"
)

// PlaceRequest is where a new Thread runs (CreateRequest.Place): on the
// Project's current branch, on a new branch made in the Project's folder,
// on an existing branch the folder switches to, or on a new branch in a new
// git worktree started from Base ("" is HEAD). It is also the body of
// POST /api/sessions/{id}/git/branch (modes branch and switch).
type PlaceRequest struct {
	Mode string `json:"mode"`
	Base string `json:"base,omitempty"`
	// Branch is the branch mode switch checks out: a local one, or with
	// Remote a remote-tracking one ("origin/main"), which a new local
	// branch of the same name then tracks.
	Branch string `json:"branch,omitempty"`
	Remote bool   `json:"remote,omitempty"`
}

// WorktreeInfo is the worktree a Thread runs in and the branch made for it.
type WorktreeInfo struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	// SizeBytes is its size on disk once measured in the background.
	SizeBytes int64 `json:"size_bytes,omitempty"`
}

// BranchEntry is one branch a new worktree can start from: a local branch,
// or a remote-tracking one named with its remote ("origin/main").
type BranchEntry struct {
	Name      string    `json:"name"`
	Remote    bool      `json:"remote"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// BranchList answers GET /api/projects/{id}/branches, most recently
// committed first. Reason says why it is empty or cut short. Busy and
// Uncommitted are what keeps the Project's folder from switching branch
// now (checkSwitch): the first Thread not finished there, and its changed
// tracked files.
type BranchList struct {
	Branches    []BranchEntry `json:"branches"`
	Reason      string        `json:"reason,omitempty"`
	Busy        *ThreadRef    `json:"busy,omitempty"`
	Uncommitted int           `json:"uncommitted"`
}

// ThreadRef names a Thread.
type ThreadRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// checkPlace refuses a malformed place before anything is made.
func checkPlace(p *PlaceRequest) error {
	if p == nil {
		return nil
	}
	if p.Mode != placeSwitch && (p.Branch != "" || p.Remote) {
		return newError(http.StatusBadRequest, "branch is only for switching the folder's branch")
	}
	switch p.Mode {
	case "", placeCurrent, placeBranch, placeSwitch:
		if p.Base != "" {
			return newError(http.StatusBadRequest, "base is only for a new worktree")
		}
		if p.Mode == placeSwitch && !validBase(p.Branch) {
			return newError(http.StatusBadRequest, "branch must be a branch name")
		}
	case placeWorktree:
		if p.Base != "" && !validBase(p.Base) {
			return newError(http.StatusBadRequest, "base must be a branch or commit name")
		}
	default:
		return newError(http.StatusBadRequest, "place mode must be current, branch, switch or worktree")
	}
	return nil
}

// validBase keeps a base name git will read as a revision, never as an
// option or several arguments; git itself says whether it exists.
func validBase(base string) bool {
	return base != "" && len(base) <= 255 && utf8.ValidString(base) && !strings.HasPrefix(base, "-") &&
		strings.IndexFunc(base, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

// slug is text as lower-case letters, digits and single hyphens, at most
// maxSlugBytes long; "" when nothing of it is left.
func slug(text string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(text) {
		if r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
			if b.Len() >= maxSlugBytes {
				break
			}
			continue
		}
		hyphen = true
	}
	return strings.TrimRight(b.String(), "-")
}

// maxWorktreeRoots caps the earlier worktrees folders kept listed.
const maxWorktreeRoots = 20

// defaultWorktreesDir is ~/.uam/worktrees, or worktreeRoot when set.
func (m *Manager) defaultWorktreesDir() (string, error) {
	if m.worktreeRoot != "" {
		return m.worktreeRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", newError(http.StatusConflict, "the server has no home directory for worktrees")
	}
	return filepath.Join(home, ".uam", "worktrees"), nil
}

// worktreesDir is the folder new Thread worktrees go in, one folder per
// Project: Settings' worktrees_dir, else the default.
func (m *Manager) worktreesDir() (string, error) {
	m.mu.Lock()
	dir := m.settings.WorktreesDir
	m.mu.Unlock()
	if dir != "" {
		return dir, nil
	}
	return m.defaultWorktreesDir()
}

// worktreeRoots are the folders that may hold Thread worktrees: the one new
// worktrees go in, the default, the earlier ones Settings named, and those
// the Threads' worktrees are in.
func (m *Manager) worktreeRoots() []string {
	var roots []string
	if dir, err := m.defaultWorktreesDir(); err == nil {
		roots = append(roots, dir)
	}
	m.mu.Lock()
	if m.settings.WorktreesDir != "" {
		roots = append(roots, m.settings.WorktreesDir)
	}
	roots = append(roots, m.oldWorktreeRoots...)
	for _, s := range m.sessions {
		if !s.removed && s.place != nil && s.place.Worktree != "" {
			roots = append(roots, filepath.Dir(filepath.Dir(s.place.Worktree)))
		}
	}
	m.mu.Unlock()
	slices.Sort(roots)
	return slices.Compact(roots)
}

// keptWorktreeRoots is the list of earlier worktrees folders once the
// setting moves from before to after ("" is the default, always listed):
// before joins it as the newest, after leaves it, at most maxWorktreeRoots.
func keptWorktreeRoots(roots []string, before, after string) []string {
	out := slices.DeleteFunc(slices.Clone(roots), func(r string) bool { return r == before || r == after })
	if before != "" && before != after {
		out = append(out, before)
	}
	if len(out) > maxWorktreeRoots {
		out = out[len(out)-maxWorktreeRoots:]
	}
	return out
}

// checkWorktreesDir cleans a worktrees folder the owner chose: ~ is the
// home folder; it must be absolute, a folder of the user's own when it
// exists, and in no git repository. It returns "" for the default.
func (m *Manager) checkWorktreesDir(raw string) (string, error) {
	const field = "worktrees_dir"
	dir := raw
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", newError(http.StatusBadRequest, "%s cannot use ~: the server has no home directory", field)
		}
		dir = home + dir[1:]
	}
	if err := checkPathText(field, dir); err != nil {
		return "", err
	}
	dir = filepath.Clean(dir)
	info, err := os.Stat(dir) // #nosec G703 -- the owner names a folder of the server on purpose; dir is absolute and clean.
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return "", dirError(field, err)
	case !info.IsDir():
		return "", newError(http.StatusBadRequest, "%s is not a directory", field)
	case !ownedByUser(info):
		return "", newError(http.StatusBadRequest, "%s belongs to another user", field)
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	if inGitRepo(ctx, dir) {
		return "", newError(http.StatusBadRequest, "%s is inside a git repository", field)
	}
	if def, err := m.defaultWorktreesDir(); err == nil && def == dir {
		return "", nil
	}
	return dir, nil
}

// inGitRepo reports whether dir, or its nearest existing parent, is in a
// git repository.
func inGitRepo(ctx context.Context, dir string) bool {
	git, err := lookGit()
	if err != nil {
		return false
	}
	for {
		if _, err := os.Stat(dir); err == nil { // #nosec G703 -- read-only probe of a folder the owner named.
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
	_, code, _, err := runGit(ctx, git, dir, 4096, "rev-parse", "--git-dir")
	return err == nil && code == 0
}

// makePlace makes the branch, or the branch and the worktree, a new Thread
// of the Project in dir asked for, and returns the Thread's folder, its
// place, and the function that takes them back if the Thread is not made.
// Without a place the Thread runs in dir as it is.
func (m *Manager) makePlace(ctx context.Context, project Project, req *PlaceRequest) (string, *store.WebPlace, func(), error) {
	none := func() {}
	if req == nil || req.Mode == "" || req.Mode == placeCurrent {
		return project.Dir, nil, none, nil
	}
	repo, reason, err := openRepo(ctx, project.Dir)
	if err != nil {
		return "", nil, none, err
	}
	if repo == nil {
		return "", nil, none, newError(http.StatusConflict, "%s", reason)
	}
	if !repo.hasHead {
		return "", nil, none, newError(http.StatusConflict, "the repository has no commits yet; a new branch needs one")
	}
	if req.Mode == placeSwitch {
		undo, err := m.switchFolder(ctx, repo, req.Branch, req.Remote)
		if err != nil {
			return "", nil, none, err
		}
		return project.Dir, nil, undo, nil
	}
	branch, err := freeBranch(ctx, repo)
	if err != nil {
		return "", nil, none, err
	}
	if req.Mode == placeBranch {
		return m.makeBranch(ctx, project, repo, branch)
	}
	return m.makeWorktree(ctx, project, repo, branch, req.Base)
}

// freeBranch is a new branch name uam/<8 hex digits> no branch has yet.
func freeBranch(ctx context.Context, repo *gitRepo) (string, error) {
	for range 4 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("generate branch name: %w", err)
		}
		name := branchPrefix + hex.EncodeToString(b[:])
		if !refExists(ctx, repo, "refs/heads/"+name) {
			return name, nil
		}
	}
	return "", newError(http.StatusConflict, "could not find a free branch name")
}

func refExists(ctx context.Context, repo *gitRepo, ref string) bool {
	_, code, _, err := runGit(ctx, repo.git, repo.top, 4096, "rev-parse", "--verify", "--quiet", ref)
	return err == nil && code == 0
}

// checkSwitch refuses switching the branch of repo's working tree, which
// every Thread in the folder shares, while one of them has not finished
// (busyThreads), and, for an existing branch, while tracked files there are
// not committed; a new branch takes those changes along.
func (m *Manager) checkSwitch(ctx context.Context, repo *gitRepo, existing bool) error {
	if ids, names := m.busyThreads(repo.top); len(ids) > 0 {
		who := "“" + names[0] + "”"
		if len(names) > 1 {
			who += fmt.Sprintf(" and %d other %s", len(names)-1, plural(len(names)-1, "Thread", "Threads"))
		}
		return &Error{Status: http.StatusConflict, Code: codeBranchBusy, Refs: ids, Message: who + " is working in this folder. Wait for it to finish."}
	}
	if !existing {
		return nil
	}
	n, err := folderUncommitted(ctx, repo)
	if err != nil {
		return err
	}
	if n > 0 {
		return &Error{Status: http.StatusConflict, Code: codeBranchDirty, Refs: []string{strconv.Itoa(n)}, Message: fmt.Sprintf("%d %s not committed", n, plural(n, "file", "files"))}
	}
	return nil
}

// folderUncommitted counts the tracked files changed in repo's working tree or
// index (trackedChanges); untracked files do not count.
func folderUncommitted(ctx context.Context, repo *gitRepo) (int, error) {
	out, code, stderr, err := runGit(ctx, repo.git, repo.top, maxPendingStatus, "status", "--porcelain=v2", "-z", "--untracked-files=no")
	if err != nil {
		return 0, err
	}
	if code != 0 {
		return 0, newError(http.StatusBadGateway, "git status failed: %s", gitMessage(stderr))
	}
	n, _ := trackedChanges(out, 0)
	return n, nil
}

// switchFolder checks out the existing branch name in repo's working tree
// (checkSwitch): a local branch, or with remote a remote-tracking one,
// which a new local branch of the same name then tracks. It returns the
// function that switches back and deletes the branch it made.
func (m *Manager) switchFolder(ctx context.Context, repo *gitRepo, name string, remote bool) (func(), error) {
	none := func() {}
	shown := clipRunes(displaytext.Sanitize(name), maxDetailRunes)
	local, create := name, false
	if remote {
		if !refExists(ctx, repo, "refs/remotes/"+name) {
			return none, newError(http.StatusBadRequest, "%q is not a remote branch of this repository", shown)
		}
		if local = remoteBranchName(ctx, repo, name); !validBase(local) {
			return none, newError(http.StatusBadRequest, "%q is not a remote branch of this repository", shown)
		}
		if refExists(ctx, repo, "refs/heads/"+local) {
			upstream, code, _, err := runGit(ctx, repo.git, repo.top, 4096, "for-each-ref", "--format=%(upstream)", "refs/heads/"+local)
			if err != nil || code != 0 || strings.TrimSpace(string(upstream)) != "refs/remotes/"+name {
				return none, newError(http.StatusConflict, "a local branch %s already exists; choose it under Local", clipRunes(displaytext.Sanitize(local), maxDetailRunes))
			}
		} else {
			create = true
		}
	} else if !refExists(ctx, repo, "refs/heads/"+name) {
		return none, newError(http.StatusBadRequest, "%q is not a branch of this repository", shown)
	}
	previous, _ := readBranch(ctx, repo.top)
	if previous == local {
		return none, nil
	}
	if err := m.checkSwitch(ctx, repo, true); err != nil {
		return none, err
	}
	end, err := m.beginWrite(repo.top)
	if err != nil {
		return none, err
	}
	defer end()
	args := []string{"switch", "--no-guess", local}
	if create {
		args = []string{"switch", "--track", "-c", local, name}
	}
	if _, err := runGitWrite(ctx, repo.top, nil, args...); err != nil {
		return none, gitFailed("Could not switch the branch", err)
	}
	log.Info("web project folder switched branch", "branch", local, "created", create)
	undo := func() {
		ctx, cancel := context.WithTimeout(m.ctx, gitWriteTimeout)
		defer cancel()
		if previous != "" {
			if _, err := runGitWrite(ctx, repo.top, nil, "switch", previous); err != nil {
				log.Warn("web project folder left on the switched branch", "branch", local, "error", err)
				return
			}
		}
		if create {
			_, _ = runGitWrite(ctx, repo.top, nil, "branch", "-D", local)
		}
	}
	return undo, nil
}

// remoteBranchName is the remote-tracking branch name ("origin/feat/x")
// without its remote ("feat/x"), the longest remote name that fits; "" when
// none does.
func remoteBranchName(ctx context.Context, repo *gitRepo, name string) string {
	out, code, _, err := runGit(ctx, repo.git, repo.top, 64<<10, "remote")
	if err != nil || code != 0 {
		return ""
	}
	best := ""
	for r := range strings.FieldsSeq(string(out)) {
		if strings.HasPrefix(name, r+"/") && len(r) > len(best) {
			best = r
		}
	}
	if best == "" {
		return ""
	}
	return name[len(best)+1:]
}

// makeBranch switches the Project's folder to branch, a new branch from
// HEAD, under the rules of checkSwitch.
func (m *Manager) makeBranch(ctx context.Context, project Project, repo *gitRepo, branch string) (string, *store.WebPlace, func(), error) {
	none := func() {}
	if err := m.checkSwitch(ctx, repo, false); err != nil {
		return "", nil, none, err
	}
	end, err := m.beginWrite(repo.top)
	if err != nil {
		return "", nil, none, err
	}
	defer end()
	previous, _ := readBranch(ctx, repo.top)
	if _, err := runGitWrite(ctx, repo.top, nil, "switch", "--no-track", "-c", branch); err != nil {
		return "", nil, none, gitFailed("Could not make the new branch", err)
	}
	log.Info("web thread branch made", "project", project.ID, "branch", branch)
	undo := func() {
		ctx, cancel := context.WithTimeout(m.ctx, gitWriteTimeout)
		defer cancel()
		if previous != "" {
			if _, err := runGitWrite(ctx, repo.top, nil, "switch", previous); err != nil {
				log.Warn("web thread branch left checked out", "branch", branch, "error", err)
				return
			}
		}
		_, _ = runGitWrite(ctx, repo.top, nil, "branch", "-D", branch)
	}
	return project.Dir, &store.WebPlace{Mode: placeBranch, Branch: branch}, undo, nil
}

// makeWorktree adds a worktree at ~/.uam/worktrees/<project>/<branch> on
// branch, a new branch from base. The Thread's folder is the Project's
// folder inside it, as the Project may be a folder inside its repository.
func (m *Manager) makeWorktree(ctx context.Context, project Project, repo *gitRepo, branch, base string) (string, *store.WebPlace, func(), error) {
	none := func() {}
	start := cmp.Or(base, "HEAD")
	if !refExists(ctx, repo, start+"^{commit}") {
		return "", nil, none, newError(http.StatusBadRequest, "%q is not a branch or commit of this repository", clipRunes(displaytext.Sanitize(base), maxDetailRunes))
	}
	prefix, code, stderr, err := runGit(ctx, repo.git, project.Dir, 4096, "rev-parse", "--show-prefix")
	if err != nil {
		return "", nil, none, err
	}
	if code != 0 {
		return "", nil, none, newError(http.StatusBadGateway, "git rev-parse failed: %s", gitMessage(stderr))
	}
	root, err := m.worktreesDir()
	if err != nil {
		return "", nil, none, err
	}
	parent := filepath.Join(root, cmp.Or(slug(project.Name), "project"))
	// A folder the owner chose may hold a repository named as the Project.
	if def, _ := m.defaultWorktreesDir(); root != def && inGitRepo(ctx, parent) {
		return "", nil, none, newError(http.StatusConflict, "%s is inside a git repository; choose another folder for new worktrees in Settings", parent)
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", nil, none, newError(http.StatusConflict, "could not make the worktrees folder: %s", shortError(err))
	}
	path := filepath.Join(parent, strings.ReplaceAll(branch, "/", "-"))
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return "", nil, none, newError(http.StatusConflict, "the worktree folder %s already exists", path)
	}
	if _, err := runGitWrite(ctx, repo.top, nil, "worktree", "add", "--no-track", "-b", branch, path, start); err != nil {
		return "", nil, none, gitFailed("Could not make the worktree", err)
	}
	log.Info("web thread worktree made", "project", project.ID, "branch", branch)
	undo := func() {
		ctx, cancel := context.WithTimeout(m.ctx, gitWriteTimeout)
		defer cancel()
		if _, err := runGitWrite(ctx, repo.top, nil, "worktree", "remove", "--force", path); err != nil {
			log.Warn("web thread worktree not removed", "path", path, "error", err)
			return
		}
		_, _ = runGitWrite(ctx, repo.top, nil, "branch", "-D", branch)
	}
	workdir := filepath.Join(path, filepath.FromSlash(strings.TrimSpace(string(prefix))))
	return workdir, &store.WebPlace{Mode: placeWorktree, Branch: branch, Base: base, Worktree: path}, undo, nil
}

// busyThreads returns the IDs and names of the Threads in the repository
// or folder top that hold its branch (holdsFolderLocked), by name.
func (m *Manager) busyThreads(top string) (ids, names []string) {
	busy := m.tasksIn(top, func(s *webSession) bool { return s.holdsFolderLocked() })
	m.mu.Lock()
	slices.SortFunc(busy, func(a, b *webSession) int { return strings.Compare(a.name+a.title, b.name+b.title) })
	for _, s := range busy {
		ids = append(ids, s.id)
		names = append(names, clipRunes(displaytext.Sanitize(firstNonEmpty(s.name, s.title, "A Thread")), maxNameRunes))
	}
	m.mu.Unlock()
	return ids, names
}

// holdsFolderLocked reports whether s keeps the folder it shares with other
// Threads from switching branch: it is active, not in a worktree of its
// own, and has not finished: working, starting, compacting, waiting for an
// answer or a permission, prompts queued, subagents or background tasks
// running.
func (s *webSession) holdsFolderLocked() bool {
	return s.stage == StageActive && (s.place == nil || s.place.Worktree == "") &&
		(s.settleableLocked() != nil || s.compacting && s.conv != nil)
}

// SwitchBranch switches the branch of the folder the started Thread id runs
// in, which every Thread there shares: to a new branch uam/<8 hex digits>
// that takes the Thread's title (mode branch), or to an existing branch
// (mode switch). The rules of checkSwitch hold, the Thread itself included.
func (m *Manager) SwitchBranch(ctx context.Context, id string, req PlaceRequest) (SessionSummary, error) {
	if req.Mode != placeBranch && req.Mode != placeSwitch {
		return SessionSummary{}, newError(http.StatusBadRequest, "mode must be branch or switch")
	}
	if err := checkPlace(&req); err != nil {
		return SessionSummary{}, err
	}
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	m.mu.Lock()
	err = s.readOnlyLocked()
	own := s.place != nil && s.place.Worktree != ""
	project := Project{ID: s.projectID, Dir: s.workdir}
	m.mu.Unlock()
	switch {
	case err != nil:
		return SessionSummary{}, err
	case own:
		return SessionSummary{}, newError(http.StatusConflict, "a Thread in its own worktree keeps its branch")
	}
	repo, reason, err := openRepo(ctx, project.Dir)
	switch {
	case err != nil:
		return SessionSummary{}, err
	case repo == nil:
		return SessionSummary{}, newError(http.StatusConflict, "%s", reason)
	case !repo.hasHead:
		return SessionSummary{}, newError(http.StatusConflict, "the repository has no commits yet; a new branch needs one")
	}
	var place *store.WebPlace
	if req.Mode == placeSwitch {
		if _, err := m.switchFolder(ctx, repo, req.Branch, req.Remote); err != nil {
			return SessionSummary{}, err
		}
	} else {
		branch, err := freeBranch(ctx, repo)
		if err != nil {
			return SessionSummary{}, err
		}
		if _, place, _, err = m.makeBranch(ctx, project, repo, branch); err != nil {
			return SessionSummary{}, err
		}
	}
	m.mu.Lock()
	before := m.summaryLocked(s)
	// A branch uam made for the Thread names it only while the Thread is on it.
	if place != nil || s.place != nil && s.place.Mode == placeBranch {
		s.place = place
	}
	if place != nil {
		s.branch = place.Branch
	}
	m.changedLocked(s, before)
	m.kickPlaceNameLocked(s)
	m.mu.Unlock()
	m.refreshBranches(ctx, true, project.ID)
	return m.Summary(id)
}

func (s *Server) handleSwitchBranch(w http.ResponseWriter, r *http.Request) {
	var req PlaceRequest
	if !decodeBody(w, r, &req) {
		return
	}
	sum, err := s.m.SwitchBranch(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// loadedPlace is a stored place, or nil when it is not one this version
// can act on.
func loadedPlace(p *store.WebPlace) *store.WebPlace {
	if p == nil || (p.Mode != placeBranch && p.Mode != placeWorktree) || !strings.HasPrefix(p.Branch, branchPrefix) {
		return nil
	}
	clone := *p
	return &clone
}

func (s *webSession) clonedPlace() *store.WebPlace {
	if s.place == nil {
		return nil
	}
	clone := *s.place
	return &clone
}

// kickPlaceNameLocked renames the branch uam made for the Thread to
// uam/<title> once the Thread has a name or a title, unless the branch was
// pushed. It runs once per Thread: a rename that fails is not retried.
// Without a name it waits while a title is on its way.
func (m *Manager) kickPlaceNameLocked(s *webSession) {
	// Not before the Thread is listed: a create that fails takes the branch back.
	if m.closed || s.place == nil || s.place.Named || s.placeNaming || m.sessions[s.id] != s || s.name == "" && (s.titling || s.titleWait != nil) {
		return
	}
	want := slug(firstNonEmpty(s.name, s.title))
	if want == "" {
		return
	}
	s.placeNaming = true
	dir, old := s.workdir, s.place.Branch
	m.wg.Add(1)
	go func() {
		defer log.Recover("branch naming", &m.mu)
		defer m.wg.Done()
		renamed := nameBranch(m.ctx, dir, old, branchPrefix+want)
		m.mu.Lock()
		defer m.mu.Unlock()
		s.placeNaming = false
		if s.removed || s.place == nil || s.place.Branch != old || m.ctx.Err() != nil {
			return
		}
		before := m.summaryLocked(s)
		place := *s.place
		place.Named = true
		if renamed != "" {
			place.Branch = renamed
			if s.branch == old {
				s.branch = renamed
			}
		}
		s.place = &place
		m.changedLocked(s, before)
	}()
}

// nameBranch renames the local branch old in the repository holding dir to
// want, or to want-<old's digits> when want is taken, and returns the new
// name; "" when it left old as it is: pushed, gone, or git refused.
func nameBranch(ctx context.Context, dir, old, want string) string {
	repo, _, err := openRepo(ctx, dir)
	if err != nil || repo == nil || !refExists(ctx, repo, "refs/heads/"+old) {
		return ""
	}
	if pushed(ctx, repo, old) {
		log.Info("web thread branch kept its name: it was pushed", "branch", old)
		return ""
	}
	next := want
	if refExists(ctx, repo, "refs/heads/"+next) {
		next = want + "-" + strings.TrimPrefix(old, branchPrefix)
		if refExists(ctx, repo, "refs/heads/"+next) {
			return ""
		}
	}
	if _, err := runGitWrite(ctx, repo.top, nil, "branch", "-m", old, next); err != nil {
		log.Info("web thread branch not renamed", "branch", old, "error", err)
		return ""
	}
	return next
}

// pushed reports whether branch has an upstream or a remote-tracking
// branch of the same name.
func pushed(ctx context.Context, repo *gitRepo, branch string) bool {
	upstream, code, _, err := runGit(ctx, repo.git, repo.top, 4096, "for-each-ref", "--format=%(upstream)", "refs/heads/"+branch)
	if err != nil || code != 0 || strings.TrimSpace(string(upstream)) != "" {
		return true
	}
	remotes, code, _, err := runGit(ctx, repo.git, repo.top, maxStatusBytes, "for-each-ref", "--format=%(refname:lstrip=3)", "refs/remotes")
	if err != nil || code != 0 {
		return true
	}
	return slices.Contains(strings.Split(string(remotes), "\n"), branch)
}

// ProjectBranches lists the branches of the Project's repository, local and
// remote-tracking, most recently committed first, and what keeps its folder
// from switching branch now.
func (m *Manager) ProjectBranches(ctx context.Context, projectID string) (BranchList, error) {
	dir, err := m.projectDir(projectID)
	if err != nil {
		return BranchList{}, err
	}
	out := BranchList{Branches: []BranchEntry{}}
	repo, reason, err := openRepo(ctx, dir)
	if err != nil {
		return out, err
	}
	if repo == nil {
		out.Reason = reason
		return out, nil
	}
	if ids, names := m.busyThreads(repo.top); len(ids) > 0 {
		out.Busy = &ThreadRef{ID: ids[0], Name: names[0]}
	}
	// Only a hint for the menu: the switch itself checks again.
	out.Uncommitted, _ = folderUncommitted(ctx, repo)
	raw, code, stderr, err := runGit(ctx, repo.git, repo.top, maxStatusBytes, "for-each-ref", "--sort=-committerdate", "--format=%(refname)%00%(committerdate:iso-strict)%00%(symref)", "refs/heads", "refs/remotes")
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, newError(http.StatusBadGateway, "git for-each-ref failed: %s", gitMessage(stderr))
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		ref, rest, _ := strings.Cut(line, "\x00")
		date, symref, _ := strings.Cut(rest, "\x00")
		if ref == "" || symref != "" { // origin/HEAD points at a branch listed anyway
			continue
		}
		if len(out.Branches) == maxBranchRefs {
			out.Reason = fmt.Sprintf("showing the %d most recent branches", maxBranchRefs)
			break
		}
		name, local := strings.CutPrefix(ref, "refs/heads/")
		if !local {
			name = strings.TrimPrefix(ref, "refs/remotes/")
		}
		entry := BranchEntry{Name: clipRunes(displaytext.Sanitize(name), maxNameRunes), Remote: !local}
		entry.UpdatedAt, _ = time.Parse(time.RFC3339, date)
		out.Branches = append(out.Branches, entry)
	}
	return out, nil
}

// projectDir is the folder of the Project id.
func (m *Manager) projectDir(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.projects[id]
	if p == nil {
		return "", errProjectNotFound
	}
	return p.Dir, nil
}

func (s *Server) handleProjectBranches(w http.ResponseWriter, r *http.Request) {
	list, err := s.m.ProjectBranches(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
