package copilot

import (
	"context"
	"errors"
	"fmt"
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

func (c *conversation) nativeDiff(ctx context.Context) ([]agentapi.FileDiff, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, agentapi.ErrClosed
	}
	sess, ok := c.sess.(workspaceDiffSession)
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
				return nil, fmt.Errorf("%w: native changes are available when the Task settles", agentapi.ErrBusy)
			case rpc.HistoryRewindUnavailableReasonFileChangeTrackingDisabled:
				return nil, fmt.Errorf("%w: this conversation did not capture file changes from its first turn; use All changes", agentapi.ErrUnsupported)
			case rpc.HistoryRewindUnavailableReasonUnsupportedRemoteSession:
				return nil, fmt.Errorf("%w: native changes must be read on this conversation's host", agentapi.ErrUnsupported)
			}
		}
		return nil, fmt.Errorf("%w: Copilot could not return this conversation's native changes; use All changes", agentapi.ErrUnsupported)
	}
	files := make([]agentapi.FileDiff, 0, len(result.Changes))
	for _, change := range result.Changes {
		binary := strings.HasPrefix(change.Diff, "Binary files ") || strings.Contains(change.Diff, "\nBinary files ") || strings.Contains(change.Diff, "\nGIT binary patch\n")
		truncated := change.IsTruncated != nil && *change.IsTruncated
		recognized := strings.HasPrefix(change.Diff, "diff --git ") || strings.HasPrefix(change.Diff, "--- ")
		oldPath := ""
		if change.OldPath != nil {
			oldPath = *change.OldPath
		}
		files = append(files, agentapi.FileDiff{
			Path: change.Path, Status: string(change.ChangeType), OldPath: oldPath, Patch: change.Diff,
			Binary: binary, Truncated: truncated,
			CountsUnknown: binary || truncated || !recognized && !(change.Diff == "" && change.ChangeType == rpc.WorkspaceDiffFileChangeTypeRenamed),
		})
	}
	return files, nil
}
