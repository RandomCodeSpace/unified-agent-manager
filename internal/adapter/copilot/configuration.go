package copilot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

const maxConfigurationDefinitions = 128

// Keep generated SDK metadata inside this adapter. In particular AgentInfo also
// carries authored prompts and MCP config which this Settings view must omit.
type configurationDiscoveryClient interface {
	DiscoverSkills(context.Context, []string, []string) (*rpc.ServerSkillList, error)
	DiscoverAgents(context.Context, []string) (*rpc.ServerAgentList, error)
}

func (a sdkClientAdapter) DiscoverSkills(ctx context.Context, projectPaths, skillDirectories []string) (*rpc.ServerSkillList, error) {
	return a.c.RPC.Skills.Discover(ctx, &rpc.SkillsDiscoverRequest{ProjectPaths: projectPaths, SkillDirectories: skillDirectories})
}

func (a sdkClientAdapter) DiscoverAgents(ctx context.Context, projectPaths []string) (*rpc.ServerAgentList, error) {
	return a.c.RPC.Agents.Discover(ctx, &rpc.AgentsDiscoverRequest{ProjectPaths: projectPaths})
}

func (p *webProvider) configurationClient(ctx context.Context) (configurationDiscoveryClient, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, errors.New("native configuration discovery could not start")
	}
	c, ok := client.(configurationDiscoveryClient)
	if !ok {
		return nil, agentapi.ErrUnsupported
	}
	return c, nil
}

func configurationDiscoveryError(err error, kind string) error {
	var rpcErr *copilot.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
		return agentapi.ErrUnsupported
	}
	if err != nil {
		// Native errors can contain authored content or provider details.
		return fmt.Errorf("native %s discovery failed", kind)
	}
	return nil
}

func (p *webProvider) DiscoverSkills(ctx context.Context, projectPaths, skillDirectories []string) (agentapi.ConfigurationCatalog, error) {
	c, err := p.configurationClient(ctx)
	if err != nil {
		return agentapi.ConfigurationCatalog{}, err
	}
	res, err := c.DiscoverSkills(ctx, projectPaths, skillDirectories)
	if err = configurationDiscoveryError(err, "skills"); err != nil {
		return agentapi.ConfigurationCatalog{}, err
	}
	if res == nil {
		return agentapi.ConfigurationCatalog{}, errors.New("native skills discovery returned no result")
	}
	out := agentapi.ConfigurationCatalog{}
	if len(res.Skills) > maxConfigurationDefinitions {
		out.Warnings = append(out.Warnings, "Native skills discovery exceeded the 128-definition limit.")
	}
	invalid := false
	for _, skill := range res.Skills[:min(len(res.Skills), maxConfigurationDefinitions)] {
		enabled, invocable := skill.Enabled, skill.UserInvocable
		entry := agentapi.ConfigurationDefinition{ID: skill.Name, Name: skill.Name, Description: skill.Description, Source: string(skill.Source), Path: deref(skill.Path), Enabled: &enabled, UserInvocable: &invocable}
		if !boundedConfigurationDefinition(&entry) {
			invalid = true
			continue
		}
		out.Definitions = append(out.Definitions, entry)
	}
	if invalid {
		out.Warnings = append(out.Warnings, "A native skill definition had invalid or oversized metadata.")
	}
	if len(res.Errors) > 16 {
		out.Warnings = append(out.Warnings, "Additional native skill diagnostics were omitted.")
	}
	for _, warning := range res.Errors[:min(len(res.Errors), 16)] {
		out.Warnings = append(out.Warnings, clip(displaytext.Sanitize(warning), 1024))
	}
	return out, nil
}

func (p *webProvider) DiscoverAgents(ctx context.Context, projectPaths []string) (agentapi.ConfigurationCatalog, error) {
	c, err := p.configurationClient(ctx)
	if err != nil {
		return agentapi.ConfigurationCatalog{}, err
	}
	res, err := c.DiscoverAgents(ctx, projectPaths)
	if err = configurationDiscoveryError(err, "agents"); err != nil {
		return agentapi.ConfigurationCatalog{}, err
	}
	if res == nil {
		return agentapi.ConfigurationCatalog{}, errors.New("native agents discovery returned no result")
	}
	out := agentapi.ConfigurationCatalog{}
	if len(res.Agents) > maxConfigurationDefinitions {
		out.Warnings = append(out.Warnings, "Native agents discovery exceeded the 128-definition limit.")
	}
	invalid := false
	for _, agent := range res.Agents[:min(len(res.Agents), maxConfigurationDefinitions)] {
		source := ""
		if agent.Source != nil {
			source = string(*agent.Source)
		}
		id := agent.ID
		if id == "" {
			id = agent.Name
		}
		entry := agentapi.ConfigurationDefinition{ID: id, Name: agent.Name, DisplayName: agent.DisplayName, Description: agent.Description, Source: source, Path: deref(agent.Path), UserInvocable: agent.UserInvocable}
		if !boundedConfigurationDefinition(&entry) {
			invalid = true
			continue
		}
		out.Definitions = append(out.Definitions, entry)
	}
	if invalid {
		out.Warnings = append(out.Warnings, "A native agent definition had invalid or oversized metadata.")
	}
	return out, nil
}

func boundedConfigurationDefinition(entry *agentapi.ConfigurationDefinition) bool {
	// Never clip an identity or path into a different definition/file.
	for _, value := range []struct {
		text  string
		limit int
	}{{entry.ID, 512}, {entry.Name, 256}, {entry.Source, 128}, {entry.Path, 4096}} {
		if len(value.text) > value.limit || displaytext.Sanitize(value.text) != value.text {
			return false
		}
	}
	if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.Name) == "" {
		return false
	}
	entry.DisplayName = clip(displaytext.Sanitize(entry.DisplayName), 256)
	entry.Description = clip(displaytext.Sanitize(entry.Description), 1024)
	return true
}
