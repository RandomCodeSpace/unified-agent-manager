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
		r.AddCookie(&http.Cookie{Name: cookieName, Value: e + "." + cookieMAC(ts.srv.signKey, "127.0.0.1:8260", e, "")})
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

// Log out also ends the file views opened under that sign-in, over HTTPS (key
// and companion cookie) and plain HTTP (key alone), across a restart; another
// sign-in's file views keep working.
func TestLogoutRevokesItsFileViews(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, _ := viewTask(t, ts)
	https := reqOpt(behindHTTPSProxy)
	type view struct {
		location string
		opts     []reqOpt
	}
	open := func(session reqOpt, secure bool) view {
		t.Helper()
		opts := []reqOpt{session}
		if secure {
			opts = append(opts, https)
		}
		w := ts.do(http.MethodGet, viewURL(sum.ID, "out/report.html"), "", opts...)
		if w.Code != http.StatusFound {
			t.Fatalf("view = %d", w.Code)
		}
		v := view{location: w.Header().Get("Location")}
		if secure {
			v.opts = append(v.opts, https)
			for _, c := range w.Result().Cookies() {
				if c.Name == fileCookieName {
					v.opts = append(v.opts, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: fileCookieName, Value: c.Value}) })
				}
			}
		}
		return v
	}
	first, second := ts.login(t, "127.0.0.1:8260"), ts.login(t, "127.0.0.1:8260")
	views := map[string]view{
		"first https": open(first, true), "first http": open(first, false),
		"second https": open(second, true), "second http": open(second, false),
	}
	check := func(ts *testServer, when string, want map[string]int) {
		t.Helper()
		for name, v := range views {
			if w := ts.do(http.MethodGet, v.location, "", v.opts...); w.Code != want[name] {
				t.Errorf("%s: %s = %d, want %d", when, name, w.Code, want[name])
			}
		}
	}
	check(ts, "before logout", map[string]int{"first https": 200, "first http": 200, "second https": 200, "second http": 200})
	if w := ts.do(http.MethodPost, "/api/logout", "", first); w.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", w.Code)
	}
	after := map[string]int{"first https": 401, "first http": 401, "second https": 200, "second http": 200}
	check(ts, "after logout", after)
	srv, err := NewServer(ServerConfig{Manager: ts.m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	check(&testServer{srv: srv, m: ts.m, prov: ts.prov}, "after restart", after)
}

// A file view can outlive its sign-in's cookie by up to fileKeyTTL, so Log out
// keeps it revoked that long too.
func TestLogoutKeepsFileViewsRevokedPastTheCookieExpiry(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, _ := viewTask(t, ts)
	short := sessionCookie(ts.srv.signKey, "127.0.0.1:8260", time.Now().Add(time.Minute).Unix())
	session := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieName, Value: short}) }
	w := ts.do(http.MethodGet, viewURL(sum.ID, "notes.md"), "", session)
	if w.Code != http.StatusFound {
		t.Fatalf("view = %d", w.Code)
	}
	location := w.Header().Get("Location")
	if w := ts.do(http.MethodPost, "/api/logout", "", session); w.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", w.Code)
	}
	if w := ts.do(http.MethodGet, location, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("file view after logout = %d, want 401", w.Code)
	}
	mac, _, _ := ts.srv.verifySession("127.0.0.1:8260", short)
	ts.srv.revoked.mu.Lock()
	until := ts.srv.revoked.until[fileSession(mac)]
	ts.srv.revoked.mu.Unlock()
	if least := time.Now().Add(fileKeyTTL - time.Minute).Unix(); until < least {
		t.Fatalf("file views revoked until %d, want at least %d", until, least)
	}
}
