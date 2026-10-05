package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// codeSignedOut marks a refusal because the provider's runtime is signed out.
const codeSignedOut = "provider_signed_out"

func signedOutReason(p agentapi.Provider) string {
	return p.DisplayName() + " is signed out. Sign in in Settings."
}

// unavailableReason is the Reason an unavailable provider shows, and whether
// it is unavailable because it is signed out.
func unavailableReason(p agentapi.Provider, err error) (string, bool) {
	if errors.Is(err, agentapi.ErrSignedOut) {
		return signedOutReason(p), true
	}
	return shortError(err), false
}

func (m *Manager) accountManager(name string) (agentapi.Provider, agentapi.AccountManager, error) {
	m.mu.Lock()
	p := m.providers[name]
	m.mu.Unlock()
	if p == nil {
		return nil, nil, newError(http.StatusNotFound, msgUnknownProvider, name)
	}
	am, ok := p.(agentapi.AccountManager)
	if !ok || !p.Capabilities().Account {
		return nil, nil, newError(http.StatusNotFound, "%s has no account to manage here", p.DisplayName())
	}
	return p, am, nil
}

// Account reads a provider's sign-in and brings its availability in line
// with it.
func (m *Manager) Account(ctx context.Context, name string) (agentapi.Account, error) {
	p, am, err := m.accountManager(name)
	if err != nil {
		return agentapi.Account{}, err
	}
	acct, err := am.Account(ctx)
	if err != nil {
		return agentapi.Account{}, accountError(err)
	}
	m.applyAccount(ctx, p, acct)
	return acct, nil
}

// SignIn hands token to the provider's runtime, which validates and stores
// it. The token is not logged or kept here.
func (m *Manager) SignIn(ctx context.Context, name, token string) (agentapi.Account, error) {
	p, am, err := m.accountManager(name)
	if err != nil {
		return agentapi.Account{}, err
	}
	acct, err := am.SignIn(ctx, token)
	if err != nil {
		return agentapi.Account{}, accountError(err)
	}
	log.Info("web provider signed in", "provider", name, "source", acct.Source)
	m.applyAccount(ctx, p, acct)
	return acct, nil
}

// SignOut removes the provider runtime's stored sign-in.
func (m *Manager) SignOut(ctx context.Context, name string) (agentapi.Account, error) {
	p, am, err := m.accountManager(name)
	if err != nil {
		return agentapi.Account{}, err
	}
	acct, err := am.SignOut(ctx)
	if err != nil {
		return agentapi.Account{}, accountError(err)
	}
	log.Info("web provider signed out", "provider", name)
	m.applyAccount(ctx, p, acct)
	return acct, nil
}

func accountError(err error) error {
	switch {
	case errors.Is(err, agentapi.ErrEnvAccount):
		_, name, _ := strings.Cut(err.Error(), ": ")
		return newError(http.StatusConflict, "%s in the service environment takes precedence over a sign-in made here; change or remove it where the service starts", name)
	case errors.Is(err, agentapi.ErrSignInRejected):
		_, msg, _ := strings.Cut(err.Error(), ": ")
		return newError(http.StatusBadRequest, "%s", msg)
	case errors.Is(err, agentapi.ErrUnsupported):
		return newError(http.StatusNotImplemented, "this provider cannot report its sign-in")
	}
	return newError(http.StatusBadGateway, "%s", shortError(err))
}

