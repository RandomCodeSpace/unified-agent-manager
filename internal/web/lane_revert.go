package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// The owner's Revert of landed work (ADR 0006 §5.7): the preview, the
// revert job, and the recovery of a revert a crash interrupted.

const (
	// jobRevert is the owner's Revert (board_ai.go).
	jobRevert = "revert"
	// revertIntent is the revert step after the store recorded the revert
	// and before the integration branch moves, for landHook.
	revertIntent = "revert"
)

// RevertRequest is the owner's Revert of a card: the cards to take along
// besides it, the cards the preview showed, which must still be the ones
// it reverts, and why.
type RevertRequest struct {
	Include []string `json:"include"`
	Expect  []string `json:"expect"`
	Comment string   `json:"comment"`
}

// RevertPreview is what a Revert would do: the integration branch, the
// IDs of the cards it reverts (posted back as expect) and of the running
// attempts to Stop first, each landing with its files, whether the base
// branch already has one of them, and the conflict, when the revert would
// not apply.
type RevertPreview struct {
	Branch   string          `json:"branch"`
	Cards    []string        `json:"cards"`
	Running  []string        `json:"running"`
	Landings []RevertLanding `json:"landings"`
	Merged   bool            `json:"merged"`
	Conflict *RevertConflict `json:"conflict,omitempty"`
}

// RevertLanding is one landed commit a Revert reverts.
type RevertLanding struct {
	CardID string   `json:"card_id"`
	Seq    int64    `json:"seq"`
	Title  string   `json:"title"`
	SHA    string   `json:"sha"`
	Files  []string `json:"files"`
}

// RevertConflict is why a Revert would not apply, as the API's refusals
// say it.
type RevertConflict struct {
	Code    string   `json:"code"`
	Message string   `json:"error"`
	Refs    []string `json:"refs,omitempty"`
}

// PreviewRevert returns what reverting the card ref, with the cards
// include adds, would do. Its dry run writes commits nobody refers to and
// moves nothing.
func (m *Manager) PreviewRevert(ref string, include []string) (RevertPreview, error) {
	ctx := m.ctx
	var c board.Card
	var closure board.Closure
	var ps board.ProjectSettings
	err := m.withBoard(func(st *board.Store) error {
		var err error
		if c, err = st.Card(ctx, ref); err != nil {
			return err
		}
		if closure, err = st.RevertClosure(ctx, ref, include); err != nil {
			return err
		}
		ps, err = st.ProjectSettings(ctx, c.ProjectID)
		return err
	})
	if err != nil {
		return RevertPreview{}, err
	}
	dir, err := m.boardDir(ctx, c.ProjectID)
	if err != nil {
		return RevertPreview{}, err
	}
	repo, err := openLanes(ctx, c.ProjectID, dir)
	if err != nil {
		return RevertPreview{}, err
	}
	out := RevertPreview{Branch: repo.integ, Cards: cardIDs(closure.Cards), Running: cardIDs(closure.Running), Landings: []RevertLanding{}}
	baseTip := ""
	if ps.BaseRef != "" {
		if baseTip, err = repo.tipOf(ctx, ps.BaseRef); err != nil {
			return RevertPreview{}, err
		}
	}
	for _, l := range closure.Landings {
		files, merged, err := repo.landedFiles(ctx, l.SHA, baseTip)
		if err != nil {
			return RevertPreview{}, err
		}
		out.Merged = out.Merged || merged
		out.Landings = append(out.Landings, RevertLanding{CardID: l.CardID, Seq: l.Seq, Title: l.Title, SHA: l.SHA, Files: files})
	}
	tip, err := repo.integTip(ctx)
	if err == nil {
		_, err = repo.revertChain(ctx, tip, revertItems(closure.Landings))
	}
	var refusal *Error
	switch {
	case errors.As(err, &refusal) && refusal.Code == codeRevertConflict:
		out.Conflict = &RevertConflict{Code: refusal.Code, Message: refusal.Message, Refs: refusal.Refs}
	case err != nil:
		return RevertPreview{}, err
	}
	return out, nil
}

