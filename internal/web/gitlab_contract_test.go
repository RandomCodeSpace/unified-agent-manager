package web

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"
	"testing/fstest"
)

func TestGitLabConnectedRoutes(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		allowed bool
		gated   bool
	}{
		{"GET /api/sessions/{id}/gitlab", true, false},
		{"GET /api/sessions/{id}/gitlab/jobs/{job_id}/trace", true, false},
		{"POST /api/sessions/{id}/gitlab/refresh", true, true},
		{"POST /api/sessions/{id}/gitlab/investigate", true, true},
		{"GET /api/sessions/{id}/gitlab/merge-requests/{iid}", true, false},
		{"POST /api/sessions/{id}/gitlab/merge-requests/{iid}/actions", true, true},
		{"POST /api/sessions/{id}/gitlab/merge-requests/{iid}/comments", true, false},
		{"POST /api/sessions/{id}/gitlab/merge-requests/{iid}/resolve", true, false},
		{"GET /api/sessions/{id}/gitlab/merge-requests/{iid}/actions", false, false},
		{"DELETE /api/sessions/{id}/gitlab/merge-requests/{iid}", false, false},
		{"POST /api/sessions/{id}/gitlab/merge-requests/{iid}", false, false},
		{"POST /api/sessions/{id}/gitlab", false, false},
		{"GET /api/sessions/{id}/gitlab/investigate", false, false},
		{"POST /api/sessions/{id}/gitlab/jobs/{job_id}/trace", false, false},
		{"GET /api/sessions/{id}/gitlab/jobs", false, false},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			if got := connectedWorkloadPatternAllowed(tc.pattern); got != tc.allowed {
				t.Fatalf("allowed=%v, want %v", got, tc.allowed)
			}
			if got := connectedAccountGated(tc.pattern); got != tc.gated {
				t.Fatalf("account-gated=%v, want %v", got, tc.gated)
			}
		})
	}
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	if !slices.Contains(ts.srv.federationDescriptor().Capabilities, "gitlab-pipelines-v1") {
		t.Fatal("GitLab pipeline capability is not advertised")
	}
	if !slices.Contains(ts.srv.federationDescriptor().Capabilities, "gitlab-reviews-v1") {
		t.Fatal("GitLab review capability is not advertised")
	}
	for _, path := range []string{
		"/api/sessions/task/gitlab/merge-requests/1",
		"/api/sessions/task/gitlab/merge-requests/1/actions",
		"/api/sessions/task/gitlab/merge-requests/1/comments",
		"/api/sessions/task/gitlab/merge-requests/1/resolve",
	} {
		method := "POST"
		if path == "/api/sessions/task/gitlab/merge-requests/1" {
			method = "GET"
		}
		_, pattern := ts.srv.mux.Handler(httptest.NewRequest(method, path, nil))
		if !connectedWorkloadPatternAllowed(pattern) {
			t.Fatalf("review route %s %s is not registered for connected instances: %q", method, path, pattern)
		}
	}
}

func TestGitLabSettingsJSON(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		data, err := json.Marshal(Settings{GitLabAutoInvestigate: enabled})
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		value, present := fields["gitlab_auto_investigate"]
		if present != enabled || (enabled && string(value) != "true") {
			t.Fatalf("automatic investigation setting %v encoded as %s", enabled, data)
		}
	}
}
