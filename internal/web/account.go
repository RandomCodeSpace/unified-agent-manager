package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// codeSignedOut marks a refusal because the provider's runtime is signed out.
const codeSignedOut = "provider_signed_out"

// codeAccountNotLinked marks a refusal because the provider's runtime is, or
// would be, signed in as another account than the one this server is linked to.
const codeAccountNotLinked = "account_not_linked"

// accountCheckEvery is how long an account read stays current for the
// checks before a Task is created or a message sent.
const accountCheckEvery = 15 * time.Second

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
	if err := m.refuseOtherAccount(ctx, p, acct); err != nil {
		return agentapi.Account{}, err
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
	var webErr *Error
	if errors.As(err, &webErr) {
		return err
	}
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
	if acct.SignedIn && acct.Login != "" {
		link, linked := m.accountLink(name)
		switch {
		case !linked:
			m.linkAccount(name, acct)
		case !sameAccount(link, acct):
			acct = m.revertAccount(ctx, p, link, acct)
			if acct.SignedIn && !sameAccount(link, acct) {
				m.mu.Lock()
				info := m.infos[name]
				info.Available, info.SignedOut, info.AccountMismatch = false, false, true
				info.Reason = mismatchReason(p, acct, link)
				m.infos[name] = info
				m.mu.Unlock()
				return
			}
		}
	}
	m.mu.Lock()
	info := m.infos[name]
	info.AccountMismatch = false
	if !acct.SignedIn {
		info.Available, info.SignedOut, info.Reason = false, true, signedOutReason(p)
		m.infos[name] = info
		m.mu.Unlock()
		m.kickQuota(name)
		return
	}
	wasOut := info.SignedOut || !info.Available
	m.infos[name] = info
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
// out, unless it has been signed in since, or whose runtime is signed in as
// another account than the linked one.
func (m *Manager) refuseSignedOut(s *webSession) error {
	m.mu.Lock()
	name := s.provider
	info := m.infos[name]
	m.mu.Unlock()
	if info.SignedOut {
		_, err := m.availableProvider(name)
		return err
	}
	return m.verifyAccount(name, info.AccountMismatch)
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
	name := r.PathValue("provider")
	acct, err := s.m.Account(r.Context(), name)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.m.accountView(name, acct))
}

func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	name := r.PathValue("provider")
	acct, err := s.m.SignIn(r.Context(), name, body.Token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.m.accountView(name, acct))
}

