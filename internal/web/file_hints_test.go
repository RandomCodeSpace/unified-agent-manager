package web

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestCompactLocalFileHints(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: added.txt\n+new\n*** Update File: old.txt\n*** Move to: moved.txt\n@@\n-old\n+new\n*** Delete File: deleted.txt\n*** End Patch\n"
	encoded, _ := json.Marshal(patch)
	for _, tc := range []struct {
		name, input string
		want        []string
	}{
		{"view", `{"path":"package.json"}`, []string{"package.json"}},
		{"create", `{"file_path":".gitignore"}`, []string{".gitignore"}},
		{"edit", `{"filePath":"/project/文%#.txt"}`, []string{"/project/文%#.txt"}},
		{"view", `{"path":"x","file_path":"y"}`, nil},
		{"view", `{"path":"x"`, nil},
		{"view", `{"path":null}`, nil},
		{"view", `{"path":""}`, nil},
		{"view", `{"path":"bad\u0000path"}`, nil},
		{"view", `{"path":"` + strings.Repeat("x", maxResolvePathBytes+1) + `"}`, nil},
		{"view", `{"path":"` + string([]byte{0xff}) + `"}`, nil},
		{"read", `{"path":"package.json"}`, nil},
		{"write", `{"path":"package.json"}`, nil},
		{"View", `{"path":"package.json"}`, nil},
		{"github-mcp-server-get_file_contents", `{"path":"package.json"}`, nil},
		{"glob", `{"path":"package.json"}`, nil},
		{"apply_patch", patch, []string{"added.txt", "old.txt", "moved.txt", "deleted.txt"}},
		{"apply_patch", string(encoded), []string{"added.txt", "old.txt", "moved.txt", "deleted.txt"}},
		{"apply_patch", strings.TrimSuffix(patch, "*** End Patch\n"), nil},
		{"apply_patch", strings.Replace(patch, "*** Move to:", "*** Rename to:", 1), nil},
		{"apply_patch", "*** Begin Patch\n*** Update File: x\n*** End Patch", nil},
		{"apply_patch", "*** Begin Patch\n*** Add File: x\n*** Move to: y\n*** End Patch", nil},
		{"apply_patch", "*** Begin Patch\n*** Delete File: x\n+bad\n*** End Patch", nil},
		{"apply_patch", "*** Begin Patch\n*** Add File: x\n*** Add File: x\n*** End Patch", []string{"x"}},
		{"apply_patch", "*** Begin Patch\n*** Add File: x\n+" + strings.Repeat("x", maxFileHintInputBytes) + "\n*** End Patch", nil},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.name, len(tc.input)), func(t *testing.T) {
			tool := &agentapi.ToolCall{Name: tc.name, Input: tc.input, Status: agentapi.ToolCompleted}
			got := projectItem(agentapi.Item{Kind: agentapi.ItemTool, Tool: tool})
			if !reflect.DeepEqual(got.Tool.FilePaths, tc.want) {
				t.Fatalf("file hints = %q, want %q", got.Tool.FilePaths, tc.want)
			}
			if got.Tool.Input != "" || tool.Input != tc.input {
				t.Fatal("projection exposed input or mutated the retained tool")
			}
		})
	}
}

func TestCompactPatchHintCaps(t *testing.T) {
	for _, tc := range []struct {
		count, pathBytes int
		ok               bool
	}{
		{64, 5, true}, {65, 5, false}, {4, 4096, true}, {5, 4096, false}, {1, 4097, false},
	} {
		var patch strings.Builder
		patch.WriteString("*** Begin Patch\n")
		for i := range tc.count {
			path := fmt.Sprintf("%04d", i) + strings.Repeat("x", tc.pathBytes-4)
			fmt.Fprintf(&patch, "*** Delete File: %s\n", path)
		}
		patch.WriteString("*** End Patch")
		got := localToolFilePaths(&agentapi.ToolCall{Name: "apply_patch", Input: patch.String()})
		if (got != nil) != tc.ok || (tc.ok && len(got) != tc.count) {
			t.Fatalf("count=%d pathBytes=%d: got %d hints", tc.count, tc.pathBytes, len(got))
		}
	}
}
