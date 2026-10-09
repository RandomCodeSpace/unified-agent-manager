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

// ConfigurationDiscoverer optionally lists the provider's native skills and
// agents without opening a conversation or changing configuration. Empty
// projectPaths selects global sources; skillDirectories must match Task setup.
// ErrUnsupported applies per kind, only when that discovery method is absent.
type ConfigurationDiscoverer interface {
	DiscoverSkills(ctx context.Context, projectPaths, skillDirectories []string) (ConfigurationCatalog, error)
	DiscoverAgents(ctx context.Context, projectPaths []string) (ConfigurationCatalog, error)
}

// AgentSelector is implemented by a conversation of a provider whose
// Capabilities.CustomAgents is true.
type AgentSelector interface {
	// SelectAgent selects the custom agent id for the next turns, or the
	// default agent when id is "". The caller never selects during a turn.
	// ErrAgentUnavailable means id cannot be selected.
	SelectAgent(ctx context.Context, id string) error
}
