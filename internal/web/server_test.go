package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type testServer struct {
	srv  *Server
	m    *Manager
	prov *agenttest.Provider
}

func newTestServer(t *testing.T, cfg ServerConfig) *testServer {
	t.Helper()
	m, prov, _ := newTestManager(t)
	cfg.Manager, cfg.Token, cfg.Version = m, testToken, "test"
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &testServer{srv: srv, m: m, prov: prov}
}

func validCookie(host string) string {
	return sessionCookie(testToken, host, time.Now().Add(time.Hour).Unix())
}

type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func withHost(h string) reqOpt      { return func(r *http.Request) { r.Host = h } }
func withCookie(ts *testServer) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieName, Value: validCookie(r.Host)}) }
}

func (ts *testServer) do(method, target, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, reader)
	r.Host = "127.0.0.1:8260"
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, opt := range opts {
		opt(r)
	}
	w := httptest.NewRecorder()
	ts.srv.ServeHTTP(w, r)
	return w
}

func TestAPIRequiresLoginAndCookieWorks(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	createSession(t, ts.m, ts.prov)
	for _, target := range []string{"/api/sessions", "/api/meta", "/api/events"} {
		w := ts.do(http.MethodGet, target, "")
		if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "task") || !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatalf("GET %s unauthenticated = %d %s", target, w.Code, w.Body)
		}
	}
	if w := ts.do(http.MethodGet, "/api/auth", ""); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"authenticated":false,"required":true}` {
		t.Fatalf("auth = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/login", `{"token":"wrong"}`); w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 {
		t.Fatalf("bad login = %d cookies=%v", w.Code, w.Result().Cookies())
	}
	w := ts.do(http.MethodPost, "/api/login", `{"token":"`+testToken+`"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("login = %d %s", w.Code, w.Body)
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != cookieName || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode || c[0].Path != "/" ||
		c[0].MaxAge != 30*24*60*60 || c[0].Secure || c[0].Value == testToken {
		t.Fatalf("cookie = %+v", c)
	}
	if w := ts.do(http.MethodPost, "/api/login", `{"token":"`+testToken+`"}`, withHeader("X-Forwarded-Proto", "https")); !w.Result().Cookies()[0].Secure {
		t.Fatal("cookie behind an HTTPS proxy must be Secure")
	}
	w = ts.do(http.MethodGet, "/api/sessions", "", func(r *http.Request) { r.AddCookie(c[0]) })
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"task"`) {
		t.Fatalf("authenticated list = %d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("api Cache-Control = %q", got)
	}
	for header, want := range map[string]string{"Content-Security-Policy": contentSecurity, "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer"} {
		if got := w.Header().Get(header); got != want {
			t.Fatalf("%s = %q", header, got)
		}
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("the service must never send CORS headers")
	}
	if w := ts.do(http.MethodPost, "/api/logout", "", withCookie(ts)); w.Code != http.StatusNoContent || w.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("logout = %d %v", w.Code, w.Result().Cookies())
	}
}

// --no-auth drops only the cookie requirement; every other check still runs.
func TestNoAuthSkipsLoginButKeepsOtherChecks(t *testing.T) {
	ts := newTestServer(t, ServerConfig{NoAuth: true})
	createSession(t, ts.m, ts.prov)
	w := ts.do(http.MethodGet, "/api/sessions", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"task"`) || w.Header().Get("Content-Security-Policy") != contentSecurity {
		t.Fatalf("list without cookie = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, "/api/auth", ""); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"authenticated":true,"required":false}` {
		t.Fatalf("auth = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, "/api/sessions", "", withHost("evil.example")); w.Code != http.StatusForbidden {
		t.Fatalf("foreign Host = %d, want 403", w.Code)
	}
	create := `{"provider":"fake","workdir":"` + t.TempDir() + `","name":"x"}`
	if w := ts.do(http.MethodPost, "/api/sessions", create, withHeader("Sec-Fetch-Site", "cross-site")); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST = %d, want 403", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/sessions", create, withHeader("Origin", "https://evil.example")); w.Code != http.StatusForbidden {
		t.Fatalf("foreign Origin POST = %d, want 403", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/sessions", create, withHeader("Content-Type", "text/plain")); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain POST = %d, want 415", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/sessions", strings.Repeat(" ", maxBodyBytes+10)+create); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d, want 413", w.Code)
	}
	if n := len(ts.m.List()); n != 1 {
		t.Fatalf("rejected requests created sessions: %d sessions", n)
	}
}

