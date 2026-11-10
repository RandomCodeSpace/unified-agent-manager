package web

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Who settled a Task when the owner did not: the quiet-Task sweep, the
// merged-work sweep, or, in records written before it was removed, the done
// check's sweep.
const (
	settledByAuto     = "auto"
	settledByMerged   = "merged"
	settledByReviewed = "reviewed"
)

// autoSettleAfter is how long an active Task stays quiet before the hourly
// sweep settles it; a var for tests. Quiet is measured from updated_at, the
// Task's last activity.
var autoSettleAfter = 7 * 24 * time.Hour

// autoSettle settles every active Task quiet for autoSettleAfter that could
// be settled by hand and waits for nothing (unattendedLocked). Reopen
// restarts its week. The settle is not activity, so updated_at keeps the
// Task's last message.
//
// A Task with unsaved work, by Settle's rule (putAwayWork), stays active:
// each due Task's work is read first.
func (m *Manager) autoSettle() {
	m.mu.Lock()
	var due []*webSession
	for _, s := range m.sessions {
		if m.autoSettleableLocked(s) {
			due = append(due, s)
		}
	}
	m.mu.Unlock()
	saved := make(map[*webSession]bool, len(due))
	for _, s := range due {
		if p, _, _ := m.putAwayWork(m.ctx, s); p == nil {
			saved[s] = true
		}
	}
	m.sweepSettle(settledByAuto, func(s *webSession) bool {
		return saved[s] && m.autoSettleableLocked(s)
	})
}

// settleMerged settles every active Thread nothing waits on (unattendedLocked)
// whose own worktree holds work that has landed on its Project's default
// branch (mergedTip), without waiting for its quiet week. Settling removes
// the worktree and, on that proof alone, the uam/… branch made for the
// Thread (kickWorktreeRemoveLocked). It runs in the hourly sweep, off every
// request path, and reads only the refs on disk: nothing is fetched, and a
// Project whose default branch is not known is left alone.
func (m *Manager) settleMerged() {
	type candidate struct {
		s                     *webSession
		id, dir, path, branch string
	}
	m.mu.Lock()
	var due []candidate
	for _, s := range m.sessions {
		p := m.projects[s.projectID]
		if p != nil && s.place != nil && s.place.Worktree != "" && strings.HasPrefix(s.place.Branch, branchPrefix) && m.unattendedLocked(s) {
			due = append(due, candidate{s, s.id, p.Dir, s.place.Worktree, s.place.Branch})
		}
	}
	m.mu.Unlock()
	if len(due) == 0 {
		return
	}
	defaults := map[string]string{}
	merged := map[string]string{}
	proven := map[*webSession]bool{}
	var kept []string
	for _, c := range due {
		def, ok := defaults[c.dir]
		if !ok {
			def = readDefaultBranch(m.ctx, c.dir)
			defaults[c.dir] = def
		}
		if def == "" || def == c.branch {
			continue
		}
		tip, unproven := mergedTip(m.ctx, c.path, c.branch, def)
		if tip != "" {
			merged[c.path], proven[c.s] = tip, true
		} else if unproven {
			kept = append(kept, c.id)
		}
	}
	m.mu.Lock()
	if m.worktrees.unproven == nil {
		m.worktrees.unproven = map[string]bool{}
	}
	// Each Thread counts once per run of uam, not once an hour.
	kept = slices.DeleteFunc(kept, func(id string) bool { return m.worktrees.unproven[id] })
	for _, id := range kept {
		m.worktrees.unproven[id] = true
	}
	m.worktrees.merged = merged
	m.mu.Unlock()
	m.countN("merged_kept", len(kept))
	if len(proven) > 0 {
		m.sweepSettle(settledByMerged, func(s *webSession) bool { return proven[s] && m.unattendedLocked(s) })
	}
	// The proof is for this sweep only: a later settle reads the work again.
	m.mu.Lock()
	m.worktrees.merged = nil
	m.mu.Unlock()
}

