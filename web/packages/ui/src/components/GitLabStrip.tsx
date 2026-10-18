import { Check, CircleCheck, CircleDashed, CircleDot, CircleX, GitPullRequest, GitPullRequestDraft, Layers, SquareCheck, X } from 'lucide-react';
import { createContext, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type MouseEvent, type RefObject } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type GitLabItemSummary, type GitLabPipelineState, type Item } from '../api';
import { cn } from '../lib/cn';
import { needsBody, refKey, refText, sameRef, turnGitLabRefs, type GitLabOrigin, type GitLabOwnPipeline, type GitLabRef } from '../lib/gitlabRefs';
import { useBodyRead } from './Details';
import { Blocks, type Block, type BlockTone } from './ui/blocks';
import type { ChipProps } from './ui/chip';

// GitLab from the conversation (DESIGN.md outcome bar): each reply that ended a turn which touched
// GitLab items carries them in its outcome bar, one chip per item with its state as blocks; a chip opens the item in the
// GitLab side panel, which steps between the turn's items. The Thread's own commit's merge
// requests keep their writes; every other item is read-only.

export function statusTone(status: string): ChipProps['tone'] {
  if (status === 'failed') return 'error';
  if (status === 'success' || status === 'opened') return 'success';
  if (status === 'merged') return 'accent';
  if (status === 'running' || status === 'pending') return 'accent';
  return 'muted';
}

export function statusLabel(status: string) {
  if (status === 'opened') return 'Open';
  const label = status.replaceAll('_', ' ');
  return label.charAt(0).toUpperCase() + label.slice(1);
}

/** What the Thread knows about its GitLab project, and how the conversation opens an item. */
export interface GitLabItems {
  sessionId: string;
  origin: GitLabOrigin;
  own?: GitLabOwnPipeline;
  state: GitLabPipelineState;
  /** The item open in the side panel, if any. */
  current?: GitLabRef;
  open: (refs: GitLabRef[], ref: GitLabRef) => void;
}

export const GitLabItemsContext = createContext<GitLabItems | null>(null);

/** The Thread's GitLab project, read once per Thread and again when a turn ends (the service reads no GitLab data for it). */
export function useGitLabOrigin(sessionId: string, working: boolean): GitLabPipelineState | undefined {
  const api = useApi();
  const supported = api.supports('gitlab-items-v1');
  const [state, setState] = useState<{ id: string; value: GitLabPipelineState }>();
  useEffect(() => {
    if (!supported || working) return;
    const controller = new AbortController();
    api.gitlabOrigin(sessionId, controller.signal).then((value) => setState({ id: sessionId, value }), () => {});
    return () => controller.abort();
  }, [api, supported, sessionId, working]);
  return state?.id === sessionId ? state.value : undefined;
}

/* ---------- item states: read in batches, kept live while on screen ---------- */

// An item an on-screen strip or the open panel shows is read again while the tab is visible: a
// running pipeline or job every 20 s, an open merge request or work item every minute. A finished
// item is not read again. Due items go to the service together, 20 to a request; the service
// shares one cached read per item between every viewer.
const RUNNING = new Set(['created', 'waiting_for_resource', 'preparing', 'pending', 'running', 'scheduled']);
const FAST = 20_000;
const SLOW = 60_000;
/** A tab that comes back reads again what is older than this. */
const FRESH = 15_000;
type Reader = (refs: string[]) => Promise<GitLabItemSummary[]>;
/** `rev` counts the reads since the first, so an open view knows to read its detail again. */
type Entry = { at: number; item?: GitLabItemSummary; loading?: boolean; rev: number };
interface Watch { scope: string; read: Reader; keys: string[]; visible: boolean }
const summaries = new Map<string, Entry>();
const watches = new Set<Watch>();
const listeners = new Set<() => void>();
let version = 0;
const changed = () => { version++; listeners.forEach((listener) => listener()); };
const subscribe = (listener: () => void) => { listeners.add(listener); return () => listeners.delete(listener); };

/** How long an item's state holds before it is read again; null once it no longer changes. */
function cadence(item?: GitLabItemSummary): number | null {
  if (!item) return 0;
  // An item GitLab would not give while rate limiting is asked again; the service answers it without GitLab.
  if (item.error) return item.stale ? SLOW : null;
  if (item.kind === 'pipeline' || item.kind === 'job') return RUNNING.has(item.state ?? '') ? FAST : null;
  return item.state === 'opened' ? SLOW : null;
}

