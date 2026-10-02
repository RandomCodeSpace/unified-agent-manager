package copilot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// mcpStatuses maps the CLI's MCP server states to the contract's.
var mcpStatuses = map[rpc.MCPServerStatus]string{
	rpc.MCPServerStatusConnected:     agentapi.MCPConnected,
	rpc.MCPServerStatusFailed:        agentapi.MCPFailed,
	rpc.MCPServerStatusNeedsAuth:     agentapi.MCPNeedsAuth,
	rpc.MCPServerStatusPending:       agentapi.MCPPending,
	rpc.MCPServerStatusDisabled:      agentapi.MCPDisabled,
	rpc.MCPServerStatusStopped:       agentapi.MCPStopped,
	rpc.MCPServerStatusNotConfigured: agentapi.MCPNotConfigured,
}

// mcpStatus is the contract's state for the CLI's; a state the contract does
// not know yet, from a newer CLI, passes through as the CLI spells it.
func mcpStatus(s rpc.MCPServerStatus) string {
	return cmp.Or(mcpStatuses[s], string(s))
}

// mcpSignInMessage is what the CLI's loopback page says once a sign-in
// finished; the browser that sees it may be on another machine.
const mcpSignInMessage = "Signed in. You can close this tab and return to uam."

// mcpConfigClient is the experimental, user-wide MCP configuration of the
// CLI (mcp.config.* and mcp.discover). sdkClientAdapter implements it.
type mcpConfigClient interface {
	MCPConfigList(ctx context.Context) (map[string]rpc.MCPSerializableServerConfig, error)
	MCPDiscover(ctx context.Context) ([]rpc.DiscoveredMCPServer, error)
	MCPConfigAdd(ctx context.Context, name string, cfg rpc.MCPSerializableServerConfig) error
	MCPConfigUpdate(ctx context.Context, name string, cfg rpc.MCPSerializableServerConfig) error
	MCPConfigRemove(ctx context.Context, name string) error
	MCPConfigEnable(ctx context.Context, name string, enabled bool) error
	MCPConfigReload(ctx context.Context) error
}

// mcpSession is the experimental session.mcp.* surface of one session.
// sdkSessionAdapter implements it.
type mcpSession interface {
	MCPList(ctx context.Context) ([]rpc.MCPServer, error)
	MCPTools(ctx context.Context, name string) ([]rpc.MCPTools, error)
	MCPEnable(ctx context.Context, name string, enabled bool) error
	MCPRestart(ctx context.Context, name string) error
	MCPLogin(ctx context.Context, req *rpc.MCPOauthLoginRequest) (*rpc.MCPOauthLoginResult, error)
}

func (a sdkClientAdapter) MCPConfigList(ctx context.Context) (map[string]rpc.MCPSerializableServerConfig, error) {
	res, err := a.c.RPC.MCP.Config().List(ctx)
	if err != nil {
		return nil, err
	}
	return res.Servers, nil
}

func (a sdkClientAdapter) MCPDiscover(ctx context.Context) ([]rpc.DiscoveredMCPServer, error) {
	res, err := a.c.RPC.MCP.Discover(ctx, &rpc.MCPDiscoverRequest{})
	if err != nil {
		return nil, err
	}
	return res.Servers, nil
}

func (a sdkClientAdapter) MCPConfigAdd(ctx context.Context, name string, cfg rpc.MCPSerializableServerConfig) error {
	_, err := a.c.RPC.MCP.Config().Add(ctx, &rpc.MCPConfigAddRequest{Name: name, Config: cfg})
	return err
}

func (a sdkClientAdapter) MCPConfigUpdate(ctx context.Context, name string, cfg rpc.MCPSerializableServerConfig) error {
	_, err := a.c.RPC.MCP.Config().Update(ctx, &rpc.MCPConfigUpdateRequest{Name: name, Config: cfg})
	return err
}

func (a sdkClientAdapter) MCPConfigRemove(ctx context.Context, name string) error {
	_, err := a.c.RPC.MCP.Config().Remove(ctx, &rpc.MCPConfigRemoveRequest{Name: name})
	return err
}

