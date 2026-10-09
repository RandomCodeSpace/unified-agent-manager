package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

type reloadConversation struct {
	agentapi.Conversation
	reload func(context.Context) error
}

func (c *reloadConversation) ReloadCustomizations(ctx context.Context) error { return c.reload(ctx) }

func reloadMCPTask(t *testing.T) (*Manager, *agenttest.Provider, SessionSummary, *agenttest.Conversation, *reloadConversation) {
	t.Helper()
	caps := allCaps
	caps.Import, caps.MCP = true, true
	prov := agenttest.NewProvider(agentapi.ProviderCopilot, caps)
	m := startManager(t, openTestStore(t), prov)
	sum, conv := createSession(t, m, prov)
	wrapped := &reloadConversation{Conversation: conv}
	m.mu.Lock()
	m.sessions[sum.ID].conv = wrapped
	m.mu.Unlock()
	return m, prov, sum, conv, wrapped
}

func TestReconnectTaskMCPReloadKeepsConversationAndEvents(t *testing.T) {
	m, prov, sum, conv, wrapped := reloadMCPTask(t)
	var calls atomic.Int32
	wrapped.reload = func(context.Context) error { calls.Add(1); return nil }
	conv.EmitItem(agentapi.Item{ID: "before", Kind: agentapi.ItemAssistant, Text: "saved reply"})
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Unsubscribe(sub) })
	m.mu.Lock()
	gen := m.sessions[sum.ID].gen
	m.mu.Unlock()
	if err := m.ReconnectTaskMCP(sum.ID); err != nil {
		t.Fatal(err)
	}
	d := detail(t, m, sum.ID)
	m.mu.Lock()
	same := m.sessions[sum.ID].conv == wrapped && m.sessions[sum.ID].gen == gen
	m.mu.Unlock()
	if calls.Load() != 1 || !same || !d.Open || d.ConversationID != sum.ConversationID || conv.Closes() != 0 || len(prov.Opens()) != 1 {
		t.Fatalf("reload=%d same=%t open=%t id=%s closes=%d opens=%d", calls.Load(), same, d.Open, d.ConversationID, conv.Closes(), len(prov.Opens()))
	}
	if len(d.Items) != 1 || d.Items[0].ID != "before" || d.Items[0].Text != "saved reply" {
		t.Fatalf("reload changed history: %+v", d.Items)
	}
	conv.EmitItem(agentapi.Item{ID: "after", Kind: agentapi.ItemAssistant, Text: "same event sink"})
	var item agentapi.Item
	decodeField(t, frameOf(t, sub, "item"), "item", &item)
	if item.ID != "after" || item.Text != "same event sink" {
		t.Fatalf("event after reload = %+v", item)
	}
}

