package copilot

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/github/copilot-sdk/go/rpc"
)

type turnChangesSession interface {
	ListRewindPoints(context.Context) (*rpc.HistoryListRewindPointsResult, error)
	PreviewRewind(context.Context, string) (*rpc.HistoryPreviewRewindResult, error)
}

func (a sdkSessionAdapter) ListRewindPoints(ctx context.Context) (*rpc.HistoryListRewindPointsResult, error) {
	return a.s.RPC.History.ListRewindPoints(ctx)
}
func (a sdkSessionAdapter) PreviewRewind(ctx context.Context, event string) (*rpc.HistoryPreviewRewindResult, error) {
	return a.s.RPC.History.PreviewRewind(ctx, &rpc.HistoryPreviewRewindRequest{EventID: event})
}

// TurnChanges is a readonly preview, not a rewind. The SDK preview aggregates
// the discarded suffix. It is this owner turn only if every later point is an
// automatic continuation and no new ordinary prompt has begun. RewindPoint's
// FileCount includes later turns and is deliberately never used here.
func (c *conversation) TurnChanges(ctx context.Context, user string) (agentapi.NativeTurnChanges, error) {
	unknown := agentapi.NativeTurnChanges{Status: "unknown"}
	c.mu.Lock()
	sess, supported := c.sess.(turnChangesSession)
	event, planPath := c.turnChangeEvent, c.planPath
	valid := !c.closed && !c.turnRunning && user != "" && c.turnChangeUser == user && event != ""
	c.mu.Unlock()
	if !valid {
		return unknown, nil
	}
	if !supported {
		return agentapi.NativeTurnChanges{Status: "unsupported"}, nil
	}
	points, err := sess.ListRewindPoints(ctx)
	if err != nil {
		return unknown, err
	}
	if points == nil {
		return unknown, nil
	}
	if points.UnavailableReason != nil {
		return unavailableTurnChanges(*points.UnavailableReason), nil
	}
	if !points.FileChangeTrackingEnabled {
		return agentapi.NativeTurnChanges{Status: "unsupported"}, nil
	}
	if len(points.Points) > 16384 {
		return unknown, nil
	}
	found := false
	for _, p := range points.Points {
		if p.EventID == event {
			if found || p.IsAutopilotContinuation {
				return unknown, nil
			}
			found = true
		} else if found && !p.IsAutopilotContinuation {
			return unknown, nil
		}
	}
	if !found {
		return unknown, nil
	}
	preview, err := sess.PreviewRewind(ctx, event)
	if err != nil {
		return unknown, err
	}
	c.mu.Lock()
	valid = !c.closed && !c.turnRunning && c.turnChangeUser == user && c.turnChangeEvent == event
	c.mu.Unlock()
	if !valid {
		return unknown, nil
	}
	if preview == nil {
		return unknown, nil
	}
	if !preview.Available {
		if preview.Reason != nil {
			return unavailableTurnChanges(*preview.Reason), nil
		}
		return unknown, nil
	}
	return nativeTurnPreview(event, withoutPlanPreview(preview, planPath)), nil
}

// withoutPlanPreview leaves the one change whose path is exactly the native
// scratch plan out of a turn's preview, as withoutScratchPlan does for the
// session diff. A repeated plan path is left for nativeTurnPreview to refuse.
func withoutPlanPreview(p *rpc.HistoryPreviewRewindResult, planPath string) *rpc.HistoryPreviewRewindResult {
	if planPath == "" {
		return p
	}
	plan := func(f rpc.HistoryRewindFilePreview) bool { return filepath.Clean(f.Path) == planPath }
	i := slices.IndexFunc(p.Files, plan)
	if i < 0 || slices.ContainsFunc(p.Files[i+1:], plan) {
		return p
	}
	out := *p
	out.Files = slices.Delete(slices.Clone(p.Files), i, i+1)
	out.FileCount--
	return &out
}

func unavailableTurnChanges(reason rpc.HistoryRewindUnavailableReason) agentapi.NativeTurnChanges {
	status := "unknown"
	switch reason {
	case rpc.HistoryRewindUnavailableReasonSessionBusy:
		status = "busy"
	case rpc.HistoryRewindUnavailableReasonFileChangeTrackingDisabled, rpc.HistoryRewindUnavailableReasonUnsupportedRemoteSession:
		status = "unsupported"
	}
	return agentapi.NativeTurnChanges{Status: status}
}

func nativeTurnPreview(event string, p *rpc.HistoryPreviewRewindResult) agentapi.NativeTurnChanges {
	unknown := agentapi.NativeTurnChanges{Status: "unknown"}
	if len(p.Files) > 16384 || p.FileCount < 0 || p.FileCount > agentapi.MaxTurnChangeCount || p.FileCount != int64(len(p.Files)) {
		return unknown
	}
	out := agentapi.NativeTurnChanges{Status: "available", EventID: event, Files: p.FileCount}
	seen := make(map[string]bool, len(p.Files))
	bytes := 0
	for _, f := range p.Files {
		if !filepath.IsAbs(f.Path) || strings.ContainsRune(f.Path, 0) || seen[f.Path] || f.LinesAdded < 0 || f.LinesRemoved < 0 || f.LinesAdded > agentapi.MaxTurnChangeCount-out.Additions || f.LinesRemoved > agentapi.MaxTurnChangeCount-out.Deletions {
			return unknown
		}
		seen[f.Path] = true
		out.Additions += f.LinesAdded
		out.Deletions += f.LinesRemoved
		if len(out.Entries) >= agentapi.MaxTurnChangeFiles || len(f.Path) > 4096 || bytes+len(f.Path) > agentapi.MaxTurnChangePathBytes || len(f.ChangeType) > 32 {
			out.Omitted++
			continue
		}
		bytes += len(f.Path)
		out.Entries = append(out.Entries, agentapi.NativeTurnFile{Path: strings.Clone(f.Path), Kind: strings.Clone(string(f.ChangeType)), Additions: f.LinesAdded, Deletions: f.LinesRemoved})
	}
	return out
}
