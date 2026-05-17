package refresh

import (
	"context"
	"time"
)

// Scheduler merges periodic refresh signals for the Bubble Tea app/service layer.
// The app currently consumes the regular Tick channel directly; this package keeps
// the refresh policy testable and centralized for CLI users that want to embed UAM.
type Scheduler struct {
	PollInterval time.Duration
	PeekInterval time.Duration
	PRInterval   time.Duration
}

func DefaultScheduler() Scheduler {
	return Scheduler{PollInterval: 2 * time.Second, PeekInterval: 5 * time.Second, PRInterval: 60 * time.Second}
}

func (s Scheduler) Ticks(ctx context.Context) <-chan time.Time {
	if s.PollInterval <= 0 {
		s.PollInterval = 2 * time.Second
	}
	ch := make(chan time.Time)
	go func() {
		defer close(ch)
		t := time.NewTicker(s.PollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				select {
				case ch <- now:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch
}
