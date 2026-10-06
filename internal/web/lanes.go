package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// Lanes (ADR 0006): each run attempt works in its own git worktree, on its
// own branch, outside the Project directory, and lands on the Project's
// integration branch as one squash commit whose trailers name the card and
// the request. uam moves its branches only by compare-and-swap, and never
// backwards. Callers hold the Project's land mutex, and record an intent in
// the store before a ref moves.
const (
	lanesDir    = "lanes"
	integPrefix = "uam-plan-"

	// The trailers uam writes: a landing names its card and request, a
	// revert its card, and uam's merge of the integration tip into a lane
	// the tip it merged.
	trailerCard    = "Uam-Card"
	trailerRequest = "Uam-Request"
	trailerRevert  = "Uam-Revert"
	trailerMerge   = "Uam-Merge"

	codeLandConflict   = "land_conflict"
	codeLandStale      = "land_stale"
	codeMergeConflict  = "merge_conflict"
	codeMergeBlocked   = "merge_blocked"
	codeRevertConflict = "revert_conflict"
	codeLocalChanges   = "local_changes"
	codeGitTooOld      = "git_too_old"
	codeNoGitIdentity  = "no_git_identity"

	// maxNamedFiles is how many paths a refusal names.
	maxNamedFiles = 10
)

// pinnedMerge is how uam runs git merge, in a lane or in the owner's
// checkout of a base branch: a commit by the ort strategy that stashes
// nothing, runs no hook and signs nothing, whatever merge.autoStash,
// branch.<name>.mergeOptions or pull.twohead say, since the command line
// outranks them.
var pinnedMerge = []string{"merge", "--commit", "--no-autostash", "-s", "ort", "--no-edit", "--no-verify", "--no-gpg-sign", "--no-verify-signatures"}

var (
	// attemptPart is an attempt branch's part after the integration branch:
	// <seq>-<id8>.
	attemptPart = regexp.MustCompile(`^[0-9]+-[0-9a-f]{8}$`)
	cardTrailer = regexp.MustCompile(`^#[0-9]+$`)
)

// lane is one attempt: its branch and its worktree.
type lane struct {
	branch string
	dir    string
}

// laneRepo is a Project's repository and its integration branch.
type laneRepo struct {
	*gitRepo
	integ string
	// common is the repository's common git directory, read from the
	// Project's directory. git keeps each lane's own git directory under it.
	common string
}

// gitAt is where uam runs a git command: a directory and, in a lane, the
// arguments that pin git to the lane's own git directory and worktree (see
// laneAt).
type gitAt struct {
	dir string
	pin []string
}

// argv is args after the pin.
func (a gitAt) argv(args ...string) []string {
	return append(slices.Clone(a.pin), args...)
}

// runLaneGit runs one git write uam makes for lanes, or for its merge into
// a base branch, at a: runGitWrite with hooks and fsmonitor off, as runGit
// has them, and every hook the configuration at a defines turned off too
// (see hooksOff), so uam's unattended writes run no hook the repository's
// configuration names. They never recurse into submodules either. The
// owner's own Commit, Pull and Push run the repository's hooks.
func runLaneGit(ctx context.Context, a gitAt, args ...string) (string, error) {
	env, err := hooksOff(ctx, a)
	if err != nil {
		return "", err
	}
	return runGitWriteEnv(ctx, a.dir, nil, env, slices.Concat(gitBase, []string{"-c", "submodule.recurse=false"}, a.argv(args...))...)
}

// hooksOff is the environment that turns off, for one git command at a,
// each hook the configuration there defines: git 2.54 and later also run
// the hooks named by hook.<name>.command, whatever core.hooksPath says, and
// only hook.<name>.enabled turns one off. The settings go in through
// GIT_CONFIG_COUNT, which outranks every configuration file and, unlike -c,
// takes any name.
func hooksOff(ctx context.Context, a gitAt) ([]string, error) {
	git, err := lookGit()
	if err != nil {
		return nil, err
	}
	out, code, stderr, err := runGit(ctx, git, a.dir, maxStatusBytes, a.argv("config", "-z", "--name-only", "--get-regexp", `^hook\.`)...)
	switch {
	case err != nil:
		return nil, err
	case code == 1:
		return nil, nil // no hook.* settings
	case code != 0:
		return nil, newError(http.StatusBadGateway, "git config failed: %s", gitMessage(stderr))
	case len(out) >= maxStatusBytes:
		return nil, newError(http.StatusBadGateway, "the git configuration holds too many hook settings")
	}
	base, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	var env []string
	seen := map[string]bool{}
	for _, key := range nulRecords(out) {
		off := "hook.enabled" // hook.command and hook.event: the hook with no name
		if rest := strings.TrimPrefix(key, "hook."); strings.Contains(rest, ".") {
			off = "hook." + rest[:strings.LastIndexByte(rest, '.')] + ".enabled"
		}
		if seen[off] {
			continue
		}
		seen[off] = true
		i := base + len(seen) - 1
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, off), fmt.Sprintf("GIT_CONFIG_VALUE_%d=false", i))
	}
	return append(env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", base+len(seen))), nil
}

// lanesRoot is where lane worktrees live: next to sessions.json, never in a
// Project directory, so lane Tasks never count as busy in the owner's
// repository.
func (m *Manager) lanesRoot() string {
	return filepath.Join(filepath.Dir(m.store.Path()), lanesDir)
}

// integBranch is the Project's integration branch.
func integBranch(projectID string) string { return integPrefix + hex8(projectID) }

// hex8 is the first 8 hex digits of a UUID.
func hex8(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	return id[:min(len(id), 8)]
}

// newLane names a new attempt at subtask #seq: a branch beside the
// integration branch with a random part, so no name is ever reused.
func newLane(root, projectID string, seq int64) (lane, error) {
	id, err := newUUID()
	if err != nil {
		return lane{}, err
	}
	return laneOf(root, projectID, fmt.Sprintf("%s-%d-%s", integBranch(projectID), seq, hex8(id)))
}