const hidden = () => typeof document !== 'undefined' && document.visibilityState === 'hidden';
let timer: number | undefined;
let queued = false;
let listening = false;

function schedule() {
  if (queued) return;
  queued = true;
  queueMicrotask(() => { queued = false; pass(); });
}

/** Reads the on-screen items that are due; `within` reads sooner, as when the tab comes back. */
function pass(within?: number) {
  window.clearTimeout(timer);
  timer = undefined;
  if (hidden()) return;
  const now = Date.now();
  let next = Infinity;
  const due = new Map<string, { read: Reader; keys: Set<string> }>();
  for (const watch of watches) {
    if (!watch.visible) continue;
    for (const key of watch.keys) {
      const entry = summaries.get(`${watch.scope}\n${key}`);
      if (entry?.loading) continue;
      const every = cadence(entry?.item);
      if (every === null) continue;
      const wait = entry?.item ? entry.at + Math.min(every, within ?? every) - now : 0;
      if (wait > 0) { next = Math.min(next, wait); continue; }
      const batch = due.get(watch.scope) ?? { read: watch.read, keys: new Set<string>() };
      batch.keys.add(key);
      due.set(watch.scope, batch);
    }
  }
  for (const [scope, batch] of due) read(scope, batch.read, [...batch.keys]);
  if (next < Infinity) timer = window.setTimeout(() => pass(), next);
}

function read(scope: string, reader: Reader, keys: string[]) {
  for (const key of keys) {
    const entry = summaries.get(`${scope}\n${key}`);
    summaries.set(`${scope}\n${key}`, { at: entry?.at ?? 0, item: entry?.item, rev: entry?.rev ?? 0, loading: true });
  }
  for (let i = 0; i < keys.length; i += 20) {
    const chunk = keys.slice(i, i + 20);
    const settle = (answer: (key: string, before?: GitLabItemSummary) => GitLabItemSummary) => {
      const at = Date.now();
      for (const key of chunk) {
        const before = summaries.get(`${scope}\n${key}`);
        summaries.set(`${scope}\n${key}`, { at, item: answer(key, before?.item), rev: before?.item ? before.rev + 1 : 0 });
      }
    };
    reader(chunk).then((items) => {
      const byRef = new Map(items.map((item) => [item.ref, item]));
      settle((key, before) => {
        const item = byRef.get(key);
        // A stale answer the service had no state for keeps the state this page last saw.
        if (item?.stale && item.error && before && !before.error) return { ...before, stale: true };
        return item ?? before ?? { ref: key, kind: '', error: 'unavailable' };
      });
    }, (error: unknown) => {
      // A failed read keeps the last state, marked stale, and is asked again later.
      settle((key, before) => before && !before.error ? { ...before, stale: true } : { ref: key, kind: '', error: describeError(error), stale: true });
    }).finally(() => { changed(); schedule(); });
  }
}

function listen() {
  if (listening || typeof document === 'undefined') return;
  listening = true;
  const back = () => { if (!hidden()) pass(FRESH); };
  document.addEventListener('visibilitychange', () => (hidden() ? pass() : back()));
  window.addEventListener('focus', back);
}

/**
 * The state of each item, keyed by its reference, kept live while it is on screen: while
 * `target`'s element is in view, or always when there is no `target` (the open panel).
 */
export function useSummaries(sessionId: string, refs: GitLabRef[], target?: RefObject<Element | null>): Map<string, GitLabItemSummary | undefined> {
  const api = useApi();
  const scope = api.cacheKey(sessionId);
  useSyncExternalStore(subscribe, () => version);
  const keys = refs.map(refKey);
  const joined = keys.join(' ');
  useEffect(() => {
    if (!joined) return;
    listen();
    const element = target?.current;
    const observe = !!element && typeof IntersectionObserver !== 'undefined';
    const watch: Watch = { scope, read: async (chunk) => (await api.gitlabItems(sessionId, chunk)).items, keys: joined.split(' '), visible: !target || (!!element && !observe) };
    watches.add(watch);
    const observer = observe ? new IntersectionObserver(([entry]) => {
      if (watch.visible === entry.isIntersecting) return;
      watch.visible = entry.isIntersecting;
      schedule();
    }) : undefined;
    if (element) observer?.observe(element);
    schedule();
    return () => { observer?.disconnect(); watches.delete(watch); schedule(); };
  }, [api, scope, sessionId, joined, target]);
  return new Map(keys.map((key) => [key, summaries.get(`${scope}\n${key}`)?.item]));
}

