import { Cpu, Terminal } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, LIVE, type SessionSummary, type TimeSplit } from '../api';
import { cn } from '../lib/cn';
import { formatMs } from '../lib/transcript';
import { Blocks, type Block } from './ui/blocks';
import { Tip } from './ui/tooltip';

/** The capability a connected instance needs for the split. */
export const TIME_SPLIT_CAPABILITY = 'time-split-v1';

/** Tool rows shown before the rest fold. */
const TOP_TOOLS = 5;

const number = new Intl.NumberFormat('en-US');
export const time = (ms: number) => (ms <= 0 ? '0s' : formatMs(ms) ?? '0s');
export const plural = (n: number, one: string, many = `${one}s`) => `${number.format(n)} ${n === 1 ? one : many}`;

/**
 * The Thread's split, read from its journal on mount and again when its state, usage or context report changes (as
 * the native metrics do); nothing polls. `supported` is false on a Thread without an id or a service without the split.
 */
export function useTimeSplit(session: SessionSummary): { supported: boolean; split: TimeSplit | null; error: string } {
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
  return supported ? { supported, split, error } : { supported, split: null, error: '' };
}

/** Whole percentages of model and local time, adding up to 100; null without both recorded or with neither. */
export function shares(split: TimeSplit): { model: number; local: number } | null {
  if (split.model_ms === null) return null;
  const total = split.model_ms + split.local_ms;
  if (total <= 0) return null;
  const model = Math.round((split.model_ms / total) * 100);
  return { model, local: 100 - model };
}

const recorded = (split: TimeSplit) => split.model_calls > 0 || split.local_runs > 0 || split.waiting_ms > 0;

/** The tip's sentence: both times, and the waiting left out. Model calls include compaction, which the service counts. */
function sentence(split: TimeSplit): string {
  const local = `${time(split.local_ms)} running local tools and scripts (${plural(split.local_runs, 'run')})`;
  const model = split.model_ms === null ? 'Model time is not recorded for this Thread\'s CLI version; ' : `${time(split.model_ms)} in the model (${plural(split.model_calls, 'call')}, compaction included) and `;
  const waiting = split.waiting_ms > 0 ? ` ${time(split.waiting_ms)} waiting on you is not counted.` : '';
  return `${model}${local}.${waiting}`;
}

/**
 * The split at the end of the row under the composer, as two blocks: `Cpu` model and `Terminal` local, their shares
 * while the Thread works and their times once it is idle (only local's time where model time is not recorded). A
 * phase shown before it sits at its left. The whole sentence is its tip and its name. Absent until something is recorded.
 */
export function TimeSplitSummary({ session }: Readonly<{ session: SessionSummary }>) {
  const { split } = useTimeSplit(session);
  if (!split || !recorded(split)) return null;
  const pct = shares(split);
  if (!pct && split.local_runs === 0) return null;
  const idle = !LIVE.includes(session.state);
  const tip = sentence(split);
  const items: Block[] = pct ? [
    { key: 'model', tone: 'accent', Icon: Cpu, label: idle ? time(split.model_ms ?? 0) : `${pct.model}%`, title: idle ? `${time(split.model_ms ?? 0)} model` : `${pct.model}% model` },
    { key: 'local', tone: 'success', Icon: Terminal, label: idle ? time(split.local_ms) : `${pct.local}%`, title: idle ? `${time(split.local_ms)} local` : `${pct.local}% local` },
  ] : [{ key: 'local', tone: 'success', Icon: Terminal, label: time(split.local_ms), title: `${time(split.local_ms)} local` }];
  return (
    <Tip openOnClick label={tip}>
      <button type="button" aria-label={`Time in this Thread: ${tip}`} className="flex h-7 shrink-0 cursor-default items-center rounded-sm">
        <Blocks items={items} />
      </button>
    </Tip>
  );
}

/** Each tool's own local time on a thin `success` bar: the top rows, the rest folded, then the caveat. */
export function ToolTimes({ split }: Readonly<{ split: TimeSplit }>) {
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
    <div className={grid}>{split.tools.slice(0, TOP_TOOLS).map(tool)}</div>
    {split.tools.length > TOP_TOOLS && <details className="mt-1.5">
      <summary className="cursor-pointer text-caption text-muted">{plural(split.tools.length - TOP_TOOLS, 'more tool')}</summary>
      <div className={cn(grid, 'mt-1.5')}>{split.tools.slice(TOP_TOOLS).map(tool)}</div>
    </details>}
    <p className="mt-1.5 text-caption text-muted">Each tool's own run time without waiting; runs that overlap count for each tool.</p>
  </>;
}
