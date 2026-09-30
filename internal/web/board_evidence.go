package web

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// Evidence-backed done (ADR 0005 §6): a done claim records what changed in
// the Project's working tree since the hold started, then runs the
// subtask's owner-authored acceptance command.
const (
	acceptTimeout   = 10 * time.Minute
	acceptTailBytes = 64 << 10
	// acceptWaitDelay is how long the output of a command that has exited,
	// or been killed, may stay open, held by a process outside its group.
	acceptWaitDelay    = time.Second
	maxEvidenceCommits = 50
	// maxEvidenceJSON is the store's limit on a request's evidence.
	maxEvidenceJSON = 1 << 20
)

var (
	errAcceptanceBusy = &board.Error{Code: board.CodeAcceptanceBusy, Message: "acceptance busy, retry"}
	// errAcceptanceNotRun marks a run whose shell did not start. It is the
	// only run failure a claim is filed with, flagged.
	errAcceptanceNotRun = errors.New("acceptance could not run")
)

// Evidence is a done request's evidence (ADR 0005 §14).
type Evidence struct {
	Baseline   board.Baseline      `json:"baseline"`
	Diff       EvidenceDiff        `json:"diff"`
	Commits    []EvidenceCommit    `json:"commits"`
	Accept     *AcceptResult       `json:"accept,omitempty"`
	Transcript *EvidenceTranscript `json:"transcript,omitempty"`
	Checklist  EvidenceChecklist   `json:"checklist"`

	// What the flags need, over every changed file rather than the listed
	// ones: whether any is a test, build or CI file, whether any overlaps
	// another hold, and whether the baseline commit was gone.
	testsOrBuild, overlap, baselineMissing bool
}

// EvidenceDiff is the working tree compared with the baseline HEAD, plus
// the untracked files. A path already dirty at the baseline appears only
// when its content changed since. The totals count every file; Files lists
// at most maxChangedFiles.
type EvidenceDiff struct {
	Added   int            `json:"added"`
	Deleted int            `json:"deleted"`
	Files   []EvidenceFile `json:"files"`
}

// EvidenceFile is one changed file. ByTask is set when the claiming Task's
// edit tools touched it, and Overlap names another live hold whose Task
// touched it. PreDirty marks a path already dirty at the baseline; its line
// counts include the changes it had then.
type EvidenceFile struct {
	Path     string           `json:"path"`
	Added    int              `json:"added"`
	Deleted  int              `json:"deleted"`
	ByTask   bool             `json:"by_task"`
	PreDirty bool             `json:"pre_dirty"`
	Overlap  *EvidenceOverlap `json:"overlap,omitempty"`
}

// EvidenceOverlap is another live hold: its subtask's #seq and its Task.
type EvidenceOverlap struct {
	Card   int64  `json:"card"`
	TaskID string `json:"task_id"`
}

// EvidenceCommit is one commit since the baseline.
type EvidenceCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// AcceptResult is one acceptance run. Head and Dirty are the tree it ran
// against; Exit is -1 when the shell did not start. Stale is stored false
// and set as the API sends a done request, once CmdHash is not the hash of
// the command its card resolves to (requestViews).
type AcceptResult struct {
	Cmd     string    `json:"cmd"`
	CmdHash string    `json:"cmd_hash"`
	Head    string    `json:"head"`
	Dirty   bool      `json:"dirty"`
	Exit    int       `json:"exit"`
	Tail    string    `json:"tail"`
	RanAt   time.Time `json:"ran_at"`
	Stale   bool      `json:"stale"`
}

// EvidenceTranscript is the span of the claiming Task's transcript. Partial
// is set when the transcript uam holds does not reach back to the hold's
// start, so the files marked by_task may be incomplete.
type EvidenceTranscript struct {
	TaskID   string `json:"task_id"`
	FromItem string `json:"from_item"`
	ToItem   string `json:"to_item"`
	Partial  bool   `json:"partial,omitempty"`
}

// EvidenceChecklist is the subtask's checklist state.
type EvidenceChecklist struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// commandHash identifies an acceptance command, so rows recorded for an
// earlier command can be shown stale.
func commandHash(cmd string) string {
	sum := sha256.Sum256([]byte(cmd))
	return hex.EncodeToString(sum[:])
}

