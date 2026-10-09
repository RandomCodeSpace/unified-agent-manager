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

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

type configurationProvider struct {
	*agenttest.Provider
	skills, agents        agentapi.ConfigurationCatalog
	hooks, instructions   agentapi.ConfigurationCatalog
	skillsErr, agentsErr  error
	hooksErr              error
	projects, directories [][]string
	toggles               []string
	toggleErr             error
}

func (p *configurationProvider) SetSkillGloballyDisabled(_ context.Context, name string, disabled bool) error {
	p.toggles = append(p.toggles, fmt.Sprint(name, "=", disabled))
	return p.toggleErr
}

func (p *configurationProvider) DiscoverSkills(_ context.Context, projects, directories []string) (agentapi.ConfigurationCatalog, error) {
	p.projects = append(p.projects, slices.Clone(projects))
	p.directories = append(p.directories, slices.Clone(directories))
	return p.skills, p.skillsErr
}
func (p *configurationProvider) DiscoverAgents(_ context.Context, projects []string) (agentapi.ConfigurationCatalog, error) {
	p.projects = append(p.projects, slices.Clone(projects))
	return p.agents, p.agentsErr
}
func (p *configurationProvider) DiscoverHooks(_ context.Context, projects []string) (agentapi.ConfigurationCatalog, error) {
	p.projects = append(p.projects, slices.Clone(projects))
	return p.hooks, p.hooksErr
}
func (p *configurationProvider) DiscoverInstructions(_ context.Context, projects []string) (agentapi.ConfigurationCatalog, error) {
	p.projects = append(p.projects, slices.Clone(projects))
	return p.instructions, nil
}
func attachConfigurationProvider(m *Manager, p *configurationProvider) {
	m.ctx = context.Background()
	m.order = []string{"fake"}
	m.providers = map[string]agentapi.Provider{"fake": p}
	m.infos = map[string]ProviderInfo{"fake": {Available: true}}
}

func TestConfigurationDiscoveryPreservesFileIdentityAndRecovery(t *testing.T) {
	m := configurationManager(t)
	file, err := m.SaveConfiguration("project", "skills", "folder-name", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := m.SaveConfiguration("project", "skills", "saved-disabled", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	disabledValue := true
	if _, err = m.SaveConfiguration("project", "skills", disabled.Name, configurationInput{Path: disabled.Path, Revision: disabled.Revision, Disabled: &disabledValue}, false); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "SKILL.md")
	if err = os.Symlink(file.Path, alias); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.agent.md")
	if err = os.WriteFile(external, []byte("private external prompt"), 0600); err != nil {
		t.Fatal(err)
	}
	enabled := false
	p := &configurationProvider{Provider: agenttest.NewProvider("fake", agentapi.Capabilities{}), skills: agentapi.ConfigurationCatalog{Definitions: []agentapi.ConfigurationDefinition{{ID: "authored-name", Name: "authored-name", Source: "project", Path: alias, Enabled: &enabled}, {ID: "authored-name", Name: "authored-name", Source: "project", Path: file.Path}}}, agents: agentapi.ConfigurationCatalog{Definitions: []agentapi.ConfigurationDefinition{{ID: "plugin:review", Name: "review", Source: "plugin", Path: external}, {ID: "remote-one", Name: "remote", Source: "remote"}, {ID: "remote-two", Name: "remote", Source: "remote"}}}}
	attachConfigurationProvider(m, p)
	m.skillDirs = []string{"/uam/custom-skills"}
	cfg, err := m.Configuration("project")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills) != 2 || len(cfg.Agents) != 3 || !cfg.Discovery["skills"].Ready || !slices.Equal(p.projects[0], []string{m.projects["project"].Dir}) || !slices.Equal(p.directories[0], m.skillDirs) {
		t.Fatalf("configuration = %+v; projects=%v dirs=%v", cfg, p.projects, p.directories)
	}
	var managed, recovery ConfigurationFile
	for _, candidate := range cfg.Skills {
		if candidate.Name == file.Name {
			managed = candidate
		} else {
			recovery = candidate
		}
	}
	if managed.Native == nil || managed.Native.Name != "authored-name" || managed.Name != file.Name || managed.Path != file.Path || managed.Revision != file.Revision || managed.Content != file.Content || !managed.Editable || managed.Disabled || managed.MetadataOnly {
		t.Fatalf("managed identity changed: %+v", managed)
	}
	if !recovery.Disabled || recovery.Native != nil || !recovery.Editable {
		t.Fatalf("disabled-file recovery changed: %+v", recovery)
	}
	for _, agent := range cfg.Agents {
		if !agent.MetadataOnly || agent.Editable || agent.Revision != "" || agent.Content != "" {
			t.Fatalf("native path gained file access: %+v", agent)
		}
	}
	if _, err = m.SaveConfiguration("project", "agents", "review", configurationInput{Path: external, Content: "overwrite"}, false); err == nil {
		t.Fatal("native path expanded mutation scope")
	}
	if _, err = m.SaveConfiguration("project", "skills", managed.Name, configurationInput{Path: managed.Path, Revision: managed.Revision, Content: testSkillDefinition + "updated"}, false); err != nil {
		t.Fatal("native name broke exact-file save", err)
	}
	if _, err = m.Configuration(""); err != nil {
		t.Fatal(err)
	}
	if len(p.projects[4]) != 0 || len(p.projects[5]) != 0 || !slices.Equal(p.directories[1], m.skillDirs) {
		t.Fatalf("global discovery got project paths: %v", p.projects)
	}
}

