package web

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestGlabManagerDispatch(t *testing.T) {
	s := &webSession{id: "fixture"}
	dir := t.TempDir()
	m := &Manager{sessions: map[string]*webSession{s.id: s}, now: time.Now}
	m.glab.dirs = map[string]glabCachedGate{dir: {gate: glabGate{Dir: dir}, ok: true, at: time.Now()}}
	use := agentapi.ToolUse{Tool: "bash", Workdir: dir, Args: map[string]any{"command": "glab issue list"}}
	if got := m.preToolUse(t.Context(), s, use); got.Args["command"] != "glab issue list -P 10" {
		t.Fatalf("warm hook=%+v", got)
	}
	s.fuses = map[fuseKey]*fuseEntry{{kind: "permission", key: "shell"}: {count: 2}}
	if got := m.preToolUse(t.Context(), s, use); got.Deny == "" || got.Args != nil {
		t.Fatalf("first deny lost: %+v", got)
	}
	s.fuses = nil
	m.glab.dirs[dir] = glabCachedGate{at: time.Now()}
	if got := m.preToolUse(t.Context(), s, use); got.Args != nil || got.Deny != "" {
		t.Fatalf("disabled gate=%+v", got)
	}
}

func TestGlabShellRules(t *testing.T) {
	for _, tc := range []struct{ command, want, deny string }{
		{"glab api projects", "", "glab ci get"},
		{"glab -R group/repo api projects", "", "glab ci get"},
		{"rtk glab search x", "", "glab issue list"},
		{"rtk proxy glab issue list --paginate", "", "-P 10"},
		{"glab issue list -A", "", "-p <page>"},
		{"glab mr list --all", "", "-P 10"},
		{"glab auth login", "", "outside the Task"},
		{"glab ci view", "", "glab ci get"},
		{"glab ci status --live", "", "without --live"},
		{"glab ci trace -p 42", "", "<job-id>"},
		{"glab ci retry --pipeline-id 42", "", "<job-id>"},
		{"glab ci trace --branch main", "", "<job-id>"},
		{"glab mr create --title example", "", "-y or -f"},
		{"glab issue list", "glab issue list -P 10", ""},
		{"glab --repo=group/repo issue list", "glab --repo=group/repo issue list -P 10", ""},
		{"glab -Rgroup/repo issue list -P 50", "glab -Rgroup/repo issue list -P 20", ""},
		{"rtk proxy glab mr list -P 50", "rtk proxy glab mr list -P 20", ""},
		{"glab ci list --per-page=80", "glab ci list --per-page=20", ""},
		{"glab incident list -P99", "glab incident list -P20", ""},
		{"glab issue list -P 5 -P 70", "glab issue list -P 5 -P 20", ""},
		{"glab issue list -P '70'", "glab issue list -P 20", ""},
		{"glab issue list -P 4", "", ""},
		{"glab ci trace lint -p 42", "", ""},
		{"glab ci trace \"lint\" -p 42", "", ""},
		{"glab ci retry 17 -p 42", "", ""},
		{"glab mr create -y", "", ""},
		{"glab mr create --fill", "", ""},
		{"glab mr note list", "", ""},
		{"for i in 1 2; do glab issue list; done", "", ""},
		{"(glab issue list)", "", ""},
		{"(glab api projects)", "", "glab ci get"},
		{"echo glab api projects", "", ""},
		{"other-glab issue list", "", ""},
		{"glab issue list | head -5", "glab issue list -P 10 | head -5", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			args := map[string]any{"command": tc.command, "description": "kept", "timeout": 1000}
			got := glabCommand(t.Context(), glabGate{}, tc.command, args)
			if (got.Deny == "") != (tc.deny == "") || !strings.Contains(got.Deny, tc.deny) {
				t.Fatalf("deny=%q want=%q", got.Deny, tc.deny)
			}
			if tc.want == "" {
				if got.Args != nil {
					t.Fatalf("unexpected rewrite=%v", got.Args)
				}
				return
			}
			if got.Args["command"] != tc.want || got.Args["description"] != "kept" || got.Args["timeout"] != 1000 || got.Context == "" || args["command"] != tc.command {
				t.Fatalf("rewrite=%+v", got)
			}
		})
	}
}