// acceptCmdOf is the acceptance command c resolves to: its own when set,
// else its Project's default, read from st once per Project into defaults.
func acceptCmdOf(ctx context.Context, st *board.Store, c board.Card, defaults map[string]string) (string, error) {
	if c.AcceptCmd != nil {
		return *c.AcceptCmd, nil
	}
	if cmd, ok := defaults[c.ProjectID]; ok {
		return cmd, nil
	}
	ps, err := st.ProjectSettings(ctx, c.ProjectID)
	if err == nil {
		defaults[c.ProjectID] = ps.AcceptCmd
	}
	return ps.AcceptCmd, err
}

// requestViews is list as the API sends it. A done request's acceptance
// row is marked stale when it ran a command other than the one its card
// resolves to now, so editing a command shows the earlier green rows for it
// stale (ADR 0005 §6) with nothing stored. cards are cards already read;
// any other a row needs is read from st.
func requestViews(ctx context.Context, st *board.Store, list []board.Request, cards []board.Card) ([]BoardRequest, error) {
	out := boardRequests(list)
	byID := make(map[string]board.Card, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	defaults := map[string]string{}
	for i, r := range list {
		if r.Kind != board.RequestDone || len(r.Evidence) == 0 {
			continue
		}
		var ev map[string]json.RawMessage
		var accept *AcceptResult
		if json.Unmarshal(r.Evidence, &ev) != nil || json.Unmarshal(ev["accept"], &accept) != nil || accept == nil {
			continue
		}
		c, ok := byID[r.CardID]
		if !ok {
			var err error
			if c, err = st.Card(ctx, r.CardID); errors.Is(err, board.ErrNotFound) {
				continue
			} else if err != nil {
				return nil, err
			}
			byID[c.ID] = c
		}
		cmd, err := acceptCmdOf(ctx, st, c, defaults)
		if err != nil {
			return nil, err
		}
		if accept.CmdHash == commandHash(cmd) {
			continue
		}
		accept.Stale = true
		if ev["accept"], err = json.Marshal(accept); err != nil {
			return nil, err
		}
		if out[i].Evidence, err = json.Marshal(ev); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// requestView is requestViews of one request.
func requestView(ctx context.Context, st *board.Store, r board.Request) (BoardRequest, error) {
	out, err := requestViews(ctx, st, []board.Request{r}, nil)
	if err != nil {
		return BoardRequest{}, err
	}
	return out[0], nil
}

// CheckCard starts "Check at HEAD" (ADR 0005 §9) on the subtask ref: a job
// that runs its resolved acceptance command in its Project's working tree
// through the Project's runner, as a done claim would. What can be told at
// once is checked before the job starts: a subtask, of a Project with git,
// with a command. The run can outlast what a proxy lets a request take, so
// its outcome comes in board_job frames: done with the run, red or green
// (exit -1 when the shell did not start), or failed with why, such as a
// runner still busy past the timeout. The run is recorded nowhere: green
// rows go stale only in done requests' evidence, which carries the
// command's hash. It returns the job ID.
func (m *Manager) CheckCard(ref string) (string, error) {
	var c board.Card
	var cmd string
	err := m.withBoard(func(st *board.Store) error {
		var err error
		if c, err = st.Card(m.ctx, ref); err != nil {
			return err
		}
		cmd, err = acceptCmdOf(m.ctx, st, c, map[string]string{})
		return err
	})
	switch {
	case err != nil:
		return "", err
	case c.ProjectID == "":
		return "", errUnassigned
	case c.Kind != board.KindSubtask:
		return "", invalidBoard("#%d is a %s; only a subtask has an acceptance command", c.Seq, c.Kind)
	}
	dir, err := m.boardDir(m.ctx, c.ProjectID)
	if err != nil {
		return "", err
	}
	if cmd == "" {
		return "", invalidBoard("#%d has no acceptance command; set one on it or on its project", c.Seq)
	}
	m.mu.Lock()
	job, err := m.startJobLocked(jobCheck, c)
	if err == nil {
		m.broadcastJobLocked(job, jobRunning, "", nil)
	}
	m.mu.Unlock()
	if err != nil {
		return "", err
	}
	go m.runCheck(job, dir, cmd)
	return job.id, nil
}

// runCheck runs one check job to its end, bound to the service rather than
// to the request that started it.
func (m *Manager) runCheck(job *boardJob, dir, cmd string) {
	ctx, cancel := m.bound(context.Background())
	defer cancel()
	res, err := m.accept.run(ctx, dir, cmd)
	switch {
	case err == nil, errors.Is(err, errAcceptanceNotRun):
		// A shell that did not start is a result too: exit -1, and why.
		m.endJob(job, jobDone, "", &res)
	default:
		m.endJob(job, jobFailed, clipRunes(displaytext.Sanitize(err.Error()), maxDetailRunes), nil)
	}
}

// isRev reports whether s is a hexadecimal object name, which git can never
// read as an option.
func isRev(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// baseline is the working tree state a hold's evidence is measured from:
// HEAD, "" before the first commit, every path git status reports, and the
// blob name of each one's content.
func baseline(ctx context.Context, dir string) (board.Baseline, error) {
	base := board.Baseline{Dirty: []string{}}
	repo, err := openEvidenceRepo(ctx, dir)
	if err != nil {
		return base, err
	}
	if base.Head, err = repo.head(ctx); err != nil {
		return base, err
	}
	entries, _, err := repo.status(ctx)
	if err != nil {
		return base, err
	}
	for _, e := range entries {
		base.Dirty = append(base.Dirty, e.path)
	}
	base.Blobs = repo.blobNames(ctx, base.Dirty)
	return base, nil
}

// blobNames maps each path to the blob name of its content in the working
// tree, or "" when it does not exist, hashing with one `git hash-object`
// that writes nothing. A path that is not a regular file, that
// --stdin-paths cannot carry, or that git could not read is left out, so
// it counts as changed.
func (r *gitRepo) blobNames(ctx context.Context, paths []string) map[string]string {
	out := map[string]string{}
	root, err := os.OpenRoot(r.top)
	if err != nil {
		return out
	}
	defer func() { _ = root.Close() }()
	var files []string
	for _, p := range paths {
		info, err := root.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			out[p] = ""
		case err == nil && info.Mode().IsRegular() && !strings.ContainsAny(p, "\r\n") && !strings.HasPrefix(p, `"`):
			files = append(files, p)
		}
	}
	if len(files) == 0 {
		return out
	}
	stdin := strings.NewReader(strings.Join(files, "\n") + "\n")
	res, code, _, err := runGitInput(ctx, r.git, r.top, stdin, 65*len(files)+1, "hash-object", "--no-filters", "--stdin-paths")
	names := strings.Fields(string(res))
	if err != nil || code != 0 || len(names) != len(files) {
		return out
	}
	for i, p := range files {
		out[p] = names[i]
	}
	return out
}

// hasCommit reports whether rev names a commit in the repository.
func (r *gitRepo) hasCommit(ctx context.Context, rev string) (bool, error) {
	_, code, _, err := runGit(ctx, r.git, r.top, 4096, "cat-file", "-e", rev+"^{commit}")
	return err == nil && code == 0, err
}

// openEvidenceRepo is openRepo, with a directory outside git refused.
func openEvidenceRepo(ctx context.Context, dir string) (*gitRepo, error) {
	repo, reason, err := openRepo(ctx, dir)
	if err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, newError(http.StatusConflict, "%s", reason)
	}
	return repo, nil
}

// head is the repository's HEAD commit, "" before the first commit.
func (r *gitRepo) head(ctx context.Context) (string, error) {
	if !r.hasHead {
		return "", nil
	}
	out, code, stderr, err := runGit(ctx, r.git, r.top, 4096, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", newError(http.StatusBadGateway, "git rev-parse failed: %s", gitMessage(stderr))
	}
	return strings.TrimSpace(string(out)), nil
}

// heldFiles is another live hold in the same Project: its subtask's #seq,
// its Task, and the files that Task's edit tools touched.
type heldFiles struct {
	Card   int64
	TaskID string
	Files  []string
}

// touchedFiles lists, in first-touch order, the files a Task's edit tools
// touched: create, edit and apply_patch, never view, and not calls that
// failed. Paths are as the tools gave them, relative to the Task's
// directory or absolute.
func touchedFiles(items []agentapi.Item) []string {
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		tool := it.Tool
		if tool == nil || tool.Name == "view" || tool.Status == agentapi.ToolFailed {
			continue
		}
		for _, p := range localToolFilePaths(tool) {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// collectEvidence records what changed in the repository at dir since
// base: the working tree compared with the baseline HEAD, the untracked
// files, and the commits since, at most maxEvidenceCommits. A path already
// dirty at the baseline is left out unless its content changed since.
// touched are the claiming Task's edited files and otherHolds the Project's
// other live holds; their paths may be relative to dir or absolute.
//
// When the baseline commit no longer exists, the evidence is only the
// touched files compared with HEAD, with no commits, and baselineMissing is
// set.
func collectEvidence(ctx context.Context, dir string, base board.Baseline, touched []string, otherHolds []heldFiles) (Evidence, error) {
	ev := Evidence{
		Baseline: board.Baseline{Head: base.Head, Dirty: append([]string{}, base.Dirty[:min(len(base.Dirty), maxChangedFiles)]...)},
		Diff:     EvidenceDiff{Files: []EvidenceFile{}},
		Commits:  []EvidenceCommit{},
	}
	if base.Head != "" && !isRev(base.Head) {
		return ev, newError(http.StatusBadRequest, "the baseline HEAD is not a commit name")
	}
	repo, err := openEvidenceRepo(ctx, dir)
	if err != nil {
		return ev, err
	}
	from := base.Head
	if from != "" {
		ok, err := repo.hasCommit(ctx, from)
		if err != nil {
			return ev, err
		}
		if !ok {
			ev.baselineMissing, from = true, ""
			if repo.hasHead {
				from = "HEAD"
			}
		}
	}
	if from == "" {
		if from, err = repo.emptyTree(ctx); err != nil {
			return ev, err
		}
	}
	tracked, err := repo.numstatSince(ctx, from)
	if err != nil {
		return ev, err
	}
	entries, _, err := repo.status(ctx)
	if err != nil {
		return ev, err
	}
	root, err := os.OpenRoot(repo.top)
	if err != nil {
		return ev, newError(http.StatusBadGateway, "could not open working tree: %s", shortError(err))
	}
	defer func() { _ = root.Close() }()
	resolve := repoPaths(repo.top, dir)
	mine := map[string]bool{}
	for _, p := range touched {
		if rel, ok := resolve(p); ok {
			mine[rel] = true
		}
	}
	others := map[string]*EvidenceOverlap{}
	holds := slices.SortedFunc(slices.Values(otherHolds), func(a, b heldFiles) int { return cmp.Compare(a.Card, b.Card) })
	for _, h := range holds {
		for _, p := range h.Files {
			if rel, ok := resolve(p); ok && others[rel] == nil {
				others[rel] = &EvidenceOverlap{Card: h.Card, TaskID: h.TaskID}
			}
		}
	}

	// A pre-dirty path is left out only when its content provably has not
	// changed. Paths that are not UTF-8 do not survive the store's JSON, so
	// they never match a pre-dirty path and count as changed.
	was, now := base.Blobs, repo.blobNames(ctx, base.Dirty)
	preDirty := make(map[string]bool, len(base.Dirty))
	for _, p := range base.Dirty {
		preDirty[p] = true
	}
	var files []EvidenceFile
	seen := map[string]bool{}
	admit := func(p string) (EvidenceFile, bool) {
		if seen[p] || (ev.baselineMissing && !mine[p]) {
			return EvidenceFile{}, false
		}
		seen[p] = true
		f := EvidenceFile{Path: p, ByTask: mine[p], Overlap: others[p]}
		if preDirty[p] {
			before, recorded := was[p]
			after, hashed := now[p]
			if recorded && hashed && before == after {
				return f, false
			}
			f.PreDirty = true
		}
		return f, true
	}
	for _, t := range tracked {
		if f, ok := admit(t.Path); ok {
			f.Added, f.Deleted = t.Added, t.Deleted
			files = append(files, f)
		}
	}
	budget := untrackedBudget
	for _, e := range entries {
		if !e.untracked {
			continue
		}
		// A path removed from the index is listed by the diff already.
		if f, ok := admit(e.path); ok {
			f.Added, budget = countLines(root, e.path, budget)
			files = append(files, f)
		}
	}
	// A pre-dirty path whose content changed back to HEAD's is in neither
	// list, but the Task changed it.
	for _, p := range base.Dirty {
		before, recorded := was[p]
		after, hashed := now[p]
		if recorded && hashed && before != after {
			if f, ok := admit(p); ok {
				files = append(files, f)
			}
		}
	}
	for _, f := range files {
		ev.Diff.Added += f.Added
		ev.Diff.Deleted += f.Deleted
		ev.testsOrBuild = ev.testsOrBuild || testOrBuildPath(f.Path)
		ev.overlap = ev.overlap || f.Overlap != nil
		if len(ev.Diff.Files) < maxChangedFiles {
			ev.Diff.Files = append(ev.Diff.Files, f)
		}
	}
	if !ev.baselineMissing {
		ev.Commits, err = repo.commitsSince(ctx, base.Head, maxEvidenceCommits)
	}
	return ev, err
}

// emptyTree is the name of the empty tree in the repository's hash.
func (r *gitRepo) emptyTree(ctx context.Context) (string, error) {
	out, code, stderr, err := runGit(ctx, r.git, r.top, 4096, "hash-object", "-t", "tree", "/dev/null")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", newError(http.StatusBadGateway, "git hash-object failed: %s", gitMessage(stderr))
	}
	return strings.TrimSpace(string(out)), nil
}

// numstatSince lists the tracked files that differ between from and the
// working tree, with their line counts; a binary file counts no lines.
func (r *gitRepo) numstatSince(ctx context.Context, from string) ([]EvidenceFile, error) {
	out, code, stderr, err := runGit(ctx, r.git, r.top, maxStatusBytes, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--numstat", "-z", from, "--")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, newError(http.StatusBadGateway, "git diff failed: %s", gitMessage(stderr))
	}
	var files []EvidenceFile
	for _, rec := range nulRecords(out) {
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 || parts[2] == "" {
			continue
		}
		added, _ := strconv.Atoi(parts[0]) // "-" for a binary file
		deleted, _ := strconv.Atoi(parts[1])
		files = append(files, EvidenceFile{Path: parts[2], Added: added, Deleted: deleted})
	}
	return files, nil
}

// nulRecords splits NUL-terminated records, dropping a last record that
// output capping cut short.
func nulRecords(out []byte) []string {
	recs := strings.Split(string(out), "\x00")
	return recs[:len(recs)-1]
}

// commitsSince lists the commits on HEAD since from, every commit when from
// is "", newest first.
func (r *gitRepo) commitsSince(ctx context.Context, from string, limit int) ([]EvidenceCommit, error) {
	commits := []EvidenceCommit{}
	if !r.hasHead {
		return commits, nil
	}
	rev := "HEAD"
	if from != "" {
		rev = from + "..HEAD"
	}
	out, code, stderr, err := runGit(ctx, r.git, r.top, maxStatusBytes, "-c", "log.showSignature=false",
		"log", "-z", "--no-color", "--format=%H%x1f%s", "-n", strconv.Itoa(limit), rev, "--")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, newError(http.StatusBadGateway, "git log failed: %s", gitMessage(stderr))
	}
	for _, rec := range strings.Split(string(out), "\x00") {
		sha, subject, ok := strings.Cut(strings.TrimSpace(rec), "\x1f")
		if ok {
			commits = append(commits, EvidenceCommit{SHA: sha, Subject: clipRunes(displaytext.Sanitize(subject), maxDetailRunes)})
		}
	}
	return commits, nil
}

// repoPaths returns a function mapping a tool's file path, relative to dir
// or absolute, to its slash-separated path in the work tree at top. dir may
// reach the work tree through a symbolic link, as a Task's tools see it.
func repoPaths(top, dir string) func(string) (string, bool) {
	dir = filepath.Clean(dir)
	prefix := ""
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		if rel, err := filepath.Rel(top, real); err == nil && filepath.IsLocal(rel) {
			prefix = rel
		}
	}
	return func(p string) (string, bool) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		for _, root := range []struct{ dir, prefix string }{{dir, prefix}, {top, ""}} {
			if rel, err := filepath.Rel(root.dir, p); err == nil && filepath.IsLocal(rel) && rel != "." {
				return filepath.ToSlash(filepath.Join(root.prefix, rel)), true
			}
		}
		return "", false
	}
}

