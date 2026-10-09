package copilot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

type rewindSession interface {
	turnChangesSession
	Rewind(context.Context, *rpc.HistoryRewindRequest) (*rpc.HistoryRewindResult, error)
}

func (a sdkSessionAdapter) Rewind(ctx context.Context, req *rpc.HistoryRewindRequest) (*rpc.HistoryRewindResult, error) {
	return a.s.RPC.History.Rewind(ctx, req)
}

func (c *conversation) rewindable() (rewindSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sess, ok := c.sess.(rewindSession)
	switch {
	case c.closed:
		return nil, agentapi.ErrClosed
	case !ok:
		return nil, agentapi.ErrUnsupported
	case c.turnRunning:
		return nil, agentapi.ErrBusy
	}
	return sess, nil
}

// PreviewRewind reads the exact persisted owner boundary, its native rewind
// point and file preview. It sends nothing and never resumes a conversation.
func (c *conversation) PreviewRewind(ctx context.Context, user string) (agentapi.RewindPreview, error) {
	sess, err := c.rewindable()
	if err != nil {
		return agentapi.RewindPreview{}, err
	}
	boundary, suffix, turns, err := c.p.readBoundary(ctx, c.client, agentapi.ForkBoundaryRequest{ConversationID: c.id, UserItemID: user})
	if err != nil {
		return agentapi.RewindPreview{}, err
	}
	points, err := sess.ListRewindPoints(ctx)
	if err != nil {
		return agentapi.RewindPreview{}, fmt.Errorf("copilot rewind points: %s", errText(err))
	}
	if points == nil {
		return agentapi.RewindPreview{}, errors.New("copilot returned no rewind points")
	}
	if points.UnavailableReason != nil {
		if *points.UnavailableReason == rpc.HistoryRewindUnavailableReasonSessionBusy {
			return agentapi.RewindPreview{}, agentapi.ErrBusy
		}
		return agentapi.RewindPreview{}, agentapi.ErrUnsupported
	}
	found := 0
	for _, p := range points.Points {
		if p.EventID == boundary.UserEventID {
			if p.IsAutopilotContinuation {
				return agentapi.RewindPreview{}, agentapi.ErrItemNotFound
			}
			found++
		}
	}
	if found != 1 {
		return agentapi.RewindPreview{}, agentapi.ErrItemNotFound
	}
	out := agentapi.RewindPreview{UserEventID: boundary.UserEventID, TailEventID: boundary.TailEventID, Turns: turns, Discarded: suffix, FilesReason: string(rpc.HistoryRewindUnavailableReasonFileChangeTrackingDisabled)}
	if points.FileChangeTrackingEnabled {
		preview, err := sess.PreviewRewind(ctx, boundary.UserEventID)
		switch {
		case err != nil:
			return agentapi.RewindPreview{}, fmt.Errorf("copilot rewind preview: %s", errText(err))
		case preview == nil:
			return agentapi.RewindPreview{}, errors.New("copilot returned no rewind preview")
		case preview.Available:
			files := nativeTurnPreview(boundary.UserEventID, preview)
			if files.Status != "available" {
				return agentapi.RewindPreview{}, errors.New("copilot returned an unusable rewind preview")
			}
			out.FilesAvailable, out.FilesReason, out.Files = true, "", files
		case preview.Reason != nil && *preview.Reason == rpc.HistoryRewindUnavailableReasonSessionBusy:
			return agentapi.RewindPreview{}, agentapi.ErrBusy
		case preview.Reason != nil:
			out.FilesReason = string(*preview.Reason)
		default:
			out.FilesReason = "unknown"
		}
	}
	if _, err := c.rewindable(); err != nil {
		return agentapi.RewindPreview{}, err
	}
	return out, nil
}

// Rewind sends exactly one native request. Only a refusal before it was sent
// or a missing method is definite; everything else may have restored files
// or truncated history, so it is reported as uncertain and never repeated.
func (c *conversation) Rewind(ctx context.Context, event, mode string) (agentapi.RewindResult, error) {
	if event == "" || len(event) > 256 || mode != agentapi.RewindConversation && mode != agentapi.RewindConversationAndFiles {
		return agentapi.RewindResult{}, errors.New("invalid rewind boundary or mode")
	}
	sess, err := c.rewindable()
	if err != nil {
		return agentapi.RewindResult{}, err
	}
	res, err := sess.Rewind(ctx, &rpc.HistoryRewindRequest{EventID: event, Mode: rpc.HistoryRewindMode(mode)})
	var rpcErr *copilot.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
		c.p.mu.Lock()
		c.p.rewindUnsupported = true
		c.p.mu.Unlock()
		return agentapi.RewindResult{}, agentapi.ErrUnsupported
	}
	if err != nil {
		return agentapi.RewindResult{}, fmt.Errorf("%w: %s", agentapi.ErrRewindUncertain, errText(err))
	}
	if res == nil {
		return agentapi.RewindResult{}, agentapi.ErrRewindUncertain
	}
	return nativeRewindResult(res)
}

// nativeRewindResult keeps the nine native outcomes and the presence of each
// optional field. An outcome this build does not know is not interpreted.
func nativeRewindResult(res *rpc.HistoryRewindResult) (agentapi.RewindResult, error) {
	switch res.Outcome {
	case rpc.HistoryRewindOutcomeSuccess, rpc.HistoryRewindOutcomeCheckpointCleanupFailed, rpc.HistoryRewindOutcomeSnapshotPruneFailed,
		rpc.HistoryRewindOutcomeTruncationFailed, rpc.HistoryRewindOutcomeFilesRolledBack, rpc.HistoryRewindOutcomeRollbackIncomplete,
		rpc.HistoryRewindOutcomeSessionBusy, rpc.HistoryRewindOutcomeFileChangeTrackingDisabled, rpc.HistoryRewindOutcomeUnsupportedRemoteSession:
	default:
		return agentapi.RewindResult{}, fmt.Errorf("%w: unknown native outcome", agentapi.ErrRewindUncertain)
	}
	out := agentapi.RewindResult{Outcome: string(res.Outcome), RestoredFiles: []string{}, SkippedFiles: []agentapi.RewindSkip{}}
	if res.Error != nil {
		text := clip(displaytext.Sanitize(*res.Error), 512)
		out.Error = &text
	}
	if res.EventsRemoved != nil {
		n := *res.EventsRemoved
		out.EventsRemoved = &n
	}
	for _, path := range res.RestoredFiles {
		if len(out.RestoredFiles) >= agentapi.MaxRewindResultFiles || !resultPath(path) {
			out.RestoredOmitted++
			continue
		}
		out.RestoredFiles = append(out.RestoredFiles, strings.Clone(path))
	}
	for _, f := range res.SkippedFiles {
		if len(out.SkippedFiles) >= agentapi.MaxRewindResultFiles || !resultPath(f.Path) || len(f.Reason) > 32 {
			out.SkippedOmitted++
			continue
		}
		out.SkippedFiles = append(out.SkippedFiles, agentapi.RewindSkip{Path: strings.Clone(f.Path), Reason: strings.Clone(string(f.Reason))})
	}
	return out, nil
}

func resultPath(path string) bool {
	return filepath.IsAbs(path) && len(path) <= 4096 && !strings.ContainsRune(path, 0)
}
