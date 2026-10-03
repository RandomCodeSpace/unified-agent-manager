import type { Settings, TokenCounts, TokenPeriodKey, TokenPriceCatalog, TokenUsageReport } from '../api';

/** Deterministic preview values exercise K, M and B without provider calls. */
export function tokenUsageFixture(settings: Partial<Settings> = {}): TokenUsageReport {
  const catalog = tokenPriceFixture(settings);
  const periods = {} as TokenUsageReport['periods'];
  for (const [period, scale] of Object.entries({ today: 1, '7d': 7, '30d': 30, lifetime: 1200 })) {
    const models = [
      { provider: 'copilot', model: 'claude-sonnet-5', input: 1250000 * scale, output: 42000 * scale, cache_read: 980000 * scale, cache_write: 180000 * scale, total: 1292000 * scale, cost_usd: null as number | null },
      { provider: 'copilot', model: 'gpt-6-luna', input: 24500 * scale, output: 3200 * scale, cache_read: 12000 * scale, cache_write: 0, total: 27700 * scale, cost_usd: null as number | null },
    ];
    for (const model of models) {
      const rates = catalog.models.find((p) => p.provider === model.provider && p.model === model.model)?.rates;
      if (rates) model.cost_usd = ((model.input - model.cache_read - model.cache_write) * rates.input + model.cache_read * (rates.cache_read ?? rates.input) + model.cache_write * (rates.cache_write ?? rates.input) + model.output * rates.output) / 1e6;
    }
    const total = models.reduce<TokenCounts>((sum, row) => ({ input: sum.input + row.input, output: sum.output + row.output, cache_read: sum.cache_read + row.cache_read, cache_write: sum.cache_write + row.cache_write, total: sum.total + row.total }), { input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0 });
    const priced = models.filter((m) => m.cost_usd !== null);
    periods[period as TokenPeriodKey] = { models, total, cost_usd: priced.length ? priced.reduce((sum, m) => sum + m.cost_usd!, 0) : null, unpriced_models: models.length - priced.length };
  }
  return { since: '2026-01-01T00:00:00Z', today: '2026-10-03', periods };
}

export function tokenPriceFixture(settings: Partial<Settings> = {}): TokenPriceCatalog {
  return { commit: 'e768ad55cef70b13385807474724f832b20a6903', models: ['claude-sonnet-5', 'gpt-6-luna'].map((model) => {
    const manual = settings.token_prices?.copilot?.[model];
    // Illustrative preview prices, not the bundled catalog.
    const bundled = model === 'claude-sonnet-5' ? { input: 3, output: 15, cache_read: 0.3, cache_write: 3.75 } : null;
    return { provider: 'copilot', model, rates: manual ?? bundled, source: manual ? 'manual' : bundled ? 'bundled' : 'unpriced' };
  }) };
}
