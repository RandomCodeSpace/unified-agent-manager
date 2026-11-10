package copilot

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// disconnectBounded detaches sess on an error path and waits until the
// detach returns, ctx ends or webStopTimeout passes: the SDK's detach has no
// deadline. A reopen of the session waits for a detach still in flight.
func (p *webProvider) disconnectBounded(ctx context.Context, sess sdkSession) {
	id := sess.ID()
	detached := p.detachStarted(id)
	done := make(chan struct{})
	go func() {
		defer log.Recover("copilot session disconnect", &p.mu)
		defer detached()
		defer close(done)
		if err := sess.Disconnect(); err != nil {
			log.Info("disconnect copilot session failed", "conversation", id, "error", err)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, webStopTimeout)
	defer cancel()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// detachWait is one detach of a session in flight: done closes when it
// returns, and a reopen waits for it until until, when release gives up.
type detachWait struct {
	done  chan struct{}
	until time.Time
}

// detachStarted records that session id is detaching and returns the
// function that ends the record once the detach has returned. Callers
// record it before the conversation ends, so no reopen can come first.
func (p *webProvider) detachStarted(id string) func() {
	w := &detachWait{done: make(chan struct{}), until: time.Now().Add(webDetachWait)}
	p.mu.Lock()
	if p.detaching == nil {
		p.detaching = map[string][]*detachWait{}
	}
	p.detaching[id] = append(p.detaching[id], w)
	p.mu.Unlock()
	return sync.OnceFunc(func() {
		p.mu.Lock()
		p.dropDetachesLocked(id, func(x *detachWait) bool { return x == w })
		p.mu.Unlock()
		close(w.done)
	})
}

func (p *webProvider) dropDetachesLocked(id string, drop func(*detachWait) bool) {
	if left := slices.DeleteFunc(p.detaching[id], drop); len(left) > 0 {
		p.detaching[id] = left
	} else {
		delete(p.detaching, id)
	}
}

// awaitDetach waits for every detach of session id in flight, each until
// it returns or is due. The SDK detaches by session ID, so one the CLI
// handled after a resume of the same session could take it from its new
// owner. A detach past due, which the CLI never answered, is forgotten.
func (p *webProvider) awaitDetach(ctx context.Context, id string) {
	now := time.Now()
	p.mu.Lock()
	p.dropDetachesLocked(id, func(w *detachWait) bool { return now.After(w.until) })
	waits := slices.Clone(p.detaching[id])
	p.mu.Unlock()
	for _, w := range waits {
		t := time.NewTimer(time.Until(w.until))
		select {
		case <-w.done:
		case <-t.C:
		case <-ctx.Done():
		}
		t.Stop()
		if ctx.Err() != nil {
			return
		}
	}
}
