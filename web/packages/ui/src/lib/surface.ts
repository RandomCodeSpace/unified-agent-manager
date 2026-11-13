/**
 * The data model of an A2UI v1.0 surface (`uam_surface`): JSON Pointer reads and writes, `@path`
 * bindings (relative inside a list template), template children and the `required` check; the
 * card's title, and the action line a Button sends as the user's message.
 */
import { createContext, useSyncExternalStore } from 'react';
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

/**
 * A copy of `model` with `value` at `path`; `null` deletes; no path, or `/`, replaces the whole
 * model (A2UI v1.0). In an array the key is an index up to its length (the length or `-`
 * appends); any other write leaves the model as it was, so no array turns sparse.
 */
export function set(model: Model, path: string, value: unknown): Model {
  const keys = tokens(path);
  if (!keys.length) return (value ?? {}) as Model;
  const write = (at: unknown, i: number): unknown => {
    let key = keys[i];
    if (key === '__proto__' || key === 'constructor' || key === 'prototype') return at;
    const copy: Model | unknown[] = Array.isArray(at) ? [...at] : at && typeof at === 'object' ? { ...(at as Model) } : /^\d+$/.test(key) ? [] : {};
    const last = i === keys.length - 1;
    if (Array.isArray(copy)) {
      if (key === '-' && last) key = String(copy.length);
      if (!/^\d+$/.test(key) || Number(key) > copy.length || (Number(key) === copy.length && value === null)) return at;
    }
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

/** Whether `required` reads a value as filled: blank text, `false`, an empty list and nothing are empty; `0` is filled. */
export const filled = (v: unknown) => !(v === undefined || v === null || v === false || (typeof v === 'string' && !v.trim()) || (Array.isArray(v) && v.length === 0));

/**
 * The `@checks` rules of a component that fail, each with the pointer its `required` value is
 * bound to (null for a plain path condition) and the agent's message; only `required` and
 * plain path conditions are understood, others pass.
 */
export function failedChecks(component: SurfaceComponent, model: unknown, base = ''): { path: string | null; message: string }[] {
  const checks = component['@checks'];
  if (!Array.isArray(checks)) return [];
  return checks.flatMap((rule: { condition?: unknown; message?: unknown }) => {
    const c = rule?.condition as Model | undefined;
    const message = typeof rule?.message === 'string' ? rule.message : '';
    if (isBinding(c)) return resolve(c, model, base) ? [] : [{ path: null, message }];
    if (c?.['@call'] !== 'required') return [];
    const value = (c.args as Model | undefined)?.value;
    return filled(resolve(value, model, base)) ? [] : [{ path: boundPath(value, base), message }];
  });
}

/** The children of a container: an id list (same base), or a template, one child per item of the list at `path`. */
export function children(spec: unknown, model: unknown, base = '', limit = Infinity): { id: string; base: string }[] {
  if (Array.isArray(spec)) return spec.filter((id): id is string => typeof id === 'string').map((id) => ({ id, base }));
  if (!spec || typeof spec !== 'object') return [];
  const { componentId, path } = spec as { componentId?: unknown; path?: unknown };
  if (typeof componentId !== 'string' || typeof path !== 'string') return [];
  const at = absolute(path, base);
  const list = get(model, at);
  const keys = Array.isArray(list) ? Array.from({ length: Math.min(list.length, limit) }, (_, i) => String(i)) : list && typeof list === 'object' ? Object.keys(list).slice(0, limit) : [];
  return keys.map((k) => ({ id: componentId, base: `${at === '/' ? '' : at}/${k.replaceAll('~', '~0').replaceAll('/', '~1')}` }));
}

/**
 * How many components the card would draw, counting a shared child once per reference and a
 * template once per item; stops counting past `budget`, so a graph that would blow up costs at
 * most `budget` steps. Follows what the renderer draws (Modal content and every tab included).
 */
export function drawnCount(byId: ReadonlyMap<string, SurfaceComponent>, model: unknown, budget: number): number {
  let n = 0;
  const visit = (id: string, base: string, depth: number) => {
    const c = byId.get(id);
    if (!c || depth > MAX_DEPTH || ++n > budget) return;
    const next = [
      ...children(c.children, model, base, budget - n + 1),
      ...[c.child, c.trigger, c.content].filter((k): k is string => typeof k === 'string').map((k) => ({ id: k, base })),
      ...(Array.isArray(c.tabs) ? c.tabs : []).map((t: { child?: unknown }) => ({ id: String(t?.child), base })),
    ];
    for (const k of next) {
      if (n > budget) return;
      visit(k.id, k.base, depth + 1);
    }
  };
  visit('root', '', 0);
  return n;
}

/** The depth past which a cycle or a runaway tree draws nothing. */
export const MAX_DEPTH = 24;

/** The text that names a row's unlabelled input: the first Text with words in the row, nearest first. */
export function rowName(byId: ReadonlyMap<string, SurfaceComponent>, model: unknown, ids: unknown, base = ''): string {
  let level = Array.isArray(ids) ? ids.map(String) : [];
  for (let depth = 0; depth < 4 && level.length; depth++) {
    const next: string[] = [];
    for (const id of level) {
      const c = byId.get(id);
      const words = c?.component === 'Text' ? text(c.text, model, base).trim() : '';
      if (words) return words;
      if (Array.isArray(c?.children)) next.push(...c.children.map(String));
      if (typeof c?.child === 'string') next.push(c.child);
    }
    level = next;
  }
  return '';
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

/** A `uam_surface` call that created a surface shows its card; update-only and failed calls carry no `surface_id` and stay activity rows. */
export function isSurfaceCall(item: Item): boolean {
  return item.kind === 'tool' && item.tool?.name === 'uam_surface' && !!item.tool.surface_id;
}

const HEADINGS = new Set(['title', 'h1', 'h2', 'h3', 'h4', 'h5']);
export const isHeading = (c: SurfaceComponent | undefined) => c?.component === 'Text' && HEADINGS.has(String(c.variant));

/** The first child of a Column at `id` (Cards unwrapped; bindings read under the template item `base`) when it is a Text that reads as a title: a heading, or one short line with no sentence stop. */
export function leadTitle(byId: ReadonlyMap<string, SurfaceComponent>, model: unknown, id: string, base = ''): { id: string; text: string } | null {
  let at = byId.get(id);
  for (let i = 0; at?.component === 'Card' && i < 8; i++) at = byId.get(String(at.child));
  const first = at?.component === 'Column' && Array.isArray(at.children) ? byId.get(String(at.children[0])) : undefined;
  if (first?.component !== 'Text' || !(first.variant === undefined || first.variant === 'body' || isHeading(first))) return null;
  const line = text(first.text, model, base).trim();
  return line && (isHeading(first) || (line.length <= 80 && !line.includes('\n') && !/[.:;?!]$/.test(line))) ? { id: first.id, text: line } : null;
}

/** The Text that titles the card: the first one the agent marked `title` (or a heading), else the root column's lead line when it reads as a title, which the card then styles as one. */
export function cardTitle(surface: Pick<Surface, 'components' | 'data_model'>): { id: string; text: string } | null {
  const marked = surface.components.find(isHeading);
  if (marked) return { id: marked.id, text: text(marked.text, surface.data_model) };
  return leadTitle(new Map(surface.components.map((c) => [c.id, c])), surface.data_model, 'root');
}

/** The surfaces the open cards loaded, by Thread and surface id, so an action line can name its button and card. */
const known = new Map<string, Surface>();
const listeners = new Set<() => void>();
export function rememberSurface(sessionId: string, surface: Surface) {
  known.set(`${sessionId}\n${surface.surface_id}`, surface);
  for (const l of listeners) l();
}
export function useKnownSurface(sessionId: string | undefined, surfaceId: string): Surface | undefined {
  return useSyncExternalStore((l) => { listeners.add(l); return () => listeners.delete(l); }, () => known.get(`${sessionId}\n${surfaceId}`));
}

export const ACTION_PREFIX = '[uam_surface action] ';
/** The action line; uam adds the pressed Button's words (`label`) and the card's title (`title`) as they read at the press. */
export interface SurfaceActionLine { name: string; surfaceId: string; sourceComponentId: string; timestamp?: string; label?: string; title?: string; context: Model }

/** The action a user message carries when a card's Button sent it (the server's one line), or null for any other text. */
export function parseActionLine(line: string): SurfaceActionLine | null {
  if (!line.startsWith(ACTION_PREFIX)) return null;
  try {
    const action = (JSON.parse(line.slice(ACTION_PREFIX.length)) as { action?: Partial<SurfaceActionLine> }).action;
    if (typeof action?.name !== 'string' || typeof action.surfaceId !== 'string') return null;
    const context = action.context && typeof action.context === 'object' && !Array.isArray(action.context) ? action.context : {};
    const words = (v: unknown) => (typeof v === 'string' && v.trim() ? v.trim() : undefined);
    return { name: action.name, surfaceId: action.surfaceId, sourceComponentId: String(action.sourceComponentId ?? ''), timestamp: action.timestamp, label: words(action.label), title: words(action.title), context };
  } catch {
    return null;
  }
}

/** What an action line shows: the words it carries from the press, else the pressed Button's text and the card's title from the known surface, else the action's name alone. */
export function actionLabel(action: SurfaceActionLine, surface: Surface | undefined): { button: string; card: string } {
  const source = surface?.components.find((c) => c.id === action.sourceComponentId);
  const label = surface?.components.find((c) => c.id === source?.child);
  const button = action.label ?? (label?.component === 'Text' ? text(label.text, surface?.data_model).trim() : '');
  return { button: button || action.name, card: action.title ?? (surface ? cardTitle(surface)?.text ?? '' : '') };
}