// buildFiles are the build manifests and lockfiles whose change tags a
// diff "tests or build files changed".
var buildFiles = map[string]bool{
	"Makefile": true, "GNUmakefile": true, "makefile": true,
	"go.mod": true, "go.sum": true, "go.work": true, "go.work.sum": true,
	"package.json": true, "package-lock.json": true, "npm-shrinkwrap.json": true,
	"yarn.lock": true, "pnpm-lock.yaml": true, "bun.lock": true, "bun.lockb": true,
	"Cargo.lock": true, "Gemfile.lock": true, "poetry.lock": true, "uv.lock": true,
	"Pipfile.lock": true, "composer.lock": true,
}

// testOrBuildPath reports whether a work tree path is a test file (*_test.*,
// *.test.*, or under a test, tests or __tests__ directory), a build file,
// or CI configuration under .github/.
func testOrBuildPath(p string) bool {
	if strings.HasPrefix(p, ".github/") {
		return true
	}
	dir, base := path.Split(p)
	if strings.Contains(base, "_test.") || strings.Contains(base, ".test.") || buildFiles[base] {
		return true
	}
	for _, seg := range strings.Split(dir, "/") {
		switch seg {
		case "test", "tests", "__tests__":
			return true
		}
	}
	return false
}

// acceptRunner runs acceptance commands; *acceptRunners is the real one.
type acceptRunner interface {
	run(ctx context.Context, dir, cmd string) (AcceptResult, error)
}

