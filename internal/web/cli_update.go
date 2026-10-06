package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const (
	// cliCheckEvery is how often the service looks for a newer provider CLI.
	cliCheckEvery = 6 * time.Hour
	// cliCheckTimeout bounds one look: the installed version, npm's folder
	// and the newest release.
	cliCheckTimeout = 60 * time.Second
	// cliRestartEvery is how often a pending CLI restart is tried besides
	// when a Task changes.
	cliRestartEvery = time.Minute
)

// CLI update states, as the UI sees them.
const (
	cliIdle     = "idle"
	cliUpdating = "updating"
	cliUpdated  = "updated"
	cliFailed   = "failed"
)

// ProviderCLI is a provider CLI's installed and newest release and its
// update, as the API reports them.
type ProviderCLI struct {
	// Installed is the release on disk; Running, while set, is the older
	// one the CLI still runs after an update, until no Task works or waits
	// and it restarts.
	Installed string `json:"installed,omitempty"`
	Running   string `json:"running,omitempty"`
	Latest    string `json:"latest,omitempty"`
	// UpdateAvailable: Latest is newer than Installed, the SDK did not
	// refuse it, and the CLI can be updated here.
	UpdateAvailable bool   `json:"update_available"`
	Manual          string `json:"manual,omitempty"`
	// Incompatible is the newest release the SDK refused; it is not offered
	// again while this service runs.
	Incompatible string `json:"incompatible,omitempty"`
	// CheckedAt is when the releases were last read; CheckError why the
	// last read failed, the releases shown being the ones read before.
	CheckedAt  time.Time `json:"checked_at,omitzero"`
	CheckError string    `json:"check_error,omitempty"`
	State      string    `json:"state"`
	// Target is the release being installed, or the last one tried.
	Target string `json:"target,omitempty"`
	Error  string `json:"error,omitempty"`
}

type cliStatus struct {
	ProviderCLI
	newer   bool // Latest is newer than Installed
	checked bool // a read finished, successfully or not
	// updates counts the updates started; a read begun before one is
	// dropped, so it cannot bring back the release replaced.
	updates int
	// restarting: a goroutine tries the pending restart.
	restarting bool
}

func (st *cliStatus) view() ProviderCLI {
	v := st.ProviderCLI
	v.UpdateAvailable = st.newer && st.Latest != st.Incompatible && st.Manual == ""
	return v
}

type cliUpdates struct {
	mu sync.Mutex
	by map[string]*cliStatus
}

// cliStatusLocked returns name's status, idle when nothing was read yet.
// The caller holds m.cli.mu.
func (m *Manager) cliStatusLocked(name string) *cliStatus {
	st := m.cli.by[name]
	if st == nil {
		st = &cliStatus{ProviderCLI: ProviderCLI{State: cliIdle}}
		if m.cli.by == nil {
			m.cli.by = map[string]*cliStatus{}
		}
		m.cli.by[name] = st
	}
	return st
}

// cliProvidersLocked lists the providers that update their CLI. The caller
// holds mu.
func (m *Manager) cliProvidersLocked() []string {
	var names []string
	for _, name := range m.order {
		p := m.providers[name]
		if _, ok := p.(agentapi.CLIUpdater); ok && p.Capabilities().CLIUpdate {
			names = append(names, name)
		}
	}
	return names
}

func (m *Manager) cliUpdater(name string) (agentapi.Provider, agentapi.CLIUpdater, error) {
	m.mu.Lock()
	p := m.providers[name]
	m.mu.Unlock()
	if p == nil {
		return nil, nil, newError(http.StatusNotFound, msgUnknownProvider, name)
	}
	u, ok := p.(agentapi.CLIUpdater)
	if !ok || !p.Capabilities().CLIUpdate {
		return nil, nil, newError(http.StatusNotFound, "%s cannot update its CLI here", p.DisplayName())
	}
	return p, u, nil
}