func TestHostOriginAndContentTypeChecks(t *testing.T) {
	ts := newTestServer(t, ServerConfig{PublicOrigins: []string{"https://uam.example.com"}})
	login := `{"token":"` + testToken + `"}`
	for _, host := range []string{"evil.example", "evil.example:8260", "127.0.0.1.evil.example"} {
		if w := ts.do(http.MethodGet, "/api/auth", "", withHost(host)); w.Code != http.StatusForbidden {
			t.Fatalf("Host %q = %d, want 403", host, w.Code)
		}
	}
	for _, host := range []string{"localhost:9999", "[::1]:8260", "127.0.0.1", "uam.example.com"} {
		if w := ts.do(http.MethodGet, "/api/auth", "", withHost(host)); w.Code != http.StatusOK {
			t.Fatalf("Host %q = %d, want 200", host, w.Code)
		}
	}
	if w := ts.do(http.MethodPost, "/api/login", login, withHeader("Sec-Fetch-Site", "cross-site")); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST = %d, want 403", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/login", login, withHeader("Origin", "https://evil.example")); w.Code != http.StatusForbidden {
		t.Fatalf("foreign Origin POST = %d, want 403", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/login", login, withHeader("Content-Type", "text/plain")); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain POST = %d, want 415", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/login", login, withHeader("Content-Type", "application/x-www-form-urlencoded")); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("form POST = %d, want 415", w.Code)
	}
	// Behind a same-host reverse proxy that preserves Host, a same-origin
	// browser request passes with no extra trusted-origin configuration.
	proxied := []reqOpt{withHost("uam.example.com"), withHeader("Origin", "https://uam.example.com"), withHeader("X-Forwarded-Proto", "https")}
	if w := ts.do(http.MethodPost, "/api/login", login, append(proxied, withHeader("Sec-Fetch-Site", "same-origin"))...); w.Code != http.StatusNoContent {
		t.Fatalf("proxied same-origin login = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/login", login, proxied...); w.Code != http.StatusNoContent {
		t.Fatalf("proxied Origin-only login = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/login", strings.Repeat(" ", maxBodyBytes+10)+login); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d, want 413", w.Code)
	}
	if _, err := NewServer(ServerConfig{Manager: ts.m, Token: testToken, PublicOrigins: []string{"https://host/path"}}); err == nil {
		t.Fatal("a public origin with a path must be rejected")
	}
}

// Beyond loopback the service also answers IP-literal Hosts (the LAN
// address); a name stays refused on every bind, and the cross-origin and JSON
// checks do not change.
func TestHostRuleFollowsTheBind(t *testing.T) {
	for _, tc := range []struct {
		listen  string
		ipHosts bool
	}{{"", false}, {"127.0.0.1:8260", false}, {"[::1]:8260", false}, {"0.0.0.0:8260", true}, {"[::]:8260", true}, {"192.0.2.10:8260", true}} {
		ts := newTestServer(t, ServerConfig{Listen: tc.listen, PublicOrigins: []string{"https://uam.example.com"}})
		for _, host := range []string{"localhost:8260", "127.0.0.1:8260", "[::1]:8260", "uam.example.com"} {
			if w := ts.do(http.MethodGet, "/api/auth", "", withHost(host)); w.Code != http.StatusOK {
				t.Fatalf("listen %q: Host %q = %d, want 200", tc.listen, host, w.Code)
			}
		}
		for _, host := range []string{"evil.example", "evil.example:8260", "127.0.0.1.evil.example", "192.0.2.10.nip.io:8260", "[fe80::1%25eth0]:8260"} {
			if w := ts.do(http.MethodGet, "/api/auth", "", withHost(host)); w.Code != http.StatusForbidden {
				t.Fatalf("listen %q: Host %q = %d, want 403", tc.listen, host, w.Code)
			}
		}
		for _, host := range []string{"192.0.2.10:8260", "192.0.2.10", "10.1.2.3:9999", "[2001:db8::1]:8260", "0.0.0.0:8260"} {
			want := http.StatusForbidden
			if tc.ipHosts {
				want = http.StatusOK
			}
			if w := ts.do(http.MethodGet, "/api/auth", "", withHost(host)); w.Code != want {
				t.Fatalf("listen %q: Host %q = %d, want %d", tc.listen, host, w.Code, want)
			}
		}
		if !tc.ipHosts {
			continue
		}
		login := `{"token":"` + testToken + `"}`
		lan := []reqOpt{withHost("192.0.2.10:8260")}
		if w := ts.do(http.MethodPost, "/api/login", login, append(lan, withHeader("Origin", "https://evil.example"))...); w.Code != http.StatusForbidden {
			t.Fatalf("listen %q: foreign Origin POST at the LAN address = %d, want 403", tc.listen, w.Code)
		}
		if w := ts.do(http.MethodPost, "/api/login", login, append(lan, withHeader("Content-Type", "text/plain"))...); w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("listen %q: text/plain POST at the LAN address = %d, want 415", tc.listen, w.Code)
		}
		if w := ts.do(http.MethodPost, "/api/login", login, append(lan, withHeader("Origin", "http://192.0.2.10:8260"))...); w.Code != http.StatusNoContent || w.Result().Cookies()[0].Secure {
			t.Fatalf("listen %q: same-origin login at the LAN address = %d %v", tc.listen, w.Code, w.Result().Cookies())
		}
	}
}

