package copilot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// backgroundOutputCap bounds the text of one ReadBackgroundOutput, like
// toolOutputCap; the CLI itself returns up to 64 KiB a read (CLI 1.0.108).
const backgroundOutputCap = int(toolOutputCap)

// backgroundJumpLimit is how long a read behind
// agentapi.BackgroundOutputLatest follows a shell that keeps printing before
// it returns the newest chunk it has: a third of the 30 s a caller's request
// is given, so one more slow CLI read still ends inside it. A var for tests.
var backgroundJumpLimit = 10 * time.Second

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
// it no longer lists) and a cursor its log no longer reaches. A cursor behind
// agentapi.BackgroundOutputLatest reads on to the end, holding two CLI reads
// at most, and returns the newest chunk; a shell it has not caught up with
// after backgroundJumpLimit gets the newest chunk read so far, with a cursor
// that continues from there.
func (c *conversation) ReadBackgroundOutput(ctx context.Context, id, cursor string) (agentapi.BackgroundOutput, error) {
	c.mu.Lock()
	sess, closed := c.sess, c.closed
	c.mu.Unlock()
	if closed || sess == nil {
		return agentapi.BackgroundOutput{}, agentapi.ErrClosed
	}
	cursor, jump := strings.CutPrefix(cursor, agentapi.BackgroundOutputLatest)
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
	// A shell printing faster than this reads is followed for backgroundJumpLimit, then the newest chunk so far is returned.
	for until := time.Now().Add(backgroundJumpLimit); jump && err == nil && res != nil && skip <= len(res.Text) && res.Text != "" && res.Exit == nil && time.Now().Before(until); {
		next, nerr := sess.ReadTaskOutput(ctx, id, res.Cursor)
		if nerr != nil || next == nil || next.Text == "" && next.Exit == nil {
			err = nerr
			break
		}
		// The newest chunk of the read before stays, so a short last read still fills one.
		kept := res.Text[skip:]
		next.Text = kept[backgroundTail(kept):] + next.Text
		res, skip = next, 0
	}
	var rejected rejectedError
	switch {
	case errors.As(err, &rejected):
		return agentapi.BackgroundOutput{}, agentapi.ErrBackgroundOutputUnavailable
	case err != nil:
		return agentapi.BackgroundOutput{}, fmt.Errorf("read shell output: %s", errText(err))
	case res == nil || skip > len(res.Text):
		return agentapi.BackgroundOutput{}, agentapi.ErrBackgroundOutputUnavailable
	}
	if jump {
		skip += backgroundTail(res.Text[skip:])
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

// backgroundTail returns where the newest backgroundOutputCap bytes of text
// start: after a line break when there is one, else on a character.
func backgroundTail(text string) int {
	if len(text) <= backgroundOutputCap {
		return 0
	}
	at := len(text) - backgroundOutputCap
	if i := strings.IndexByte(text[at:len(text)-1], '\n'); i >= 0 {
		return at + i + 1
	}
	for at < len(text) && !utf8.RuneStart(text[at]) {
		at++
	}
	return at
}