// integTip is the integration branch's commit, refusing when there is no
// such branch.
func (r *laneRepo) integTip(ctx context.Context) (string, error) {
	tip, err := r.tipOf(ctx, r.integ)
	if err == nil && tip == "" {
		err = newError(http.StatusConflict, "there is no branch %s", r.integ)
	}
	return tip, err
}

// landedFiles lists the files the landing sha changed, and reports whether
// baseTip ("" for none) has it. A landing whose commit is gone, after a
// hand edit of the integration branch, changed nothing that can be told.
func (r *laneRepo) landedFiles(ctx context.Context, sha, baseTip string) ([]string, bool, error) {
	if exists, err := r.hasCommit(ctx, sha); err != nil || !exists {
		return []string{}, false, err
	}
	out, code, stderr, err := runGit(ctx, r.git, r.top, maxStatusBytes, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", sha)
	if err != nil {
		return nil, false, err
	}
	if code != 0 {
		return nil, false, newError(http.StatusBadGateway, "git diff-tree failed: %s", gitMessage(stderr))
	}
	files := append([]string{}, nulRecords(out)...)
	if baseTip == "" {
		return files, false, nil
	}
	merged, err := r.isAncestor(ctx, sha, baseTip)
	return files, merged, err
}

func cardIDs(cards []board.Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}

func revertItems(landings []board.Landing) []revertItem {
	out := make([]revertItem, len(landings))
	for i, l := range landings {
		out[i] = revertItem{sha: l.SHA, seq: l.Seq, title: l.Title}
	}
	return out
}

// RevertCard starts the owner's Revert of the card ref as a job and returns
// its ID. It refuses at once, as the job would, while the cards it would
// revert are not the ones req expects (stale) or an attempt that started
// on top of them runs (revert_running).
func (m *Manager) RevertCard(ref string, req RevertRequest) (string, error) {
	a, c, err := m.cardOwner(m.ctx, ref)
	if err != nil {
		return "", err
	}
	if err := m.withBoard(func(st *board.Store) error {
		_, err := st.CheckRevert(m.ctx, a, ref, req.Include, req.Expect)
		return err
	}); err != nil {
		return "", err
	}
	m.mu.Lock()
	job, err := m.startJobLocked(jobRevert, c)
	if err == nil {
		m.broadcastJobLocked(job, jobRunning, "", nil)
	}
	m.mu.Unlock()
	if err != nil {
		return "", err
	}
	go m.runRevert(job, a, req)
	return job.id, nil
}

// runRevert runs one revert job to its end, bound to the service rather
// than to the request that started it.
func (m *Manager) runRevert(job *boardJob, a board.Actor, req RevertRequest) {
	ctx, cancel := m.bound(context.Background())
	defer cancel()
	if err := m.revertLanded(ctx, a, job.card, req); err != nil {
		m.endJob(job, jobFailed, clipRunes(displaytext.Sanitize(err.Error()), maxDetailRunes), nil)
		return
	}
	m.endJob(job, jobDone, "", nil)
}

// revertLanded reverts what reverting the card c takes along, under the
// Project's land mutex. On ctx it checks the closure again and builds the
// chain of revert commits on the integration tip as objects only, which a
// conflict refuses with nothing written. Then, whatever happens to ctx, the
// store records the revert and the branch moves by compare-and-swap; when
// it cannot move, the store's record is withdrawn.
func (m *Manager) revertLanded(ctx context.Context, a board.Actor, c board.Card, req RevertRequest) error {
	dir, err := m.boardDir(ctx, c.ProjectID)
	if err != nil {
		return err
	}
	unlock, err := m.lockLand(ctx, c.ProjectID)
	if err != nil {
		return err
	}
	defer unlock()
	var closure board.Closure
	if err := m.withBoard(func(st *board.Store) error {
		var err error
		closure, err = st.CheckRevert(ctx, a, c.ID, req.Include, req.Expect)
		return err
	}); err != nil {
		return err
	}
	repo, err := openLanes(ctx, c.ProjectID, dir)
	if err != nil {
		return err
	}
	tip, err := repo.integTip(ctx)
	if err != nil {
		return err
	}
	commit, err := repo.revertChain(ctx, tip, revertItems(closure.Landings))
	if err != nil {
		return err
	}

	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlTimeout)
	defer cancel()
	if err := m.withBoard(func(st *board.Store) error {
		_, err := st.Revert(cctx, a, c.ID, req.Include, req.Expect, commit, req.Comment)
		return err
	}); err != nil {
		return err
	}
	if m.landHook != nil {
		m.landHook(revertIntent)
	}
	if err := repo.moveBranch(cctx, repo.integ, commit, tip); err != nil {
		reason := displaytext.Sanitize(err.Error())
		if uerr := m.withBoard(func(st *board.Store) error { return st.UndoRevert(cctx, commit, reason) }); uerr != nil {
			log.Warn("withdraw a revert failed; recovery finishes it", "commit", commit, "error", uerr)
		}
		return &Error{Status: http.StatusConflict, Code: apiCode(err), Message: "revert not applied: " + reason}
	}
	return nil
}

