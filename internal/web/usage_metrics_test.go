package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

type usageMetricsConversation struct {
	*agenttest.Conversation
	metrics *agentapi.TaskUsageMetrics
	readErr error
	calls   int
	during  func()
}

func (c *usageMetricsConversation) UsageMetrics(context.Context) (*agentapi.TaskUsageMetrics, error) {
	c.calls++
	if c.during != nil {
		c.during()
	}
	return c.metrics, c.readErr
}
func attachUsageMetricsReader(m *Manager, id string, base *agenttest.Conversation) *usageMetricsConversation {
	c := &usageMetricsConversation{Conversation: base, metrics: &agentapi.TaskUsageMetrics{StartedAt: time.Now(), UserRequests: 1, Models: []agentapi.UsageMetricModel{{Model: "native-reader-only", Input: 100, CacheRead: 80}}, Agents: []agentapi.UsageMetricAgent{}, TokenDetails: []agentapi.UsageTokenDetail{}}}
	m.mu.Lock()
	m.sessions[id].conv = c
	m.sessions[id].usage = &agentapi.Usage{AIUnits: 9}
	m.mu.Unlock()
	return c
}
func TestTaskUsageMetricsIsTransientAndDoesNotTouchCombinedUsageOrLedger(t *testing.T) {
	m, p, _ := newTestManager(t)
	sum, base := createSession(t, m, p)
	c := attachUsageMetricsReader(m, sum.ID, base)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	for len(sub.ch) > 0 {
		<-sub.ch
	}
	m.mu.Lock()
	before, _ := json.Marshal(m.tokens)
	m.mu.Unlock()
	first, err := m.TaskUsageMetrics(context.Background(), sum.ID)
	if err != nil || first == nil || first.UserRequests != 1 || c.calls != 1 {
		t.Fatalf("read=%+v %v", first, err)
	}
	c.metrics = &agentapi.TaskUsageMetrics{StartedAt: time.Now(), UserRequests: 2, Models: []agentapi.UsageMetricModel{}, Agents: []agentapi.UsageMetricAgent{}, TokenDetails: []agentapi.UsageTokenDetail{}}
	second, err := m.TaskUsageMetrics(context.Background(), sum.ID)
	if err != nil || second.UserRequests != 2 || first.UserRequests != 1 || c.calls != 2 {
		t.Fatalf("absolute snapshot=%+v %v", second, err)
	}
	m.mu.Lock()
	after, _ := json.Marshal(m.tokens)
	m.mu.Unlock()
	d := detail(t, m, sum.ID)
	wire, _ := json.Marshal(d)
	if string(before) != string(after) || d.Usage == nil || d.Usage.AIUnits != 9 || strings.Contains(string(wire), "native-reader-only") || len(sub.ch) != 0 || len(base.Sends()) != 0 || len(p.Opens()) != 1 {
		t.Fatalf("read changed ledger/combined usage/transcript or ran a model: %s", wire)
	}
}
func TestTaskUsageMetricsDeclinesInactiveUnsupportedAndMissingWithoutResume(t *testing.T) {
	for _, state := range []string{"closed", StageSettled, StageArchived, "unsupported", "nil", "starting"} {
		t.Run(state, func(t *testing.T) {
			m, p, _ := newTestManager(t)
			sum, base := createSession(t, m, p)
			c := attachUsageMetricsReader(m, sum.ID, base)
			if state == "closed" {
				if _, err := m.Close(sum.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				m.mu.Lock()
				s := m.sessions[sum.ID]
				switch state {
				case "unsupported":
					s.conv = base
				case "nil":
					s.conv = nil
				case "starting":
					s.base = StateStarting
				default:
					s.stage = state
				}
				m.mu.Unlock()
			}
			before := len(p.Opens())
			if got, err := m.TaskUsageMetrics(context.Background(), sum.ID); statusOf(err) != http.StatusConflict || got != nil {
				t.Fatalf("%s=%+v %v", state, got, err)
			}
			if c.calls != 0 || len(p.Opens()) != before {
				t.Fatal("inactive read reached provider or resumed")
			}
		})
	}
}
func TestTaskUsageMetricsRejectsDetachedGenerationAndSelection(t *testing.T) {
	for _, change := range []string{"model", "effort", "tier", "gen", "conv-id", "conv", "task", "stage", "exit"} {
		t.Run(change, func(t *testing.T) {
			m, p, _ := newTestManager(t)
			sum, base := createSession(t, m, p)
			c := attachUsageMetricsReader(m, sum.ID, base)
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
					s.model = "next"
				case "effort":
					s.effort = "high"
				case "tier":
					s.contextSize = "long_context"
				case "gen":
					s.gen++
				case "conv-id":
					s.convID = "next"
				case "conv":
					s.conv = base
				case "task":
					m.sessions[sum.ID] = newSession(s.id, s.provider, s.name, s.workdir, s.convID, s.createdAt)
				case "stage":
					s.stage = StageSettled
				}
			}
			if got, err := m.TaskUsageMetrics(context.Background(), sum.ID); statusOf(err) != http.StatusConflict || got != nil || c.calls != 1 {
				t.Fatalf("stale %s=%+v %v", change, got, err)
			}
		})
	}
}
func TestTaskUsageMetricsHeldReaderDoesNotReachProvider(t *testing.T) {
	m, p, _, _ := importManager(t)
	sum, base := createSession(t, m, p)
	c := attachUsageMetricsReader(m, sum.ID, base)
	m.mu.Lock()
	m.sessions[sum.ID].terminalID = "linked"
	m.mu.Unlock()
	p.SetInUse([]string{sum.ConversationID}, nil)
	if _, err := m.TaskUsageMetrics(context.Background(), sum.ID); !errors.Is(err, errHeldElsewhere) || c.calls != 0 {
		t.Fatalf("held=%v calls=%d", err, c.calls)
	}
}
func TestTaskUsageMetricsAuthenticatedNoStoreRouteAndSanitizedErrors(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, base := createSession(t, ts.m, ts.prov)
	c := attachUsageMetricsReader(ts.m, sum.ID, base)
	path := "/api/sessions/" + sum.ID + "/usage-metrics"
	if w := ts.do(http.MethodGet, path, ""); w.Code != http.StatusUnauthorized || c.calls != 0 {
		t.Fatalf("unauthenticated=%d", w.Code)
	}
	if w := ts.do(http.MethodGet, path, "", withCookie(ts)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"user_requests":1`) || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("route=%d %s cache=%s", w.Code, w.Body, w.Header().Get("Cache-Control"))
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{agentapi.ErrUnsupported, http.StatusConflict}, {agentapi.ErrClosed, http.StatusConflict}, {errors.New("private raw metrics"), http.StatusBadGateway}} {
		c.readErr = tc.err
		if w := ts.do(http.MethodGet, path, "", withCookie(ts)); w.Code != tc.status || strings.Contains(w.Body.String(), "private raw metrics") {
			t.Fatalf("error=%d %s", w.Code, w.Body)
		}
	}
	c.readErr = nil
	c.metrics = nil
	if w := ts.do(http.MethodGet, path, "", withCookie(ts)); w.Code != http.StatusBadGateway {
		t.Fatalf("missing snapshot=%d %s", w.Code, w.Body)
	}
}
