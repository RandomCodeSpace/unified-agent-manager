// Hidden models (issue #191): a display preference kept in Settings. Pure, so the unit tests run in node.

import type { Model, ProviderInfo } from '../api';

/** The models a provider offers for choosing: its catalog without the IDs hidden in Settings. */
export function visibleModels<T extends Pick<Model, 'id'>>(models: readonly T[], hidden?: readonly string[]): T[] {
  if (!hidden?.length) return models.slice();
  const set = new Set(hidden);
  return models.filter((m) => !set.has(m.id));
}

/** One row of a model picker; `note` says why a model that is not offered is still there. */
export interface ModelChoice {
  model: Model;
  note?: 'Hidden in Settings' | 'Not offered now';
}

/**
 * What a model picker lists: the visible catalog, led by the current model when it is hidden
 * or no longer listed, so a Task or a Project default keeps showing what it runs on without
 * offering it anywhere else.
 */
export function modelChoices(catalog: readonly Model[], hidden: readonly string[] | undefined, current: string): ModelChoice[] {
  const visible = visibleModels(catalog, hidden).map((model) => ({ model }));
  if (!current || visible.some((c) => c.model.id === current)) return visible;
  const listed = catalog.find((m) => m.id === current);
  return [{ model: listed ?? { id: current, name: current }, note: listed ? 'Hidden in Settings' : 'Not offered now' }, ...visible];
}

const NAMES = new Intl.Collator('en', { numeric: true, sensitivity: 'base' });

/** Models in reading order: by display name, numbers compared as numbers ("Claude Sonnet 5" before "Claude Sonnet 5.5"). */
export function byModelName(a: Pick<Model, 'id' | 'name'>, b: Pick<Model, 'id' | 'name'>): number {
  return NAMES.compare(a.name || a.id, b.name || b.id) || NAMES.compare(a.id, b.id);
}

const EFFORTS: Record<string, string> = { none: 'None', minimal: 'Minimal', low: 'Low', medium: 'Medium', high: 'High', xhigh: 'Extra high', max: 'Max' };

/** A reasoning effort as a menu shows it; the stored value stays the provider's own ("xhigh" reads "Extra high"). */
export function effortLabel(effort: string): string {
  return EFFORTS[effort] ?? (effort.charAt(0).toUpperCase() + effort.slice(1));
}

/** The Utility model's opt-out (`title_model` value `none`): the provider keeps its own title and UAM makes no AI call. */
export const UTILITY_NONE = 'none';

/**
 * The label of the unset Utility model, which the service resolves to the provider's cheapest priced model for its
 * jobs other than titles (a Task is titled by its own model then).
 */
export function cheapestLabel(provider: Pick<ProviderInfo, 'display_name' | 'models' | 'cheapest_model'>): string {
  const id = provider.cheapest_model;
  if (!id) return 'Cheapest (none priced)';
  return `Cheapest (currently ${provider.models.find((m) => m.id === id)?.name || id})`;
}