// laneOf is the attempt on branch, with its worktree derived from the name:
// <root>/<project id>/<seq>-<id8>.
func laneOf(root, projectID, branch string) (lane, error) {
	part, ok := strings.CutPrefix(branch, integBranch(projectID)+"-")
	if !ok || !attemptPart.MatchString(part) {
		return lane{}, fmt.Errorf("%q is not an attempt branch of project %s", branch, projectID)
	}
	return lane{branch: branch, dir: filepath.Join(root, projectID, part)}, nil
}

// laneWorkdir is where an attempt's Task works: the worktree plus the
// Project's place in the repository whose top is top.
func laneWorkdir(l lane, top, projectDir string) (string, error) {
	rel, err := filepath.Rel(realPath(top), realPath(projectDir))
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%s is not in the repository at %s", projectDir, top)
	}
	return filepath.Join(l.dir, rel), nil
}

// openLanes opens the repository holding the Project directory dir.
func openLanes(ctx context.Context, projectID, dir string) (*laneRepo, error) {
	repo, err := openEvidenceRepo(ctx, dir)
	if err != nil {
		return nil, err
	}
	r := &laneRepo{gitRepo: repo, integ: integBranch(projectID)}
	if r.common, err = r.output(ctx, repo.top, "rev-parse", "--git-common-dir"); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(r.common) {
		r.common = filepath.Join(repo.top, r.common)
	}
	return r, nil
}

// preflight checks what lanes need: git 2.40 or later for merge-tree
// --write-tree, a committer identity, a commit, and the base branch.
func (r *laneRepo) preflight(ctx context.Context, base string) error {
	version, err := r.output(ctx, r.top, "version")
	if err != nil {
		return err
	}
	if !gitAtLeast(version, 2, 40) {
		return &Error{Status: http.StatusConflict, Code: codeGitTooOld, Message: fmt.Sprintf("running a plan needs git 2.40 or later; the server has %s", strings.TrimPrefix(version, "git version "))}
	}
	if _, code, stderr, err := runGit(ctx, r.git, r.top, 4096, "var", "GIT_COMMITTER_IDENT"); err != nil {
		return err
	} else if code != 0 {
		return &Error{Status: http.StatusConflict, Code: codeNoGitIdentity, Message: "git has no committer identity for this repository; set user.name and user.email: " + gitMessage(stderr)}
	}
	if !r.hasHead {
		return newError(http.StatusConflict, "the repository has no commit yet")
	}
	_, code, _, err := runGit(ctx, r.git, r.top, 4096, "check-ref-format", "refs/heads/"+base)
	if err != nil {
		return err
	}
	var tip string
	if code == 0 {
		if tip, err = r.tipOf(ctx, base); err != nil {
			return err
		}
	}
	if tip == "" {
		return newError(http.StatusConflict, "there is no branch %s", displaytext.Sanitize(base))
	}
	return nil
}

// gitAtLeast reports whether `git version` printed a version at least
// major.minor.
func gitAtLeast(version string, major, minor int) bool {
	fields := strings.Fields(version)
	if len(fields) < 3 {
		return false
	}
	parts := strings.SplitN(fields[2], ".", 3)
	if len(parts) < 2 {
		return false
	}
	gotMajor, err1 := strconv.Atoi(parts[0])
	gotMinor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return gotMajor > major || gotMajor == major && gotMinor >= minor
}

// syncInteg brings base's commits onto the integration branch and returns
// its tip. It creates the branch at base's tip on first need. Later it
// merges base in as objects, the integration branch as first parent, and
// never fast-forwards, so every landing stays on its first-parent line. It
// skips a merge that would change nothing (see hasAll). A conflict refuses
// merge_conflict and moves nothing.
func (r *laneRepo) syncInteg(ctx context.Context, base string) (string, error) {
	baseTip, err := r.tipOf(ctx, base)
	if err != nil {
		return "", err
	}
	if baseTip == "" {
		return "", newError(http.StatusConflict, "there is no branch %s", displaytext.Sanitize(base))
	}
	tip, err := r.tipOf(ctx, r.integ)
	if err != nil {
		return "", err
	}
	if tip == "" {
		if err := r.moveBranch(ctx, r.integ, baseTip, ""); err != nil {
			return "", err
		}
		return baseTip, nil
	}
	if synced, err := r.hasAll(ctx, tip, baseTip); err != nil || synced {
		return tip, err
	}
	tree, conflicts, err := r.mergeTree(ctx, tip, baseTip)
	if err != nil {
		return "", err
	}
	if len(conflicts) > 0 {
		cards := r.cardsTouching(ctx, baseTip, tip, conflicts)
		return "", &Error{Status: http.StatusConflict, Code: codeMergeConflict, Refs: cards,
			Message: fmt.Sprintf("%s does not merge cleanly into %s: %s conflict%s", displaytext.Sanitize(base), r.integ, fileList(conflicts), changedBy(cards))}
	}
	merged, err := r.commitTree(ctx, tree, fmt.Sprintf("Merge %s into %s\n", base, r.integ), tip, baseTip)
	if err != nil {
		return "", err
	}
	if err := r.moveBranch(ctx, r.integ, merged, tip); err != nil {
		return "", err
	}
	return merged, nil
}

// addLane makes the attempt's worktree on a new branch at tip.
func (r *laneRepo) addLane(ctx context.Context, l lane, tip string) error {
	if _, err := runLaneGit(ctx, gitAt{dir: r.top}, "worktree", "add", "--quiet", "-b", l.branch, l.dir, tip); err != nil {
		return gitFailed("git worktree add failed", err)
	}
	return nil
}

