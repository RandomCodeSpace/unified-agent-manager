package web

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// mcpProvider is a fake provider with a user-wide MCP configuration.
type mcpProvider struct {
	*agenttest.Provider
	mu      sync.Mutex
	servers map[string]agentapi.MCPServer
	writes  int
}

func (p *mcpProvider) MCPServers(context.Context) ([]agentapi.MCPServer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := slices.Collect(maps.Values(p.servers))
	slices.SortFunc(out, func(a, b agentapi.MCPServer) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (p *mcpProvider) AddMCPServer(_ context.Context, cfg agentapi.MCPServerConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes++
	p.servers[cfg.Name] = agentapi.MCPServer{MCPServerConfig: cfg, Enabled: true, Source: "user"}
	return nil
}

func (p *mcpProvider) UpdateMCPServer(_ context.Context, cfg agentapi.MCPServerConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes++
	s := p.servers[cfg.Name]
	s.MCPServerConfig = cfg
	p.servers[cfg.Name] = s
	return nil
}

func (p *mcpProvider) RemoveMCPServer(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes++
	delete(p.servers, name)
	return nil
}

func (p *mcpProvider) SetMCPServerEnabled(_ context.Context, name string, enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes++
	s := p.servers[name]
	s.Enabled = enabled
	p.servers[name] = s
	return nil
}

func (p *mcpProvider) server(name string) agentapi.MCPServer {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.servers[name]
}

func newMCPServer(t *testing.T) (*testServer, *mcpProvider, reqOpt) {
	t.Helper()
	caps := allCaps
	caps.MCP = true
	prov := &mcpProvider{Provider: agenttest.NewProvider("fake", caps), servers: map[string]agentapi.MCPServer{
		"docs":   {MCPServerConfig: agentapi.MCPServerConfig{Name: "docs", Type: agentapi.MCPHTTP, URL: "https://mcp.example/docs", Headers: map[string]string{"Authorization": "Bearer secret-header-value"}}, Enabled: true, Source: "user"},
		"local":  {MCPServerConfig: agentapi.MCPServerConfig{Name: "local", Type: agentapi.MCPStdio, Command: "node", Args: []string{"server.js"}, Env: map[string]string{"API_KEY": "secret-env-value", "MODE": "x"}}, Enabled: false, Source: "user"},
		"plugin": {MCPServerConfig: agentapi.MCPServerConfig{Name: "plugin", Type: agentapi.MCPStdio}, Enabled: true, Source: "plugin"},
	}}
	m := startManager(t, openTestStore(t), prov)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m, prov: prov.Provider}
	return ts, prov, ts.login(t, "127.0.0.1:8260")
}

func mcpList(t *testing.T, w *httptest.ResponseRecorder) MCPServers {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var out MCPServers
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMCPListNeverReturnsSecretValues(t *testing.T) {
	ts, _, auth := newMCPServer(t)
	w := ts.do(http.MethodGet, "/api/mcp", "", auth)
	if strings.Contains(w.Body.String(), "secret-") {
		t.Fatal("the list carries a secret value")
	}
	got := mcpList(t, w)
	if !got.Available || got.StdioAllowed || len(got.Servers) != 3 {
		t.Fatalf("list = %+v", got)
	}
	local := got.Servers[1]
	if local.Name != "local" || local.Enabled || local.Command != "node" || !slices.Equal(local.Env, []MCPSecret{{Key: "API_KEY", Set: true}, {Key: "MODE", Set: true}}) {
		t.Fatalf("local = %+v", local)
	}
	if docs := got.Servers[0]; len(docs.Headers) != 1 || docs.Headers[0].Key != "Authorization" || !docs.Headers[0].Set {
		t.Fatalf("docs = %+v", docs)
	}
}

func TestMCPStdioNeedsTerminal(t *testing.T) {
	ts, prov, auth := newMCPServer(t)
	stdio := `{"name":"echo","type":"stdio","command":"/tmp/echo-server","args":["--fast"]}`
	if w := ts.do(http.MethodPost, "/api/mcp/servers", stdio, auth); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Terminal") {
		t.Fatalf("stdio add with Terminal off = %d %s", w.Code, w.Body.String())
	}
	// Editing an existing command server is refused too, even into a remote one.
	if w := ts.do(http.MethodPut, "/api/mcp/servers/local", `{"type":"http","url":"https://x.example/mcp"}`, auth); w.Code != http.StatusForbidden {
		t.Fatalf("stdio edit with Terminal off = %d", w.Code)
	}
	if prov.writes != 0 {
		t.Fatalf("refused changes wrote %d times", prov.writes)
	}
	// Remote servers need no Terminal; enabling and removing a command server neither.
	if w := ts.do(http.MethodPost, "/api/mcp/servers", `{"name":"remote","type":"sse","url":"https://x.example/sse","headers":[{"key":"X-Key","value":"k"}]}`, auth); w.Code != http.StatusOK {
		t.Fatalf("remote add = %d %s", w.Code, w.Body.String())
	}
	if w := ts.do(http.MethodPatch, "/api/mcp/servers/local", `{"enabled":true}`, auth); w.Code != http.StatusOK || !prov.server("local").Enabled {
		t.Fatalf("enable = %d", w.Code)
	}
	on := true
	if _, err := ts.m.UpdateSettings(SettingsPatch{Terminal: &on}); err != nil {
		t.Fatal(err)
	}
	if w := ts.do(http.MethodPost, "/api/mcp/servers", stdio, auth); w.Code != http.StatusOK || !mcpList(t, w).StdioAllowed {
		t.Fatalf("stdio add with Terminal on = %d %s", w.Code, w.Body.String())
	}
	if got := prov.server("echo"); got.Command != "/tmp/echo-server" || !slices.Equal(got.Args, []string{"--fast"}) {
		t.Fatalf("stored = %+v", got)
	}
	if w := ts.do(http.MethodDelete, "/api/mcp/servers/echo", "", auth); w.Code != http.StatusOK || prov.server("echo").Name != "" {
		t.Fatalf("remove = %d", w.Code)
	}
}

func TestMCPEditKeepsOrReplacesSecretValues(t *testing.T) {
	ts, prov, auth := newMCPServer(t)
	on := true
	if _, err := ts.m.UpdateSettings(SettingsPatch{Terminal: &on}); err != nil {
		t.Fatal(err)
	}
	// API_KEY kept (no value), MODE dropped, NEW added.
	body := `{"type":"stdio","command":"node","args":["v2.js"],"env":[{"key":"API_KEY"},{"key":"NEW","value":"n"}]}`
	if w := ts.do(http.MethodPut, "/api/mcp/servers/local", body, auth); w.Code != http.StatusOK {
		t.Fatalf("edit = %d %s", w.Code, w.Body.String())
	}
	if got := prov.server("local").Env; !maps.Equal(got, map[string]string{"API_KEY": "secret-env-value", "NEW": "n"}) {
		t.Fatalf("env = %v", got)
	}
	if w := ts.do(http.MethodPut, "/api/mcp/servers/docs", `{"type":"http","url":"https://mcp.example/v2","headers":[{"key":"Authorization","value":"Bearer new"}]}`, auth); w.Code != http.StatusOK {
		t.Fatalf("header edit = %d", w.Code)
	}
	if got := prov.server("docs"); got.URL != "https://mcp.example/v2" || got.Headers["Authorization"] != "Bearer new" {
		t.Fatalf("docs = %+v", got)
	}
	// A key with no value that is not stored yet is refused.
	if w := ts.do(http.MethodPut, "/api/mcp/servers/docs", `{"type":"http","url":"https://mcp.example/v2","headers":[{"key":"X-Other"}]}`, auth); w.Code != http.StatusBadRequest {
		t.Fatalf("missing value = %d", w.Code)
	}
	if w := ts.do(http.MethodPut, "/api/mcp/servers/plugin", `{"type":"http","url":"https://x.example"}`, auth); w.Code != http.StatusConflict {
		t.Fatalf("plugin edit = %d", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/mcp/servers", `{"name":"docs","type":"http","url":"https://x.example"}`, auth); w.Code != http.StatusConflict {
		t.Fatalf("duplicate add = %d", w.Code)
	}
}

func TestMCPInputValidation(t *testing.T) {
	for _, in := range []MCPServerInput{
		{Name: "", Type: "http", URL: "https://x.example"},
		{Name: "bad name", Type: "http", URL: "https://x.example"},
		{Name: "a", Type: "ws", URL: "https://x.example"},
		{Name: "a", Type: "http", URL: "ftp://x.example"},
		{Name: "a", Type: "http", URL: "https://user:pass@x.example"},
		{Name: "a", Type: "http", URL: "https://x.example", Command: "sh"},
		{Name: "a", Type: "stdio"},
		{Name: "a", Type: "stdio", Command: "sh\nrm"},
		{Name: "a", Type: "stdio", Command: "sh", Cwd: "relative"},
		{Name: "a", Type: "stdio", Command: "sh", Headers: []MCPSecretInput{{Key: "X"}}},
	} {
		if _, err := checkMCPInput(in); err == nil {
			t.Errorf("accepted %+v", in)
		}
	}
	v := "x\ny"
	for _, e := range [][]MCPSecretInput{{{Key: "1BAD", Value: &v}}, {{Key: "A", Value: &v}}} {
		if _, err := mergeSecrets(e, nil, "env"); err == nil {
			t.Errorf("accepted env %+v", e)
		}
	}
	if _, err := mergeSecrets([]MCPSecretInput{{Key: "Bad Header"}}, nil, "header"); err == nil {
		t.Error("accepted a header name with a space")
	}
}

// fakeCallback mimics the provider's loopback listener: it records the
// query it was called with.
func fakeCallback(t *testing.T, status int) (*httptest.Server, *url.URL, chan url.Values) {
	t.Helper()
	got := make(chan url.Values, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		got <- r.URL.Query()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	callback, _ := url.Parse(srv.URL + "/callback")
	return srv, callback, got
}

func pendSignIn(m *Manager, id, name string, callback *url.URL, expires time.Time) {
	m.signIns.mu.Lock()
	defer m.signIns.mu.Unlock()
	if m.signIns.pending == nil {
		m.signIns.pending = map[string]*signIn{}
	}
	m.signIns.pending[signInKey(id, name)] = &signIn{callback: callback, state: "s1", expires: expires}
}

func TestMCPSignInRelayForwardsOnlyThePendingCallbackOnce(t *testing.T) {
	m, _, _ := newTestManager(t)
	_, callback, got := fakeCallback(t, http.StatusOK)
	port := callback.Port()
	pendSignIn(m, "task", "docs", callback, time.Now().Add(time.Minute))
	for _, bad := range []string{
		"https://127.0.0.1:" + port + "/callback?code=c&state=s1",
		"http://evil.example:" + port + "/callback?code=c&state=s1",
		"http://127.0.0.1:1/callback?code=c&state=s1",
		"http://127.0.0.1:" + port + "/other?code=c&state=s1",
		"http://127.0.0.1:" + port + "/callback?code=c&state=other",
		"http://127.0.0.1:" + port + "/callback?state=s1",
		"not a url",
	} {
		if err := m.FinishMCPSignIn("task", "docs", bad); err == nil {
			t.Errorf("relayed %q", bad)
		}
	}
	if len(got) != 0 {
		t.Fatal("a refused address reached the listener")
	}
	if err := m.FinishMCPSignIn("other-task", "docs", "http://127.0.0.1:"+port+"/callback?code=c&state=s1"); err == nil {
		t.Fatal("another Task's sign-in was relayed")
	}
	// localhost names the same listener; the query is passed as it came.
	if err := m.FinishMCPSignIn("task", "docs", " http://localhost:"+port+"/callback?code=the-code&state=s1 "); err != nil {
		t.Fatal(err)
	}
	if q := <-got; q.Get("code") != "the-code" || q.Get("state") != "s1" {
		t.Fatalf("relayed query = %v", q)
	}
	if err := m.FinishMCPSignIn("task", "docs", "http://127.0.0.1:"+port+"/callback?code=c&state=s1"); err == nil {
		t.Fatal("a sign-in was relayed twice")
	}
}

func TestMCPSignInRelayExpiresAndReportsRefusals(t *testing.T) {
	m, _, _ := newTestManager(t)
	_, callback, got := fakeCallback(t, http.StatusBadRequest)
	pendSignIn(m, "task", "docs", callback, time.Now().Add(-time.Second))
	pasted := fmt.Sprintf("http://127.0.0.1:%s/callback?code=c&state=s1", callback.Port())
	if err := m.FinishMCPSignIn("task", "docs", pasted); err == nil || !strings.Contains(err.Error(), "start it again") {
		t.Fatalf("expired sign-in = %v", err)
	}
	pendSignIn(m, "task", "docs", callback, time.Now().Add(time.Minute))
	if err := m.FinishMCPSignIn("task", "docs", pasted); err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("refused callback = %v", err)
	}
	<-got
	// The listener gone: the relay says so instead of hanging.
	closed, callback2, _ := fakeCallback(t, http.StatusOK)
	closed.Close()
	pendSignIn(m, "task", "docs", callback2, time.Now().Add(time.Minute))
	if err := m.FinishMCPSignIn("task", "docs", fmt.Sprintf("http://127.0.0.1:%s/callback?code=c&state=s1", callback2.Port())); err == nil || !strings.Contains(err.Error(), "no longer waiting") {
		t.Fatalf("gone listener = %v", err)
	}
}

func TestMCPSignInRelayDoesNotFollowRedirects(t *testing.T) {
	m, _, _ := newTestManager(t)
	reached := make(chan struct{}, 1)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached <- struct{}{} }))
	t.Cleanup(elsewhere.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	callback, _ := url.Parse(srv.URL + "/callback")
	pendSignIn(m, "task", "docs", callback, time.Now().Add(time.Minute))
	if err := m.FinishMCPSignIn("task", "docs", srv.URL+"/callback?code=c&state=s1"); err == nil {
		t.Fatal("a redirect counted as a finished sign-in")
	}
	select {
	case <-reached:
		t.Fatal("the relay followed a redirect")
	default:
	}
}

func TestLoopbackCallback(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://127.0.0.1:4321/callback": true,
		"http://localhost:4321/":         true,
		"http://[::1]:4321/cb":           true,
		"http://127.0.0.1/callback":      false,
		"https://127.0.0.1:4321/":        false,
		"http://example.com:4321/":       false,
		"http://u@127.0.0.1:4321/":       false,
	} {
		u, _ := url.Parse(raw)
		if got := loopbackCallback(u); got != want {
			t.Errorf("loopbackCallback(%s) = %v", raw, got)
		}
	}
}
