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
	Installed string `json:"installed,omitempty"`
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
// outlives the request. It is refused when no update is available or while
// any of the provider's Tasks works or waits.
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
	busy := m.cliBusyTasks(name)
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
	case busy > 0:
		return ProviderCLI{}, busyTasksError(p, busy)
	}
	st.State, st.Target, st.Error = cliUpdating, st.Latest, ""
	st.updates++
	m.wg.Add(1)
	go m.runCLIUpdate(p, u, st.Target)
	return st.view(), nil
}

func (m *Manager) runCLIUpdate(p agentapi.Provider, u agentapi.CLIUpdater, target string) {
	defer m.wg.Done()
	name := p.Name()
	err := u.UpdateCLI(m.ctx, target, func() error { return m.quiesceCLI(p) })
	if err == nil {
		log.Info("web provider CLI updated", "provider", name, "version", target)
	} else {
		log.Warn("update web provider CLI failed", "provider", name, "version", target, "error", err)
	}
	// Read again, so the installed version is the one now in place.
	m.checkCLI(m.ctx, name, u)
	m.cli.mu.Lock()
	defer m.cli.mu.Unlock()
	st := m.cli.by[name]
	var webErr *Error
	switch {
	case err == nil:
		st.State = cliUpdated
		// UpdateCLI checked the version in place; a failed read must not
		// keep offering the release just installed.
		if st.Installed != target {
			st.Installed, st.newer = target, false
		}
	case errors.Is(err, agentapi.ErrCLIIncompatible):
		reason := err.Error()
		if _, after, ok := strings.Cut(reason, agentapi.ErrCLIIncompatible.Error()+": "); ok {
			reason = after
		}
		st.State, st.Incompatible = cliFailed, target
		st.Error = shortError(fmt.Errorf("%s CLI %s needs a newer uam: %s", p.DisplayName(), target, reason))
	case errors.As(err, &webErr):
		st.State, st.Error = cliFailed, webErr.Message
	default:
		st.State, st.Error = cliFailed, shortError(err)
	}
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

func busyTasksError(p agentapi.Provider, n int) error {
	return newError(http.StatusConflict, "%d %s %s working or waiting for an answer; update when %s",
		n, p.DisplayName(), plural(n, "task is", "tasks are"), plural(n, "it finishes", "they finish"))
}

// quiesceCLI closes the open conversations of p's Tasks for its CLI to be
// replaced. The Tasks keep their state and reopen on their next view or
// prompt, as after an idle close. A Task that works or waits, or that an
// operation holds, keeps its conversation and fails the update; the idle
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