/** How many times the item was read again since it was first shown: an open view reads its detail again when it changes. */
export function useSummaryRevision(sessionId: string, ref: GitLabRef): number {
  const scope = useApi().cacheKey(sessionId);
  return useSyncExternalStore(subscribe, () => summaries.get(`${scope}\n${refKey(ref)}`)?.rev ?? 0);
}

/* ---------- glyphs and words ---------- */

const TONE_TEXT = { success: 'text-success', error: 'text-error', accent: 'text-accent', muted: 'text-muted', warning: 'text-warning', attention: 'text-attention' } as const;
/** The state word's colour; the word itself carries the state. */
export const toneText = (status: string) => TONE_TEXT[statusTone(status) ?? 'muted'];

export function stateWord(item?: GitLabItemSummary): string {
  if (!item) return '';
  if (item.error) return 'unavailable';
  if (item.kind === 'mr' && item.draft && item.state === 'opened') return 'draft';
  return item.state ? statusLabel(item.state).toLowerCase() : '';
}

export function KindGlyph({ r, item, className }: Readonly<{ r: GitLabRef; item?: GitLabItemSummary; className?: string }>) {
  const c = cn('size-3.5 shrink-0', className);
  if (r.kind === 'pipeline' || r.kind === 'job') {
    if (item?.state === 'failed') return <CircleX aria-hidden="true" className={cn(c, 'text-error')} />;
    if (item?.state === 'success') return <CircleCheck aria-hidden="true" className={cn(c, 'text-success')} />;
    return <CircleDashed aria-hidden="true" className={cn(c, 'text-muted')} />;
  }
  if (r.kind === 'mr') return item?.draft ? <GitPullRequestDraft aria-hidden="true" className={cn(c, 'text-muted')} /> : <GitPullRequest aria-hidden="true" className={cn(c, 'text-muted')} />;
  if (r.kind === 'epic' || item?.kind === 'epic') return <Layers aria-hidden="true" className={cn(c, 'text-muted')} />;
  if (item?.kind === 'task') return <SquareCheck aria-hidden="true" className={cn(c, 'text-muted')} />;
  return <CircleDot aria-hidden="true" className={cn(c, 'text-muted')} />;
}

export const KIND_WORD: Record<string, string> = { mr: 'Merge request', issue: 'Issue', incident: 'Incident', task: 'Task', epic: 'Epic', pipeline: 'Pipeline', job: 'Job' };

/** The item's name as the strip and the panel write it. */
export function itemLabel(r: GitLabRef, item?: GitLabItemSummary, long = false): string {
  if (r.kind === 'pipeline') return `Pipeline #${r.id}`;
  if (r.kind === 'job') return item?.title || `Job #${r.id}`;
  return long ? `${KIND_WORD[item?.kind || r.kind] ?? 'Issue'} ${refText(r)}` : refText(r);
}

/** A modified or middle click keeps the browser's own handling: GitLab in a new tab. */
export const plainClick = (e: MouseEvent) => e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey;

/* ---------- the whole calls a strip reads ---------- */

// The compact view holds a call's output back; a strip reads the whole call once (DESIGN.md
// Receipts reads test output the same way) and keeps it, so scrolling back does not read again.
const BODY_LIMIT = 300;
const callBodies = new Map<string, Item>();
const bodyReads = new Set<string>();

function rememberBody(key: string, item: Item) {
  if (callBodies.size >= BODY_LIMIT) callBodies.delete(callBodies.keys().next().value!);
  const t = item.tool;
  callBodies.set(key, t ? { ...item, tool: { ...t, input: t.input?.slice(0, 64 << 10), output: (t.output ?? '').slice(0, 64 << 10) } } : item);
  changed();
}