func (a sdkClientAdapter) MCPConfigEnable(ctx context.Context, name string, enabled bool) error {
	var err error
	if enabled {
		_, err = a.c.RPC.MCP.Config().Enable(ctx, &rpc.MCPConfigEnableRequest{Names: []string{name}})
	} else {
		_, err = a.c.RPC.MCP.Config().Disable(ctx, &rpc.MCPConfigDisableRequest{Names: []string{name}})
	}
	return err
}

func (a sdkClientAdapter) MCPConfigReload(ctx context.Context) error {
	_, err := a.c.RPC.MCP.Config().Reload(ctx)
	return err
}

func (a sdkSessionAdapter) MCPList(ctx context.Context) ([]rpc.MCPServer, error) {
	res, err := a.s.RPC.MCP.List(ctx)
	if err != nil {
		return nil, err
	}
	return res.Servers, nil
}

func (a sdkSessionAdapter) MCPTools(ctx context.Context, name string) ([]rpc.MCPTools, error) {
	res, err := a.s.RPC.MCP.ListTools(ctx, &rpc.MCPListToolsRequest{ServerName: name})
	if err != nil {
		return nil, err
	}
	return res.Tools, nil
}

func (a sdkSessionAdapter) MCPEnable(ctx context.Context, name string, enabled bool) error {
	var err error
	if enabled {
		_, err = a.s.RPC.MCP.Enable(ctx, &rpc.MCPEnableRequest{ServerName: name})
	} else {
		_, err = a.s.RPC.MCP.Disable(ctx, &rpc.MCPDisableRequest{ServerName: name})
	}
	return err
}

// MCPRestart restarts a server with the configuration the session opened
// with: the CLI ignores a replacement configuration here.
func (a sdkSessionAdapter) MCPRestart(ctx context.Context, name string) error {
	_, err := a.s.RPC.MCP.RestartServer(ctx, &rpc.MCPRestartServerRequest{ServerName: name})
	return err
}

func (a sdkSessionAdapter) MCPLogin(ctx context.Context, req *rpc.MCPOauthLoginRequest) (*rpc.MCPOauthLoginResult, error) {
	return a.s.RPC.MCP.Oauth().Login(ctx, req)
}

func (p *webProvider) mcpClient(ctx context.Context) (mcpConfigClient, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	mc, ok := client.(mcpConfigClient)
	if !ok {
		return nil, agentapi.ErrUnsupported
	}
	return mc, nil
}

// MCPServers lists the user-configured servers, then the plugin and built-in
// ones discovery adds. A Project's own servers depend on its folder and show
// only in its Tasks.
func (p *webProvider) MCPServers(ctx context.Context) ([]agentapi.MCPServer, error) {
	mc, err := p.mcpClient(ctx)
	if err != nil {
		return nil, err
	}
	configs, err := mc.MCPConfigList(ctx)
	if err != nil {
		return nil, fmt.Errorf("list MCP servers: %s", rpcText(err))
	}
	discovered, err := mc.MCPDiscover(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover MCP servers: %s", rpcText(err))
	}
	enabled := map[string]bool{}
	out := make([]agentapi.MCPServer, 0, len(configs))
	for _, d := range discovered {
		if d.Source == rpc.MCPServerSourceUser {
			enabled[d.Name] = d.Enabled
			continue
		}
		if d.Source == rpc.MCPServerSourceWorkspace {
			continue
		}
		if _, dup := configs[d.Name]; dup {
			continue
		}
		kind := ""
		if d.Type != nil {
			kind = string(*d.Type)
		}
		out = append(out, agentapi.MCPServer{MCPServerConfig: agentapi.MCPServerConfig{Name: d.Name, Type: kind}, Enabled: d.Enabled, Source: string(d.Source)})
	}
	for name, cfg := range configs {
		on, known := enabled[name]
		out = append(out, agentapi.MCPServer{MCPServerConfig: fromRPCConfig(name, cfg), Enabled: on || !known, Source: string(rpc.MCPServerSourceUser)})
	}
	slices.SortFunc(out, func(a, b agentapi.MCPServer) int {
		return cmp.Or(cmp.Compare(sourceRank(a.Source), sourceRank(b.Source)), strings.Compare(a.Name, b.Name))
	})
	return out, nil
}

func sourceRank(source string) int {
	if source == string(rpc.MCPServerSourceUser) {
		return 0
	}
	return 1
}

