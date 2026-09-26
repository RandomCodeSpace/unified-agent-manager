package web

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func frameAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                   {Data: []byte(`<!doctype html><div id="root"></div>`)},
		"diagram-frame.html":           {Data: []byte(`<!doctype html><script src="/assets/diagram-frame-abc.js"></script>`)},
		"assets/diagram-frame-abc.js":  {Data: []byte("(()=>{})();")},
		"assets/app.js":                {Data: []byte("console.log(1)")},
		"nested/diagram-frame.html":    {Data: []byte("<!doctype html>not the frame")},
		"assets/style.css":             {Data: []byte("body{}")},
		"assets/inter-latin.woff2":     {Data: []byte("wOF2")},
		"assets/diagram-frame-abc.map": {Data: []byte("{}")},
	}
}

// The diagram frame document gets its own policy and may be framed by this
// origin; method and cross-origin checks still apply, and
// no cookie is needed, as for the rest of the static app.
func TestDiagramFrameDocumentHeaders(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: frameAssets()})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := ts.do(method, "/diagram-frame.html", "")
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s frame = %d %q", method, w.Code, w.Header().Get("Content-Type"))
		}
		for header, want := range map[string]string{
			"Content-Security-Policy": frameSecurity,
			"X-Frame-Options":         "SAMEORIGIN",
			"Cache-Control":           "no-cache",
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         "no-referrer",
		} {
			if got := w.Header().Get(header); got != want {
				t.Fatalf("%s %s = %q, want %q", method, header, got, want)
			}
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("the frame document must not carry CORS headers")
		}
	}
	// The policy follows the document: the one spelling the mux does not
	// redirect but handleStatic cleans to the frame gets it too.
	if w := ts.do(http.MethodGet, "/diagram-frame.html/", ""); w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") != frameSecurity || !strings.Contains(w.Body.String(), "diagram-frame-abc.js") {
		t.Fatalf("GET /diagram-frame.html/ = %d %q", w.Code, w.Header().Get("Content-Security-Policy"))
	}
	if w := ts.do(http.MethodPost, "/diagram-frame.html", "{}"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST frame = %d, want 405", w.Code)
	}
	for _, directive := range []string{"default-src 'none'", "connect-src 'none'", "frame-ancestors 'self'", "script-src 'self'", "style-src 'self' 'unsafe-inline'"} {
		if !strings.Contains(frameSecurity, directive) {
			t.Fatalf("frame policy lacks %q", directive)
		}
	}
	if strings.Contains(frameSecurity, "allow-same-origin") || strings.Contains(frameSecurity, "unsafe-eval") {
		t.Fatal("frame policy must not grant eval")
	}
}

// Every response but the frame document keeps the service policy, byte for
// byte, and stays unframeable: the relaxation never leaks to a look-alike
// path, an asset, the SPA fallback, the API or a refusal.
func TestOnlyTheDiagramFrameRelaxesThePolicy(t *testing.T) {
	const want = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
	if contentSecurity != want {
		t.Fatalf("service policy changed:\n got %q\nwant %q", contentSecurity, want)
	}
	ts := newTestServer(t, ServerConfig{Assets: frameAssets()})
	createSession(t, ts.m, ts.prov)
	paths := []string{
		"/", "/index.html", "/sessions/abc", "/diagram-frame", "/diagram-frame.htm", "/nested/diagram-frame.html", "/diagram-frame.html.bak",
		"/assets/diagram-frame-abc.js", "/assets/diagram-frame-abc.map", "/assets/app.js", "/assets/style.css", "/assets/inter-latin.woff2",
		"/missing.js", "/api/auth", "/api/sessions", "/api/nope", "/api/sessions/none",
	}
	for _, p := range paths {
		w := ts.do(http.MethodGet, p, "", withCookie(ts))
		if got := w.Header().Get("Content-Security-Policy"); got != contentSecurity {
			t.Fatalf("GET %s (%d): CSP = %q", p, w.Code, got)
		}
		if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("GET %s (%d): X-Frame-Options = %q", p, w.Code, got)
		}
	}
	// Refusals and state changes too.
	for _, w := range []interface{ Header() http.Header }{
		ts.do(http.MethodPost, "/diagram-frame.html", "{}", withHeader("Origin", "https://evil.example")),
		ts.do(http.MethodGet, "/api/sessions", ""),
		ts.do(http.MethodPost, "/api/login", `{"token":"`+testToken+`"}`),
		ts.do(http.MethodPost, "/api/logout", "", withCookie(ts)),
	} {
		if got := w.Header().Get("Content-Security-Policy"); got != contentSecurity {
			t.Fatalf("CSP = %q", got)
		}
	}
}
