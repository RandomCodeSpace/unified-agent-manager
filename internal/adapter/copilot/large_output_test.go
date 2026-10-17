package copilot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// Every Task session, created or resumed, saves tool output over
// toolOutputCap to a file instead of the context.
func TestWebLargeOutputCapOnCreateAndResume(t *testing.T) {
	h := openWeb(t)
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-2", ConversationID: "s-1", Workdir: "/work", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]*copilot.LargeToolOutputConfig{"create": h.fc.create[0].LargeOutput, "resume": h.fc.resume[0].LargeOutput} {
		if got == nil || got.MaxSizeBytes == nil || *got.MaxSizeBytes != 8192 || got.Enabled != nil {
			t.Errorf("%s LargeOutput = %+v", name, got)
		}
	}
}

// The CLI deletes saved tool output when its session closes, so the copy in
// the session's folder is what the model is pointed to.
func TestSavedToolOutputIsKeptWithTheSession(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	workspace := t.TempDir()
	spill := filepath.Join(tmp, "1792246326725-copilot-tool-output-719190-e87b.txt")
	if err := os.WriteFile(spill, []byte("1\n2\n3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &conversation{id: "s-1", sess: &fakeSession{id: "s-1", workspace: workspace}}
	text := "Output too large. Saved to " + spill + ". Preview: 1"
	result := map[string]any{
		"contents":         []any{map[string]any{"outputFilePath": spill, "exitCode": float64(0)}},
		"textResultForLlm": text,
		"resultType":       "success",
	}
	out, err := c.succeededToolUse(copilot.PostToolUseHookInput{ToolName: "bash", ToolResult: result}, copilot.HookInvocation{})
	if err != nil || out == nil {
		t.Fatalf("succeededToolUse = %+v, %v", out, err)
	}
	kept := filepath.Join(workspace, "tool-output", filepath.Base(spill))
	got := out.ModifiedResult.(map[string]any)
	if want := strings.ReplaceAll(text, spill, kept); got["textResultForLlm"] != want {
		t.Errorf("textResultForLlm = %q, want %q", got["textResultForLlm"], want)
	}
	if p := got["contents"].([]any)[0].(map[string]any)["outputFilePath"]; p != kept {
		t.Errorf("outputFilePath = %v, want %s", p, kept)
	}
	if b, err := os.ReadFile(kept); err != nil || string(b) != "1\n2\n3\n" {
		t.Errorf("kept copy = %q, %v", b, err)
	}

	// A path outside the temp directory, or a name the CLI does not write,
	// is left alone.
	for _, p := range []string{filepath.Join(workspace, "x-copilot-tool-output-y.txt"), filepath.Join(tmp, "notes.txt")} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		in := map[string]any{"contents": []any{map[string]any{"outputFilePath": p}}}
		if out, err := c.succeededToolUse(copilot.PostToolUseHookInput{ToolName: "bash", ToolResult: in}, copilot.HookInvocation{}); out != nil || err != nil {
			t.Errorf("%s: succeededToolUse = %+v, %v; want nil", p, out, err)
		}
	}
}