// removeLane ends an attempt's worktree: it aborts a merge in progress,
// commits what is left to the attempt branch, removes the worktree, and
// deletes the branch only when its work landed. A worktree already gone is
// pruned.
func (r *laneRepo) removeLane(ctx context.Context, l lane, seq int64, landed bool) error {
	switch _, err := os.Stat(l.dir); {
	case errors.Is(err, fs.ErrNotExist):
		if _, err := runLaneGit(ctx, gitAt{dir: r.top}, "worktree", "prune"); err != nil {
			return gitFailed("git worktree prune failed", err)
		}
	case err != nil:
		return err
	default:
		a, err := r.laneAt(l)
		if err != nil {
			return err
		}
		merging, err := r.mergeHead(ctx, a)
		if err != nil {
			return err
		}
		if merging {
			if _, err := runLaneGit(ctx, a, "merge", "--abort"); err != nil {
				return gitFailed("git merge --abort failed", err)
			}
		}
		if _, err := r.commitLeftovers(ctx, l, seq); err != nil {
			return err
		}
		// Pinned to the lane, as git checks it is clean with a git status
		// run there: the hooks the lane's own configuration defines are off
		// too.
		if _, err := runLaneGit(ctx, gitAt{dir: r.top, pin: a.pin}, "worktree", "remove", l.dir); err != nil {
			return gitFailed("git worktree remove failed", err)
		}
	}
	if !landed {
		return nil
	}
	if tip, err := r.tipOf(ctx, l.branch); err != nil || tip == "" {
		return err
	}
	if _, err := runLaneGit(ctx, gitAt{dir: r.top}, "branch", "-D", l.branch); err != nil {
		return gitFailed("git branch -D failed", err)
	}
	return nil
}

// commitLeftovers commits what the attempt left uncommitted to the
// attempt's branch, by compare-and-swap, and reports whether there was
// anything. Like laneHead it refuses unless the lane has that branch
// checked out.
func (r *laneRepo) commitLeftovers(ctx context.Context, l lane, seq int64) (bool, error) {
	a, err := r.laneAt(l)
	if err != nil {
		return false, err
	}
	head, err := r.headAt(ctx, a, l)
	if err != nil {
		return false, err
	}
	if _, err := runLaneGit(ctx, a, "add", "-A"); err != nil {
		return false, gitFailed("git add failed", err)
	}
	// write-tree rewrites the index, which runs post-index-change hooks.
	out, err := runLaneGit(ctx, a, "write-tree")
	if err != nil {
		return false, gitFailed("git write-tree failed", err)
	}
	tree := out[strings.LastIndexByte(out, '\n')+1:]
	if was, err := r.output(ctx, r.top, "rev-parse", head+"^{tree}"); err != nil || was == tree {
		return false, err
	}
	commit, err := r.commitTree(ctx, tree, fmt.Sprintf("#%d: work in progress\n", seq), head)
	if err != nil {
		return false, err
	}
	if _, err := runLaneGit(ctx, gitAt{dir: r.top}, "update-ref", "-m", "uam", "refs/heads/"+l.branch, commit, head); err != nil {
		return false, gitFailed("git update-ref failed", err)
	}
	return true, nil
}

// syncForLanding keeps a landing from carrying base's own commits. When the
// lane l holds commits of base that the integration tip lacks, as after the
// agent merged base into its lane, it syncs the integration branch with
// base first, so the landing is the lane's own work alone and a later
// Revert of it never takes base's commits out of base. When the
// integration branch still lacks them, as when base does not merge cleanly
// into it, it refuses land_stale with the agent's steps and moves nothing.
// base "" syncs nothing.
func (r *laneRepo) syncForLanding(ctx context.Context, l lane, base string) error {
	if base == "" {
		return nil
	}
	head, err := r.laneHead(ctx, l)
	if err != nil {
		return err
	}
	if carries, err := r.carriesBase(ctx, head, base); err != nil || !carries {
		return err
	}
	if _, err := r.syncInteg(ctx, base); err != nil && apiCode(err) != codeMergeConflict {
		return err
	}
	if carries, err := r.carriesBase(ctx, head, base); err != nil || !carries {
		return err
	}
	b := displaytext.Sanitize(base)
	return &Error{Status: http.StatusConflict, Code: codeLandStale,
		Message: fmt.Sprintf("your lane has commits of %s that %s lacks and cannot take now, so landing would carry %s's own work: take your merge of %s out of your branch, run `git merge %s` instead, commit, and file done again",
			b, r.integ, b, b, r.integ)}
}

// carriesBase reports whether the commit head has commits of base that the
// integration tip lacks: a best common ancestor of head and base that the
// tip does not have all of (see hasAll).
func (r *laneRepo) carriesBase(ctx context.Context, head, base string) (bool, error) {
	baseTip, err := r.tipOf(ctx, base)
	if err != nil || baseTip == "" {
		return false, err
	}
	tip, err := r.tipOf(ctx, r.integ)
	if err != nil || tip == "" {
		return false, err
	}
	out, code, stderr, err := runGit(ctx, r.git, r.top, 64<<10, "merge-base", "--all", head, baseTip)
	switch {
	case err != nil:
		return false, err
	case code == 1:
		return false, nil // no common history
	case code != 0:
		return false, newError(http.StatusBadGateway, "git merge-base failed: %s", gitMessage(stderr))
	}
	for _, common := range strings.Fields(string(out)) {
		if has, err := r.hasAll(ctx, tip, common); err != nil || !has {
			return err == nil, err
		}
	}
	return false, nil
}

// checkLane refuses to land the attempt as it is: land_conflict while its
// worktree is mid-merge or has unmerged paths, so leftovers are never
// committed with conflict markers in them; land_stale when it holds a
// landing or a revert that the integration tip no longer has, which only an
// edit of the integration branch by hand causes.
func (r *laneRepo) checkLane(ctx context.Context, l lane, tip string) error {
	if err := r.notMerging(ctx, l); err != nil {
		return err
	}
	a, err := r.laneAt(l)
	if err != nil {
		return err
	}
	out, err := r.outputAt(ctx, a, "log", "--format=%(trailers:key="+trailerRequest+",key="+trailerRevert+",valueonly)", tip+"..HEAD", "--")
	if err != nil {
		return err
	}
	if out != "" {
		return &Error{Status: http.StatusConflict, Code: codeLandStale,
			Message: fmt.Sprintf("your lane holds landed work that %s no longer has, so it cannot land; end your turn", r.integ)}
	}
	return nil
}