// cliLoop reads the releases of each provider's CLI at start and every
// cliCheckEvery.
func (m *Manager) cliLoop(names []string) {
	defer m.wg.Done()
	t := time.NewTicker(cliCheckEvery)
	defer t.Stop()
	for {
		for _, name := range names {
			_, _ = m.ProviderCLI(m.ctx, name, true)
		}
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ProviderCLI reports the provider CLI's releases and update. It reads the
// releases first when none were read yet or refresh is set, unless an update
// runs.
func (m *Manager) ProviderCLI(ctx context.Context, name string, refresh bool) (ProviderCLI, error) {
	_, u, err := m.cliUpdater(name)
	if err != nil {
		return ProviderCLI{}, err
	}
	m.cli.mu.Lock()
	st := m.cliStatusLocked(name)
	due := (refresh || !st.checked) && st.State != cliUpdating
	m.cli.mu.Unlock()
	if due {
		m.checkCLI(ctx, name, u)
	}
	m.cli.mu.Lock()
	defer m.cli.mu.Unlock()
	return m.cli.by[name].view(), nil
}

// checkCLI reads the provider CLI's releases. A failed read keeps the
// releases read before, and its error.
func (m *Manager) checkCLI(ctx context.Context, name string, u agentapi.CLIUpdater) {
	m.cli.mu.Lock()
	updates := m.cliStatusLocked(name).updates
	m.cli.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, cliCheckTimeout)
	rel, err := u.CLIRelease(cctx)
	cancel()
	if err != nil {
		log.Warn("read web provider CLI releases failed", "provider", name, "error", err)
	}
	m.cli.mu.Lock()
	defer m.cli.mu.Unlock()
	st := m.cli.by[name]
	if st.updates != updates {
		return
	}
	st.checked = true
	if err != nil {
		st.CheckError = shortError(err)
		if st.CheckedAt.IsZero() {
			st.Installed = rel.Installed
		}
		return
	}
	st.Installed, st.Latest, st.newer, st.Manual = rel.Installed, rel.Latest, rel.Newer, rel.Manual
	st.CheckedAt, st.CheckError = m.now(), ""
}

// cliUpdatesAvailable maps each provider with a CLI update available to the
// release it updates to.
func (m *Manager) cliUpdatesAvailable() map[string]string {
	m.cli.mu.Lock()
	defer m.cli.mu.Unlock()
	out := map[string]string{}
	for name, st := range m.cli.by {
		if v := st.view(); v.UpdateAvailable {
			out[name] = v.Latest
		}
	}
	return out
}

// StartCLIUpdate starts updating the provider's CLI to its newest release,
// or returns the update in progress. It runs on the service's context, so it
// outlives the request. It is refused when no update is available. Tasks
// that work or wait are not disturbed: the CLI restarts onto the release
// once none does.
func (m *Manager) StartCLIUpdate(ctx context.Context, name string) (ProviderCLI, error) {
	p, u, err := m.cliUpdater(name)
	if err != nil {
		return ProviderCLI{}, err
	}
	m.cli.mu.Lock()
	checked := m.cliStatusLocked(name).checked
	m.cli.mu.Unlock()
	if !checked {
		m.checkCLI(ctx, name, u)
	}
	m.cli.mu.Lock()
	defer m.cli.mu.Unlock()
	st := m.cli.by[name]
	view := st.view()
	switch {
	case view.State == cliUpdating:
		return view, nil
	case view.Manual != "":
		return ProviderCLI{}, newError(http.StatusConflict, "%s", view.Manual)
	case !view.UpdateAvailable:
		return ProviderCLI{}, newError(http.StatusConflict, "no update of the %s CLI is available", p.DisplayName())
	}
	st.State, st.Target, st.Error = cliUpdating, st.Latest, ""
	st.updates++
	m.wg.Add(1)
	go m.runCLIUpdate(p, u, st.Target, st.Installed)
	return st.view(), nil
}

// runCLIUpdate installs target over from, then restarts the CLI onto it at
// once when no Task works or waits, else as soon as none does.
func (m *Manager) runCLIUpdate(p agentapi.Provider, u agentapi.CLIUpdater, target, from string) {
	defer m.wg.Done()
	name := p.Name()
	err := u.UpdateCLI(m.ctx, target)
	pending := false
	if err == nil {
		log.Info("web provider CLI updated", "provider", name, "version", target)
		pending = m.restartCLI(p, u) != nil
	} else {
		log.Warn("update web provider CLI failed", "provider", name, "version", target, "error", err)
	}
	// Read again, so the installed version is the one now in place.
	m.checkCLI(m.ctx, name, u)
	m.cli.mu.Lock()
	st := m.cli.by[name]
	switch {
	case err == nil:
		st.State = cliUpdated
		// UpdateCLI checked the version in place; a failed read must not
		// keep offering the release just installed.
		if st.Installed != target {
			st.Installed, st.newer = target, false
		}
		// A second update while a restart is pending keeps the release
		// still running.
		switch {
		case !pending:
			st.Running = ""
		case st.Running == "":
			st.Running = from
		}
	case errors.Is(err, agentapi.ErrCLIIncompatible):
		reason := err.Error()
		if _, after, ok := strings.Cut(reason, agentapi.ErrCLIIncompatible.Error()+": "); ok {
			reason = after
		}
		st.State, st.Incompatible = cliFailed, target
		st.Error = shortError(fmt.Errorf("%s CLI %s needs a newer uam: %s", p.DisplayName(), target, reason))
	default:
		st.State, st.Error = cliFailed, shortError(err)
	}
	wait := pending && !st.restarting
	st.restarting = st.restarting || wait
	m.cli.mu.Unlock()
	if wait {
		m.restartCLIWhenIdle(p, u)
	}
}

// restartCLIWhenIdle tries the pending restart of p's CLI each time a Task
// changes and every cliRestartEvery, until it is done or the service stops.
func (m *Manager) restartCLIWhenIdle(p agentapi.Provider, u agentapi.CLIUpdater) {
	t := time.NewTicker(cliRestartEvery)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		case <-m.cliKick:
		}
		if err := m.restartCLI(p, u); err != nil {
			if errors.Is(err, agentapi.ErrClosed) {
				return
			}
			continue
		}
		log.Info("web provider CLI restarted onto its update", "provider", p.Name())
		m.cli.mu.Lock()
		st := m.cli.by[p.Name()]
		st.Running, st.restarting = "", false
		m.cli.mu.Unlock()
		return
	}
}

