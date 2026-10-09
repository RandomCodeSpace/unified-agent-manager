package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

type contextConversation struct {
	*agenttest.Conversation
	info                        *agentapi.ContextInfo
	attribution                 *agentapi.ContextAttribution
	readErr                     error
	infoCalls, attributionCalls int
	during                      func()
}

func (c *contextConversation) ContextInfo(context.Context) (*agentapi.ContextInfo, error) {
	c.infoCalls++
	if c.during != nil {
		c.during()
	}
	return c.info, c.readErr
}

func (c *contextConversation) ContextAttribution(context.Context) (*agentapi.ContextAttribution, error) {
	c.attributionCalls++
	if c.during != nil {
		c.during()
	}
	return c.attribution, c.readErr
}

func attachContextReader(m *Manager, id string, base *agenttest.Conversation) *contextConversation {
	c := &contextConversation{Conversation: base, info: &agentapi.ContextInfo{Model: "selected", TotalTokens: 17, Limit: 200, PromptTokenLimit: 160, BufferTokens: 40}, attribution: &agentapi.ContextAttribution{Model: "selected", TotalTokens: 17, Limit: 200, Categories: agentapi.ContextCategories{FreeSpace: 143, Buffer: 40}, Entries: []agentapi.ContextSource{{ID: "source:one", Kind: "skill", Label: "Source only in reader", Tokens: 9}}}}
	m.mu.Lock()
	m.sessions[id].conv = c
	m.mu.Unlock()
	return c
}

func TestContextBreakdownIsOnDemandAndNotRetainedInTaskState(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, base := createSession(t, m, prov)
	c := attachContextReader(m, sum.ID, base)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	for len(sub.ch) > 0 {
		<-sub.ch
	}
	got, err := m.ContextBreakdown(context.Background(), sum.ID, false)
	if err != nil || got.Info == nil || got.Info.TotalTokens != 17 || got.Attribution != nil || c.infoCalls != 1 || c.attributionCalls != 0 {
		t.Fatalf("info = %+v, %v, calls %d/%d", got, err, c.infoCalls, c.attributionCalls)
	}
	got, err = m.ContextBreakdown(context.Background(), sum.ID, true)
	if err != nil || got.Info != nil || got.Attribution == nil || got.Attribution.TotalTokens != 17 || c.infoCalls != 1 || c.attributionCalls != 1 {
		t.Fatalf("attribution = %+v, %v", got, err)
	}
	d := detail(t, m, sum.ID)
	wire, _ := json.Marshal(d)
	if strings.Contains(string(wire), "Source only in reader") || strings.Contains(string(wire), "free_space") || len(sub.ch) != 0 || len(base.Sends()) != 0 || len(prov.Opens()) != 1 {
		t.Fatalf("on-demand metadata reached Task state/stream or opened/ran a model: %s", wire)
	}
	c.info = nil
	if got, err = m.ContextBreakdown(context.Background(), sum.ID, false); err != nil || got.Info != nil {
		t.Fatalf("uninitialized snapshot = %+v, %v", got, err)
	}
}

func TestContextBreakdownDeclinesClosedSettledArchivedAndUnsupported(t *testing.T) {
	for _, state := range []string{"closed", StageSettled, StageArchived, "unsupported"} {
		t.Run(state, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			sum, base := createSession(t, m, prov)
			c := attachContextReader(m, sum.ID, base)
			if state == "closed" {
				if _, err := m.Close(sum.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				m.mu.Lock()
				if state == "unsupported" {
					m.sessions[sum.ID].conv = base
				} else {
					m.sessions[sum.ID].stage = state
				}
				m.mu.Unlock()
			}
			before := len(prov.Opens())
			if _, err := m.ContextBreakdown(context.Background(), sum.ID, false); statusOf(err) != http.StatusConflict {
				t.Fatalf("%s read = %v", state, err)
			}
			if c.infoCalls != 0 || c.attributionCalls != 0 || len(prov.Opens()) != before {
				t.Fatal("unavailable read reached provider or resumed")
			}
		})
	}
}

func TestContextBreakdownRejectsChangedSelectionAndDetachedGeneration(t *testing.T) {
	for _, change := range []string{"model", "effort", "tier", "generation", "conversation", "exit"} {
		t.Run(change, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			sum, base := createSession(t, m, prov)
			c := attachContextReader(m, sum.ID, base)
			c.during = func() {
				if change == "exit" {
					base.Exit("provider detached")
					return
				}
				m.mu.Lock()
				defer m.mu.Unlock()
				s := m.sessions[sum.ID]
				switch change {
				case "model":
					s.model = "different"
				case "effort":
					s.effort = "high"
				case "tier":
					s.contextSize = "long_context"
				case "generation":
					s.gen++
				case "conversation":
					s.convID = "different"
				}
			}
			got, err := m.ContextBreakdown(context.Background(), sum.ID, true)
			if statusOf(err) != http.StatusConflict || got.Attribution != nil || c.attributionCalls != 1 {
				t.Fatalf("stale %s = %+v, %v", change, got, err)
			}
		})
	}
}

func TestContextBreakdownHeldConversationDoesNotReachReader(t *testing.T) {
	m, prov, _, _ := importManager(t)
	sum, base := createSession(t, m, prov)
	c := attachContextReader(m, sum.ID, base)
	m.mu.Lock()
	m.sessions[sum.ID].terminalID = "linked"
	m.mu.Unlock()
	prov.SetInUse([]string{sum.ConversationID}, nil)
	if _, err := m.ContextBreakdown(context.Background(), sum.ID, true); !errors.Is(err, errHeldElsewhere) || c.attributionCalls != 0 {
		t.Fatalf("held reader = %v, calls %d", err, c.attributionCalls)
	}
}

func TestContextBreakdownErrorAndAuthenticatedReadRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, base := createSession(t, ts.m, ts.prov)
	c := attachContextReader(ts.m, sum.ID, base)
	path := "/api/sessions/" + sum.ID + "/context"
	if w := ts.do(http.MethodGet, path, ""); w.Code != http.StatusUnauthorized || c.infoCalls != 0 {
		t.Fatalf("unauthenticated metadata = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, path, "", withCookie(ts)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total_tokens":17`) || c.attributionCalls != 0 {
		t.Fatalf("info route = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, path+"?attribution=true", "", withCookie(ts)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Source only in reader") {
		t.Fatalf("attribution route = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, path+"?attribution=all", "", withCookie(ts)); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown query = %d", w.Code)
	}
	for _, test := range []struct {
		err    error
		status int
	}{{agentapi.ErrUnsupported, http.StatusConflict}, {agentapi.ErrClosed, http.StatusConflict}, {errors.New("private raw body"), http.StatusBadGateway}} {
		c.readErr = test.err
		if w := ts.do(http.MethodGet, path, "", withCookie(ts)); w.Code != test.status || strings.Contains(w.Body.String(), "private raw body") {
			t.Fatalf("provider failure = %d %s", w.Code, w.Body)
		}
	}
}
