package copilot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// cliStart is one CLI start: done closes when it has ended, and err then
// says why it failed. cancel ends it early.
type cliStart struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

// ensureStarted returns the running client and starts the CLI when none
// runs. One start runs at a time, without mu held: each caller waits for it
// within its own ctx and gets its result, and a failed one is started again
// by the next call.
func (p *webProvider) ensureStarted(ctx context.Context) (sdkClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		// An update is replacing the CLI: a start waits for the new install
		// instead of failing, and an outdated client is not handed out while
		// it is being stopped. A current one keeps serving.
		for p.updating != nil && (p.client == nil || p.outdated) {
			updating := p.updating
			p.mu.Unlock()
			select {
			case <-updating:
			case <-ctx.Done():
			}
			p.mu.Lock()
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("wait for the Copilot CLI update: %w", err)
			}
		}
		if p.shut {
			return nil, agentapi.ErrClosed
		}
		if p.client != nil {
			return p.client, nil
		}
		st := p.starting
		if st == nil {
			st = p.startLocked()
		}
		p.mu.Unlock()
		select {
		case <-st.done:
		case <-ctx.Done():
			p.mu.Lock()
			return nil, fmt.Errorf("start copilot CLI: %s", errText(ctx.Err()))
		}
		p.mu.Lock()
		if st.err != nil {
			return nil, st.err
		}
	}
}

// startLocked begins a CLI start on its own goroutine. No caller's ctx
// bounds it: the callers that wait for it each have their own. The caller
// holds mu and has found no client and no start in flight.
func (p *webProvider) startLocked() *cliStart {
	ctx, cancel := context.WithCancel(context.Background())
	st := &cliStart{done: make(chan struct{}), cancel: cancel}
	p.starting = st
	sweep, probe := !p.swept, !p.importProbed
	p.swept = true
	go p.runStart(ctx, st, sweep, probe)
	return st
}

// runStart starts a CLI and, under mu, makes it the running client, unless
// the start failed or the provider shut down meanwhile: then no CLI is left
// running.
func (p *webProvider) runStart(ctx context.Context, st *cliStart, sweep, probe bool) {
	defer log.Recover("copilot CLI start", &p.mu)
	var (
		c    sdkClient
		path string
		file os.FileInfo
		// What a start that panics ends with, so nothing waits on it for good.
		err = errors.New("start copilot CLI: the start did not finish")
	)
	defer func() {
		p.mu.Lock()
		p.starting = nil
		if p.shut {
			err = agentapi.ErrClosed
		}
		if err == nil {
			p.cliPath, p.cliFile, p.importProbed = path, file, true
			p.client, p.stop = c, make(chan struct{})
			p.assistedRefused.Store(nil)
			p.stalls.Store(0)
			// This CLI's sign-in read is newer than any call of the one before.
			p.quotaMu.Lock()
			p.live, p.plan, p.credits = nil, "", nil
			p.quotaMu.Unlock()
			go p.watch(c, p.stop) // #nosec G118 -- provider-owned watchdog outlives requests; Shutdown/fail closes stop and each ping has a timeout.
		}
		st.err = err
		p.mu.Unlock()
		if err != nil && c != nil {
			c.ForceStop()
		}
		st.cancel()
		close(st.done)
	}()
	if sweep {
		if n := sweepOrphanCLIs(); n > 0 {
			log.Warn("stopped copilot CLIs left running by a uam that is gone", "count", n)
		}
	}
	// Read before the start: a file replaced meanwhile reads as replaced,
	// never the reverse.
	if p.installedCLI != nil {
		if now, info, statErr := p.installedCLI(); statErr == nil {
			path, file = now, info
		}
	}
	c, err = p.startClient(ctx, probe)
}

// startClient starts a CLI within webStartTimeout and returns its client;
// probe also asks it whether it imports sessions. A CLI that fails to start
// is stopped.
func (p *webProvider) startClient(ctx context.Context, probe bool) (sdkClient, error) {
	c, err := p.newClient()
	if err != nil {
		return nil, err
	}
	sctx, cancel := context.WithTimeout(ctx, webStartTimeout)
	defer cancel()
	if err := c.Start(sctx); err != nil {
		c.ForceStop()
		return nil, fmt.Errorf("start copilot CLI: %s", errText(err))
	}
	if probe {
		if probe, ok := c.(interface{ ImportSupported(context.Context) bool }); ok {
			probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
			p.importSupported.Store(probe.ImportSupported(probeCtx))
			probeCancel()
		}
	}
	return c, nil
}

