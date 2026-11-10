package web

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// A Task quiet for a week settles itself; the settle keeps updated_at, the
// Task's last message, and says who settled it.
func TestAutoSettleAfterSevenQuietDays(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	last := mustSummary(t, m, sum.ID).UpdatedAt

	setNow(m, last.Add(autoSettleAfter-time.Second))
	m.autoSettle()
	if got := mustSummary(t, m, sum.ID); got.Stage != StageActive || conv.Closes() != 0 {
		t.Fatalf("settled before a quiet week: %+v", got)
	}

	at := last.Add(autoSettleAfter)
	setNow(m, at)
	m.autoSettle()
	got := mustSummary(t, m, sum.ID)
	if got.Stage != StageSettled || got.SettledBy != settledByAuto || !got.SettledAt.Equal(at) || !got.UpdatedAt.Equal(last) || got.Open || conv.Closes() != 1 {
		t.Fatalf("after a quiet week = %+v, closes %d", got, conv.Closes())
	}
	rec, _ := loadRecord(t, st, "fake", sum.ID)
	if rec.Web.Stage != StageSettled || rec.Web.SettledBy != settledByAuto || !rec.Web.UpdatedAt.Equal(last) {
		t.Fatalf("stored = %+v", rec.Web)
	}
	m.mu.Lock()
	row := m.uamTaskRowLocked(m.sessions[sum.ID])
	m.mu.Unlock()
	if row.Stage != StageSettled || row.SettledBy != settledByAuto {
		t.Fatalf("uam task row = %+v", row)
	}
}

// A quiet Task that runs, waits or holds something the owner should see stays
// active.
func TestAutoSettleSparesTasksThatWaitOrRun(t *testing.T) {
	for name, hold := range map[string]func(*testing.T, *Manager, SessionSummary, *agenttest.Conversation){
		"working": func(t *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
				t.Fatal(err)
			}
		},
		"asking": func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitInteraction(question("q1"))
		},
		"queued": func(t *testing.T, m *Manager, sum SessionSummary, conv *agenttest.Conversation) {
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "later", RequestID: mustUUID(t), Mode: ModeQueue}); err != nil {
				t.Fatal(err)
			}
			conv.EmitTurn(agentapi.TurnCancelled, "") // pauses the queue
		},
		"subagent": func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitSubagent(agentapi.Subagent{ID: "a1", Name: "survey", Status: agentapi.SubagentRunning})
		},
		"unseen failure": func(_ *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			setSession(m, sum.ID, func(s *webSession) { s.unseenEnd = true })
		},
		"rewind held": func(_ *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			setSession(m, sum.ID, func(s *webSession) { s.rewind = &store.WebRewind{RequestID: "r", State: rewindUncertain} })
		},
		"page open": func(t *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			sub, _, err := m.Subscribe(sum.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { m.Unsubscribe(sub) })
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			sum, conv := createSession(t, m, prov)
			hold(t, m, sum, conv)
			setNow(m, mustSummary(t, m, sum.ID).UpdatedAt.Add(2*autoSettleAfter))
			m.autoSettle()
			if got := mustSummary(t, m, sum.ID); got.Stage != StageActive || got.SettledBy != "" {
				t.Fatalf("settled a Task that is %s: %+v", name, got)
			}
		})
	}
}

