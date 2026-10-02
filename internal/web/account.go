package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

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
