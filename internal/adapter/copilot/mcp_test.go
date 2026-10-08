package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestMCPEditKeepsFieldsUAMDoesNotEdit(t *testing.T) {
	timeout, source, path := int64(4000), rpc.MCPServerSourceUser, "/home/u/.copilot/mcp-config.json"
	clientID := "client"
	base := &rpc.MCPServerConfigHTTP{URL: "https://old.example", Tools: []string{"search"}, Timeout: &timeout, OauthClientID: &clientID, Source: &source, SourcePath: &path, ConfigWarnings: []string{"w"}}
	got := toRPCConfig(agentapi.MCPServerConfig{Type: agentapi.MCPSSE, URL: "https://new.example", Headers: map[string]string{"X": "y"}}, base)
	h, ok := got.(*rpc.MCPServerConfigHTTP)
	if !ok {
		t.Fatalf("config = %T", got)
	}
	if h.URL != "https://new.example" || h.Type == nil || *h.Type != rpc.MCPServerConfigHTTPTypeSSE || h.Headers["X"] != "y" {
		t.Fatalf("transport = %+v", h)
	}
	if !slices.Equal(h.Tools, []string{"search"}) || h.Timeout == nil || *h.Timeout != 4000 || h.OauthClientID == nil {
		t.Fatalf("kept fields lost: %+v", h)
	}
	if h.Source != nil || h.SourcePath != nil || h.ConfigWarnings != nil {
		t.Fatalf("load metadata written back: %+v", h)
	}
	if base.URL != "https://old.example" {
		t.Fatal("the stored entry was changed in place")
	}
	// Another transport starts from a fresh entry with every tool.
	s, ok := toRPCConfig(agentapi.MCPServerConfig{Type: agentapi.MCPStdio, Command: "node", Args: []string{"a.js"}, Cwd: "/srv"}, base).(*rpc.MCPServerConfigStdio)
	if !ok || s.Command != "node" || !slices.Equal(s.Args, []string{"a.js"}) || s.Cwd == nil || *s.Cwd != "/srv" || !slices.Equal(s.Tools, []string{"*"}) || s.Timeout != nil {
		t.Fatalf("stdio = %+v", s)
	}
	raw, err := json.Marshal(s)
	if err != nil || string(raw) == "" {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil || back["type"] != "stdio" {
		t.Fatalf("stdio JSON = %s", raw)
	}
}

func TestMCPConfigReadsBothTransports(t *testing.T) {
	sse := rpc.MCPServerConfigHTTPTypeSSE
	cwd := "/w"
	if got := fromRPCConfig("a", &rpc.MCPServerConfigHTTP{URL: "https://x", Type: &sse, Headers: map[string]string{"K": "v"}}); got.Type != agentapi.MCPSSE || got.URL != "https://x" || got.Headers["K"] != "v" {
		t.Fatalf("http = %+v", got)
	}
	if got := fromRPCConfig("a", &rpc.MCPServerConfigHTTP{URL: "https://x"}); got.Type != agentapi.MCPHTTP {
		t.Fatalf("untyped remote = %+v", got)
	}
	if got := fromRPCConfig("b", &rpc.MCPServerConfigStdio{Command: "c", Cwd: &cwd, Env: map[string]string{"E": "1"}}); got.Type != agentapi.MCPStdio || got.Cwd != "/w" || got.Env["E"] != "1" {
		t.Fatalf("stdio = %+v", got)
	}
}

func TestMCPErrorTextDropsTheRPCWrapping(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"JSON-RPC Error -32603: Request session.mcp.restartServer failed with message: failed to spawn MCP server process", "failed to spawn MCP server process"},
		// The wrapping goes before the text is clipped, and the rest is trimmed.
		{"JSON-RPC Error -32603: Request " + strings.Repeat("x", maxErrorText) + " failed with message:  spawn failed \n", "spawn failed"},
	} {
		if got := rpcText(errors.New(tc.in)); got != tc.want {
			t.Errorf("rpcText = %q, want %q", got, tc.want)
		}
	}
}

// Every server state the CLI reports maps to one the contract names.
func TestMCPStatusesAreTheContracts(t *testing.T) {
	for _, s := range []rpc.MCPServerStatus{rpc.MCPServerStatusConnected, rpc.MCPServerStatusFailed, rpc.MCPServerStatusNeedsAuth, rpc.MCPServerStatusPending,
		rpc.MCPServerStatusDisabled, rpc.MCPServerStatusStopped, rpc.MCPServerStatusNotConfigured} {
		if _, ok := mcpStatuses[s]; !ok {
			t.Errorf("state %q has no contract state", s)
		}
	}
	if got := mcpStatus(rpc.MCPServerStatusNotConfigured); got != agentapi.MCPNotConfigured {
		t.Fatalf("not configured = %q", got)
	}
}

// mcpFakeClient adds the user-wide MCP surface to fakeClient and records the
// folder discovery ran in.
type mcpFakeClient struct {
	*fakeClient
	discoverDirs []string
}

func (f *mcpFakeClient) MCPConfigList(context.Context) (map[string]rpc.MCPSerializableServerConfig, error) {
	return map[string]rpc.MCPSerializableServerConfig{}, nil
}

func (f *mcpFakeClient) MCPDiscover(_ context.Context, workdir string) ([]rpc.DiscoveredMCPServer, error) {
	f.discoverDirs = append(f.discoverDirs, workdir)
	return []rpc.DiscoveredMCPServer{{Name: "github", Source: rpc.MCPServerSourceBuiltin, Enabled: true}}, nil
}

