import { useEffect, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, LIVE, type SessionSummary, type TimeSplit } from '../api';
import { cn } from '../lib/cn';
import { formatMs } from '../lib/transcript';
import { PanelSection } from './ui/panel';
import { Tip } from './ui/tooltip';

/** The capability a connected instance needs for the split. */
export const TIME_SPLIT_CAPABILITY = 'time-split-v1';

/** Tool rows shown before the rest fold. */
const TOP_TOOLS = 5;

const number = new Intl.NumberFormat('en-US');
const time = (ms: number) => (ms <= 0 ? '0s' : formatMs(ms) ?? '0s');
const plural = (n: number, one: string, many = `${one}s`) => `${number.format(n)} ${n === 1 ? one : many}`;

/**
 * The Thread's split, read from its journal on mount and again when its state, usage or context report changes (as
 * the native metrics do); nothing polls. Null until read, and on a Thread without an id or a service without the split.
 */
function useTimeSplit(session: SessionSummary): { split: TimeSplit | null; error: string } {
  const api = useApi();
  const [split, setSplit] = useState<TimeSplit | null>(null);
  const [error, setError] = useState('');
  const supported = !!session.id && api.supports(TIME_SPLIT_CAPABILITY);
  const live = [session.usage?.ai_units, session.context?.used, session.state].join('\u0000');
  useEffect(() => {
    if (!supported) return;
    const controller = new AbortController();
    api.taskTimeSplit(session.id, controller.signal).then(
      (data) => { if (!controller.signal.aborted) { setSplit(data); setError(''); } },
      (err: unknown) => { if (!controller.signal.aborted) setError(describeError(err).slice(0, 512)); },
    );
    return () => controller.abort();
  }, [api, session.id, supported, live]);
  return supported ? { split, error } : { split: null, error: '' };
}

/** Whole percentages of model and local time, adding up to 100; null without both recorded or with neither. */
function shares(split: TimeSplit): { model: number; local: number } | null {
  if (split.model_ms === null) return null;
  const total = split.model_ms + split.local_ms;
  if (total <= 0) return null;
  const model = Math.round((split.model_ms / total) * 100);
  return { model, local: 100 - model };
}

const recorded = (split: TimeSplit) => split.model_calls > 0 || split.local_runs > 0 || split.waiting_ms > 0;

/** The tip's sentence: both times, and the waiting left out. */
function sentence(split: TimeSplit): string {
  const local = `${time(split.local_ms)} running local tools and scripts (${plural(split.local_runs, 'run')})`;
  const model = split.model_ms === null ? 'Model time is not recorded for this Thread\'s CLI version; ' : `${time(split.model_ms)} in the model (${plural(split.model_calls, 'call')}) and `;
  const waiting = split.waiting_ms > 0 ? ` ${time(split.waiting_ms)} waiting on you is not counted.` : '';
  return `${model}${local}.${waiting}`;
}

/** Model, local and waiting time as one stacked bar; colour follows the text beside it, never stands alone. */
function SplitBar({ split, waiting, className }: Readonly<{ split: TimeSplit; waiting: boolean; className?: string }>) {
  const model = split.model_ms ?? 0;
  const wait = waiting ? split.waiting_ms : 0;
  const total = model + split.local_ms + wait;
  if (total <= 0) return null;
  const part = (ms: number, tone: string) => ms > 0 && <span className={cn('h-full rounded-full', tone)} style={{ width: `${(ms / total) * 100}%` }} />;
  return (
    <span aria-hidden="true" className={cn('flex gap-0.5 overflow-hidden rounded-full', className)}>
      {part(model, 'bg-accent')}
      {part(split.local_ms, 'bg-success')}
      {part(wait, 'bg-hairline-strong')}
    </span>
  );
}

/**
 * The split at the end of the row under the composer: a 56px bar and "38% model · 62% local", with both times once the
 * Thread is idle. A phase shown before it sits at its left. Absent until something is recorded.
 */