func TestReconnectTaskMCPReloadRejectsUnsafeTasks(t *testing.T) {
	for name, keep := range map[string]func(*Manager, string, *agenttest.Provider, *agenttest.Conversation){
		"working": func(_ *Manager, _ string, _ *agenttest.Provider, c *agenttest.Conversation) {
			c.EmitTurn(agentapi.TurnWorking, "")
		},
		"permission": func(_ *Manager, _ string, _ *agenttest.Provider, c *agenttest.Conversation) {
			c.EmitInteraction(permissionRequest("p1"))
		},
		"queue": func(m *Manager, id string, _ *agenttest.Provider, _ *agenttest.Conversation) {
			m.mu.Lock()
			m.sessions[id].queue = []QueuedPrompt{{RequestID: "queued", Text: "keep me"}}
			m.mu.Unlock()
		},
		"subagent": func(_ *Manager, _ string, _ *agenttest.Provider, c *agenttest.Conversation) {
			c.EmitSubagent(agentapi.Subagent{ID: "child", Status: agentapi.SubagentRunning})
		},
		"shell": func(_ *Manager, _ string, _ *agenttest.Provider, c *agenttest.Conversation) {
			c.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "shell", Status: "running"}}}})
		},
		"settled": func(m *Manager, id string, _ *agenttest.Provider, _ *agenttest.Conversation) {
			m.mu.Lock()
			m.sessions[id].stage = StageSettled
			m.mu.Unlock()
		},
		"archived": func(m *Manager, id string, _ *agenttest.Provider, _ *agenttest.Conversation) {
			m.mu.Lock()
			m.sessions[id].stage = StageArchived
			m.mu.Unlock()
		},
		"held imported": func(m *Manager, id string, p *agenttest.Provider, c *agenttest.Conversation) {
			m.mu.Lock()
			m.sessions[id].imported = true
			m.mu.Unlock()
			p.SetInUse([]string{c.ID()}, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, prov, sum, conv, wrapped := reloadMCPTask(t)
			var calls atomic.Int32
			wrapped.reload = func(context.Context) error { calls.Add(1); return nil }
			keep(m, sum.ID, prov, conv)
			if err := m.ReconnectTaskMCP(sum.ID); statusOf(err) != http.StatusConflict || calls.Load() != 0 || conv.Closes() != 0 {
				t.Fatalf("refusal=%v calls=%d closes=%d", err, calls.Load(), conv.Closes())
			}
			if name == "queue" {
				m.mu.Lock()
				queue := m.sessions[sum.ID].queue
				m.mu.Unlock()
				if len(queue) != 1 || queue[0].Text != "keep me" {
					t.Fatalf("refused reload discarded queued prompt: %+v", queue)
				}
			}
		})
	}
}

func TestReconnectTaskMCPReloadFallbackOnlyForUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		fallback bool
	}{
		{"unsupported", agentapi.ErrUnsupported, true},
		{"partial reload", errors.New("skills reloaded; MCP failed"), false},
		{"timeout", context.DeadlineExceeded, false},
		{"text resembles unsupported", errors.New("Method not found while applying MCP config"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, prov, sum, conv, wrapped := reloadMCPTask(t)
			wrapped.reload = func(context.Context) error { return tc.err }
			err := m.ReconnectTaskMCP(sum.ID)
			if tc.fallback {
				if err != nil || conv.Closes() != 1 {
					t.Fatalf("unsupported fallback: %v closes=%d", err, conv.Closes())
				}
			} else if statusOf(err) != http.StatusBadGateway || !strings.Contains(err.Error(), tc.err.Error()) || conv.Closes() != 0 {
				t.Fatalf("failed reload: %v closes=%d", err, conv.Closes())
			}
			if len(prov.Opens()) != 1 {
				t.Fatalf("reload unexpectedly resumed: %d opens", len(prov.Opens()))
			}
		})
	}
}

func TestReconnectTaskMCPWithoutReloadCapabilityRetainsFallback(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	if err := m.ReconnectTaskMCP(sum.ID); err != nil || conv.Closes() != 1 {
		t.Fatalf("legacy fallback=%v closes=%d", err, conv.Closes())
	}
}

func TestReconnectTaskMCPReloadRechecksQuietStateAfterHolderCheck(t *testing.T) {
	m, prov, sum, conv, wrapped := reloadMCPTask(t)
	var calls atomic.Int32
	wrapped.reload = func(context.Context) error { calls.Add(1); return nil }
	m.mu.Lock()
	m.sessions[sum.ID].imported = true
	m.mu.Unlock()
	prov.SetInUseHook(func(context.Context, []string) ([]string, error) {
		conv.EmitTurn(agentapi.TurnWorking, "")
		return nil, nil
	})
	if err := m.ReconnectTaskMCP(sum.ID); statusOf(err) != http.StatusConflict || calls.Load() != 0 || conv.Closes() != 0 {
		t.Fatalf("task became busy during holder check: %v calls=%d closes=%d", err, calls.Load(), conv.Closes())
	}
}