// acceptRunners runs acceptance commands one at a time per Project
// directory. The zero value is ready to use.
type acceptRunners struct {
	// timeout bounds a run, and a caller's wait for its directory's runner;
	// zero means acceptTimeout.
	timeout time.Duration
	mu      sync.Mutex
	slots   map[string]chan struct{}
}

func (r *acceptRunners) slot(dir string) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.slots == nil {
		r.slots = map[string]chan struct{}{}
	}
	s := r.slots[dir]
	if s == nil {
		s = make(chan struct{}, 1)
		r.slots[dir] = s
	}
	return s
}

// run runs the acceptance command cmd as `$SHELL -lc cmd` in dir, in its
// own process group, and returns its exit code and the last
// acceptTailBytes of its output. The group is killed when the command
// exits, when the timeout passes, and when ctx ends. The caller refuses a
// non-zero exit.
//
// run waits for dir's earlier run; past the timeout it refuses with
// acceptance_busy. When ctx ends the run is discarded and ctx's cause is
// returned. A shell that does not start returns an error wrapping
// errAcceptanceNotRun, with the result saying why.
func (r *acceptRunners) run(ctx context.Context, dir, cmd string) (AcceptResult, error) {
	if cmd == "" {
		return AcceptResult{}, &board.Error{Code: board.CodeInvalid, Message: "no acceptance command is set"}
	}
	timeout := cmp.Or(r.timeout, acceptTimeout)
	if ctx.Err() != nil {
		return AcceptResult{}, context.Cause(ctx)
	}
	slot := r.slot(filepath.Clean(dir))
	wait := time.NewTimer(timeout)
	select {
	case slot <- struct{}{}:
		wait.Stop()
	case <-wait.C:
		return AcceptResult{}, errAcceptanceBusy
	case <-ctx.Done():
		wait.Stop()
		return AcceptResult{}, context.Cause(ctx)
	}
	defer func() { <-slot }()

	res := AcceptResult{Cmd: cmd, CmdHash: commandHash(cmd), Exit: -1, RanAt: time.Now().UTC()}
	if base, err := baseline(ctx, dir); err == nil {
		res.Head, res.Dirty = base.Head, len(base.Dirty) > 0
	}
	notRun := func(why string, err error) (AcceptResult, error) {
		res.Tail = why + ": " + shortError(err)
		return res, fmt.Errorf("%w: %s", errAcceptanceNotRun, res.Tail)
	}
	shell, err := terminalShell()
	if err != nil {
		return notRun("no shell found", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(runCtx, shell, "-lc", cmd) // #nosec G204 G702 -- the owner-authored acceptance command (ADR 0005 §6), which no agent can write, in the service user's shell.
	c.Dir = dir
	c.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, readyEnv+"=") })
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = acceptWaitDelay
	tail := &tailBuffer{limit: acceptTailBytes}
	c.Stdout, c.Stderr = tail, tail
	if err := c.Start(); err != nil {
		if ctx.Err() != nil {
			return AcceptResult{}, context.Cause(ctx)
		}
		return notRun("the shell did not start", err)
	}
	_ = c.Wait() // the exit code is read from the process state
	// Whatever the command left running in its group ends with it; a group
	// ID is not reused while any member remains.
	_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	if ctx.Err() != nil {
		return AcceptResult{}, context.Cause(ctx)
	}
	res.Exit = exitCode(c.ProcessState)
	if res.Exit != 0 && runCtx.Err() != nil {
		_, _ = fmt.Fprintf(tail, "\n[uam] the command did not finish within %s, so its process group was killed.\n", timeout)
	}
	res.Tail = tail.String()
	return res, nil
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	buf   []byte
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= t.limit {
		t.buf = append(t.buf[:0], p[n-t.limit:]...)
		return n, nil
	}
	if over := len(t.buf) + n - t.limit; over > 0 {
		t.buf = t.buf[:copy(t.buf, t.buf[over:])]
	}
	t.buf = append(t.buf, p...)
	return n, nil
}

