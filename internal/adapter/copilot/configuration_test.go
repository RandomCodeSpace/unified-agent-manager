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
	err                   error
	projects, directories []string
}

func (f *configurationFakeClient) DiscoverSkills(_ context.Context, projects, directories []string) (*rpc.ServerSkillList, error) {
	f.projects, f.directories = slices.Clone(projects), slices.Clone(directories)
	return f.skills, f.err
}

func (f *configurationFakeClient) DiscoverAgents(_ context.Context, projects []string) (*rpc.ServerAgentList, error) {
	f.projects = slices.Clone(projects)
	return f.agents, f.err
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
		})
	}
	provider := newWebProvider(func() (sdkClient, error) { return &fakeClient{}, nil }, time.Hour)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	if _, err := provider.DiscoverSkills(context.Background(), nil, nil); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("absent client capability = %v", err)
	}
}

func TestConfigurationDiscoveryBounds(t *testing.T) {
	client := &configurationFakeClient{fakeClient: &fakeClient{}, skills: &rpc.ServerSkillList{}, agents: &rpc.ServerAgentList{}}
	for i := 0; i < 130; i++ {
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
}