// --log-headers writes one JSON record per request where the checks decide,
// refused requests included, with credentials redacted and bodies never read.
func TestLogHeaders(t *testing.T) {
	var buf bytes.Buffer
	previous := uamlog.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { uamlog.SetLogger(previous) })
	records := func() []map[string]any {
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var rec map[string]any
			if line != "" && json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == "web request" {
				out = append(out, rec)
			}
		}
		buf.Reset()
		return out
	}
	login := `{"token":"` + testToken + `"}`

	off := newTestServer(t, ServerConfig{})
	off.do(http.MethodGet, "/api/auth", "")
	off.do(http.MethodGet, "/api/auth", "", withHost("evil.example"))
	if got := records(); len(got) != 0 {
		t.Fatalf("header logging is off by default, got %v", got)
	}

	ts := newTestServer(t, ServerConfig{LogHeaders: true})
	forged := "v\r\n{\"level\":\"ERROR\",\"msg\":\"web request\",\"outcome\":\"forged\"}\n"
	buf.Reset()
	ts.do(http.MethodGet, "/api/auth?probe=1", "", func(r *http.Request) {
		r.Header["cookie"] = []string{"uam_web=secret-1"}
		r.Header["AUTHORIZATION"] = []string{"Bearer secret-2"}
		r.Header.Set("Proxy-Authorization", "Basic secret-3")
		r.Header.Set("X-Api-Key", "secret-4")
		r.Header.Set("X-Auth-Token", "secret-5")
		r.Header.Set("Cf-Access-Jwt-Assertion", "secret-6")
		r.Header.Set("X-Long", strings.Repeat("a", 4000))
		r.Header.Set("X-Evil", forged)
	})
	raw := buf.String()
	got := records()
	if len(got) != 1 || strings.Count(strings.TrimSpace(raw), "\n") != 0 || strings.Contains(raw, "secret-") {
		t.Fatalf("one record without credentials expected, got %q", raw)
	}
	rec := got[0]
	if rec["method"] != "GET" || rec["path"] != "/api/auth?probe=1" || rec["remote"] != "192.0.2.1:1234" || rec["host"] != "127.0.0.1:8260" ||
		rec["outcome"] != "allowed" || rec["status"] != nil {
		t.Fatalf("allowed record = %v", rec)
	}
	headers, _ := rec["headers"].(map[string]any)
	for _, name := range []string{"cookie", "AUTHORIZATION", "Proxy-Authorization", "X-Api-Key", "X-Auth-Token", "Cf-Access-Jwt-Assertion"} {
		if v, _ := headers[name].([]any); len(v) != 1 || v[0] != "[redacted]" {
			t.Fatalf("header %s = %v, want [redacted]", name, headers[name])
		}
	}
	if v, _ := headers["X-Long"].([]any); len(v) != 1 || len(v[0].(string)) > maxLoggedValue+len("…") {
		t.Fatalf("long header not capped: %d bytes", len(v[0].(string)))
	}
	if v, _ := headers["X-Evil"].([]any); len(v) != 1 || v[0] != forged {
		t.Fatalf("CR/LF header = %v, want it kept inside the one record", headers["X-Evil"])
	}

	for _, tc := range []struct {
		name, method, target, body, outcome string
		status                              int
		opts                                []reqOpt
	}{
		{"foreign Host", http.MethodGet, "/api/auth", "", "host not allowed", http.StatusForbidden, []reqOpt{withHost("evil.example")}},
		{"foreign Origin", http.MethodPost, "/api/login", login, "cross-origin request rejected", http.StatusForbidden, []reqOpt{withHeader("Origin", "https://evil.example")}},
		{"text/plain", http.MethodPost, "/api/login", login, "requests must use Content-Type: application/json", http.StatusUnsupportedMediaType, []reqOpt{withHeader("Content-Type", "text/plain")}},
		{"no cookie", http.MethodGet, "/api/sessions", "", "authentication required", http.StatusUnauthorized, nil},
		{"login", http.MethodPost, "/api/login", login, "allowed", 0, nil},
	} {
		w := ts.do(tc.method, tc.target, tc.body, tc.opts...)
		raw := buf.String()
		got := records()
		if len(got) != 1 || got[0]["outcome"] != tc.outcome || got[0]["method"] != tc.method || got[0]["path"] != tc.target {
			t.Fatalf("%s: records = %v", tc.name, got)
		}
		if status, _ := got[0]["status"].(float64); int(status) != tc.status || (tc.status != 0 && w.Code != tc.status) {
			t.Fatalf("%s: logged status %v, response %d, want %d", tc.name, got[0]["status"], w.Code, tc.status)
		}
		if strings.Contains(raw, testToken) {
			t.Fatalf("%s: a request body reached the log: %q", tc.name, raw)
		}
	}
}

