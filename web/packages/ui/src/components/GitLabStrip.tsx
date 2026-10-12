import { CircleCheck, CircleDashed, CircleDot, CircleX, GitPullRequest, GitPullRequestDraft, Layers, SquareCheck } from 'lucide-react';
import { createContext, useContext, useEffect, useMemo, useState, useSyncExternalStore, type MouseEvent } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type GitLabItemSummary, type GitLabPipelineState, type Item } from '../api';
import { cn } from '../lib/cn';
import { needsBody, refKey, refText, sameRef, turnGitLabRefs, type GitLabOrigin, type GitLabOwnPipeline, type GitLabRef } from '../lib/gitlabRefs';
import { useBodyRead } from './Details';
import type { ChipProps } from './ui/chip';

// GitLab from the conversation (DESIGN.md): each reply that ended a turn which touched GitLab items
// ends in a "GitLab" strip, one entry per item with its state word; an entry opens the item in the
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

/* ---------- item states, read in batches and kept for a minute ---------- */

const SUMMARY_TTL = 60_000;
const RUNNING = new Set(['created', 'waiting_for_resource', 'preparing', 'pending', 'running', 'scheduled']);
type Entry = { at: number; item?: GitLabItemSummary; loading?: boolean };
const summaries = new Map<string, Entry>();
const waiting = new Map<string, { read: (refs: string[]) => Promise<GitLabItemSummary[]>; refs: Set<string> }>();
const listeners = new Set<() => void>();
let version = 0;
const changed = () => { version++; listeners.forEach((listener) => listener()); };
const subscribe = (listener: () => void) => { listeners.add(listener); return () => listeners.delete(listener); };

function flush() {
  for (const [scope, batch] of waiting) {
    waiting.delete(scope);
    const refs = [...batch.refs];
    for (let i = 0; i < refs.length; i += 20) {
      const chunk = refs.slice(i, i + 20);
      batch.read(chunk).then((items) => {
        for (const item of items) summaries.set(`${scope}\n${item.ref}`, { at: Date.now(), item });
      }, (error: unknown) => {
        for (const ref of chunk) summaries.set(`${scope}\n${ref}`, { at: Date.now(), item: { ref, kind: '', error: describeError(error) } });
      }).finally(changed);
    }
  }
}

/** The references not held (or held over a minute) and not on their way, marked as on their way. */
function claim(scope: string, keys: string[]): string[] {
  const now = Date.now();
  return keys.filter((key) => {
    const entry = summaries.get(`${scope}\n${key}`);
    if (entry?.loading || (entry && now - entry.at <= SUMMARY_TTL)) return false;
    summaries.set(`${scope}\n${key}`, { ...entry, at: now, loading: true });
    return true;
  });
}

/** The state of each item, keyed by its reference; asks the service for the ones it does not hold. */
export function useSummaries(sessionId: string, refs: GitLabRef[]): Map<string, GitLabItemSummary | undefined> {
  const api = useApi();
  const scope = api.cacheKey(sessionId);
  useSyncExternalStore(subscribe, () => version);
  const keys = refs.map(refKey);
  const joined = keys.join(' ');
  // A pipeline or job still running is read again each minute while it is on screen.
  const [tick, setTick] = useState(0);
  const running = keys.some((key) => RUNNING.has(summaries.get(`${scope}\n${key}`)?.item?.state ?? ''));
  useEffect(() => {
    if (!running) return;
    const timer = window.setTimeout(() => setTick((n) => n + 1), SUMMARY_TTL + 1000);
    return () => window.clearTimeout(timer);
  }, [running, tick]);
  useEffect(() => {
    const missing = claim(scope, joined.split(' ').filter(Boolean));
    if (!missing.length) return;
    const batch = waiting.get(scope) ?? { read: async (chunk: string[]) => (await api.gitlabItems(sessionId, chunk)).items, refs: new Set<string>() };
    missing.forEach((key) => batch.refs.add(key));
    if (!waiting.size) queueMicrotask(flush);
    waiting.set(scope, batch);
  }, [api, scope, sessionId, joined, tick]);
  return new Map(keys.map((key) => [key, summaries.get(`${scope}\n${key}`)?.item]));
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
  const items = useSummaries(sessionId, gl ? refs : []);
  if (!gl || !refs.length) return null;
  return (
    <div role="group" aria-label="GitLab items in this turn" className="mt-3 flex flex-wrap items-center gap-1.5 font-sans">
      <span className="mr-1 text-eyebrow uppercase text-muted">GitLab</span>
      {refs.map((r) => {
        const item = items.get(refKey(r));
        const word = stateWord(item);
        const on = sameRef(gl.current, r);
        const body = <>
          <KindGlyph r={r} item={item} className="size-3" />
          <span className={cn('font-medium', r.kind === 'job' && 'font-mono')}>{itemLabel(r, item)}</span>
          {word && <span className={item?.error ? 'text-muted' : toneText(item?.draft ? 'draft' : item?.state ?? '')}>{word}</span>}
        </>;
        const className = 'inline-flex h-6 items-center gap-1.5 rounded-sm bg-raised px-2 text-caption text-ink shadow-raised transition-colors duration-100 hover:bg-tint-hover aria-[current=true]:bg-tint-selected';
        const title = item?.title && r.kind !== 'job' ? `${itemLabel(r, item, true)} · ${item.title}` : undefined;
        return item?.web_url
          ? <a key={refKey(r)} href={item.web_url} target="_blank" rel="noreferrer" aria-current={on || undefined} title={title} className={className} onClick={(e) => { if (!plainClick(e)) return; e.preventDefault(); gl.open(refs, r); }}>{body}</a>
          : <button key={refKey(r)} type="button" aria-current={on || undefined} title={title} className={className} onClick={() => gl.open(refs, r)}>{body}</button>;
      })}
    </div>
  );
}

/** The open item's name and address in GitLab, for the panel's head. */
export function useGitLabItemHead(current?: GitLabRef): { label?: string; url?: string } {
  const gl = useContext(GitLabItemsContext);
  const items = useSummaries(gl?.sessionId ?? '', gl && current ? [current] : []);
  if (!current) return {};
  const item = items.get(refKey(current));
  return { label: current.kind === 'job' ? `Job ${item?.title ?? `#${current.id}`}` : itemLabel(current, item, true), url: item?.web_url };
}

