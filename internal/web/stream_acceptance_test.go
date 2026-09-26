package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// The handler owns all fields except the readiness notification.
type streamFailureWriter struct {
	header            http.Header
	ready             chan struct{}
	writes, failWrite int
	failFlush         bool
}

func (w *streamFailureWriter) Header() http.Header              { return w.header }
func (w *streamFailureWriter) WriteHeader(int)                  {}
func (w *streamFailureWriter) SetWriteDeadline(time.Time) error { return nil }
func (w *streamFailureWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == 1 {
		close(w.ready)
	}
	if w.writes == w.failWrite {
		return 0, errors.New("connection closed")
	}
	return len(p), nil
}
func (w *streamFailureWriter) FlushError() error {
	if w.failFlush {
		return errors.New("flush failed")
	}
	return nil
}

func TestAcceptanceStreamFailureReleasesSubscriptionAndReconnects(t *testing.T) {
	for _, failure := range []string{"snapshot", "flush", "event", "heartbeat", "shutdown"} {
		t.Run(failure, func(t *testing.T) {
			ts := newTestServer(t, ServerConfig{})
			ts.srv.heartbeat = time.Hour
			sum, conv := createSession(t, ts.m, ts.prov)
			w := &streamFailureWriter{header: make(http.Header), ready: make(chan struct{})}
			switch failure {
			case "snapshot":
				w.failWrite = 1
			case "flush":
				w.failFlush = true
			case "event":
				w.failWrite = 2
			case "heartbeat":
				w.failWrite = 2
				ts.srv.heartbeat = time.Millisecond
			}
			r := httptest.NewRequest(http.MethodGet, "/api/events?session="+sum.ID, nil)
			r.Host = "127.0.0.1:8260"
			ts.login(t, r.Host)(r)
			done := make(chan struct{})
			go func() { defer close(done); ts.srv.ServeHTTP(w, r) }()
			select {
			case <-w.ready:
			case <-time.After(3 * time.Second):
				t.Fatal("stream did not send snapshot")
			}
			if failure == "event" {
				conv.EmitItem(agentapi.Item{ID: "live", Kind: agentapi.ItemAssistant, Text: "recorded while connected"})
			}
			if failure == "shutdown" {
				ts.m.DropSubscribers()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("stream did not release failed connection")
			}
			ts.m.mu.Lock()
			remaining := len(ts.m.subs)
			ts.m.mu.Unlock()
			if remaining != 0 || conv.Closes() != 0 || conv.Cancels() != 0 {
				t.Fatalf("disconnect: subscribers=%d closes=%d cancels=%d", remaining, conv.Closes(), conv.Cancels())
			}
			conv.EmitItem(agentapi.Item{ID: "after", Kind: agentapi.ItemAssistant, Text: "recorded after disconnect"})
			sub, snapshot, err := ts.m.Subscribe(sum.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer ts.m.Unsubscribe(sub)
			if !strings.Contains(string(snapshot), "recorded after disconnect") {
				t.Fatal("reconnect missed output after stream failure")
			}
		})
	}
}
