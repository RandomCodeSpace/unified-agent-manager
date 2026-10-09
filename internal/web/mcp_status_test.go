package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type statusConversation struct {
	agentapi.Conversation
	status     func() agentapi.MCPStatusSnapshot
	tools      []string
	toolResult []agentapi.MCPTool
	richCalls  int
	toolsRead  func() []agentapi.MCPTool
	richRead   func() []agentapi.MCPStatus
}

func (c *statusConversation) MCPStatus(context.Context) ([]agentapi.MCPStatus, error) {
	c.richCalls++
	if c.richRead != nil {
		return c.richRead(), nil
	}
	return []agentapi.MCPStatus{{Name: "notes", Status: agentapi.MCPConnected, Tools: []agentapi.MCPTool{{Name: "search", Description: "private tool description"}}}}, nil
}
func (c *statusConversation) MCPStatusSnapshot(context.Context) (agentapi.MCPStatusSnapshot, error) {
	return c.status(), nil
}
func (c *statusConversation) MCPTools(_ context.Context, name string) ([]agentapi.MCPTool, error) {
	c.tools = append(c.tools, name)
	if c.toolsRead != nil {
		return c.toolsRead(), nil
	}
	return c.toolResult, nil
}
func (*statusConversation) SetMCPServerEnabled(context.Context, string, bool) error { return nil }
func (*statusConversation) RestartMCPServer(context.Context, string) error          { return nil }
func (*statusConversation) MCPSignIn(context.Context, string, bool) (string, error) { return "", nil }

func TestMCPStatusSummaryPreservesFullGETAndReadsOnlySelectedTools(t *testing.T) {
	ts, _, auth := newMCPServer(t)
	sum, conv := createSession(t, ts.m, ts.prov)
	c := &statusConversation{Conversation: conv, status: func() agentapi.MCPStatusSnapshot {
		return agentapi.MCPStatusSnapshot{Supported: true, Ready: true, Servers: []agentapi.MCPServerStatus{{Name: "notes", Status: agentapi.MCPConnected}}}
	}, toolResult: []agentapi.MCPTool{{Name: "search", Description: "private tool description"}}}
	ts.m.mu.Lock()
	ts.m.sessions[sum.ID].conv = c
	ts.m.mu.Unlock()
	base := "/api/sessions/" + sum.ID + "/mcp"
	w := ts.do(http.MethodGet, base+"?summary=1", "", auth)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mcp_status"`) || strings.Contains(w.Body.String(), "private tool description") || strings.Contains(w.Body.String(), `"tools"`) || c.richCalls != 0 || len(c.tools) != 0 {
		t.Fatalf("summary=%d %s rich=%d tools=%v", w.Code, w.Body.String(), c.richCalls, c.tools)
	}
	w = ts.do(http.MethodGet, base, "", auth)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "private tool description") || c.richCalls != 1 {
		t.Fatalf("full GET changed: %d %s rich=%d", w.Code, w.Body.String(), c.richCalls)
	}
	w = ts.do(http.MethodGet, base+"/servers/notes/tools", "", auth)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "private tool description") || c.richCalls != 1 || len(c.tools) != 1 || c.tools[0] != "notes" {
		t.Fatalf("tool GET=%d %s rich=%d calls=%v", w.Code, w.Body.String(), c.richCalls, c.tools)
	}
	c.toolResult = nil
	w = ts.do(http.MethodGet, base+"/servers/notes/tools", "", auth)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"tools":[]`) {
		t.Fatalf("empty tools=%d %s", w.Code, w.Body.String())
	}
	// An older provider still offers explicit status and selected-server tools
	// through its original rich list, without claiming native event support.
	legacy := &struct {
		agentapi.Conversation
		agentapi.MCPController
	}{Conversation: conv, MCPController: c}
	ts.m.mu.Lock()
	ts.m.sessions[sum.ID].conv = legacy
	ts.m.mu.Unlock()
	w = ts.do(http.MethodGet, base+"?summary=1", "", auth)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"supported":false`) || !strings.Contains(w.Body.String(), `"ready":true`) || strings.Contains(w.Body.String(), "private tool description") || c.richCalls != 2 {
		t.Fatalf("legacy summary=%d %s rich=%d", w.Code, w.Body.String(), c.richCalls)
	}
	w = ts.do(http.MethodGet, base+"/servers/notes/tools", "", auth)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "private tool description") || c.richCalls != 3 {
		t.Fatalf("legacy tools=%d %s rich=%d", w.Code, w.Body.String(), c.richCalls)
	}
}