// String is the tail as valid UTF-8: a character cut at the front, and any
// other invalid byte, is dropped.
func (t *tailBuffer) String() string { return strings.ToValidUTF8(string(t.buf), "") }

// claimStore is the store API a claim is evaluated through; *board.Store
// satisfies it.
type claimStore interface {
	CheckFinishable(ctx context.Context, a board.Actor, ref string) (board.Finishable, error)
	Detail(ctx context.Context, ref string) (board.Detail, error)
}

// claimInput is one agent's done claim on a subtask.
type claimInput struct {
	Actor board.Actor
	Ref   string
	// Dir is the Project directory.
	Dir string
	// Comment is the claim text. ProposedAcceptCmd is a command the owner
	// may copy into the subtask; it never runs before that.
	Comment           string
	ProposedAcceptCmd string
	// Touched are the files the claiming Task's edit tools touched, as
	// touchedFiles lists them.
	Touched []string
	// OtherHolds lists the live holds in card's Project other than the
	// claiming Task's; nil means none. It is called before the run.
	OtherHolds func(ctx context.Context, card board.Card) ([]heldFiles, error)
	Transcript *EvidenceTranscript
}

// claimResult is an evaluated claim: the subtask, its evidence, and the
// done request to file with FileRequest.
type claimResult struct {
	Card     board.Card
	Evidence Evidence
	Request  board.RequestInput
}