export function TimeSplitSummary({ session }: Readonly<{ session: SessionSummary }>) {
  const { split } = useTimeSplit(session);
  if (!split || !recorded(split)) return null;
  const pct = shares(split);
  if (!pct && split.local_runs === 0) return null;
  const idle = !LIVE.includes(session.state);
  const tip = sentence(split);
  return (
    <Tip openOnClick label={tip}>
      <button type="button" aria-label={`Time in this Thread: ${tip}`} className="ml-auto flex h-7 min-w-0 shrink-0 cursor-default items-center gap-2 rounded-sm px-2 text-caption tabular-nums">
        {pct ? <>
          <SplitBar split={split} waiting={false} className="h-1 w-14 shrink-0" />
          <span className="truncate"><span className="text-accent">{pct.model}% model</span><span aria-hidden="true" className="text-faint"> · </span><span className="text-success">{pct.local}% local</span></span>
          {idle && <span className="shrink-0 text-muted">· {time(split.model_ms ?? 0)} / {time(split.local_ms)}</span>}
        </> : <span className="truncate text-muted">{time(split.local_ms)} local</span>}
      </button>
    </Tip>
  );
}

function Swatch({ tone }: Readonly<{ tone: string }>) {
  return <span aria-hidden="true" className={cn('inline-block size-2 shrink-0 rounded-xs', tone)} />;
}

/** The Usage tab's sections: the split with its legend and counts, then each tool's own local time. */
export function TimeSplitSection({ session }: Readonly<{ session: SessionSummary }>) {
  const { split, error } = useTimeSplit(session);
  if (!session.id) return null;
  if (error) return <PanelSection label="Time in this Thread"><p className="text-caption text-muted">{error}</p></PanelSection>;
  if (!split) return <PanelSection label="Time in this Thread"><p className="text-caption text-muted">Reading the session journal…</p></PanelSection>;
  if (!recorded(split)) return <PanelSection label="Time in this Thread"><p className="text-caption text-muted">Nothing recorded yet.</p></PanelSection>;
  const pct = shares(split);
  const rows: [string, string][] = [
    ['Model', split.model_ms === null ? 'Not recorded' : `${time(split.model_ms)} · ${plural(split.model_calls, 'call')}`],
    ['Local tools and scripts', `${time(split.local_ms)} · ${plural(split.local_runs, 'run')}`],
    ['Waiting on you', `${time(split.waiting_ms)} · ${plural(split.waits, 'request')}`],
  ];
  const scale = Math.max(split.local_ms, split.tools[0]?.ms ?? 0);
  const tool = (t: TimeSplit['tools'][number]) => (
    <div key={t.name} className="contents">
      <span className="min-w-0 truncate text-body">{t.name}</span>
      <span aria-hidden="true" className="h-1 overflow-hidden rounded-full bg-hairline"><span className="block h-full rounded-full bg-success" style={{ width: `${scale > 0 ? Math.min(100, (t.ms / scale) * 100) : 0}%` }} /></span>
      <span className="text-right text-body">{time(t.ms)}</span>
      <span className="text-right text-muted">{plural(t.runs, 'run')}</span>
    </div>
  );
  const grid = 'grid grid-cols-[minmax(0,8rem)_minmax(2rem,1fr)_auto_auto] items-center gap-x-3 gap-y-1.5 text-ui tabular-nums';
  return <>
    <PanelSection label="Time in this Thread">
      <SplitBar split={split} waiting className="mt-1 h-2" />
      <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted">
        {pct && <>
          <span className="inline-flex items-center gap-1.5"><Swatch tone="bg-accent" /><span className="font-medium text-ink">{pct.model}%</span> model</span>
          <span className="inline-flex items-center gap-1.5"><Swatch tone="bg-success" /><span className="font-medium text-ink">{pct.local}%</span> local</span>
        </>}
        {split.waiting_ms > 0 && <span className="inline-flex items-center gap-1.5"><Swatch tone="bg-hairline-strong" />waiting on you, not counted</span>}
      </p>
      {split.model_ms === null && <p className="text-caption text-muted">Model time not recorded for this Thread's CLI version.</p>}
      <dl className="mt-1 space-y-1 tabular-nums">{rows.map(([label, value]) => <div key={label} className="flex items-start justify-between gap-4"><dt className="text-muted">{label}</dt><dd className="shrink-0 text-body">{value}</dd></div>)}</dl>
    </PanelSection>
    {split.tools.length > 0 && <PanelSection label="Local time by tool">
      <div className={grid}>{split.tools.slice(0, TOP_TOOLS).map(tool)}</div>
      {split.tools.length > TOP_TOOLS && <details>
        <summary className="cursor-pointer text-caption text-muted">{plural(split.tools.length - TOP_TOOLS, 'more tool')}</summary>
        <div className={cn(grid, 'mt-1.5')}>{split.tools.slice(TOP_TOOLS).map(tool)}</div>
      </details>}
      <p className="text-caption text-muted">Each tool's own run time without waiting; runs that overlap count for each tool.</p>
    </PanelSection>}
  </>;
}