func TestConfigurationDiscoveryFailuresRetainManagedFiles(t *testing.T) {
	m := configurationManager(t)
	file, err := m.SaveConfiguration("project", "agents", "reviewer", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	p := &configurationProvider{Provider: agenttest.NewProvider("fake", agentapi.Capabilities{}), skillsErr: agentapi.ErrUnsupported, agentsErr: errors.New("private RPC content")}
	attachConfigurationProvider(m, p)
	cfg, err := m.Configuration("project")
	if err != nil || len(cfg.Agents) != 1 || cfg.Agents[0].Revision != file.Revision || !cfg.Agents[0].Editable || cfg.Discovery["skills"].Supported || cfg.Discovery["skills"].Ready || !cfg.Discovery["agents"].Supported || cfg.Discovery["agents"].Ready {
		t.Fatalf("failed native discovery erased files/readiness: %+v, %v", cfg, err)
	}
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "private RPC content") {
		t.Fatal("raw native error disclosed")
	}
	m.providers["fake"] = p.Provider
	cfg, err = m.Configuration("project")
	if err != nil || cfg.Discovery != nil || len(cfg.Agents) != 1 {
		t.Fatalf("absent capability = %+v, %v", cfg, err)
	}
	before := len(p.projects)
	if _, err = m.Configuration("missing"); err == nil || len(p.projects) != before {
		t.Fatal("unknown Project reached native discovery")
	}
}

func TestConfigurationDiscoveryPartialAndOverflow(t *testing.T) {
	m := configurationManager(t)
	p := &configurationProvider{Provider: agenttest.NewProvider("fake", agentapi.Capabilities{}), skills: agentapi.ConfigurationCatalog{Warnings: []string{"Malformed skill definition"}}}
	attachConfigurationProvider(m, p)
	for i := 0; i < 130; i++ {
		p.agents.Definitions = append(p.agents.Definitions, agentapi.ConfigurationDefinition{ID: fmt.Sprint(i), Name: "remote", Source: "remote"})
	}
	cfg, err := m.Configuration("project")
	if err != nil || cfg.Discovery["skills"].Ready || len(cfg.Agents) != 128 || cfg.Discovery["agents"].Ready || !strings.Contains(strings.Join(cfg.Discovery["agents"].Warnings, " "), "limit") {
		t.Fatalf("incomplete/overflow discovery claimed ready: %+v, %v", cfg, err)
	}
}