// notMerging refuses land_conflict while the attempt's worktree is
// mid-merge or has unmerged paths.
func (r *laneRepo) notMerging(ctx context.Context, l lane) error {
	a, err := r.laneAt(l)
	if err != nil {
		return err
	}
	merging, err := r.mergeHead(ctx, a)
	if err != nil {
		return err
	}
	files, err := r.unmerged(ctx, a)
	if err != nil {
		return err
	}
	if !merging && len(files) == 0 {
		return nil
	}
	msg := fmt.Sprintf("finish your merge of %s and commit, then file done again", r.integ)
	if len(files) > 0 {
		msg += "; unmerged: " + fileList(files)
	}
	return &Error{Status: http.StatusConflict, Code: codeLandConflict, Message: msg}
}

// mergeHead reports whether the worktree at a is mid-merge.
func (r *laneRepo) mergeHead(ctx context.Context, a gitAt) (bool, error) {
	_, code, _, err := runGit(ctx, r.git, a.dir, 4096, a.argv("rev-parse", "--verify", "--quiet", "MERGE_HEAD")...)
	return err == nil && code == 0, err
}

// unmerged lists the unmerged paths of the worktree at a.
func (r *laneRepo) unmerged(ctx context.Context, a gitAt) ([]string, error) {
	out, err := r.outputAt(ctx, a, "ls-files", "--unmerged", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, rec := range nulRecords([]byte(out)) {
		if _, path, ok := strings.Cut(rec, "\t"); ok && !slices.Contains(files, path) {
			files = append(files, path)
		}
	}
	return files, nil
}

// mergeTip merges the integration tip into the attempt when the lane lacks
// it. A merge commit carries a Uam-Merge trailer naming the tip, so uam can
// tell its own merges from the agent's. A lane already mid-merge refuses as
// checkLane does, untouched. On a conflict it aborts, leaving the lane as it
// was, and refuses land_conflict with the files, the cards that changed them
// since the lane forked, and the agent's steps.
func (r *laneRepo) mergeTip(ctx context.Context, l lane, tip string) error {
	a, err := r.laneAt(l)
	if err != nil {
		return err
	}
	if err := r.notMerging(ctx, l); err != nil {
		return err
	}
	head, err := r.headAt(ctx, a, l)
	if err != nil {
		return err
	}
	if has, err := r.isAncestor(ctx, tip, head); err != nil || has {
		return err
	}
	msg := fmt.Sprintf("Merge %s\n\n%s: %s\n", r.integ, trailerMerge, tip)
	_, mergeErr := runLaneGit(ctx, a, slices.Concat(pinnedMerge, []string{"--ff", "-m", msg, tip})...)
	if mergeErr == nil {
		return nil
	}
	merging, err := r.mergeHead(ctx, a)
	if err != nil {
		return err
	}
	if !merging {
		return gitFailed("git merge failed", mergeErr)
	}
	files, err := r.unmerged(ctx, a)
	if err != nil {
		return err
	}
	if _, err := runLaneGit(ctx, a, "merge", "--abort"); err != nil {
		return gitFailed("git merge --abort failed", err)
	}
	if len(files) == 0 {
		return gitFailed("git merge failed", mergeErr)
	}
	var cards []string
	if base, err := r.output(ctx, r.top, "merge-base", head, tip); err == nil {
		cards = r.cardsTouching(ctx, base, tip, files)
	}
	return &Error{Status: http.StatusConflict, Code: codeLandConflict, Refs: cards,
		Message: fmt.Sprintf("merging %s into your lane conflicts in %s%s: run `git merge %s` in your directory, resolve %s, commit, and file done again",
			r.integ, fileList(files), changedBy(cards), r.integ, fileList(files))}
}

// squashLane writes the landing commit: the lane HEAD's tree on top of tip,
// as one commit. It refuses unless the lane has tip, since the tree would
// otherwise undo what landed after the lane forked. It moves no ref.
func (r *laneRepo) squashLane(ctx context.Context, l lane, tip, message string) (string, error) {
	head, err := r.laneHead(ctx, l)
	if err != nil {
		return "", err
	}
	if has, err := r.isAncestor(ctx, tip, head); err != nil {
		return "", err
	} else if !has {
		return "", newError(http.StatusConflict, "the lane lacks %s; merge it in before landing", shortSHA(tip))
	}
	return r.commitTree(ctx, head+"^{tree}", message, tip)
}

// landMessage is a landing's commit message: the title, the claim, and the
// trailers naming the card and the request.
func landMessage(title string, seq int64, claim, requestID string) string {
	msg := fmt.Sprintf("%s (#%d)\n\n", strings.TrimSpace(title), seq)
	if claim = strings.TrimSpace(claim); claim != "" {
		msg += claim + "\n\n"
	}
	return msg + fmt.Sprintf("%s: #%d\n%s: %s\n", trailerCard, seq, trailerRequest, requestID)
}

// moveBranch moves branch from the commit from to to with a
// compare-and-swap; from "" creates it only when it does not exist. It
// refuses git_busy, to retry later, while a worktree has the branch checked
// out or when another writer moved it first.
func (r *laneRepo) moveBranch(ctx context.Context, branch, to, from string) error {
	dir, err := r.checkedOut(ctx, branch)
	if err != nil {
		return err
	}
	if dir != "" {
		return &Error{Status: http.StatusConflict, Code: codeGitBusy,
			Message: fmt.Sprintf("%s is checked out in %s; uam moves it only while no worktree has it checked out", displaytext.Sanitize(branch), displaytext.Sanitize(dir))}
	}
	_, err = runLaneGit(ctx, gitAt{dir: r.top}, "update-ref", "-m", "uam", "refs/heads/"+branch, to, from)
	if err == nil {
		return nil
	}
	if now, terr := r.tipOf(ctx, branch); terr == nil && now != from {
		return &Error{Status: http.StatusConflict, Code: codeGitBusy, Message: fmt.Sprintf("%s moved while uam was updating it", displaytext.Sanitize(branch))}
	}
	return gitFailed("git update-ref failed", err)
}

// finishOnInteg finishes an intent whose commit is x (ADR 0006): it reports
// true when x is on the integration branch, fast-forwarding the branch to x
// when it is behind, and false when the two diverged or x is gone, so the
// caller undoes the intent and redoes the work.
func (r *laneRepo) finishOnInteg(ctx context.Context, x string) (bool, error) {
	tip, err := r.tipOf(ctx, r.integ)
	if err != nil || tip == "" {
		return false, err
	}
	if exists, err := r.hasCommit(ctx, x); err != nil || !exists {
		return false, err
	}
	if on, err := r.isAncestor(ctx, x, tip); err != nil || on {
		return on, err
	}
	if behind, err := r.isAncestor(ctx, tip, x); err != nil || !behind {
		return false, err
	}
	if err := r.moveBranch(ctx, r.integ, x, tip); err != nil {
		return false, err
	}
	return true, nil
}

// revertItem is a landing to revert: its commit, card and title.
type revertItem struct {
	sha   string
	seq   int64
	title string
}

// revertChain builds, as objects only, one revert commit per landing on top
// of tip, newest landing first by its place on the integration branch's
// first-parent line, and returns the last. It moves no ref. A landing off
// that line, or a conflict, refuses revert_conflict; a conflict names the
// files and the later landings that changed them.
func (r *laneRepo) revertChain(ctx context.Context, tip string, items []revertItem) (string, error) {
	line, err := r.output(ctx, r.top, "rev-list", "--first-parent", tip, "--")
	if err != nil {
		return "", err
	}
	place := map[string]int{}
	for i, sha := range strings.Fields(line) {
		place[sha] = i
	}
	items = slices.Clone(items)
	for _, it := range items {
		if _, ok := place[it.sha]; !ok {
			return "", &Error{Status: http.StatusConflict, Code: codeRevertConflict,
				Message: fmt.Sprintf("#%d landed as %s, which is not on %s; reopen it without reverting code", it.seq, shortSHA(it.sha), r.integ)}
		}
	}
	slices.SortFunc(items, func(a, b revertItem) int { return place[a.sha] - place[b.sha] })
	cur := tip
	for _, it := range items {
		tree, conflicts, err := r.mergeTree(ctx, "--merge-base="+it.sha, cur, it.sha+"^")
		if err != nil {
			return "", err
		}
		if len(conflicts) > 0 {
			cards := r.cardsTouching(ctx, it.sha, tip, conflicts)
			return "", &Error{Status: http.StatusConflict, Code: codeRevertConflict, Refs: cards,
				Message: fmt.Sprintf("reverting #%d conflicts in %s%s; include those cards or reopen #%d without reverting code", it.seq, fileList(conflicts), changedBy(cards), it.seq)}
		}
		msg := fmt.Sprintf("Revert #%d %s\n\nThis reverts %s.\n\n%s: #%d\n", it.seq, strings.TrimSpace(it.title), it.sha, trailerRevert, it.seq)
		if cur, err = r.commitTree(ctx, tree, msg, cur); err != nil {
			return "", err
		}
	}
	return cur, nil
}

// mergeIntoBase merges the integration branch into the owner's branch base
// and returns base's new tip, "" when base already has it all (see hasAll),
// so uam's sync merges never come back to base as merges that change
// nothing. Where a worktree has base checked out it runs git merge there, so
// git refuses to overwrite local changes; otherwise it merges as objects and
// moves base by compare-and-swap. Either way it runs no repository hook: it
// is unattended, and agents can write the repository's configuration (see
// runLaneGit). A lane under lanes, the lanes root, with base checked out
// refuses git_busy: uam runs no git merge in a lane. A conflict refuses
// merge_conflict before anything is touched. The caller holds the land
// mutex, and beginWrite when base is checked out.
func (r *laneRepo) mergeIntoBase(ctx context.Context, lanes, base, message string) (string, error) {
	baseTip, err := r.tipOf(ctx, base)
	if err != nil {
		return "", err
	}
	tip, err := r.tipOf(ctx, r.integ)
	if err != nil {
		return "", err
	}
	if baseTip == "" || tip == "" {
		return "", newError(http.StatusConflict, "there is no branch %s or %s", displaytext.Sanitize(base), r.integ)
	}
	if merged, err := r.hasAll(ctx, baseTip, tip); err != nil || merged {
		return "", err
	}
	tree, conflicts, err := r.mergeTree(ctx, baseTip, tip)
	if err != nil {
		return "", err
	}
	if len(conflicts) > 0 {
		cards := r.cardsTouching(ctx, baseTip, tip, conflicts)
		return "", &Error{Status: http.StatusConflict, Code: codeMergeConflict, Refs: cards,
			Message: fmt.Sprintf("%s does not merge cleanly into %s: %s conflict%s", r.integ, displaytext.Sanitize(base), fileList(conflicts), changedBy(cards))}
	}
	dir, err := r.checkedOut(ctx, base)
	if err != nil {
		return "", err
	}
	if dir != "" && inDir(realPath(lanes), realPath(dir)) {
		return "", &Error{Status: http.StatusConflict, Code: codeGitBusy,
			Message: fmt.Sprintf("%s is checked out in the lane %s; uam merges into it only where you have it checked out", displaytext.Sanitize(base), displaytext.Sanitize(dir))}
	}
	if dir != "" {
		return r.mergeIn(ctx, dir, base, baseTip, tip, message)
	}
	merged, err := r.commitTree(ctx, tree, message, baseTip, tip)
	if err != nil {
		return "", err
	}
	if err := r.moveBranch(ctx, base, merged, baseTip); err != nil {
		return "", err
	}
	return merged, nil
}

// mergeIn runs git merge of tip into baseTip, checked out in the worktree
// at dir, and returns the merge commit, through runLaneGit: no hooks and no
// fsmonitor, and it signs nothing and checks no signature, as the merge as
// objects does. The merge is pinned (pinnedMerge), and only a merge commit
// whose parents are baseTip and tip counts as merged. It refuses
// local_changes while dir is in the middle of a git operation or has
// unmerged paths. A merge it started and could not finish, for example
// behind another git process's lock, is aborted, so dir is never left
// mid-merge by uam.
func (r *laneRepo) mergeIn(ctx context.Context, dir, base, baseTip, tip, message string) (string, error) {
	if op, err := r.inProgress(ctx, dir); err != nil {
		return "", err
	} else if op != "" {
		return "", &Error{Status: http.StatusConflict, Code: codeLocalChanges, Message: fmt.Sprintf("%s is in progress in %s; finish or abort it first", op, displaytext.Sanitize(dir))}
	}
	if files, err := r.unmerged(ctx, gitAt{dir: dir}); err != nil {
		return "", err
	} else if len(files) > 0 {
		return "", &Error{Status: http.StatusConflict, Code: codeLocalChanges, Message: fmt.Sprintf("%s has unmerged paths: %s; resolve and commit them first", displaytext.Sanitize(dir), fileList(files))}
	}
	if err := r.onBase(ctx, dir, base, baseTip); err != nil {
		return "", err
	}
	_, mergeErr := runLaneGit(ctx, gitAt{dir: dir}, slices.Concat(pinnedMerge, []string{"--no-ff", "-m", message, tip})...)
	if mergeErr == nil {
		line, err := r.output(ctx, dir, "rev-list", "--parents", "-n1", "HEAD", "--")
		if err != nil {
			return "", err
		}
		if f := strings.Fields(line); len(f) == 3 && f[1] == baseTip && f[2] == tip {
			return f[0], nil
		}
		mergeErr = newError(http.StatusConflict, "git merge in %s did not make a merge of %s and %s", displaytext.Sanitize(dir), shortSHA(baseTip), shortSHA(tip))
	}
	if err := r.abortOwnMergeIn(ctx, dir, tip, message); err != nil {
		return "", err
	}
	var gerr *gitError
	if errors.As(mergeErr, &gerr) && containsAny(gerr.output, "would be overwritten by merge") {
		e := gitFailed(fmt.Sprintf("merging %s would overwrite uncommitted changes in %s; commit or stash them", r.integ, displaytext.Sanitize(dir)), mergeErr)
		e.Code = codeLocalChanges
		return "", e
	}
	return "", gitFailed("git merge failed", mergeErr)
}

// abortOwnMergeIn aborts the merge in progress in the worktree at dir when
// it is uam's: of tip alone, with uam's message. Any other merge there, the
// owner's own merge of the integration branch included, is left as it is
// and refused local_changes, to retry once the owner finished it.
func (r *laneRepo) abortOwnMergeIn(ctx context.Context, dir, tip, message string) error {
	paths, err := r.gitPaths(ctx, gitAt{dir: dir}, "MERGE_HEAD", "MERGE_MSG")
	if err != nil {
		return err
	}
	head, err := readGitFile(paths[0])
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	msg, err := readGitFile(paths[1])
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	rest, own := strings.CutPrefix(msg, strings.TrimSpace(message))
	if strings.TrimSpace(head) == tip && own && (rest == "" || rest[0] == '\n') {
		if _, err := runLaneGit(ctx, gitAt{dir: dir}, "merge", "--abort"); err != nil {
			return gitFailed("git merge --abort failed", err)
		}
		return nil
	}
	return &Error{Status: http.StatusConflict, Code: codeLocalChanges,
		Message: fmt.Sprintf("a merge uam did not start is in progress in %s; uam left it as it is: finish or abort it", displaytext.Sanitize(dir))}
}

// maxGitFile is the most uam reads of a file git keeps in its directory.
const maxGitFile = 64 << 10

// readGitFile reads a file git keeps in its directory, such as MERGE_MSG:
// a regular file, opened without following a symbolic link or waiting on a
// FIFO, of which it reads at most maxGitFile bytes.
func readGitFile(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) // #nosec G304 G703 -- a path git names inside its own directory.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	b, err := io.ReadAll(io.LimitReader(f, maxGitFile))
	return string(b), err
}