// A Task whose op is held is left for the next sweep; the others settle in
// the same sweep.
func TestAutoSettleSkipsAHeldTask(t *testing.T) {
	m, prov, st := newTestManager(t)
	held, _ := createSession(t, m, prov)
	free, _ := createSession(t, m, prov)
	setNow(m, time.Now().Add(2*autoSettleAfter))
	s, err := m.lookup(held.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.op.Lock()
	m.autoSettle()
	s.op.Unlock()
	if mustSummary(t, m, held.ID).Stage != StageActive {
		t.Fatal("settled a Task an operation held")
	}
	if rec, _ := loadRecord(t, st, "fake", free.ID); rec.Web.Stage != StageSettled || rec.Web.SettledBy != settledByAuto {
		t.Fatalf("free Task stored = %+v", rec.Web)
	}
	m.autoSettle()
	if got := mustSummary(t, m, held.ID); got.Stage != StageSettled || got.SettledBy != settledByAuto {
		t.Fatalf("held Task after the next sweep = %+v", got)
	}
}

// Reopen clears settled_by and is activity, so the week starts again; a
// settle by hand records no settled_by.
func TestAutoSettleReopenClearsSettledByAndRestartsTheClock(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	start := mustSummary(t, m, sum.ID).UpdatedAt.Add(autoSettleAfter)
	setNow(m, start)
	m.autoSettle()
	reopenedAt := start.Add(time.Hour)
	setNow(m, reopenedAt)
	reopened, err := m.Reopen(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Stage != StageActive || reopened.SettledBy != "" || !reopened.UpdatedAt.Equal(reopenedAt) {
		t.Fatalf("reopened = %+v", reopened)
	}
	if rec, _ := loadRecord(t, st, "fake", sum.ID); rec.Web.SettledBy != "" {
		t.Fatalf("stored after reopen = %+v", rec.Web)
	}
	setNow(m, reopenedAt.Add(autoSettleAfter-time.Minute))
	m.autoSettle()
	if got := mustSummary(t, m, sum.ID); got.Stage != StageActive {
		t.Fatalf("settled within a week of the reopen: %+v", got)
	}
	settled, err := m.Settle(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.SettledBy != "" {
		t.Fatalf("settled by hand = %+v", settled)
	}
}

// mergedThread is a Thread in its own worktree with one commit on its
// branch, in a repository whose origin has main as its default branch, as
// the refs on disk have it. Nothing is fetched: land moves that ref.
func mergedThread(t *testing.T) (ts *testServer, repo string, sum SessionSummary, land func()) {
	t.Helper()
	repo = worktreeRepo(t)
	commitFile(t, repo, "long.txt", longFile("1", "20"))
	gitIn(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "remote.git"))
	land = func() { gitIn(t, repo, "update-ref", "refs/remotes/origin/main", "main") }
	land()
	gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	ts = newTestServer(t, ServerConfig{})
	sum, _ = worktreeThread(t, ts, repo, "")
	commitFile(t, sum.Worktree.Path, "work.txt", "work\n")
	return ts, repo, sum, land
}

// longFile is twenty numbered lines with its first and last as given.
func longFile(first, last string) string {
	lines := []string{first}
	for i := 2; i < 20; i++ {
		lines = append(lines, strconv.Itoa(i))
	}
	return strings.Join(append(lines, last), "\n") + "\n"
}

func commitFile(t *testing.T, dir, name, text string) {
	t.Helper()
	writeRepoFile(t, dir, name, text)
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-q", "-m", "change "+name)
}

// squashInto lands branch on the repository's main as one new commit.
func squashInto(t *testing.T, repo, branch string) {
	t.Helper()
	gitIn(t, repo, "merge", "-q", "--squash", branch)
	gitIn(t, repo, "commit", "-q", "-m", "squash "+branch)
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	return gitOutput(t, repo, "branch", "--list", branch) != ""
}

// A Thread whose branch landed on the default branch settles in the next
// sweep, and its worktree and branch go, each removal logged and counted.
// Anything short of that proof, and anything not saved, keeps all three.
func TestMergedWorkSettlesAndRemovesItsBranch(t *testing.T) {
	for name, tc := range map[string]struct {
		setup  func(t *testing.T, repo, wt, branch string)
		merged bool
		// kept is the count of commits kept for want of a proof.
		kept int
	}{
		"plain merge": {setup: func(t *testing.T, repo, _, branch string) {
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", branch)
		}, merged: true},
		"fast-forward": {setup: func(t *testing.T, repo, _, branch string) {
			gitIn(t, repo, "merge", "-q", "--ff-only", branch)
		}, merged: true},
		// The default branch moved on after the squash, in another file and
		// in another part of the same one.
		"squash merge": {setup: func(t *testing.T, repo, wt, branch string) {
			commitFile(t, wt, "long.txt", longFile("1", "by the thread"))
			squashInto(t, repo, branch)
			commitFile(t, repo, "later.txt", "later\n")
			commitFile(t, repo, "long.txt", longFile("later", "by the thread"))
		}, merged: true},
		"squash merge, then more commits": {setup: func(t *testing.T, repo, wt, branch string) {
			squashInto(t, repo, branch)
			commitFile(t, wt, "more.txt", "more\n")
		}, kept: 1},
		"squash merge, then the same file changed again": {setup: func(t *testing.T, repo, wt, branch string) {
			squashInto(t, repo, branch)
			commitFile(t, wt, "work.txt", "work, reworked\n")
		}, kept: 1},
		"uncommitted change": {setup: func(t *testing.T, repo, wt, branch string) {
			squashInto(t, repo, branch)
			writeRepoFile(t, wt, "work.txt", "not committed\n")
		}},
		"untracked file": {setup: func(t *testing.T, repo, wt, branch string) {
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", branch)
			writeRepoFile(t, wt, "notes.txt", "new\n")
		}},
		"stash": {setup: func(t *testing.T, repo, wt, branch string) {
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", branch)
			writeRepoFile(t, wt, "work.txt", "stashed\n")
			gitIn(t, wt, "stash", "-q")
		}},
		"never merged": {setup: func(t *testing.T, repo, _, _ string) {
			commitFile(t, repo, "later.txt", "later\n")
		}, kept: 1},
		"squash merge, then reverted": {setup: func(t *testing.T, repo, _, branch string) {
			squashInto(t, repo, branch)
			gitIn(t, repo, "revert", "--no-edit", "HEAD")
		}, kept: 1},
		// A reverted merge still has every commit of the branch in the
		// default branch's history: nothing is lost with the branch.
		"plain merge, then reverted": {setup: func(t *testing.T, repo, _, branch string) {
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", branch)
			gitIn(t, repo, "revert", "--no-edit", "-m", "1", "HEAD")
		}, merged: true},
		"commits that cancel out": {setup: func(t *testing.T, _, wt, _ string) {
			gitIn(t, wt, "revert", "--no-edit", "HEAD")
		}, kept: 1},
		// The commit is in no history, only in the reflog the branch takes
		// with it.
		"commit reset away, branch on the default branch": {setup: func(t *testing.T, repo, wt, _ string) {
			commitFile(t, repo, "later.txt", "later\n")
			gitIn(t, wt, "reset", "-q", "--hard", "main")
		}, kept: 1},
		"no commit of its own": {setup: func(t *testing.T, repo, wt, _ string) {
			gitIn(t, wt, "reset", "-q", "--hard", "HEAD~1")
			commitFile(t, repo, "later.txt", "later\n")
		}},
		"switched to another branch": {setup: func(t *testing.T, repo, wt, branch string) {
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", branch)
			gitIn(t, wt, "switch", "-q", "-c", "other")
		}},
		"default branch unknown": {setup: func(t *testing.T, repo, _, branch string) {
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", branch)
			gitIn(t, repo, "symbolic-ref", "-d", "refs/remotes/origin/HEAD")
		}},
	} {
		t.Run(name, func(t *testing.T) {
			ts, repo, sum, land := mergedThread(t)
			wt, branch := sum.Worktree.Path, sum.Worktree.Branch
			tc.setup(t, repo, wt, branch)
			land()
			// Twice: a kept Thread counts once, not once a sweep.
			ts.m.settleMerged()
			ts.m.settleMerged()
			got := summaryOf(t, ts.m, sum.ID)
			if kept := countOf(ts.m, "merged_kept"); kept != tc.kept {
				t.Fatalf("merged_kept = %d, want %d", kept, tc.kept)
			}
			if !tc.merged {
				if got.Stage != StageActive || got.SettledBy != "" || worktreeGone(wt) || !branchExists(t, repo, branch) || countOf(ts.m, "merged_removed") != 0 {
					t.Fatalf("kept Thread = %+v, worktree gone %v, branch %v", got, worktreeGone(wt), branchExists(t, repo, branch))
				}
				return
			}
			if got.Stage != StageSettled || got.SettledBy != settledByMerged {
				t.Fatalf("merged Thread = %+v", got)
			}
			var removed []RemovedWorktree
			waitUntil(t, "removal logged", func() bool {
				removed = ts.m.removedLog()
				return len(removed) == 1
			})
			if r := removed[0]; r.Path != wt || r.Branch != branch || !r.BranchRemoved || r.TaskID != sum.ID {
				t.Fatalf("removed = %+v", r)
			}
			if !worktreeGone(wt) || branchExists(t, repo, branch) || countOf(ts.m, "merged_removed") != 1 || countOf(ts.m, "settle_merged") != 1 {
				t.Fatalf("worktree gone %v, branch %v, removed %d", worktreeGone(wt), branchExists(t, repo, branch), countOf(ts.m, "merged_removed"))
			}
			landed := gitOutput(t, repo, "rev-parse", "refs/remotes/origin/main")
			if !branchExists(t, repo, "main") {
				t.Fatal("the default branch went")
			}
			// Branch from here starts from the default branch too.
			_, forked, start, undo, err := ts.m.branchWorktree(t.Context(), Project{Dir: repo}, store.WebPlace{Mode: placeWorktree, Branch: branch, Worktree: wt}, "fork")
			if err != nil || start != landed {
				t.Fatalf("branch from a removed branch = %+v at %s, %v", forked, start, err)
			}
			undo()
			// Using the Thread again starts its branch from the default
			// branch, which holds its work.
			if _, err := ts.m.Reopen(sum.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := ts.m.Submit(sum.ID, PromptRequest{Text: "again", RequestID: mustUUID(t)}); err != nil {
				t.Fatal(err)
			}
			waitUntil(t, "worktree recreated", func() bool { return !worktreeGone(filepath.Join(wt, "README.md")) })
			if head := gitOutput(t, wt, "symbolic-ref", "--short", "HEAD"); head != branch || gitOutput(t, wt, "rev-parse", "HEAD") != landed {
				t.Fatalf("recreated on %q at %s", head, gitOutput(t, wt, "rev-parse", "--short", "HEAD"))
			}
		})
	}
}

// The merged-work sweep leaves a Thread that works, waits on the owner or
// has a prompt queued, whatever its branch.
func TestMergedWorkSparesThreadsThatWaitOrRun(t *testing.T) {
	for name, hold := range map[string]func(*testing.T, *Manager, SessionSummary, *agenttest.Conversation){
		"working": func(t *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
				t.Fatal(err)
			}
		},
		"asking": func(_ *testing.T, _ *Manager, _ SessionSummary, conv *agenttest.Conversation) {
			conv.EmitInteraction(question("q1"))
		},
		"queued": func(t *testing.T, m *Manager, sum SessionSummary, conv *agenttest.Conversation) {
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "work", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Submit(sum.ID, PromptRequest{Text: "later", RequestID: mustUUID(t), Mode: ModeQueue}); err != nil {
				t.Fatal(err)
			}
			conv.EmitTurn(agentapi.TurnCancelled, "") // pauses the queue
		},
		"page open": func(t *testing.T, m *Manager, sum SessionSummary, _ *agenttest.Conversation) {
			sub, _, err := m.Subscribe(sum.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { m.Unsubscribe(sub) })
		},
	} {
		t.Run(name, func(t *testing.T) {
			ts, repo, sum, land := mergedThread(t)
			gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", sum.Worktree.Branch)
			land()
			hold(t, ts.m, sum, ts.prov.Last())
			ts.m.settleMerged()
			if got := summaryOf(t, ts.m, sum.ID); got.Stage != StageActive || worktreeGone(sum.Worktree.Path) || !branchExists(t, repo, sum.Worktree.Branch) {
				t.Fatalf("settled a Thread that is %s: %+v", name, got)
			}
		})
	}
}

// Only a branch uam made for the Thread goes, and only while nothing else
// has it checked out and it is still at the commit proven merged.
func TestMergedWorkRemovesOnlyTheThreadsOwnBranch(t *testing.T) {
	t.Run("not a uam branch", func(t *testing.T) {
		ts, repo, sum, land := mergedThread(t)
		gitIn(t, sum.Worktree.Path, "branch", "-m", "topic")
		setSession(ts.m, sum.ID, func(s *webSession) { s.place.Branch = "topic" })
		gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", "topic")
		land()
		ts.m.settleMerged()
		if got := summaryOf(t, ts.m, sum.ID); got.Stage != StageActive || !branchExists(t, repo, "topic") {
			t.Fatalf("settled a Thread on a branch uam did not make: %+v", got)
		}
	})
	t.Run("checked out in another worktree", func(t *testing.T) {
		ts, repo, sum, land := mergedThread(t)
		wt, branch := sum.Worktree.Path, sum.Worktree.Branch
		gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", branch)
		land()
		gitIn(t, repo, "worktree", "add", "-q", "--force", filepath.Join(t.TempDir(), "other"), branch)
		ts.m.settleMerged()
		var removed []RemovedWorktree
		waitUntil(t, "removal logged", func() bool {
			removed = ts.m.removedLog()
			return len(removed) == 1
		})
		if !worktreeGone(wt) || removed[0].BranchRemoved || !branchExists(t, repo, branch) || countOf(ts.m, "merged_removed") != 0 {
			t.Fatalf("removed = %+v, branch %v", removed[0], branchExists(t, repo, branch))
		}
	})
	t.Run("moved since the proof", func(t *testing.T) {
		_, repo, sum, _ := mergedThread(t)
		branch := sum.Worktree.Branch
		if removeMergedBranch(t.Context(), repo, branch, gitOutput(t, repo, "rev-parse", "main")) || !branchExists(t, repo, branch) {
			t.Fatal("removed a branch that is not at the proven commit")
		}
		if removeMergedBranch(t.Context(), repo, "main", gitOutput(t, repo, "rev-parse", "main")) || !branchExists(t, repo, "main") {
			t.Fatal("removed a branch uam did not make")
		}
	})
	// A settle by hand in the same Project keeps the branch, as before.
	t.Run("settled by hand", func(t *testing.T) {
		ts, repo, sum, land := mergedThread(t)
		gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", sum.Worktree.Branch)
		land()
		if _, err := ts.m.Settle(sum.ID); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, "worktree removed", func() bool { return worktreeGone(sum.Worktree.Path) })
		ts.m.settleMerged()
		if got := summaryOf(t, ts.m, sum.ID); got.SettledBy != "" || !branchExists(t, repo, sum.Worktree.Branch) {
			t.Fatalf("a Thread settled by hand lost its branch: %+v", got)
		}
	})
}