func TestConfigurationDiscoveryHooksAndInstructionsStayMetadata(t *testing.T) {
	m := configurationManager(t)
	// Native and managed paths both keep a symlinked ancestor unresolved.
	parent := filepath.Join(t.TempDir(), "parent-link")
	if err := os.Symlink(filepath.Dir(m.projects["project"].Dir), parent); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, filepath.Base(m.projects["project"].Dir))
	m.projects["project"].Dir = link
	hook, err := m.SaveConfiguration("project", "hooks", "audit", configurationInput{Content: `{"version":1,"hooks":{"preToolUse":[{"type":"command","bash":"printf one"}],"postToolUse":[{"type":"command","bash":"printf two"}]}}`}, false)
	if err != nil {
		t.Fatal(err)
	}
	instructions, err := m.SaveConfiguration("project", "instructions", "copilot-instructions", configurationInput{Content: "Keep changes focused.\n"}, false)
	if err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(t.TempDir(), "policy-hooks.json")
	if err = os.WriteFile(policy, []byte(`{"private":"policy"}`), 0600); err != nil {
		t.Fatal(err)
	}
	disabled := false
	p := &configurationProvider{Provider: agenttest.NewProvider("fake", agentapi.Capabilities{}),
		hooks:        agentapi.ConfigurationCatalog{Definitions: []agentapi.ConfigurationDefinition{{ID: "pre", Name: "preToolUse", Source: "repository", Path: hook.Path, Enabled: &disabled}, {ID: "post", Name: "postToolUse", Source: "repository", Path: hook.Path}, {ID: "policy", Name: "preToolUse", Source: "policy", Path: policy}, {ID: "plugin", Name: "sessionStart", Source: "plugin", Description: "audit-plugin"}}},
		instructions: agentapi.ConfigurationCatalog{Definitions: []agentapi.ConfigurationDefinition{{ID: "repo", Name: "Repository instructions", Source: "repository", Path: instructions.Path}, {ID: "rules", Name: "Plugin rules", Source: "plugin"}}}}
	attachConfigurationProvider(m, p)
	cfg, err := m.Configuration("project")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Discovery["hooks"].Ready || !cfg.Discovery["instructions"].Ready || !slices.Equal(p.projects[2], []string{link}) || !slices.Equal(p.projects[3], []string{link}) {
		t.Fatalf("discovery = %+v; projects=%v", cfg.Discovery, p.projects)
	}
	if len(cfg.Hooks) != 4 || cfg.Hooks[0].Native == nil || cfg.Hooks[0].Native.ID != "pre" || cfg.Hooks[0].Path != hook.Path || cfg.Hooks[0].Revision != hook.Revision || !cfg.Hooks[0].Editable || cfg.Hooks[0].MetadataOnly {
		t.Fatalf("managed hook identity changed: %+v", cfg.Hooks)
	}
	for _, row := range cfg.Hooks[1:] {
		if !row.MetadataOnly || row.Editable || row.Content != "" || row.Revision != "" {
			t.Fatalf("native hook gained file access: %+v", row)
		}
	}
	if len(cfg.InstructionFiles) != 3 || cfg.InstructionFiles[0].Native == nil || cfg.InstructionFiles[0].Native.ID != "repo" || cfg.InstructionFiles[0].Revision != instructions.Revision || !cfg.InstructionFiles[0].Editable {
		t.Fatalf("managed instruction identity changed: %+v", cfg.InstructionFiles)
	}
	if rules := cfg.InstructionFiles[2]; !rules.MetadataOnly || rules.Editable || rules.Path != "" || rules.Content != "" {
		t.Fatalf("pathless instruction gained file access: %+v", rules)
	}
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "private") {
		t.Fatal("external hook content disclosed")
	}
	if _, err = m.SaveConfiguration("project", "hooks", "policy-hooks", configurationInput{Path: policy, Content: "{}"}, false); err == nil {
		t.Fatal("native hook path expanded mutation scope")
	}
	if _, err = m.SaveConfiguration("project", "hooks", hook.Name, configurationInput{Path: hook.Path, Revision: hook.Revision, Content: `{"version":1,"hooks":{}}`}, false); err != nil {
		t.Fatal("native hook metadata broke exact-file save", err)
	}
	p.hooksErr = errors.New("private hook failure")
	if cfg, err = m.Configuration("project"); err != nil || len(cfg.Hooks) != 1 || !cfg.Hooks[0].Editable || cfg.Discovery["hooks"].Ready || !cfg.Discovery["instructions"].Ready {
		t.Fatalf("failed hook discovery erased managed files: %+v, %v", cfg, err)
	}
}