// awaitStartLocked returns once no CLI start is in flight, for what stops or
// replaces the CLI: it must not miss one that is starting. The caller holds
// mu, which is released while it waits.
func (p *webProvider) awaitStartLocked() {
	for p.starting != nil {
		done := p.starting.done
		p.mu.Unlock()
		<-done
		p.mu.Lock()
	}
}

// startUtility returns the running client for a utility prompt, counted
// until done is called, so an update's restart does not stop the CLI under
// it. A client stopped between the start and the count is not used.
func (p *webProvider) startUtility(ctx context.Context) (client sdkClient, done func(), err error) {
	for {
		if client, err = p.ensureStarted(ctx); err != nil {
			return nil, nil, err
		}
		p.mu.Lock()
		if p.client == client {
			p.utilities++
			p.mu.Unlock()
			return client, sync.OnceFunc(func() {
				p.mu.Lock()
				p.utilities--
				p.mu.Unlock()
			}), nil
		}
		p.mu.Unlock()
	}
}

// watch pings the CLI until stop closes. The SDK reports neither a CLI exit
// nor a hang, so a failed ping is the only signal that the runtime is gone.
// A ping that fails ends it at once; one that times out does only after
// webPingMisses in a row, since a busy CLI can miss one and recover.
func (p *webProvider) watch(c sdkClient, stop <-chan struct{}) {
	defer log.Recover("copilot watchdog", &p.mu)
	t := time.NewTicker(p.pingEvery)
	defer t.Stop()
	misses := 0
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		case <-p.kick:
		}
		ctx, cancel := context.WithTimeout(context.Background(), webPingTimeout)
		err := c.Ping(ctx)
		cancel()
		switch {
		case err == nil:
			misses = 0
		case errors.Is(err, context.DeadlineExceeded) && misses+1 < webPingMisses:
			misses++
		default:
			p.fail(c, "Copilot CLI stopped: "+exitText(err))
			return
		}
	}
}

// poke asks the watchdog for an immediate ping after an unexpected error.
func (p *webProvider) poke() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// fail ends every conversation on a dead client and resets the provider so a
// later Open starts a fresh CLI. Nothing is resent or reopened.
func (p *webProvider) fail(c sdkClient, reason string) {
	p.mu.Lock()
	if p.client != c {
		p.mu.Unlock()
		return
	}
	p.client, p.outdated = nil, false
	close(p.stop)
	p.stop = nil
	convs := p.takeConvs()
	p.mu.Unlock()
	for _, conv := range convs {
		conv.exit(reason)
	}
	c.ForceStop()
	if err := p.releaseClientUsage(c); err != nil {
		log.Warn("release stopped copilot usage ownership failed", "error", err)
	}
}

func (p *webProvider) takeConvs() []*conversation {
	convs := make([]*conversation, 0, len(p.convs))
	for c := range p.convs {
		convs = append(convs, c)
	}
	clear(p.convs)
	return convs
}

func (p *webProvider) track(c *conversation) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shut || p.client != c.client {
		return false
	}
	p.convs[c] = struct{}{}
	return true
}

func (p *webProvider) forget(c *conversation) {
	p.mu.Lock()
	delete(p.convs, c)
	p.mu.Unlock()
}

func (p *webProvider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.shut = true
	if st := p.starting; st != nil {
		// A start in flight ends early and stops what it started.
		st.cancel()
		p.mu.Unlock()
		select {
		case <-st.done:
		case <-ctx.Done():
		}
		p.mu.Lock()
	}
	convs := p.takeConvs()
	client := p.client
	p.client, p.outdated = nil, false
	if p.stop != nil {
		close(p.stop)
		p.stop = nil
	}
	p.mu.Unlock()
	var errs []error
	for _, c := range convs {
		if err := c.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if client != nil {
		if err := stopClient(ctx, client); err != nil {
			errs = append(errs, err)
		}
		if err := p.releaseClientUsage(client); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// stopClient stops the CLI gracefully and kills it when that takes too long.
// Stop kills the process even when it reports an error.
func stopClient(ctx context.Context, c sdkClient) error {
	ctx, cancel := context.WithTimeout(ctx, webStopTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer log.Recover("copilot cli stop")
		done <- c.Stop()
	}()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("stop copilot CLI: %s", errText(err))
		}
		return nil
	case <-ctx.Done():
		c.ForceStop()
		return fmt.Errorf("stop copilot CLI: %w", ctx.Err())
	}
}
