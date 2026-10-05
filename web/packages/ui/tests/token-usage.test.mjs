import assert from 'node:assert/strict';
import test from 'node:test';
import { aggregateUsageModels, cacheHitRate, estimateCacheSaving, groupUsageModels, mergeTokenReports, splitTokens, sumCacheSavings } from '../src/lib/token-usage.ts';

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

test('cache saving uses each tool rate, never adds cache to input twice, and subtracts each recorded cost', () => {
  const rows = [
    row({ input: 1_000_000, output: 50_000, cache_read: 600_000, cache_write: 200_000, total: 1_050_000, cost_usd: 1 }),
    row({ provider: 'codex', input: 1_000_000, output: 50_000, cache_read: 900_000, total: 1_050_000, cost_usd: 2 }),
  ];
  assert.deepEqual(estimateCacheSaving(rows, catalog({}, { provider: 'codex', rates: { input: 5, output: 20 } })), { saving: 5.5, partial: false });
});

test('cache saving prices require exact provider and model, while explicit zero rates count', () => {
  const rows = [row({ model: 'free', cost_usd: 0 }), row({ provider: 'codex' }), row({ model: 'unpriced' })];
  const prices = catalog({}, { model: 'free', rates: { input: 0, output: 0 } }, { model: 'unpriced', rates: null, source: 'unpriced' });
  assert.deepEqual(estimateCacheSaving(rows, prices), { saving: 0, partial: true });
  assert.deepEqual(estimateCacheSaving([row({ provider: 'codex' })], prices), { saving: null, partial: true });
  assert.deepEqual(estimateCacheSaving([row({ model: 'provider/model-a' })], prices), { saving: null, partial: true });
});

test('cache saving excludes source-only costs, unknown token components and unknown costs', () => {
  const costOnly = row({ input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0, cost_usd: 5 });
  const totalOnly = row({ input: 0, output: 0, cache_read: 0, cache_write: 0, total: 100, cost_usd: null });
  assert.deepEqual(estimateCacheSaving([costOnly, totalOnly], catalog({})), { saving: null, partial: true });
  assert.deepEqual(estimateCacheSaving([costOnly, row({ input: 1_000_000, output: 0, cost_usd: 0.5 })], catalog({})), { saving: 1.5, partial: true });
  assert.deepEqual(estimateCacheSaving([row({ input: 1_000_000, output: 0, cost_usd: null })], catalog({})), { saving: null, partial: true });
});

test('cache saving preserves partial flags and can be below zero', () => {
  const counts = row({ input: 1_000_000, output: 0, cost_usd: 10, cost_partial: true });
  assert.deepEqual(estimateCacheSaving([counts], catalog({})), { saving: -8, partial: true });
});

test('empty usage costs zero and missing catalogs remain unknown', () => {
  assert.deepEqual(groupUsageModels([]), []);
  assert.deepEqual(aggregateUsageModels([], 'Other'), {
    model: 'Other', input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0, cost_usd: 0, cost_partial: false, tools: [],
  });
  assert.deepEqual(estimateCacheSaving([], null), { saving: 0, partial: false });
  assert.deepEqual(estimateCacheSaving([row()], null), { saving: null, partial: true });
  assert.deepEqual(estimateCacheSaving([row({ input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0, cost_usd: null })], catalog({})), { saving: null, partial: true });
  assert.deepEqual(estimateCacheSaving([row({ input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0, cost_usd: 0 })], catalog({})), { saving: 0, partial: false });
});

test('machine reports merge by summing totals and known costs, as fresh as the stalest collection', () => {
  const report = (since, updated, status, models, cost) => ({
    since, today: '2026-10-05', collection: { status, updated_at: updated, copilot_since: since },
    periods: Object.fromEntries(['today', '7d', '30d', 'lifetime'].map((key) => [key, { models, total: { input: 100, output: 20, cache_read: 30, cache_write: 10, total: 120 }, cost_usd: cost, unpriced_models: cost === null ? models.length : 0 }])),
  });
  const merged = mergeTokenReports([
    report('2026-02-01T00:00:00Z', '2026-10-05T10:00:00Z', 'ready', [row()], 1),
    report('2026-01-01T00:00:00Z', '2026-10-05T09:00:00Z', 'partial', [row({ cost_usd: null })], null),
  ]);
  assert.equal(merged.since, '2026-01-01T00:00:00Z');
  assert.deepEqual(merged.collection, { status: 'partial', updated_at: '2026-10-05T09:00:00Z', copilot_since: '2026-02-01T00:00:00Z' });
  assert.deepEqual(merged.periods.today.total, { input: 200, output: 40, cache_read: 60, cache_write: 20, total: 240 });
  assert.equal(merged.periods.today.cost_usd, 1);
  assert.equal(merged.periods.today.unpriced_models, 1);
  assert.equal(merged.periods.today.models.length, 2);
  // No machine priced anything: no cost, rather than $0.
  assert.equal(mergeTokenReports([report('2026-01-01T00:00:00Z', undefined, 'ready', [row({ cost_usd: null })], null)]).periods.today.cost_usd, null);
  assert.deepEqual(sumCacheSavings([{ saving: 2, partial: false }, { saving: null, partial: false }]), { saving: 2, partial: true });
  assert.deepEqual(sumCacheSavings([{ saving: null, partial: true }]), { saving: null, partial: true });
});
