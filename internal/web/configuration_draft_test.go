package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const validAgentDraft = `{"name":"review-helper","description":"Review changes","prompt":"Inspect the proposed changes.","model":"available","tools":[]}`
const validSkillDraft = `{"name":"review-helper","description":"Use for focused reviews","prompt":"Inspect the proposed changes."}`
const validHookDraft = `{"name":"review-helper","event":"preToolUse","bash":"printf ready","powershell":"Write-Output ready","cwd":".","timeout_sec":10,"env":{"MODE":"review"}}`

func newConfigurationDraftServer(t *testing.T) *testServer {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	prov := agenttest.NewProvider("copilot", utilityCaps)
	prov.SetModels([]agentapi.Model{{ID: "auto"}, {ID: "available"}, {ID: "hidden"}, {ID: "custom/no-key"}}, nil)
	m := startManager(t, openTestStore(t), prov)
	m.mu.Lock()
	m.settings.Terminal = true
	m.skillDirs = nil
	m.settings.TitleModel = map[string]string{"copilot": "available"}
	m.settings.HiddenModels = map[string][]string{"copilot": {"hidden"}}
	m.settings.CustomModels = []CustomModel{{Name: "custom", ModelID: "no-key", KeyPresent: false}}
	m.projects["project"] = &Project{ID: "project", Dir: filepath.Join(t.TempDir(), "unreadable-project")}
	m.mu.Unlock()
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test", Assets: frameAssets()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), ".copilot"))
	return &testServer{srv: srv, m: m, prov: prov}
}

func TestConfigurationDraftKindsAreEditableAndDoNotWrite(t *testing.T) {
	for _, tc := range []struct{ kind, reply string }{{"agents", validAgentDraft}, {"skills", validSkillDraft}, {"hooks", validHookDraft}} {
		for _, projectID := range []string{"", "project"} {
			t.Run(tc.kind+"/"+projectID, func(t *testing.T) {
				ts := newConfigurationDraftServer(t)
				ts.prov.SetUtilityHook(func(ctx context.Context, req agentapi.UtilityRequest) (string, error) {
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > configurationDraftTimeout || time.Until(deadline) < configurationDraftTimeout-time.Second {
						t.Errorf("utility deadline = %v, %v", deadline, ok)
					}
					if req.Model != "available" || req.Purpose != purposeConfigurationDraft || req.Timeout != configurationDraftTimeout || req.Workdir != "" || len(req.Tools) != 0 || req.CallTool != nil || len(req.Attachments) != 0 {
						t.Errorf("unexpected utility request: %+v", req)
					}
					if !strings.Contains(req.Prompt, "Create a review helper") || strings.Contains(req.Prompt+req.System, "unreadable-project") {
						t.Error("request does not contain only the brief and configuration rules")
					}
					if tc.kind == "agents" && (!strings.Contains(req.System, `["available"]`) || strings.Contains(req.System, `"hidden"`) || strings.Contains(req.System, `"auto"`) || strings.Contains(req.System, "custom/no-key")) {
						t.Error("agent draft was offered unavailable models")
					}
					req.OnUsage(agentapi.UtilityUsage{InputTokens: 10, OutputTokens: 20})
					return tc.reply, nil
				})
				w := ts.do(http.MethodPost, "/api/configuration/"+tc.kind+"/draft?project_id="+projectID, `{"brief":"Create a review helper"}`, withCookie(ts))
				if w.Code != http.StatusOK {
					t.Fatalf("draft = %d %s", w.Code, w.Body.String())
				}
				var draft ConfigurationDraft
				if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil || draft.Name != "review-helper" || draft.Provider != "copilot" || draft.UtilityModel != "available" {
					t.Fatalf("draft = %+v, %v", draft, err)
				}
				if tc.kind == "agents" && (draft.Tools == nil || len(*draft.Tools) != 0 || !strings.Contains(w.Body.String(), `"tools":[]`)) {
					t.Fatal("explicitly disabled tools must remain an empty array")
				}
				if tc.kind == "skills" && draft.Tools != nil {
					t.Fatal("unspecified tools must remain absent")
				}
				for _, root := range []string{os.Getenv("COPILOT_HOME"), ts.m.projects["project"].Dir} {
					if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("draft created configuration root %s: %v", root, err)
					}
				}
				calls := ts.m.UtilityLog(0, 10).Calls
				if len(calls) != 1 || calls[0].Purpose != purposeConfigurationDraft || calls[0].ProjectID != projectID || calls[0].InputTokens != 10 || calls[0].OutputTokens != 20 || calls[0].Outcome != utilityOK {
					t.Fatalf("utility log = %+v", calls)
				}
				if len(ts.m.sessions) != 0 {
					t.Fatal("drafting must not create a task")
				}
			})
		}
	}
}

