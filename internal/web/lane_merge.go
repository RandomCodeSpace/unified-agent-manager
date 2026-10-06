package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// The merge of a Project's integration branch into its base branch (ADR
// 0006 §5.8). Approving an epic authorizes it, so events start it, not a
// click: a write under an approved epic that derives done, such as the
// landing or the owner's cancel that finished it, and the first pass after
// the planner opens, for unmerged work of approved epics that are done; and
// the owner's Revert of work the base branch already has. Each starts the
// merge job only while the base lacks something the integration branch
// carries. The owner's Retry merge starts it too, also to merge an
// unfinished epic's work early. A merge that waits on the owner's working
// tree or git, a lock another git process holds included, is tried again
// with backoff; one that conflicts, or fails otherwise, waits for Retry
// merge or new tips. Once the base has it all, as after the owner merged by
// hand, either is forgotten.

const (
	// jobMerge is the merge job (board_ai.go); it runs on a Project.
	jobMerge = "merge"
	// The states of a merge that did not go through, as the Planner header
	// shows them.
	mergeWaiting = "waiting"
	mergeBlocked = "blocked"
)

// maxListedMerged is how many landings and reverts a merge's comment names.
const maxListedMerged = 100

// mergeBackoff are the waits before a waiting merge is tried again; the
// last one repeats.
var mergeBackoff = []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute}

var errMergeBusy = &Error{Status: http.StatusConflict, Code: codeJobBusy, Message: "a merge of this project is running; wait for it to end"}

// BoardMerge is a Project's last merge that did not go through: waiting,
// with when uam tries it again, or blocked until Retry merge or new tips.
type BoardMerge struct {
	State   string     `json:"state"`
	Reason  string     `json:"reason"`
	RetryAt *time.Time `json:"retry_at,omitempty"`
}

// projectMerge is a Project's merge in memory: whether its job runs and a
// trigger came meanwhile, and the last merge that did not go through, with
// the integration and base tips a blocked one was tried at, how often a
// waiting one was tried, and its retry.
type projectMerge struct {
	running, again bool
	shown          BoardMerge
	integ, base    string
	tries          int
	retry          *time.Timer
}

// mergeOfLocked is the Project's merge; the caller holds m.lanes.mu.
func (m *Manager) mergeOfLocked(project string) *projectMerge {
	if m.lanes.merges == nil {
		m.lanes.merges = map[string]*projectMerge{}
	}
	pm := m.lanes.merges[project]
	if pm == nil {
		pm = &projectMerge{}
		m.lanes.merges[project] = pm
	}
	return pm
}

// settle forgets the merge that did not go through and its retry.
func (pm *projectMerge) settle() {
	if pm.retry != nil {
		pm.retry.Stop()
	}
	pm.shown, pm.integ, pm.base, pm.tries, pm.retry = BoardMerge{}, "", "", 0, nil
}

// mergeShown is the Project's merge state for the Planner header, nil when
// nothing waits or the base branch has it all.
func (m *Manager) mergeShown(ctx context.Context, project string) *BoardMerge {
	m.lanes.mu.Lock()
	var shown BoardMerge
	if pm := m.lanes.merges[project]; pm != nil {
		shown = pm.shown
	}
	m.lanes.mu.Unlock()
	if shown.State == "" {
		return nil
	}
	if has, _, _, err := m.baseHasAll(ctx, project); err == nil && has {
		return nil
	}
	return &shown
}

// mergeOnChange merges as mergeFinished does after a write whose cards,
// the changed ones and their ancestors, hold an approved epic that derives
// done: the landing, or the owner's cancel or done, that finished it. A
// merge that waits is left to its retry.
func (m *Manager) mergeOnChange(project string, cards []board.Card) {
	if project == "" || !slices.ContainsFunc(cards, func(c board.Card) bool {
		return c.Kind == board.KindEpic && c.Run != nil && c.Status == board.StatusDone
	}) {
		return
	}
	m.lanes.mu.Lock()
	pm := m.lanes.merges[project]
	waiting := pm != nil && pm.shown.State == mergeWaiting
	m.lanes.mu.Unlock()
	if !waiting {
		m.goLanes(func(ctx context.Context) { m.mergeFinished(ctx, project) })
	}
}