// restartCLI restarts p's CLI onto the release an update installed unless
// any of p's Tasks works or waits. The idle conversations are closed for it
// and reopen on their next use. nil once no CLI runs a replaced release.
func (m *Manager) restartCLI(p agentapi.Provider, u agentapi.CLIUpdater) error {
	if n := m.cliBusyTasks(p.Name()); n > 0 {
		return busyTasksError(p, n)
	}
	return u.RestartCLI(m.ctx, func() error { return m.quiesceCLI(p) })
}

// cliBusyTasks counts the provider's Tasks whose open conversation works or
// waits for anything.
func (m *Manager) cliBusyTasks(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if s.provider == name && s.conv != nil && s.runsOrWaitsLocked() {
			n++
		}
	}
	return n
}

// busyTasksError says how many of p's Tasks hold its CLI restart back.
func busyTasksError(p agentapi.Provider, n int) error {
	return fmt.Errorf("%d %s %s working or waiting for an answer", n, p.DisplayName(), plural(n, "task is", "tasks are"))
}

// quiesceCLI closes the open conversations of p's Tasks for its CLI to be
// restarted. The Tasks keep their state and reopen on their next view or
// prompt, as after an idle close. A Task that works or waits, or that an
// operation holds, keeps its conversation and fails the restart; the idle
// ones are closed all the same.
func (m *Manager) quiesceCLI(p agentapi.Provider) error {
	m.mu.Lock()
	var open []*webSession
	for _, s := range m.sessions {
		if s.provider == p.Name() && s.conv != nil {
			open = append(open, s)
		}
	}
	m.mu.Unlock()
	busy := 0
	for _, s := range open {
		if !m.suspendForCLI(s) {
			busy++
		}
	}
	if busy > 0 {
		return busyTasksError(p, busy)
	}
	return nil
}

// suspendForCLI closes s's conversation unless an operation holds s or the
// conversation works or waits. It reports whether s has none open.
func (m *Manager) suspendForCLI(s *webSession) bool {
	if !s.op.TryLock() {
		return false
	}
	defer s.op.Unlock()
	m.mu.Lock()
	if m.closed || s.removed || s.conv == nil {
		m.mu.Unlock()
		return true
	}
	if s.runsOrWaitsLocked() {
		m.mu.Unlock()
		return false
	}
	before := m.summaryLocked(s)
	// A viewer keeps its transcript; the reopen merges the provider's record
	// into it.
	conv := m.suspendLocked(s, false)
	m.changedLocked(s, before)
	m.mu.Unlock()
	m.closeConversation(conv)
	log.Info("closed web conversation for a CLI update", "session", s.id)
	return true
}

func (s *Server) handleProviderCLI(w http.ResponseWriter, r *http.Request) {
	cli, err := s.m.ProviderCLI(r.Context(), r.PathValue("provider"), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cli)
}

func (s *Server) handleUpdateProviderCLI(w http.ResponseWriter, r *http.Request) {
	cli, err := s.m.StartCLIUpdate(r.Context(), r.PathValue("provider"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cli)
}
