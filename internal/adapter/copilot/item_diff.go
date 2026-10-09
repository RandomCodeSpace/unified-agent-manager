package copilot

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/sourcegraph/go-diff/diff"
)

func nativeFileEdits(eventID string, d *rpc.ToolExecutionCompleteData) (string, []agentapi.FileEdit, bool) {
	if len(d.FileEdits) == 0 || eventID == "" || len(eventID) > 256 {
		return "", nil, false
	}
	files, status := nativePatchFiles(d)
	var edits []agentapi.FileEdit
	used, truncated := 0, false
	for _, edit := range d.FileEdits {
		if len(edits) >= agentapi.MaxFileEdits || used+len(edit.Path) > agentapi.MaxFileEditPathsBytes {
			truncated = true
			continue
		}
		if !validEditPath(edit.Path) {
			truncated = true
			continue
		}
		result, additions, deletions := nativeEditPatch(files, status, edit.Path, false)
		for i := range edits {
			if edits[i].Path == edit.Path {
				edits[i].DiffStatus, edits[i].Additions, edits[i].Deletions = "unsupported", 0, 0
				result.Status, additions, deletions = "unsupported", 0, 0
			}
		}
		edits = append(edits, agentapi.FileEdit{Path: edit.Path, Kind: string(edit.Kind), Additions: additions, Deletions: deletions, DiffStatus: result.Status})
		used += len(edit.Path)
	}
	return eventID, edits, truncated
}

// Native patch text belongs only to the readonly diff route, including a
// provider that duplicates its DetailedContent into the model-facing slot.
func nativeConciseResult(d *rpc.ToolExecutionCompleteData, edits []agentapi.FileEdit) string {
	if len(edits) > 0 && d.Result.DetailedContent != nil && d.Result.Content == *d.Result.DetailedContent {
		return ""
	}
	return d.Result.Content
}

func validEditPath(path string) bool {
	return filepath.IsAbs(path) && len(path) <= agentapi.MaxFileEditPathBytes && utf8.ValidString(path) && !strings.ContainsFunc(path, unicode.IsControl)
}

// Native FileEdits supplies absolute provider identities. Observed native Git
// headers encode that identity as a/ or b/ plus the path without its leading
// slash. Compare it directly; never resolve a diff header or open that file.
func nativePatchFiles(d *rpc.ToolExecutionCompleteData) ([]*diff.FileDiff, string) {
	if d.Result == nil || d.Result.DetailedContent == nil || *d.Result.DetailedContent == "" {
		return nil, "missing"
	}
	text := *d.Result.DetailedContent
	if len(text) > agentapi.MaxEditPatchBytes {
		return nil, "too_large"
	}
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return nil, "unsupported"
	}
	files, err := diff.ParseMultiFileDiffOptions([]byte(strings.TrimLeft(text, "\r\n")), diff.ParseOptions{KeepCR: true})
	if err != nil || len(files) == 0 {
		return nil, "unsupported"
	}
	return files, "available"
}

func nativeEditPatch(files []*diff.FileDiff, status, path string, includePatch bool) (agentapi.ItemDiff, int, int) {
	result := agentapi.ItemDiff{Path: path, Status: status}
	if status != "available" {
		return result, 0, 0
	}
	var match *diff.FileDiff
	for _, file := range files {
		old, fresh := strings.TrimPrefix(file.OrigName, "a/"), strings.TrimPrefix(file.NewName, "b/")
		identity := strings.TrimPrefix(path, "/")
		sameFile := old == identity && fresh == identity || old == "/dev/null" && fresh == identity || fresh == "/dev/null" && old == identity
		if !sameFile {
			continue
		}
		if match != nil {
			result.Status = "unsupported"
			return result, 0, 0
		}
		match = file
	}
	if match == nil {
		result.Status = "unsupported"
		return result, 0, 0
	}
	if len(match.Hunks) == 0 {
		result.Status = "unsupported"
		for _, line := range match.Extended {
			if line == "GIT binary patch" || strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ") {
				result.Status = "binary"
			}
		}
		return result, 0, 0
	}
	additions, deletions := 0, 0
	for _, h := range match.Hunks {
		old, new := int32(0), int32(0)
		for _, line := range bytes.Split(bytes.TrimSuffix(h.Body, []byte{'\n'}), []byte{'\n'}) {
			if len(line) == 0 {
				result.Status = "unsupported"
				return result, 0, 0
			}
			switch line[0] {
			case '+':
				additions++
				new++
			case '-':
				deletions++
				old++
			case ' ':
				old++
				new++
			default:
				result.Status = "unsupported"
				return result, 0, 0
			}
		}
		// Some parsers accept an incomplete last hunk. Never claim its counts.
		if old != h.OrigLines || new != h.NewLines {
			result.Status = "unsupported"
			return result, 0, 0
		}
	}
	if !includePatch {
		return result, additions, deletions
	}
	patch, err := diff.PrintFileDiff(match)
	if err != nil || len(patch) > agentapi.MaxEditPatchBytes {
		result.Status = "too_large"
		return result, 0, 0
	}
	result.Status, result.Patch = "available", string(patch)
	return result, additions, deletions
}

func (p *webProvider) ReadItemDiff(ctx context.Context, req agentapi.ItemDiffRequest) (agentapi.ItemDiff, error) {
	if req.ConversationID == "" || req.ItemID == "" || req.EventID == "" || !validEditPath(req.Path) {
		return agentapi.ItemDiff{}, agentapi.ErrItemNotFound
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return agentapi.ItemDiff{}, err
	}
	for attempt := 1; ; attempt++ {
		result, found, ambiguous := agentapi.ItemDiff{}, false, false
		err = p.readForward(ctx, client, req.ConversationID, func(ev copilot.SessionEvent) {
			if ev.ID != req.EventID {
				return
			}
			d, ok := ev.Data.(*rpc.ToolExecutionCompleteData)
			if !ok || agentOf(ev) != req.AgentID || d.ToolCallID != req.ItemID {
				return
			}
			for _, edit := range d.FileEdits {
				if edit.Path != req.Path {
					continue
				}
				if found {
					ambiguous = true
					return
				}
				found = true
				files, status := nativePatchFiles(d)
				result, _, _ = nativeEditPatch(files, status, req.Path, true)
			}
		})
		if errors.Is(err, errJournalChanged) && attempt < webWindowAttempts {
			continue
		}
		if err != nil {
			return agentapi.ItemDiff{}, err
		}
		if !found || ambiguous {
			return agentapi.ItemDiff{}, agentapi.ErrItemNotFound
		}
		return result, nil
	}
}
