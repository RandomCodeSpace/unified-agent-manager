import type { Item } from '../api';
import { looksLikePath, taskFile } from './markdown.ts';
import { isFileDeclaration } from './transcript.ts';

export const FILE_CACHE_ENTRIES = 256;
export const FILE_CACHE_BYTES = 256 * 1024;
export const FILE_BATCH_PATHS = 64;
export const FILE_BODY_BYTES = 512 * 1024;
const encoder = new TextEncoder();
const localTools = new Set(['view', 'create', 'edit', 'apply_patch', 'uam_show_file']);
const changingTools = new Set(['create', 'edit', 'apply_patch']);
const RETRY_MS = 2_000;
const RETRY_MAX_MS = 30_000;

export function hintPath(path: string, workdir: string): string | undefined {
  // Tool arguments are literal paths, unlike Markdown hrefs.
  return taskFile(path.split('/').map(encodeURIComponent).join('/'), workdir)?.path;
}
export function localHints(item: Item, workdir: string): string[] {
  if (!item.tool || !localTools.has(item.tool.name)) return [];
  if (item.tool.name === 'uam_show_file' && !isFileDeclaration(item)) return [];
  const paths = item.tool.name === 'uam_show_file'
    ? (item.tool.file_paths ?? []).filter(path => path === item.tool?.declaration?.path)
    : item.tool.file_paths ?? [];
  return paths.flatMap(path => {
    const normalized = hintPath(path, workdir);
    return normalized ? [normalized] : [];
  });
}

/**
 * Only resolver answers enter this cache. Errors remain unknown. An expired "exists" answer
 * stays readable until a fresh answer replaces it, so a link never drops back to plain code
 * while it is rechecked; an expired "missing" answer reads as unknown, which renders the same.
 * A name that is not itself a file but means exactly one (`gen.py` for `tools/gen.py`)
 * exists with that file as its target.
 */
export class FileCache {
  private readonly entries = new Map<string, { exists: boolean; until: number; bytes: number; stale?: boolean; target?: string }>();
  expired?: (path: string) => void;
  constructor(expired?: (path: string) => void) { this.expired = expired; }
  bytes = 0;
  get size() { return this.entries.size; }
  get(path: string, now = Date.now()): boolean | undefined {
    const entry = this.entries.get(path);
    if (!entry) return undefined;
    if (entry.until <= now) {
      if (!entry.exists) { this.delete(path); this.expired?.(path); return undefined; }
      if (!entry.stale) { entry.stale = true; this.expired?.(path); }
    }
    this.entries.delete(path);
    this.entries.set(path, entry);
    return entry.exists;
  }
  /** The file `path` means when it is not itself one, as last answered. */
  target(path: string): string | undefined { return this.entries.get(path)?.target; }
  /** Whether the answer for `path` needs no recheck yet. */
  fresh(path: string, now = Date.now()): boolean {
    const entry = this.entries.get(path);
    return !!entry && entry.until > now;
  }
  set(path: string, exists: boolean, now = Date.now(), target?: string) {
    this.delete(path);
    // Count UTF-16 retained strings as well as fixed entry data, conservatively.
    const bytes = (path.length + (target?.length ?? 0)) * 2 + 64;
    if (bytes > FILE_CACHE_BYTES) return;
    this.entries.set(path, { exists, until: now + (exists ? 30_000 : 5_000), bytes, ...(target ? { target } : {}) });
    this.bytes += bytes;
    while (this.size > FILE_CACHE_ENTRIES || this.bytes > FILE_CACHE_BYTES) this.delete(this.entries.keys().next().value!);
  }
  delete(path: string) {
    this.bytes -= this.entries.get(path)?.bytes ?? 0;
    this.entries.delete(path);
  }
  clear() { this.entries.clear(); this.bytes = 0; }
}

export function fitsFileBatch(paths: string[], path: string): boolean {
  return paths.length < FILE_BATCH_PATHS && !!path && !path.includes('\0') && encoder.encode(path).length <= 4096 && encoder.encode(JSON.stringify({ paths: [...paths, path] })).length <= FILE_BODY_BYTES;
}

type Resolve = (paths: string[], signal: AbortSignal) => Promise<{ files: { path: string; exists: boolean; kind: 'file' | 'unavailable'; target?: string }[] }>;
interface Root { visible: boolean }

const MAX_TASK_CACHES = 32;
const taskCaches = new WeakMap<object, Map<string, FileCache>>();

/**
 * The answers for one Task folder, kept across lookup lifetimes (Thread switches, history
 * reloads) of the same API client, so an opened Thread shows its known links at once.
 */
export function taskFileCache(client: object, sessionId: string, workdir: string): FileCache {
  let caches = taskCaches.get(client);
  if (!caches) taskCaches.set(client, caches = new Map());
  const key = `${sessionId}\n${workdir}`;
  const cache = caches.get(key) ?? new FileCache();
  caches.delete(key);
  caches.set(key, cache);
  while (caches.size > MAX_TASK_CACHES) caches.delete(caches.keys().next().value!);
  return cache;
}

