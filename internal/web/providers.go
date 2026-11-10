package web

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

func (m *Manager) checkProviders(ctx context.Context) map[string]ProviderInfo {
	infos := make(map[string]ProviderInfo, len(m.order))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range m.order {
		p := m.providers[name]
		wg.Add(1)
		go func() {
			defer log.Recover("provider check", &m.mu)
			defer wg.Done()
			info := ProviderInfo{Name: p.Name(), DisplayName: p.DisplayName(), Capabilities: p.Capabilities(), Available: true, Models: []agentapi.Model{}}
			checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
			err := p.Check(checkCtx)
			cancel()
			if err != nil {
				info.Available = false
				info.Reason, info.SignedOut = unavailableReason(p, err)
				log.Warn("web provider unavailable", "provider", name, "error", err)
			} else if models, err := loadModels(ctx, p); err != nil {
				log.Warn("load web provider models failed", "provider", name, "error", err)
			} else {
				info.Models = models
			}
			info.Capabilities = p.Capabilities()
			mu.Lock()
			infos[name] = info
			mu.Unlock()
		}()
	}
	wg.Wait()
	return infos
}

// loadModels reads p's selectable models. ErrUnsupported means only the
// provider default is offered.
func loadModels(ctx context.Context, p agentapi.Provider) ([]agentapi.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	models, err := p.Models(ctx)
	if errors.Is(err, agentapi.ErrUnsupported) {
		return []agentapi.Model{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]agentapi.Model, 0, len(models))
	seen := map[string]bool{}
	for _, mo := range models {
		if mo.ID == "" || seen[mo.ID] {
			continue
		}
		seen[mo.ID] = true
		name := clipRunes(strings.TrimSpace(displaytext.Sanitize(mo.Name)), maxNameRunes)
		mo.Name = cmp.Or(name, mo.ID)
		mo.Efforts = append([]string{}, mo.Efforts...)
		mo.ContextSizes = append([]agentapi.ContextSize{}, mo.ContextSizes...)
		if !validCostTier(mo.CostTier) {
			mo.CostTier = ""
		}
		if mo.DiscountPercent < 0 || mo.DiscountPercent > 100 {
			mo.DiscountPercent = 0
		}
		mo.Prices = cleanPrices(mo.Prices)
		if mo.Media != nil {
			media := *mo.Media
			media.Types = slices.Clone(media.Types)
			mo.Media = &media
		}
		out = append(out, mo)
	}
	return out, nil
}

// RefreshModels reloads the catalog of every available provider whose copy
// is older than modelsMaxAge. It waits only for providers with no models
// yet: one that has some keeps serving them while it reloads. The returned
// channel is closed once every reload it started has finished. A failed load
// keeps the previous catalog and is retried after modelsMaxAge.
func (m *Manager) RefreshModels() <-chan struct{} {
	var all, empty sync.WaitGroup
	m.mu.Lock()
	for _, name := range m.order {
		info := m.infos[name]
		if m.closed || !info.Available || m.fetching[name] || m.now().Sub(m.modelsAt[name]) < modelsMaxAge {
			continue
		}
		m.fetching[name] = true
		p, wait := m.providers[name], len(info.Models) == 0
		m.wg.Add(1)
		all.Add(1)
		if wait {
			empty.Add(1)
		}
		go func() {
			defer log.Recover("model load", &m.mu)
			defer m.wg.Done()
			defer all.Done()
			if wait {
				defer empty.Done()
			}
			models, err := loadModels(m.ctx, p)
			m.mu.Lock()
			defer m.mu.Unlock()
			delete(m.fetching, p.Name())
			m.modelsAt[p.Name()] = m.now()
			if err != nil {
				log.Warn("refresh web provider models failed", "provider", p.Name(), "error", err)
				return
			}
			info := m.infos[p.Name()]
			info.Models = models
			m.infos[p.Name()] = info
		}()
	}
	m.mu.Unlock()
	empty.Wait()
	done := make(chan struct{})
	go func() {
		all.Wait()
		close(done)
	}()
	return done
}

// Providers lists every provider with its availability.
func (m *Manager) Providers() []ProviderInfo {
	updates := m.cliUpdatesAvailable()
	for _, name := range m.order {
		m.syncAssisted(name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ProviderInfo, 0, len(m.order))
	for _, name := range m.order {
		info := m.infos[name]
		info.CheapestModel = m.cheapestModelLocked(name)
		info.CLIUpdate = updates[name]
		out = append(out, info)
	}
	return out
}

func (m *Manager) availableProvider(name string) (agentapi.Provider, error) {
	m.mu.Lock()
	prov := m.providers[name]
	info := m.infos[name]
	m.mu.Unlock()
	if prov == nil {
		return nil, newError(http.StatusBadRequest, msgUnknownProvider, name)
	}
	if info.Available || info.AccountMismatch {
		// The runtime may have been signed in to another account since.
		if err := m.verifyAccount(name, info.AccountMismatch); err != nil {
			return nil, err
		}
		return prov, nil
	}
	// Re-check so installing or signing in to a provider does not need a
	// service restart.
	ctx, cancel := context.WithTimeout(m.ctx, checkTimeout)
	err := prov.Check(ctx)
	cancel()
	m.mu.Lock()
	info.Available = err == nil
	info.Reason, info.SignedOut = "", false
	if err != nil {
		info.Reason, info.SignedOut = unavailableReason(prov, err)
	}
	m.infos[name] = info
	m.mu.Unlock()
	if info.SignedOut {
		return nil, &Error{Status: http.StatusConflict, Code: codeSignedOut, Message: info.Reason}
	}
	if err != nil {
		return nil, newError(http.StatusConflict, "%s is unavailable: %s", prov.DisplayName(), info.Reason)
	}
	if err := m.verifyAccount(name, true); err != nil {
		return nil, err
	}
	return prov, nil
}
