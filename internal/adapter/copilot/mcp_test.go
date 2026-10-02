package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
