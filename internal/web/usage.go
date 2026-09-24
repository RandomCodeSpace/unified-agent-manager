package web

import (
	"context"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const (
	// quotaMaxAge is how often, at most, a connected browser makes the
	// service read account quotas again.
	quotaMaxAge = time.Minute
	// quotaCheck is how often the usage loop checks whether a read is due.
	quotaCheck = 5 * time.Second
)

// quotaCache is one usage provider's last good quotas and when they were
// read; stale is set while its latest read failed.
type quotaCache struct {
	quotas []Quota
	at     time.Time
	stale  bool
}

// usageProviderLocked reports whether an available provider has the usage
// capability and reports quotas.
func (m *Manager) usageProviderLocked() bool {
	return slices.ContainsFunc(m.order, func(name string) bool { return m.quotaReporterLocked(name) != nil })
}

func (m *Manager) quotaReporterLocked(name string) agentapi.QuotaReporter {
	if info := m.infos[name]; !info.Available || !info.Capabilities.Usage {
		return nil
	}
	qr, _ := m.providers[name].(agentapi.QuotaReporter)
	return qr
}

// usageLoop reads account quotas at start, after each turn of a usage
// provider ends, and every quotaMaxAge at most while a browser is connected.
func (m *Manager) usageLoop() {
	defer m.wg.Done()
	m.refreshQuota()
	t := time.NewTicker(m.quotaTick)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.quotaKick:
			m.refreshQuota()
		case <-t.C:
			m.pollQuota()
		}
	}
}

// pollQuota reads the quotas when a browser is connected and the last read
// began quotaMaxAge ago or more.
func (m *Manager) pollQuota() {
	m.mu.Lock()
	due := len(m.subs) > 0 && m.now().Sub(m.quotaPolled) >= quotaMaxAge
	m.mu.Unlock()
	if due {
		m.refreshQuota()
	}
}

// kickQuotaLocked asks the usage loop for a read after a turn of provider
// ended. Kicks during a read coalesce into one more read.
func (m *Manager) kickQuotaLocked(provider string) {
	if m.quotaReporterLocked(provider) == nil {
		return
	}
	select {
	case m.quotaKick <- struct{}{}:
	default:
	}
}

// refreshQuota reads the quotas of every usage provider at once and sends a
// usage frame when what browsers see changed. A failed read keeps that
// provider's last quotas and marks them stale. Only the usage loop calls it,
// so reads never overlap.
func (m *Manager) refreshQuota() {
	m.mu.Lock()
	m.quotaPolled = m.now()
	reporters := map[string]agentapi.QuotaReporter{}
	for _, name := range m.order {
		if qr := m.quotaReporterLocked(name); qr != nil {
			reporters[name] = qr
		}
	}
	m.mu.Unlock()
	type result struct {
		quotas []Quota
		err    error
	}
	results := make(map[string]result, len(reporters))
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for name, qr := range reporters {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(m.ctx, checkTimeout)
			defer cancel()
			got, err := qr.Quota(ctx)
			var quotas []Quota
			for _, q := range got {
				if clean, ok := cleanQuota(name, q); ok {
					quotas = append(quotas, clean)
				}
			}
			slices.SortFunc(quotas, func(a, b Quota) int { return strings.Compare(a.Type, b.Type) })
			mu.Lock()
			results[name] = result{quotas: quotas, err: err}
			mu.Unlock()
		})
	}
	wg.Wait()
	if m.ctx.Err() != nil {
		return // shutting down; the failure says nothing about the account
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	before := m.accountUsageLocked()
	for name, r := range results {
		c := m.quota[name]
		if c == nil {
			c = &quotaCache{}
			m.quota[name] = c
		}
		if r.err != nil {
			log.Warn("read web provider quota failed", "provider", name, "error", r.err)
			c.stale = true
			continue
		}
		*c = quotaCache{quotas: r.quotas, at: m.now()}
	}
	after := m.accountUsageLocked()
	if after.Stale != before.Stale || !slices.EqualFunc(after.Quotas, before.Quotas, sameQuota) {
		m.broadcastLocked("usage", "", func(seq uint64) any { return usageEvent{Seq: seq, Usage: after} })
	}
}