func TestConfigurationSkillGlobalSettingLeavesFiles(t *testing.T) {
	m := configurationManager(t)
	file, err := m.SaveConfiguration("project", "skills", "folder-name", configurationInput{Content: testSkillDefinition}, false)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	p := &configurationProvider{Provider: agenttest.NewProvider("fake", agentapi.Capabilities{}), skills: agentapi.ConfigurationCatalog{Definitions: []agentapi.ConfigurationDefinition{{ID: "review", Name: "review", Source: "project", Path: file.Path, Enabled: &enabled}, {ID: "review", Name: "review", Source: "plugin", Enabled: &enabled}}}}
	attachConfigurationProvider(m, p)
	before, err := m.Configuration("project")
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetSkillGloballyDisabled("project", "review", true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.toggles, []string{"review=true"}) || !slices.Equal(p.projects[len(p.projects)-1], []string{m.projects["project"].Dir}) {
		t.Fatalf("toggles = %v; projects = %v", p.toggles, p.projects)
	}
	after, err := m.Configuration("project")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Skills) != len(before.Skills) || after.Skills[0].Path != file.Path || after.Skills[0].Revision != file.Revision || after.Skills[0].Disabled {
		t.Fatalf("global setting changed the project file: %+v", after.Skills)
	}
	if _, err = os.Stat(file.Path + disabledConfigurationSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("global setting renamed a file: %v", err)
	}
	for _, tc := range []struct {
		name, skill string
		status      int
		setup       func()
	}{
		{"unknown name", "missing", http.StatusNotFound, nil},
		{"empty name", "", http.StatusBadRequest, nil},
		{"padded name", " review", http.StatusBadRequest, nil},
		{"control character", "review\x1b[31m", http.StatusBadRequest, nil},
		{"oversized name", strings.Repeat("r", 257), http.StatusBadRequest, nil},
		{"unknown project", "review", http.StatusNotFound, func() { delete(m.projects, "gone") }},
		{"terminal off", "review", http.StatusForbidden, func() { m.settings.Terminal = false }},
		{"discovery failed", "review", http.StatusBadGateway, func() { m.settings.Terminal = true; p.skillsErr = errors.New("private") }},
		{"unsupported", "review", http.StatusConflict, func() { p.skillsErr = nil; p.toggleErr = agentapi.ErrUnsupported }},
		{"native failure", "review", http.StatusBadGateway, func() { p.toggleErr = errors.New("native skill setting failed") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup()
			}
			calls := len(p.toggles)
			project := "project"
			if tc.name == "unknown project" {
				project = "gone"
			}
			err := m.SetSkillGloballyDisabled(project, tc.skill, true)
			if configurationStatus(err) != tc.status {
				t.Fatalf("status = %d (%v), want %d", configurationStatus(err), err, tc.status)
			}
			wantCalls := calls
			if tc.name == "unsupported" || tc.name == "native failure" {
				wantCalls++
			}
			if len(p.toggles) != wantCalls {
				t.Fatalf("toggles = %v", p.toggles)
			}
		})
	}
	m.providers["fake"] = p.Provider
	if err = m.SetSkillGloballyDisabled("project", "review", true); configurationStatus(err) != http.StatusConflict {
		t.Fatalf("absent capability = %v", err)
	}
}

func TestConfigurationSkillGlobalSettingHTTP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), ".copilot"))
	ts := newTestServer(t, ServerConfig{Assets: frameAssets()})
	ts.m.settings.Terminal = true
	enabled := false
	p := &configurationProvider{Provider: agenttest.NewProvider("fake", agentapi.Capabilities{}), skills: agentapi.ConfigurationCatalog{Definitions: []agentapi.ConfigurationDefinition{{ID: "review", Name: "review", Source: "personal-copilot", Enabled: &enabled}}}}
	ts.m.mu.Lock()
	ts.m.order = []string{"fake"}
	ts.m.providers = map[string]agentapi.Provider{"fake": p}
	ts.m.infos = map[string]ProviderInfo{"fake": {Available: true}}
	ts.m.mu.Unlock()
	for _, body := range []string{`{"name":"review"}`, `{"disabled":true}`, `{"name":"review","disabled":"yes"}`} {
		if w := ts.do(http.MethodPost, "/api/configuration/skills/global-disabled", body, withCookie(ts)); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid body %s = %d %s", body, w.Code, w.Body)
		}
	}
	if len(p.toggles) != 0 {
		t.Fatalf("invalid bodies changed the setting: %v", p.toggles)
	}
	if w := ts.do(http.MethodPost, "/api/configuration/skills/global-disabled", `{"name":"review","disabled":false}`, withCookie(ts)); w.Code != http.StatusNoContent {
		t.Fatalf("enable = %d %s", w.Code, w.Body)
	}
	if !slices.Equal(p.toggles, []string{"review=false"}) {
		t.Fatalf("toggles = %v", p.toggles)
	}
	if w := ts.do(http.MethodPost, "/api/configuration/skills/global-disabled", `{"name":"review","disabled":true}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out request = %d", w.Code)
	}
}
