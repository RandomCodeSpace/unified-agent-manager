package copilot

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

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
