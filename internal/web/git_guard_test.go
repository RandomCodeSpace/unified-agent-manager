package web

import (
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestGitGuard(t *testing.T) {
	denied := []string{
		"git add -A",
		"git add .",
		"git add --all && git commit -m x",
		"git -C ../other add -Av",
		"cd repo; git add *",
		"git reset --hard origin/main",
		"/usr/bin/git reset --hard",
		"git push -f",
		"git push --force origin HEAD",
		"git push -uf origin HEAD",
		"git push origin +HEAD:main",
		"git push --force --force-with-lease",
		"git stash",
		"git stash -u",
		"git stash push",
		"git stash pop",
		"git stash pop stash@{1}",
		"git stash apply",
		"git stash drop",
		"git stash clear",
		"git commit --no-verify -m x",
		"git commit -n -m x",
		"git push --no-verify",
		"echo $(git stash)",
		"for b in a b; do git push -f origin $b; done",
	}
	for _, command := range denied {
		if got := gitGuardToolUse(agentapi.ToolUse{Tool: "bash", Args: map[string]any{"command": command}}); !strings.HasPrefix(got, gitGuardLead) {
			t.Errorf("%q not refused", command)
		}
	}
	allowed := []string{
		"git add internal/web/git_guard.go README.md",
		"git add -p file.go",
		"git commit -m 'never git add -A or git push -f'",
		"git commit -am 'fix: say -A is refused'",
		"git push -u origin HEAD",
		"git push --force-with-lease origin HEAD",
		"git push --force-with-lease=main:abc123 origin main",
		"git reset --soft HEAD~1",
		"git reset HEAD file.go",
		"git stash push -u -m t3-tag",
		"git stash push --message=t3-tag",
		"git stash list",
		"git stash show -p stash@{0}",
		"git stash apply 3f2a1b",
		"git stash drop stash@{2}",
		"git log --oneline -5 && git status -s",
		"echo git add -A",
		"grep -n 'git push -f' notes.md",
		"gh pr merge 12 --squash",
	}
	for _, command := range allowed {
		if got := gitGuardToolUse(agentapi.ToolUse{Tool: "bash", Args: map[string]any{"command": command}}); got != "" {
			t.Errorf("%q refused: %s", command, got)
		}
	}
	if got := gitGuardToolUse(agentapi.ToolUse{Tool: "edit", Args: map[string]any{"command": "git add -A"}}); got != "" {
		t.Errorf("non-shell tool refused: %s", got)
	}

	s := &webSession{id: "s"}
	m := &Manager{sessions: map[string]*webSession{"s": s}}
	got := m.preToolUse(t.Context(), s, agentapi.ToolUse{Tool: "bash", Args: map[string]any{"command": "git reset --hard"}})
	if !strings.HasPrefix(got.Deny, gitGuardLead) {
		t.Fatalf("preToolUse did not refuse: %+v", got)
	}
}
