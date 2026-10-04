package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestConnectedWorkloadPolicy(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/api/meta", true},
		{"POST", "/api/sessions", true},
		{"POST", "/api/sessions/task/prompt", true},
		{"GET", "/api/events", true},
		{"HEAD", "/api/sessions/task/files/view/out/index.html", true},
		{"GET", "/api/projects/project/terminal", true},
		{"POST", "/api/sessions/task/attachments", true},
		{"GET", "/api/sessions/task/file-grants/grant/key", true},
		{"POST", "/api/providers/copilot/account/sign-in", true},
		{"GET", "/api/auth", false},
		{"POST", "/api/login", false},
		{"POST", "/api/logout", false},
		{"GET", "/api/connections", false},
		{"GET", "/api/connected/other/api/sessions", false},
		{"POST", "/api/federation/pair", false},
		{"GET", "/api/federation/workload/api/sessions", false},
		{"GET", "/api/push", false},
		{"POST", "/api/push/subscribe", false},
		{"POST", "/api/viewing", false},
		{"GET", "/api/sessions/task/files/key/key/index.html", false},
		{"POST", "/api/events", false},
		{"TRACE", "/api/sessions", false},
		{"CONNECT", "/api/sessions", false},
		{"GET", "/api/new-admin-feature", false},
		{"GET", "/sw.js", false},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			_, pattern := ts.srv.mux.Handler(r)
			if got := connectedWorkloadPatternAllowed(pattern); got != tc.allowed {
				t.Fatalf("route %q allowed=%v, want %v", pattern, got, tc.allowed)
			}
		})
	}
	// A new owner route remains private even when it exists in the router.
	ts.srv.mux.HandleFunc("GET /api/secrets", func(http.ResponseWriter, *http.Request) {})
	_, pattern := ts.srv.mux.Handler(httptest.NewRequest("GET", "/api/secrets", nil))
	if connectedWorkloadPatternAllowed(pattern) {
		t.Fatal("new owner API was implicitly granted to attached instances")
	}
}
