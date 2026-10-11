package web

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const receiptTask = "aaaaaaaa-0000-4000-8000-000000000001"

var receiptGate = glabGate{Host: "gitlab.example.com", Path: "group/repo"}

func receiptRepo(t *testing.T) string {
	t.Helper()
	glabTestFake(t, "exit 0")
	repo := branchRepo(t)
	gitIn(t, repo, "remote", "add", "origin", "https://gitlab.example.com/group/repo.git")
	return repo
}

func TestGitLabCommitMarking(t *testing.T) {
	action := gitLabCommitAction(receiptTask, "feature/o'$(touch nope)", receiptGate)
	if strings.ContainsAny(action, " '()$") {
		t.Fatalf("receipt contains shell syntax: %q", action)
	}
	prefix := "GIT_REFLOG_ACTION=" + action + " "
	for _, tc := range []struct{ command, want string }{
		{"git commit -m 'fix: parser'", prefix + "git commit -m 'fix: parser'"},
		{"git add . && git commit -m fix && false", "git add . && " + prefix + "git commit -m fix && false"},
		{"rtk proxy git commit --amend --no-edit", prefix + "rtk proxy git commit --amend --no-edit"},
		{"rtk git commit -m fix; git push", prefix + "rtk git commit -m fix; git push"},
		{"LANG=C git commit -m fix", prefix + "LANG=C git commit -m fix"},
		{"git commit -m first; git commit -m second", prefix + "git commit -m first; " + prefix + "git commit -m second"},
		{"echo 'git commit -m fake'", "echo 'git commit -m fake'"},
		{"git pull; git reset --hard HEAD", "git pull; git reset --hard HEAD"},
		{"git -C /elsewhere commit -m fix", "git -C /elsewhere commit -m fix"},
		{"GIT_DIR=/elsewhere git commit -m fix", "GIT_DIR=/elsewhere git commit -m fix"},
		{"GIT_REFLOG_ACTION=other git commit -m fix", "GIT_REFLOG_ACTION=other git commit -m fix"},
		{"$tool commit -m fix", "$tool commit -m fix"},
		{"git $verb -m fix", "git $verb -m fix"},
		{"./commit.sh", "./commit.sh"},
		{"git() { command git reset --soft HEAD; }; git commit", "git() { command git reset --soft HEAD; }; git commit"},
		{"source helpers.sh; git commit -m fix", "source helpers.sh; git commit -m fix"},
		{"eval setup; git commit -m fix", "eval setup; git commit -m fix"},
		{"git commit -m 'unterminated", "git commit -m 'unterminated"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			if got := markGitLabCommits(tc.command, action); got != tc.want {
				t.Fatalf("marked = %q, want %q", got, tc.want)
			}
		})
	}
	if got := gitLabCommitAction("untrusted $(id)", "main", receiptGate); got != "" {
		t.Fatalf("invalid Task ID received a receipt: %q", got)
	}
}

func TestGitLabReceiptParserExcludesOtherActionsAndBranches(t *testing.T) {
	sha := strings.Repeat("a", 40)
	valid := sha + "\x00" + gitLabCommitAction(receiptTask, "main", receiptGate) + ": fix\n"
	output := valid + strings.Join([]string{
		sha + "\x00commit: " + gitLabCommitAction(receiptTask, "main", receiptGate) + ": user subject",
		sha + "\x00pull: Fast-forward",
		sha + "\x00reset: moving to HEAD",
		sha + "\x00" + gitLabCommitAction(receiptTask, "other", receiptGate) + ": fix",
		"abc123\x00" + gitLabCommitAction(receiptTask, "main", receiptGate) + ": short SHA",
		strings.Repeat("z", 40) + "\x00" + gitLabCommitAction(receiptTask, "main", receiptGate) + ": invalid SHA",
		sha + "\x00uam-commit:foreign:6d61696e: invalid Task",
	}, "\n")
	got := parseGitLabCommitReceipts(output, "main", receiptGate)
	if len(got) != 1 || got[0] != (gitLabCommitReceipt{taskID: receiptTask, sha: sha, branch: "main", origin: gitLabOriginFingerprint(receiptGate)}) {
		t.Fatalf("receipts = %+v", got)
	}
}

func TestGitLabCommitReceiptSurvivesCompoundFailureAndLaterForeignCommit(t *testing.T) {
	gitIdentity(t)
	repo := receiptRepo(t)
	command := markGitLabCommits("git commit -q --allow-empty -m owned && false", gitLabCommitAction(receiptTask, "main", receiptGate))
	cmd := exec.Command("bash", "-c", command) // #nosec G204 -- static test command in an isolated temporary repository.
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("compound command should fail: %s", out)
	}
	owned := gitOutput(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "external")
	gitIn(t, repo, "reset", "--soft", "HEAD")
	got := readGitLabCommitReceipts(context.Background(), repo)
	if len(got) != 1 || got[0].sha != owned || got[0].taskID != receiptTask || got[0].branch != "main" {
		t.Fatalf("receipts = %+v, want owned commit %s", got, owned)
	}
	gitIn(t, repo, "switch", "-q", "-c", "other")
	if got := readGitLabCommitReceipts(context.Background(), repo); len(got) != 0 {
		t.Fatalf("other branch inherited ownership: %+v", got)
	}
	gitIn(t, repo, "switch", "-q", "main")
	if got := readGitLabCommitReceipts(context.Background(), repo); len(got) != 1 || got[0].sha != owned {
		t.Fatalf("receipt lost when returning to branch: %+v", got)
	}
}