// AccountUsage returns the cached account quotas.
func (m *Manager) AccountUsage() AccountUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.accountUsageLocked()
}

func (m *Manager) accountUsageLocked() AccountUsage {
	u := AccountUsage{Quotas: []Quota{}}
	now := m.now()
	for _, name := range m.order {
		c := m.quota[name]
		if c == nil {
			continue
		}
		for _, q := range c.quotas {
			if !q.ResetAt.After(now) {
				q.ResetAt = time.Time{}
			}
			u.Quotas = append(u.Quotas, q)
		}
		u.Stale = u.Stale || c.stale
		if !c.at.IsZero() && (u.UpdatedAt.IsZero() || c.at.Before(u.UpdatedAt)) {
			u.UpdatedAt = c.at
		}
	}
	return u
}

func sameQuota(a, b Quota) bool {
	resetA, resetB := a.ResetAt, b.ResetAt
	a.ResetAt, b.ResetAt = time.Time{}, time.Time{}
	return a == b && resetA.Equal(resetB)
}

// cleanQuota makes a provider's quota safe to show and to encode: a
// sanitized type, no negative counts, and finite percentages within 0-100.
func cleanQuota(provider string, q agentapi.Quota) (Quota, bool) {
	kind := clipRunes(strings.TrimSpace(displaytext.Sanitize(q.Type)), maxNameRunes)
	if kind == "" {
		return Quota{}, false
	}
	out := Quota{
		Provider: provider, Type: kind, Used: max(q.Used, 0), Entitlement: max(q.Entitlement, 0), Unlimited: q.Unlimited,
		RemainingPercent: min(max(finite(q.RemainingPercent), 0), 100), Overage: max(finite(q.Overage), 0), ResetAt: q.ResetAt,
	}
	if out.Unlimited {
		out.Entitlement = 0
	}
	return out, true
}

// finite returns v, or 0 when it is NaN or infinite, which JSON cannot carry.
func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// validUsage reports whether a provider's per-conversation usage can be
// shown.
func validUsage(u *agentapi.Usage) bool {
	return u != nil && u.AIUnits >= 0 && !math.IsInf(u.AIUnits, 0)
}

// applyUsageLocked records the conversation total u for s when its provider
// has the usage capability.
func (m *Manager) applyUsageLocked(s *webSession, u *agentapi.Usage) {
	if m.infos[s.provider].Capabilities.Usage && validUsage(u) && (s.usage == nil || *s.usage != *u) {
		usage := *u
		s.usage = &usage
	}
}

// cleanPrices copies a model's prices, dropping any that is negative or not
// finite, and returns nil when none is left.
func cleanPrices(p *agentapi.Prices) *agentapi.Prices {
	if p == nil {
		return nil
	}
	out := &agentapi.Prices{BatchSize: max(p.BatchSize, 0), TierPrices: cleanTier(p.TierPrices)}
	if p.LongContext != nil {
		if long := cleanTier(*p.LongContext); long != (agentapi.TierPrices{}) {
			out.LongContext = &long
		}
	}
	if out.TierPrices == (agentapi.TierPrices{}) && out.LongContext == nil {
		return nil
	}
	return out
}

func cleanTier(t agentapi.TierPrices) agentapi.TierPrices {
	price := func(v *float64) *float64 {
		if v == nil || *v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0) {
			return nil
		}
		c := *v
		return &c
	}
	return agentapi.TierPrices{
		Input: price(t.Input), Output: price(t.Output), CacheRead: price(t.CacheRead), CacheWrite: price(t.CacheWrite),
		MaxPromptTokens: max(t.MaxPromptTokens, 0),
	}
}

// validCostTier reports whether tier is one of agentapi's cost tiers.
func validCostTier(tier string) bool {
	switch tier {
	case agentapi.CostLow, agentapi.CostMedium, agentapi.CostHigh, agentapi.CostVeryHigh:
		return true
	}
	return false
}
