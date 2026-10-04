import assert from 'node:assert/strict';
import test from 'node:test';
import { aggregateUsageModels, cacheHitRate, estimateWithoutCache, groupUsageModels, splitTokens } from '../src/lib/token-usage.ts';

function row(values = {}) {
  return { provider: 'copilot', model: 'model-a', input: 100, output: 20, cache_read: 30, cache_write: 10, total: 120, cost_usd: 1, ...values };
}

function catalog(...models) {
  return { commit: 'test', models: models.map((values) => ({ provider: 'copilot', model: 'model-a', rates: { input: 2, output: 10 }, source: 'manual', ...values })) };
}

test('exact model IDs combine across tools and sort by source totals with model ties', () => {
  const rows = [
    row({ model: 'model-b', total: 300 }),
    row({ provider: 'codex', total: 100 }),
    row({ total: 100 }),
    row({ total: 100 }),
    row({ model: 'provider/model-a', total: 10 }),
  ];
  const original = structuredClone(rows);
  const models = groupUsageModels(rows);
  assert.deepEqual(models.map((model) => model.model), ['model-a', 'model-b', 'provider/model-a']);
  assert.deepEqual(models[0], {
    model: 'model-a', input: 300, output: 60, cache_read: 90, cache_write: 30, total: 300, cost_usd: 3, cost_partial: false,
    tools: [
      { provider: 'copilot', input: 200, output: 40, cache_read: 60, cache_write: 20, total: 200, cost_usd: 2, cost_partial: false },
      { provider: 'codex', input: 100, output: 20, cache_read: 30, cache_write: 10, total: 100, cost_usd: 1, cost_partial: false },
    ],
  });
  assert.deepEqual(rows, original);
});

test('group costs distinguish unknown, zero, and partially priced usage', () => {
  const models = groupUsageModels([
    row({ cost_usd: 0 }),
    row({ provider: 'codex', cost_usd: null }),
    row({ model: 'unknown', cost_usd: null }),
    row({ model: 'partial', cost_usd: 3, cost_partial: true }),
  ]);
  assert.deepEqual(models.map(({ model, cost_usd, cost_partial }) => ({ model, cost_usd, cost_partial })), [
    { model: 'model-a', cost_usd: 0, cost_partial: true },
    { model: 'partial', cost_usd: 3, cost_partial: true },
    { model: 'unknown', cost_usd: null, cost_partial: true },
  ]);
  assert.equal(models[0].tools.find((tool) => tool.provider === 'codex').cost_usd, null);
});

test('cache reads and writes are clamped before tools are grouped', () => {
  const [model] = groupUsageModels([
    row({ cache_read: 1000, cache_write: 500 }),
    row({ provider: 'codex', cache_read: 0, cache_write: 0 }),
  ]);
  assert.deepEqual(splitTokens(model), { input: 100, output: 40, cache: 100 });
  assert.deepEqual(splitTokens(model.tools[0]), { input: 100, output: 20, cache: 0 });
  assert.deepEqual(splitTokens(row({ cache_read: 60, cache_write: 80 })), { input: 0, output: 20, cache: 100 });
});

test('source totals remain authoritative even when token components differ', () => {
  const [model] = groupUsageModels([row({ total: 999 })]);
  assert.equal(model.total, 999);
  assert.equal(model.tools[0].total, 999);
  assert.deepEqual(splitTokens(model), { input: 60, output: 20, cache: 40 });
});

test('cache hit rate counts reads against input, excludes writes and output, and weights grouped usage', () => {
  assert.equal(cacheHitRate(row({ cache_read: 30, cache_write: 50, output: 900, total: 9999 })), 0.3);
  assert.equal(cacheHitRate(row({ cache_read: 0, cache_write: 100 })), 0);
  assert.equal(cacheHitRate(row({ cache_read: 200 })), 1);
  assert.equal(cacheHitRate(row({ input: 0, cache_read: 0 })), null);
  const [model] = groupUsageModels([
    row({ input: 100, cache_read: 80 }),
    row({ provider: 'codex', input: 900, cache_read: 90 }),
  ]);
  assert.equal(cacheHitRate(model), 0.17);
  assert.equal(cacheHitRate(aggregateUsageModels([model], 'Other')), 0.17);
});

