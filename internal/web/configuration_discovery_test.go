package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	skillsErr, agentsErr  error
	projects, directories [][]string
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
	if len(p.projects[2]) != 0 || len(p.projects[3]) != 0 || !slices.Equal(p.directories[1], m.skillDirs) {
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