// evaluateClaim evaluates a done claim in ADR 0005 §6's order: the store's
// finishing guard and the resolved acceptance command, then the evidence
// since the hold started, then the command's run when it is non-empty. It
// writes nothing: the caller files Request, and FileRequest repeats the
// guard.
//
// A command that exits non-zero, 127 included, refuses the claim with
// acceptance_failed; the result still carries the run. A runner busy past
// the timeout refuses with acceptance_busy. When ctx ends the run is
// discarded and ctx's cause returned. Only a shell that does not start
// files the claim, flagged acceptance_could_not_run. A command that exits 0
// is the Request's PassedCmd, so the store accepts the request as it files it.
func evaluateClaim(ctx context.Context, st claimStore, runner acceptRunner, in claimInput) (claimResult, error) {
	fin, err := st.CheckFinishable(ctx, in.Actor, in.Ref)
	if err != nil {
		return claimResult{}, err
	}
	out := claimResult{Card: fin.Card}
	cmd := fin.AcceptCmd
	detail, err := st.Detail(ctx, fin.Card.ID)
	if err != nil {
		return out, err
	}
	i := slices.IndexFunc(detail.Holds, func(h board.Hold) bool { return h.EndedAt == nil && h.TaskID == in.Actor.TaskID })
	if i < 0 {
		return out, &board.Error{Code: board.CodeNotHeld, Message: fmt.Sprintf("#%d is not held by this Task", fin.Card.Seq)}
	}
	base := detail.Holds[i].Baseline

	// The evidence is the Task's work, gathered before the run so files the
	// command writes (build output, lockfiles) are not counted as the Task's.
	var others []heldFiles
	if in.OtherHolds != nil {
		if others, err = in.OtherHolds(ctx, fin.Card); err != nil {
			return out, err
		}
	}
	ev, err := collectEvidence(ctx, in.Dir, base, in.Touched, others)
	if err != nil {
		return out, err
	}
	ev.Transcript = in.Transcript
	for _, c := range fin.Card.Checklist {
		ev.Checklist.Total++
		if c.Done {
			ev.Checklist.Done++
		}
	}
	out.Evidence = ev

	notRun, passed := false, ""
	if cmd != "" {
		res, err := runner.run(ctx, in.Dir, cmd)
		switch {
		case errors.Is(err, errAcceptanceNotRun):
			notRun = true
		case err != nil:
			return out, err
		}
		ev.Accept = &res
		out.Evidence = ev
		if !notRun && res.Exit != 0 {
			return out, &board.Error{Code: board.CodeAcceptanceFailed, Message: fmt.Sprintf(
				"the acceptance command exited %d, so the claim is refused. If this subtask is red by design, "+
					"such as one that adds a failing test, fold the red and green steps into one subtask, or ask "+
					"the owner to set this subtask's command to ''.\n\nOutput tail:\n%s", res.Exit, res.Tail)}
		}
		if !notRun {
			passed = cmd
		}
	}
	data, err := marshalEvidence(ev)
	if err != nil {
		return out, err
	}
	// Only the owner's command that ran green accepts the request as it is
	// filed (ADR 0005 decision 5); the proposed one is text for the owner.
	out.Request = board.RequestInput{Kind: board.RequestDone, Comment: in.Comment, Evidence: data,
		Flags: evidenceFlags(ev, cmd, notRun), ProposedAcceptCmd: in.ProposedAcceptCmd, PassedCmd: passed}
	return out, nil
}