test('Other sums remaining models and combines their tool breakdowns', () => {
  const models = groupUsageModels([
    row({ model: 'one', cost_usd: 2 }),
    row({ model: 'two', cost_usd: null }),
    row({ provider: 'codex', model: 'two', cost_usd: 0 }),
  ]);
  const other = aggregateUsageModels(models, 'Other models');
  assert.equal(other.model, 'Other models');
  assert.equal(other.total, 360);
  assert.equal(other.cost_usd, 2);
  assert.equal(other.cost_partial, true);
  assert.deepEqual(other.tools.map(({ provider, total, cost_usd, cost_partial }) => ({ provider, total, cost_usd, cost_partial })), [
    { provider: 'copilot', total: 240, cost_usd: 2, cost_partial: true },
    { provider: 'codex', total: 120, cost_usd: 0, cost_partial: false },
  ]);
  assert.deepEqual(splitTokens(other), { input: 180, output: 60, cache: 120 });
});

test('without-cache estimate uses each tool rate and never adds cache to input twice', () => {
  const rows = [
    row({ input: 1_000_000, output: 50_000, cache_read: 600_000, cache_write: 200_000, total: 1_050_000 }),
    row({ provider: 'codex', input: 1_000_000, output: 50_000, cache_read: 900_000, total: 1_050_000 }),
  ];
  assert.deepEqual(estimateWithoutCache(rows, catalog({}, { provider: 'codex', rates: { input: 5, output: 20 } })), { cost: 8.5, partial: false });
});

test('without-cache prices require exact provider and model, while explicit zero rates count', () => {
  const rows = [row({ model: 'free' }), row({ provider: 'codex' }), row({ model: 'unpriced' })];
  const prices = catalog({}, { model: 'free', rates: { input: 0, output: 0 } }, { model: 'unpriced', rates: null, source: 'unpriced' });
  assert.deepEqual(estimateWithoutCache(rows, prices), { cost: 0, partial: true });
  assert.deepEqual(estimateWithoutCache([row({ provider: 'codex' })], prices), { cost: null, partial: true });
  assert.deepEqual(estimateWithoutCache([row({ model: 'provider/model-a' })], prices), { cost: null, partial: true });
});

test('without-cache estimate excludes source-only costs and unknown token components', () => {
  const costOnly = row({ input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0, cost_usd: 5 });
  const totalOnly = row({ input: 0, output: 0, cache_read: 0, cache_write: 0, total: 100, cost_usd: null });
  assert.deepEqual(estimateWithoutCache([costOnly, totalOnly], catalog({})), { cost: null, partial: true });
  assert.deepEqual(estimateWithoutCache([costOnly, row({ input: 1_000_000, output: 0 })], catalog({})), { cost: 2, partial: true });
});

test('without-cache estimate preserves partial flags and can be below reported costs', () => {
  const counts = row({ input: 1_000_000, output: 0, cost_usd: 10, cost_partial: true });
  assert.deepEqual(estimateWithoutCache([counts], catalog({})), { cost: 2, partial: true });
});

test('empty usage costs zero and missing catalogs remain unknown', () => {
  assert.deepEqual(groupUsageModels([]), []);
  assert.deepEqual(aggregateUsageModels([], 'Other'), {
    model: 'Other', input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0, cost_usd: 0, cost_partial: false, tools: [],
  });
  assert.deepEqual(estimateWithoutCache([], null), { cost: 0, partial: false });
  assert.deepEqual(estimateWithoutCache([row()], null), { cost: null, partial: true });
  assert.deepEqual(estimateWithoutCache([row({ input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0, cost_usd: null })], catalog({})), { cost: null, partial: true });
  assert.deepEqual(estimateWithoutCache([row({ input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0, cost_usd: 0 })], catalog({})), { cost: 0, partial: false });
});
