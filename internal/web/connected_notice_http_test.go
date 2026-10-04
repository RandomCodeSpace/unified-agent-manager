package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// Uses two real managers and the guarded TLS source dialer, with fake delivery
// instead of contacting a browser push service. B has an open standalone page;
// A has no page at all and must still receive B's local notice.
func TestConnectedNoticeHTTPHeadlessForwardingAndDisable(t *testing.T) {
	b, provider, _ := newTestManager(t)
	bRegistry, err := openConnectionRegistry(b.ctx, t.TempDir(), "fixture-b-owner")
	if err != nil {
		t.Fatal(err)
	}
	defer bRegistry.close()
	bServer := &Server{m: b, connections: bRegistry}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-workload-grant" || r.Header.Get(headerUAMInstance) != bRegistry.InstanceID() || r.Header.Get("Cookie") != "" {
			t.Error("source dial did not use only its dedicated grant and expected identity")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		bServer.handleFederationNotices(w, r)
	}))
	defer remote.Close()
	summary, conversation := createSession(t, b, provider)
	standalone, _, err := b.Subscribe(summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Unsubscribe(standalone)
	b.notePage(standalone, "b-page")
	b.SetViewing("b-page", summary.ID)

	a, _, _ := newTestManager(t)
	aRegistry, err := openConnectionRegistry(a.ctx, t.TempDir(), "fixture-a-owner")
	if err != nil {
		t.Fatal(err)
	}
	defer aRegistry.close()
	target := connectionTarget{Connection: Connection{ID: "11111111-1111-4111-8111-111111111111", InstanceID: bRegistry.InstanceID(), Label: "Work", BaseURL: remote.URL,
		Enabled: true, AllowPrivate: true, Capabilities: []string{"notices-v1"}}, Credential: "fixture-workload-grant", AllowedAddresses: []string{"127.0.0.1"}}
	saved, err := aRegistry.put(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(remote.Certificate())
	aServer := &Server{m: a, connections: aRegistry, connectionTLS: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	aServer.initConnectedNotifications()
	defer aServer.notices.close()
	sent := make(chan pushPayload, 4)
	aServer.notices.send = func(ctx context.Context, payload pushPayload) {
		select {
		case sent <- payload:
		case <-ctx.Done():
		}
	}
	waitUntil(t, "the daemon's source baseline", func() bool {
		aServer.notices.mu.Lock()
		defer aServer.notices.mu.Unlock()
		state := aServer.notices.sources[saved.ID]
		return state != nil && state.fresh
	})
	conversation.EmitTurn(agentapi.TurnWorking, "")
	conversation.EmitInteraction(agentapi.Interaction{ID: "q1", Kind: agentapi.InteractionQuestion, Title: "Which?", State: agentapi.InteractionPending,
		Questions: []agentapi.Question{{Text: "Which?", Choices: []string{"x", "y"}}}})
	select {
	case notice := <-sent:
		if notice.InstanceID != bRegistry.InstanceID() || notice.HomeID != aRegistry.InstanceID() || notice.Task != summary.ID || notice.Generation != saved.Generation {
			t.Fatalf("qualified push: %+v", notice)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("B's visible page suppressed A's headless delivery")
	}
	target.Enabled = false
	if _, err := aRegistry.put(target, saved.Generation); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "disabled source reader removal", func() bool {
		aServer.notices.mu.Lock()
		defer aServer.notices.mu.Unlock()
		return len(aServer.notices.sources) == 0 && len(aServer.notices.cursors) == 0
	})
	if _, err := b.Answer(summary.ID, "q1", agentapi.Answer{Answers: [][]string{{"x"}}}); err != nil {
		t.Fatal(err)
	}
	conversation.EmitTurn(agentapi.TurnCompleted, "")
	select {
	case payload := <-sent:
		t.Fatalf("disabled source pushed: %+v", payload)
	default:
	}
}

func TestConnectedNoticeViewingRejectsReplacedGeneration(t *testing.T) {
	h, _, _ := testNoticeHome(t)
	server := &Server{notices: h}
	for _, generation := range []string{"2", "3"} {
		request := httptest.NewRequest(http.MethodPost, "/api/connected-notifications/viewing", strings.NewReader(`{"page":"page","connection_id":"saved-b","generation":`+generation+`,"task":"same-task"}`))
		response := httptest.NewRecorder()
		server.handleConnectedNoticeViewing(response, request)
		want := http.StatusConflict
		if generation == "3" {
			want = http.StatusNoContent
		}
		if response.Code != want {
			t.Fatalf("generation %s status %d, want %d", generation, response.Code, want)
		}
	}
}