// mergeFinished merges the Project's integration branch when its base
// branch lacks work of an approved epic that derives done: after a write
// that finished one, and on the first pass after the planner opens, which
// finishes a merge a restart interrupted. Work of an epic still running
// waits for its epic, even beside an epic that finished and was merged long
// ago.
func (m *Manager) mergeFinished(ctx context.Context, project string) {
	_, items, err := m.carriedItems(ctx, project)
	if err != nil {
		if !plannerDown(err) && ctx.Err() == nil {
			log.Warn("read what a project's merge would carry failed", "project", project, "error", err)
		}
		return
	}
	if slices.ContainsFunc(items, func(it mergeItem) bool { return it.epic.Run != nil && it.epic.Status == board.StatusDone }) {
		m.autoMerge(ctx, project)
	}
}

// autoMerge starts the Project's merge job unless its base branch has
// everything the integration branch carries, or a failed merge blocks these
// tips. While the job runs, it runs again once it ends.
func (m *Manager) autoMerge(ctx context.Context, project string) {
	due, err := m.mergeDue(ctx, project)
	if err == nil && due {
		_, err = m.startMerge(project, false)
	}
	if err != nil && !errors.Is(err, errMergeBusy) && !errors.Is(err, errShuttingDown) && !plannerDown(err) && ctx.Err() == nil {
		log.Warn("start a merge failed", "project", project, "error", err)
	}
}

// mergeDue reports whether the Project's base branch lacks something its
// integration branch carries, at tips no failed merge blocks.
func (m *Manager) mergeDue(ctx context.Context, project string) (bool, error) {
	has, baseTip, tip, err := m.baseHasAll(ctx, project)
	if err != nil || has || tip == "" || baseTip == "" {
		return false, err
	}
	m.lanes.mu.Lock()
	defer m.lanes.mu.Unlock()
	pm := m.mergeOfLocked(project)
	return pm.shown.State != mergeBlocked || pm.integ != tip || pm.base != baseTip, nil
}

// baseHasAll reports whether the Project's base branch has everything its
// integration branch carries, and returns both tips, "" for a branch that
// is not there or while no approval named a base branch. Once the base has
// it all, a merge that did not go through is forgotten, with its retry.
func (m *Manager) baseHasAll(ctx context.Context, project string) (bool, string, string, error) {
	repo, base, err := m.mergeRepo(ctx, project)
	if err != nil || repo == nil {
		return false, "", "", err
	}
	baseTip, tip, err := repo.mergeTips(ctx, base)
	if err != nil || tip == "" || baseTip == "" {
		return false, baseTip, tip, err
	}
	has, err := repo.hasAll(ctx, baseTip, tip)
	if has {
		m.mergeSettled(project)
	}
	return has, baseTip, tip, err
}

// mergeRepo opens the Project's repository for lanes and returns its base
// branch; a nil repository when no approval named one yet.
func (m *Manager) mergeRepo(ctx context.Context, project string) (*laneRepo, string, error) {
	dir, err := m.boardDir(ctx, project)
	if err != nil {
		return nil, "", err
	}
	var ps board.ProjectSettings
	if err := m.withBoard(func(st *board.Store) error {
		var err error
		ps, err = st.ProjectSettings(ctx, project)
		return err
	}); err != nil || ps.BaseRef == "" {
		return nil, "", err
	}
	repo, err := openLanes(ctx, project, dir)
	return repo, ps.BaseRef, err
}

// mergeTips are the base branch's and the integration branch's commits, ""
// for a branch that is not there.
func (r *laneRepo) mergeTips(ctx context.Context, base string) (string, string, error) {
	baseTip, err := r.tipOf(ctx, base)
	if err != nil {
		return "", "", err
	}
	tip, err := r.tipOf(ctx, r.integ)
	return baseTip, tip, err
}

// MergePreview is what a merge of a Project's integration branch into its
// base branch would carry: each landing and revert the base lacks, oldest
// first.
type MergePreview struct {
	Branch  string         `json:"branch"`
	BaseRef string         `json:"base_ref"`
	Items   []MergeCarried `json:"items"`
}

// MergeCarried is a landing, or a revert, a merge carries: its subtask, the
// subtask's epic, and whether the landing changed tests or build files.
type MergeCarried struct {
	CardID  string `json:"card_id"`
	Seq     int64  `json:"seq"`
	Title   string `json:"title"`
	EpicID  string `json:"epic_id"`
	Revert  bool   `json:"revert"`
	Flagged bool   `json:"flagged"`
}

