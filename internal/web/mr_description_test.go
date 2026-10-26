package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestGlabMRCreateRewrite(t *testing.T) {
	const file = "/tmp/uam mr.md"
	const flag = " --description-file '/tmp/uam mr.md'"
	for _, tc := range []struct{ command, want, target string }{
		{"glab mr create -f -y", "glab mr create -f -y" + flag, ""},
		{"rtk glab mr create --fill --yes -b develop", "rtk glab mr create --fill --yes -b develop" + flag, "develop"},
		{"glab mr create -f -y --target-branch=release -l bug", "glab mr create -f -y --target-branch=release -l bug" + flag, "release"},
		{"glab mr create -y -t 'Fix it' -bmain", "glab mr create -y -t 'Fix it' -bmain" + flag, "main"},
		// glab refuses --title with a description beside --fill.
		{"glab mr create -f -y --title 'Fix it'", "glab mr create --push -y --title 'Fix it'" + flag, ""},
		{"glab mr create -f --fill-commit-body -y -t T", "glab mr create --push  -y -t T" + flag, ""},
		{"glab mr create -f -y && echo ok", "glab mr create -f -y" + flag + " && echo ok", ""},
		{"glab mr create -f -y - x", "glab mr create -f -y - x" + flag, ""},
		// Left alone: the agent's own description or template, another
		// branch or repository, bundled --fill beside a title, a loop.
		{"glab mr create -f -y -d 'Own words'", "", ""},
		{"glab mr create -y -t T --description=own", "", ""},
		{"glab mr create -f -y --description-file own.md", "", ""},
		{"glab mr create -y -t T --template bug_fix", "", ""},
		{"glab mr create -f -y -s other", "", ""},
		{"glab -R group/other mr create -f -y", "", ""},
		{"glab mr create -fy -t T", "", ""},
		{"glab mr create --fill=true -y -t T", "", ""},
		{"glab mr create -f -y -b", "", ""},
		{"glab mr create -f -y -b \"$BASE\"", "", ""},
		{"for i in 1; do glab mr create -f -y; done", "", ""},
		{"glab mr list", "glab mr list -P 10", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			var asked []string
			describe := func(_ context.Context, target string) (string, error) {
				asked = append(asked, target)
				return file, nil
			}
			got := glabCommand(t.Context(), glabGate{}, tc.command, map[string]any{"command": tc.command}, describe)
			if tc.want == "" {
				if got.Args != nil || len(asked) != 0 {
					t.Fatalf("rewrite=%+v asked=%v", got, asked)
				}
				return
			}
			if got.Args["command"] != tc.want {
				t.Fatalf("command=%q want %q", got.Args["command"], tc.want)
			}
			if strings.Contains(tc.want, flag) && (len(asked) != 1 || asked[0] != tc.target || !strings.Contains(got.Context, "Utility model")) {
				t.Fatalf("asked=%v context=%q", asked, got.Context)
			}
			if strings.Contains(got.Context, "capped") != strings.Contains(tc.command, "list") {
				t.Fatalf("context=%q", got.Context)
			}
		})
	}
	got := glabCommand(t.Context(), glabGate{}, "glab mr create -f -y", map[string]any{"command": "glab mr create -f -y"}, func(context.Context, string) (string, error) {
		return "", errors.New("Background AI is off")
	})
	if got.Args != nil || got.Deny != "" || !strings.Contains(got.Context, "could not write") || !strings.Contains(got.Context, "Background AI is off") {
		t.Fatalf("failed describe=%+v", got)
	}
}

