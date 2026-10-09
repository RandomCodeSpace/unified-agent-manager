package agentapi

import "context"

// MCP server transports.
const (
	MCPStdio = "stdio"
	MCPHTTP  = "http"
	MCPSSE   = "sse"
)

// MCPServerConfig is one MCP server of the provider's own, user-wide
// configuration: a command it runs (MCPStdio) or a remote endpoint (MCPHTTP,
// MCPSSE). Env and Headers carry real values; the web service never returns
// them to a browser.
type MCPServerConfig struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"-"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"-"`
}

// MCPServer is a configured server and whether new conversations start it.
// Source is where it is configured: "user" servers are the ones
// MCPConfigurer edits; others (a plugin, built in) are listed read-only.
type MCPServer struct {
	MCPServerConfig
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"`
}

// MCPConfigurer is implemented by a provider whose Capabilities.MCP is true.
// It edits the provider's user-wide MCP configuration, which applies to
// conversations opened afterwards. ctx bounds each call.
type MCPConfigurer interface {
	MCPServers(ctx context.Context) ([]MCPServer, error)
	// AddMCPServer adds a new server; UpdateMCPServer replaces the
	// transport fields of an existing one and keeps the rest of its entry.
	AddMCPServer(ctx context.Context, cfg MCPServerConfig) error
	UpdateMCPServer(ctx context.Context, cfg MCPServerConfig) error
	RemoveMCPServer(ctx context.Context, name string) error
	SetMCPServerEnabled(ctx context.Context, name string, enabled bool) error
}

// MCP server states in one conversation: MCPStatus.Status is one of them.
const (
	MCPConnected     = "connected"
	MCPFailed        = "failed"
	MCPNeedsAuth     = "needs-auth"
	MCPPending       = "pending"
	MCPDisabled      = "disabled"
	MCPStopped       = "stopped"
	MCPNotConfigured = "not_configured"
)

// MCPStatus is one MCP server as a conversation sees it. Tools are listed
// only while it is connected.
type MCPStatus struct {
	Name   string    `json:"name"`
	Status string    `json:"status"`
	Error  string    `json:"error,omitempty"`
	Source string    `json:"source,omitempty"`
	Remote bool      `json:"remote,omitempty"`
	Tools  []MCPTool `json:"tools,omitempty"`
}

// MCPTool is one tool an MCP server offers.
type MCPTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// MCPController is implemented by a conversation of a provider whose
// Capabilities.MCP is true. A conversation keeps the server configuration
// it opened with; its changes here last as long as it stays open and leave
// the user-wide configuration unchanged.
type MCPController interface {
	MCPStatus(ctx context.Context) ([]MCPStatus, error)
	SetMCPServerEnabled(ctx context.Context, name string, enabled bool) error
	RestartMCPServer(ctx context.Context, name string) error
	// MCPSignIn starts the OAuth sign-in of a remote server and returns the
	// address to open in a browser, or "" when a kept sign-in sufficed. The
	// provider waits for the browser on a loopback address of this host.
	// again discards a kept sign-in first.
	MCPSignIn(ctx context.Context, name string, again bool) (string, error)
}

// CustomizationsReloader optionally refreshes an open conversation's discovered
// configuration without closing it. Prompt and tool changes apply on the next
// turn. ErrUnsupported means the capability is absent and nothing changed;
// other errors may follow a partially applied reload and must not be retried
// by closing and reopening the conversation.
type CustomizationsReloader interface {
	ReloadCustomizations(ctx context.Context) error
}