// PreviewMerge lists what merging the Project id's integration branch into
// its base branch would carry, for the owner's Retry merge or Merge now.
func (m *Manager) PreviewMerge(id string) (MergePreview, error) {
	if _, err := m.boardOwner(m.ctx, id); err != nil {
		return MergePreview{}, err
	}
	base, items, err := m.carriedItems(m.ctx, id)
	if err != nil {
		return MergePreview{}, err
	}
	out := MergePreview{Branch: integBranch(id), BaseRef: base, Items: []MergeCarried{}}
	for _, it := range items {
		out.Items = append(out.Items, MergeCarried{CardID: it.card.ID, Seq: it.card.Seq, Title: it.card.Title, EpicID: it.epic.ID, Revert: it.revert, Flagged: it.flagged})
	}
	return out, nil
}

// carriedItems returns the Project's base branch, "" while no approval
// named one, and lists, oldest first, the landings and reverts on the
// integration branch that the base branch lacks; none once the base has
// everything the integration branch carries, or when either branch is not
// there.
func (m *Manager) carriedItems(ctx context.Context, project string) (string, []mergeItem, error) {
	repo, base, err := m.mergeRepo(ctx, project)
	if err != nil || repo == nil {
		return "", nil, err
	}
	baseTip, tip, err := repo.mergeTips(ctx, base)
	if err != nil || tip == "" || baseTip == "" {
		return base, nil, err
	}
	if has, err := repo.hasAll(ctx, baseTip, tip); err != nil || has {
		return base, nil, err
	}
	commits, err := repo.carried(ctx, baseTip, tip)
	if err != nil {
		return base, nil, err
	}
	return base, m.mergeItems(ctx, commits), nil
}

// MergeProject is the owner's Retry merge of the Project id, or an early
// merge of what landed (ADR 0006 §5.8): it starts the merge job, forgetting
// a failed merge's block and retry, and returns the job's ID.
func (m *Manager) MergeProject(id string) (string, error) {
	if _, err := m.boardOwner(m.ctx, id); err != nil {
		return "", err
	}
	return m.startMerge(id, true)
}

// startMerge starts the Project's merge job and returns its ID. While one
// runs it refuses, and an automatic trigger has it run again once it ends.
func (m *Manager) startMerge(project string, owner bool) (string, error) {
	m.lanes.mu.Lock()
	pm := m.mergeOfLocked(project)
	if pm.running {
		pm.again = pm.again || !owner
		m.lanes.mu.Unlock()
		return "", errMergeBusy
	}
	if owner {
		pm.settle()
	}
	pm.running = true
	m.lanes.mu.Unlock()
	m.mu.Lock()
	job, err := m.startProjectJobLocked(jobMerge, project)
	if err == nil {
		m.broadcastJobLocked(job, jobRunning, "", nil)
	}
	m.mu.Unlock()
	if err != nil {
		m.lanes.mu.Lock()
		pm.running = false
		m.lanes.mu.Unlock()
		return "", err
	}
	go m.runMerge(job)
	return job.id, nil
}

// runMerge runs one merge job to its end, bound to the service, and runs
// it again when a trigger came meanwhile.
func (m *Manager) runMerge(job *boardJob) {
	ctx, cancel := m.bound(context.Background())
	defer cancel()
	if err := m.mergeProject(ctx, job.project); err != nil {
		m.endJob(job, jobFailed, clipRunes(displaytext.Sanitize(err.Error()), maxDetailRunes), nil)
	} else {
		m.endJob(job, jobDone, "", nil)
	}
	m.lanes.mu.Lock()
	pm := m.mergeOfLocked(job.project)
	again := pm.again
	pm.running, pm.again = false, false
	m.lanes.mu.Unlock()
	if again {
		m.autoMerge(ctx, job.project)
	}
}

