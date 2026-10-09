package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type configurationFakeClient struct {
	*fakeClient
	skills                *rpc.ServerSkillList
	agents                *rpc.ServerAgentList
	hooks                 *rpc.HooksDiscoverResult
	instructions          *rpc.ServerInstructionSourceList
	err                   error
	projects, directories []string
	toggles               []string
}

func (f *configurationFakeClient) SetSkillDisabled(_ context.Context, name string, disabled bool) error {
	f.toggles = append(f.toggles, fmt.Sprint(name, "=", disabled))
	return f.err
}

func TestConfigurationSkillGlobalSetting(t *testing.T) {
	client := &configurationFakeClient{fakeClient: &fakeClient{}}
	provider := newWebProvider(func() (sdkClient, error) { return client, nil }, time.Hour)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	if err := provider.SetSkillGloballyDisabled(context.Background(), "review", true); err != nil {
		t.Fatal(err)
	}
	if err := provider.SetSkillGloballyDisabled(context.Background(), "review", false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(client.toggles, []string{"review=true", "review=false"}) {
		t.Fatalf("native toggles = %v", client.toggles)
	}
	client.err = &copilot.RPCError{Code: -32601, Message: "private detail"}
	if err := provider.SetSkillGloballyDisabled(context.Background(), "review", true); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("absent method = %v", err)
	}
	client.err = &copilot.RPCError{Code: -32000, Message: "private detail"}
	if err := provider.SetSkillGloballyDisabled(context.Background(), "review", true); err == nil || errors.Is(err, agentapi.ErrUnsupported) || strings.Contains(err.Error(), "private detail") {
		t.Fatalf("native failure = %v", err)
	}
	absent := newWebProvider(func() (sdkClient, error) { return &fakeClient{}, nil }, time.Hour)
	t.Cleanup(func() { _ = absent.Shutdown(context.Background()) })
	if err := absent.SetSkillGloballyDisabled(context.Background(), "review", true); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("absent client capability = %v", err)
	}
}

func (f *configurationFakeClient) DiscoverSkills(_ context.Context, projects, directories []string) (*rpc.ServerSkillList, error) {
	f.projects, f.directories = slices.Clone(projects), slices.Clone(directories)
	return f.skills, f.err
}

func (f *configurationFakeClient) DiscoverAgents(_ context.Context, projects []string) (*rpc.ServerAgentList, error) {
	f.projects = slices.Clone(projects)
	return f.agents, f.err
}

func (f *configurationFakeClient) DiscoverHooks(_ context.Context, projects []string) (*rpc.HooksDiscoverResult, error) {
	f.projects = slices.Clone(projects)
	return f.hooks, f.err
}

func (f *configurationFakeClient) DiscoverInstructions(_ context.Context, projects []string) (*rpc.ServerInstructionSourceList, error) {
	f.projects = slices.Clone(projects)
	return f.instructions, f.err
}