func fromRPCConfig(name string, cfg rpc.MCPSerializableServerConfig) agentapi.MCPServerConfig {
	switch c := cfg.(type) {
	case *rpc.MCPServerConfigStdio:
		return agentapi.MCPServerConfig{Name: name, Type: agentapi.MCPStdio, Command: c.Command, Args: c.Args, Cwd: deref(c.Cwd), Env: c.Env}
	case *rpc.MCPServerConfigHTTP:
		kind := agentapi.MCPHTTP
		if c.Type != nil && *c.Type == rpc.MCPServerConfigHTTPTypeSSE {
			kind = agentapi.MCPSSE
		}
		return agentapi.MCPServerConfig{Name: name, Type: kind, URL: c.URL, Headers: c.Headers}
	}
	return agentapi.MCPServerConfig{Name: name}
}

// toRPCConfig writes cfg's transport fields over base, the server's current
// entry when it has the same transport, so the fields uam does not edit (tool
// filters, timeouts, OAuth client settings) are kept. What the CLI records
// about where it loaded an entry from is not written back.
func toRPCConfig(cfg agentapi.MCPServerConfig, base rpc.MCPSerializableServerConfig) rpc.MCPSerializableServerConfig {
	if cfg.Type == agentapi.MCPStdio {
		c := rpc.MCPServerConfigStdio{Tools: []string{"*"}}
		if b, ok := base.(*rpc.MCPServerConfigStdio); ok {
			c = *b
		}
		kind := rpc.MCPServerConfigStdioTypeStdio
		if c.Type == nil {
			c.Type = &kind
		}
		c.Command, c.Args, c.Env = cfg.Command, cfg.Args, cfg.Env
		c.Cwd = nil
		if cfg.Cwd != "" {
			c.Cwd = &cfg.Cwd
		}
		c.ConfigWarnings, c.Source, c.SourcePath, c.SourcePlugin, c.SourcePluginSpec, c.SourcePluginVersion = nil, nil, nil, nil, nil, nil
		return &c
	}
	c := rpc.MCPServerConfigHTTP{Tools: []string{"*"}}
	if b, ok := base.(*rpc.MCPServerConfigHTTP); ok {
		c = *b
	}
	kind := rpc.MCPServerConfigHTTPTypeHTTP
	if cfg.Type == agentapi.MCPSSE {
		kind = rpc.MCPServerConfigHTTPTypeSSE
	}
	c.Type, c.URL, c.Headers = &kind, cfg.URL, cfg.Headers
	c.ConfigWarnings, c.Source, c.SourcePath, c.SourcePlugin, c.SourcePluginSpec, c.SourcePluginVersion = nil, nil, nil, nil, nil, nil
	return &c
}

func (p *webProvider) AddMCPServer(ctx context.Context, cfg agentapi.MCPServerConfig) error {
	mc, err := p.mcpClient(ctx)
	if err != nil {
		return err
	}
	if err := mc.MCPConfigAdd(ctx, cfg.Name, toRPCConfig(cfg, nil)); err != nil {
		return fmt.Errorf("add MCP server: %s", rpcText(err))
	}
	return p.reloadMCPConfig(ctx, mc)
}

func (p *webProvider) UpdateMCPServer(ctx context.Context, cfg agentapi.MCPServerConfig) error {
	mc, err := p.mcpClient(ctx)
	if err != nil {
		return err
	}
	configs, err := mc.MCPConfigList(ctx)
	if err != nil {
		return fmt.Errorf("list MCP servers: %s", rpcText(err))
	}
	base, ok := configs[cfg.Name]
	if !ok {
		return fmt.Errorf("%w: MCP server %q", errMCPNotFound, cfg.Name)
	}
	if err := mc.MCPConfigUpdate(ctx, cfg.Name, toRPCConfig(cfg, base)); err != nil {
		return fmt.Errorf("update MCP server: %s", rpcText(err))
	}
	return p.reloadMCPConfig(ctx, mc)
}

var errMCPNotFound = errors.New("not configured")

func (p *webProvider) RemoveMCPServer(ctx context.Context, name string) error {
	mc, err := p.mcpClient(ctx)
	if err != nil {
		return err
	}
	if err := mc.MCPConfigRemove(ctx, name); err != nil {
		return fmt.Errorf("remove MCP server: %s", rpcText(err))
	}
	return p.reloadMCPConfig(ctx, mc)
}