/** One selected task. Candidate text stays in the rendered DOM, never an unbounded request queue. */
export class FileReferences {
  readonly cache: FileCache;
  private readonly sources = new Map<string, { items: readonly Item[]; hints: Set<string> }>();
  private readonly roots = new Map<Element, Root>();
  private attempted = new WeakSet<Element>();
  private nearby = new WeakSet<Element>();
  private visibilityChanged = false;
  private readonly listeners = new Set<() => void>();
  /** How often each path was changed by a completed tool while this lookup lived, for open views to reload. */
  private readonly changes = new Map<string, number>();
  private readonly changeListeners = new Set<() => void>();
  private observer?: IntersectionObserver;
  private controller?: AbortController;
  private resolving = new Set<string>();
  private timer?: ReturnType<typeof setTimeout>;
  private retry?: ReturnType<typeof setTimeout>;
  private retryDelay = RETRY_MS;
  private scrollFrame?: number;
  private epoch = 0;
  private revision = 0;
  private active = false;
  readonly workdir: string;
  private readonly resolve: Resolve;
  constructor(workdir: string, resolve: Resolve, cache = new FileCache()) { this.workdir = workdir; this.resolve = resolve; this.cache = cache; }
  private readonly expired = (path: string) => {
    // Expiry permits a fresh lookup of this path, without retrying evictions or errors.
    for (const element of this.roots.keys()) this.forCandidates(element, candidate => {
      if (candidate.dataset.fileReference === path) this.attempted.delete(candidate);
    });
    this.demand();
  };
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  snapshot = () => this.revision;
  subscribeChanges = (listener: () => void) => { this.changeListeners.add(listener); return () => { this.changeListeners.delete(listener); }; };
  changeCount = (path: string) => this.changes.get(path) ?? 0;
  private emit() { this.revision++; this.listeners.forEach(listener => listener()); }
  /** A code span is looked up when it looks like a path; a bare name without an extension (`Makefile`) only when a tool named it. */
  eligible(text: string, path: string) { return text.includes('/') || looksLikePath(text) || [...this.sources.values()].some(source => source.hints.has(path)); }
  start() {
    this.active = true;
    this.cache.expired = this.expired;
    this.observer = new IntersectionObserver(entries => {
      let changed = false;
      for (const entry of entries) {
        const root = this.roots.get(entry.target);
        if (root && root.visible !== entry.isIntersecting) {
          root.visible = entry.isIntersecting;
          if (root.visible) this.forCandidates(entry.target, element => this.attempted.delete(element));
          changed = true;
        }
      }
      if (changed) this.demand();
    }, { rootMargin: '200px' });
    for (const element of this.roots.keys()) this.observer.observe(element);
    this.demand();
  }
  stop() {
    this.active = false;
    this.epoch++;
    this.controller?.abort();
    clearTimeout(this.timer); this.timer = undefined;
    clearTimeout(this.retry); this.retry = undefined;
    if (this.cache.expired === this.expired) this.cache.expired = undefined;
    if (this.scrollFrame !== undefined) cancelAnimationFrame(this.scrollFrame);
    this.scrollFrame = undefined;
    this.observer?.disconnect(); this.observer = undefined;
    this.sources.clear();
    this.attempted = new WeakSet();
    this.nearby = new WeakSet();
    this.emit();
  }
  observe(element: Element) {
    this.roots.set(element, { visible: false });
    this.observer?.observe(element);
    return () => {
      this.observer?.unobserve(element);
      this.roots.delete(element);
      this.demand();
    };
  }
  setItems(source: string, items: readonly Item[]) {
    const previous = this.sources.get(source);
    const oldById = new Map((previous?.items ?? []).map(item => [item.id, item]));
    const hints = new Set<string>();
    let changed = false;
    let invalidated = false;
    const edited: string[] = [];
    for (const item of items) {
      const before = oldById.get(item.id);
      const paths = localHints(item, this.workdir);
      paths.forEach(path => hints.add(path));
      if (before?.tool === item.tool) continue;
      const pathsChanged = JSON.stringify(paths) !== JSON.stringify(before ? localHints(before, this.workdir) : []);
      if (pathsChanged) changed = true;
      if (item.tool?.status === 'completed' && changingTools.has(item.tool.name) && (before?.tool?.status !== 'completed' || pathsChanged)) {
        for (const path of paths) this.cache.delete(path);
        if (paths.length) invalidated = true;
        // A source's first items are its history, not edits made while a view is open.
        if (previous) edited.push(...paths);
      }
    }
    if (previous && (previous.hints.size !== hints.size || [...previous.hints].some(path => !hints.has(path)))) changed = true;
    this.sources.set(source, { items, hints });
    if (invalidated) { this.epoch++; this.controller?.abort(); }
    if (edited.length) {
      for (const path of edited) this.changes.set(path, this.changeCount(path) + 1);
      this.changeListeners.forEach(listener => listener());
    }
    if (changed || invalidated) {
      this.attempted = new WeakSet();
      this.emit();
      this.demand();
    }
  }
  /** A closed source's hints stay for the Task's lifetime, so links that rely on them never drop back to code. */
  removeItems(source: string) {
    const previous = this.sources.get(source);
    if (previous?.items.length) this.sources.set(source, { items: [], hints: previous.hints });
  }
  focus = () => {
    this.attempted = new WeakSet();
    this.emit();
    this.demand();
  };
  scrolled = () => {
    // Coalesce scroll events without changing scrolling or anchoring behavior.
    if (!this.active || this.scrollFrame !== undefined) return;
    this.scrollFrame = requestAnimationFrame(() => {
      this.scrollFrame = undefined;
      this.visibilityChanged = true;
      this.demand();
    });
  };
  demand = () => {
    if (!this.active || this.timer !== undefined) return;
    this.timer = setTimeout(() => { this.timer = undefined; void this.scan(); }, 0);
  };
  private forCandidates(root: Element, visit: (element: HTMLElement) => void) {
    // The candidates are the transcript's own spans and divs (common.tsx), so `dataset` is there.
    root.querySelectorAll<HTMLElement>('[data-file-reference]').forEach(visit);
  }
  private near(candidate: Element): boolean {
    const rect = candidate.getBoundingClientRect();
    return !!candidate.getClientRects().length && rect.bottom >= -200 && rect.top <= window.innerHeight + 200;
  }
  private async scan() {
    if (!this.active) return;
    if (this.visibilityChanged) {
      this.visibilityChanged = false;
      for (const [element, root] of this.roots) {
        if (!root.visible) continue;
        this.forCandidates(element, candidate => {
          const near = this.near(candidate);
          if (near && !this.nearby.has(candidate)) this.attempted.delete(candidate);
          if (near) this.nearby.add(candidate); else this.nearby.delete(candidate);
        });
      }
    }
    if (this.controller) {
      let wanted = false;
      for (const [element, root] of this.roots) {
        if (root.visible) this.forCandidates(element, candidate => {
          if (this.resolving.has(candidate.dataset.fileReference ?? '') && this.near(candidate)) wanted = true;
        });
      }
      if (!wanted) { this.epoch++; this.controller.abort(); }
      return;
    }
    const paths: string[] = [];
    const included = new Set<string>();
    for (const [element, root] of this.roots) {
      if (!root.visible) continue;
      this.forCandidates(element, candidate => {
        const path = candidate.dataset.fileReference;
        if (!path || this.attempted.has(candidate)) return;
        // Keep marking duplicates after a full batch so a failed answer cannot retry
        // through another spelling of the same visible candidate. No layout reads then.
        if (included.has(path)) { this.attempted.add(candidate); return; }
        if (!fitsFileBatch(paths, path)) return;
        if (!this.near(candidate)) return;
        this.nearby.add(candidate);
        if (this.cache.fresh(path)) { this.attempted.add(candidate); return; }
        paths.push(path); included.add(path); this.attempted.add(candidate);
      });
    }
    if (!paths.length) return;
    const epoch = this.epoch;
    const controller = new AbortController();
    this.controller = controller;
    this.resolving = included;
    // Stop showing expired answers before the request returns.
    this.emit();
    try {
      const result = await this.resolve(paths, controller.signal);
      if (!this.active || controller.signal.aborted || epoch !== this.epoch) return;
      for (const file of result.files) {
        if (!included.has(file.path)) continue;
        if (file.exists === true && file.kind === 'file') this.cache.set(file.path, true);
        else if (file.exists === false && file.kind === 'file' && file.target) this.cache.set(file.path, true, undefined, file.target);
        else if (file.exists === false && file.kind === 'unavailable') this.cache.set(file.path, false);
      }
      this.retryDelay = RETRY_MS;
      this.emit();
    } catch (error) {
      // Never cache transport or auth failures. Retry the visible candidates with backoff,
      // as a slow or briefly unreachable instance must not leave them unlinked for good;
      // a refusal (an instance without file lookups) waits for new demand instead.
      const status = (error as { status?: unknown }).status;
      const transient = typeof status !== 'number' || status === 0 || status === 429 || status >= 500;
      if (transient && this.active && !controller.signal.aborted && this.retry === undefined) {
        this.retry = setTimeout(() => { this.retry = undefined; this.attempted = new WeakSet(); this.demand(); }, this.retryDelay);
        this.retryDelay = Math.min(this.retryDelay * 2, RETRY_MAX_MS);
      }
    } finally {
      if (this.controller === controller) { this.controller = undefined; this.resolving.clear(); }
      this.demand();
    }
  }
}
