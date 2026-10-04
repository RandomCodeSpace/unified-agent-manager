import type { TokenCounts, TokenPriceCatalog, TokenUsageReport } from '../api';

type UsageRow = TokenUsageReport['periods']['today']['models'][number];
type CostedCounts = TokenCounts & { cost_usd: number | null; cost_partial?: boolean };

export type UsageTool = TokenCounts & { provider: string; cost_usd: number | null; cost_partial: boolean };
export type UsageModel = TokenCounts & { model: string; cost_usd: number | null; cost_partial: boolean; tools: UsageTool[] };

function cacheCounts(counts: TokenCounts) {
  // Match server pricing: reads take precedence, and cache never exceeds input.
  const read = Math.min(counts.cache_read, counts.input);
  return { read, write: Math.min(counts.cache_write, counts.input - read) };
}

/** Input in the API includes cache; the three displayed segments are disjoint. */
export function splitTokens(counts: TokenCounts): { input: number; output: number; cache: number } {
  const { read, write } = cacheCounts(counts);
  return { input: counts.input - read - write, output: counts.output, cache: read + write };
}

/** Share of recorded input served by cache reads; writes are not cache hits. */
export function cacheHitRate(counts: TokenCounts): number | null {
  return counts.input > 0 ? cacheCounts(counts).read / counts.input : null;
}

function sumUsage(rows: readonly CostedCounts[]): TokenCounts & { cost_usd: number | null; cost_partial: boolean } {
  const sum = { input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0, cost_usd: null as number | null, cost_partial: false };
  for (const row of rows) {
    const { read, write } = cacheCounts(row);
    sum.input += row.input;
    sum.output += row.output;
    // Clamp each source before grouping so one tool cannot consume another's input.
    sum.cache_read += read;
    sum.cache_write += write;
    sum.total += row.total;
    if (row.cost_usd !== null) sum.cost_usd = (sum.cost_usd ?? 0) + row.cost_usd;
    sum.cost_partial ||= row.cost_usd === null || !!row.cost_partial;
  }
  if (rows.length === 0) sum.cost_usd = 0;
  return sum;
}

function groupTools(rows: readonly (CostedCounts & { provider: string })[]): UsageTool[] {
  const tools = new Map<string, CostedCounts[]>();
  for (const row of rows) {
    const group = tools.get(row.provider) ?? [];
    group.push(row);
    tools.set(row.provider, group);
  }
  return [...tools].map(([provider, group]) => ({ provider, ...sumUsage(group) }))
    .sort((a, b) => b.total - a.total || a.provider.localeCompare(b.provider));
}

/** Group exact model IDs across tools; retain the collector's authoritative total. */
export function groupUsageModels(rows: readonly UsageRow[]): UsageModel[] {
  const models = new Map<string, UsageRow[]>();
  for (const row of rows) {
    const group = models.get(row.model) ?? [];
    group.push(row);
    models.set(row.model, group);
  }
  return [...models].map(([model, group]) => ({ model, ...sumUsage(group), tools: groupTools(group) }))
    .sort((a, b) => b.total - a.total || a.model.localeCompare(b.model));
}

export function aggregateUsageModels(models: readonly UsageModel[], label: string): UsageModel {
  return { model: label, ...sumUsage(models), tools: groupTools(models.flatMap((model) => model.tools)) };
}

/** Current input/output rates applied to available counts, with all cache billed as input. */
export function estimateWithoutCache(rows: readonly UsageRow[], catalog: TokenPriceCatalog | null): { cost: number | null; partial: boolean } {
  if (rows.length === 0) return { cost: 0, partial: false };
  const prices = new Map(catalog?.models.map((row) => [JSON.stringify([row.provider, row.model]), row.rates]));
  let cost: number | null = null;
  let partial = false;
  for (const row of rows) {
    const rates = prices.get(JSON.stringify([row.provider, row.model]));
    const missingCounts = row.input === 0 && row.output === 0 && (row.total > 0 || row.cost_usd !== 0);
    if (!rates || missingCounts) {
      partial = true;
      continue;
    }
    cost = (cost ?? 0) + (row.input * rates.input + row.output * rates.output) / 1e6;
    partial ||= !!row.cost_partial;
  }
  return { cost, partial };
}