func TestConfigurationDraftRejectsMalformedReplies(t *testing.T) {
	for _, tc := range []struct{ name, kind, reply string }{
		{"fence", "skills", "```json\n" + validSkillDraft + "\n```"},
		{"trailing object", "skills", validSkillDraft + `{}`},
		{"trailing text", "skills", validSkillDraft + " done"},
		{"array", "skills", "[" + validSkillDraft + "]"},
		{"unknown field", "skills", strings.Replace(validSkillDraft, `"name":`, `"unexpected":true,"name":`, 1)},
		{"missing instructions", "skills", `{"name":"review","description":"Review"}`},
		{"empty description", "skills", `{"name":"review","description":" ","prompt":"Review"}`},
		{"unsafe name", "skills", strings.Replace(validSkillDraft, "review-helper", "../review", 1)},
		{"uppercase name", "skills", strings.Replace(validSkillDraft, "review-helper", "Review", 1)},
		{"long name", "skills", strings.Replace(validSkillDraft, "review-helper", strings.Repeat("a", 65), 1)},
		{"wrong kind fields", "skills", validAgentDraft},
		{"spoofed provider", "skills", strings.Replace(validSkillDraft, `"name":`, `"provider":"copilot","name":`, 1)},
		{"too large", "skills", strings.Repeat(" ", maxConfigurationDraft+1)},
		{"invalid UTF8", "skills", string([]byte{0xff})},
		{"long description", "skills", strings.Replace(validSkillDraft, "Use for focused reviews", strings.Repeat("a", 1025), 1)},
		{"long instructions", "skills", strings.Replace(validSkillDraft, "Inspect the proposed changes.", strings.Repeat("a", 32<<10+1), 1)},
		{"empty tool", "agents", strings.Replace(validAgentDraft, `"tools":[]`, `"tools":[""]`, 1)},
		{"unknown hook event", "hooks", strings.Replace(validHookDraft, "preToolUse", "unknown", 1)},
		{"no hook command", "hooks", `{"name":"review","event":"preToolUse"}`},
		{"zero hook timeout", "hooks", strings.Replace(validHookDraft, `"timeout_sec":10`, `"timeout_sec":0`, 1)},
		{"large hook timeout", "hooks", strings.Replace(validHookDraft, `"timeout_sec":10`, `"timeout_sec":601`, 1)},
		{"invalid hook env", "hooks", strings.Replace(validHookDraft, `"MODE":"review"`, `"":"review"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseConfigurationDraft(tc.kind, tc.reply, nil); err == nil {
				t.Fatal("invalid draft accepted")
			}
		})
	}
}

func TestConfigurationDraftRevalidatesAgentModel(t *testing.T) {
	for _, name := range []string{"auto", "unknown", "hidden", "custom/no-key", "disappeared", "newly hidden", "provider unavailable", "inherit"} {
		t.Run(name, func(t *testing.T) {
			ts := newConfigurationDraftServer(t)
			ts.prov.SetUtilityHook(func(_ context.Context, _ agentapi.UtilityRequest) (string, error) {
				model := name
				ts.m.mu.Lock()
				switch name {
				case "disappeared":
					model = "available"
					info := ts.m.infos["copilot"]
					info.Models = nil
					ts.m.infos["copilot"] = info
				case "newly hidden":
					model = "available"
					ts.m.settings.HiddenModels["copilot"] = []string{"available"}
				case "provider unavailable", "inherit":
					model = "available"
					info := ts.m.infos["copilot"]
					info.Available = false
					ts.m.infos["copilot"] = info
					if name == "inherit" {
						model = ""
					}
				}
				ts.m.mu.Unlock()
				return strings.Replace(validAgentDraft, `"model":"available"`, `"model":"`+model+`"`, 1), nil
			})
			draft, err := ts.m.DraftConfiguration(context.Background(), "", "agents", "Create a review helper")
			if name == "inherit" {
				if err != nil || draft.Model != "" {
					t.Fatalf("inherited model = %+v, %v", draft, err)
				}
				return
			}
			if configurationStatus(err) != http.StatusBadGateway {
				t.Fatalf("unavailable model error = %v", err)
			}
			if calls := ts.m.UtilityLog(0, 10).Calls; len(calls) != 1 || calls[0].Outcome != utilityError {
				t.Fatalf("rejected draft not logged as failed: %+v", calls)
			}
		})
	}
}

func TestConfigurationDraftUtilityAvailability(t *testing.T) {
	for _, mode := range []string{"none", "no default", "stale model", "provider unavailable", "missing capability", "missing custom key", "hidden utility"} {
		t.Run(mode, func(t *testing.T) {
			ts := newConfigurationDraftServer(t)
			ts.m.mu.Lock()
			info := ts.m.infos["copilot"]
			switch mode {
			case "none":
				ts.m.settings.TitleModel["copilot"] = store.WebTitleModelNone
			case "no default":
				delete(ts.m.settings.TitleModel, "copilot")
			case "stale model":
				ts.m.settings.TitleModel["copilot"] = "removed"
			case "provider unavailable":
				info.Available = false
			case "missing capability":
				info.Capabilities.HostTools = false
			case "missing custom key":
				ts.m.settings.TitleModel["copilot"] = "custom/no-key"
			case "hidden utility":
				ts.m.settings.TitleModel["copilot"] = "hidden"
			}
			ts.m.infos["copilot"] = info
			ts.m.mu.Unlock()
			ts.prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return validSkillDraft, nil })
			draft, err := ts.m.DraftConfiguration(context.Background(), "", "skills", "Draft a review skill")
			if mode == "hidden utility" {
				if err != nil || draft.UtilityModel != "hidden" {
					t.Fatalf("explicitly selected hidden utility model = %+v, %v", draft, err)
				}
				return
			}
			var e *Error
			if !errors.As(err, &e) || e.Status != http.StatusConflict || e.Code != codeUtilityUnavailable || len(ts.prov.UtilityRequests()) != 0 {
				t.Fatalf("unavailable utility = %v", err)
			}
		})
	}
}

func TestConfigurationDraftBudgetAndProviderFailures(t *testing.T) {
	for _, mode := range []string{"off", "limit", "provider failure", "invalid output"} {
		t.Run(mode, func(t *testing.T) {
			ts := newConfigurationDraftServer(t)
			ts.prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) {
				if mode == "provider failure" {
					return "", errors.New("provider unavailable")
				}
				if mode == "invalid output" {
					return "not JSON", nil
				}
				return validSkillDraft, nil
			})
			limit := 1
			if mode == "off" {
				limit = 0
			}
			ts.m.mu.Lock()
			ts.m.settings.UtilityDailyLimit = &limit
			ts.m.mu.Unlock()
			if mode == "limit" {
				if _, err := ts.m.DraftConfiguration(context.Background(), "", "skills", "Draft a review skill"); err != nil {
					t.Fatal(err)
				}
			}
			_, err := ts.m.DraftConfiguration(context.Background(), "", "skills", "Draft a review skill")
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("expected utility error, got %v", err)
			}
			calls := ts.m.UtilityLog(0, 10).Calls
			if mode == "off" || mode == "limit" {
				if e.Code != codeUtilityPaused || e.Status != http.StatusConflict || calls[0].Outcome != utilitySkipped || len(ts.prov.UtilityRequests()) != limit {
					t.Fatalf("budget error = %+v, calls = %+v", e, calls)
				}
			} else if e.Code != codeUtilityFailed || e.Status != http.StatusBadGateway || calls[0].Outcome != utilityError {
				t.Fatalf("provider error = %+v, calls = %+v", e, calls)
			}
		})
	}
}

func TestConfigurationDraftHTTPGuards(t *testing.T) {
	ts := newConfigurationDraftServer(t)
	for _, tc := range []struct {
		name, kind, query, body string
		status                  int
		opts                    []reqOpt
	}{
		{"no auth", "agents", "", `{"brief":"Review"}`, http.StatusUnauthorized, nil},
		{"cross origin", "agents", "", `{"brief":"Review"}`, http.StatusForbidden, []reqOpt{withCookie(ts), withHeader("Origin", "https://elsewhere.invalid")}},
		{"bad project", "agents", "?project_id=unknown", `{"brief":"Review"}`, http.StatusNotFound, []reqOpt{withCookie(ts)}},
		{"bad kind", "instructions", "", `{"brief":"Review"}`, http.StatusBadRequest, []reqOpt{withCookie(ts)}},
		{"bad JSON", "agents", "", "{", http.StatusBadRequest, []reqOpt{withCookie(ts)}},
		{"trailing JSON", "agents", "", `{"brief":"Review"}{}`, http.StatusBadRequest, []reqOpt{withCookie(ts)}},
		{"empty brief", "agents", "", `{"brief":" "}`, http.StatusBadRequest, []reqOpt{withCookie(ts)}},
		{"large brief", "agents", "", `{"brief":"` + strings.Repeat("a", maxConfigurationBrief+1) + `"}`, http.StatusBadRequest, []reqOpt{withCookie(ts)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := ts.do(http.MethodPost, "/api/configuration/"+tc.kind+"/draft"+tc.query, tc.body, tc.opts...)
			if w.Code != tc.status {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
		})
	}
	ts.m.mu.Lock()
	ts.m.settings.Terminal = false
	ts.m.mu.Unlock()
	for _, kind := range []string{"agents", "skills", "hooks"} {
		w := ts.do(http.MethodPost, "/api/configuration/"+kind+"/draft", `{"brief":"Review"}`, withCookie(ts))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Terminal") {
			t.Fatalf("Terminal guard = %d: %s", w.Code, w.Body.String())
		}
	}
	if len(ts.prov.UtilityRequests()) != 0 {
		t.Fatal("rejected request called the model")
	}
}

func TestConfigurationDraftCancellationAndShutdown(t *testing.T) {
	for _, source := range []string{"request", "shutdown"} {
		t.Run(source, func(t *testing.T) {
			ts := newConfigurationDraftServer(t)
			started, canceled, cleanup := make(chan struct{}), make(chan struct{}), make(chan struct{})
			ts.prov.SetUtilityHook(func(ctx context.Context, _ agentapi.UtilityRequest) (string, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				<-cleanup
				return "", ctx.Err()
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := ts.m.DraftConfiguration(ctx, "", "skills", "Draft a review skill")
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("draft did not start")
			}
			var shutdown chan error
			if source == "shutdown" {
				shutdown = make(chan error, 1)
				go func() { shutdown <- ts.m.Shutdown(context.Background()) }()
			} else {
				cancel()
			}
			select {
			case <-canceled:
			case <-time.After(5 * time.Second):
				t.Fatal("utility was not canceled")
			}
			if shutdown != nil {
				select {
				case err := <-shutdown:
					t.Fatalf("shutdown did not wait for utility cleanup: %v", err)
				default:
				}
			}
			close(cleanup)
			select {
			case err := <-done:
				if configurationStatus(err) != http.StatusBadGateway || !strings.Contains(err.Error(), "context canceled") {
					t.Fatalf("canceled draft = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("draft did not release its utility job")
			}
			if shutdown != nil {
				select {
				case err := <-shutdown:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("shutdown did not finish")
				}
			}
			if calls := ts.m.UtilityLog(0, 10).Calls; len(calls) != 1 || calls[0].Outcome != utilityError {
				t.Fatalf("cancel log = %+v", calls)
			}
		})
	}
}

func TestConfigurationDraftToolListsAndHookEvents(t *testing.T) {
	for _, event := range configurationHookEvents {
		draft, err := parseConfigurationDraft("hooks", strings.Replace(validHookDraft, "preToolUse", event, 1), nil)
		if err != nil || draft.Event != event || draft.Env["MODE"] != "review" || draft.TimeoutSec == nil || *draft.TimeoutSec != 10 {
			t.Fatalf("hook event %s = %+v, %v", event, draft, err)
		}
	}
	reply := strings.Replace(validAgentDraft, `"tools":[]`, `"tools":["read","search"]`, 1)
	if draft, err := parseConfigurationDraft("agents", reply, nil); err != nil || draft.Tools == nil || !slices.Equal(*draft.Tools, []string{"read", "search"}) {
		t.Fatalf("tool list = %+v, %v", draft, err)
	}
}

func writeDraftSkill(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationDraftSimilarSkillsUseOneCallAndResolvedIdentity(t *testing.T) {
	for _, tc := range []struct{ kind, reply string }{{"agents", validAgentDraft}, {"skills", validSkillDraft}, {"hooks", validHookDraft}} {
		t.Run(tc.kind, func(t *testing.T) {
			ts := newConfigurationDraftServer(t)
			project := ts.m.projects["project"].Dir
			globalSkill := filepath.Join(os.Getenv("COPILOT_HOME"), "skills", "review", "SKILL.md")
			projectSkill := filepath.Join(project, ".github", "skills", "deploy", "SKILL.md")
			content := "---\nname: review\ndescription: Review a release before deploying\n---\nCheck the release."
			writeDraftSkill(t, globalSkill, content)
			writeDraftSkill(t, projectSkill, "Deploy a reviewed release.")
			alias := filepath.Join(project, ".agents", "skills", "review-alias")
			if err := os.MkdirAll(filepath.Dir(alias), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Dir(globalSkill), alias); err != nil {
				t.Fatal(err)
			}
			writeDraftSkill(t, filepath.Join(os.Getenv("HOME"), ".agents", "skills", "writing", "SKILL.md"), "Write clear documentation.")
			writeDraftSkill(t, filepath.Join(project, "AGENTS.md"), "DO_NOT_INCLUDE_INSTRUCTIONS")
			writeDraftSkill(t, filepath.Join(project, ".github", "hooks", "secret.json"), "DO_NOT_INCLUDE_HOOKS")
			ts.prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
				if !strings.Contains(req.System, "untrusted reference data") || strings.Contains(req.Prompt, project) || strings.Contains(req.Prompt, os.Getenv("HOME")) || strings.Contains(req.Prompt, "DO_NOT_INCLUDE") {
					t.Fatalf("unexpected skill catalogue prompt: %s", req.Prompt)
				}
				_, rest, ok := strings.Cut(req.Prompt, "<installed_skills>\n")
				encoded, _, end := strings.Cut(rest, "\n</installed_skills>")
				var catalog []configurationDraftSkill
				if !ok || !end || json.Unmarshal([]byte(encoded), &catalog) != nil || len(catalog) != 3 {
					t.Fatalf("expected three unique project and global skills: %s", encoded)
				}
				var reply map[string]any
				if err := json.Unmarshal([]byte(tc.reply), &reply); err != nil {
					t.Fatal(err)
				}
				reply["similar_skills"] = []map[string]string{{"id": catalog[0].ID, "reason": "Covers the release deployment."}, {"id": catalog[1].ID, "reason": "Covers the release review."}, {"id": catalog[2].ID, "reason": "Explains how to document the release."}}
				b, err := json.Marshal(reply)
				return string(b), err
			})
			draft, err := ts.m.DraftConfiguration(context.Background(), "project", tc.kind, "Prepare, review, and document a release")
			if err != nil {
				t.Fatal(err)
			}
			if len(draft.SimilarSkills) != 3 || draft.SimilarSkills[0].Path != projectSkill || draft.SimilarSkills[0].ProjectID != "project" || draft.SimilarSkills[1].Path != globalSkill || draft.SimilarSkills[1].ProjectID != "project" || draft.SimilarSkills[2].ProjectID != "" {
				t.Fatalf("resolved suggestions = %+v", draft.SimilarSkills)
			}
			if len(ts.prov.UtilityRequests()) != 1 || len(ts.m.UtilityLog(0, 10).Calls) != 1 {
				t.Fatal("suggestions must share the draft's only Utility call")
			}
			if b, err := os.ReadFile(globalSkill); err != nil || string(b) != content {
				t.Fatal("generating recommendations changed the installed skill")
			}
		})
	}
}

func TestConfigurationDraftSkillCatalogueBounds(t *testing.T) {
	for _, mode := range []string{"count", "bytes and UTF-8"} {
		t.Run(mode, func(t *testing.T) {
			ts := newConfigurationDraftServer(t)
			content := "Use for reviews."
			if mode == "bytes and UTF-8" {
				content = strings.Repeat("<", 511) + "é" + strings.Repeat("more", 256)
			}
			for i := range 128 {
				writeDraftSkill(t, filepath.Join(os.Getenv("COPILOT_HOME"), "skills", fmt.Sprintf("skill-%03d", i), "SKILL.md"), content)
			}
			writeDraftSkill(t, filepath.Join(os.Getenv("HOME"), ".agents", "skills", "one-more", "SKILL.md"), content)
			catalog := ts.m.configurationDraftSkills("")
			encoded, err := json.Marshal(catalog)
			if err != nil || len(catalog) == 0 || len(catalog) > 128 || len(encoded) > maxDraftSkillCatalog || strings.Contains(string(encoded), os.Getenv("COPILOT_HOME")) {
				t.Fatalf("catalogue bound: entries=%d bytes=%d error=%v", len(catalog), len(encoded), err)
			}
			if mode == "count" && len(catalog) != 128 || mode != "count" && len(catalog) == 128 {
				t.Fatalf("%s bound did not apply: %d entries", mode, len(catalog))
			}
			for _, skill := range catalog {
				if len(skill.Excerpt) > maxDraftSkillExcerpt || !utf8.ValidString(skill.Excerpt) || mode != "count" && len(skill.Excerpt) != 511 {
					t.Fatalf("excerpt is not a bounded UTF-8 prefix: %q", skill.Excerpt)
				}
			}
		})
	}
}

func TestConfigurationDraftUnavailableSkillsDoNotBlockGeneration(t *testing.T) {
	for _, mode := range []string{"missing", "broken link", "invalid root", "too many"} {
		t.Run(mode, func(t *testing.T) {
			ts := newConfigurationDraftServer(t)
			switch mode {
			case "broken link":
				dir := filepath.Join(os.Getenv("COPILOT_HOME"), "skills")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(dir, "broken")); err != nil {
					t.Fatal(err)
				}
			case "invalid root":
				t.Setenv("COPILOT_HOME", "relative-is-invalid")
			case "too many":
				for i := range maxConfigurationFiles + 1 {
					writeDraftSkill(t, filepath.Join(os.Getenv("COPILOT_HOME"), "skills", fmt.Sprintf("skill-%03d", i), "SKILL.md"), "Use for reviews.")
				}
			}
			ts.prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
				if !strings.Contains(req.Prompt, "<installed_skills>\n[]\n</installed_skills>") {
					t.Fatalf("unavailable skills should leave an empty catalogue: %s", req.Prompt)
				}
				return strings.TrimSuffix(validSkillDraft, "}") + `,"similar_skills":[]}`, nil
			})
			draft, err := ts.m.DraftConfiguration(context.Background(), "", "skills", "Draft a review helper")
			if err != nil || draft.Name != "review-helper" || len(draft.SimilarSkills) != 0 || len(ts.prov.UtilityRequests()) != 1 {
				t.Fatalf("draft without catalogue = %+v, %v", draft, err)
			}
		})
	}
}

func TestConfigurationDraftRejectsUnverifiedSkillSuggestions(t *testing.T) {
	catalog := []configurationDraftSkill{{ID: "skill-1", Name: "review", path: "/known/review/SKILL.md"}}
	for _, matches := range []string{
		`[{"id":"unknown","reason":"Related"}]`,
		`[{"id":"","reason":"Related"}]`,
		`[{"id":"skill-1","reason":"Related"},{"id":"skill-1","reason":"Also related"}]`,
		`[{"id":"skill-1","reason":" "}]`,
		`[{"id":"skill-1","reason":"` + strings.Repeat("a", 513) + `"}]`,
		`[{"id":"skill-1","reason":"Bad\u0000reason"}]`,
		`[{"id":"skill-1","reason":"Related","path":"/arbitrary/SKILL.md"}]`,
		`[{"name":"review","reason":"Related"}]`,
		`[{"id":"skill-1","reason":"Related"},{"id":"skill-2","reason":"Related"},{"id":"skill-3","reason":"Related"},{"id":"skill-4","reason":"Related"}]`,
	} {
		reply := strings.TrimSuffix(validSkillDraft, "}") + `,"similar_skills":` + matches + `}`
		if _, err := parseConfigurationDraft("skills", reply, catalog); err == nil {
			t.Errorf("accepted unverified suggestion: %s", matches)
		}
	}
	ts := newConfigurationDraftServer(t)
	ts.prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) {
		return strings.TrimSuffix(validSkillDraft, "}") + `,"similar_skills":[{"id":"invented","reason":"Related"}]}`, nil
	})
	if _, err := ts.m.DraftConfiguration(context.Background(), "", "skills", "Review helper"); configurationStatus(err) != http.StatusBadGateway {
		t.Fatalf("unknown skill ID must fail the response: %v", err)
	}
	if calls := ts.m.UtilityLog(0, 10).Calls; len(calls) != 1 || calls[0].Outcome != utilityError {
		t.Fatalf("unverified suggestion must log a failed Utility call: %+v", calls)
	}
}

func TestConfigurationDraftInvocationControls(t *testing.T) {
	for _, kind := range []string{"agents", "skills"} {
		for _, value := range []string{"true", "false"} {
			reply := strings.TrimSuffix(validSkillDraft, "}") + `,"disable_model_invocation":` + value + `,"user_invocable":false}`
			draft, err := parseConfigurationDraft(kind, reply, nil)
			if err != nil || draft.DisableModelInvocation == nil || *draft.DisableModelInvocation != (value == "true") || draft.UserInvocable == nil || *draft.UserInvocable {
				t.Fatalf("%s invocation controls = %+v, %v", kind, draft, err)
			}
			encoded, err := json.Marshal(draft)
			if err != nil || !strings.Contains(string(encoded), `"disable_model_invocation":`+value) || !strings.Contains(string(encoded), `"user_invocable":false`) {
				t.Fatalf("invocation controls were lost: %s, %v", encoded, err)
			}
		}
		if system := configurationDraftSystemFor(kind, nil); !strings.Contains(system, "disable_model_invocation=true for a manual-only") {
			t.Fatalf("%s creator lacks guidance for manual-only requests", kind)
		}
	}
	for _, reply := range []string{
		strings.TrimSuffix(validHookDraft, "}") + `,"disable_model_invocation":false}`,
		strings.TrimSuffix(validHookDraft, "}") + `,"user_invocable":false}`,
	} {
		if _, err := parseConfigurationDraft("hooks", reply, nil); err == nil {
			t.Fatal("hook accepted skill/agent invocation controls")
		}
	}
	if _, err := parseConfigurationDraft("skills", strings.TrimSuffix(validSkillDraft, "}")+`,"disable_model_invocation":"yes"}`, nil); err == nil {
		t.Fatal("accepted non-boolean invocation control")
	}
}

func TestConfigurationDraftHookExampleAndNotification(t *testing.T) {
	system := configurationDraftSystemFor("hooks", nil)
	if !strings.Contains(system, configurationHookDraftExample) || !strings.Contains(system, "notification") || !strings.Contains(system, "timeout_sec with an underscore") {
		t.Fatal("hook prompt must provide the exact draft shape and notification event")
	}
	for _, reply := range []string{configurationHookDraftExample, strings.Replace(configurationHookDraftExample, "sessionStart", "notification", 1)} {
		draft, err := parseConfigurationDraft("hooks", reply, nil)
		if err != nil || draft.TimeoutSec == nil || *draft.TimeoutSec != 10 || len(draft.SimilarSkills) != 0 {
			t.Fatalf("hook prompt example must parse: %+v, %v", draft, err)
		}
	}
	for _, field := range []string{"type", "version", "hooks", "timeoutSec", "command", "private-model-output"} {
		reply := strings.TrimSuffix(configurationHookDraftExample, "}") + `,"` + field + `":"private-model-value"}`
		_, err := parseConfigurationDraft("hooks", reply, nil)
		if err == nil || !strings.Contains(err.Error(), "unsupported JSON field") || strings.Contains(err.Error(), field) || strings.Contains(err.Error(), "private-model-value") {
			t.Fatalf("unexpected diagnostic for unsupported field %q: %v", field, err)
		}
	}
}

func TestConfigurationDraftSkillsExcludeDisabled(t *testing.T) {
	m := configurationManager(t)
	file, err := m.SaveConfiguration("project", "skills", "example", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	if catalog := m.configurationDraftSkills("project"); len(catalog) != 1 {
		t.Fatalf("initial catalogue = %+v", catalog)
	}
	disabled := true
	stored, err := m.SaveConfiguration("project", "skills", file.Name, configurationInput{Path: file.Path, Revision: file.Revision, Disabled: &disabled}, false)
	if err != nil {
		t.Fatal(err)
	}
	if catalog := m.configurationDraftSkills("project"); len(catalog) != 0 {
		t.Fatalf("disabled skill recommended = %+v", catalog)
	}
	disabled = false
	if _, err := m.SaveConfiguration("project", "skills", stored.Name, configurationInput{Path: stored.Path, Revision: stored.Revision, Disabled: &disabled}, false); err != nil {
		t.Fatal(err)
	}
	if catalog := m.configurationDraftSkills("project"); len(catalog) != 1 || catalog[0].path != file.Path {
		t.Fatalf("restored catalogue = %+v", catalog)
	}
}
