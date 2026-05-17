package refresh

import (
	"context"
	"testing"
	"time"
)

func TestDefaultSchedulerAndTicks(t *testing.T) {
	s := DefaultScheduler()
	if s.PollInterval == 0 || s.PRInterval == 0 || s.PeekInterval == 0 {
		t.Fatalf("bad default %+v", s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.PollInterval = time.Millisecond
	ch := s.Ticks(ctx)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no tick")
	}
	cancel()
	select {
	case <-ch:
		// Channel may yield a final tick or close after cancellation.
	case <-time.After(time.Second):
		t.Fatal("not closed")
	}
}