func TestEmbeddedIndexAndSPAFallback(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	for _, target := range []string{"/", "/sessions/abc", "/index.html"} {
		w := ts.do(http.MethodGet, target, "")
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), `<div id="root">`) {
			t.Fatalf("GET %s = %d %q %q", target, w.Code, w.Header().Get("Content-Type"), w.Body)
		}
	}
	assets := fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><title>app</title>")},
		"assets/app.js":    {Data: []byte("console.log(1)")},
		"assets/style.css": {Data: []byte("body{}")},
	}
	ts2 := newTestServer(t, ServerConfig{Assets: assets})
	if w := ts2.do(http.MethodGet, "/assets/app.js", ""); w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("js = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w := ts2.do(http.MethodGet, "/assets/style.css", ""); !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("css content type %q", w.Header().Get("Content-Type"))
	}
	if w := ts2.do(http.MethodGet, "/assets/", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<title>app</title>") {
		t.Fatalf("directory must not be listed: %d %s", w.Code, w.Body)
	}
	if w := ts2.do(http.MethodGet, "/assets/missing.js", ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing asset = %d, want 404", w.Code)
	}
	if w := ts2.do(http.MethodGet, "/api/nope", "", withCookie(ts2)); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("unknown api = %d %s", w.Code, w.Body)
	}
}

func TestSessionRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	dir := t.TempDir()
	w := ts.do(http.MethodPost, "/api/projects", `{"dir":"`+dir+`"}`, auth)
	var project Project
	if err := json.Unmarshal(w.Body.Bytes(), &project); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("add project = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodPost, "/api/sessions", `{"provider":"fake","project_id":"`+project.ID+`","name":"api task"}`, auth)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var sum SessionSummary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil || sum.State != StateIdle || !sum.Open || sum.Capabilities != allCaps {
		t.Fatalf("created summary = %+v %v", sum, err)
	}
	conv := ts.prov.Last()
	rid := mustUUID(t)
	prompt := `{"text":"hello","request_id":"` + rid + `"}`
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/prompt", prompt, auth); w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"status":"accepted"`) {
		t.Fatalf("prompt = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/prompt", prompt, auth); w.Code != http.StatusAccepted || len(conv.Sends()) != 1 {
		t.Fatalf("repeat prompt = %d sends=%d", w.Code, len(conv.Sends()))
	}
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/prompt", `{"text":"again","request_id":"`+mustUUID(t)+`"}`, auth); w.Code != http.StatusConflict {
		t.Fatalf("prompt while working = %d, want 409", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/prompt", `{"text":"x"}`, auth); w.Code != http.StatusBadRequest {
		t.Fatalf("prompt without request_id = %d, want 400", w.Code)
	}
	conv.EmitInteraction(permissionRequest("p1"))
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/interactions/p1", `{"decision":"maybe"}`, auth); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid decision = %d, want 400", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/interactions/p1", `{"decision":"allow"}`, auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"answered"`) {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/interactions/p1", `{"decision":"deny"}`, auth); w.Code != http.StatusConflict {
		t.Fatalf("second answer = %d, want 409", w.Code)
	}
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/cancel", "", auth); w.Code != http.StatusAccepted || conv.Cancels() != 1 {
		t.Fatalf("cancel = %d cancels=%d", w.Code, conv.Cancels())
	}
	if w := ts.do(http.MethodPatch, "/api/sessions/"+sum.ID, `{"name":"renamed"}`, auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"renamed"`) {
		t.Fatalf("rename = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodGet, "/api/sessions/"+sum.ID, "", auth)
	var d SessionDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || d.LastSubmission == nil || d.LastSubmission.RequestID != rid || len(d.Interactions) != 1 {
		t.Fatalf("detail = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/close", "", auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"closed"`) || conv.Closes() != 1 {
		t.Fatalf("close = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/missing", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("missing session = %d", w.Code)
	}
	w = ts.do(http.MethodGet, "/api/meta", "", auth)
	var meta Meta
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil || meta.Version != "test" || len(meta.Providers) != 1 || !meta.Providers[0].Available {
		t.Fatalf("meta = %s", w.Body)
	}
	canonical, _ := canonicalWorkdir(dir)
	if len(meta.RecentWorkdirs) != 1 || meta.RecentWorkdirs[0] != canonical {
		t.Fatalf("recent workdirs = %q", meta.RecentWorkdirs)
	}
}

func TestPromptModeAndQueueRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, conv := createSession(t, ts.m, ts.prov)
	base := "/api/sessions/" + sum.ID
	prompt := func(text, rid, mode string) *httptest.ResponseRecorder {
		body := `{"text":"` + text + `","request_id":"` + rid + `"`
		if mode != "" {
			body += `,"mode":"` + mode + `"`
		}
		return ts.do(http.MethodPost, base+"/prompt", body+"}", auth)
	}
	if w := prompt("first", mustUUID(t), ""); w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"status":"accepted"`) {
		t.Fatalf("prompt without mode = %d %s", w.Code, w.Body)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if w := prompt("x", mustUUID(t), "later"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown mode = %d, want 400", w.Code)
	}
	if w := prompt("x", mustUUID(t), "send"); w.Code != http.StatusConflict {
		t.Fatalf("send while busy = %d, want 409", w.Code)
	}
	rid := mustUUID(t)
	w := prompt("queued one", rid, "queue")
	var sub Submission
	if err := json.Unmarshal(w.Body.Bytes(), &sub); err != nil || w.Code != http.StatusAccepted || sub.Status != SubmissionQueued || sub.RequestID != rid {
		t.Fatalf("queue while busy = %d %s", w.Code, w.Body)
	}
	if w := prompt("steer it", mustUUID(t), "steer"); w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"status":"accepted"`) || len(conv.Steers()) != 1 {
		t.Fatalf("steer while busy = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodGet, base, "", auth)
	var d SessionDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || d.Queued != 1 || len(d.Queue) != 1 || d.Queue[0].RequestID != rid || d.Queue[0].Text != "queued one" || d.QueuePaused {
		t.Fatalf("detail = %d %s", w.Code, w.Body)
	}
	for range 2 {
		if w := ts.do(http.MethodDelete, base+"/queue/"+rid, "", auth); w.Code != http.StatusNoContent {
			t.Fatalf("cancel queued = %d %s", w.Code, w.Body)
		}
	}
	if w := ts.do(http.MethodDelete, base+"/queue/"+mustUUID(t), "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("cancel unknown = %d, want 404", w.Code)
	}
	prompt("again", mustUUID(t), "queue")
	conv.EmitTurn(agentapi.TurnCancelled, "")
	if w := ts.do(http.MethodPost, base+"/queue/resume", "", auth); w.Code != http.StatusNoContent {
		t.Fatalf("resume = %d %s", w.Code, w.Body)
	}
	waitUntil(t, "resumed queue sent", func() bool { return len(conv.Sends()) == 2 })
	if w := prompt("more", mustUUID(t), "queue"); !strings.Contains(w.Body.String(), `"status":"queued"`) {
		t.Fatalf("queue behind the resumed prompt = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, base+"/queue/clear", "", auth); w.Code != http.StatusNoContent || ts.m.List()[0].Queued != 0 {
		t.Fatalf("clear = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, "/api/sessions/missing/queue/clear", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("clear unknown session = %d, want 404", w.Code)
	}
}

func TestEventStreamOverHTTP(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	ts.srv.heartbeat = 50 * time.Millisecond
	sum, conv := createSession(t, ts.m, ts.prov)
	httpSrv := httptest.NewServer(ts.srv)
	defer httpSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpSrv.URL+"/api/events?session="+sum.ID, nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: validCookie(req.Host)})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(resp.Body)
	next := func() string {
		var b strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("read stream: %v", err)
			}
			if line == "\n" {
				if b.Len() > 0 && !strings.HasPrefix(b.String(), "retry:") {
					return b.String()
				}
				b.Reset()
				continue
			}
			b.WriteString(line)
		}
	}
	if first := next(); !strings.Contains(first, "event: snapshot") {
		t.Fatalf("first event = %q", first)
	}
	const text = "<script>alert(1)</script>\n\nevent: forged\ndata: {}"
	conv.EmitItem(agentapi.Item{ID: "a1", Kind: agentapi.ItemAssistant, Text: text})
	sawItem, sawHeartbeat := false, false
	for !sawItem || !sawHeartbeat {
		ev := next()
		if strings.HasPrefix(ev, "event: item\n") {
			if strings.Contains(ev, "<script>") || strings.Contains(ev, "\nevent: forged") {
				t.Fatalf("unescaped stream content = %q", ev)
			}
			var payload struct {
				Item agentapi.Item `json:"item"`
			}
			data := strings.TrimSuffix(strings.TrimPrefix(ev, "event: item\ndata: "), "\n")
			if err := json.Unmarshal([]byte(data), &payload); err != nil || payload.Item.Text != text {
				t.Fatalf("stream JSON = %q, %v", data, err)
			}
		}
		sawItem = sawItem || strings.Contains(ev, "event: item")
		sawHeartbeat = sawHeartbeat || strings.HasPrefix(ev, ": keep-alive")
	}
	cancel() // the browser goes away
	waitUntil(t, "subscriber removed", func() bool {
		ts.m.mu.Lock()
		defer ts.m.mu.Unlock()
		return len(ts.m.subs) == 0
	})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if conv.Cancels() != 0 || conv.Closes() != 0 {
		t.Fatal("stream disconnect reached the provider")
	}
	if d := detail(t, ts.m, sum.ID); d.State != StateCompleted {
		t.Fatalf("state after disconnect = %s", d.State)
	}
}

