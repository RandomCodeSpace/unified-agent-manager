package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

type asideConversation struct {
	*agenttest.Conversation
	mu        sync.Mutex
	answer    *agentapi.AsideAnswer
	err       error
	questions []string
	during    func(ctx context.Context)
}

func (c *asideConversation) AskAside(ctx context.Context, question string) (*agentapi.AsideAnswer, error) {
	c.mu.Lock()
	c.questions = append(c.questions, question)
	during := c.during
	c.mu.Unlock()
	if during != nil {
		during(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.answer, c.err
}

func (c *asideConversation) asked() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.questions...)
}

func attachAsideAsker(m *Manager, id string, base *agenttest.Conversation) *asideConversation {
	c := &asideConversation{Conversation: base, answer: &agentapi.AsideAnswer{Text: "aside answer"}}
	m.mu.Lock()
	m.sessions[id].conv = c
	m.mu.Unlock()
	return c
}

func TestAskAsideIsTransient(t *testing.T) {
	m, p, _ := newTestManager(t)
	sum, base := createSession(t, m, p)
	c := attachAsideAsker(m, sum.ID, base)
	m.mu.Lock()
	m.sessions[sum.ID].queue = []QueuedPrompt{{RequestID: "queued-1", Text: "later"}}
	m.mu.Unlock()
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	for len(sub.ch) > 0 {
		<-sub.ch
	}
	before, _ := json.Marshal(detail(t, m, sum.ID))
	got, err := m.AskAside(context.Background(), sum.ID, "  what changed?  ")
	if err != nil || got == nil || got.Text != "aside answer" {
		t.Fatalf("aside = %+v, %v", got, err)
	}
	if q := c.asked(); len(q) != 1 || q[0] != "what changed?" {
		t.Fatalf("questions = %q", q)
	}
	after, _ := json.Marshal(detail(t, m, sum.ID))
	if string(before) != string(after) || strings.Contains(string(after), "aside answer") {
		t.Fatalf("aside changed the Task:\nbefore %s\nafter  %s", before, after)
	}
	if len(sub.ch) != 0 || len(base.Sends()) != 0 || len(p.Opens()) != 1 {
		t.Fatalf("aside published %d events, sent %d prompts or reopened", len(sub.ch), len(base.Sends()))
	}
	m.mu.Lock()
	asking := m.sessions[sum.ID].asking
	m.mu.Unlock()
	if asking {
		t.Fatal("aside left its in-flight mark")
	}
}

func TestAskAsideValidatesQuestionBeforeProvider(t *testing.T) {
	m, p, _ := newTestManager(t)
	sum, base := createSession(t, m, p)
	c := attachAsideAsker(m, sum.ID, base)
	for _, q := range []string{"", " \n\t", strings.Repeat("x", maxAsideQuestionBytes+1), "bad \xff"} {
		if got, err := m.AskAside(context.Background(), sum.ID, q); statusOf(err) != http.StatusBadRequest || got != nil {
			t.Fatalf("question %.20q = %+v, %v", q, got, err)
		}
	}
	if len(c.asked()) != 0 {
		t.Fatal("invalid question reached the provider")
	}
}

func TestAskAsideDeclinesInactiveTasksWithoutResume(t *testing.T) {
	for _, state := range []string{"closed", StageSettled, StageArchived, "unsupported", "nil", "starting"} {
		t.Run(state, func(t *testing.T) {
			m, p, _ := newTestManager(t)
			sum, base := createSession(t, m, p)
			c := attachAsideAsker(m, sum.ID, base)
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
			if got, err := m.AskAside(context.Background(), sum.ID, "hi"); statusOf(err) != http.StatusConflict || got != nil {
				t.Fatalf("%s = %+v, %v", state, got, err)
			}
			if len(c.asked()) != 0 || len(p.Opens()) != 1 || len(base.Sends()) != 0 {
				t.Fatal("inactive aside reached the provider or reopened the Task")
			}
		})
	}
}

