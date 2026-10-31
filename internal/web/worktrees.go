package web

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

// worktreeRoots are the folders earlier versions put Thread worktrees in:
// ~/.uam/worktrees and the ones Settings named. New worktrees go where the
// Copilot CLI puts its own (worktreePath).
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
func (m *Manager) makePlace(ctx context.Context, project Project, req *PlaceRequest, hint string) (string, *store.WebPlace, func(), error) {
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
	branch, err := freeBranch(ctx, repo, hint)
	if err != nil {
		return "", nil, none, err
	}
	if req.Mode == placeBranch {
		return m.makeBranch(ctx, project, repo, branch)
	}
	return m.makeWorktree(ctx, project, repo, branch, req.Base)
}

// freeBranch is a new branch name no local or remote branch has yet:
// uam/<slug of hint>, with random hex digits added when that is taken or
// hint leaves no slug. The name is final: uam never renames it, as the
// Copilot CLI never renames the branches it makes for worktrees.
func freeBranch(ctx context.Context, repo *gitRepo, hint string) (string, error) {
	want := slug(hint)
	if want != "" && branchFree(ctx, repo, branchPrefix+want) {
		return branchPrefix + want, nil
	}
	for range 4 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("generate branch name: %w", err)
		}
		name := branchPrefix + strings.TrimPrefix(want+"-"+hex.EncodeToString(b[:]), "-")
		if branchFree(ctx, repo, name) {
			return name, nil
		}
	}
	return "", newError(http.StatusConflict, "could not find a free branch name")
}