// mergedTip is the proof that the work on branch, checked out in the
// Thread's worktree at path, has landed on def, origin's default branch as
// the refs on disk have it (refs/remotes/origin/<def>). It returns the
// branch's commit when all three hold, "" otherwise:
//
//  1. The worktree is on branch, and the branch has moved since it was made
//     (the oldest entry of its reflog is another commit): a Thread that
//     never committed has landed nothing.
//  2. The branch's commit is an ancestor of def (a merge or fast-forward:
//     def's history has every commit); or, for a squash or a rebase, the
//     branch differs from its merge base with def and merging it into def
//     (git merge-tree, which checks nothing out) is clean and gives exactly
//     def's tree. Def then already holds every change the branch made. A
//     change that never landed, one made on the branch after the merge, and
//     one def reverted or changed again each make that merge differ from
//     def or conflict, so none of them passes.
//  3. Nothing else would be lost: the worktree has no changed or untracked
//     file (ignored ones aside) and no stash was made on the branch. The
//     commits on no remote are the ones 2 covers, so they are not counted
//     again.
//
// unproven reports that 1 held and 2 did not: commits kept for want of a
// proof. A git failure, a cut-off answer or any other doubt keeps
// everything.
func mergedTip(ctx context.Context, path, branch, def string) (tip string, unproven bool) {
	repo, _, err := openRepo(ctx, path)
	if err != nil || repo == nil {
		return "", false
	}
	// git answers with a command's trimmed output and exit code, -1 when it
	// could not run or said more than is kept.
	const limit = 1 << 20
	git := func(args ...string) (string, int) {
		out, code, _, err := runGit(ctx, repo.git, repo.top, limit, args...)
		if err != nil || len(out) >= limit {
			return "", -1
		}
		return strings.TrimSpace(string(out)), code
	}
	ref := "refs/heads/" + branch
	if head, code := git("symbolic-ref", "--quiet", "HEAD"); code != 0 || head != ref {
		return "", false
	}
	tip, code := git("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if code != 0 || tip == "" {
		return "", false
	}
	target, code := git("rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+def+"^{commit}")
	if code != 0 || target == "" {
		return "", false
	}
	moves, code := git("reflog", "show", "--format=%H", ref)
	if made := moves[strings.LastIndexByte(moves, '\n')+1:]; code != 0 || made == "" || made == tip {
		return "", false
	}
	if _, code := git("merge-base", "--is-ancestor", tip, target); code != 0 {
		if code != 1 {
			return "", true
		}
		base, code := git("merge-base", tip, target)
		if code != 0 || base == "" {
			return "", true
		}
		// The trees of the merge base, the branch and def.
		out, code := git("rev-parse", base+"^{tree}", tip+"^{tree}", target+"^{tree}")
		trees := strings.Fields(out)
		if code != 0 || len(trees) != 3 || trees[0] == trees[1] {
			return "", true
		}
		// merge-tree writes the merged tree's objects and nothing else: no
		// ref, index or file changes.
		if out, code := git("merge-tree", "--write-tree", target, tip); code != 0 || firstLine(out) != trees[2] {
			return "", true
		}
	}
	if entries, truncated, err := repo.status(ctx); err != nil || truncated || len(entries) > 0 {
		return "", false
	}
	stashes, code := git("stash", "list", "--format=%gs")
	if code != 0 {
		return "", false
	}
	for line := range strings.Lines(stashes) {
		if strings.HasPrefix(line, "WIP on "+branch+":") || strings.HasPrefix(line, "On "+branch+":") {
			return "", false
		}
	}
	return tip, false
}

// sweepSettle settles, as by, every Task settleable reports, then writes
// them once.
func (m *Manager) sweepSettle(by string, settleable func(*webSession) bool) {
	m.mu.Lock()
	var due []*webSession
	for _, s := range m.sessions {
		if settleable(s) {
			due = append(due, s)
		}
	}
	m.mu.Unlock()
	settled := 0
	for _, s := range due {
		if m.settleOne(s, by, settleable) {
			settled++
		}
	}
	if settled == 0 {
		return
	}
	if err := m.flush(); err != nil {
		log.Warn("persist settled web tasks failed", "by", by, "error", err)
	}
	log.Info("settled web tasks", "by", by, "tasks", settled)
}

// settleOne settles s as by when settleable still holds. A Task whose op is
// held (a prompt, open or stage change) is left for the next sweep.
func (m *Manager) settleOne(s *webSession, by string, settleable func(*webSession) bool) bool {
	if !s.op.TryLock() {
		return false
	}
	defer s.op.Unlock()
	m.mu.Lock()
	if m.closed || !settleable(s) {
		m.mu.Unlock()
		return false
	}
	conv, err := m.restageLocked(s, StageSettled, by)
	m.mu.Unlock()
	if err != nil {
		return false
	}
	m.endCalls(s.id)
	m.count("settle_" + by)
	if conv != nil {
		m.closeConversation(conv)
	}
	return true
}

// autoSettleableLocked reports whether the quiet-Task sweep may settle s now.
func (m *Manager) autoSettleableLocked(s *webSession) bool {
	return m.now().Sub(s.updatedAt) >= autoSettleAfter && m.unattendedLocked(s)
}

// unattendedLocked reports whether s is an active Task a sweep may settle:
// Settle would allow it and nothing waits on it, no turn, answer, queue,
// rewind, unseen failure or open page.
func (m *Manager) unattendedLocked(s *webSession) bool {
	if s.removed || s.stage != StageActive || s.opening != nil {
		return false
	}
	if s.runsOrWaitsLocked() || s.pendingAsk() != nil || s.rewindHoldLocked() != nil || s.unseenEnd {
		return false
	}
	return !m.watchedLocked(s.id)
}
