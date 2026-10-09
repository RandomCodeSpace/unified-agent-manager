package web

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// MCP servers (docs/web.md, MCP servers): the provider's user-wide
// configuration, edited only through its own API, and each open Task's
// servers. Env and header values are write-only: no response carries them.

const (
	mcpTimeout       = 45 * time.Second
	maxMCPEntries    = 64
	maxMCPArg        = 4096
	maxMCPValue      = 8192
	signInTTL        = 10 * time.Minute
	signInRelayLimit = 15 * time.Second
)

var (
	mcpNameExpr   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	envKeyExpr    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	headerKeyExpr = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,128}$")
	errStdioOff   = newError(http.StatusForbidden, "a server that runs a command needs Settings → Shell access → Terminal on; add a remote (HTTP or SSE) server instead")
)

// MCPSecret is one env variable or header: its name and whether a value is
// stored. The value itself is never sent.
type MCPSecret struct {
	Key string `json:"key"`
	Set bool   `json:"set"`
}

// MCPServerView is one configured server as the browser sees it.
type MCPServerView struct {
	Name    string      `json:"name"`
	Type    string      `json:"type"`
	Command string      `json:"command,omitempty"`
	Args    []string    `json:"args,omitempty"`
	Cwd     string      `json:"cwd,omitempty"`
	URL     string      `json:"url,omitempty"`
	Env     []MCPSecret `json:"env"`
	Headers []MCPSecret `json:"headers"`
	Enabled bool        `json:"enabled"`
	Source  string      `json:"source"`
}

// MCPServers is GET /api/mcp. Available is false when no provider manages
// MCP servers; StdioAllowed mirrors the Terminal setting.
type MCPServers struct {
	Available    bool            `json:"available"`
	StdioAllowed bool            `json:"stdio_allowed"`
	Servers      []MCPServerView `json:"servers"`
}

// MCPSecretInput is one env variable or header of an add or edit. A nil
// Value on an edit keeps the stored one.
type MCPSecretInput struct {
	Key   string  `json:"key"`
	Value *string `json:"value"`
}

// MCPServerInput is the body of an add or edit.
type MCPServerInput struct {
	Name    string           `json:"name"`
	Type    string           `json:"type"`
	Command string           `json:"command"`
	Args    []string         `json:"args"`
	Cwd     string           `json:"cwd"`
	URL     string           `json:"url"`
	Env     []MCPSecretInput `json:"env"`
	Headers []MCPSecretInput `json:"headers"`
}

// MCPSignIn is the answer to starting a sign-in. URL is empty when a kept
// sign-in sufficed. Relay is true when the browser may paste the address it
// ends on, for the service to pass to the provider's loopback listener.
type MCPSignIn struct {
	URL   string `json:"url,omitempty"`
	Relay bool   `json:"relay,omitempty"`
}

func (m *Manager) mcpConfigurer() (agentapi.MCPConfigurer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, name := range m.order {
		if info := m.infos[name]; !info.Available || !info.Capabilities.MCP {
			continue
		}
		if c, ok := m.providers[name].(agentapi.MCPConfigurer); ok {
			return c, true
		}
	}
	return nil, false
}

func mcpFailure(err error) error {
	var webErr *Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &webErr):
		return err
	case errors.Is(err, agentapi.ErrUnsupported):
		return newError(http.StatusConflict, "this provider cannot manage MCP servers")
	case errors.Is(err, agentapi.ErrClosed):
		return newError(http.StatusConflict, msgConversationClosed)
	}
	return newError(http.StatusBadGateway, "%s", shortError(err))
}

// MCPServers lists the configured servers without their secret values.
func (m *Manager) MCPServers() (MCPServers, error) {
	m.mu.Lock()
	out := MCPServers{StdioAllowed: m.settings.Terminal, Servers: []MCPServerView{}}
	m.mu.Unlock()
	c, ok := m.mcpConfigurer()
	if !ok {
		return out, nil
	}
	out.Available = true
	ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
	defer cancel()
	servers, err := c.MCPServers(ctx)
	if err != nil {
		return MCPServers{}, mcpFailure(err)
	}
	for _, s := range servers {
		out.Servers = append(out.Servers, MCPServerView{
			Name: s.Name, Type: s.Type, Command: s.Command, Args: s.Args, Cwd: s.Cwd, URL: s.URL,
			Env: secretKeys(s.Env), Headers: secretKeys(s.Headers), Enabled: s.Enabled, Source: s.Source,
		})
	}
	return out, nil
}