// mrRepo is a repository on branch feature, one commit ahead of origin's
// default branch main, with a default merge request template.
func mrRepo(t *testing.T) string {
	t.Helper()
	gitIdentity(t)
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	gitIn(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitIn(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitIn(t, dir, "switch", "-q", "-c", "feature")
	writeRepoFile(t, dir, "parser.go", "package parser\n")
	writeRepoFile(t, dir, ".gitlab/merge_request_templates/Default.md", "## What\n\n## Why\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "feat: add the parser", "-m", "Covered by go test ./parser.")
	return dir
}

func TestMRDescriptionHook(t *testing.T) {
	repo := mrRepo(t)
	g := newGitTask(t, repo)
	// Let any background gate check finish before standing in a GitLab gate.
	g.m.glab.gate(t.Context(), repo)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		g.m.glab.mu.Lock()
		e := g.m.glab.dirs[repo]
		g.m.glab.mu.Unlock()
		if !e.loading && !e.at.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the gate check did not finish")
		}
	}
	g.m.glab.mu.Lock()
	g.m.glab.dirs = map[string]glabCachedGate{repo: {gate: glabGate{Dir: repo}, ok: true, at: time.Now()}}
	g.m.glab.mu.Unlock()
	g.m.mu.Lock()
	s := g.m.sessions[g.id]
	g.m.mu.Unlock()
	use := func(command string) agentapi.ToolUse {
		return agentapi.ToolUse{Tool: "bash", Workdir: repo, Args: map[string]any{"command": command}}
	}
	g.prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) {
		return "```markdown\n## What\n\nAdds the parser.\n\nGenerated with GitHub Copilot AI\n```", nil
	})

	got := g.m.preToolUse(t.Context(), s, use("glab mr create -f -y"))
	command, _ := got.Args["command"].(string)
	path := strings.TrimPrefix(command, "glab mr create -f -y --description-file ")
	if path == command || !regexp.MustCompile(`^/\S+uam-mr-description-\d+\.md$`).MatchString(path) || !strings.Contains(got.Context, "Utility model") {
		t.Fatalf("verdict=%+v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "## What\n\nAdds the parser.\n" {
		t.Fatalf("description=%q err=%v", data, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode=%v err=%v", info, err)
	}
	reqs := g.prov.UtilityRequests()
	if len(reqs) != 1 || reqs[0].Purpose != purposeMRDescription || reqs[0].Model != "b" || !strings.HasPrefix(reqs[0].System, mrDescriptionTask+defaultMRPrompt) || !strings.Contains(reqs[0].System, "untrusted") {
		t.Fatalf("requests=%+v", reqs)
	}
	for _, part := range []string{"<thread_title>Fix the parser</thread_title>", "<target_branch>main</target_branch>", "feat: add the parser\nCovered by go test ./parser.", "parser.go", "<template>\n## What\n\n## Why\n</template>"} {
		if !strings.Contains(reqs[0].Prompt, part) {
			t.Fatalf("prompt %q lacks %q", reqs[0].Prompt, part)
		}
	}
	if calls := g.m.UtilityLog(0, 10).Calls; len(calls) != 1 || calls[0].Purpose != purposeMRDescription || calls[0].TaskID != g.id {
		t.Fatalf("utility log=%+v", calls)
	}
	// The finished call removes its file, whichever form of the command it reports.
	g.m.succeededToolUse(t.Context(), s, use("glab mr create -f -y"))
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file kept after the call: %v", err)
	}

	// The agent's own description is kept, without a model call.
	if got := g.m.preToolUse(t.Context(), s, use("glab mr create -f -y -d 'Mine'")); got.Args != nil || len(g.prov.UtilityRequests()) != 1 {
		t.Fatalf("own description=%+v", got)
	}
	// The owner's prompt replaces the default; the fixed rules stay.
	if w := g.ts.do(http.MethodPatch, "/api/settings", `{"gitlab_mr_prompt":"Write one short paragraph."}`, withCookie(g.ts)); w.Code != http.StatusOK {
		t.Fatalf("settings = %d %s", w.Code, w.Body)
	}
	got = g.m.preToolUse(t.Context(), s, use("glab mr create -f -y -b main"))
	if reqs := g.prov.UtilityRequests(); got.Args == nil || len(reqs) != 2 || !strings.HasPrefix(reqs[1].System, mrDescriptionTask+"Write one short paragraph.\n") || !strings.Contains(reqs[1].System, "untrusted") {
		t.Fatalf("custom prompt verdict=%+v", got)
	}
	// A model failure, or Background AI off, leaves the command alone and says so.
	g.prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return "", errors.New("model gone") })
	if got := g.m.preToolUse(t.Context(), s, use("glab mr create -f -y")); got.Args != nil || !strings.Contains(got.Context, "model gone") {
		t.Fatalf("failed model verdict=%+v", got)
	}
	if w := g.ts.do(http.MethodPatch, "/api/settings", `{"utility_daily_limit":0}`, withCookie(g.ts)); w.Code != http.StatusOK {
		t.Fatalf("settings = %d %s", w.Code, w.Body)
	}
	if got := g.m.preToolUse(t.Context(), s, use("glab mr create -f -y")); got.Args != nil || !strings.Contains(got.Context, "Background AI is off") {
		t.Fatalf("paused verdict=%+v", got)
	}
	// A target branch that does not exist is reported without a model call.
	n := len(g.prov.UtilityRequests())
	if got := g.m.preToolUse(t.Context(), s, use("glab mr create -f -y -b nowhere")); got.Args != nil || !strings.Contains(got.Context, "nowhere") || len(g.prov.UtilityRequests()) != n {
		t.Fatalf("unknown target verdict=%+v", got)
	}
}

