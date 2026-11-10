package copilot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// backgroundOutputCap bounds the text of one ReadBackgroundOutput, like
// toolOutputCap; the CLI itself returns up to 64 KiB a read (CLI 1.0.108).
const backgroundOutputCap = int(toolOutputCap)

func (a sdkSessionAdapter) ReadTaskOutput(ctx context.Context, id, cursor string) (*rpc.TasksReadOutputResult, error) {
	req := &rpc.TasksReadOutputRequest{ID: id}
	if cursor != "" {
		req.Cursor = &cursor
	}
	result, err := a.s.RPC.Tasks.ReadOutput(ctx, req)
	if err != nil && isRPCError(err) {
		return nil, rejectedError{err}
	}
	return result, err
}

// ReadBackgroundOutput returns at most backgroundOutputCap bytes of a
// detached shell's output. Its cursor is the count of bytes already returned
// from one CLI read, a colon, then the CLI's own cursor for that read: the CLI
// cannot be asked for less, so the same read is repeated until it is used up.
// The CLI refuses a shell it does not keep output for (an attached one, or one
// it no longer lists) and a cursor its log no longer reaches.
func (c *conversation) ReadBackgroundOutput(ctx context.Context, id, cursor string) (agentapi.BackgroundOutput, error) {
	c.mu.Lock()
	sess, closed := c.sess, c.closed
	c.mu.Unlock()
	if closed || sess == nil {
		return agentapi.BackgroundOutput{}, agentapi.ErrClosed
	}
	skip, from := 0, ""
	if cursor != "" {
		n, rest, ok := strings.Cut(cursor, ":")
		used, err := strconv.Atoi(n)
		if !ok || err != nil || used < 0 {
			return agentapi.BackgroundOutput{}, agentapi.ErrBackgroundOutputUnavailable
		}
		skip, from = used, rest
	}
	res, err := sess.ReadTaskOutput(ctx, id, from)
	var rejected rejectedError
	switch {
	case errors.As(err, &rejected):
		return agentapi.BackgroundOutput{}, agentapi.ErrBackgroundOutputUnavailable
	case err != nil:
		return agentapi.BackgroundOutput{}, fmt.Errorf("read shell output: %s", errText(err))
	case res == nil || skip > len(res.Text):
		return agentapi.BackgroundOutput{}, agentapi.ErrBackgroundOutputUnavailable
	}
	rest := res.Text[skip:]
	chunk := rest
	if len(chunk) > backgroundOutputCap {
		chunk = clip(chunk, backgroundOutputCap)
		if i := strings.LastIndexByte(chunk, '\n'); i >= 0 {
			chunk = chunk[:i+1]
		}
	}
	out := agentapi.BackgroundOutput{Text: displaytext.SanitizeText(chunk)}
	if len(chunk) < len(rest) {
		out.Cursor, out.More = strconv.Itoa(skip+len(chunk))+":"+from, true
	} else {
		out.Cursor, out.Done = "0:"+res.Cursor, res.Exit != nil
	}
	return out, nil
}