/** Every GitLab item a turn touched, at its reply's end; `items` are its candidates (`gitLabCandidates`). */
export function TurnStrip({ items: calls }: Readonly<{ items: Item[] }>) {
  const gl = useContext(GitLabItemsContext);
  const read = useBodyRead();
  const seen = useSyncExternalStore(subscribe, () => version);
  const sessionId = gl?.sessionId ?? '';
  useEffect(() => {
    if (!sessionId || !read) return;
    for (const call of calls) {
      const key = `${sessionId}\n${call.id}\n${call.tool?.status}`;
      if (!needsBody(call) || callBodies.has(key) || bodyReads.has(key)) continue;
      bodyReads.add(key);
      read(call).then((whole) => rememberBody(key, whole), () => {}).finally(() => bodyReads.delete(key));
    }
  }, [calls, read, sessionId]);
  const origin = gl?.origin;
  const own = gl?.own;
  const refs = useMemo(() => {
    if (!origin) return [];
    void seen; // A body read since the last render changes what the calls show.
    return turnGitLabRefs(calls.map((call) => callBodies.get(`${sessionId}\n${call.id}\n${call.tool?.status}`) ?? call), origin, own);
  }, [calls, origin, own, sessionId, seen]);
  const box = useRef<HTMLSpanElement>(null);
  const items = useSummaries(sessionId, gl ? refs : [], box);
  if (!gl || !refs.length) return null;
  return (
    <span ref={box} role="group" aria-label="GitLab items in this turn" className="flex min-w-0 max-w-full flex-wrap items-center gap-1.5 font-sans">
      {refs.map((r) => {
        const item = items.get(refKey(r));
        const on = sameRef(gl.current, r);
        const body = <>
          <KindGlyph r={r} item={item} className="size-3" />
          <span className={cn('min-w-0 truncate font-semibold', r.kind === 'job' && 'font-mono font-medium')}>{itemLabel(r, item)}</span>
          <Blocks items={stateBlocks(item)} />
        </>;
        const className = "relative inline-flex h-6 min-w-0 max-w-full items-center gap-1.5 rounded-sm bg-raised pr-0.5 pl-2 text-caption text-ink shadow-raised transition-colors duration-100 after:absolute after:inset-x-0 after:-inset-y-1 after:content-[''] hover:bg-tint-hover aria-[current=true]:bg-tint-selected pointer-coarse:after:-inset-y-2.5";
        const name = item?.title && r.kind !== 'job' ? `${itemLabel(r, item, true)} · ${item.title}` : undefined;
        const title = item?.stale && !item.error ? `${name ? `${name} · ` : ''}Last known state: GitLab is rate limiting reads` : name;
        return item?.web_url
          ? <a key={refKey(r)} href={item.web_url} target="_blank" rel="noreferrer" aria-current={on || undefined} title={title} className={className} onClick={(e) => { if (!plainClick(e)) return; e.preventDefault(); gl.open(refs, r); }}>{body}</a>
          : <button key={refKey(r)} type="button" aria-current={on || undefined} title={title} className={className} onClick={() => gl.open(refs, r)}>{body}</button>;
      })}
    </span>
  );
}

const BLOCK_TONE: Record<NonNullable<ChipProps['tone']>, BlockTone> = { success: 'success', error: 'error', accent: 'accent', warning: 'warning', attention: 'warning', muted: 'none' };
const blockTone = (status: string): BlockTone => BLOCK_TONE[statusTone(status) ?? 'muted'];

/** An item's state as blocks: its state word, an open merge request's pipeline (a glyph and "pipeline", its state in words) and "stale" while GitLab rate limits reads. */
export function stateBlocks(item?: GitLabItemSummary): Block[] {
  const word = stateWord(item);
  if (!item || !word) return [];
  if (item.error) return [{ key: 'state', tone: 'none', label: word, title: word }];
  const out: Block[] = [{ key: 'state', tone: blockTone(item.draft ? 'draft' : item.state ?? ''), label: word, title: word }];
  if (item.pipeline_state && item.state === 'opened') {
    const pipeline = item.pipeline_state;
    out.push({ key: 'pipeline', tone: blockTone(pipeline), Icon: pipeline === 'success' ? Check : pipeline === 'failed' ? X : CircleDashed, label: 'pipeline', title: `pipeline ${statusLabel(pipeline).toLowerCase()}` });
  }
  if (item.stale) out.push({ key: 'stale', tone: 'none', label: 'stale', title: 'stale' });
  return out;
}

/** The open item's name and address in GitLab, for the panel's head. */
export function useGitLabItemHead(current?: GitLabRef): { label?: string; url?: string } {
  const gl = useContext(GitLabItemsContext);
  const items = useSummaries(gl?.sessionId ?? '', gl && current ? [current] : []);
  if (!current) return {};
  const item = items.get(refKey(current));
  return { label: current.kind === 'job' ? `Job ${item?.title ?? `#${current.id}`}` : itemLabel(current, item, true), url: item?.web_url };
}

