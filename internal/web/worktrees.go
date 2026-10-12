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
	// branchPrefix starts every branch uam makes for a Thread.
	branchPrefix = "uam/"
	// maxSlugBytes caps the title part of a branch name and a folder name.
	maxSlugBytes = 48
	// maxBranchRefs caps GET /api/projects/{id}/branches.
	maxBranchRefs  = 500
	codeBranchBusy = "branch_busy"
)

// PlaceRequest is where a new Thread runs (CreateRequest.Place): on the
// Project's current branch, on a new branch made in the Project's folder,
// or on a new branch in a new git worktree started from Base ("" is HEAD).
type PlaceRequest struct {
	Mode string `json:"mode"`
	Base string `json:"base,omitempty"`
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
// committed first. Reason says why it is empty or cut short.
type BranchList struct {
	Branches []BranchEntry `json:"branches"`
	Reason   string        `json:"reason,omitempty"`
}

// checkPlace refuses a malformed place before anything is made.
func checkPlace(p *PlaceRequest) error {
	if p == nil {
		return nil
	}
	switch p.Mode {
	case "", placeCurrent, placeBranch:
		if p.Base != "" {
			return newError(http.StatusBadRequest, "base is only for a new worktree")
		}
	case placeWorktree:
		if p.Base != "" && !validBase(p.Base) {
			return newError(http.StatusBadRequest, "base must be a branch or commit name")
		}
	default:
		return newError(http.StatusBadRequest, "place mode must be current, branch or worktree")
	}
	return nil
}

// validBase keeps a base name git will read as a revision, never as an
// option or several arguments; git itself says whether it exists.
func validBase(base string) bool {
	return len(base) <= 255 && utf8.ValidString(base) && !strings.HasPrefix(base, "-") &&
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

// worktreesDir is the folder holding every Thread worktree, one folder per
// Project: ~/.uam/worktrees.
func (m *Manager) worktreesDir() (string, error) {
	if m.worktreeRoot != "" {
		return m.worktreeRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", newError(http.StatusConflict, "the server has no home directory for worktrees")
	}
	return filepath.Join(home, ".uam", "worktrees"), nil
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

// makeBranch switches the Project's folder to branch, a new branch from
// HEAD. Every Thread in the folder shares its working tree, so it is
// refused while another Thread there could still be writing.
func (m *Manager) makeBranch(ctx context.Context, project Project, repo *gitRepo, branch string) (string, *store.WebPlace, func(), error) {
	none := func() {}
	if ids, names := m.busyThreads(repo.top); len(ids) > 0 {
		who := "“" + names[0] + "”"
		if len(names) > 1 {
			who += fmt.Sprintf(" and %d other %s", len(names)-1, plural(len(names)-1, "Thread", "Threads"))
		}
		return "", nil, none, &Error{Status: http.StatusConflict, Code: codeBranchBusy, Refs: ids,
			Message: who + " is working in this folder, and a new branch would switch it too. Wait for it to finish, or start the Thread in a new worktree."}
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
// or folder top that could still be writing there (repoBusy), by name.
func (m *Manager) busyThreads(top string) (ids, names []string) {
	busy := m.tasksIn(top, func(s *webSession) bool { return s.settleableLocked() != nil })
	m.mu.Lock()
	slices.SortFunc(busy, func(a, b *webSession) int { return strings.Compare(a.name+a.title, b.name+b.title) })
	for _, s := range busy {
		ids = append(ids, s.id)
		names = append(names, clipRunes(displaytext.Sanitize(firstNonEmpty(s.name, s.title, "A Thread")), maxNameRunes))
	}
	m.mu.Unlock()
	return ids, names
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
func (m *Manager) kickPlaceNameLocked(s *webSession) {
	// Not before the Thread is listed: a create that fails takes the branch back.
	if m.closed || s.place == nil || s.place.Named || s.placeNaming || m.sessions[s.id] != s {
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

// ProjectBranches lists the branches a new worktree of the Project can
// start from: local and remote-tracking, most recently committed first.
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
