package copilot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// ForkSession is optional on the client seam so older runtimes and the
// existing non-fork fixtures retain their ordinary conversation behavior.
func (a sdkClientAdapter) ForkSession(ctx context.Context, req *rpc.SessionsForkRequest) (*rpc.SessionsForkResult, error) {
	return a.c.RPC.Sessions.Fork(ctx, req)
}

type nativeForkClient interface {
	ForkSession(context.Context, *rpc.SessionsForkRequest) (*rpc.SessionsForkResult, error)
}

// forkJournalClient bounds the existing forward fold without keeping any
// event bodies or SDK cursor beyond this call. A page can contain one
// oversized native event; the fold retains only small exact identities.
type forkJournalClient struct {
	sdkClient
	pages int
}

func (c *forkJournalClient) ReadEvents(ctx context.Context, req *rpc.SessionsReadPersistedEventsRequest) (*rpc.EventsReadResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.pages >= webReadPages {
		return nil, errors.New("conversation is too large to verify its branch boundary")
	}
	c.pages++
	return c.sdkClient.ReadEvents(ctx, req)
}

func (p *webProvider) ReadForkBoundary(ctx context.Context, req agentapi.ForkBoundaryRequest) (agentapi.ForkBoundary, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return agentapi.ForkBoundary{}, err
	}
	return p.readForkBoundary(ctx, client, req)
}

func (p *webProvider) readForkBoundary(ctx context.Context, client sdkClient, req agentapi.ForkBoundaryRequest) (agentapi.ForkBoundary, error) {
	boundary, _, _, err := p.readBoundary(ctx, client, req)
	return boundary, err
}

// readBoundary also returns the visible root owner IDs from the selected one
// on, newest MaxRewindDiscarded kept, and their total: a rewind's suffix.
func (p *webProvider) readBoundary(ctx context.Context, client sdkClient, req agentapi.ForkBoundaryRequest) (agentapi.ForkBoundary, []string, int, error) {
	if req.ConversationID == "" || req.UserItemID == "" || len(req.UserItemID) > 256 {
		return agentapi.ForkBoundary{}, nil, 0, agentapi.ErrItemNotFound
	}
	for attempt := 1; ; attempt++ {
		var boundary agentapi.ForkBoundary
		var matches, turns int
		var suffix []string
		settled, taskComplete, invalid := false, false, false
		err := p.readForward(ctx, &forkJournalClient{sdkClient: client}, req.ConversationID, func(ev copilot.SessionEvent) {
			if ev.ID == "" || len(ev.ID) > 256 {
				invalid = true
				return
			}
			boundary.TailEventID = strings.Clone(ev.ID)
			if agentOf(ev) != "" {
				return
			}
			switch d := ev.Data.(type) {
			case *rpc.UserMessageData:
				root := (d.Delivery == nil || *d.Delivery != rpc.UserMessageDeliverySteering) && (d.IsAutopilotContinuation == nil || !*d.IsAutopilotContinuation)
				if !root {
					if boundary.UserEventID != "" && boundary.ToEventID == "" {
						settled, taskComplete = false, false
					}
					return
				}
				id := ev.ID
				if d.MessageID != nil && *d.MessageID != "" {
					id = *d.MessageID
				}
				if boundary.UserEventID != "" && boundary.ToEventID == "" {
					boundary.ToEventID = strings.Clone(ev.ID)
					settled = true
				}
				if id == req.UserItemID {
					matches++
					if matches == 1 {
						boundary.UserEventID = strings.Clone(ev.ID)
						settled, taskComplete = false, false
					}
				}
				if boundary.UserEventID != "" {
					turns++
					if len(suffix) == agentapi.MaxRewindDiscarded {
						suffix = slices.Delete(suffix, 0, 1)
					}
					suffix = append(suffix, strings.Clone(id))
				}
			case *rpc.SessionTaskCompleteData:
				taskComplete = true
			case *rpc.SessionIdleData:
				if boundary.UserEventID != "" && boundary.ToEventID == "" && (d.Mode == nil || *d.Mode != rpc.SessionModeAutopilot || taskComplete || d.Aborted != nil && *d.Aborted) {
					settled = true
				}
			}
		})
		if errors.Is(err, errJournalChanged) && attempt < webWindowAttempts {
			continue
		}
		if err != nil {
			return agentapi.ForkBoundary{}, nil, 0, err
		}
		if invalid || matches > 1 {
			return agentapi.ForkBoundary{}, nil, 0, errors.New("the recorded branch boundary is ambiguous")
		}
		if matches == 0 {
			return agentapi.ForkBoundary{}, nil, 0, agentapi.ErrItemNotFound
		}
		if !settled {
			return agentapi.ForkBoundary{}, nil, 0, agentapi.ErrBusy
		}
		return boundary, suffix, turns, nil
	}
}

func (p *webProvider) Fork(ctx context.Context, req agentapi.ForkRequest) (string, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return "", err
	}
	forker, ok := client.(nativeForkClient)
	if !ok {
		return "", agentapi.ErrUnsupported
	}
	current, err := p.readForkBoundary(ctx, client, req.ForkBoundaryRequest)
	if err != nil {
		return "", err
	}
	if current != req.Boundary {
		return "", errors.New("the recorded conversation changed before branching; choose the turn again")
	}
	params := &rpc.SessionsForkRequest{SessionID: req.ConversationID}
	if current.ToEventID != "" {
		params.ToEventID = &current.ToEventID
	}
	res, err := forker.ForkSession(ctx, params)
	var rpcErr *copilot.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
		p.mu.Lock()
		p.forkUnsupported = true
		p.mu.Unlock()
		return "", agentapi.ErrUnsupported
	}
	if err != nil {
		return "", fmt.Errorf("%w: %s", agentapi.ErrForkUncertain, errText(err))
	}
	if res == nil || res.SessionID == "" {
		return "", agentapi.ErrForkUncertain
	}
	return res.SessionID, nil
}
