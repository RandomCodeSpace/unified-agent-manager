// Custom (BYOM) models grouped by provider for Settings. Pure, so the unit tests run in node.

import type { CustomModel } from '../api';

/** A provider connection and its enabled models; entries with one name share the connection. */
export interface CustomProvider {
  name: string;
  base_url: string;
  api_key_env: string;
  wire_api?: NonNullable<CustomModel['wire_api']>;
  key_present?: boolean;
  models: CustomModel[];
}

/** The custom models grouped by provider name, in the order the providers first appear. */
export function customProviders(models: readonly CustomModel[]): CustomProvider[] {
  const out: CustomProvider[] = [];
  for (const m of models) {
    let p = out.find((g) => g.name === m.name);
    if (!p) {
      p = { name: m.name, base_url: m.base_url, api_key_env: m.api_key_env, wire_api: m.wire_api, key_present: m.key_present, models: [] };
      out.push(p);
    }
    p.models.push(m);
  }
  return out;
}

/** The list as PATCH takes it: without the output-only `key_present`. */
export function storedModels(models: readonly CustomModel[]): CustomModel[] {
  return models.map(({ key_present: _, ...m }) => m);
}

/**
 * The list with provider `original` (none for a new one) replaced by `provider` offering the
 * model IDs in `ids`, in place; a model it already had keeps its display name, a new one is
 * named by its ID. `vision` replaces the image-enabled IDs when supplied, otherwise their
 * saved settings are kept. No IDs removes the provider.
 */
export function withProvider(models: readonly CustomModel[], original: string | undefined, provider: Omit<CustomProvider, 'models' | 'key_present'>, ids: readonly string[], vision?: readonly string[]): CustomModel[] {
  const stored = storedModels(models);
  const old = stored.filter((m) => m.name === original);
  const entries: CustomModel[] = [...new Set(ids)].map((model_id) => {
    const previous = old.find((m) => m.model_id === model_id);
    const display_name = previous?.display_name || model_id;
    const images = vision ? vision.includes(model_id) : previous?.vision;
    return { name: provider.name, base_url: provider.base_url, api_key_env: provider.api_key_env, ...(provider.wire_api ? { wire_api: provider.wire_api } : {}), model_id, display_name, ...(images ? { vision: true } : {}) };
  });
  const at = stored.findIndex((m) => m.name === original);
  const rest = stored.filter((m) => m.name !== original);
  // No removed model precedes the first match, so its index stays the same.
  const index = at < 0 ? rest.length : at;
  return [...rest.slice(0, index), ...entries, ...rest.slice(index)];
}

/** The IDs matching a search, case-insensitively. */
export function matchingIds(ids: readonly string[], query: string): string[] {
  const q = query.trim().toLowerCase();
  return q ? ids.filter((id) => id.toLowerCase().includes(q)) : ids.slice();
}
