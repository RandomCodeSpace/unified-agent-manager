// Hidden models (issue #191): a display preference kept in Settings. Pure, so the unit tests run in node.

import type { Model } from '../api';

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
