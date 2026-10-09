package copilot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	DiscoverHooks(context.Context, []string) (*rpc.HooksDiscoverResult, error)
	DiscoverInstructions(context.Context, []string) (*rpc.ServerInstructionSourceList, error)
}

func (a sdkClientAdapter) DiscoverSkills(ctx context.Context, projectPaths, skillDirectories []string) (*rpc.ServerSkillList, error) {
	return a.c.RPC.Skills.Discover(ctx, &rpc.SkillsDiscoverRequest{ProjectPaths: projectPaths, SkillDirectories: skillDirectories})
}

func (a sdkClientAdapter) DiscoverAgents(ctx context.Context, projectPaths []string) (*rpc.ServerAgentList, error) {
	return a.c.RPC.Agents.Discover(ctx, &rpc.AgentsDiscoverRequest{ProjectPaths: projectPaths})
}

func (a sdkClientAdapter) DiscoverHooks(ctx context.Context, projectPaths []string) (*rpc.HooksDiscoverResult, error) {
	return a.c.RPC.Hooks.Discover(ctx, &rpc.HooksDiscoverRequest{ProjectPaths: projectPaths})
}

func (a sdkClientAdapter) DiscoverInstructions(ctx context.Context, projectPaths []string) (*rpc.ServerInstructionSourceList, error) {
	return a.c.RPC.Instructions.Discover(ctx, &rpc.InstructionsDiscoverRequest{ProjectPaths: projectPaths})
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

// DiscoverHooks lists hook actions by event, origin and source. DisableKey is a
// content hash for the native disabled-hooks setting, so it is not exposed.
func (p *webProvider) DiscoverHooks(ctx context.Context, projectPaths []string) (agentapi.ConfigurationCatalog, error) {
	c, err := p.configurationClient(ctx)
	if err != nil {
		return agentapi.ConfigurationCatalog{}, err
	}
	res, err := c.DiscoverHooks(ctx, projectPaths)
	if err = configurationDiscoveryError(err, "hooks"); err != nil {
		return agentapi.ConfigurationCatalog{}, err
	}
	if res == nil {
		return agentapi.ConfigurationCatalog{}, errors.New("native hooks discovery returned no result")
	}
	// Errors make discovery incomplete; native warnings leave it complete.
	return configurationCatalog("hook", res.Hooks, res.Errors, func(hook rpc.DiscoveredHook) agentapi.ConfigurationDefinition {
		enabled, source := hook.Enabled, deref(hook.Source)
		entry := agentapi.ConfigurationDefinition{ID: hook.ID, Name: string(hook.HookType), Source: string(hook.Origin), Enabled: &enabled}
		if filepath.IsAbs(source) {
			entry.Path = source
		} else {
			entry.Description = source
		}
		return entry
	}), nil
}

// DiscoverInstructions lists instruction sources without their content or the
// description, which is the body after the frontmatter. Relative plugin paths
// cannot be resolved here and stay pathless.
func (p *webProvider) DiscoverInstructions(ctx context.Context, projectPaths []string) (agentapi.ConfigurationCatalog, error) {
	c, err := p.configurationClient(ctx)
	if err != nil {
		return agentapi.ConfigurationCatalog{}, err
	}
	res, err := c.DiscoverInstructions(ctx, projectPaths)
	if err = configurationDiscoveryError(err, "instructions"); err != nil {
		return agentapi.ConfigurationCatalog{}, err
	}
	if res == nil {
		return agentapi.ConfigurationCatalog{}, errors.New("native instructions discovery returned no result")
	}
	return configurationCatalog("instruction", res.Sources, nil, func(source rpc.InstructionSource) agentapi.ConfigurationDefinition {
		entry := agentapi.ConfigurationDefinition{ID: source.ID, Name: source.Label, Source: string(source.Location)}
		if filepath.IsAbs(source.SourcePath) {
			entry.Path = source.SourcePath
		} else if project := deref(source.ProjectPath); filepath.IsAbs(project) && source.SourcePath != "" {
			entry.Path = filepath.Join(project, source.SourcePath)
		}
		return entry
	}), nil
}

func configurationCatalog[T any](kind string, rows []T, diagnostics []string, convert func(T) agentapi.ConfigurationDefinition) agentapi.ConfigurationCatalog {
	out := agentapi.ConfigurationCatalog{}
	if len(rows) > maxConfigurationDefinitions {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Native %ss discovery exceeded the 128-definition limit.", kind))
	}
	invalid := false
	for _, row := range rows[:min(len(rows), maxConfigurationDefinitions)] {
		entry := convert(row)
		if !boundedConfigurationDefinition(&entry) {
			invalid = true
			continue
		}
		out.Definitions = append(out.Definitions, entry)
	}
	if invalid {
		out.Warnings = append(out.Warnings, fmt.Sprintf("A native %s definition had invalid or oversized metadata.", kind))
	}
	if len(diagnostics) > 16 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Additional native %s diagnostics were omitted.", kind))
	}
	for _, diagnostic := range diagnostics[:min(len(diagnostics), 16)] {
		out.Warnings = append(out.Warnings, clip(displaytext.Sanitize(diagnostic), 1024))
	}
	return out
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
