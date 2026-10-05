import assert from 'node:assert/strict';
import test from 'node:test';
import { estimateTurnCost, modelCostLine, priceTier, providerQuota, ringFraction, ringTone, quotaPace, quotaBurn, accountQuota } from '../src/lib/cost.ts';

test('cost is unknown without reported input prices or context', () => {
  assert.equal(estimateTurnCost({}, { used: 31000, limit: 200000 }), null);
  assert.equal(estimateTurnCost({ prices: { input: 2 } }, undefined), null);
  assert.equal(estimateTurnCost({ prices: { output: 10 } }, { used: 31000, limit: 200000 }), null);
});

test('context cost prices cached tokens once and excludes output', () => {
  const model = { prices: { input: 10, cache_read: 2, output: 100, batch_size: 1000 } };
  assert.equal(estimateTurnCost(model, { used: 1000, cached: 750, limit: 2000 }), 4);
  assert.equal(estimateTurnCost(model, { used: 1000, limit: 2000 }), 10);
  assert.equal(estimateTurnCost(model, { used: 1000, cached: 1500, limit: 2000 }), 2);
  assert.equal(estimateTurnCost(model, { used: 1000, cached: -20, limit: 2000 }), 10);
});

test('long-context prices follow selection or reported token count', () => {
  const prices = { input: 2, max_prompt_tokens: 200000, long_context: { input: 4 } };
  assert.equal(priceTier(prices, 200000, 'default'), prices);
  assert.equal(priceTier(prices, 200001, 'default'), prices.long_context);
  assert.equal(priceTier(prices, 1000, 'long_context'), prices.long_context);
  assert.equal(estimateTurnCost({ prices }, { used: 250000, limit: 1000000 }), 1);
});

test('context and quota warning boundaries match their different meanings', () => {
  assert.equal(ringFraction(undefined), 0);
  assert.equal(ringFraction({ used: 300, limit: 200 }), 1);
  assert.equal(ringTone(0.79), 'accent');
  assert.equal(ringTone(0.8), 'attention');
  assert.equal(ringTone(0.95), 'danger');
});

test('quota belongs to the selected provider and prefers a limited allowance', () => {
  const limited = { provider: 'copilot', type: 'premium_interactions', unlimited: false };
  assert.equal(providerQuota([{ provider: 'other', unlimited: false }, { provider: 'copilot', unlimited: true }, limited], 'copilot'), limited);
  assert.equal(providerQuota([limited], 'other'), null);
});

const quota = { provider: 'copilot', type: 'premium_interactions', used: 50, entitlement: 100, unlimited: false, remaining_percent: 50, overage: 0, reset_at: '2026-11-01T00:00:00Z' };

test('quota pace compares average consumption with the daily allowance budget', () => {
  const now = Date.parse('2026-10-16T12:00:00Z');
  const onPace = quotaPace(quota, now);
  assert.equal(onPace.tone, 'healthy');
  assert.equal(onPace.label, 'On pace');
  assert.equal(onPace.daysAtPace, 15.5);
  assert.equal(onPace.daysUntilReset, 15.5);
  assert.deepEqual(quotaPace({ ...quota, reset_at: undefined }, now), onPace);
  for (const [remaining, label, tone] of [[70, 'Under pace', 'healthy'], [52.5, 'On pace', 'healthy'], [47.5, 'On pace', 'healthy'], [45, 'Ahead of pace', 'danger'], [30, 'Ahead of pace', 'danger']]) {
    const pace = quotaPace({ ...quota, remaining_percent: remaining }, now);
    assert.equal(pace.label, label);
    assert.equal(pace.tone, tone);
  }
  // A low percentage can be healthy near reset; a high percentage can be too fast early on.
  assert.equal(quotaPace({ ...quota, remaining_percent: 5 }, Date.parse('2026-10-31T12:00:00Z')).tone, 'healthy');
  assert.equal(quotaPace({ ...quota, remaining_percent: 80 }, Date.parse('2026-10-02T00:00:00Z')).tone, 'danger');
});

test('quota pace handles empty, unlimited, stale, expired and unknown billing periods', () => {
  const now = Date.parse('2026-10-16T12:00:00Z');
  assert.equal(quotaPace(null, now).remaining, null);
  assert.equal(quotaPace({ ...quota, remaining_percent: NaN }, now).remaining, null);
  assert.equal(quotaPace({ ...quota, remaining_percent: 100 }, now).daysAtPace, null);
  assert.equal(quotaPace({ ...quota, remaining_percent: 100 }, now).label, 'Under pace');
  assert.equal(quotaPace({ ...quota, remaining_percent: 100 }, now).tone, 'healthy');
  assert.equal(quotaPace({ ...quota, remaining_percent: 0 }, now).tone, 'danger');
  assert.equal(quotaPace({ ...quota, remaining_percent: -10 }, now).remaining, 0);
  assert.equal(quotaPace({ ...quota, unlimited: true }, now).tone, 'healthy');
  assert.equal(quotaPace(quota, now, true).tone, 'muted');
  assert.equal(quotaPace({ ...quota, reset_at: '2026-10-01T00:00:00Z' }, now).tone, 'muted');
  assert.equal(quotaPace({ ...quota, provider: 'other', reset_at: undefined }, now).daysAtPace, null);
  assert.equal(quotaPace({ ...quota, provider: 'other' }, now).daysAtPace, null);
  assert.equal(accountQuota([{ ...quota, unlimited: true }, quota]), quota);
});

test('the model cost line names its unit and what its discount applies to', () => {
  assert.equal(modelCostLine({ cost_tier: 'high', prices: { input: 10, output: 50 } }), 'High cost · 10 in, 50 out credits per 1M tokens');
  assert.equal(modelCostLine({ discount_percent: 10 }), '10% off usage');
  assert.equal(modelCostLine({}), '');
});

test('the allowance month: used so far, projected to the reset at the average pace, and when it runs out', () => {
  const quota = { provider: 'copilot', type: 'premium_interactions', used: 300, entitlement: 1500, unlimited: false, remaining_percent: 80, overage: 0, reset_at: '2026-11-01T00:00:00Z' };
  // A third through October (31 days): 20% used projects to about 60%.
  const now = Date.parse('2026-10-11T08:00:00Z');
  const burn = quotaBurn(quota, now);
  assert.equal(burn.start, Date.parse('2026-10-01T00:00:00Z'));
  assert.equal(burn.reset, Date.parse('2026-11-01T00:00:00Z'));
  assert.ok(Math.abs(burn.elapsed - 1 / 3) < 0.01);
  assert.equal(burn.used, 0.2);
  assert.ok(Math.abs(burn.projected - 0.6) < 0.02);
  assert.equal(burn.runsOut, null);
  // Spending fast runs out before the reset: half used a fifth of the way in runs out 40% of the way.
  const fast = quotaBurn({ ...quota, remaining_percent: 50 }, Date.parse('2026-10-07T04:48:00Z'));
  assert.ok(fast.projected > 1);
  assert.ok(Math.abs((fast.runsOut - fast.start) / (fast.reset - fast.start) - 0.4) < 0.01);
  // Nothing to draw without a known calendar month.
  assert.equal(quotaBurn({ ...quota, provider: 'claude' }, now), null);
  assert.equal(quotaBurn({ ...quota, unlimited: true }, now), null);
  assert.equal(quotaBurn({ ...quota, reset_at: '2026-10-20T00:00:00Z' }, now), null);
  assert.equal(quotaBurn(null, now), null);
});
