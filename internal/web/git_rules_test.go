package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestGitRules(t *testing.T) {
	rules, provider := Settings{}.gitRules()
	if provider || !strings.HasPrefix(rules, "Commit messages:\n"+defaultCommitPrompt+"\n\nPull and merge requests:\n"+defaultMRPrompt+"\n\n") || !strings.Contains(rules, "no Co-authored-by trailer") {
		t.Fatalf("defaults = %v %q", provider, rules)
	}
	rules, provider = Settings{CommitPrompt: "One line.", GitLabMRPrompt: "Two lines.", CoAuthor: store.WebCoAuthor{Enabled: true, Name: "Ada <ada@example.com>"}}.gitRules()
	if provider || !strings.HasPrefix(rules, "Commit messages:\nOne line.\n\nPull and merge requests:\nTwo lines.\n\n") || !strings.Contains(rules, `"Co-authored-by: Ada <ada@example.com>"`) {
		t.Fatalf("named = %v %q", provider, rules)
	}
	// Without a name the provider adds its own trailer.
	rules, provider = Settings{CoAuthor: store.WebCoAuthor{Enabled: true}}.gitRules()
	if !provider || strings.Contains(rules, "Co-authored-by") {
		t.Fatalf("provider's = %v %q", provider, rules)
	}
}

func TestGitRulesSettings(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	if !slices.Contains(ts.srv.federationDescriptor().Capabilities, "git-rules-v1") {
		t.Fatal("connected instances cannot tell the settings are offered")
	}
	for _, body := range []string{`{"commit_prompt":null}`, `{"commit_prompt":3}`, `{"commit_prompt":"` + strings.Repeat("é", maxMRPromptRunes+1) + `"}`,
		`{"coauthor":null}`, `{"coauthor":"Ada"}`, `{"coauthor":{"enabled":true,"name":"Ada\nSigned-off-by: x"}}`, `{"coauthor":{"name":"` + strings.Repeat("a", maxCoAuthorRunes+1) + `"}}`} {
		if w := ts.do(http.MethodPatch, "/api/settings", body, withCookie(ts)); w.Code != http.StatusBadRequest {
			t.Fatalf("%.40s = %d %s", body, w.Code, w.Body)
		}
	}
	if w := ts.do(http.MethodPatch, "/api/settings", `{"commit_prompt":" One line. ","coauthor":{"enabled":true,"name":" Ada <ada@example.com> "}}`, withCookie(ts)); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"commit_prompt":"One line."`) || !strings.Contains(w.Body.String(), `"coauthor":{"enabled":true,"name":"Ada \u003cada@example.com\u003e"}`) {
		t.Fatalf("set = %d %s", w.Code, w.Body)
	}
	cfg, err := ts.m.store.Load()
	if err != nil || cfg.WebSettings.CommitPrompt != "One line." || cfg.WebSettings.CoAuthor != (store.WebCoAuthor{Enabled: true, Name: "Ada <ada@example.com>"}) {
		t.Fatalf("stored=%+v err=%v", cfg.WebSettings, err)
	}
	// The default itself, or nothing, puts the default back; off is omitted.
	quoted, err := json.Marshal(defaultCommitPrompt)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"commit_prompt":"","coauthor":{}}`, `{"commit_prompt":` + string(quoted) + `,"coauthor":{"enabled":false}}`} {
		if _, err := ts.m.UpdateSettings(SettingsPatch{CommitPrompt: new("One line."), CoAuthor: &store.WebCoAuthor{Enabled: true}}); err != nil {
			t.Fatal(err)
		}
		if w := ts.do(http.MethodPatch, "/api/settings", body, withCookie(ts)); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"commit_prompt":`) || strings.Contains(w.Body.String(), `"coauthor":`) {
			t.Fatalf("reset = %d %s", w.Code, w.Body)
		}
	}
}

// The browser shows the default as the instructions' placeholder; its copy must match.
func TestCommitPromptDefaultMirroredInUI(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "web", "packages", "ui", "src", "api.ts"))
	if err != nil {
		t.Skip(err)
	}
	if !strings.Contains(string(data), "export const DEFAULT_COMMIT_PROMPT = `"+defaultCommitPrompt+"`;") {
		t.Fatal("DEFAULT_COMMIT_PROMPT in api.ts differs from defaultCommitPrompt")
	}
}
