package copilot

import (
	"context"
	"errors"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"strings"
	"testing"
	"time"
)

const recordedEditPatch = "\ndiff --git a/work/file.txt b/work/file.txt\n--- a/work/file.txt\n+++ b/work/file.txt\n@@ -1 +1 @@\n-before\n+first\n"

func editComplete(patch string, success bool) *rpc.ToolExecutionCompleteData {
	return &rpc.ToolExecutionCompleteData{ToolCallID: "tool", Success: success, FileEdits: []rpc.ToolExecutionCompleteFileEdit{{Path: "/work/file.txt", Kind: rpc.ToolExecutionCompleteFileEditKind("edit")}}, Result: &rpc.ToolExecutionCompleteResult{Content: "edited", DetailedContent: &patch}}
}
func TestNativeFileEditsAreCompactCommittedMetadata(t *testing.T) {
	for _, success := range []bool{true, false} {
		d := editComplete(recordedEditPatch, success)
		it, ok := newTranscript().item(ev("native-event", d))
		if !ok || it.Tool.EditEventID != "native-event" || len(it.Tool.FileEdits) != 1 {
			t.Fatalf("item=%+v", it)
		}
		edit := it.Tool.FileEdits[0]
		if edit.Path != "/work/file.txt" || edit.DiffStatus != "available" || edit.Additions != 1 || edit.Deletions != 1 {
			t.Fatalf("edit=%+v", edit)
		}
		if strings.Contains(it.Tool.Output, "+first") {
			t.Fatal("patch leaked into transcript output")
		}
		clone := cloneTool(it.Tool)
		clone.FileEdits[0].Path = "changed"
		if it.Tool.FileEdits[0].Path != "/work/file.txt" {
			t.Fatal("mutable edit metadata shared")
		}
	}
}
func TestNativeFileEditUnknownDetailsNeverInventCounts(t *testing.T) {
	cases := map[string]string{
		"missing": "", "unsupported": "not a diff", "truncated": "--- a/work/file.txt\n+++ b/work/file.txt\n@@ -1,2 +1,2 @@\n-a\n+b\n",
		"binary":    "diff --git a/work/file.txt b/work/file.txt\nindex 1234567..7654321 100644\nBinary files a/work/file.txt and b/work/file.txt differ\n",
		"mismatch":  strings.ReplaceAll(recordedEditPatch, "work/file.txt", "other/file.txt"),
		"too_large": strings.Repeat("x", agentapi.MaxEditPatchBytes+1),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			_, edits, _ := nativeFileEdits("event", editComplete(text, true))
			if len(edits) != 1 || edits[0].DiffStatus == "available" || edits[0].Additions != 0 || edits[0].Deletions != 0 {
				t.Fatalf("edits=%+v", edits)
			}
		})
	}
	d := editComplete(recordedEditPatch, true)
	d.FileEdits = make([]rpc.ToolExecutionCompleteFileEdit, agentapi.MaxFileEdits+2)
	for i := range d.FileEdits {
		d.FileEdits[i] = rpc.ToolExecutionCompleteFileEdit{Path: "/work/file.txt", Kind: rpc.ToolExecutionCompleteFileEditKind("edit")}
	}
	_, edits, cut := nativeFileEdits("event", d)
	if edits[0].DiffStatus != "unsupported" || edits[0].Additions != 0 {
		t.Fatal("ambiguous duplicate paths claimed exact per-edit counts")
	}
	if len(edits) != agentapi.MaxFileEdits || !cut {
		t.Fatalf("bounded edits=%d cut=%v", len(edits), cut)
	}
}
func TestReadItemDiffBindsExactRecordedIdentityAndNeverResumes(t *testing.T) {
	original := agentEv("original", "agent", editComplete(recordedEditPatch, false))
	later := agentEv("later", "agent", editComplete(strings.ReplaceAll(recordedEditPatch, "first", "second"), true))
	fc := &fakeClient{journal: []copilot.SessionEvent{original, later}, pageSize: 1}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	req := agentapi.ItemDiffRequest{ReadRequest: agentapi.ReadRequest{ConversationID: "recorded", Workdir: "/work"}, AgentID: "agent", ItemID: "tool", EventID: "original", Path: "/work/file.txt"}
	result, err := p.ReadItemDiff(context.Background(), req)
	if err != nil || result.Status != "available" || !strings.Contains(result.Patch, "+first") || strings.Contains(result.Patch, "second") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, bad := range []agentapi.ItemDiffRequest{
		func() agentapi.ItemDiffRequest { v := req; v.AgentID = ""; return v }(),
		func() agentapi.ItemDiffRequest { v := req; v.ItemID = "wrong"; return v }(),
		func() agentapi.ItemDiffRequest { v := req; v.EventID = "missing"; return v }(),
		func() agentapi.ItemDiffRequest { v := req; v.Path = "/work/other.txt"; return v }(),
	} {
		if _, err = p.ReadItemDiff(context.Background(), bad); !errors.Is(err, agentapi.ErrItemNotFound) {
			t.Fatalf("bad request=%+v err=%v", bad, err)
		}
	}
	if len(fc.create) != 0 || len(fc.resume) != 0 {
		t.Fatal("readonly patch read opened a conversation")
	}
	// A replaced journal snapshot must restart its exact lookup, not serve an
	// earlier snapshot result merely because its event was found on page one.
	fc.mu.Lock()
	fc.reads = nil
	fc.expireRead = 2
	fc.mu.Unlock()
	result, err = p.ReadItemDiff(context.Background(), req)
	if err != nil || !strings.Contains(result.Patch, "+first") {
		t.Fatalf("replacement retry=%+v %v", result, err)
	}
}

