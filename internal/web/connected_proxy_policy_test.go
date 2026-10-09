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
		{"POST", "/api/sessions/task/fork", true},
		{"GET", "/api/sessions/task/fork", false},
		{"POST", "/api/sessions/task/fork/dismiss", true},
		{"DELETE", "/api/sessions/task/fork/dismiss", false},
		{"GET", "/api/sessions/task/rewind/preview", true},
		{"POST", "/api/sessions/task/rewind", true},
		{"POST", "/api/sessions/task/rewind/reconcile", true},
		{"POST", "/api/sessions/task/rewind/release", true},
		{"GET", "/api/sessions/task/rewind/release", false},
		{"POST", "/api/sessions/task/resend", true},
		{"GET", "/api/sessions/task/resend", false},
		{"GET", "/api/sessions/task/rewind", false},
		{"DELETE", "/api/sessions/task/rewind", false},
		{"GET", "/api/events", true},
		{"HEAD", "/api/sessions/task/files/view/out/index.html", true},
		{"GET", "/api/projects/project/terminal", true},
		{"POST", "/api/sessions/task/attachments", true},
		{"GET", "/api/sessions/task/file-grants/grant/key", true},
		{"POST", "/api/providers/copilot/account/sign-in", true},
		// Every Settings section of a connected instance is managed through it.
		{"GET", "/api/settings", true},
		{"PATCH", "/api/settings", true},
		{"POST", "/api/settings/custom-models/discover", true},
		{"GET", "/api/providers/copilot/account", true},
		{"POST", "/api/providers/copilot/account/sign-out", true},
		{"POST", "/api/providers/copilot/account/device", true},
		{"GET", "/api/providers/copilot/account/device", true},
		{"DELETE", "/api/providers/copilot/account/device", true},
		{"DELETE", "/api/providers/copilot/account/link", true},
		{"GET", "/api/providers/copilot/cli", true},
		{"POST", "/api/providers/copilot/cli/update", true},
		{"GET", "/api/mcp", true},
		{"POST", "/api/mcp/servers", true},
		{"PUT", "/api/mcp/servers/docs", true},
		{"PATCH", "/api/mcp/servers/docs", true},
		{"DELETE", "/api/mcp/servers/docs", true},
		{"GET", "/api/configuration", true},
		{"POST", "/api/configuration/skills/draft", true},
		{"POST", "/api/configuration/skills/list", true},
		{"POST", "/api/configuration/skills/install", true},
		{"PUT", "/api/configuration/skills/name", true},
		{"DELETE", "/api/configuration/skills/name", true},
		// Same policy as the skill file toggle above: Settings writes are allowed.
		{"POST", "/api/configuration/skills/global-disabled", true},
		{"GET", "/api/usage/prices", true},
		{"GET", "/api/sessions/task/turns/turn/todos", true},
		{"GET", "/api/sessions/task/usage-metrics", true},
		{"POST", "/api/sessions/task/usage-metrics", false},
		{"GET", "/api/sessions/task/usage-metrics/extra", false},
		{"GET", "/api/sessions/task/items/tool/diff", true},
		{"POST", "/api/sessions/task/items/tool/diff", false},
		{"POST", "/api/sessions/task/turns/turn/todos", false},
		{"GET", "/api/sessions/task/context", true},
		{"GET", "/api/sessions/task/context?attribution=true", true},
		{"POST", "/api/sessions/task/context", false},
		{"GET", "/api/sessions/task/context/extra", false},
		{"GET", "/api/sessions/task/turns/turn/changes", true},
		{"POST", "/api/sessions/task/turns/turn/changes", false},
		{"GET", "/api/utility", true},
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
	if connectedAccountGated("GET /api/sessions/{id}/usage-metrics") {
		t.Fatal("native Task usage is incorrectly account-gated")
	}
	// A new owner route remains private even when it exists in the router.
	ts.srv.mux.HandleFunc("GET /api/secrets", func(http.ResponseWriter, *http.Request) {})
	_, pattern := ts.srv.mux.Handler(httptest.NewRequest("GET", "/api/secrets", nil))
	if connectedWorkloadPatternAllowed(pattern) {
		t.Fatal("new owner API was implicitly granted to attached instances")
	}
	if connectedAccountGated("GET /api/sessions/{id}/context") {
		t.Fatal("read-only context metadata was account-gated")
	}
}
