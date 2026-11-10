package copilot

import (
	"context"
	"fmt"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// Previous lists the local sessions recorded with exactly workdir as their
// working directory, newest first. A session is listed once it has its first
// message.
func (p *webProvider) Previous(ctx context.Context, workdir string) ([]agentapi.PreviousConversation, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	list, err := client.ListSessions(ctx, workdir)
	if err != nil {
		p.poke()
		return nil, fmt.Errorf("list copilot sessions: %s", errText(err))
	}
	out := []agentapi.PreviousConversation{}
	for _, s := range list {
		// A remote session has no local journal to read.
		if s.IsRemote || s.Context == nil || (workdir != "" && s.Context.WorkingDirectory != workdir) {
			continue
		}
		c := agentapi.PreviousConversation{ID: s.SessionID, Workdir: s.Context.WorkingDirectory, CreatedAt: s.StartTime, UpdatedAt: s.ModifiedTime}
		if s.Summary != nil {
			c.Title = *s.Summary
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b agentapi.PreviousConversation) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out, nil
}

// InUse reports which of ids another process holds, through the
// experimental sessions.checkInUse: live in-use locks on this host under the
// same COPILOT_HOME. The CLI this provider runs is not reported.
func (p *webProvider) InUse(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	held, err := client.CheckInUse(ctx, ids)
	if err != nil {
		p.poke()
		return nil, fmt.Errorf("check copilot sessions in use: %s", errText(err))
	}
	return held, nil
}
