package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Git actions: the owner sets up a repository or reverts a Thread's change
// from the web; commits, pushes and pulls happen in the conversation. Every
// call is the git CLI with structured arguments, literal pathspecs after --,
// no terminal prompts and a time limit. Write actions are refused while a
// Task in the same repository is mid-turn, starting, or has subagents or
// background jobs running, and no turn starts there while one runs.
const (
	// gitWriteTimeout bounds one git write action (hooks run).
	gitWriteTimeout = 2 * time.Minute
	// maxGitOutput is the tail of a write action's output kept to show.
	maxGitOutput = 8 << 10

	codeGitBusy = "git_busy"
)

// GitFile is a changed file of the repository. Mine is set when this Task's
// edit tools touched it; OtherTask when another Task's did and this one's
// did not. Both come from the Tasks' edit records (task_changes.go).
type GitFile struct {
	ChangedFile
	Mine      bool `json:"mine,omitempty"`
	OtherTask bool `json:"other_task,omitempty"`
}

// GitState is what the commit panel shows for a Task's repository.
type GitState struct {
	// Repo is false when the Task's directory is in no repository; Reason
	// then says why, and CanInit whether "Set up git here" may run.
	Repo    bool   `json:"repo"`
	Reason  string `json:"reason,omitempty"`
	CanInit bool   `json:"can_init,omitempty"`
	// Branch is empty when HEAD is detached. Upstream is the branch's
	// upstream, such as origin/main; Ahead and Behind count commits against
	// it as last fetched. Remote is whether Push has somewhere to go.
	Branch   string `json:"branch,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Remote   bool   `json:"remote"`
	// HasCommits is false before the first commit.
	HasCommits bool      `json:"has_commits"`
	Files      []GitFile `json:"files"`
	// TaskFilesKnown is false when this Task's edits from before its history
	// was read may be missing (after a restart), so Mine may be incomplete.
	TaskFilesKnown bool `json:"task_files_known"`
	// Busy says why write actions are refused now; empty when they may run.
	Busy string `json:"busy,omitempty"`
}

// gitTarget is a Task's directory and Project for a git action.
type gitTarget struct {
	session    *webSession
	workdir    string
	projectID  string
	projectDir string
}

func (m *Manager) gitTarget(id string) (gitTarget, error) {
	s, err := m.lookup(id)
	if err != nil {
		return gitTarget{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := gitTarget{session: s, workdir: s.workdir, projectID: s.projectID, projectDir: s.workdir}
	if p := m.projects[s.projectID]; p != nil {
		t.projectDir = p.Dir
	}
	return t, nil
}

// GitState reads the Task's repository: its branch, its changed files with
// whose they are, and whether write actions may run now.
func (m *Manager) GitState(ctx context.Context, id string) (GitState, error) {
	t, err := m.gitTarget(id)
	if err != nil {
		return GitState{}, err
	}
	out := GitState{Files: []GitFile{}}
	repo, reason, err := openRepo(ctx, t.workdir)
	if err != nil {
		return out, err
	}
	if repo == nil {
		out.Reason = reason
		_, noGit := readBranch(ctx, t.projectDir)
		out.CanInit = noGit == noGitRepository
		out.Busy = m.repoBusy(t.projectDir)
		return out, nil
	}
	out.Repo, out.HasCommits = true, repo.hasHead
	if err := repo.branchState(ctx, &out); err != nil {
		return out, err
	}
	changes, err := repo.listing(ctx)
	if err != nil {
		return out, err
	}
	mine, others, known := m.taskFiles(t.session, repo.top)
	out.TaskFilesKnown = known
	for _, f := range changes.Files {
		out.Files = append(out.Files, GitFile{ChangedFile: f, Mine: mine[f.Path], OtherTask: !mine[f.Path] && others[f.Path]})
	}
	out.Busy = m.repoBusy(repo.top)
	return out, nil
}

// branchState fills the branch, its upstream, how far apart they are, and
// whether there is a remote to push to.
func (r *gitRepo) branchState(ctx context.Context, out *GitState) error {
	// The headers come first; the tracked changes after them are not read.
	status, code, stderr, err := runGit(ctx, r.git, r.top, 64<<10, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=no")
	if err != nil {
		return err
	}
	if code != 0 {
		return newError(http.StatusBadGateway, "git status failed: %s", gitMessage(stderr))
	}
	for _, rec := range strings.Split(string(status), "\x00") {
		header, ok := strings.CutPrefix(rec, "# branch.")
		if !ok {
			continue
		}
		key, value, _ := strings.Cut(header, " ")
		switch key {
		case "head":
			if value != "(detached)" {
				out.Branch = clipRunes(displaytext.Sanitize(value), maxNameRunes)
			}
		case "upstream":
			out.Upstream = clipRunes(displaytext.Sanitize(value), maxNameRunes)
		case "ab":
			for f := range strings.FieldsSeq(value) {
				n, _ := strconv.Atoi(f[1:])
				if f[0] == '+' {
					out.Ahead = n
				} else {
					out.Behind = n
				}
			}
		}
	}
	remotes, code, _, err := runGit(ctx, r.git, r.top, 64<<10, "remote")
	if err != nil {
		return err
	}
	out.Remote = out.Upstream != "" || code == 0 && slices.Contains(strings.Fields(string(remotes)), "origin")
	return nil
}

// taskFiles returns the repository paths the Task s's edit tools touched,
// those other Tasks' touched, and whether s's edits are known in full. They
// come from the Tasks' edit records, which outlive trimmed and evicted
// transcripts; archived Tasks do not count.
func (m *Manager) taskFiles(s *webSession, top string) (mine, others map[string]bool, known bool) {
	type work struct {
		dir   string
		paths []string
	}
	var own work
	var rest []work
	m.mu.Lock()
	known = s.editsKnown
	for _, o := range m.sessions {
		if o.removed || len(o.edits) == 0 || o != s && o.stage == StageArchived {
			continue
		}
		w := work{o.workdir, slices.Collect(maps.Keys(o.edits))}
		if o == s {
			own = w
		} else {
			rest = append(rest, w)
		}
	}
	m.mu.Unlock()
	resolve := func(dir string, paths []string, into map[string]bool) {
		r := repoPaths(top, dir)
		for _, p := range paths {
			if rel, ok := r(p); ok {
				into[rel] = true
			}
		}
	}
	mine, others = map[string]bool{}, map[string]bool{}
	resolve(own.dir, own.paths, mine)
	for _, w := range rest {
		resolve(w.dir, w.paths, others)
	}
	return mine, others, known
}

// tasksIn returns the Tasks, not removed, for which pick holds and whose
// directory is in the repository or folder top. pick runs under mu; paths are
// resolved outside it.
func (m *Manager) tasksIn(top string, pick func(*webSession) bool) []*webSession {
	type task struct {
		s   *webSession
		dir string
	}
	var picked []task
	m.mu.Lock()
	for _, s := range m.sessions {
		if !s.removed && pick(s) {
			picked = append(picked, task{s, s.workdir})
		}
	}
	m.mu.Unlock()
	top = realPath(top)
	var in []*webSession
	for _, t := range picked {
		if inDir(top, realPath(t.dir)) {
			in = append(in, t.s)
		}
	}
	return in
}

// repoBusy says why write actions in the repository or folder top must
// wait: a Task whose directory is in it could still be writing there, by the
// rules that keep a Task from being settled (settleableLocked): mid-turn,
// starting, prompts queued, subagents or background jobs running. It returns
// "" when none is.
func (m *Manager) repoBusy(top string) string {
	busy := m.tasksIn(top, func(s *webSession) bool { return s.settleableLocked() != nil })
	names := make([]string, 0, len(busy))
	m.mu.Lock()
	for _, s := range busy {
		names = append(names, clipRunes(displaytext.Sanitize(firstNonEmpty(s.name, s.title, "A thread")), maxNameRunes))
	}
	m.mu.Unlock()
	slices.Sort(names)
	switch len(names) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("“%s” is still working in this repository. Wait for it to finish.", names[0])
	case 2:
		return fmt.Sprintf("“%s” and 1 other thread are still working in this repository. Wait for them to finish.", names[0])
	default:
		return fmt.Sprintf("“%s” and %d other threads are still working in this repository. Wait for them to finish.", names[0], len(names)-1)
	}
}

// realPath is path with its symbolic links resolved, or path itself when
// they cannot be.
func realPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// inDir reports whether dir is top or inside it.
func inDir(top, dir string) bool {
	rel, err := filepath.Rel(top, dir)
	return err == nil && filepath.IsLocal(rel)
}

// gitWriteLocked is the end of the git write action running in the
// repository that holds the real path dir, nil when none runs there.
func (m *Manager) gitWriteLocked(dir string) chan struct{} {
	for top, done := range m.gitWrites {
		if inDir(top, dir) {
			return done
		}
	}
	return nil
}

// beginWrite refuses a write action in top while a Task there could still
// be writing (repoBusy) or another write action runs there; otherwise it
// returns the function that ends the action. From the start until then, a
// turn starting in top waits (Manager.send), so none starts mid-write.
func (m *Manager) beginWrite(top string) (func(), error) {
	top = realPath(top)
	m.mu.Lock()
	for other := range m.gitWrites {
		if inDir(other, top) || inDir(top, other) {
			m.mu.Unlock()
			return nil, &Error{Status: http.StatusConflict, Code: codeGitBusy, Message: "Another git action is running in this repository. Try again when it finishes."}
		}
	}
	if m.gitWrites == nil {
		m.gitWrites = map[string]chan struct{}{}
	}
	done := make(chan struct{})
	m.gitWrites[top] = done
	m.mu.Unlock()
	end := func() {
		m.mu.Lock()
		delete(m.gitWrites, top)
		m.mu.Unlock()
		close(done)
	}
	// Checked once the action holds the repository: a turn that started
	// before shows here, and one starting from now on waits for end.
	if busy := m.repoBusy(top); busy != "" {
		end()
		return nil, &Error{Status: http.StatusConflict, Code: codeGitBusy, Message: busy}
	}
	return end, nil
}

// openWriteRepo opens the Task's repository for a write action.
func (m *Manager) openWriteRepo(ctx context.Context, id string) (gitTarget, *gitRepo, func(), error) {
	t, err := m.gitTarget(id)
	if err != nil {
		return t, nil, nil, err
	}
	repo, reason, err := openRepo(ctx, t.workdir)
	if err != nil {
		return t, nil, nil, err
	}
	if repo == nil {
		return t, nil, nil, newError(http.StatusConflict, "%s", reason)
	}
	unlock, err := m.beginWrite(repo.top)
	if err != nil {
		return t, nil, nil, err
	}
	return t, repo, unlock, nil
}

// GitInit sets up a repository in the Task's Project directory, only when
// git says that directory is in none, and re-reads the Project's git state.
func (m *Manager) GitInit(ctx context.Context, id string) (GitState, error) {
	t, err := m.gitTarget(id)
	if err != nil {
		return GitState{}, err
	}
	switch _, noGit := readBranch(ctx, t.projectDir); noGit {
	case noGitRepository:
	case noGitInstalled:
		return GitState{}, newError(http.StatusConflict, "git is not installed in a standard location on the server")
	default:
		return GitState{}, newError(http.StatusConflict, "this folder is already in a Git repository")
	}
	unlock, err := m.beginWrite(t.projectDir)
	if err != nil {
		return GitState{}, err
	}
	defer unlock()
	if _, err := runGitWrite(ctx, t.projectDir, nil, "init"); err != nil {
		return GitState{}, gitFailed("git init failed", err)
	}
	log.Info("git repository initialised from the web", "session", id, "project", t.projectID)
	m.refreshBranches(ctx, true, t.projectID)
	return m.GitState(ctx, id)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// gitError is a write action git refused: its exit code and the tail of its
// output.
type gitError struct {
	code   int
	output string
}

func (e *gitError) Error() string { return fmt.Sprintf("git exited %d", e.code) }

// gitFailed is a refusal saying what, then git's output when there is any.
func gitFailed(what string, err error) *Error {
	var gerr *gitError
	if errors.As(err, &gerr) {
		if out := strings.TrimSpace(gerr.output); out != "" {
			return newError(http.StatusConflict, "%s:\n%s", what, out)
		}
		return newError(http.StatusConflict, "%s (git exited %d).", what, gerr.code)
	}
	var werr *Error
	if errors.As(err, &werr) {
		return werr
	}
	return newError(http.StatusBadGateway, "%s: %s", what, shortError(err))
}

// runGitWrite runs one git write action in dir with structured arguments
// and gitWriteTimeout. Unlike runGit it runs the repository's hooks, so a
// commit hook can refuse a commit. Pathspecs are literal and git never asks
// on a terminal. It returns the tail of git's combined output, made safe to
// show; a non-zero exit is a *gitError.
func runGitWrite(ctx context.Context, dir string, stdin io.Reader, args ...string) (string, error) {
	return runGitWriteEnv(ctx, dir, stdin, nil, args...)
}

// runGitWriteEnv is runGitWrite with env added to git's environment.
func runGitWriteEnv(ctx context.Context, dir string, stdin io.Reader, env []string, args ...string) (string, error) {
	git, err := lookGit()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, gitWriteTimeout)
	defer cancel()
	argv := append([]string{"-C", dir, "-c", "core.fsmonitor=false", literalPathspecs}, args...)
	cmd := exec.CommandContext(ctx, git, argv...) // #nosec G204 G702 -- resolved git binary, fixed commands, user paths after --, no shell.
	cmd.Stdin = stdin
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true",
		"LC_ALL=C", "GIT_PAGER=cat", "GIT_LITERAL_PATHSPECS=1", "SSH_ASKPASS_REQUIRE=never"), env...)
	tail := &tailBuffer{limit: maxGitOutput}
	cmd.Stdout, cmd.Stderr = tail, tail
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	out := strings.TrimSpace(displaytext.SanitizeText(tail.String()))
	if ctx.Err() != nil {
		return out, newError(http.StatusGatewayTimeout, "git did not finish within %s", gitWriteTimeout)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out, &gitError{code: exitErr.ExitCode(), output: out}
	}
	if err != nil {
		return out, newError(http.StatusBadGateway, "git failed: %s", shortError(err))
	}
	return out, nil
}

var (
	fenceRE = regexp.MustCompile("(?s)^```[a-zA-Z]*\\n(.*?)\\n?```$")
	// trailerRE matches a trailer; creditRE a line crediting an AI or a tool.
	trailerRE = regexp.MustCompile(`(?i)^\s*(co-authored-by|signed-off-by|assisted-by|generated-by|helped-by|on-behalf-of|ai-assisted|made-with)\s*:`)
	creditRE  = regexp.MustCompile(`(?i)(generated|written|created|authored|drafted|made)\s+(with|by|using)\b.*\b(ai|copilot|claude|chatgpt|gpt|gemini|llm|assistant|model)\b`)
)

// unwrapDraft is a drafted reply without reasoning blocks or an enclosing
// code fence.
func unwrapDraft(reply string) string {
	reply = strings.TrimSpace(thinkRE.ReplaceAllString(strings.ReplaceAll(reply, "\r\n", "\n"), ""))
	if m := fenceRE.FindStringSubmatch(reply); m != nil {
		reply = strings.TrimSpace(m[1])
	}
	return reply
}

// draftLines are a drafted text's lines made safe, without any trailer or
// AI credit line, and with no more than one blank line in a row and none at
// either end.
func draftLines(reply string) []string {
	var lines []string
	for line := range strings.Lines(reply) {
		line = strings.TrimRight(displaytext.SanitizeText(line), " \t\n")
		if trailerRE.MatchString(line) || creditRE.MatchString(line) || strings.Contains(line, "🤖") {
			continue
		}
		lines = append(lines, line)
	}
	var out []string
	for _, line := range lines {
		if line == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// lookGit is the resolved git binary.
func lookGit() (string, error) {
	git, err := execpath.Resolve("git")
	if err != nil {
		return "", newError(http.StatusConflict, "git is not installed in a standard location on the server")
	}
	return git, nil
}

// The handlers.

func (s *Server) handleGitState(w http.ResponseWriter, r *http.Request) {
	state, err := s.m.GitState(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleGitInit(w http.ResponseWriter, r *http.Request) {
	if !decodeOptionalBody(w, r, &struct{}{}) {
		return
	}
	state, err := s.m.GitInit(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
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
