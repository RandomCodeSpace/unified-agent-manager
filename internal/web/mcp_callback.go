package web

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const mcpCallbackPath = "/api/mcp/oauth/callback"

// mcpCallbackURI never trusts forwarded headers or the proxy's internal URL.
func (s *Server) mcpCallbackURI(host string) string {
	if origin := s.mcpCallbackOrigins[strings.ToLower(host)]; origin != "" {
		return origin + mcpCallbackPath
	}
	return ""
}

func (m *Manager) startMCPSignIn(id, name string, again bool, redirectURI string) (MCPSignIn, error) {
	if redirectURI == "" {
		return m.StartMCPSignIn(id, name, again)
	}
	if !listedMCPName(name) {
		return MCPSignIn{}, newError(http.StatusNotFound, "MCP server not found")
	}
	s, err := m.lookup(id)
	if err != nil {
		return MCPSignIn{}, err
	}
	out, err := func() (MCPSignIn, error) {
		s.op.Lock()
		defer s.op.Unlock()
		c, err := m.taskMCPLocked(s)
		if err != nil {
			return MCPSignIn{}, err
		}
		callback, ok := c.(agentapi.MCPCallbackController)
		if !ok {
			return MCPSignIn{}, agentapi.ErrUnsupported
		}
		return m.startMCPCallbackLocked(s, name, again, redirectURI, callback)
	}()
	// Unsupported is effect-free. A supported failure must not start another login.
	if errors.Is(err, agentapi.ErrUnsupported) {
		return m.StartMCPSignIn(id, name, again)
	}
	return out, err
}

