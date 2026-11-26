import { DEFAULT_COMPACT_THRESHOLD, type Model, type SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { contextText, estimateTurnCost, formatCredits, ringFraction, ringTone } from '../lib/cost';
import { Num, useApp } from './common';

const tones = { accent: 'text-accent', attention: 'text-attention', danger: 'text-error', muted: 'text-muted' };
const fills = { accent: 'bg-accent', attention: 'bg-attention', danger: 'bg-error', muted: 'bg-muted' };

/** Whether the Task has a context report to show: a reported limit, or a provider that reads its breakdown on demand. */
export function contextShown(session: SessionDetail): boolean {
  return !!(session.context && session.context.limit > 0) || !!session.capabilities.context_breakdown;
}

/** The context in words: the reported share, or that none is reported yet. */
export function contextLabel(session: SessionDetail): string {
  return session.context && session.context.limit > 0 ? contextText(session.context) : 'Context usage not reported yet';
}

/**
 * Where the conversation starts compacting: what the open conversation uses (a later change in
 * Settings reaches it when it reopens), else the setting it will open with.
 */
function useThreshold(session: SessionDetail): number {
  const { settings } = useApp();
  return (session.compact_threshold ?? settings.compact_threshold ?? DEFAULT_COMPACT_THRESHOLD) / 100;
}

/**
 * The context on the Tools button: a flat bar with a tick where compaction starts, and its share, which gains "context"
 * and the `attention` tone from 80% and `danger` from 95%. A folded row (a phone) keeps the bar. Null while no limit is
 * reported. A bar, not a ring: the Working mark is the one ring on screen.
 */
export function ContextMeter({ session }: Readonly<{ session: SessionDetail }>) {
  const threshold = useThreshold(session);
  const context = session.context;
  if (!context || !(context.limit > 0)) return null;
  const fraction = ringFraction(context);
  const tone = ringTone(fraction);
  const pct = Math.round(fraction * 100);
  return (
    <>
      <span aria-hidden="true" className="relative h-1 w-9 shrink-0 rounded-full bg-hairline-strong max-sm:w-7">
        <span className={cn('block h-full rounded-full', fills[tone])} style={{ width: `${pct}%` }} />
        <span data-compact-mark="" className="absolute -top-0.5 h-2 w-[1.25px] bg-ink" style={{ left: `${threshold * 100}%` }} />
      </span>
      <span aria-hidden="true" className={cn('text-caption tabular-nums in-data-[fold~=model]:hidden max-sm:hidden', tone === 'accent' ? 'text-muted' : tones[tone])}>
        {tone === 'accent' ? `${pct}%` : `${pct}% context`}
      </span>
    </>
  );
}

/**
 * The latest context report's detail lines, under the Context tab's headline; unknown values never become a zero estimate.
 * `compactAt` is the CLI's own compaction threshold, which wins over the one computed from the setting.
 */
export function ContextReport({ session, model, compactAt }: Readonly<{ session: SessionDetail; model?: Model; compactAt?: number }>) {
  const threshold = useThreshold(session);
  const context = session.context;
  const cost = estimateTurnCost(model, context, session.context_size);
  return (
    <>
      {compactAt ? <p className="text-caption text-muted">Compacts at <Num value={compactAt} /> tokens</p>
        : context && context.limit > 0 && <p className="text-caption text-muted">Compacts at {Math.round(threshold * 100)}% (<Num value={Math.round(context.limit * threshold)} /> tokens)</p>}
      {context?.prompt !== undefined && <p className="text-caption text-muted">Latest prompt: <Num value={context.prompt} /> tokens</p>}
      {context?.cached !== undefined && <p className="text-caption text-muted">Cached in latest call: <Num value={context.cached} /> tokens</p>}
      {cost !== null && context && <p className="text-caption">Estimate: ≈ <Num value={cost} format={formatCredits} /> AI credits per model request at <Num value={context.used} /> context tokens, input only. A prompt makes one or more requests. Reported cached tokens use the cache-read price; actual usage may differ.</p>}
    </>
  );
}