// onBase refuses git_busy, to retry later, unless the worktree at dir is
// one of this repository's, by its common git directory, with base checked
// out at baseTip: git merge goes into whatever dir has checked out.
func (r *laneRepo) onBase(ctx context.Context, dir, base, baseTip string) error {
	ref, code, stderr, err := runGit(ctx, r.git, dir, 4096, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return err
	}
	if code != 0 && code != 1 {
		return newError(http.StatusBadGateway, "git symbolic-ref failed: %s", gitMessage(stderr))
	}
	out, err := r.output(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir", "HEAD^{commit}")
	if err != nil {
		return err
	}
	common, head, _ := strings.Cut(out, "\n")
	if code == 0 && strings.TrimSpace(string(ref)) == "refs/heads/"+base && head == baseTip && realPath(common) == realPath(r.common) {
		return nil
	}
	return &Error{Status: http.StatusConflict, Code: codeGitBusy,
		Message: fmt.Sprintf("%s no longer has %s checked out at %s; uam merges once it is", displaytext.Sanitize(dir), displaytext.Sanitize(base), shortSHA(baseTip))}
}

// laneAt is where uam runs git in the lane l: its directory, with git pinned
// to its worktree and to the git directory git made for it under the
// repository's common directory when uam added it, never to what the .git
// file in the agent's directory names. It refuses while the lane's
// directory is a symbolic link, or that git directory is missing or names
// another worktree or repository, and leaves the lane where it is.
func (r *laneRepo) laneAt(l lane) (gitAt, error) {
	gitDir := filepath.Join(r.common, "worktrees", filepath.Base(l.dir))
	dotGit, err := adminPath(gitDir, "gitdir")
	common, cerr := adminPath(gitDir, "commondir")
	info, lerr := os.Lstat(l.dir)
	if err != nil || cerr != nil || lerr != nil || !info.IsDir() || filepath.Base(dotGit) != ".git" ||
		realPath(filepath.Dir(dotGit)) != realPath(l.dir) || realPath(common) != realPath(r.common) {
		return gitAt{}, newError(http.StatusConflict, "%s is not a lane worktree of this repository: it is a symbolic link, or its git directory is missing or names another worktree or repository", displaytext.Sanitize(l.dir))
	}
	return gitAt{dir: l.dir, pin: []string{"--git-dir=" + gitDir, "--work-tree=" + l.dir}}, nil
}

// adminPath is the path the file name in a lane's git directory gitDir
// holds, a relative one taken from gitDir (worktree.useRelativePaths).
func adminPath(gitDir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(gitDir, name)) // #nosec G304 -- a file git keeps in the repository's own git directory.
	path := strings.TrimSpace(string(b))
	if err == nil && path == "" {
		err = fmt.Errorf("%s is empty", name)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(gitDir, path)
	}
	return path, err
}

