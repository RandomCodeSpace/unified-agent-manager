package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestMCPNativeStatusSupportIsKnownBeforeFirstEvent(t *testing.T) {
	fc := &switchingClient{fakeClient: &fakeClient{}, switched: make(chan string, 8)}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	sink := &recSink{}
	conv, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "native", Workdir: "/work", Events: sink})
	if err != nil {
		t.Fatal(err)
	}
	initial := latestMCPStatus(t, webHarness{sink: sink})
	if !initial.Supported || initial.Ready || len(initial.Servers) != 0 {
		t.Fatalf("initial support=%+v", initial)
	}
	read, err := conv.(agentapi.MCPStatusReader).MCPStatusSnapshot(context.Background())
	if err != nil || !read.Supported || !read.Ready || len(read.Servers) != 0 {
		t.Fatalf("quiet supported read=%+v error=%v", read, err)
	}
}

type statusSession struct {
	*mcpSwitchSession
	list      func() []rpc.MCPServer
	toolNames []string
}

func (s *statusSession) MCPList(context.Context) ([]rpc.MCPServer, error) { return s.list(), nil }
func (s *statusSession) MCPTools(_ context.Context, name string) ([]rpc.MCPTools, error) {
	s.toolNames = append(s.toolNames, name)
	description := "Only this server's description"
	return []rpc.MCPTools{{Name: "search", Description: &description}}, nil
}

func TestMCPNativeStatusReadDoesNotFetchToolsOrLoseReconnect(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	url := "https://private.example/mcp?token=private-value"
	s := &statusSession{mcpSwitchSession: &mcpSwitchSession{fakeSession: h.fs}, list: func() []rpc.MCPServer {
		return []rpc.MCPServer{{Name: "notes", Status: rpc.MCPServerStatusConnected, URL: &url}}
	}}
	c.sess = s
	snapshot, err := c.MCPStatusSnapshot(context.Background())
	if err != nil || !snapshot.Supported || !snapshot.Ready || len(snapshot.Servers) != 1 || !snapshot.Servers[0].Remote || len(s.toolNames) != 0 {
		t.Fatalf("summary=%+v error=%v tools=%v", snapshot, err, s.toolNames)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(raw), "private-value") || strings.Contains(string(raw), "description") || strings.Contains(string(raw), "tools") {
		t.Fatalf("summary carries native bodies: %s error=%v", raw, err)
	}
	h.fs.onEvent(ev("reconnect", &rpc.SessionMCPServerNeedsReconnectData{ServerName: "notes"}))
	snapshot, err = c.MCPStatusSnapshot(context.Background())
	if err != nil || !snapshot.Servers[0].NeedsReconnect {
		t.Fatalf("refresh forgot reconnect request: %+v error=%v", snapshot, err)
	}
	tools, err := c.MCPTools(context.Background(), "notes")
	if err != nil || len(tools) != 1 || tools[0].Description != "Only this server's description" || len(s.toolNames) != 1 || s.toolNames[0] != "notes" {
		t.Fatalf("tools=%+v calls=%v error=%v", tools, s.toolNames, err)
	}
}

func TestMCPNativeStatusReadCannotOverwriteNewerEvent(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.sess = &statusSession{mcpSwitchSession: &mcpSwitchSession{fakeSession: h.fs}, list: func() []rpc.MCPServer {
		h.fs.onEvent(ev("loaded", &rpc.SessionMCPServersLoadedData{Servers: []rpc.MCPServersLoadedServer{{Name: "notes", Status: rpc.MCPServerStatusFailed}}}))
		return []rpc.MCPServer{{Name: "notes", Status: rpc.MCPServerStatusConnected}}
	}}
	snapshot, err := c.MCPStatusSnapshot(context.Background())
	if err != nil || !snapshot.Ready || snapshot.Servers[0].Status != agentapi.MCPFailed {
		t.Fatalf("stale list replaced newer event: %+v error=%v", snapshot, err)
	}
}