// mergeProject merges the Project's integration branch into its base
// branch under the land mutex, unless the base has everything it carries.
// Once the tips are read, it runs to its end whatever happens to ctx: the
// merge, the comment on each epic whose work the integration branch
// carries, and the record of a merge that did not go through.
func (m *Manager) mergeProject(ctx context.Context, project string) error {
	repo, base, err := m.mergeRepo(ctx, project)
	if err != nil || repo == nil {
		return err
	}
	unlock, err := m.lockLand(ctx, project)
	if err != nil {
		return err
	}
	defer unlock()
	baseTip, tip, err := repo.mergeTips(ctx, base)
	if err != nil || tip == "" {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	at := mergeAttempt{repo.integ, tip, base, baseTip}
	if baseTip == "" {
		err := newError(http.StatusConflict, "there is no branch %s to merge %s into", displaytext.Sanitize(base), repo.integ)
		m.mergeOutcome(ctx, project, nil, at, "", err)
		return err
	}
	if has, err := repo.hasAll(ctx, baseTip, tip); err != nil || has {
		if has {
			m.mergeSettled(project)
		}
		return err
	}
	commits, err := repo.carried(ctx, baseTip, tip)
	if err != nil {
		return err
	}
	items := m.mergeItems(ctx, commits)
	merged, err := m.mergeInto(ctx, repo, base, mergeMessage(repo.integ, items))
	m.mergeOutcome(ctx, project, items, at, merged, err)
	return err
}

// mergeInto merges the integration branch into base with message. Where a
// worktree has base checked out, the merge runs there as the owner's git
// actions do, refused git_busy while one runs or a Task there works.
func (m *Manager) mergeInto(ctx context.Context, repo *laneRepo, base, message string) (string, error) {
	dir, err := repo.checkedOut(ctx, base)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return repo.mergeIntoBase(ctx, base, message)
	}
	end, err := m.beginWrite(dir)
	if err != nil {
		return "", err
	}
	defer end()
	merged, err := repo.mergeIntoBase(ctx, base, message)
	if merged != "" {
		m.kickDiffsIn(dir)
	}
	return merged, err
}

// mergeSettled forgets the Project's failed merge: its base has it all.
func (m *Manager) mergeSettled(project string) {
	m.lanes.mu.Lock()
	m.mergeOfLocked(project).settle()
	m.lanes.mu.Unlock()
}

// mergeAttempt is what a merge merged: the integration branch at tip into
// base at baseTip.
type mergeAttempt struct {
	integ, tip, base, baseTip string
}

func (a mergeAttempt) String() string {
	return fmt.Sprintf("%s at %s into %s at %s", a.integ, shortSHA(a.tip), displaytext.Sanitize(a.base), shortSHA(a.baseTip))
}

// mergeOutcome records how the merge attempt went and says so, once per
// outcome, on each epic whose work it carries: merged, with what it
// carried; waiting on git, its locks or the owner's working tree, tried
// again after the next backoff; or, failed otherwise, blocked for these
// tips.
func (m *Manager) mergeOutcome(ctx context.Context, project string, items []mergeItem, at mergeAttempt, merged string, err error) {
	var body string
	reason := ""
	if err != nil {
		reason = displaytext.Sanitize(err.Error())
	}
	m.lanes.mu.Lock()
	pm := m.mergeOfLocked(project)
	tries := pm.tries
	pm.settle()
	switch code := apiCode(err); {
	case err == nil:
		if merged != "" {
			body = mergedText(at.base, merged, items)
		}
	case code == codeGitBusy || code == codeLocalChanges || gitLocked(err):
		wait := mergeBackoff[min(tries, len(mergeBackoff)-1)]
		retryAt := time.Now().Add(wait)
		pm.shown, pm.tries = BoardMerge{State: mergeWaiting, Reason: reason, RetryAt: &retryAt}, tries+1
		pm.retry = time.AfterFunc(wait, func() { m.goLanes(func(ctx context.Context) { m.autoMerge(ctx, project) }) })
		body = fmt.Sprintf("Merge of %s is waiting: %s\nuam tries it again until it goes through.", at, reason)
	default:
		pm.shown, pm.integ, pm.base = BoardMerge{State: mergeBlocked, Reason: reason}, at.tip, at.baseTip
		body = fmt.Sprintf("Merge of %s is blocked: %s\nResolve it on %s, or revert the subtask it conflicts with, then Retry merge in the Planner.", at, reason, displaytext.Sanitize(at.base))
	}
	m.lanes.mu.Unlock()
	if body == "" {
		return
	}
	for _, epic := range epicsOf(items) {
		m.noteCard(ctx, epic.ID, body)
	}
}

// gitLocked reports whether git refused because a lock file was there:
// another git process, such as an editor's, holds the index or a ref, for
// now.
func gitLocked(err error) bool {
	return strings.Contains(err.Error(), ".lock': File exists")
}