func TestGitLabReceiptRejectsBranchSwitchWithinCommand(t *testing.T) {
	gitIdentity(t)
	repo := receiptRepo(t)
	command := markGitLabCommits("git switch -q -c other && git commit -q --allow-empty -m other", gitLabCommitAction(receiptTask, "main", receiptGate))
	cmd := exec.Command("bash", "-c", command) // #nosec G204 -- static test command in an isolated temporary repository.
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compound command: %v %s", err, out)
	}
	if got := readGitLabCommitReceipts(context.Background(), repo); len(got) != 0 {
		t.Fatalf("commit was attributed to branch from before the switch: %+v", got)
	}
}

func TestGitLabUICommitHasRecoverableReceipt(t *testing.T) {
	gitIdentity(t)
	repo := receiptRepo(t)
	g := newGitTask(t, repo)
	writeRepoFile(t, repo, "file.txt", "content\n")
	if _, err := g.m.GitCommit(context.Background(), g.id, []string{"file.txt"}, "feat: owned"); err != nil {
		t.Fatal(err)
	}
	got := readGitLabCommitReceipts(context.Background(), repo)
	if len(got) != 1 || got[0].taskID != g.id || got[0].sha != gitOutput(t, repo, "rev-parse", "HEAD") {
		t.Fatalf("UI commit receipt = %+v", got)
	}
}

func TestGitLabStampPreservesArgumentsAndExcludesOtherWorkdirs(t *testing.T) {
	repo := receiptRepo(t)
	g := newGitTask(t, repo)
	g.m.mu.Lock()
	s := g.m.sessions[g.id]
	g.m.mu.Unlock()
	use := agentapi.ToolUse{Tool: "bash", Workdir: repo, Args: map[string]any{"command": "git commit -m fix", "timeout": 1000}}
	got := g.m.stampGitLabCommit(context.Background(), s, use)
	if got["timeout"] != 1000 || !strings.HasPrefix(got["command"].(string), "GIT_REFLOG_ACTION=") || use.Args["command"] != "git commit -m fix" {
		t.Fatalf("args = %+v, original = %+v", got, use.Args)
	}
	use.Workdir = t.TempDir()
	if got := g.m.stampGitLabCommit(context.Background(), s, use); got != nil {
		t.Fatalf("other workdir marked: %+v", got)
	}
}

func TestGitLabReceiptBindsOriginWithoutCredentials(t *testing.T) {
	gitIdentity(t)
	repo := receiptRepo(t)
	action := gitLabProvenanceAction(t.Context(), repo, receiptTask)
	if action == "" {
		t.Fatal("GitLab origin was not stamped")
	}
	if _, err := runGitWriteEnv(t.Context(), repo, nil, []string{"GIT_REFLOG_ACTION=" + action}, "commit", "--allow-empty", "-m", "owned"); err != nil {
		t.Fatal(err)
	}
	// Preserve the exact origin read with the receipt through delivery. A
	// remote change between this read and persistence must not retarget it.
	read := readGitLabCommitReceipts(t.Context(), repo)
	if len(read) != 1 || read[0].origin != gitLabOriginFingerprint(receiptGate) {
		t.Fatalf("receipt lost its origin: %+v", read)
	}
	for _, remote := range []string{
		"https://user:fixture-secret@gitlab.example.com/group/repo.git",
		"git@gitlab.example.com:group/repo.git",
	} {
		gitIn(t, repo, "remote", "set-url", "origin", remote)
		if got := gitLabProvenanceAction(t.Context(), repo, receiptTask); got != action {
			t.Fatalf("equivalent origin changed the receipt: %q", got)
		}
		if got := readGitLabCommitReceipts(t.Context(), repo); len(got) != 1 {
			t.Fatalf("equivalent origin lost receipt: %+v", got)
		}
	}
	for _, remote := range []string{
		"https://other.example.com/group/repo.git",
		"https://gitlab.example.com/group/other.git",
	} {
		gitIn(t, repo, "remote", "set-url", "origin", remote)
		gate, ok := glabOrigin(t.Context(), repo)
		if !ok || read[0].origin == gitLabOriginFingerprint(gate) {
			t.Fatalf("previously read receipt adopted a changed origin: %+v", read)
		}
		if got := readGitLabCommitReceipts(t.Context(), repo); len(got) != 0 {
			t.Fatalf("receipt rebound to another origin: %+v", got)
		}
	}
}

func TestGitLabProvenanceRequiresGitLabAndGlab(t *testing.T) {
	repo := receiptRepo(t)
	gitIn(t, repo, "remote", "set-url", "origin", "https://github.com/group/repo.git")
	if got := gitLabProvenanceAction(t.Context(), repo, receiptTask); got != "" {
		t.Fatalf("GitHub commit marked: %q", got)
	}
	gitIn(t, repo, "remote", "set-url", "origin", "https://gitlab.example.com/group/repo.git")
	t.Setenv("PATH", t.TempDir())
	if got := gitLabProvenanceAction(t.Context(), repo, receiptTask); got != "" {
		t.Fatalf("commit marked without glab: %q (PATH=%q)", got, os.Getenv("PATH"))
	}
}