func TestGlabLintPush(t *testing.T) {
	for _, tc := range []struct {
		name, output          string
		changed, remote, deny bool
	}{
		{"no-ci", "./.gitlab-ci.yml is invalid.\n1 scrpt", false, true, false},
		{"invalid", "./.gitlab-ci.yml is invalid.\n1 jobs:test unknown keys: scrpt", true, true, true},
		{"static", "./.gitlab-ci.yml is invalid.\n1 jobs:test unknown keys: scrpt", true, false, true},
		{"include", "./.gitlab-ci.yml is invalid.\n1 include project not found", true, true, false},
		{"access", "./.gitlab-ci.yml is invalid.\n1 include access denied", true, true, false},
		{"api-error", "HTTP 503 service unavailable", true, true, false},
		{"valid", "CI/CD YAML is valid!", true, true, false},
		{"timeout", "", true, true, false},
		{"bounded", "./.gitlab-ci.yml is invalid.\n" + strings.Repeat("diagnostic\n", 25), true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := glabTestFake(t, "printf '%s\\n' \"$@\" > \"$0.args\"\nprintf '%s\\n' '"+tc.output+"'\nexit 1")
			if tc.name == "timeout" {
				glabTestWrite(t, bin, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$0.args\"\nexec sleep 2")
			}
			dir := glabTestRepo(t, "https://gitlab.com/group/scratch.git")
			glabTestWrite(t, filepath.Join(dir, "README"), "base")
			glabTestGit(t, dir, "add", ".")
			glabTestGit(t, dir, "commit", "-m", "base")
			base := glabTestGit(t, dir, "rev-parse", "HEAD")
			glabTestGit(t, dir, "update-ref", "refs/remotes/origin/main", base)
			glabTestGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
			if tc.name == "invalid" {
				glabTestGit(t, dir, "config", "branch.main.remote", "origin")
				glabTestGit(t, dir, "config", "branch.main.merge", "refs/heads/main")
			}
			if !tc.remote {
				glabTestGit(t, dir, "switch", "-c", "new-branch")
			}
			file := "README"
			if tc.changed {
				file = ".gitlab-ci.yml"
			}
			glabTestWrite(t, filepath.Join(dir, file), "test:\n  scrpt: echo fixture\n")
			glabTestGit(t, dir, "add", ".")
			glabTestGit(t, dir, "commit", "-m", "change")
			gate := glabGate{Dir: dir, Host: "gitlab.com", Path: "group/scratch", executable: bin}
			checkCtx := t.Context()
			if tc.name == "timeout" {
				var cancel context.CancelFunc
				checkCtx, cancel = context.WithTimeout(checkCtx, 300*time.Millisecond)
				defer cancel()
			}
			got := glabCommand(checkCtx, gate, "git push --dry-run", map[string]any{"command": "git push --dry-run"})
			if (got.Deny != "") != tc.deny {
				t.Fatalf("verdict=%+v", got)
			}
			if tc.name == "bounded" && strings.Count(got.Deny, "diagnostic") != 20 {
				t.Fatalf("unbounded detail=%q", got.Deny)
			}
			argv, err := os.ReadFile(bin + ".args")
			if !tc.changed {
				if err == nil {
					t.Fatal("lint ran without CI changes")
				}
				return
			}
			if err != nil || !strings.Contains(string(argv), "./.gitlab-ci.yml") || strings.Contains(string(argv), "--dry-run") != tc.remote {
				t.Fatalf("argv=%s err=%v", argv, err)
			}
			if !tc.remote && !strings.Contains(got.Context, "static only") {
				t.Fatal("missing static note")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if got := glabLintPush(ctx, gate); got.Deny != "" {
				t.Fatalf("canceled lint denied: %+v", got)
			}
		})
	}
}
