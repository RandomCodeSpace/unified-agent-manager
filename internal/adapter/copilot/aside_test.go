package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type fakeAsideSession struct {
	*fakeSession
	questions []string
	result    *rpc.UIEphemeralQueryResult
	err       error
	during    func(ctx context.Context) error
}

func (f *fakeAsideSession) EphemeralQuery(ctx context.Context, question string) (*rpc.UIEphemeralQueryResult, error) {
	f.questions = append(f.questions, question)
	if f.during != nil {
		if err := f.during(ctx); err != nil {
			return nil, err
		}
	}
	return f.result, f.err
}

func TestAskAsideSendsOnlyTheQuestionAndChangesNothing(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	f := &fakeAsideSession{fakeSession: h.fs, result: &rpc.UIEphemeralQueryResult{Answer: "line one\n\x1b[31mred\x1b[0m"}}
	c.sess = f
	if !h.p.Capabilities().Aside {
		t.Fatal("aside capability absent")
	}
	before := len(h.sink.all())
	got, err := c.AskAside(context.Background(), "what is left?")
	if err != nil || got == nil || got.Text != "line one\nred" || got.Truncated {
		t.Fatalf("aside = %+v, %v", got, err)
	}
	if len(f.questions) != 1 || f.questions[0] != "what is left?" {
		t.Fatalf("questions = %q", f.questions)
	}
	if len(h.fs.sent) != 0 || h.fs.aborts != 0 || len(h.fs.modelRequests) != 0 || len(h.sink.all()) != before {
		t.Fatal("aside sent a prompt, aborted, switched model or emitted an event")
	}
	c.mu.Lock()
	running := c.aside != nil
	c.mu.Unlock()
	if running {
		t.Fatal("aside left its cancel handle")
	}
}

func TestAskAsideBoundsTheAnswerOnARuneBoundary(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	long := strings.Repeat("a", maxAsideAnswerBytes-1) + "é" + "tail"
	c.sess = &fakeAsideSession{fakeSession: h.fs, result: &rpc.UIEphemeralQueryResult{Answer: long}}
	got, err := c.AskAside(context.Background(), "q")
	if err != nil || !got.Truncated || len(got.Text) > maxAsideAnswerBytes || !utf8.ValidString(got.Text) || !strings.HasPrefix(long, got.Text) {
		t.Fatalf("bounded = %d bytes, truncated %v, %v", len(got.Text), got.Truncated, err)
	}
}

func TestAskAsideErrors(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	f := &fakeAsideSession{fakeSession: h.fs}
	c.sess = f
	if _, err := c.AskAside(context.Background(), "q"); err == nil {
		t.Fatal("missing result accepted")
	}
	f.err = &copilot.RPCError{Code: -32601, Message: "method not found"}
	if _, err := c.AskAside(context.Background(), "q"); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("unknown method = %v", err)
	}
	c.sess = h.fs
	if _, err := c.AskAside(context.Background(), "q"); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("session without the RPC = %v", err)
	}
}

func TestAskAsideCloseCancelsTheWaitAndRefusesAfter(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	entered := make(chan struct{})
	f := &fakeAsideSession{fakeSession: h.fs, result: &rpc.UIEphemeralQueryResult{Answer: "late"}}
	f.during = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	c.sess = f
	done := make(chan error, 1)
	go func() {
		_, err := c.AskAside(context.Background(), "q")
		done <- err
	}()
	<-entered
	if _, err := c.AskAside(context.Background(), "second"); !errors.Is(err, errAsideRunning) {
		t.Fatalf("second aside = %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, agentapi.ErrClosed) {
			t.Fatalf("closed aside = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the aside wait")
	}
	if _, err := c.AskAside(context.Background(), "q"); !errors.Is(err, agentapi.ErrClosed) || len(f.questions) != 1 {
		t.Fatalf("after close = %v, questions %q", err, f.questions)
	}
}
