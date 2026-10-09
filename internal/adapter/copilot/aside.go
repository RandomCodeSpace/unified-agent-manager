package copilot

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// maxAsideAnswerBytes bounds the answer one aside question returns.
const maxAsideAnswerBytes = 64 << 10

var errAsideRunning = errors.New("an aside question is already waiting for its answer")

type asideSession interface {
	EphemeralQuery(ctx context.Context, question string) (*rpc.UIEphemeralQueryResult, error)
}

// EphemeralQuery sends only the public Question. The request's internal
// in-process members stay nil, so no callback or signal goes over JSON.
func (a sdkSessionAdapter) EphemeralQuery(ctx context.Context, question string) (*rpc.UIEphemeralQueryResult, error) {
	return a.s.RPC.UI.EphemeralQuery(ctx, &rpc.UIEphemeralQueryRequest{Question: question})
}

// AskAside runs session.ui.ephemeralQuery: a no-tools answer from the
// conversation's context with its current model, kept out of its history.
// The ui.ephemeral_query stream events stay ignored: their request ID is not
// in the RPC's request or result, so they cannot be tied to this call. Close
// cancels the wait.
func (c *conversation) AskAside(ctx context.Context, question string) (*agentapi.AsideAnswer, error) {
	asker, ok := c.sess.(asideSession)
	if !ok {
		return nil, agentapi.ErrUnsupported
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	switch {
	case c.closed:
		c.mu.Unlock()
		return nil, agentapi.ErrClosed
	case c.aside != nil:
		c.mu.Unlock()
		return nil, errAsideRunning
	}
	c.aside = cancel
	c.mu.Unlock()
	res, err := asker.EphemeralQuery(ctx, question)
	c.mu.Lock()
	c.aside = nil
	closed := c.closed
	c.mu.Unlock()
	switch {
	case closed:
		return nil, agentapi.ErrClosed
	case err != nil:
		var rpcErr *copilot.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
			return nil, agentapi.ErrUnsupported
		}
		return nil, err
	case res == nil:
		return nil, errors.New("copilot returned no aside answer")
	}
	text, truncated := clipAside(displaytext.SanitizeText(res.Answer))
	return &agentapi.AsideAnswer{Text: strings.Clone(text), Truncated: truncated}, nil
}

// clipAside cuts s to maxAsideAnswerBytes on a rune boundary.
func clipAside(s string) (string, bool) {
	if len(s) <= maxAsideAnswerBytes {
		return s, false
	}
	n := maxAsideAnswerBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}
