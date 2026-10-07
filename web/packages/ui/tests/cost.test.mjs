import assert from 'node:assert/strict';
import test from 'node:test';
import { estimateTurnCost, modelCostLine, priceTier, providerQuota, ringFraction, ringTone, quotaPace, quotaBurn, accountQuota } from '../src/lib/cost.ts';

// Pace counts working days in the browser's time zone; the expectations below are in UTC unless a test picks a zone.
process.env.TZ = 'UTC';
function inZone(zone, run) {
  process.env.TZ = zone;
  try { return run(); } finally { process.env.TZ = 'UTC'; }
}
const near = (actual, expected, epsilon = 1e-9) => assert.ok(Math.abs(actual - expected) < epsilon, `${actual} is not ${expected}`);

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

// October 2026 starts on a Thursday and has 22 working days; the 31st is a Saturday.
test('quota pace compares average use with the budget per working day', () => {
  // Friday the 16th begins with 11 of the 22 working days gone, though only 15 of 31 calendar days.
  const now = Date.parse('2026-10-16T00:00:00Z');
  const onPace = quotaPace(quota, now);
  assert.equal(onPace.tone, 'healthy');
  assert.equal(onPace.label, 'On pace');
  assert.equal(onPace.daysAtPace, 11);
  // The reset stays a calendar countdown: it names a date.
  assert.equal(onPace.daysUntilReset, 16);
  assert.deepEqual(quotaPace({ ...quota, reset_at: undefined }, now), onPace);
  for (const [remaining, label, tone] of [[70, 'Under pace', 'healthy'], [52.5, 'On pace', 'healthy'], [47.5, 'On pace', 'healthy'], [45, 'Ahead of pace', 'danger'], [30, 'Ahead of pace', 'danger']]) {
    const pace = quotaPace({ ...quota, remaining_percent: remaining }, now);
    assert.equal(pace.label, label);
    assert.equal(pace.tone, tone);
  }
  // The current working day counts by the share of it gone.
  const noon = quotaPace(quota, Date.parse('2026-10-16T12:00:00Z'));
  assert.equal(noon.daysAtPace, 11.5);
  assert.equal(noon.daysUntilReset, 15.5);
  // A low percentage can be healthy near reset; a high percentage can be too fast early on.
  assert.equal(quotaPace({ ...quota, remaining_percent: 5 }, Date.parse('2026-10-31T12:00:00Z')).tone, 'healthy');
  assert.equal(quotaPace({ ...quota, remaining_percent: 80 }, Date.parse('2026-10-02T00:00:00Z')).tone, 'danger');
});

test('a weekend adds no time: the pace holds from Friday\'s end to Monday', () => {
  // 10% used over Thursday and Friday (2 of 22 working days) is ahead, though under the calendar budget (3.5 of 31 days on Sunday).
  const weekend = { ...quota, remaining_percent: 90 };
  for (const at of ['2026-10-03T00:00:00Z', '2026-10-04T12:00:00Z', '2026-10-05T00:00:00Z']) {
    const pace = quotaPace(weekend, Date.parse(at));
    assert.equal(pace.label, 'Ahead of pace', at);
    assert.equal(pace.daysAtPace, 18, at);
  }
  // Use made on the weekend still counts as used.
  near(quotaPace({ ...quota, remaining_percent: 85 }, Date.parse('2026-10-04T12:00:00Z')).daysAtPace, 85 * 2 / 15);
});

test('a month that starts on a weekend makes no pace claim before its first working day', () => {
  // November 2026 starts on a Sunday and has 21 working days.
  const november = { ...quota, remaining_percent: 95, reset_at: '2026-12-01T00:00:00Z' };
  const sunday = Date.parse('2026-11-01T12:00:00Z');
  for (const remaining of [95, 100]) {
    const pace = quotaPace({ ...november, remaining_percent: remaining }, sunday);
    assert.equal(pace.label, 'Usage pace unavailable');
    assert.equal(pace.tone, 'muted');
    assert.equal(pace.daysAtPace, null);
    assert.equal(pace.remaining, remaining);
    assert.equal(pace.daysUntilReset, 29.5);
  }
  assert.equal(quotaBurn(november, sunday), null);
  assert.equal(quotaPace(november, Date.parse('2026-11-02T00:00:00Z')).label, 'Usage pace unavailable');
  // Monday noon: half of the first working day.
  const monday = Date.parse('2026-11-02T12:00:00Z');
  const pace = quotaPace({ ...november, remaining_percent: 99 }, monday);
  assert.equal(pace.label, 'Under pace');
  assert.equal(pace.daysAtPace, 49.5);
  near(quotaBurn(november, monday).elapsed, 0.5 / 21);
});

