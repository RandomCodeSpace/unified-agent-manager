package copilot

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Quota reads the account's quotas through account.getQuota. The CLI answers
// that from the copilot user it read at sign-in, so a type a model call has
// since reported takes that call's snapshot. An entitlement of -1 means
// unlimited, as does the unlimited flag.
func (p *webProvider) Quota(ctx context.Context) ([]agentapi.Quota, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	snaps, err := client.Quota(ctx)
	if err != nil {
		p.poke()
		return nil, fmt.Errorf("read copilot quota: %s", errText(err))
	}
	p.noteAccount(ctx, client)
	p.quotaMu.Lock()
	defer p.quotaMu.Unlock()
	out := make([]agentapi.Quota, 0, len(snaps))
	for kind, q := range snaps {
		if l, ok := p.live[kind]; ok {
			q = rpc.AccountQuotaSnapshot{
				EntitlementRequests: l.EntitlementRequests, IsUnlimitedEntitlement: l.IsUnlimitedEntitlement, Overage: l.Overage,
				RemainingPercentage: l.RemainingPercentage, ResetDate: l.ResetDate, UsedRequests: l.UsedRequests,
			}
		}
		quota := agentapi.Quota{
			Type: kind, Used: q.UsedRequests, Entitlement: q.EntitlementRequests, Unlimited: q.IsUnlimitedEntitlement || q.EntitlementRequests < 0,
			RemainingPercent: q.RemainingPercentage, Overage: q.Overage,
		}
		if quota.Unlimited {
			quota.Entitlement = 0
		}
		if q.ResetDate != nil {
			quota.ResetAt = *q.ResetDate
		}
		if credits, ok := p.credits[kind]; ok {
			quota.Unit = agentapi.QuotaUnitRequests
			if credits {
				quota.Unit = agentapi.QuotaUnitCredits
			}
		}
		quota.Plan = p.plan
		out = append(out, quota)
	}
	slices.SortFunc(out, func(a, b agentapi.Quota) int { return strings.Compare(a.Type, b.Type) })
	return out, nil
}

// noteQuota keeps the quota snapshots a model call's response carried.
func (p *webProvider) noteQuota(snaps map[string]rpc.AssistantUsageQuotaSnapshot) {
	if len(snaps) == 0 {
		return
	}
	p.quotaMu.Lock()
	defer p.quotaMu.Unlock()
	if p.live == nil {
		p.live = make(map[string]rpc.AssistantUsageQuotaSnapshot, len(snaps))
	}
	maps.Copy(p.live, snaps)
	for kind, q := range snaps {
		p.noteCreditsLocked(kind, q.TokenBasedBilling)
	}
}

// quotaSession is a session that reads its quota and account projection;
// only the SDK adapter has it.
type quotaSession interface {
	QuotaInfo(ctx context.Context) (*rpc.SessionQuotaGetResult, error)
}

// noteAccount keeps the plan tier and the billing of each quota type from an
// open conversation of client, when it has one: account.getQuota reports
// neither, and session.quota.get needs a session. A failed read keeps what
// was known.
func (p *webProvider) noteAccount(ctx context.Context, client sdkClient) {
	p.mu.Lock()
	convs := slices.Collect(maps.Keys(p.convs))
	p.mu.Unlock()
	for _, c := range convs {
		c.mu.Lock()
		qs, ok := c.sess.(quotaSession)
		c.mu.Unlock()
		if !ok || c.client != client {
			continue
		}
		res, err := qs.QuotaInfo(ctx)
		if err != nil || res == nil {
			log.Debug("read copilot session quota failed", "error", err)
			return
		}
		p.quotaMu.Lock()
		defer p.quotaMu.Unlock()
		if res.PlanTier != "" && res.PlanTier != rpc.SessionQuotaPlanTierUnknown {
			p.plan = string(res.PlanTier)
		}
		for kind, q := range res.Snapshots {
			p.noteCreditsLocked(kind, q.TokenBasedBilling)
		}
		return
	}
}

// noteCreditsLocked keeps whether quota type kind is billed by tokens, when
// the CLI said.
func (p *webProvider) noteCreditsLocked(kind string, tokenBased *bool) {
	if tokenBased == nil {
		return
	}
	if p.credits == nil {
		p.credits = map[string]bool{}
	}
	p.credits[kind] = *tokenBased
}