func (p *webProvider) SetMCPServerEnabled(ctx context.Context, name string, enabled bool) error {
	mc, err := p.mcpClient(ctx)
	if err != nil {
		return err
	}
	if err := mc.MCPConfigEnable(ctx, name, enabled); err != nil {
		return fmt.Errorf("change MCP server: %s", rpcText(err))
	}
	return p.reloadMCPConfig(ctx, mc)
}

// reloadMCPConfig drops the CLI's cached server definitions so the next
// session it opens reads the file just written.
func (p *webProvider) reloadMCPConfig(ctx context.Context, mc mcpConfigClient) error {
	if err := mc.MCPConfigReload(ctx); err != nil {
		return fmt.Errorf("reload MCP servers: %s", rpcText(err))
	}
	return nil
}

func (c *conversation) mcp() (mcpSession, error) {
	if c.isClosed() {
		return nil, agentapi.ErrClosed
	}
	ms, ok := c.sess.(mcpSession)
	if !ok {
		return nil, agentapi.ErrUnsupported
	}
	return ms, nil
}

// MCPStatus lists the session's servers, with the tools of each connected
// one. Remote marks the user's remote servers and any that wait for a
// sign-in, so the caller can offer one.
func (c *conversation) MCPStatus(ctx context.Context) ([]agentapi.MCPStatus, error) {
	ms, err := c.mcp()
	if err != nil {
		return nil, err
	}
	servers, err := ms.MCPList(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the task's MCP servers: %s", rpcText(err))
	}
	remote := map[string]bool{}
	if mc, ok := c.client.(mcpConfigClient); ok {
		if configs, err := mc.MCPConfigList(ctx); err == nil {
			for name, cfg := range configs {
				_, remote[name] = cfg.(*rpc.MCPServerConfigHTTP)
			}
		}
	}
	out := make([]agentapi.MCPStatus, 0, len(servers))
	for _, s := range servers {
		st := agentapi.MCPStatus{Name: s.Name, Status: mcpStatus(s.Status), Error: errDetail(s.Error), Remote: remote[s.Name] || s.Status == rpc.MCPServerStatusNeedsAuth}
		if s.Source != nil {
			st.Source = string(*s.Source)
		}
		if s.Status == rpc.MCPServerStatusConnected {
			tools, err := ms.MCPTools(ctx, s.Name)
			if err != nil {
				st.Error = "could not list its tools: " + rpcText(err)
			}
			for _, t := range tools {
				st.Tools = append(st.Tools, agentapi.MCPTool{Name: t.Name, Description: clip(displaytext.Sanitize(deref(t.Description)), maxErrorText)})
			}
		}
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b agentapi.MCPStatus) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func errDetail(s *string) string {
	if s == nil {
		return ""
	}
	return clip(displaytext.Sanitize(*s), maxErrorText)
}

func (c *conversation) SetMCPServerEnabled(ctx context.Context, name string, enabled bool) error {
	ms, err := c.mcp()
	if err != nil {
		return err
	}
	if err := ms.MCPEnable(ctx, name, enabled); err != nil {
		return fmt.Errorf("change the MCP server: %s", rpcText(err))
	}
	return nil
}

func (c *conversation) RestartMCPServer(ctx context.Context, name string) error {
	ms, err := c.mcp()
	if err != nil {
		return err
	}
	if err := ms.MCPRestart(ctx, name); err != nil {
		return fmt.Errorf("restart the MCP server: %s", rpcText(err))
	}
	return nil
}

func (c *conversation) MCPSignIn(ctx context.Context, name string, again bool) (string, error) {
	ms, err := c.mcp()
	if err != nil {
		return "", err
	}
	client, message := "uam", mcpSignInMessage
	req := &rpc.MCPOauthLoginRequest{ServerName: name, ClientName: &client, CallbackSuccessMessage: &message}
	if again {
		req.ForceReauth = &again
	}
	res, err := ms.MCPLogin(ctx, req)
	if err != nil {
		return "", fmt.Errorf("start the sign-in: %s", rpcText(err))
	}
	return deref(res.AuthorizationURL), nil
}