func secretKeys(values map[string]string) []MCPSecret {
	out := make([]MCPSecret, 0, len(values))
	for k := range values {
		out = append(out, MCPSecret{Key: k, Set: true})
	}
	slices.SortFunc(out, func(a, b MCPSecret) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// SaveMCPServer adds a server (existing false) or replaces the one named
// name. A server that runs a command needs Terminal on, checked under
// settingsMu so the setting cannot turn off while the change is written.
func (m *Manager) SaveMCPServer(name string, in MCPServerInput, existing bool) error {
	c, ok := m.mcpConfigurer()
	if !ok {
		return newError(http.StatusConflict, "no provider here manages MCP servers")
	}
	if existing {
		in.Name = name
	}
	cfg, err := checkMCPInput(in)
	if err != nil {
		return err
	}
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	m.mu.Lock()
	terminal := m.settings.Terminal
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
	defer cancel()
	servers, err := c.MCPServers(ctx)
	if err != nil {
		return mcpFailure(err)
	}
	i := slices.IndexFunc(servers, func(s agentapi.MCPServer) bool { return s.Name == cfg.Name })
	switch {
	case !existing && i >= 0:
		return newError(http.StatusConflict, "an MCP server named %q already exists", cfg.Name)
	case existing && i < 0:
		return newError(http.StatusNotFound, "MCP server not found")
	case existing && servers[i].Source != "user":
		return newError(http.StatusConflict, "this server is not in your MCP configuration and cannot be edited here")
	case !terminal && (cfg.Type == agentapi.MCPStdio || (existing && servers[i].Type == agentapi.MCPStdio)):
		return errStdioOff
	}
	var stored agentapi.MCPServerConfig
	if existing {
		stored = servers[i].MCPServerConfig
	}
	if cfg.Env, err = mergeSecrets(in.Env, stored.Env, "env"); err != nil {
		return err
	}
	if cfg.Headers, err = mergeSecrets(in.Headers, stored.Headers, "header"); err != nil {
		return err
	}
	if existing {
		err = c.UpdateMCPServer(ctx, cfg)
	} else {
		err = c.AddMCPServer(ctx, cfg)
	}
	return mcpFailure(err)
}

// checkMCPInput validates everything but the secret values' presence.
func checkMCPInput(in MCPServerInput) (agentapi.MCPServerConfig, error) {
	cfg := agentapi.MCPServerConfig{Name: strings.TrimSpace(in.Name), Type: in.Type}
	if !mcpNameExpr.MatchString(cfg.Name) {
		return cfg, newError(http.StatusBadRequest, "a name is 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	switch in.Type {
	case agentapi.MCPStdio:
		cfg.Command = strings.TrimSpace(in.Command)
		if cfg.Command == "" || len(cfg.Command) > maxMCPArg || hasControl(cfg.Command) {
			return cfg, newError(http.StatusBadRequest, "a command is required, on one line")
		}
		if len(in.Args) > maxMCPEntries {
			return cfg, newError(http.StatusBadRequest, "at most %d arguments", maxMCPEntries)
		}
		for _, a := range in.Args {
			if len(a) > maxMCPArg || hasControl(a) {
				return cfg, newError(http.StatusBadRequest, "an argument is too long or has a control character")
			}
		}
		cfg.Args = slices.Clone(in.Args)
		cfg.Cwd = strings.TrimSpace(in.Cwd)
		if cfg.Cwd != "" && (!filepath.IsAbs(cfg.Cwd) || hasControl(cfg.Cwd)) {
			return cfg, newError(http.StatusBadRequest, "the working folder must be an absolute path")
		}
		if len(in.Headers) > 0 {
			return cfg, newError(http.StatusBadRequest, "headers apply to remote servers only")
		}
	case agentapi.MCPHTTP, agentapi.MCPSSE:
		cfg.URL = strings.TrimSpace(in.URL)
		u, err := url.Parse(cfg.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || hasControl(cfg.URL) || len(cfg.URL) > maxMCPArg {
			return cfg, newError(http.StatusBadRequest, "the address must be an http:// or https:// URL")
		}
		if u.User != nil {
			return cfg, newError(http.StatusBadRequest, "put credentials in a header, not in the address: the address is shown to anyone signed in")
		}
		if in.Command != "" || len(in.Args) > 0 || in.Cwd != "" || len(in.Env) > 0 {
			return cfg, newError(http.StatusBadRequest, "a command, arguments and environment apply to command (stdio) servers only")
		}
	default:
		return cfg, newError(http.StatusBadRequest, "type must be stdio, http or sse")
	}
	return cfg, nil
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

// maxMCPName bounds the name of a listed server an action names.
const maxMCPName = 256

// listedMCPName reports whether name can name a server the provider lists.
// The provider's own configuration allows names uam would not give a new
// server (mcpNameExpr), so actions on a listed server take any such name.
func listedMCPName(name string) bool {
	return name != "" && len(name) <= maxMCPName && utf8.ValidString(name) && !hasControl(name)
}

// mergeSecrets builds the stored map from the request: a new value, or the
// stored one when Value is nil. Keys not in the request are dropped.
func mergeSecrets(in []MCPSecretInput, stored map[string]string, kind string) (map[string]string, error) {
	if len(in) > maxMCPEntries {
		return nil, newError(http.StatusBadRequest, "at most %d %s entries", maxMCPEntries, kind)
	}
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for _, e := range in {
		valid := envKeyExpr.MatchString(e.Key)
		if kind == "header" {
			valid = headerKeyExpr.MatchString(e.Key)
		}
		if !valid {
			return nil, newError(http.StatusBadRequest, "%q is not a valid %s name", e.Key, kind)
		}
		if _, dup := out[e.Key]; dup {
			return nil, newError(http.StatusBadRequest, "%s %q is listed twice", kind, e.Key)
		}
		if e.Value == nil {
			v, ok := stored[e.Key]
			if !ok {
				return nil, newError(http.StatusBadRequest, "%s %q needs a value", kind, e.Key)
			}
			out[e.Key] = v
			continue
		}
		if len(*e.Value) > maxMCPValue || strings.ContainsAny(*e.Value, "\r\n\x00") {
			return nil, newError(http.StatusBadRequest, "the value of %s %q is too long or spans lines", kind, e.Key)
		}
		out[e.Key] = *e.Value
	}
	return out, nil
}

// RemoveMCPServer and SetMCPServerEnabled change a user-configured server.
// Neither starts a new command, so neither needs Terminal.
func (m *Manager) RemoveMCPServer(name string) error {
	return m.changeMCPServer(name, func(ctx context.Context, c agentapi.MCPConfigurer) error { return c.RemoveMCPServer(ctx, name) })
}

func (m *Manager) SetMCPServerEnabled(name string, enabled bool) error {
	return m.changeMCPServer(name, func(ctx context.Context, c agentapi.MCPConfigurer) error {
		return c.SetMCPServerEnabled(ctx, name, enabled)
	})
}

func (m *Manager) changeMCPServer(name string, change func(context.Context, agentapi.MCPConfigurer) error) error {
	if !listedMCPName(name) {
		return newError(http.StatusNotFound, "MCP server not found")
	}
	c, ok := m.mcpConfigurer()
	if !ok {
		return newError(http.StatusConflict, "no provider here manages MCP servers")
	}
	ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
	defer cancel()
	servers, err := c.MCPServers(ctx)
	if err != nil {
		return mcpFailure(err)
	}
	i := slices.IndexFunc(servers, func(s agentapi.MCPServer) bool { return s.Name == name })
	if i < 0 {
		return newError(http.StatusNotFound, "MCP server not found")
	}
	if servers[i].Source != "user" {
		return newError(http.StatusConflict, "this server is not in your MCP configuration and cannot be changed here")
	}
	return mcpFailure(change(ctx, c))
}

// taskMCP opens the exact conversation of an active Task, as the command
// list does, and returns its MCP controls. Nothing is sent to the agent.
func (m *Manager) taskMCP(id string) (agentapi.MCPController, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	return m.taskMCPLocked(s)
}

// taskMCPLocked retains the caller's operation lock across a metadata read.
func (m *Manager) taskMCPLocked(s *webSession) (agentapi.MCPController, error) {
	m.mu.Lock()
	err := s.readOnlyLocked()
	supported := m.infos[s.provider].Capabilities.MCP
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !supported {
		return nil, newError(http.StatusConflict, "this provider cannot manage MCP servers")
	}
	if err = m.checkHolder(s); err != nil {
		return nil, err
	}
	if err = m.openLocked(s, true); err != nil {
		return nil, err
	}
	m.mu.Lock()
	conv := s.conv
	m.mu.Unlock()
	if conv == nil {
		return nil, newError(http.StatusConflict, msgConversationNotOpen)
	}
	c, ok := conv.(agentapi.MCPController)
	if !ok {
		return nil, newError(http.StatusConflict, "this provider cannot manage MCP servers")
	}
	return c, nil
}

// TaskMCP lists a Task's servers with their state and tools.
func (m *Manager) TaskMCP(id string) ([]agentapi.MCPStatus, error) {
	c, err := m.taskMCP(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
	defer cancel()
	out, err := c.MCPStatus(ctx)
	if err != nil {
		return nil, mcpFailure(err)
	}
	if out == nil {
		out = []agentapi.MCPStatus{}
	}
	return out, nil
}

// ReconnectTaskMCP refreshes a quiet Task's configuration in its open
// conversation. A provider without reload support retains the close/reopen
// fallback; a reload that failed may have partially applied changes.
func (m *Manager) ReconnectTaskMCP(id string) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	s.op.Lock()
	m.mu.Lock()
	err = s.readOnlyLocked()
	if err == nil {
		err = s.settleableLocked()
	}
	m.mu.Unlock()
	if err == nil {
		err = m.checkHolder(s)
	}
	m.mu.Lock()
	if err == nil {
		err = s.readOnlyLocked()
	}
	if err == nil {
		err = s.settleableLocked()
	}
	conv, gen := s.conv, s.gen
	m.mu.Unlock()
	if err != nil {
		s.op.Unlock()
		return err
	}
	if reload, ok := conv.(agentapi.CustomizationsReloader); ok {
		ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
		err = reload.ReloadCustomizations(ctx)
		cancel()
		if !errors.Is(err, agentapi.ErrUnsupported) {
			s.op.Unlock()
			return mcpFailure(err)
		}
	}
	m.mu.Lock()
	err = s.settleableLocked()
	if s.gen != gen {
		err = newError(http.StatusConflict, "the task's conversation changed while reloading configuration; try again")
	}
	conv = nil
	if err == nil && s.conv != nil {
		before := m.summaryLocked(s)
		conv = m.disconnectLocked(s)
		m.changedLocked(s, before)
	}
	m.mu.Unlock()
	s.op.Unlock()
	if err != nil {
		return err
	}
	if conv != nil {
		m.closeConversation(conv)
	}
	if err := m.flush(); err != nil {
		log.Warn("persist reconnected web session failed", "session", id, "error", err)
	}
	return nil
}

// TaskMCPAction runs enable, disable or restart on a Task's conversation.
// They last until the conversation closes.
func (m *Manager) TaskMCPAction(id, name, action string) error {
	if !listedMCPName(name) {
		return newError(http.StatusNotFound, "MCP server not found")
	}
	c, err := m.taskMCP(id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
	defer cancel()
	switch action {
	case "enable", "disable":
		err = c.SetMCPServerEnabled(ctx, name, action == "enable")
	case "restart":
		err = c.RestartMCPServer(ctx, name)
	default:
		return newError(http.StatusNotFound, "not found")
	}
	if err != nil {
		return mcpFailure(err)
	}
	return nil
}

// mcpSignIns are the sign-ins waiting for the browser's pasted callback
// address, by Task and server.
type mcpSignIns struct {
	mu      sync.Mutex
	pending map[string]*signIn
}

type signIn struct {
	// callback is the loopback redirect_uri of the authorization URL the
	// provider issued; state is its state parameter.
	callback *url.URL
	state    string
	expires  time.Time
}

func signInKey(id, name string) string { return id + "\x00" + name }

// sweepLocked drops the expired sign-ins, so ones abandoned, or left by a
// deleted Task, are not held until the service restarts. The caller holds mu.
func (p *mcpSignIns) sweepLocked(now time.Time) {
	maps.DeleteFunc(p.pending, func(_ string, s *signIn) bool { return now.After(s.expires) })
}

// StartMCPSignIn asks the Task's provider for a sign-in address. When its
// redirect is a loopback address of this host, the browser, which may be on
// another machine, can paste the address it ends on to FinishMCPSignIn.
func (m *Manager) StartMCPSignIn(id, name string, again bool) (MCPSignIn, error) {
	if !listedMCPName(name) {
		return MCPSignIn{}, newError(http.StatusNotFound, "MCP server not found")
	}
	c, err := m.taskMCP(id)
	if err != nil {
		return MCPSignIn{}, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
	defer cancel()
	address, err := c.MCPSignIn(ctx, name, again)
	if err != nil {
		return MCPSignIn{}, mcpFailure(err)
	}
	key := signInKey(id, name)
	m.signIns.mu.Lock()
	defer m.signIns.mu.Unlock()
	m.signIns.sweepLocked(time.Now())
	delete(m.signIns.pending, key)
	if address == "" {
		return MCPSignIn{}, nil
	}
	auth, err := url.Parse(address)
	if err != nil || (auth.Scheme != "https" && auth.Scheme != "http") {
		return MCPSignIn{}, newError(http.StatusBadGateway, "the provider returned an unusable sign-in address")
	}
	out := MCPSignIn{URL: address}
	callback, err := url.Parse(auth.Query().Get("redirect_uri"))
	if err == nil && loopbackCallback(callback) {
		if m.signIns.pending == nil {
			m.signIns.pending = map[string]*signIn{}
		}
		m.signIns.pending[key] = &signIn{callback: callback, state: auth.Query().Get("state"), expires: time.Now().Add(signInTTL)}
		out.Relay = true
	}
	return out, nil
}

// loopbackCallback reports whether u is a plain-HTTP address with a port on
// a loopback host, as a native app's OAuth redirect is.
func loopbackCallback(u *url.URL) bool {
	if u.Scheme != "http" || u.Port() == "" || u.User != nil {
		return false
	}
	return loopbackHost(u.Hostname())
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// FinishMCPSignIn passes the pasted callback address to the provider's
// loopback listener. Only the pending sign-in's exact listener (port and
// path) and state are accepted; the pending entry is used once.
func (m *Manager) FinishMCPSignIn(id, name, pasted string) error {
	key := signInKey(id, name)
	m.signIns.mu.Lock()
	m.signIns.sweepLocked(time.Now())
	p := m.signIns.pending[key]
	if p == nil {
		m.signIns.mu.Unlock()
		return newError(http.StatusConflict, "no sign-in is waiting for this server; start it again")
	}
	target, err := callbackTarget(p, strings.TrimSpace(pasted))
	if err != nil {
		m.signIns.mu.Unlock()
		return err
	}
	delete(m.signIns.pending, key)
	m.signIns.mu.Unlock()
	return relaySignIn(m.ctx, target)
}

// callbackTarget checks a pasted address against the pending sign-in and
// returns the address to request: the pending callback with the pasted
// query, so only the code and state come from the browser.
func callbackTarget(p *signIn, pasted string) (*url.URL, error) {
	u, err := url.Parse(pasted)
	if err != nil || u.Scheme != "http" || !loopbackHost(u.Hostname()) {
		return nil, newError(http.StatusBadRequest, "paste the whole address from the browser's address bar, starting with http://127.0.0.1 or http://localhost")
	}
	if u.Port() != p.callback.Port() || u.EscapedPath() != p.callback.EscapedPath() {
		return nil, newError(http.StatusBadRequest, "that address is not this sign-in's; paste the address the browser ended on after this sign-in")
	}
	q := u.Query()
	if q.Get("state") != p.state {
		return nil, newError(http.StatusBadRequest, "that address belongs to another sign-in; start the sign-in again")
	}
	if q.Get("code") == "" && q.Get("error") == "" {
		return nil, newError(http.StatusBadRequest, "that address has no sign-in result; finish signing in first")
	}
	target := *p.callback
	target.RawQuery = u.RawQuery
	target.Fragment = ""
	return &target, nil
}

// relaySignIn requests target, a validated loopback address, without
// following redirects and with a dialer that reaches loopback addresses
// only. Its query holds the sign-in code: it is never logged.
func relaySignIn(ctx context.Context, target *url.URL) error {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			return errors.New("not a loopback address")
		}
		return nil
	}}
	client := &http.Client{
		Timeout:       signInRelayLimit,
		Transport:     &http.Transport{DialContext: dialer.DialContext, Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return newError(http.StatusBadRequest, "that address cannot be used")
	}
	res, err := client.Do(req)
	if err != nil {
		return newError(http.StatusBadGateway, "the sign-in is no longer waiting on the server (it may have timed out); start it again")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	_ = res.Body.Close()
	if res.StatusCode >= 300 {
		return newError(http.StatusBadGateway, "the provider did not accept the sign-in (HTTP %d); start it again", res.StatusCode)
	}
	return nil
}

func (s *Server) mcpRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mcp", s.handleMCPServers)
	mux.HandleFunc("POST /api/mcp/servers", s.handleSaveMCPServer(false))
	mux.HandleFunc("PUT /api/mcp/servers/{name}", s.handleSaveMCPServer(true))
	mux.HandleFunc("PATCH /api/mcp/servers/{name}", s.handleEnableMCPServer)
	mux.HandleFunc("DELETE /api/mcp/servers/{name}", s.handleRemoveMCPServer)
	mux.HandleFunc("GET /api/sessions/{id}/mcp", s.handleTaskMCP)
	mux.HandleFunc("POST /api/sessions/{id}/mcp/reconnect", s.handleReconnectTaskMCP)
	mux.HandleFunc("GET /api/sessions/{id}/mcp/servers/{name}/tools", s.handleTaskMCPTools)
	mux.HandleFunc("POST /api/sessions/{id}/mcp/servers/{name}/enable", s.handleTaskMCPAction("enable"))
	mux.HandleFunc("POST /api/sessions/{id}/mcp/servers/{name}/disable", s.handleTaskMCPAction("disable"))
	mux.HandleFunc("POST /api/sessions/{id}/mcp/servers/{name}/restart", s.handleTaskMCPAction("restart"))
	mux.HandleFunc("POST /api/sessions/{id}/mcp/servers/{name}/sign-in", s.handleMCPSignIn)
	mux.HandleFunc("POST /api/sessions/{id}/mcp/servers/{name}/sign-in/finish", s.handleFinishMCPSignIn)
}

func (s *Server) handleMCPServers(w http.ResponseWriter, _ *http.Request) {
	out, err := s.m.MCPServers()
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSaveMCPServer(existing bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in MCPServerInput
		if !decodeBody(w, r, &in) {
			return
		}
		if err := s.m.SaveMCPServer(r.PathValue("name"), in, existing); err != nil {
			writeFailure(w, err)
			return
		}
		s.handleMCPServers(w, r)
	}
}

func (s *Server) handleEnableMCPServer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	if err := s.m.SetMCPServerEnabled(r.PathValue("name"), *in.Enabled); err != nil {
		writeFailure(w, err)
		return
	}
	s.handleMCPServers(w, r)
}

func (s *Server) handleRemoveMCPServer(w http.ResponseWriter, r *http.Request) {
	if err := s.m.RemoveMCPServer(r.PathValue("name")); err != nil {
		writeFailure(w, err)
		return
	}
	s.handleMCPServers(w, r)
}

func (s *Server) handleTaskMCP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("summary") == "1" {
		snapshot, err := s.m.TaskMCPStatus(r.PathValue("id"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"servers": snapshot.Servers, "mcp_status": snapshot})
		return
	}
	servers, err := s.m.TaskMCP(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": servers})
}

func (s *Server) handleTaskMCPTools(w http.ResponseWriter, r *http.Request) {
	tools, err := s.m.TaskMCPTools(r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

func (s *Server) handleTaskMCPAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.m.TaskMCPAction(r.PathValue("id"), r.PathValue("name"), action); err != nil {
			writeFailure(w, err)
			return
		}
		s.handleTaskMCP(w, r)
	}
}

func (s *Server) handleReconnectTaskMCP(w http.ResponseWriter, r *http.Request) {
	if err := s.m.ReconnectTaskMCP(r.PathValue("id")); err != nil {
		writeFailure(w, err)
		return
	}
	s.handleTaskMCP(w, r)
}

func (s *Server) handleMCPSignIn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Again bool `json:"again"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	out, err := s.m.StartMCPSignIn(r.PathValue("id"), r.PathValue("name"), in.Again)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFinishMCPSignIn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if err := s.m.FinishMCPSignIn(r.PathValue("id"), r.PathValue("name"), in.URL); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