func (s *Server) handleSignOut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	acct, err := s.m.SignOut(r.Context(), name)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.m.accountView(name, acct))
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
	State   string       `json:"state"`
	URL     string       `json:"verification_uri,omitempty"`
	Code    string       `json:"user_code,omitempty"`
	Error   string       `json:"error,omitempty"`
	Account *AccountView `json:"account,omitempty"`
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
		err = m.refuseOtherAccount(m.ctx, p, acct)
	}
	if err == nil {
		log.Info("web provider signed in with a device code", "provider", p.Name(), "source", acct.Source)
		m.applyAccount(m.ctx, p, acct)
	}
	m.devices.mu.Lock()
	switch {
	case err == nil:
		view := m.accountView(p.Name(), acct)
		d.State, d.Account = deviceSignedIn, &view
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

// AccountView is a provider's sign-in with the account this server is linked
// to, as the API reports it.
type AccountView struct {
	agentapi.Account
	Linked *store.AccountLink `json:"linked,omitempty"`
}

func (m *Manager) accountView(name string, acct agentapi.Account) AccountView {
	v := AccountView{Account: acct}
	if link, ok := m.accountLink(name); ok {
		v.Linked = &link
	}
	return v
}

func (m *Manager) accountLink(name string) (store.AccountLink, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	link, ok := m.links[name]
	return link, ok
}

// sameAccount reports whether acct is signed in as link's account.
func sameAccount(link store.AccountLink, acct agentapi.Account) bool {
	host := func(h string) string {
		return strings.TrimSuffix(strings.ToLower(cmp.Or(h, "https://github.com")), "/")
	}
	return strings.EqualFold(link.Login, acct.Login) && host(link.Host) == host(acct.Host)
}

// linkAccount links the provider to acct's account when it has none: the
// first sign-in, or the sign-in found on a server that had none linked.
func (m *Manager) linkAccount(name string, acct agentapi.Account) {
	link := store.AccountLink{Login: acct.Login, Host: acct.Host, LinkedAt: m.now()}
	err := m.store.Update(func(cfg *store.Config) error {
		if _, ok := cfg.WebAccountLinks[name]; ok {
			return nil
		}
		if cfg.WebAccountLinks == nil {
			cfg.WebAccountLinks = map[string]store.AccountLink{}
		}
		cfg.WebAccountLinks[name] = link
		return nil
	})
	if err != nil {
		log.Warn("link web provider account failed", "provider", name, "error", err)
		return
	}
	m.mu.Lock()
	if _, ok := m.links[name]; !ok {
		m.links[name] = link
	}
	m.mu.Unlock()
	log.Info("web provider account linked", "provider", name, "login", acct.Login)
}

// revertAccount signs out a sign-in the runtime stored for another account
// than link's, such as a `copilot login` run in a terminal, and returns the
// account then in effect. A token in the service environment or another
// tool's sign-in cannot be signed out here; it is returned unchanged.
func (m *Manager) revertAccount(ctx context.Context, p agentapi.Provider, link store.AccountLink, acct agentapi.Account) agentapi.Account {
	am, ok := p.(agentapi.AccountManager)
	if !ok || acct.Source != agentapi.AccountStored {
		return acct
	}
	out, err := am.SignOut(ctx)
	if err != nil {
		log.Warn("sign out an account other than the linked one failed", "provider", p.Name(), "error", err)
		return acct
	}
	log.Warn("signed out an account other than the linked one", "provider", p.Name(), "login", acct.Login, "linked", link.Login)
	return out
}

// refuseOtherAccount refuses a sign-in as another account than the linked
// one. The sign-in is undone first, so it never takes effect.
func (m *Manager) refuseOtherAccount(ctx context.Context, p agentapi.Provider, acct agentapi.Account) error {
	link, linked := m.accountLink(p.Name())
	if !linked || !acct.SignedIn || acct.Login == "" || sameAccount(link, acct) {
		return nil
	}
	log.Warn("refused a sign-in as another account than the linked one", "provider", p.Name(), "login", acct.Login, "linked", link.Login)
	m.applyAccount(ctx, p, acct)
	return &Error{Status: http.StatusConflict, Code: codeAccountNotLinked, Message: fmt.Sprintf("This server is linked to %s account %s. Sign in with that account.", p.DisplayName(), link.Login)}
}

func mismatchReason(p agentapi.Provider, acct agentapi.Account, link store.AccountLink) string {
	return fmt.Sprintf("%s is signed in as %s, but this server is linked to %s. Sign in as %s, or unlink the account in Settings.", p.DisplayName(), acct.Login, link.Login, link.Login)
}

// verifyAccount reads the provider's account, unless read in the last
// accountCheckEvery and not forced, and brings availability in line with it
// and the link. It refuses while the runtime is signed in as another account
// or signed out. A provider without accounts, or an account that cannot be
// read, is left to the calls that need it.
func (m *Manager) verifyAccount(name string, force bool) error {
	m.mu.Lock()
	p := m.providers[name]
	fresh := !force && m.now().Sub(m.accountAt[name]) < accountCheckEvery
	m.mu.Unlock()
	am, ok := p.(agentapi.AccountManager)
	if !ok || !p.Capabilities().Account {
		return nil
	}
	if !fresh {
		ctx, cancel := context.WithTimeout(m.ctx, checkTimeout)
		acct, err := am.Account(ctx)
		cancel()
		if err != nil {
			return nil
		}
		m.applyAccount(m.ctx, p, acct)
		m.mu.Lock()
		m.accountAt[name] = m.now()
		m.mu.Unlock()
	}
	m.mu.Lock()
	info := m.infos[name]
	m.mu.Unlock()
	switch {
	case info.AccountMismatch:
		return &Error{Status: http.StatusConflict, Code: codeAccountNotLinked, Message: info.Reason}
	case info.SignedOut:
		return &Error{Status: http.StatusConflict, Code: codeSignedOut, Message: info.Reason}
	}
	return nil
}

// UnlinkAccount clears the provider's linked account and signs out a sign-in
// the runtime stored, so the next sign-in links its account. A token in the
// service environment or another tool's sign-in stays, and is linked again.
func (m *Manager) UnlinkAccount(ctx context.Context, name string) (agentapi.Account, error) {
	p, am, err := m.accountManager(name)
	if err != nil {
		return agentapi.Account{}, err
	}
	err = m.store.Update(func(cfg *store.Config) error {
		delete(cfg.WebAccountLinks, name)
		if len(cfg.WebAccountLinks) == 0 {
			cfg.WebAccountLinks = nil
		}
		return nil
	})
	if err != nil {
		return agentapi.Account{}, fmt.Errorf("unlink account: %w", err)
	}
	m.mu.Lock()
	delete(m.links, name)
	delete(m.accountAt, name)
	m.mu.Unlock()
	log.Info("web provider account unlinked", "provider", name)
	acct, err := am.Account(ctx)
	if err != nil {
		return agentapi.Account{}, accountError(err)
	}
	if acct.SignedIn && acct.Source == agentapi.AccountStored {
		if acct, err = am.SignOut(ctx); err != nil {
			return agentapi.Account{}, accountError(err)
		}
	}
	m.applyAccount(ctx, p, acct)
	return acct, nil
}

func (s *Server) handleUnlinkAccount(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	acct, err := s.m.UnlinkAccount(r.Context(), name)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.m.accountView(name, acct))
}