// applyAccount marks p unavailable while it is signed out, and available
// again with its models loaded once it is signed in, so a sign-in needs no
// service restart.
func (m *Manager) applyAccount(ctx context.Context, p agentapi.Provider, acct agentapi.Account) {
	name := p.Name()
	m.mu.Lock()
	info := m.infos[name]
	if !acct.SignedIn {
		info.Available, info.SignedOut, info.Reason = false, true, signedOutReason(p)
		m.infos[name] = info
		m.mu.Unlock()
		m.kickQuota(name)
		return
	}
	wasOut := info.SignedOut || !info.Available
	m.mu.Unlock()
	if !wasOut {
		m.kickQuota(name)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	err := p.Check(cctx)
	cancel()
	var models []agentapi.Model
	if err == nil {
		var loadErr error
		if models, loadErr = loadModels(ctx, p); loadErr != nil {
			log.Warn("load web provider models failed", "provider", name, "error", loadErr)
		}
	}
	m.mu.Lock()
	info = m.infos[name]
	info.Available, info.Reason, info.SignedOut = err == nil, "", false
	if err != nil {
		info.Reason, info.SignedOut = unavailableReason(p, err)
	}
	if models != nil {
		info.Models = models
		m.modelsAt[name] = m.now()
	}
	m.infos[name] = info
	m.mu.Unlock()
	m.kickQuota(name)
}

func (m *Manager) kickQuota(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kickQuotaLocked(name)
}

// refuseSignedOut refuses a send to a Task whose provider was found signed
// out, unless it has been signed in since.
func (m *Manager) refuseSignedOut(s *webSession) error {
	m.mu.Lock()
	name := s.provider
	signedOut := m.infos[name].SignedOut
	m.mu.Unlock()
	if !signedOut {
		return nil
	}
	_, err := m.availableProvider(name)
	return err
}

// kickSignedOutLocked checks, after s's turn failed with detail, whether its
// provider is signed out. If so the provider is marked so and the Task's
// failure says to sign in instead of the runtime's own error.
func (m *Manager) kickSignedOutLocked(s *webSession, detail string) {
	p := m.providers[s.provider]
	am, ok := p.(agentapi.AccountManager)
	if !ok || m.closed || !p.Capabilities().Account {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(m.ctx, checkTimeout)
		defer cancel()
		acct, err := am.Account(ctx)
		if err != nil || acct.SignedIn {
			return
		}
		m.applyAccount(ctx, p, acct)
		m.mu.Lock()
		defer m.mu.Unlock()
		if s.removed || s.base != StateFailed || s.detail != detail {
			return
		}
		before := m.summaryLocked(s)
		s.setBase(StateFailed, signedOutReason(p))
		m.changedLocked(s, before)
	}()
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	acct, err := s.m.Account(r.Context(), r.PathValue("provider"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acct)
}

func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	acct, err := s.m.SignIn(r.Context(), r.PathValue("provider"), body.Token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acct)
}

func (s *Server) handleSignOut(w http.ResponseWriter, r *http.Request) {
	acct, err := s.m.SignOut(r.Context(), r.PathValue("provider"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acct)
}

// Device sign-in states, as the UI sees them.
const (
	deviceIdle     = "idle"
	deviceStarting = "starting"
	deviceWaiting  = "waiting"
	deviceSignedIn = "signed_in"
	deviceFailed   = "failed"
	deviceCanceled = "canceled"
)

// deviceCodeWait bounds how long starting a device sign-in waits for its code.
const deviceCodeWait = 30 * time.Second

// DeviceSignIn is a device sign-in's state. URL and Code are set while it
// waits for the person to approve; Account once signed in; Error once failed.
type DeviceSignIn struct {
	State   string            `json:"state"`
	URL     string            `json:"verification_uri,omitempty"`
	Code    string            `json:"user_code,omitempty"`
	Error   string            `json:"error,omitempty"`
	Account *agentapi.Account `json:"account,omitempty"`
}

type deviceSignIn struct {
	DeviceSignIn
	cancel context.CancelFunc
	err    error         // the failure, as the request reports it
	ready  chan struct{} // closed once the code is shown or the sign-in ends
	once   sync.Once
}

func (d *deviceSignIn) live() bool { return d.State == deviceStarting || d.State == deviceWaiting }

type deviceSignIns struct {
	mu sync.Mutex
	by map[string]*deviceSignIn
}

// StartDeviceSignIn starts a device sign-in for the provider, or returns the
// one in progress, once its code is known. It runs on the service's context,
// so it outlives the request and ends with the service or CancelDeviceSignIn.
// A refusal before any code is shown is returned as an error.
func (m *Manager) StartDeviceSignIn(ctx context.Context, name string) (DeviceSignIn, error) {
	p, _, err := m.accountManager(name)
	if err != nil {
		return DeviceSignIn{}, err
	}
	dm, ok := p.(agentapi.DeviceSignInManager)
	if !ok || !p.Capabilities().DeviceSignIn {
		return DeviceSignIn{}, newError(http.StatusNotFound, "%s cannot sign in with a device code", p.DisplayName())
	}
	m.devices.mu.Lock()
	d := m.devices.by[name]
	if d == nil || !d.live() {
		dctx, cancel := context.WithCancel(m.ctx)
		d = &deviceSignIn{DeviceSignIn: DeviceSignIn{State: deviceStarting}, cancel: cancel, ready: make(chan struct{})}
		if m.devices.by == nil {
			m.devices.by = map[string]*deviceSignIn{}
		}
		m.devices.by[name] = d
		m.wg.Add(1)
		go m.runDeviceSignIn(dctx, p, dm, d)
	}
	m.devices.mu.Unlock()
	select {
	case <-d.ready:
	case <-ctx.Done():
		return DeviceSignIn{}, ctx.Err()
	case <-time.After(deviceCodeWait):
	}
	m.devices.mu.Lock()
	defer m.devices.mu.Unlock()
	if d.State == deviceFailed && d.Code == "" && d.err != nil {
		if m.devices.by[name] == d {
			delete(m.devices.by, name)
		}
		return DeviceSignIn{}, d.err
	}
	return d.DeviceSignIn, nil
}

func (m *Manager) runDeviceSignIn(ctx context.Context, p agentapi.Provider, dm agentapi.DeviceSignInManager, d *deviceSignIn) {
	defer m.wg.Done()
	defer d.cancel()
	acct, err := dm.DeviceSignIn(ctx, func(c agentapi.DeviceCode) {
		m.devices.mu.Lock()
		if d.State == deviceStarting {
			d.State, d.URL, d.Code = deviceWaiting, c.URL, c.Code
		}
		m.devices.mu.Unlock()
		d.once.Do(func() { close(d.ready) })
	})
	if err == nil {
		log.Info("web provider signed in with a device code", "provider", p.Name(), "source", acct.Source)
		m.applyAccount(m.ctx, p, acct)
	}
	m.devices.mu.Lock()
	switch {
	case err == nil:
		d.State, d.Account = deviceSignedIn, &acct
	case errors.Is(err, context.Canceled):
		d.State = deviceCanceled
	default:
		d.err = accountError(err)
		_, msg := errorStatus(d.err)
		d.State, d.Error = deviceFailed, msg
	}
	d.URL, d.Code = "", ""
	m.devices.mu.Unlock()
	d.once.Do(func() { close(d.ready) })
}

// DeviceSignIn reports the provider's device sign-in, idle when none started.
func (m *Manager) DeviceSignIn(name string) DeviceSignIn {
	m.devices.mu.Lock()
	defer m.devices.mu.Unlock()
	if d := m.devices.by[name]; d != nil {
		return d.DeviceSignIn
	}
	return DeviceSignIn{State: deviceIdle}
}

// CancelDeviceSignIn stops the provider's device sign-in in progress.
func (m *Manager) CancelDeviceSignIn(name string) {
	m.devices.mu.Lock()
	defer m.devices.mu.Unlock()
	if d := m.devices.by[name]; d != nil && d.live() {
		d.cancel()
	}
}

func (s *Server) handleStartDeviceSignIn(w http.ResponseWriter, r *http.Request) {
	d, err := s.m.StartDeviceSignIn(r.Context(), r.PathValue("provider"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeviceSignIn(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.m.DeviceSignIn(r.PathValue("provider")))
}

func (s *Server) handleCancelDeviceSignIn(w http.ResponseWriter, r *http.Request) {
	s.m.CancelDeviceSignIn(r.PathValue("provider"))
	w.WriteHeader(http.StatusNoContent)
}