func latestMCPStatus(t *testing.T, h webHarness) agentapi.MCPStatusSnapshot {
	t.Helper()
	events := h.sink.all()
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Kind == agentapi.EventMCPStatus && event.MCPStatus != nil {
			return *event.MCPStatus
		}
	}
	t.Fatal("native MCP status did not reach the event sink")
	return agentapi.MCPStatusSnapshot{}
}

func TestMCPNativeStatusUpdatesReachTheSameConversation(t *testing.T) {
	h := openWeb(t)
	transport := rpc.MCPServerTransport("http")
	h.fs.onEvent(ev("loaded", &rpc.SessionMCPServersLoadedData{Servers: []rpc.MCPServersLoadedServer{{Name: "notes", Status: rpc.MCPServerStatusPending, Transport: &transport}}}))
	first := latestMCPStatus(t, h)
	if !first.Supported || !first.Ready || len(first.Servers) != 1 || first.Servers[0].Status != agentapi.MCPPending || !first.Servers[0].Remote {
		t.Fatalf("loaded status = %+v", first)
	}
	before := len(h.sink.all())
	h.fs.onEvent(ev("same", &rpc.SessionMCPServerStatusChangedData{ServerName: "notes", Status: rpc.MCPServerStatusPending}))
	if len(h.sink.all()) != before {
		t.Fatal("identical MCP state emitted another frame")
	}
	h.fs.onEvent(ev("connected", &rpc.SessionMCPServerStatusChangedData{ServerName: "notes", Status: rpc.MCPServerStatusConnected}))
	last := h.sink.last()
	if last.MCPStatus == nil || last.MCPStatus.Servers[0].Status != agentapi.MCPConnected || !last.MCPStatus.Servers[0].Remote {
		t.Fatalf("connected status = %+v", last)
	}
	h.fs.onEvent(ev("reconnect", &rpc.SessionMCPServerNeedsReconnectData{ServerName: "notes"}))
	if last = h.sink.last(); last.MCPStatus == nil || !last.MCPStatus.Servers[0].NeedsReconnect {
		t.Fatalf("reconnect state = %+v", last)
	}
	h.fs.onEvent(ev("removed", &rpc.SessionMCPServerRemovedData{ServerName: "notes"}))
	if last = h.sink.last(); last.MCPStatus == nil || len(last.MCPStatus.Servers) != 0 || !last.MCPStatus.Ready {
		t.Fatalf("removed state = %+v", last)
	}
	if h.fs.disconnected || len(h.fc.resume) != 0 {
		t.Fatal("status updates replaced the conversation")
	}
}

func TestMCPNativeStatusIsBoundedAndReportsOverflow(t *testing.T) {
	h := openWeb(t)
	rows := make([]rpc.MCPServersLoadedServer, agentapi.MaxMCPStatusServers+1)
	for i := range rows {
		rows[i] = rpc.MCPServersLoadedServer{Name: fmt.Sprintf("server-%03d", i), Status: rpc.MCPServerStatusConnected}
	}
	h.fs.onEvent(ev("loaded", &rpc.SessionMCPServersLoadedData{Servers: rows}))
	snapshot := latestMCPStatus(t, h)
	if len(snapshot.Servers) != agentapi.MaxMCPStatusServers || !snapshot.Truncated {
		t.Fatalf("bounded snapshot = %d rows, truncated=%t", len(snapshot.Servers), snapshot.Truncated)
	}
	long := strings.Repeat("x", 1<<16)
	h.fs.onEvent(ev("failed", &rpc.SessionMCPServerStatusChangedData{ServerName: "server-000", Status: rpc.MCPServerStatusFailed, Error: &long}))
	if last := h.sink.last(); last.MCPStatus == nil || len(last.MCPStatus.Servers[0].Error) > maxErrorText {
		t.Fatal("unbounded MCP failure text")
	}
}
