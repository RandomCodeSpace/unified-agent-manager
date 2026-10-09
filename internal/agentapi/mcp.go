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
// only while it is connected. SignIn marks a server that uses a sign-in: its
// configuration names an OAuth client, or it waited for a sign-in.
type MCPStatus struct {
	Name   string    `json:"name"`
	Status string    `json:"status"`
	Error  string    `json:"error,omitempty"`
	Source string    `json:"source,omitempty"`
	Remote bool      `json:"remote,omitempty"`
	SignIn bool      `json:"sign_in,omitempty"`
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

// MaxMCPStatusServers bounds live MCP metadata independently of tool bodies.
const MaxMCPStatusServers = 128

// MCPServerStatus is lightweight state, never a server URL or tool description.
// SignIn marks a server seen waiting for a sign-in; it stays set.
type MCPServerStatus struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	Source         string `json:"source,omitempty"`
	Remote         bool   `json:"remote,omitempty"`
	SignIn         bool   `json:"sign_in,omitempty"`
	NeedsReconnect bool   `json:"needs_reconnect,omitempty"`
}

// MCPStatusSnapshot belongs to the current open conversation, not its history.
// Supported is provider capability; Ready means a full initial list arrived.
// Truncated reports server states that could not fit in the bounded snapshot.
type MCPStatusSnapshot struct {
	Supported bool              `json:"supported"`
	Ready     bool              `json:"ready"`
	Servers   []MCPServerStatus `json:"servers"`
	Truncated bool              `json:"truncated,omitempty"`
}

// MCPStatusReader optionally supplies event-backed state and on-demand tools.
// The original MCPController list remains available to older providers/clients.
type MCPStatusReader interface {
	MCPStatusSnapshot(ctx context.Context) (MCPStatusSnapshot, error)
	MCPTools(ctx context.Context, name string) ([]MCPTool, error)
}

// MCPCallbackSignIn is an SDK-owned authorization waiting at a fixed HTTPS
// callback. An empty URL means kept credentials already connected the server.
type MCPCallbackSignIn struct {
	AuthorizationID string
	URL             string
}

// MCPCallbackController optionally supports a host-delivered OAuth callback.
// The provider owns discovery, PKCE, exchange, token storage and reconnect.
// ErrUnsupported is effect-free; other login errors must not start a fallback.
type MCPCallbackController interface {
	MCPSignInCallback(ctx context.Context, name string, again bool, redirectURI string) (MCPCallbackSignIn, error)
	CompleteMCPSignIn(ctx context.Context, authorizationID, callbackURL string) error
}