// laneHead is the commit the lane l has checked out: its branch's tip. It
// refuses unless the lane's HEAD names that branch, so uam never commits or
// merges onto another branch, nor lands one.
func (r *laneRepo) laneHead(ctx context.Context, l lane) (string, error) {
	a, err := r.laneAt(l)
	if err != nil {
		return "", err
	}
	return r.headAt(ctx, a, l)
}

// headAt is laneHead for the lane l at a.
func (r *laneRepo) headAt(ctx context.Context, a gitAt, l lane) (string, error) {
	ref, code, stderr, err := runGit(ctx, r.git, a.dir, 4096, a.argv("symbolic-ref", "--quiet", "HEAD")...)
	switch {
	case err != nil:
		return "", err
	case code != 0 && code != 1:
		return "", newError(http.StatusBadGateway, "git symbolic-ref failed: %s", gitMessage(stderr))
	case code == 1 || strings.TrimSpace(string(ref)) != "refs/heads/"+l.branch:
		return "", newError(http.StatusConflict, "the lane at %s does not have its branch %s checked out; switch back to it", displaytext.Sanitize(l.dir), l.branch)
	}
	head, err := r.tipOf(ctx, l.branch)
	if err == nil && head == "" {
		err = newError(http.StatusConflict, "there is no branch %s", l.branch)
	}
	return head, err
}