// carriedCommit is a landing or a revert on the integration branch: the
// request a landing accepted, or the card a revert reverted ("#seq").
type carriedCommit struct {
	request, revert string
}

// carried lists, oldest first, the landings and reverts on the integration
// branch's first-parent line up to tip that baseTip lacks.
func (r *laneRepo) carried(ctx context.Context, baseTip, tip string) ([]carriedCommit, error) {
	out, err := r.output(ctx, r.top, "log", "--first-parent", "--reverse",
		"--format=%(trailers:key="+trailerRequest+",valueonly,separator=%x2C)%x1f%(trailers:key="+trailerRevert+",valueonly,separator=%x2C)", baseTip+".."+tip, "--")
	if err != nil {
		return nil, err
	}
	var commits []carriedCommit
	for _, line := range strings.Split(out, "\n") {
		request, revert, _ := strings.Cut(line, "\x1f")
		request, revert = strings.TrimSpace(request), strings.TrimSpace(revert)
		switch {
		case request != "" && !strings.Contains(request, ","):
			commits = append(commits, carriedCommit{request: request})
		case cardTrailer.MatchString(revert):
			commits = append(commits, carriedCommit{revert: revert})
		}
	}
	return commits, nil
}

// mergeItem is a landing or a revert a merge carries: its subtask, the
// subtask's epic, and whether the landing changed tests or build files.
type mergeItem struct {
	card, epic      board.Card
	revert, flagged bool
}

// mergeItems are the commits' subtasks; a commit whose card or request
// the board no longer has is left out.
func (m *Manager) mergeItems(ctx context.Context, commits []carriedCommit) []mergeItem {
	var out []mergeItem
	err := m.withBoard(func(st *board.Store) error {
		for _, c := range commits {
			it := mergeItem{revert: c.request == ""}
			ref := c.revert
			if !it.revert {
				r, err := st.Request(ctx, c.request)
				if err != nil {
					continue
				}
				ref, it.flagged = r.CardID, slices.Contains(r.Flags, board.FlagTestsOrBuildChanged)
			}
			card, err := st.Card(ctx, ref)
			if err != nil {
				continue
			}
			path, err := ancestors(ctx, st, card)
			if err != nil || len(path) == 0 {
				continue
			}
			it.card, it.epic = card, path[0]
			out = append(out, it)
		}
		return nil
	})
	if err != nil {
		log.Warn("read what a merge carries failed", "error", err)
	}
	return out
}

// epicsOf are the items' epics, in the order they first come.
func epicsOf(items []mergeItem) []board.Card {
	var out []board.Card
	for _, it := range items {
		if !slices.ContainsFunc(out, func(c board.Card) bool { return c.ID == it.epic.ID }) {
			out = append(out, it.epic)
		}
	}
	return out
}

// mergeMessage is the merge commit's message: the integration branch and
// the epics whose work it carries.
func mergeMessage(integ string, items []mergeItem) string {
	var names []string
	for _, e := range epicsOf(items) {
		names = append(names, fmt.Sprintf("%s (#%d)", strings.TrimSpace(e.Title), e.Seq))
	}
	if len(names) == 0 {
		return "Merge " + integ
	}
	return fmt.Sprintf("Merge %s: %s", integ, strings.Join(names, ", "))
}

// mergedText is the comment on a merge that went through: what it carried,
// marking the landings that changed tests or build files (ADR 0006 §9
// decision 3).
func mergedText(base, merged string, items []mergeItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Merged into %s as %s.", displaytext.Sanitize(base), shortSHA(merged))
	if len(items) > 0 {
		b.WriteString(" It carried:")
	}
	shown := items[:min(len(items), maxListedMerged)]
	for _, it := range shown {
		switch {
		case it.revert:
			fmt.Fprintf(&b, "\n- the revert of #%d %s", it.card.Seq, it.card.Title)
		case it.flagged:
			fmt.Fprintf(&b, "\n- #%d %s, which changed tests or build files", it.card.Seq, it.card.Title)
		default:
			fmt.Fprintf(&b, "\n- #%d %s", it.card.Seq, it.card.Title)
		}
	}
	if more := len(items) - len(shown); more > 0 {
		fmt.Fprintf(&b, "\n- and %d more", more)
	}
	return b.String()
}
