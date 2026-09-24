import assert from 'node:assert/strict';
import test from 'node:test';
import { estimateTurnCost, priceTier, providerQuota, ringFraction, ringTone, creditsTone } from '../src/lib/cost.ts';

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
  assert.equal(creditsTone({ unlimited: false, remaining_percent: 20 }), 'attention');
  assert.equal(creditsTone({ unlimited: false, remaining_percent: 5 }), 'danger');
  assert.equal(creditsTone({ unlimited: true, remaining_percent: 0 }), 'muted');
});

test('quota belongs to the selected provider and prefers a limited allowance', () => {
  const limited = { provider: 'copilot', type: 'premium_interactions', unlimited: false };
  assert.equal(providerQuota([{ provider: 'other', unlimited: false }, { provider: 'copilot', unlimited: true }, limited], 'copilot'), limited);
  assert.equal(providerQuota([limited], 'other'), null);
});