// branchFree reports whether no local branch and no remote's branch is
// named name, so a push cannot land on an older branch of that name.
func branchFree(ctx context.Context, repo *gitRepo, name string) bool {
	out, code, _, err := runGit(ctx, repo.git, repo.top, 4096, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads/"+name, "refs/remotes/*/"+name)
	return err == nil && code == 0 && strings.TrimSpace(string(out)) == ""
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
	return project.Dir, &store.WebPlace{Mode: placeBranch, Branch: branch, Base: previous}, undo, nil
}

// makeWorktree adds a worktree on branch, a new branch from base, where the
// Copilot CLI puts its own (worktreePath). The Thread's folder is the
// Project's folder inside it, as the Project may be a folder inside its
// repository.
func (m *Manager) makeWorktree(ctx context.Context, project Project, repo *gitRepo, branch, base string) (string, *store.WebPlace, func(), error) {
	none := func() {}
	pathTemplate, baseRef := m.copilotWorktreeSettings(repo.top)
	start, base := worktreeStart(ctx, repo, base, baseRef)
	if !refExists(ctx, repo, start+"^{commit}") {
		return "", nil, none, newError(http.StatusBadRequest, "%q is not a branch or commit of this repository", clipRunes(displaytext.Sanitize(start), maxDetailRunes))
	}
	prefix, code, stderr, err := runGit(ctx, repo.git, project.Dir, 4096, "rev-parse", "--show-prefix")
	if err != nil {
		return "", nil, none, err
	}
	if code != 0 {
		return "", nil, none, newError(http.StatusBadGateway, "git rev-parse failed: %s", gitMessage(stderr))
	}
	path, err := worktreePath(pathTemplate, repo.top, branch)
	if err != nil {
		return "", nil, none, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", nil, none, newError(http.StatusConflict, "could not make the worktrees folder: %s", shortError(err))
	}
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

// placeInstructionsLocked tells the agent of a Thread on a branch uam made
// where it works and how its work leaves: push its own branch and open the
// pull or merge request into the base, as the Copilot CLI's own worktree
// flow does, so it never has to ask which branch to push. "" elsewhere.
func (m *Manager) placeInstructionsLocked(s *webSession) string {
	p := s.place
	if p == nil || p.Branch == "" || (p.Mode != placeWorktree && p.Mode != placeBranch) {
		return ""
	}
	where := "the Project's folder"
	if p.Mode == placeWorktree {
		where = "a git worktree of the Project's repository"
		if project := m.projects[s.projectID]; project != nil {
			where += " (main checkout " + project.Dir + ")"
		}
	}
	base, into := "", "the repository's default branch"
	if p.Base != "" {
		base, into = ", started from "+p.Base, p.Base
	}
	return fmt.Sprintf("uam place: you work in %s on the branch %s%s, made for this Thread. Commit there. When the owner asks you to push, run `git push -u origin HEAD`: that pushes %s, never the base branch. When the owner asks for a pull or merge request, open it from %s into %s, following the git rules above. Do not push to %s, merge into it or switch branches unless the owner asks for exactly that, and do not ask which branch to push.",
		where, p.Branch, base, p.Branch, p.Branch, into, into)
}

// copilotWorktreeSettings reads the Copilot CLI's worktree settings for the
// repository at top. worktreeBaseRef comes from the repository's
// .github/copilot/settings.local.json, then its settings.json, then the
// user's settings.json in COPILOT_HOME, the CLI's own order.
// worktreePathTemplate comes from the user's file only: a repository must
// not choose where on disk its files are checked out. top "" reads the
// user's file only.
func (m *Manager) copilotWorktreeSettings(top string) (pathTemplate, baseRef string) {
	var files []string
	if top != "" {
		files = append(files, filepath.Join(top, ".github", "copilot", "settings.local.json"), filepath.Join(top, ".github", "copilot", "settings.json"))
	}
	user := ""
	if scope, err := m.configurationScope(""); err == nil {
		user = filepath.Join(scope.base, "settings.json")
		files = append(files, user)
	}
	for _, file := range files {
		var s struct {
			PathTemplate string `json:"worktreePathTemplate"`
			BaseRef      string `json:"worktreeBaseRef"`
		}
		// shortcut: plain JSON only; a file with comments is skipped like a missing one.
		b, err := os.ReadFile(file) // #nosec G304 -- the Copilot CLI's own settings files.
		if err != nil || json.Unmarshal(b, &s) != nil {
			continue
		}
		baseRef = cmp.Or(baseRef, s.BaseRef)
		if file == user {
			pathTemplate = s.PathTemplate
		}
	}
	return pathTemplate, baseRef
}

// worktreeStart is the ref a new worktree starts from and the name recorded
// as its base ("" for a detached HEAD): base when given; else, by
// worktreeBaseRef, the remote's
// default branch for "defaultBranch" or the Project's current branch for
// "head", the CLI's default.
func worktreeStart(ctx context.Context, repo *gitRepo, base, baseRef string) (start, name string) {
	if base != "" {
		return base, base
	}
	if baseRef == "defaultBranch" {
		out, code, _, err := runGit(ctx, repo.git, repo.top, 4096, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
		if ref := strings.TrimSpace(string(out)); err == nil && code == 0 && ref != "" {
			return ref, strings.TrimPrefix(ref, "origin/")
		}
	}
	// A detached HEAD records no base: the agent is then told the default branch.
	current, _ := readBranch(ctx, repo.top)
	return "HEAD", current
}

// worktreePath is where the worktree of branch goes, as the Copilot CLI
// places its own: by the owner's worktreePathTemplate ({repoPath},
// {repo}, {branch}, {branchSlug}, leading ~), else
// <repository>.worktrees/<branchSlug> beside the repository.
func worktreePath(template, top, branch string) (string, error) {
	branchSlug := strings.ReplaceAll(branch, "/", "-")
	if template == "" {
		return filepath.Join(top+".worktrees", branchSlug), nil
	}
	path := template
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/') {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", newError(http.StatusConflict, "the server has no home directory for worktrees")
		}
		path = home + rest
	}
	path = strings.NewReplacer("{repoPath}", top, "{repo}", filepath.Base(top), "{branchSlug}", branchSlug, "{branch}", branch).Replace(path)
	if !filepath.IsAbs(path) {
		return "", newError(http.StatusConflict, "the Copilot setting worktreePathTemplate %q is not an absolute path or ~/…", clipRunes(displaytext.Sanitize(template), maxDetailRunes))
	}
	return filepath.Clean(path), nil
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
	hint := firstNonEmpty(s.name, s.title)
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
		branch, err := freeBranch(ctx, repo, hint)
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