func TestMRDescriptionTemplateStaysInRepo(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".gitlab/merge_request_templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, mrTemplatePath)); err != nil {
		t.Skip(err)
	}
	if got := readMRTemplate(repo); got != "" {
		t.Fatalf("template followed a link out of the repository: %q", got)
	}
}

func TestMRPromptSetting(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	if !slices.Contains(ts.srv.federationDescriptor().Capabilities, "gitlab-mr-prompt-v1") {
		t.Fatal("connected instances cannot tell the setting is offered")
	}
	for _, body := range []string{`{"gitlab_mr_prompt":null}`, `{"gitlab_mr_prompt":3}`, `{"gitlab_mr_prompt":"` + strings.Repeat("é", maxMRPromptRunes+1) + `"}`} {
		if w := ts.do(http.MethodPatch, "/api/settings", body, withCookie(ts)); w.Code != http.StatusBadRequest {
			t.Fatalf("%.40s = %d %s", body, w.Code, w.Body)
		}
	}
	if w := ts.do(http.MethodPatch, "/api/settings", `{"gitlab_mr_prompt":"  `+strings.Repeat("é", maxMRPromptRunes)+`  "}`, withCookie(ts)); w.Code != http.StatusOK {
		t.Fatalf("longest prompt = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/settings", `{"gitlab_mr_prompt":"Be brief."}`, withCookie(ts)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"gitlab_mr_prompt":"Be brief."`) {
		t.Fatalf("set = %d %s", w.Code, w.Body)
	}
	cfg, err := ts.m.store.Load()
	if err != nil || cfg.WebSettings.GitLabMRPrompt != "Be brief." {
		t.Fatalf("stored=%q err=%v", cfg.WebSettings.GitLabMRPrompt, err)
	}
	// The default itself, or nothing, puts the default back.
	quoted, err := json.Marshal(defaultMRPrompt)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"gitlab_mr_prompt":""}`, `{"gitlab_mr_prompt":` + string(quoted) + `}`} {
		if _, err := ts.m.UpdateSettings(SettingsPatch{GitLabMRPrompt: new("Be brief.")}); err != nil {
			t.Fatal(err)
		}
		if w := ts.do(http.MethodPatch, "/api/settings", body, withCookie(ts)); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"gitlab_mr_prompt":`) {
			t.Fatalf("reset = %d %s", w.Code, w.Body)
		}
	}
}

// The browser shows the default as the prompt's placeholder; its copy must match.
func TestMRPromptDefaultMirroredInUI(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "web", "packages", "ui", "src", "api.ts"))
	if err != nil {
		t.Skip(err)
	}
	if !strings.Contains(string(data), "export const DEFAULT_MR_PROMPT = `"+defaultMRPrompt+"`;") {
		t.Fatal("DEFAULT_MR_PROMPT in api.ts differs from defaultMRPrompt")
	}
}