func TestMCPStatusEventsSurviveStaleReadAndResetWhenClosed(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	m.mu.Lock()
	s := m.sessions[sum.ID]
	gen := s.gen
	m.mu.Unlock()
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Unsubscribe(sub) })
	latest := agentapi.MCPStatusSnapshot{Supported: true, Ready: true, Servers: []agentapi.MCPServerStatus{{Name: "notes", Status: agentapi.MCPFailed}}}
	conv.Emit(agentapi.Event{Kind: agentapi.EventMCPStatus, MCPStatus: &latest})
	var frameStatus agentapi.MCPStatusSnapshot
	decodeField(t, frameOf(t, sub, "mcp_status"), "mcp_status", &frameStatus)
	if frameStatus.Servers[0].Status != agentapi.MCPFailed || detail(t, m, sum.ID).MCPStatus.Servers[0].Status != agentapi.MCPFailed {
		t.Fatal("native status missing from snapshot/SSE")
	}
	stale := agentapi.MCPStatusSnapshot{Supported: true, Ready: true, Servers: []agentapi.MCPServerStatus{{Name: "notes", Status: agentapi.MCPConnected}}}
	c := &statusConversation{Conversation: conv, status: func() agentapi.MCPStatusSnapshot {
		conv.Emit(agentapi.Event{Kind: agentapi.EventMCPStatus, MCPStatus: &latest})
		return stale
	}}
	// Make the in-flight read publish a different native state, then an older response.
	latest.Servers[0].Status = agentapi.MCPStopped
	m.mu.Lock()
	s.conv = c
	info := m.infos[s.provider]
	info.Capabilities.MCP = true
	m.infos[s.provider] = info
	m.mu.Unlock()
	read, err := m.TaskMCPStatus(sum.ID)
	if err != nil || read.Servers[0].Status != agentapi.MCPStopped {
		t.Fatalf("stale read won: %+v error=%v", read, err)
	}
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	closed := detail(t, m, sum.ID).MCPStatus
	if closed == nil || !closed.Supported || closed.Ready || len(closed.Servers) != 0 {
		t.Fatalf("closed cache=%+v", closed)
	}
	m.handleEvent(s, gen, agentapi.Event{Kind: agentapi.EventMCPStatus, MCPStatus: &stale})
	closed = detail(t, m, sum.ID).MCPStatus
	if closed.Ready || len(closed.Servers) != 0 {
		t.Fatalf("old generation repopulated status: %+v", closed)
	}
}

func TestMCPStatusSnapshotBoundsProviderMetadata(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	latest := agentapi.MCPStatusSnapshot{Supported: true, Ready: true, Servers: []agentapi.MCPServerStatus{{Name: strings.Repeat("x", 1<<16), Status: agentapi.MCPConnected}}}
	conv.Emit(agentapi.Event{Kind: agentapi.EventMCPStatus, MCPStatus: &latest})
	d := detail(t, m, sum.ID)
	raw, err := json.Marshal(d.MCPStatus)
	if err != nil || d.MCPStatus == nil || !d.MCPStatus.Truncated || len(d.MCPStatus.Servers) != 0 || len(raw) > 200 {
		t.Fatalf("unbounded provider metadata: %d bytes error=%v", len(raw), err)
	}
}

func TestMCPStatusToolsRejectOldGeneration(t *testing.T) {
	for _, native := range []bool{true, false} {
		t.Run(map[bool]string{true: "native", false: "fallback"}[native], func(t *testing.T) {
			ts, _, _ := newMCPServer(t)
			sum, conv := createSession(t, ts.m, ts.prov)
			started, finish := make(chan struct{}), make(chan struct{})
			tools := []agentapi.MCPTool{{Name: "old-generation", Description: "must not escape"}}
			c := &statusConversation{Conversation: conv, toolsRead: func() []agentapi.MCPTool { close(started); <-finish; return tools }, richRead: func() []agentapi.MCPStatus {
				close(started)
				<-finish
				return []agentapi.MCPStatus{{Name: "notes", Tools: tools}}
			}}
			var wrapped agentapi.Conversation = c
			if !native {
				wrapped = &struct {
					agentapi.Conversation
					agentapi.MCPController
				}{Conversation: conv, MCPController: c}
			}
			ts.m.mu.Lock()
			ts.m.sessions[sum.ID].conv = wrapped
			ts.m.mu.Unlock()
			result := make(chan error, 1)
			go func() {
				out, err := ts.m.TaskMCPTools(sum.ID, "notes")
				if len(out) != 0 {
					result <- errors.New("old-generation tools returned")
					return
				}
				result <- err
			}()
			<-started
			conv.Emit(agentapi.Event{Kind: agentapi.EventExit})
			close(finish)
			if err := <-result; statusOf(err) != http.StatusConflict {
				t.Fatalf("late tools result=%v", err)
			}
		})
	}
}

func TestMCPStatusToolsPreserveReadOnlyAndHolderGuards(t *testing.T) {
	for _, guard := range []string{"archived", "held"} {
		t.Run(guard, func(t *testing.T) {
			m, prov, sum, conv, _ := reloadMCPTask(t)
			c := &statusConversation{Conversation: conv}
			m.mu.Lock()
			s := m.sessions[sum.ID]
			s.conv = c
			if guard == "archived" {
				s.stage = StageArchived
			} else {
				s.imported = true
			}
			m.mu.Unlock()
			if guard == "held" {
				prov.SetInUse([]string{sum.ConversationID}, nil)
			}
			if _, err := m.TaskMCPTools(sum.ID, "notes"); statusOf(err) != http.StatusConflict || len(c.tools) != 0 || c.richCalls != 0 {
				t.Fatalf("tools crossed %s guard: error=%v calls=%v rich=%d", guard, err, c.tools, c.richCalls)
			}
		})
	}
}
