package web

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Log out ends the session on the server: the same cookie is refused
// afterwards, while another sign-in's cookie keeps working.
func TestLogoutRevokesOnlyItsOwnSession(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	first, second := ts.login(t, "127.0.0.1:8260"), ts.login(t, "127.0.0.1:8260")
	for _, cookie := range []reqOpt{first, second} {
		if w := ts.do(http.MethodGet, "/api/sessions", "", cookie); w.Code != http.StatusOK {
			t.Fatalf("signed-in list = %d", w.Code)
		}
	}
	if w := ts.do(http.MethodPost, "/api/logout", "", first); w.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/sessions", "", first); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out cookie = %d, want 401", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/auth", "", first); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"authenticated":false`) {
		t.Fatalf("auth after logout = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, "/api/sessions", "", second); w.Code != http.StatusOK {
		t.Fatalf("other session after logout = %d, want 200", w.Code)
	}
	// A restart reads the set back from disk.
	srv, err := NewServer(ServerConfig{Manager: ts.m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts2 := &testServer{srv: srv, m: ts.m, prov: ts.prov}
	if w := ts2.do(http.MethodGet, "/api/sessions", "", first); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out cookie after restart = %d, want 401", w.Code)
	}
	if w := ts2.do(http.MethodGet, "/api/sessions", "", second); w.Code != http.StatusOK {
		t.Fatalf("other session after restart = %d, want 200", w.Code)
	}
}

// A cookie from before the nonce still signs in, and Log out ends it too.
func TestLogoutRevokesACookieWithoutNonce(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	e := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	old := func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: e + "." + legacyMAC("uam-web-session-v3|127.0.0.1:8260|"+e)})
	}
	if w := ts.do(http.MethodGet, "/api/sessions", "", old); w.Code != http.StatusOK {
		t.Fatalf("cookie without nonce = %d, want 200", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/logout", "", old); w.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/sessions", "", old); w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie without nonce after logout = %d, want 401", w.Code)
	}
}

// Signed-out sessions are kept on disk only until their cookie expires.
func TestRevokedSessionsPruneExpiredEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), signedOutFileName)
	r, err := openRevokedSessions(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if err := r.revoke("a", now+100, now); err != nil {
		t.Fatal(err)
	}
	// "a" has expired by the time "b" is recorded, so the save drops it.
	if err := r.revoke("b", now+3600, now+200); err != nil {
		t.Fatal(err)
	}
	if r.revoked("a") || !r.revoked("b") {
		t.Fatalf("after pruning: a=%v b=%v", r.revoked("a"), r.revoked("b"))
	}
	again, err := openRevokedSessions(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.revoked("a") || !again.revoked("b") {
		t.Fatalf("reloaded: a=%v b=%v", again.revoked("a"), again.revoked("b"))
	}
	if _, err := openRevokedSessions(filepath.Join(t.TempDir(), signedOutFileName)); err != nil {
		t.Fatalf("missing file = %v, want an empty set", err)
	}
}
