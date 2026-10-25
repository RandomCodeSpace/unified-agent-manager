package web

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func TestRevertChange(t *testing.T) {
	const hunk = "@@ -1,2 +1,3 @@\n one\n-two\n+three\n+four\n"
	for _, tc := range []struct {
		name, path, hunk string
		archived         bool
		status           int
		file, want       string // want "" means the file is gone
	}{
		{name: "one hunk", path: "tracked.txt", hunk: hunk, file: "tracked.txt", want: "one\ntwo\n"},
		{name: "stale hunk", path: "tracked.txt", hunk: "@@ -1,2 +1,3 @@\n one\n-two\n+THREE\n+four\n", status: http.StatusConflict, file: "tracked.txt", want: "one\nthree\nfour\n"},
		{name: "hunk naming another file", path: "tracked.txt", hunk: hunk + "diff --git a/unchanged.txt b/unchanged.txt\n", status: http.StatusBadRequest, file: "tracked.txt", want: "one\nthree\nfour\n"},
		{name: "hunk smuggling another file's patch", path: "tracked.txt", hunk: hunk + "--- a/unchanged.txt\n+++ b/unchanged.txt\n@@ -1 +1 @@\n-same\n+evil\n", status: http.StatusBadRequest, file: "unchanged.txt", want: "same\n"},
		{name: "whole tracked file", path: "tracked.txt", file: "tracked.txt", want: "one\ntwo\n"},
		{name: "whole new file", path: "sub/new file.txt", file: "sub/new file.txt"},
		{name: "file the thread did not edit", path: "unchanged.txt", status: http.StatusBadRequest, file: "unchanged.txt", want: "same\n"},
		{name: "archived thread", path: "tracked.txt", archived: true, status: http.StatusConflict, file: "tracked.txt", want: "one\nthree\nfour\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitRepoFixture(t)
			prov := agenttest.NewProvider("native", agentapi.Capabilities{History: true, SessionDiff: true, SessionDiffNeedsTracking: true})
			m := startManager(t, openTestStore(t), prov)
			sum, err := m.Create(CreateRequest{Provider: "native", ProjectID: addProject(t, m, dir)})
			if err != nil {
				t.Fatal(err)
			}
			conv := prov.Last()
			conv.EmitItem(toolEdit("e1", "edit", "tracked.txt", agentapi.ToolCompleted, time.Now()))
			conv.EmitItem(toolEdit("e2", "create", filepath.Join(dir, "sub/new file.txt"), agentapi.ToolCompleted, time.Now()))
			if tc.archived {
				m.mu.Lock()
				m.sessions[sum.ID].stage = StageArchived
				m.mu.Unlock()
			}
			err = m.RevertChange(t.Context(), sum.ID, tc.path, tc.hunk)
			if got := statusOf(err); tc.status == 0 && err != nil || tc.status != 0 && got != tc.status {
				t.Fatalf("revert = %v (status %d), want status %d", err, got, tc.status)
			}
			data, err := os.ReadFile(filepath.Join(dir, tc.file))
			switch {
			case tc.want == "" && !errors.Is(err, fs.ErrNotExist):
				t.Fatalf("%s still exists: %q, %v", tc.file, data, err)
			case tc.want != "" && string(data) != tc.want:
				t.Fatalf("%s = %q, %v; want %q", tc.file, data, err, tc.want)
			}
		})
	}
}

func TestRevertChangeRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, _ := createSession(t, ts.m, ts.prov)
	w := ts.do(http.MethodPost, "/api/sessions/"+sum.ID+"/changes/revert", `{"path":"x.txt"}`, withCookie(ts))
	if w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed || w.Code == http.StatusOK {
		t.Fatalf("revert route = %d %s", w.Code, w.Body)
	}
}