func (f *mcpFakeClient) MCPConfigAdd(context.Context, string, rpc.MCPSerializableServerConfig) error {
	return nil
}

func (f *mcpFakeClient) MCPConfigUpdate(context.Context, string, rpc.MCPSerializableServerConfig) error {
	return nil
}
func (f *mcpFakeClient) MCPConfigRemove(context.Context, string) error       { return nil }
func (f *mcpFakeClient) MCPConfigEnable(context.Context, string, bool) error { return nil }
func (f *mcpFakeClient) MCPConfigReload(context.Context) error               { return nil }

// Discovery names an existing folder, the user's home, rather than leaving
// the CLI to use its own working directory, which may have been deleted.
func TestMCPServersDiscoverInTheHomeFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	gone := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	fc := &mcpFakeClient{fakeClient: &fakeClient{}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	servers, err := p.MCPServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "github" {
		t.Fatalf("servers = %+v", servers)
	}
	if len(fc.discoverDirs) != 1 || fc.discoverDirs[0] != home {
		t.Fatalf("discovery folders = %q, want [%q]", fc.discoverDirs, home)
	}
}

// Tasks and Utility calls leave the built-in GitHub MCP server off, on create
// and on resume, until it is turned on.
func TestGitHubMCPServerOffUntilTurnedOn(t *testing.T) {
	h := openWeb(t)
	h.fc.mu.Lock()
	h.fc.reply = func(context.Context, copilot.MessageOptions) (string, error) { return "ok", nil }
	h.fc.mu.Unlock()
	open := func(session, conversation string) {
		t.Helper()
		if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: session, ConversationID: conversation, Workdir: "/work", Events: &recSink{}}); err != nil {
			t.Fatal(err)
		}
		if _, err := h.p.RunUtility(context.Background(), agentapi.UtilityRequest{Model: "gpt-6-luna", Purpose: "title", System: "Write a title.", Prompt: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	open("s-2", "s-1")
	off := []string{githubMCPServer}
	for name, got := range map[string][]string{"create": h.fc.create[0].DisabledMCPServers, "resume": h.fc.resume[0].DisabledMCPServers, "utility": h.fc.create[1].DisabledMCPServers} {
		if !slices.Equal(got, off) {
			t.Fatalf("%s disabled servers = %q, want %q", name, got, off)
		}
	}
	h.p.SetGitHubMCP(true)
	open("s-3", "s-1")
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-4", Workdir: "/work", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]string{"resume": h.fc.resume[1].DisabledMCPServers, "utility": h.fc.create[2].DisabledMCPServers, "create": h.fc.create[3].DisabledMCPServers} {
		if got != nil {
			t.Fatalf("%s disabled servers = %q with the server on", name, got)
		}
	}
}

// mcpSwitchSession is a session that reports each MCP server it turns on or
// off.
type mcpSwitchSession struct {
	*fakeSession
	switched chan string
}

func (s *mcpSwitchSession) MCPList(context.Context) ([]rpc.MCPServer, error) { return nil, nil }
func (s *mcpSwitchSession) MCPTools(context.Context, string) ([]rpc.MCPTools, error) {
	return nil, nil
}
func (s *mcpSwitchSession) MCPRestart(context.Context, string) error { return nil }
func (s *mcpSwitchSession) MCPLogin(context.Context, *rpc.MCPOauthLoginRequest) (*rpc.MCPOauthLoginResult, error) {
	return nil, nil
}
func (s *mcpSwitchSession) MCPEnable(_ context.Context, name string, enabled bool) error {
	s.switched <- fmt.Sprintf("%s=%v", name, enabled)
	return nil
}

// switchingClient creates mcpSwitchSessions and runs during while one opens.
type switchingClient struct {
	*fakeClient
	during   func()
	switched chan string
}

func (c *switchingClient) CreateSession(ctx context.Context, cfg *copilot.SessionConfig) (sdkSession, error) {
	s, err := c.fakeClient.CreateSession(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if c.during != nil {
		c.during()
	}
	return &mcpSwitchSession{fakeSession: s.(*fakeSession), switched: c.switched}, nil
}

// Turning the server on or off reaches the open sessions, one turned while
// it opened too; turning it to what it is changes nothing, and after quick
// changes the session ends as the setting does.
func TestGitHubMCPSwitchReachesOpenSessions(t *testing.T) {
	fc := &switchingClient{fakeClient: &fakeClient{}, switched: make(chan string, 8)}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	fc.during = func() { p.SetGitHubMCP(true) }
	if _, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-1", Workdir: "/work", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	fc.during = nil
	if got := fc.create[0].DisabledMCPServers; !slices.Equal(got, []string{githubMCPServer}) {
		t.Fatalf("opened with %q", got)
	}
	next := func(want string) {
		t.Helper()
		select {
		case got := <-fc.switched:
			if got != want {
				t.Fatalf("switched %s, want %s", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no switch, want %s", want)
		}
	}
	next(githubMCPServer + "=true")
	p.SetGitHubMCP(true)
	p.SetGitHubMCP(false)
	next(githubMCPServer + "=false")
	select {
	case got := <-fc.switched:
		t.Fatalf("extra switch %s", got)
	case <-time.After(100 * time.Millisecond):
	}
	for i := range 6 {
		p.SetGitHubMCP(i%2 == 0)
	}
	last := ""
	for quiet := false; !quiet; {
		select {
		case last = <-fc.switched:
		case <-time.After(200 * time.Millisecond):
			quiet = true
		}
	}
	if last != githubMCPServer+"=false" {
		t.Fatalf("last switch %q, want the setting, off", last)
	}
}