test('working days follow local calendar days across daylight saving changes', () => {
  inZone('America/New_York', () => {
    // Clocks go back on Sunday 1 November. The UTC month runs from Saturday 31 October 20:00 EDT to Monday 30
    // November 19:00 EST: 20 working days and 19 hours of the 30th. Monday the 2nd starts at 05:00 UTC, not 04:00.
    const november = { ...quota, reset_at: '2026-12-01T00:00:00Z' };
    const total = 20 + 19 / 24;
    assert.equal(quotaBurn(november, Date.parse('2026-11-02T05:00:00Z')), null);
    near(quotaBurn(november, Date.parse('2026-11-02T17:00:00Z')).elapsed, 0.5 / total);
    near(quotaBurn(november, Date.parse('2026-11-30T12:00:00Z')).elapsed, (20 + 7 / 24) / total);
  });
  inZone('Asia/Jerusalem', () => {
    // Clocks go forward on Friday 27 March 2026, a 23-hour working day. The UTC month runs from Sunday 1 March 02:00
    // to Wednesday 1 April 03:00 local: 22 working days and 3 hours of the 1st.
    const march = { ...quota, reset_at: '2026-04-01T00:00:00Z' };
    // 09:00 UTC is 11 of Friday's 23 hours.
    near(quotaBurn(march, Date.parse('2026-03-27T09:00:00Z')).elapsed, (19 + 11 / 23) / (22 + 3 / 24));
  });
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
  // Sunday the 11th: 7 of October's 22 working days, as at Friday's end; 20% used projects to 20% × 22 / 7.
  const now = Date.parse('2026-10-11T08:00:00Z');
  const burn = quotaBurn(quota, now);
  assert.equal(burn.start, Date.parse('2026-10-01T00:00:00Z'));
  assert.equal(burn.reset, Date.parse('2026-11-01T00:00:00Z'));
  near(burn.elapsed, 7 / 22);
  assert.equal(burn.used, 0.2);
  near(burn.projected, 0.2 * 22 / 7);
  assert.equal(burn.runsOut, null);
  assert.deepEqual(quotaBurn(quota, Date.parse('2026-10-10T00:00:00Z')), burn);
  // Spending fast runs out before the reset, on a working day: 20% used by Friday noon (1.5 working days) lasts
  // 7.5 working days, to noon on Monday the 12th, past the weekend.
  const fast = quotaBurn(quota, Date.parse('2026-10-02T12:00:00Z'));
  near(fast.projected, 0.2 * 22 / 1.5);
  near(fast.runsOut, Date.parse('2026-10-12T12:00:00Z'), 1000);
  // In New York the month starts at 20:00 EDT on Wednesday 30 September, so by Friday noon 1⅔ working days are
  // gone and 20% lasts 8⅓: to 04:00 local on Tuesday the 13th.
  inZone('America/New_York', () => {
    near(quotaBurn(quota, Date.parse('2026-10-02T16:00:00Z')).runsOut, Date.parse('2026-10-13T08:00:00Z'), 1000);
  });
  // Nothing to draw without a known calendar month.
  assert.equal(quotaBurn({ ...quota, provider: 'claude' }, now), null);
  assert.equal(quotaBurn({ ...quota, unlimited: true }, now), null);
  assert.equal(quotaBurn({ ...quota, reset_at: '2026-10-20T00:00:00Z' }, now), null);
  assert.equal(quotaBurn(null, now), null);
});