// tipOf is the commit branch names, "" when there is no such branch.
func (r *laneRepo) tipOf(ctx context.Context, branch string) (string, error) {
	out, code, stderr, err := runGit(ctx, r.git, r.top, 4096, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	switch {
	case err != nil:
		return "", err
	case code == 0:
		return strings.TrimSpace(string(out)), nil
	case strings.TrimSpace(stderr) == "":
		return "", nil
	}
	return "", newError(http.StatusBadGateway, "git rev-parse failed: %s", gitMessage(stderr))
}

// isAncestor reports whether commit a is b or an ancestor of it.
func (r *laneRepo) isAncestor(ctx context.Context, a, b string) (bool, error) {
	_, code, stderr, err := runGit(ctx, r.git, r.top, 4096, "merge-base", "--is-ancestor", a, b)
	switch {
	case err != nil:
		return false, err
	case code == 0 || code == 1:
		return code == 0, nil
	}
	return false, newError(http.StatusBadGateway, "git merge-base failed: %s", gitMessage(stderr))
}

// hasAll reports whether merging commit b into commit a would bring
// nothing: b is a or an ancestor of it, or a is an ancestor of b with the
// same tree, so b adds only merges that change nothing. Skipping that merge
// loses no history git needs later, since a stays their merge base.
func (r *laneRepo) hasAll(ctx context.Context, a, b string) (bool, error) {
	if has, err := r.isAncestor(ctx, b, a); err != nil || has {
		return has, err
	}
	if behind, err := r.isAncestor(ctx, a, b); err != nil || !behind {
		return false, err
	}
	trees, err := r.output(ctx, r.top, "rev-parse", a+"^{tree}", b+"^{tree}")
	if err != nil {
		return false, err
	}
	t := strings.Fields(trees)
	return len(t) == 2 && t[0] == t[1], nil
}

// checkedOut is the worktree that has branch checked out, "" when none has.
// Like git, it counts a worktree in the middle of a rebase or a bisect that
// started from branch, which git lists as detached. More than one refuses
// git_busy: uam then moves or merges into branch nowhere.
func (r *laneRepo) checkedOut(ctx context.Context, branch string) (string, error) {
	out, err := r.output(ctx, r.top, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", err
	}
	var dir string
	var dirs, detached []string
	for _, line := range strings.Split(out, "\x00") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			dir = path
		} else if line == "branch refs/heads/"+branch {
			dirs = append(dirs, dir)
		} else if line == "detached" {
			detached = append(detached, dir)
		}
	}
	for _, dir := range detached {
		if _, err := os.Stat(dir); err != nil { // #nosec G703 -- a worktree directory git lists for the Project's repository; only checks it exists.
			continue // its directory is gone, so nothing can go on there
		}
		if from, err := r.startedFrom(ctx, dir, branch); err != nil {
			return "", err
		} else if from {
			dirs = append(dirs, dir)
		}
	}
	switch len(dirs) {
	case 0:
		return "", nil
	case 1:
		return dirs[0], nil
	}
	return "", &Error{Status: http.StatusConflict, Code: codeGitBusy,
		Message: fmt.Sprintf("%s is checked out in more than one worktree: %s; uam leaves it alone until only one has it", displaytext.Sanitize(branch), fileList(dirs))}
}

