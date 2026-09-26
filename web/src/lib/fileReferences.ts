import type { Item } from '../api';
import { taskFile } from './markdown.ts';

export const FILE_CACHE_ENTRIES = 256;
export const FILE_CACHE_BYTES = 256 * 1024;
export const FILE_BATCH_PATHS = 64;
export const FILE_BODY_BYTES = 512 * 1024;
const encoder = new TextEncoder();
const localTools = new Set(['view', 'create', 'edit', 'apply_patch']);
const changingTools = new Set(['create', 'edit', 'apply_patch']);

export function hintPath(path: string, workdir: string): string | undefined {
  // Tool arguments are literal paths, unlike Markdown hrefs.
  return taskFile(path.split('/').map(encodeURIComponent).join('/'), workdir)?.path;
}
export function localHints(item: Item, workdir: string): string[] {
  if (!item.tool || !localTools.has(item.tool.name)) return [];
  return (item.tool.file_paths ?? []).flatMap(path => {
    const normalized = hintPath(path, workdir);
    return normalized ? [normalized] : [];
  });
}

/** Only resolver answers enter this cache. Errors remain unknown. */
export class FileCache {
  private entries = new Map<string, { exists: boolean; until: number; bytes: number }>();
  bytes = 0;
  get size() { return this.entries.size; }
  get(path: string, now = Date.now()): boolean | undefined {
    const entry = this.entries.get(path);
    if (!entry) return undefined;
    if (entry.until <= now) { this.delete(path); return undefined; }
    this.entries.delete(path);
    this.entries.set(path, entry);
    return entry.exists;
  }
  set(path: string, exists: boolean, now = Date.now()) {
    this.delete(path);
    // Count UTF-16 retained strings as well as fixed entry data, conservatively.
    const bytes = path.length * 2 + 64;
    if (bytes > FILE_CACHE_BYTES) return;
    this.entries.set(path, { exists, until: now + (exists ? 30_000 : 5_000), bytes });
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

type Resolve = (paths: string[], signal: AbortSignal) => Promise<{ files: { path: string; exists: boolean; kind: 'file' | 'unavailable' }[] }>;
interface Root { visible: boolean }

/** One selected task. Candidate text stays in the rendered DOM, never an unbounded request queue. */
export class FileReferences {
  readonly cache = new FileCache();
  private sources = new Map<string, { items: readonly Item[]; hints: Set<string> }>();
  private roots = new Map<Element, Root>();
  private attempted = new WeakSet<Element>();
  private nearby = new WeakSet<Element>();
  private visibilityChanged = false;
  private listeners = new Set<() => void>();
  private observer?: IntersectionObserver;
  private controller?: AbortController;
  private resolving = new Set<string>();
  private timer?: ReturnType<typeof setTimeout>;
  private scrollFrame?: number;
  private epoch = 0;
  private revision = 0;
  private active = false;
  readonly workdir: string;
  private resolve: Resolve;
  constructor(workdir: string, resolve: Resolve) { this.workdir = workdir; this.resolve = resolve; }
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  snapshot = () => this.revision;
  private emit() { this.revision++; this.listeners.forEach(listener => listener()); }
  eligible(text: string, path: string) { return text.includes('/') || [...this.sources.values()].some(source => source.hints.has(path)); }
  start() {
    this.active = true;
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
    if (this.scrollFrame !== undefined) cancelAnimationFrame(this.scrollFrame);
    this.scrollFrame = undefined;
    this.observer?.disconnect(); this.observer = undefined;
    this.cache.clear();
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
      }
    }
    if (previous && (previous.hints.size !== hints.size || [...previous.hints].some(path => !hints.has(path)))) changed = true;
    this.sources.set(source, { items, hints });
    if (invalidated) { this.epoch++; this.controller?.abort(); }
    if (changed || invalidated) {
      this.attempted = new WeakSet();
      this.emit();
      this.demand();
    }
  }
  removeItems(source: string) { if (this.sources.delete(source)) { this.emit(); this.demand(); } }
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
  private forCandidates(root: Element, visit: (element: Element) => void) {
    root.querySelectorAll('[data-file-reference]').forEach(visit);
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
          if (this.resolving.has(candidate.getAttribute('data-file-reference') ?? '') && this.near(candidate)) wanted = true;
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
        const path = candidate.getAttribute('data-file-reference');
        if (!path || this.attempted.has(candidate)) return;
        // Keep marking duplicates after a full batch so a failed answer cannot retry
        // through another spelling of the same visible candidate. No layout reads then.
        if (included.has(path)) { this.attempted.add(candidate); return; }
        if (!fitsFileBatch(paths, path)) return;
        if (!this.near(candidate)) return;
        this.nearby.add(candidate);
        if (this.cache.get(path) !== undefined) { this.attempted.add(candidate); return; }
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
        if (included.has(file.path) && ((file.exists === true && file.kind === 'file') || (file.exists === false && file.kind === 'unavailable'))) this.cache.set(file.path, file.exists);
      }
      this.emit();
    } catch {
      // A later visibility/focus demand may retry. Never cache transport or auth failures.
    } finally {
      if (this.controller === controller) { this.controller = undefined; this.resolving.clear(); }
      this.demand();
    }
  }
}