func (m *Manager) startMCPCallbackLocked(s *webSession, name string, again bool, redirectURI string, c agentapi.MCPCallbackController) (MCPSignIn, error) {
	callback, _ := url.Parse(redirectURI)
	m.mu.Lock()
	p := &signIn{callback: callback, id: s.id, name: name, convID: s.convID, gen: s.gen, public: true, expires: time.Now().Add(signInTTL)}
	m.mu.Unlock()
	key := signInKey(s.id, name)
	// Reserve capacity before asking the SDK to start an authorization. A new
	// attempt for this Task/server replaces the abandoned attempt immediately.
	m.signIns.mu.Lock()
	m.signIns.sweepLocked(time.Now())
	if m.signIns.pending[key] == nil && len(m.signIns.pending) >= maxMCPEntries {
		m.signIns.mu.Unlock()
		return MCPSignIn{}, newError(http.StatusConflict, "too many MCP sign-ins are waiting; finish one or try again later")
	}
	if m.signIns.pending == nil {
		m.signIns.pending = map[string]*signIn{}
	}
	m.signIns.pending[key] = p
	m.signIns.mu.Unlock()
	keep := false
	defer func() {
		if !keep {
			m.signIns.mu.Lock()
			if m.signIns.pending[key] == p {
				delete(m.signIns.pending, key)
			}
			m.signIns.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
	defer cancel()
	started, err := c.MCPSignInCallback(ctx, name, again, redirectURI)
	if errors.Is(err, agentapi.ErrUnsupported) {
		return MCPSignIn{}, agentapi.ErrUnsupported
	}
	if err != nil {
		return MCPSignIn{}, newError(http.StatusBadGateway, "the provider could not start MCP sign-in; start it again")
	}
	m.mu.Lock()
	current := !m.closed && !s.removed && s.conv != nil && s.gen == p.gen && s.convID == p.convID
	m.mu.Unlock()
	if !current {
		return MCPSignIn{}, newError(http.StatusConflict, msgConversationClosed)
	}
	if started.URL == "" {
		return MCPSignIn{}, nil
	}
	auth, err := url.Parse(started.URL)
	if err != nil || (auth.Scheme != "http" && auth.Scheme != "https") || auth.Host == "" || auth.User != nil || auth.Fragment != "" || len(started.URL) > maxMCPValue || hasControl(started.URL) {
		return MCPSignIn{}, newError(http.StatusBadGateway, "the provider returned an unusable sign-in address")
	}
	q, err := url.ParseQuery(auth.RawQuery)
	if err != nil || started.AuthorizationID == "" || len(started.AuthorizationID) > maxMCPValue || hasControl(started.AuthorizationID) || len(q["state"]) != 1 || q.Get("state") != started.AuthorizationID || len(q["redirect_uri"]) != 1 || q.Get("redirect_uri") != redirectURI {
		return MCPSignIn{}, newError(http.StatusBadGateway, "the provider returned an unusable sign-in address")
	}
	m.signIns.mu.Lock()
	// SDK state cannot select another pending Task or server, even if a faulty
	// provider repeats it. Reject the new attempt rather than choose one.
	for _, other := range m.signIns.pending {
		if other != p && other.public && other.state == started.AuthorizationID {
			m.signIns.mu.Unlock()
			return MCPSignIn{}, newError(http.StatusBadGateway, "the provider returned an unusable sign-in address")
		}
	}
	p.state = started.AuthorizationID
	keep = true
	m.signIns.mu.Unlock()
	return MCPSignIn{URL: started.URL, Callback: true}, nil
}

// completeMCPCallback forwards the full trusted public URL unchanged to the
// SDK. UAM neither exchanges codes nor stores provider OAuth credentials.
func (m *Manager) completeMCPCallback(host, rawQuery string) error {
	if len(rawQuery) > maxMCPValue {
		return newError(http.StatusBadRequest, "this sign-in callback cannot be used; start it again")
	}
	q, err := url.ParseQuery(rawQuery)
	code := len(q["code"]) == 1 && q.Get("code") != ""
	denied := len(q["error"]) == 1 && q.Get("error") != ""
	if err != nil || len(q["state"]) != 1 || q.Get("state") == "" || hasControl(q.Get("state")) || code == denied || len(q["code"]) > 1 || len(q["error"]) > 1 {
		return newError(http.StatusBadRequest, "this sign-in callback cannot be used; start it again")
	}
	var p *signIn
	m.signIns.mu.Lock()
	m.signIns.sweepLocked(time.Now())
	for _, pending := range m.signIns.pending {
		if pending.public && pending.state == q.Get("state") {
			p = pending
			break
		}
	}
	m.signIns.mu.Unlock()
	if p == nil || !strings.EqualFold(host, p.callback.Host) {
		return newError(http.StatusConflict, "no matching MCP sign-in is waiting; start it again")
	}
	s, err := m.lookup(p.id)
	if err != nil {
		return newError(http.StatusConflict, "this MCP sign-in is no longer waiting; start it again")
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.signIns.mu.Lock()
	key := signInKey(p.id, p.name)
	waiting := m.signIns.pending[key] == p && !time.Now().After(p.expires)
	if waiting {
		delete(m.signIns.pending, key)
	}
	m.signIns.mu.Unlock()
	if !waiting {
		return newError(http.StatusConflict, "this MCP sign-in is no longer waiting; start it again")
	}
	m.mu.Lock()
	current := !m.closed && !s.removed && s.conv != nil && s.gen == p.gen && s.convID == p.convID
	readOnly := s.readOnlyLocked()
	m.mu.Unlock()
	if !current || readOnly != nil {
		return newError(http.StatusConflict, "this MCP sign-in is no longer waiting; start it again")
	}
	if err := m.checkHolder(s); err != nil {
		return newError(http.StatusConflict, "this MCP sign-in is no longer waiting; start it again")
	}
	m.mu.Lock()
	current = !m.closed && !s.removed && s.conv != nil && s.gen == p.gen && s.convID == p.convID
	controller, supported := s.conv.(agentapi.MCPCallbackController)
	m.mu.Unlock()
	if !current || !supported {
		return newError(http.StatusConflict, "this MCP sign-in is no longer waiting; start it again")
	}
	target := *p.callback
	target.RawQuery = rawQuery
	ctx, cancel := context.WithTimeout(m.ctx, mcpTimeout)
	defer cancel()
	if err := controller.CompleteMCPSignIn(ctx, p.state, target.String()); err != nil {
		return newError(http.StatusBadGateway, "the provider could not complete MCP sign-in; start it again")
	}
	m.mu.Lock()
	current = !m.closed && !s.removed && s.conv != nil && s.gen == p.gen && s.convID == p.convID
	m.mu.Unlock()
	if !current {
		return newError(http.StatusConflict, "this MCP sign-in is no longer waiting; start it again")
	}
	if q.Get("error") != "" {
		return newError(http.StatusBadRequest, "MCP sign-in was not approved; start it again")
	}
	return nil
}

func (s *Server) handleMCPCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	status, message := http.StatusOK, "MCP sign-in completed. Return to your Task; use Refresh if its status has not updated. You can close this tab."
	if err := s.m.completeMCPCallback(r.Host, r.URL.RawQuery); err != nil {
		status, message = errorStatus(err)
	}
	w.Header().Set(headerContentType, "text/html; charset=utf-8")
	w.WriteHeader(status)
	// Messages are fixed application text, never provider query values or errors.
	_, _ = io.WriteString(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>MCP sign-in</title><body><h1>MCP sign-in</h1><p>`+html.EscapeString(message)+`</p></body></html>`)
}
