import { DEFAULT_COMPACT_THRESHOLD, type Model, type SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { compactTokens, contextText, estimateTurnCost, formatCredits, ringFraction, ringTone } from '../lib/cost';
import { useApp } from './common';

const tones = { accent: 'text-accent', attention: 'text-attention', danger: 'text-error', muted: 'text-muted' };

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

/** The context ring, with a tick where compaction starts; null while no limit is reported. */
export function ContextRing({ session, className }: Readonly<{ session: SessionDetail; className?: string }>) {
  const threshold = useThreshold(session);
  const context = session.context;
  if (!context || !(context.limit > 0)) return null;
  const fraction = ringFraction(context);
  const tick = 2 * Math.PI * threshold;
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true" className={cn('size-4 shrink-0 -rotate-90', tones[ringTone(fraction)], className)} fill="none">
      <circle cx="8" cy="8" r="6" stroke="currentColor" strokeWidth="2" className="text-hairline-strong" />
      <circle cx="8" cy="8" r="6" pathLength="100" stroke="currentColor" strokeWidth="2" strokeDasharray={`${fraction * 100} 100`} />
      <line x1={8 + 4.25 * Math.cos(tick)} y1={8 + 4.25 * Math.sin(tick)} x2={8 + 7.75 * Math.cos(tick)} y2={8 + 7.75 * Math.sin(tick)} stroke="currentColor" strokeWidth="1.25" className="text-ink" data-compact-mark="" />
    </svg>
  );
}

/** The latest context report in lines; unknown values never become a zero estimate. */
export function ContextReport({ session, model }: Readonly<{ session: SessionDetail; model?: Model }>) {
  const threshold = useThreshold(session);
  const context = session.context;
  const cost = estimateTurnCost(model, context, session.context_size);
  return (
    <>
      <p>{contextLabel(session)}</p>
      {context && context.limit > 0 && <p className="text-caption text-muted">Compacts at {Math.round(threshold * 100)}% ({compactTokens(Math.round(context.limit * threshold))} tokens)</p>}
      {context?.prompt !== undefined && <p className="text-caption text-muted">Latest prompt: {compactTokens(context.prompt)} tokens</p>}
      {context?.cached !== undefined && <p className="text-caption text-muted">Cached in latest call: {compactTokens(context.cached)} tokens</p>}
      {session.capabilities.usage && <p className="text-caption">This task: {session.usage ? `${formatCredits(session.usage.ai_units)} AI units` : 'not reported yet'}</p>}
      {cost !== null && context && <p className="text-caption">≈ {formatCredits(cost)} credits per turn at {compactTokens(context.used)} context tokens, input only. Reported cached tokens use the cache-read price; actual usage may differ.</p>}
    </>
  );
}