// recoverReverts finishes the reverts a crash interrupted (ADR 0006 §4.7):
// the integration branch is fast-forwarded to a revert commit it is behind,
// and a revert it moved away from, or cannot take, is withdrawn.
func (m *Manager) recoverReverts(ctx context.Context) {
	var cards []board.Card
	if err := m.withBoard(func(st *board.Store) error {
		var err error
		cards, err = st.Reverted(ctx)
		return err
	}); err != nil {
		log.Warn("read the reverts to recover failed", "error", err)
		return
	}
	done := map[string]bool{}
	for _, c := range cards {
		if done[c.Lane.RevertedSHA] {
			continue
		}
		done[c.Lane.RevertedSHA] = true
		if err := m.recoverRevert(ctx, c.ProjectID, c.Lane.RevertedSHA); err != nil && !plannerDown(err) {
			log.Warn("recover a revert failed", "card", c.ID, "commit", c.Lane.RevertedSHA, "error", err)
		}
	}
}

// recoverRevert finishes the revert in commit of the Project project.
func (m *Manager) recoverRevert(ctx context.Context, project, commit string) error {
	dir, err := m.boardDir(ctx, project)
	if err != nil {
		return err
	}
	unlock, err := m.lockLand(ctx, project)
	if err != nil {
		return err
	}
	defer unlock()
	repo, err := openLanes(ctx, project, dir)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlTimeout)
	defer cancel()
	on, err := repo.finishOnInteg(cctx, commit)
	switch {
	case err == nil && on:
		return nil
	case err != nil && apiCode(err) != codeGitBusy:
		return err
	}
	reason := fmt.Sprintf("%s moved on before the revert reached it", repo.integ)
	if err != nil {
		reason = displaytext.Sanitize(err.Error())
	}
	return m.withBoard(func(st *board.Store) error { return st.UndoRevert(cctx, commit, reason) })
}

// reopenKeepingCode is the owner's "Reopen without reverting code" of the
// landed subtask ref: the status post with keep_code.
func reopenKeepingCode(ctx context.Context, st *board.Store, a board.Actor, ref string, body statusBody) (board.Card, error) {
	if body.Status != board.StatusTodo || body.Force {
		return board.Card{}, invalidBoard("keep_code goes only with status todo")
	}
	c, err := st.Card(ctx, ref)
	if err != nil {
		return c, err
	}
	return st.Reopen(ctx, a, ref, integBranch(c.ProjectID), body.Comment)
}