func TestConfigurationDiscoveryHooksAndInstructionsMetadata(t *testing.T) {
	hookFile, plugin, disableKey, project := "/home/user/.copilot/hooks/audit.json", "audit-plugin", "private-disable-hash", "/project"
	description, global := "private instruction description", "/home/user/.copilot/copilot-instructions.md"
	client := &configurationFakeClient{fakeClient: &fakeClient{},
		hooks: &rpc.HooksDiscoverResult{Hooks: []rpc.DiscoveredHook{{ID: "hook-one", HookType: rpc.HookTypePreToolUse, Origin: rpc.HookOriginUser, Source: &hookFile, DisableKey: &disableKey}, {ID: "hook-two", HookType: rpc.HookTypeSessionStart, Origin: rpc.HookOriginPlugin, Source: &plugin, Enabled: true}}, Errors: []string{"Hook file could not be parsed"}, Warnings: []string{"Recoverable hook warning"}},
		instructions: &rpc.ServerInstructionSourceList{Sources: []rpc.InstructionSource{
			{ID: "repo", Label: "Repository instructions", Location: rpc.InstructionSourceLocationRepository, SourcePath: ".github/copilot-instructions.md", ProjectPath: &project, Content: "private instruction content", Description: &description, ApplyTo: []string{"**/*.go"}, Type: rpc.InstructionSourceTypeRepo},
			{ID: "home", Label: "Personal instructions", Location: rpc.InstructionSourceLocationUser, SourcePath: global, Content: "private home content", Type: rpc.InstructionSourceTypeHome},
			{ID: "plugin-rules", Label: "Plugin rules", Location: rpc.InstructionSourceLocationPlugin, SourcePath: "rules/review.md", Content: "private plugin content", Type: rpc.InstructionSourceTypePlugin},
		}}}
	provider := newWebProvider(func() (sdkClient, error) { return client, nil }, time.Hour)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	hooks, err := provider.DiscoverHooks(context.Background(), []string{project})
	if err != nil || len(hooks.Definitions) != 2 || !slices.Equal(client.projects, []string{project}) {
		t.Fatalf("hooks = %+v, %v; projects=%v", hooks, err, client.projects)
	}
	first, second := hooks.Definitions[0], hooks.Definitions[1]
	if first.ID != "hook-one" || first.Name != "preToolUse" || first.Source != "user" || first.Path != hookFile || first.Enabled == nil || *first.Enabled {
		t.Fatalf("hook file metadata = %+v", first)
	}
	if second.Path != "" || second.Description != plugin || second.Source != "plugin" || second.Enabled == nil || !*second.Enabled {
		t.Fatalf("pathless hook metadata = %+v", second)
	}
	if !slices.Equal(hooks.Warnings, []string{"Hook file could not be parsed"}) {
		t.Fatalf("hook diagnostics = %v", hooks.Warnings)
	}
	instructions, err := provider.DiscoverInstructions(context.Background(), nil)
	if err != nil || len(instructions.Definitions) != 3 || len(client.projects) != 0 {
		t.Fatalf("instructions = %+v, %v; projects=%v", instructions, err, client.projects)
	}
	paths := []string{}
	for _, definition := range instructions.Definitions {
		paths = append(paths, definition.Path)
		if definition.Enabled != nil {
			t.Fatalf("instruction claimed global enablement: %+v", definition)
		}
	}
	if !slices.Equal(paths, []string{"/project/.github/copilot-instructions.md", global, ""}) || instructions.Definitions[0].Name != "Repository instructions" || instructions.Definitions[0].Source != "repository" {
		t.Fatalf("instruction metadata = %+v", instructions.Definitions)
	}
	raw, err := json.Marshal([]any{hooks.Definitions, instructions.Definitions})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{disableKey, "private instruction content", "private home content", "private plugin content", description, "**/*.go"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("native metadata disclosed %q", secret)
		}
	}
}

func TestConfigurationDiscoveryMetadata(t *testing.T) {
	path, prompt, source := "/project/.github/agents/file.agent.md", "private authored prompt", rpc.AgentInfoSourcePlugin
	client := &configurationFakeClient{fakeClient: &fakeClient{}, skills: &rpc.ServerSkillList{Skills: []rpc.ServerSkill{{Name: "runtime-name", Description: "A discovered skill", Enabled: false, Source: rpc.SkillSourceProject, UserInvocable: true}}}, agents: &rpc.ServerAgentList{Agents: []rpc.AgentInfo{{ID: "plugin:reviewer", Name: "reviewer", DisplayName: "Review agent", Description: "Review only", Source: &source, Path: &path, Prompt: &prompt, MCPServers: map[string]any{"secret": map[string]any{"headers": map[string]string{"Authorization": "private bearer"}}}, Tools: []string{"private-tool"}}}}}
	provider := newWebProvider(func() (sdkClient, error) { return client, nil }, time.Hour)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	skills, err := provider.DiscoverSkills(context.Background(), []string{"/project"}, []string{"/uam/skills"})
	if err != nil || len(skills.Definitions) != 1 || !slices.Equal(client.projects, []string{"/project"}) || !slices.Equal(client.directories, []string{"/uam/skills"}) {
		t.Fatalf("discovery = %+v, %v; projects=%v, directories=%v", skills, err, client.projects, client.directories)
	}
	if skills.Definitions[0].Enabled == nil || *skills.Definitions[0].Enabled || skills.Definitions[0].Name != "runtime-name" {
		t.Fatalf("native enablement/name lost: %+v", skills)
	}
	agents, err := provider.DiscoverAgents(context.Background(), nil)
	if err != nil || len(agents.Definitions) != 1 || len(client.projects) != 0 || agents.Definitions[0].ID != "plugin:reviewer" {
		t.Fatalf("agents = %+v, %v; projects=%v", agents, err, client.projects)
	}
	raw, err := json.Marshal(agents.Definitions)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{prompt, "private bearer", "private-tool", "mcpServers", "prompt"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("native metadata disclosed %q", secret)
		}
	}
}

func TestConfigurationDiscoveryErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		unsupported bool
	}{
		{"method absent", &copilot.RPCError{Code: -32601, Message: "private detail"}, true},
		{"wrapped absent", fmt.Errorf("wrapped: %w", &copilot.RPCError{Code: -32601}), true},
		{"real failure", &copilot.RPCError{Code: -32000, Message: "Method not found private detail"}, false},
		{"nil result", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &configurationFakeClient{fakeClient: &fakeClient{}, err: tc.err}
			provider := newWebProvider(func() (sdkClient, error) { return client, nil }, time.Hour)
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			_, err := provider.DiscoverSkills(context.Background(), nil, nil)
			if err == nil || errors.Is(err, agentapi.ErrUnsupported) != tc.unsupported || strings.Contains(err.Error(), "private detail") {
				t.Fatalf("skills error = %v", err)
			}
			_, err = provider.DiscoverAgents(context.Background(), nil)
			if err == nil || errors.Is(err, agentapi.ErrUnsupported) != tc.unsupported || strings.Contains(err.Error(), "private detail") {
				t.Fatalf("agents error = %v", err)
			}
			_, err = provider.DiscoverHooks(context.Background(), nil)
			if err == nil || errors.Is(err, agentapi.ErrUnsupported) != tc.unsupported || strings.Contains(err.Error(), "private detail") {
				t.Fatalf("hooks error = %v", err)
			}
			_, err = provider.DiscoverInstructions(context.Background(), nil)
			if err == nil || errors.Is(err, agentapi.ErrUnsupported) != tc.unsupported || strings.Contains(err.Error(), "private detail") {
				t.Fatalf("instructions error = %v", err)
			}
		})
	}
	provider := newWebProvider(func() (sdkClient, error) { return &fakeClient{}, nil }, time.Hour)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	if _, err := provider.DiscoverSkills(context.Background(), nil, nil); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("absent client capability = %v", err)
	}
}

func TestConfigurationDiscoveryBounds(t *testing.T) {
	client := &configurationFakeClient{fakeClient: &fakeClient{}, skills: &rpc.ServerSkillList{}, agents: &rpc.ServerAgentList{}, hooks: &rpc.HooksDiscoverResult{}, instructions: &rpc.ServerInstructionSourceList{}}
	for i := 0; i < 130; i++ {
		client.hooks.Hooks = append(client.hooks.Hooks, rpc.DiscoveredHook{ID: fmt.Sprint("hook-", i), HookType: rpc.HookTypePreToolUse, Origin: rpc.HookOriginPolicy})
		client.hooks.Errors = append(client.hooks.Errors, strings.Repeat("bad hook ", 200))
		client.instructions.Sources = append(client.instructions.Sources, rpc.InstructionSource{ID: fmt.Sprint("instruction-", i), Label: "rules", Location: rpc.InstructionSourceLocationPlugin})
		client.skills.Skills = append(client.skills.Skills, rpc.ServerSkill{Name: fmt.Sprint("skill-", i), Description: strings.Repeat("x", 2048), Source: rpc.SkillSourcePlugin})
		client.agents.Agents = append(client.agents.Agents, rpc.AgentInfo{ID: fmt.Sprint("agent-", i), Name: "agent", Description: strings.Repeat("x", 2048)})
		client.skills.Errors = append(client.skills.Errors, strings.Repeat("bad skill ", 200))
	}
	client.agents.Agents[0].ID = strings.Repeat("oversized", 100)
	provider := newWebProvider(func() (sdkClient, error) { return client, nil }, time.Hour)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	skills, err := provider.DiscoverSkills(context.Background(), nil, nil)
	if err != nil || len(skills.Definitions) != 128 || len(skills.Definitions[0].Description) > 1024 || len(skills.Warnings) > 19 || !strings.Contains(strings.Join(skills.Warnings, " "), "limit") || !strings.Contains(strings.Join(skills.Warnings, " "), "omitted") {
		t.Fatalf("skills bounds = %d rows, %d warnings, %v", len(skills.Definitions), len(skills.Warnings), err)
	}
	agents, err := provider.DiscoverAgents(context.Background(), nil)
	if err != nil || len(agents.Definitions) != 127 || !strings.Contains(strings.Join(agents.Warnings, " "), "invalid") {
		t.Fatalf("agent bounds = %+v, %v", agents, err)
	}
	hooks, err := provider.DiscoverHooks(context.Background(), nil)
	if err != nil || len(hooks.Definitions) != 128 || len(hooks.Warnings) > 19 || len(hooks.Warnings[1]) > 1024 || !strings.Contains(strings.Join(hooks.Warnings, " "), "limit") || !strings.Contains(strings.Join(hooks.Warnings, " "), "omitted") {
		t.Fatalf("hook bounds = %d rows, %d warnings, %v", len(hooks.Definitions), len(hooks.Warnings), err)
	}
	instructions, err := provider.DiscoverInstructions(context.Background(), nil)
	if err != nil || len(instructions.Definitions) != 128 || !strings.Contains(strings.Join(instructions.Warnings, " "), "limit") {
		t.Fatalf("instruction bounds = %d rows, %v", len(instructions.Definitions), err)
	}
}