func TestNativeEditPatchCountsCreateDeleteAndNoNewline(t *testing.T) {
	for name, text := range map[string]string{
		"create":     "diff --git a/work/file.txt b/work/file.txt\nnew file mode 100644\n--- /dev/null\n+++ b/work/file.txt\n@@ -0,0 +1 @@\n+created\n",
		"delete":     "diff --git a/work/file.txt b/work/file.txt\ndeleted file mode 100644\n--- a/work/file.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-deleted\n",
		"no-newline": "--- a/work/file.txt\n+++ b/work/file.txt\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, edits, _ := nativeFileEdits("event", editComplete(text, true))
			if len(edits) != 1 || edits[0].DiffStatus != "available" || edits[0].Additions+edits[0].Deletions == 0 {
				t.Fatalf("edits=%+v", edits)
			}
		})
	}
	d := editComplete(recordedEditPatch, true)
	d.Result.Content = recordedEditPatch
	it, _ := newTranscript().item(ev("event", d))
	if it.Tool.Output != "" {
		t.Fatal("duplicated native patch leaked into history output")
	}
	// A normal file's changed contents can literally contain the words Binary
	// files without being classified as an unrelated binary edit.
	text := strings.ReplaceAll(recordedEditPatch, "first", "Binary files a and b differ")
	_, edits, _ := nativeFileEdits("event", editComplete(text, true))
	if edits[0].DiffStatus != "available" {
		t.Fatalf("text marker misclassified=%+v", edits)
	}
}

func TestNativeEditMixedBinaryAndTextAvailability(t *testing.T) {
	binary := "diff --git a/work/image.png b/work/image.png\nindex 1234567..7654321 100644\nBinary files a/work/image.png and b/work/image.png differ\n"
	d := editComplete(binary+strings.TrimLeft(recordedEditPatch, "\n"), true)
	d.FileEdits = append(d.FileEdits, rpc.ToolExecutionCompleteFileEdit{Path: "/work/image.png", Kind: rpc.ToolExecutionCompleteFileEditKind("edit")})
	_, edits, _ := nativeFileEdits("event", d)
	if len(edits) != 2 || edits[0].DiffStatus != "available" || edits[0].Additions != 1 || edits[1].DiffStatus != "binary" || edits[1].Additions != 0 {
		t.Fatalf("mixed edits=%+v", edits)
	}
	// Header names alone never authorize a path missing from FileEdits.
	text := strings.ReplaceAll(recordedEditPatch, "work/file.txt", "work/other.txt")
	fc := &fakeClient{journal: []copilot.SessionEvent{ev("event", editComplete(text, true))}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	req := agentapi.ItemDiffRequest{ReadRequest: agentapi.ReadRequest{ConversationID: "recorded"}, ItemID: "tool", EventID: "event", Path: "/work/other.txt"}
	if _, err := p.ReadItemDiff(context.Background(), req); !errors.Is(err, agentapi.ErrItemNotFound) {
		t.Fatalf("uncommitted arbitrary header=%v", err)
	}
}
