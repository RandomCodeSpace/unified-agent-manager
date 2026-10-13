package copilot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type workspaceDiffSession interface {
	WorkspaceDiff(context.Context) (*rpc.WorkspaceDiffResult, error)
}

func (a sdkSessionAdapter) WorkspaceDiff(ctx context.Context) (*rpc.WorkspaceDiffResult, error) {
	return a.s.RPC.Workspaces.Diff(ctx, &rpc.WorkspacesDiffRequest{Mode: rpc.WorkspaceDiffModeSession})
}

type workspaceIdentitySession interface {
	WorkspaceIdentity(context.Context) (*rpc.WorkspacesGetWorkspaceResult, error)
}

func (a sdkSessionAdapter) WorkspaceIdentity(ctx context.Context) (*rpc.WorkspacesGetWorkspaceResult, error) {
	return a.s.RPC.Workspaces.GetWorkspace(ctx)
}

// nativeDiffRoot is the directory a session diff's relative paths resolve
// against: the workspace's working directory. A missing or relative Cwd, or
// a Git root that differs from it, leaves the root unknown. Path is the
// session's own state directory (where the scratch plan lives), never a root.
func nativeDiffRoot(ws *rpc.WorkspacesGetWorkspaceResult) string {
	if ws == nil || ws.Workspace == nil || ws.Workspace.Cwd == nil || !filepath.IsAbs(*ws.Workspace.Cwd) {
		return ""
	}
	cwd := filepath.Clean(*ws.Workspace.Cwd)
	if root := ws.Workspace.GitRoot; root != nil && *root != "" && filepath.Clean(*root) != cwd {
		return ""
	}
	return cwd
}

// withoutScratchPlan drops the change whose path is exactly the native plan
// file. An absolute path compares directly; a relative one needs the one
// workspace lookup, and an unknown root keeps every relative change.
func withoutScratchPlan(ctx context.Context, sess any, planPath string, changes []rpc.WorkspaceDiffFileChange) []rpc.WorkspaceDiffFileChange {
	if planPath == "" {
		return changes
	}
	root, looked := "", false
	kept := changes[:0:0]
	for _, change := range changes {
		path := change.Path
		if !filepath.IsAbs(path) {
			if !looked {
				looked = true
				if lookup, ok := sess.(workspaceIdentitySession); ok {
					if ws, err := lookup.WorkspaceIdentity(ctx); err == nil {
						root = nativeDiffRoot(ws)
					}
				}
			}
			if root == "" {
				kept = append(kept, change)
				continue
			}
			path = filepath.Join(root, path)
		}
		if filepath.Clean(path) != planPath {
			kept = append(kept, change)
		}
	}
	return kept
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@`)

// withTrailingContext puts back the blank context lines the CLI trims from
// the end of a patch, with its final newline: a last hunk whose header
// promises more lines than its body holds, the same number short on both
// sides, ended in blank context. A patch parser refuses the short hunk.
func withTrailingContext(patch string) string {
	start := strings.LastIndex(patch, "\n@@ ") + 1
	if start == 0 && !strings.HasPrefix(patch, "@@ ") {
		return patch
	}
	header, body, _ := strings.Cut(patch[start:], "\n")
	m := hunkHeader.FindStringSubmatch(header)
	if m == nil {
		return patch
	}
	oldWant, newWant := hunkCount(m[1]), hunkCount(m[2])
	oldHave, newHave := 0, 0
	if body != "" {
		for line := range strings.SplitSeq(strings.TrimSuffix(body, "\n"), "\n") {
			switch {
			case strings.HasPrefix(line, "\\"): // "\ No newline at end of file"
			case strings.HasPrefix(line, "+"):
				newHave++
			case strings.HasPrefix(line, "-"):
				oldHave++
			default:
				oldHave++
				newHave++
			}
		}
	}
	missing := oldWant - oldHave
	if missing <= 0 || missing != newWant-newHave {
		return patch
	}
	if !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	return patch + strings.Repeat(" \n", missing)
}

// hunkCount is a hunk header's line count; an absent one means 1.
func hunkCount(s string) int {
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

func (c *conversation) nativeDiff(ctx context.Context) ([]agentapi.FileDiff, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, agentapi.ErrClosed
	}
	sess, ok := c.sess.(workspaceDiffSession)
	planPath := c.planPath
	c.mu.Unlock()
	if !ok {
		return nil, agentapi.ErrUnsupported
	}
	result, err := sess.WorkspaceDiff(ctx)
	if err != nil {
		var rpcErr *copilot.RPCError
		if errors.As(err, &rpcErr) && (rpcErr.Code == -32601 || rpcErr.Message == "Unhandled method session.workspaces.diff") {
			return nil, fmt.Errorf("%w: this Copilot runtime does not support native session changes", agentapi.ErrUnsupported)
		}
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("copilot: native changes returned no result")
	}
	// A fallback is the owner's working tree, not this conversation's edits.
	if result.IsFallback || result.Mode != rpc.WorkspaceDiffModeSession || result.RequestedMode != rpc.WorkspaceDiffModeSession || result.UnavailableReason != nil {
		if result.UnavailableReason != nil {
			switch *result.UnavailableReason {
			case rpc.HistoryRewindUnavailableReasonSessionBusy:
				return nil, fmt.Errorf("%w: native changes are available when the Thread settles", agentapi.ErrBusy)
			case rpc.HistoryRewindUnavailableReasonFileChangeTrackingDisabled:
				return nil, fmt.Errorf("%w: this conversation did not capture file changes from its first turn; use All changes", agentapi.ErrUnsupported)
			case rpc.HistoryRewindUnavailableReasonUnsupportedRemoteSession:
				return nil, fmt.Errorf("%w: native changes must be read on this conversation's host", agentapi.ErrUnsupported)
			}
		}
		return nil, fmt.Errorf("%w: Copilot could not return this conversation's native changes; use All changes", agentapi.ErrUnsupported)
	}
	changes := withoutScratchPlan(ctx, sess, planPath, result.Changes)
	files := make([]agentapi.FileDiff, 0, len(changes))
	for _, change := range changes {
		binary := strings.HasPrefix(change.Diff, "Binary files ") || strings.Contains(change.Diff, "\nBinary files ") || strings.Contains(change.Diff, "\nGIT binary patch\n")
		truncated := change.IsTruncated != nil && *change.IsTruncated
		recognized := strings.HasPrefix(change.Diff, "diff --git ") || strings.HasPrefix(change.Diff, "--- ")
		patch := change.Diff
		if recognized && !binary && !truncated {
			patch = withTrailingContext(patch)
		}
		oldPath := ""
		if change.OldPath != nil {
			oldPath = *change.OldPath
		}
		files = append(files, agentapi.FileDiff{
			Path: change.Path, Status: string(change.ChangeType), OldPath: oldPath, Patch: patch,
			Binary: binary, Truncated: truncated,
			CountsUnknown: binary || truncated || !recognized && (change.Diff != "" || change.ChangeType != rpc.WorkspaceDiffFileChangeTypeRenamed),
		})
	}
	return files, nil
}
