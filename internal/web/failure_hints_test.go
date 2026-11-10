package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func hintCount(m *Manager, name string) int {
	m.counts.mu.Lock()
	defer m.counts.mu.Unlock()
	return m.counts.n[name]
}

func TestFailureHintView(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"lib/gen.py", "a/dup.go", "b/dup.go"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte("FILE-CONTENT-MARKER"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const missing = "Path does not exist"
	for _, tc := range []struct {
		name, path, failure, want string
	}{
		{"one match by name", filepath.Join(dir, "gen.py"), missing, "uam: did you mean " + filepath.Join(dir, "lib/gen.py") + "?"},
		{"one match by wrong folder", filepath.Join(dir, "src/gen.py"), missing, "uam: did you mean " + filepath.Join(dir, "lib/gen.py") + "?"},
		{"one match relative", "src/gen.py", missing, "uam: did you mean " + filepath.Join(dir, "lib/gen.py") + "?"},
		{"no match", filepath.Join(dir, "nothing.py"), missing, ""},
		{"several matches", filepath.Join(dir, "dup.go"), missing, ""},
		{"outside the folder", "/elsewhere/gen.py", missing, ""},
		{"path exists", filepath.Join(dir, "lib/gen.py"), missing, ""},
		{"unrecognised failure", filepath.Join(dir, "gen.py"), "Invalid view_range", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &webSession{id: "s", workdir: dir}
			m := &Manager{now: time.Now, sessions: map[string]*webSession{"s": s}}
			got := m.failedToolUse(t.Context(), s, agentapi.ToolUse{Tool: "view", Args: map[string]any{"path": tc.path}}, tc.failure)
			if got != tc.want || strings.Contains(got, "FILE-CONTENT-MARKER") {
				t.Fatalf("hint=%q, want %q", got, tc.want)
			}
			if n, want := hintCount(m, "hints_view_path"), min(len(tc.want), 1); n != want {
				t.Fatalf("count=%d, want %d", n, want)
			}
		})
	}
}

func TestFailureHintPatch(t *testing.T) {
	now := time.Now()
	failure := "Failed to apply patch: Error: Failed to find expected lines in /repo/doc.md:\n| Node.js | 1.2 |"
	use := agentapi.ToolUse{Tool: "apply_patch", Workdir: "/repo", Args: map[string]any{"patch": "*** Begin Patch\n*** Update File: /repo/doc.md\n@@\n-FILE-CONTENT-MARKER\n+b\n*** End Patch"}}
	for _, tc := range []struct {
		name    string
		sibling time.Time
		own     time.Time
		failure string
		want    string
	}{
		{"changed this turn", now.Add(-time.Minute), time.Time{}, failure, "uam: another Thread edited /repo/doc.md this turn; view it again."},
		{"changed before this turn", now.Add(-time.Hour), time.Time{}, failure, ""},
		{"own edit after the sibling's", now.Add(-time.Minute), now.Add(-time.Second), failure, ""},
		{"unrecognised failure", now.Add(-time.Minute), time.Time{}, "Failed to apply patch: Error: File already exists: /repo/doc.md", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &webSession{id: "caller", projectID: "p", workdir: "/repo", turnStart: now.Add(-10 * time.Minute), edits: map[string]time.Time{}}
			if !tc.own.IsZero() {
				caller.edits["doc.md"] = tc.own
			}
			sibling := &webSession{id: "sibling", projectID: "p", workdir: "/repo", stage: StageActive, base: StateIdle, edits: map[string]time.Time{"doc.md": tc.sibling}}
			m := &Manager{now: func() time.Time { return now }, sessions: map[string]*webSession{"caller": caller, "sibling": sibling}}
			got := m.failedToolUse(t.Context(), caller, use, tc.failure)
			if got != tc.want || strings.Contains(got, "FILE-CONTENT-MARKER") || strings.Contains(got, "Node.js") {
				t.Fatalf("hint=%q, want %q", got, tc.want)
			}
			if n, want := hintCount(m, "hints_patch_changed"), min(len(tc.want), 1); n != want {
				t.Fatalf("count=%d, want %d", n, want)
			}
		})
	}
}