// evidenceFlags are the flags of a done request with evidence ev, whose
// acceptance command is cmd; notRun is set when its shell did not start.
// Without the baseline commit, whether nothing changed cannot be told.
func evidenceFlags(ev Evidence, cmd string, notRun bool) []string {
	var flags []string
	if notRun {
		flags = append(flags, board.FlagAcceptanceCouldNotRun)
	}
	if ev.baselineMissing {
		flags = append(flags, board.FlagBaselineMissing)
	} else if cmd == "" && len(ev.Diff.Files) == 0 && len(ev.Commits) == 0 {
		flags = append(flags, board.FlagNoChangeInTree)
	}
	if ev.testsOrBuild {
		flags = append(flags, board.FlagTestsOrBuildChanged)
	}
	if ev.overlap {
		flags = append(flags, board.FlagOverlap)
	}
	return flags
}

// marshalEvidence encodes ev within the store's limit, halving the file
// list and the baseline's dirty paths, then the output tail, until it fits.
// The diff totals still count every file.
func marshalEvidence(ev Evidence) (json.RawMessage, error) {
	for {
		data, err := json.Marshal(ev)
		if err != nil || len(data) <= maxEvidenceJSON {
			return data, err
		}
		switch {
		case len(ev.Diff.Files) > 0 || len(ev.Baseline.Dirty) > 0:
			ev.Diff.Files = ev.Diff.Files[:len(ev.Diff.Files)/2]
			ev.Baseline.Dirty = ev.Baseline.Dirty[:len(ev.Baseline.Dirty)/2]
		case ev.Accept != nil && ev.Accept.Tail != "":
			accept := *ev.Accept
			accept.Tail = strings.ToValidUTF8(accept.Tail[len(accept.Tail)/2:], "")
			ev.Accept = &accept
		default:
			return nil, fmt.Errorf("evidence exceeds %d bytes", maxEvidenceJSON)
		}
	}
}
