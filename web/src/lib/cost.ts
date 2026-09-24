// Context usage, credits and cost (issues #186, #188): the numbers behind the composer's
// context ring, credits chip and cost estimate. Pure, so the unit tests run in node.

import type { ContextUsage, CostTier, Model, Prices, Quota, TierPrices } from '../api';

export type Tone = 'muted' | 'accent' | 'attention' | 'danger';

/** Share of the context window used, 0…1: 0 before any report, and never above 1 when the provider reports usage past the limit. */
export function ringFraction(context: Pick<ContextUsage, 'used' | 'limit'> | undefined): number {
  if (!context || !(context.limit > 0) || !(context.used > 0)) return 0;
  return Math.min(1, context.used / context.limit);
}

/** The ring's colour: `accent`, `attention` from 80% and `danger` from 95% (#186). */
export function ringTone(fraction: number): Tone {
  return fraction >= 0.95 ? 'danger' : fraction >= 0.8 ? 'attention' : 'accent';
}

/** The credits chip's colour: `attention` at 20% left or less, `danger` at 5% or less (#188); unlimited quotas stay quiet. */
export function creditsTone(quota: Pick<Quota, 'unlimited' | 'remaining_percent'>): Tone {
  if (quota.unlimited) return 'muted';
  return quota.remaining_percent <= 5 ? 'danger' : quota.remaining_percent <= 20 ? 'attention' : 'muted';
}

/** 31K, 200K, 1.2M. */
export function compactTokens(n: number): string {
  return n.toLocaleString('en-US', { notation: 'compact', maximumFractionDigits: 1 });
}

/** "31K of 200K tokens · 15%": the ring's value, as its popover, tooltip and accessible name carry it. */
export function contextText(context: Pick<ContextUsage, 'used' | 'limit'>): string {
  const pct = context.limit > 0 ? Math.round((context.used / context.limit) * 100) : 0;
  return `${compactTokens(context.used)} of ${compactTokens(context.limit)} tokens · ${pct}%`;
}

const BATCH = 1_000_000;

/** The tier whose prices apply: long context when it is selected, or when the context exceeds the default prompt budget, where the model has one. */
export function priceTier(prices: Prices, tokens: number, contextSize: string): TierPrices {
  const long = prices.long_context;
  if (!long) return prices;
  if (contextSize === 'long_context') return long;
  return prices.max_prompt_tokens && tokens > prices.max_prompt_tokens ? long : prices;
}

/**
 * Estimated AI Credits to send the Task's current context to `model` once more: the cached
 * share at `cache_read` (at `input` when the tier has no cache price), the rest at `input`.
 * Output is excluded. Null when the model has no input price or nothing has been reported.
 */
export function estimateTurnCost(model: Pick<Model, 'prices'> | undefined, context: ContextUsage | undefined, contextSize = 'default'): number | null {
  const prices = model?.prices;
  if (!prices || !context || !(context.used > 0)) return null;
  const tier = priceTier(prices, context.used, contextSize);
  const input = tier.input;
  if (input === undefined) return null;
  const cacheRead = tier.cache_read ?? input;
  const cached = Math.max(0, Math.min(context.cached ?? 0, context.used));
  return ((context.used - cached) * input + cached * cacheRead) / (prices.batch_size || BATCH);
}

/** AI Credits for reading: two decimals under 1, one under 10, whole numbers past that, and "<0.01" for a trace. */
export function formatCredits(n: number): string {
  if (n === 0) return '0';
  if (n < 0.005) return '<0.01';
  if (n < 1) return n.toFixed(2);
  if (n < 10) return n.toFixed(1);
  return Math.round(n).toLocaleString('en-US');
}

export const COST_TIER_LABEL: Record<CostTier, string> = { low: 'Low cost', medium: 'Medium cost', high: 'High cost', very_high: 'Very high cost' };

/** "Low cost · 0.25 in, 2 out per 1M tokens", "10% off", or "" when the model reports no cost. */
export function modelCostLine(model: Pick<Model, 'cost_tier' | 'discount_percent' | 'prices'>): string {
  const parts: string[] = [];
  if (model.cost_tier) parts.push(COST_TIER_LABEL[model.cost_tier]);
  if (model.discount_percent) parts.push(`${model.discount_percent}% off`);
  const p = model.prices;
  if (p && (p.input !== undefined || p.output !== undefined)) {
    const per = compactTokens(p.batch_size || BATCH);
    const io = [p.input !== undefined ? `${p.input} in` : '', p.output !== undefined ? `${p.output} out` : ''].filter(Boolean).join(', ');
    parts.push(`${io} per ${per} tokens`);
  }
  return parts.join(' · ');
}

/** The quota the credits chip shows for a provider: its first limited one, else its first; null when it reports none. */
export function providerQuota(quotas: readonly Quota[] | undefined, provider: string): Quota | null {
  const mine = (quotas ?? []).filter((q) => q.provider === provider);
  return mine.find((q) => !q.unlimited) ?? mine[0] ?? null;
}

/** "premium requests" for Copilot's premium_interactions; other types read as words. */
export function quotaLabel(type: string): string {
  return type === 'premium_interactions' ? 'premium requests' : type.replace(/_/g, ' ');
}

/** The chip's face: "88% left", or "Unlimited". */
export function quotaFace(quota: Pick<Quota, 'unlimited' | 'remaining_percent'>): string {
  return quota.unlimited ? 'Unlimited' : `${Math.round(quota.remaining_percent)}% left`;
}

/** "180 of 1,500 premium requests used", with the overage when there is one. */
export function quotaText(quota: Quota): string {
  if (quota.unlimited) return `Unlimited ${quotaLabel(quota.type)}`;
  const base = `${quota.used.toLocaleString('en-US')} of ${quota.entitlement.toLocaleString('en-US')} ${quotaLabel(quota.type)} used`;
  return quota.overage > 0 ? `${base} · ${quota.overage.toLocaleString('en-US')} over` : base;
}