// startedFrom reports whether the worktree at dir is in the middle of a
// rebase or a bisect that started from branch, from the files git keeps
// for each, as git itself checks before it moves a branch.
func (r *laneRepo) startedFrom(ctx context.Context, dir, branch string) (bool, error) {
	paths, err := r.gitPaths(ctx, gitAt{dir: dir}, "rebase-merge/head-name", "rebase-apply/head-name", "BISECT_START")
	if err != nil {
		return false, err
	}
	for _, path := range paths {
		name, err := readGitFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if strings.TrimPrefix(strings.TrimSpace(name), "refs/heads/") == branch {
			return true, nil
		}
	}
	return false, nil
}

// inProgress names the git operation the worktree at dir is in the middle
// of, "" when none.
func (r *laneRepo) inProgress(ctx context.Context, dir string) (string, error) {
	for _, op := range []struct{ ref, name string }{{"MERGE_HEAD", "a merge"}, {"CHERRY_PICK_HEAD", "a cherry-pick"}, {"REVERT_HEAD", "a revert"}} {
		_, code, _, err := runGit(ctx, r.git, dir, 4096, "rev-parse", "--verify", "--quiet", op.ref)
		if err != nil {
			return "", err
		}
		if code == 0 {
			return op.name, nil
		}
	}
	paths, err := r.gitPaths(ctx, gitAt{dir: dir}, "rebase-merge", "rebase-apply", "BISECT_LOG")
	if err != nil {
		return "", err
	}
	for i, name := range []string{"a rebase", "a rebase or am", "a bisect"} {
		if _, err := os.Lstat(paths[i]); err == nil { // #nosec G703 -- a path git names inside its own directory; only checks it exists.
			return name, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}

// gitPaths is where the worktree at a keeps each of the files git names
// relative to its git directory.
func (r *laneRepo) gitPaths(ctx context.Context, a gitAt, names ...string) ([]string, error) {
	args := []string{"rev-parse", "--path-format=absolute"}
	for _, name := range names {
		args = append(args, "--git-path", name)
	}
	out, err := r.outputAt(ctx, a, args...)
	if err != nil {
		return nil, err
	}
	paths := strings.Split(out, "\n")
	if len(paths) != len(names) {
		return nil, newError(http.StatusBadGateway, "git rev-parse --git-path named %d paths for %d files", len(paths), len(names))
	}
	return paths, nil
}

// mergeTree merges as objects only and returns the tree, or the
// conflicted paths when the merge is not clean.
func (r *laneRepo) mergeTree(ctx context.Context, args ...string) (string, []string, error) {
	argv := append([]string{"merge-tree", "--write-tree", "-z", "--name-only", "--no-messages"}, args...)
	out, code, stderr, err := runGit(ctx, r.git, r.top, maxStatusBytes, argv...)
	if err != nil {
		return "", nil, err
	}
	recs := nulRecords(out)
	if (code != 0 && code != 1) || len(recs) == 0 {
		return "", nil, newError(http.StatusBadGateway, "git merge-tree failed: %s", gitMessage(stderr))
	}
	if code == 1 {
		files := slices.Clone(recs[1:])
		slices.Sort(files)
		return "", slices.Compact(files), nil
	}
	return recs[0], nil, nil
}

// commitTree writes a commit of tree with message and parents, moving no
// ref.
func (r *laneRepo) commitTree(ctx context.Context, tree, message string, parents ...string) (string, error) {
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	out, code, stderr, err := runGitInput(ctx, r.git, r.top, strings.NewReader(message), 4096, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", newError(http.StatusBadGateway, "git commit-tree failed: %s", gitMessage(stderr))
	}
	return strings.TrimSpace(string(out)), nil
}

// cardsTouching lists, newest first, the cards whose landings in from..to
// changed any of files. It is best effort: nil when git cannot tell.
func (r *laneRepo) cardsTouching(ctx context.Context, from, to string, files []string) []string {
	args := append([]string{"--literal-pathspecs", "log", "--format=%(trailers:key=" + trailerCard + ",valueonly)", from + ".." + to, "--"}, files...)
	out, code, _, err := runGit(ctx, r.git, r.top, 64<<10, args...)
	if err != nil || code != 0 {
		return nil
	}
	var cards []string
	for _, card := range strings.Fields(string(out)) {
		if cardTrailer.MatchString(card) && !slices.Contains(cards, card) {
			cards = append(cards, card)
		}
	}
	return cards
}

// output runs a git command that must succeed in dir and returns its
// output without surrounding white space.
func (r *laneRepo) output(ctx context.Context, dir string, args ...string) (string, error) {
	return r.outputAt(ctx, gitAt{dir: dir}, args...)
}

// outputAt is output at a.
func (r *laneRepo) outputAt(ctx context.Context, a gitAt, args ...string) (string, error) {
	out, code, stderr, err := runGit(ctx, r.git, a.dir, maxStatusBytes, a.argv(args...)...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", newError(http.StatusBadGateway, "git %s failed: %s", args[0], gitMessage(stderr))
	}
	return strings.TrimSpace(string(out)), nil
}

// fileList names up to maxNamedFiles paths, made safe to show.
func fileList(files []string) string {
	shown := files[:min(len(files), maxNamedFiles)]
	names := make([]string, len(shown))
	for i, f := range shown {
		names[i] = displaytext.Sanitize(f)
	}
	list := strings.Join(names, ", ")
	if more := len(files) - len(shown); more > 0 {
		list += fmt.Sprintf(" and %d more", more)
	}
	return list
}

// changedBy is " (changed by #3, #5)", or "" for no cards.
func changedBy(cards []string) string {
	if len(cards) == 0 {
		return ""
	}
	return " (changed by " + strings.Join(cards, ", ") + ")"
}

// shortSHA is the first 7 characters of a commit name.
func shortSHA(sha string) string { return sha[:min(len(sha), 7)] }