func TestSessionCookieIsHostBoundAndExpires(t *testing.T) {
	ts := newTestServer(t, ServerConfig{PublicOrigins: []string{"https://uam.example.com"}})
	sessions := func(host, cookie string) int {
		return ts.do(http.MethodGet, "/api/sessions", "", withHost(host), func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
		}).Code
	}
	loopback := validCookie("127.0.0.1:8260")
	if code := sessions("127.0.0.1:8260", loopback); code != http.StatusOK {
		t.Fatalf("cookie on its own host = %d, want 200", code)
	}
	if code := sessions("uam.example.com", loopback); code != http.StatusUnauthorized {
		t.Fatalf("loopback cookie replayed at the public host = %d, want 401", code)
	}
	expired := sessionCookie(testToken, "127.0.0.1:8260", time.Now().Add(-time.Second).Unix())
	if code := sessions("127.0.0.1:8260", expired); code != http.StatusUnauthorized {
		t.Fatalf("expired cookie = %d, want 401", code)
	}
	for _, bad := range []string{"", "nodot", "x." + strings.Repeat("0", 64)} {
		if code := sessions("127.0.0.1:8260", bad); code != http.StatusUnauthorized {
			t.Fatalf("malformed cookie %q = %d, want 401", bad, code)
		}
	}
	login := `{"token":"` + testToken + `"}`
	w := ts.do(http.MethodPost, "/api/login", login, withHost("uam.example.com"), withHeader("Origin", "https://uam.example.com"))
	if w.Code != http.StatusNoContent {
		t.Fatalf("login = %d %s", w.Code, w.Body)
	}
	minted := w.Result().Cookies()[0].Value
	if code := sessions("uam.example.com", minted); code != http.StatusOK {
		t.Fatalf("minted cookie on its host = %d, want 200", code)
	}
	if code := sessions("127.0.0.1:8260", minted); code != http.StatusUnauthorized {
		t.Fatalf("public cookie replayed at loopback = %d, want 401", code)
	}
}

func TestLoginBodyIsSmall(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	body := `{"token":"` + strings.Repeat("a", maxLoginBytes) + `"}`
	if w := ts.do(http.MethodPost, "/api/login", body); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized login = %d, want 413", w.Code)
	}
}
