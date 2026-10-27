package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func signInRequest(remote string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	r.RemoteAddr = remote
	return r
}

func TestSignInLimiterPerClient(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	l := newSignInLimiter()
	l.now = func() time.Time { return at }
	a, b := signInRequest("192.0.2.1:1000"), signInRequest("192.0.2.1:2000")
	for i := range signInPerClient {
		if wait := l.admit(a); wait != 0 {
			t.Fatalf("attempt %d limited: %v", i+1, wait)
		}
	}
	at = at.Add(time.Minute)
	if wait := l.admit(b); wait != signInWindow-time.Minute {
		t.Fatalf("same host on another port wait = %v", wait)
	}
	if wait := l.admit(signInRequest("198.51.100.7:1000")); wait != 0 {
		t.Fatalf("another client limited: %v", wait)
	}
	l.succeeded(a)
	if wait := l.admit(a); wait != 0 {
		t.Fatalf("success did not clear the client: %v", wait)
	}
	for range signInPerClient - 1 {
		l.admit(a)
	}
	if l.admit(a) == 0 {
		t.Fatal("cleared client got more than the limit")
	}
	at = at.Add(signInWindow)
	if wait := l.admit(a); wait != 0 {
		t.Fatalf("new window still limited: %v", wait)
	}
}

func TestSignInLimiterGlobalAndBounded(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	l := newSignInLimiter()
	l.now = func() time.Time { return at }
	for i := range signInGlobal - 1 {
		if wait := l.admit(signInRequest(fmt.Sprintf("10.0.%d.%d:1", i/256, i%256))); wait != 0 {
			t.Fatalf("client %d limited: %v", i, wait)
		}
	}
	at = at.Add(5 * time.Minute)
	if l.admit(signInRequest("10.1.0.0:1")) != 0 {
		t.Fatal("last attempt under the ceiling limited")
	}
	if wait := l.admit(signInRequest("10.2.0.0:1")); wait != 5*time.Minute {
		t.Fatalf("over the global ceiling wait = %v", wait)
	}
	if len(l.clients) != signInGlobal {
		t.Fatalf("a refused client was stored: %d", len(l.clients))
	}
	at = at.Add(6 * time.Minute)
	if l.admit(signInRequest("10.2.0.0:1")) != 0 {
		t.Fatal("new global window still limited")
	}
	// The sweep drops the expired entries and keeps the one still counting.
	if len(l.clients) != 2 || l.clients["10.1.0.0"] == nil {
		t.Fatalf("expired clients kept: %d", len(l.clients))
	}
}

func TestSignInClientAndRetryAfter(t *testing.T) {
	if got := signInClient(signInRequest("[2001:db8::1]:443")); got != "2001:db8::1" {
		t.Fatalf("ipv6 client = %q", got)
	}
	if got := signInClient(signInRequest("pipe")); got != "pipe" {
		t.Fatalf("portless client = %q", got)
	}
	w := httptest.NewRecorder()
	setRetryAfter(w, 1500*time.Millisecond)
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q", got)
	}
}

func TestLoginAndPairingAreLimited(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	good := `{"token":"` + testToken + `"}`
	for range signInPerClient - 1 {
		if w := ts.do(http.MethodPost, "/api/login", `{"token":"wrong"}`); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong token = %d", w.Code)
		}
	}
	if w := ts.do(http.MethodPost, "/api/login", good); w.Code != http.StatusNoContent {
		t.Fatalf("owner locked out after mistakes: %d", w.Code)
	}
	for range signInPerClient {
		ts.do(http.MethodPost, "/api/login", `{"token":"wrong"}`)
	}
	w := ts.do(http.MethodPost, "/api/login", good)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || len(w.Result().Cookies()) != 0 {
		t.Fatalf("limited login = %d Retry-After=%q", w.Code, w.Header().Get("Retry-After"))
	}
	pair := `{"token":"` + testToken + `","client_instance_id":"` + uuid.NewString() + `","label":"home"}`
	w = ts.do(http.MethodPost, "/api/federation/pair", pair)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), `"rate_limited"`) {
		t.Fatalf("limited pairing = %d %s", w.Code, w.Body)
	}
	if len(ts.srv.connections.data.Grants) != 0 {
		t.Fatal("limited pairing created a grant")
	}
}