func TestAskAsideDropsAnswerFromChangedTask(t *testing.T) {
	for _, change := range []string{"model", "effort", "tier", "gen", "conv-id", "conv", "task", "stage", "error-closed"} {
		t.Run(change, func(t *testing.T) {
			m, p, _ := newTestManager(t)
			sum, base := createSession(t, m, p)
			c := attachAsideAsker(m, sum.ID, base)
			c.during = func(context.Context) {
				if change == "error-closed" {
					c.answer, c.err = nil, agentapi.ErrClosed
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
			if got, err := m.AskAside(context.Background(), sum.ID, "hi"); statusOf(err) != http.StatusConflict || got != nil {
				t.Fatalf("stale %s = %+v, %v", change, got, err)
			}
		})
	}
}

// Stop, Close and model changes take s.op; an aside waiting on the provider
// must not hold it, and one Task has one aside at a time.
func TestAskAsideReleasesOpAndAllowsOneAtATime(t *testing.T) {
	m, p := queuedSettingsManager(t)
	sum, base := createSession(t, m, p)
	c := attachAsideAsker(m, sum.ID, base)
	entered, release := make(chan struct{}), make(chan struct{})
	c.during = func(ctx context.Context) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := m.AskAside(context.Background(), sum.ID, "first")
		done <- err
	}()
	<-entered
	if _, err := m.AskAside(context.Background(), sum.ID, "second"); statusOf(err) != http.StatusConflict {
		t.Fatalf("second aside = %v", err)
	}
	if _, err := m.SetModel(sum.ID, setting("a"), nil, nil); err != nil {
		t.Fatalf("SetModel while an aside waits: %v", err)
	}
	closed := make(chan error, 1)
	go func() {
		_, err := m.Close(sum.ID)
		closed <- err
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for the aside")
	}
	close(release)
	if err := <-done; statusOf(err) != http.StatusConflict {
		t.Fatalf("answer after close = %v", err)
	}
	if q := c.asked(); len(q) != 1 {
		t.Fatalf("questions = %q", q)
	}
}

func TestAskAsideMapsProviderErrors(t *testing.T) {
	m, p, _ := newTestManager(t)
	sum, base := createSession(t, m, p)
	c := attachAsideAsker(m, sum.ID, base)
	for _, tc := range []struct {
		err    error
		answer *agentapi.AsideAnswer
		status int
	}{
		{agentapi.ErrUnsupported, nil, http.StatusConflict},
		{context.DeadlineExceeded, nil, http.StatusGatewayTimeout},
		{errors.New("model \x1b[31mbusy"), nil, http.StatusBadGateway},
		{nil, nil, http.StatusBadGateway},
	} {
		c.err, c.answer = tc.err, tc.answer
		_, err := m.AskAside(context.Background(), sum.ID, "hi")
		if statusOf(err) != tc.status || err != nil && strings.Contains(err.Error(), "\x1b") {
			t.Fatalf("%v = %v", tc.err, err)
		}
	}
}

func TestAskAsideHeldConversationDoesNotReachProvider(t *testing.T) {
	m, p, _, _ := importManager(t)
	sum, base := createSession(t, m, p)
	c := attachAsideAsker(m, sum.ID, base)
	m.mu.Lock()
	m.sessions[sum.ID].terminalID = "linked"
	m.mu.Unlock()
	p.SetInUse([]string{sum.ConversationID}, nil)
	if _, err := m.AskAside(context.Background(), sum.ID, "hi"); !errors.Is(err, errHeldElsewhere) || len(c.asked()) != 0 {
		t.Fatalf("held = %v, asked %d", err, len(c.asked()))
	}
}

func TestAskAsideRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	sum, base := createSession(t, ts.m, ts.prov)
	c := attachAsideAsker(ts.m, sum.ID, base)
	path := "/api/sessions/" + sum.ID + "/aside"
	if w := ts.do(http.MethodPost, path, `{"question":"hi"}`); w.Code != http.StatusUnauthorized || len(c.asked()) != 0 {
		t.Fatalf("unauthenticated = %d", w.Code)
	}
	w := ts.do(http.MethodPost, path, `{"question":"hi"}`, withCookie(ts))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"text":"aside answer"`) || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("route = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, path, `{"question":"hi"} {}`, withCookie(ts)); w.Code != http.StatusBadRequest {
		t.Fatalf("trailing body = %d", w.Code)
	}
}

// An aside waiting for its answer holds a CLI restart back, as a turn does.
func TestAskAsideKeepsItsConversationThroughACLIRestart(t *testing.T) {
	m, p, _ := newTestManager(t)
	sum, base := createSession(t, m, p)
	c := attachAsideAsker(m, sum.ID, base)
	entered, release := make(chan struct{}), make(chan struct{})
	c.during = func(context.Context) {
		close(entered)
		<-release
	}
	done := make(chan error, 1)
	go func() {
		_, err := m.AskAside(context.Background(), sum.ID, "what changed?")
		done <- err
	}()
	<-entered
	m.mu.Lock()
	s := m.sessions[sum.ID]
	m.mu.Unlock()
	busy, suspended := m.cliBusyTasks(p.Name()), m.suspendForCLI(s)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("aside = %v", err)
	}
	if busy != 1 || suspended {
		t.Fatalf("busy %d, suspended %v: the restart closed a waiting aside", busy, suspended)
	}
}

// An aside runs a model on the account, so it is refused like a send when
// the runtime is signed in to an account other than the linked one.
func TestAskAsideRefusesAnotherAccount(t *testing.T) {
	prov := newAccountProvider(true)
	m := startManager(t, openTestStore(t), prov)
	sum, base := createSession(t, m, prov.Provider)
	c := attachAsideAsker(m, sum.ID, base)
	prov.switchAccount("mallory", agentapi.AccountEnv)
	later := time.Now().Add(time.Minute)
	m.now = func() time.Time { return later }
	if _, err := m.AskAside(context.Background(), sum.ID, "what changed?"); errCode(err) != codeAccountNotLinked {
		t.Fatalf("aside as another account = %v (code %q)", err, errCode(err))
	}
	if q := c.asked(); len(q) != 0 {
		t.Fatalf("the provider was asked %q", q)
	}
}
