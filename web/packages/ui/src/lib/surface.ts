// PROTOTYPE: uam_surface (A2UI v1.0). Throwaway.
/**
 * The data model of an A2UI v1.0 surface: JSON Pointer reads and writes, `@path` bindings
 * (relative inside a list template), template children and the `required` check.
 */
import { createContext } from 'react';
import type { Item, Surface, SurfaceComponent } from '../api';

type Model = Record<string, unknown>;

function tokens(path: string): string[] {
  if (!path || path === '/') return [];
  return path.replace(/^\//, '').split('/').map((t) => t.replaceAll('~1', '/').replaceAll('~0', '~'));
}

/** The absolute pointer for `path`: one with a leading `/` stands alone, any other is relative to `base` (a template item). */
export function absolute(path: string, base = ''): string {
  if (path.startsWith('/')) return path;
  return path && path !== '.' ? `${base}/${path}` : base || '/';
}

export function get(model: unknown, path: string): unknown {
  let at = model;
  for (const t of tokens(path)) {
    if (at === null || typeof at !== 'object' || !Object.hasOwn(at, t)) return undefined;
    at = (at as Model)[t];
  }
  return at;
}

/** A copy of `model` with `value` at `path`; `null` deletes; no path replaces the whole model. */
export function set(model: Model, path: string, value: unknown): Model {
  const keys = tokens(path);
  if (!keys.length) return (value ?? {}) as Model;
  const write = (at: unknown, i: number): unknown => {
    const key = keys[i];
    if (key === '__proto__' || key === 'constructor' || key === 'prototype') return at;
    const copy: Model | unknown[] = Array.isArray(at) ? [...at] : at && typeof at === 'object' ? { ...(at as Model) } : /^\d+$/.test(key) ? [] : {};
    const last = i === keys.length - 1;
    const next = last ? value : write((copy as Model)[key], i + 1);
    if (last && value === null) {
      if (Array.isArray(copy)) copy.splice(Number(key), 1);
      else delete copy[key];
    } else (copy as Model)[key] = next;
    return copy;
  };
  return write(model, 0) as Model;
}

const isBinding = (v: unknown): v is { '@path': string } => !!v && typeof v === 'object' && typeof (v as Model)['@path'] === 'string';

/** A dynamic value: a literal, or `{"@path"}` read from the model. `@call` is not supported and reads as nothing. */
export function resolve(value: unknown, model: unknown, base = ''): unknown {
  if (isBinding(value)) return get(model, absolute(value['@path'], base));
  if (value && typeof value === 'object' && '@call' in value) return undefined;
  return value;
}

/** The pointer a two-way bound prop writes to, or null for a literal. */
export function boundPath(value: unknown, base = ''): string | null {
  return isBinding(value) ? absolute(value['@path'], base) : null;
}

export function text(value: unknown, model: unknown, base = ''): string {
  const v = resolve(value, model, base);
  return typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean' ? String(v) : '';
}

/** Whether every `@checks` rule passes; only `required` and plain path conditions are understood, others pass. */
export function checksPass(component: SurfaceComponent, model: unknown, base = ''): boolean {
  const checks = component['@checks'];
  if (!Array.isArray(checks)) return true;
  return checks.every((rule: { condition?: unknown }) => {
    const c = rule?.condition as Model | undefined;
    if (isBinding(c)) return !!resolve(c, model, base);
    if (c?.['@call'] !== 'required') return true;
    const v = resolve((c.args as Model | undefined)?.value, model, base);
    return !(v === undefined || v === null || v === '' || (Array.isArray(v) && v.length === 0));
  });
}

/** The children of a container: an id list (same base), or a template, one child per item of the list at `path`. */
export function children(spec: unknown, model: unknown, base = ''): { id: string; base: string }[] {
  if (Array.isArray(spec)) return spec.filter((id): id is string => typeof id === 'string').map((id) => ({ id, base }));
  if (!spec || typeof spec !== 'object') return [];
  const { componentId, path } = spec as { componentId?: unknown; path?: unknown };
  if (typeof componentId !== 'string' || typeof path !== 'string') return [];
  const at = absolute(path, base);
  const list = get(model, at);
  const keys = Array.isArray(list) ? list.map((_, i) => String(i)) : list && typeof list === 'object' ? Object.keys(list) : [];
  return keys.map((k) => ({ id: componentId, base: `${at === '/' ? '' : at}/${k.replaceAll('~', '~0').replaceAll('/', '~1')}` }));
}

/** The action context with every binding resolved against the model. */
export function actionContext(context: unknown, model: unknown, base = ''): Model {
  if (!context || typeof context !== 'object') return {};
  return Object.fromEntries(Object.entries(context).map(([k, v]) => [k, resolve(v, model, base) ?? null]));
}

type Message = { createSurface?: Model; updateComponents?: Model; updateDataModel?: Model; deleteSurface?: Model };

/**
 * Applies one agent-to-renderer message to a surface, as the server does; the mock uses it to
 * stand in for the server. Returns the surface unchanged for a message about another surface.
 */
export function applyMessage(surface: Surface | null, message: Message, callId = ''): Surface | null {
  const create = message.createSurface;
  if (create) {
    return { surface_id: String(create.surfaceId), call_id: callId, catalog_id: String(create.catalogId), version: 1, deleted: false, components: (create.components as SurfaceComponent[] | undefined) ?? [], data_model: (create.dataModel as Model | undefined) ?? {} };
  }
  if (!surface) return surface;
  const body = message.updateComponents ?? message.updateDataModel ?? message.deleteSurface;
  if (!body || body.surfaceId !== surface.surface_id) return surface;
  const next = { ...surface, version: surface.version + 1 };
  if (message.updateComponents) {
    const byId = new Map(surface.components.map((c) => [c.id, c]));
    for (const c of body.components as SurfaceComponent[]) byId.set(c.id, c);
    return { ...next, components: [...byId.values()] };
  }
  if (message.updateDataModel) return { ...next, data_model: set(surface.data_model, typeof body.path === 'string' ? body.path : '', body.value) };
  return { ...next, deleted: true };
}

/** The latest `surface` event's version by creating call id, for the open cards (provided by the Thread view). */
export const SurfaceVersions = createContext<Readonly<Record<string, number>>>({});

/** A completed `uam_surface` call whose messages create a surface shows its card; update-only calls stay activity rows. */
export function isSurfaceCall(item: Item): boolean {
  return item.kind === 'tool' && item.tool?.name === 'uam_surface' && item.tool.status === 'completed'; // the transcript's tool rows carry no input, so an update-only call answers 404 and the card renders nothing
}
