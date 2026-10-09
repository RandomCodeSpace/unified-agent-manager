package agentapi

import "context"

// ConfigurationDefinition describes a runtime-discovered customization. It does
// not contain authored prompts, tool catalogs or MCP server configuration, and
// its path never grants permission to read or write a file.
type ConfigurationDefinition struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name,omitempty"`
	Description   string `json:"description,omitempty"`
	Source        string `json:"source"`
	Path          string `json:"path,omitempty"`
	Enabled       *bool  `json:"enabled,omitempty"`
	UserInvocable *bool  `json:"user_invocable,omitempty"`
}

// ConfigurationCatalog is metadata only. Diagnostics mean discovery was
// incomplete; callers must retain safe file editing and disabled-file recovery.
type ConfigurationCatalog struct {
	Definitions []ConfigurationDefinition
	Warnings    []string
}

// ConfigurationDiscoverer optionally lists the provider's native skills, agents,
// hooks and instructions without opening a conversation or changing
// configuration. Hook and instruction rows never include actions or content. Empty
// projectPaths selects global sources; skillDirectories must match Task setup.
// ErrUnsupported applies per kind, only when that discovery method is absent.
type ConfigurationDiscoverer interface {
	DiscoverSkills(ctx context.Context, projectPaths, skillDirectories []string) (ConfigurationCatalog, error)
	DiscoverAgents(ctx context.Context, projectPaths []string) (ConfigurationCatalog, error)
	DiscoverHooks(ctx context.Context, projectPaths []string) (ConfigurationCatalog, error)
	DiscoverInstructions(ctx context.Context, projectPaths []string) (ConfigurationCatalog, error)
}

// SkillGlobalSetter optionally adds a name to, or removes it from, the
// provider's global disabled-skills list. The change applies to every skill
// with that name in every project and leaves other names untouched.
// ErrUnsupported means the runtime has no such setting.
type SkillGlobalSetter interface {
	SetSkillGloballyDisabled(ctx context.Context, name string, disabled bool) error
}
