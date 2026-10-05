import { type ReactNode } from 'react';
import { DEFAULT_COMPACT_THRESHOLD, type Model, type SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { compactTokens, contextText, estimateTurnCost, formatCredits, ringFraction, ringTone } from '../lib/cost';
import { useApp } from './common';
import { Button } from './ui/button';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

const tones = { accent: 'text-accent', attention: 'text-attention', danger: 'text-error', muted: 'text-muted' };

function Value({ id, label, title, face, children, className }: Readonly<{ id?: string; label: string; title: string; face: ReactNode; children: ReactNode; className?: string }>) {
  return (
    <Popover.Root>
      <Tip label={label}>
        <Popover.Trigger render={<Button id={id} size="sm" variant="subtle" aria-label={label} className={cn('px-1.5 text-caption tabular-nums pointer-coarse:min-w-11', className)} />}>
          {face}
        </Popover.Trigger>
      </Tip>
      <Popover.Content className="tabular-nums">
        <Popover.Title>{title}</Popover.Title>
        {children}
      </Popover.Content>
    </Popover.Root>
  );
}

/** Usage belongs beside the model; unknown values never become a zero estimate. */
export function ComposerUsage({ session, model }: Readonly<{ session: SessionDetail; model?: Model }>) {
  const { settings } = useApp();
  const context = session.context;
  const fraction = ringFraction(context);
  const contextLabel = context && context.limit > 0 ? contextText(context) : 'Context usage not reported yet';
  // Where the conversation starts compacting, as a tick on the ring: what the open conversation uses (a later
  // change in Settings reaches it when it reopens), else the setting it will open with.
  const threshold = (session.compact_threshold ?? settings.compact_threshold ?? DEFAULT_COMPACT_THRESHOLD) / 100;
  const tick = 2 * Math.PI * threshold;
  const cost = estimateTurnCost(model, context, session.context_size);
  return (
    <>
      {context && context.limit > 0 && <Value id="composer-context-usage" label={contextLabel} title="Context usage" className={tones[ringTone(fraction)]} face={
        <svg viewBox="0 0 16 16" aria-hidden="true" className="size-4 -rotate-90" fill="none">
          <circle cx="8" cy="8" r="6" stroke="currentColor" strokeWidth="2" className="text-hairline-strong" />
          <circle cx="8" cy="8" r="6" pathLength="100" stroke="currentColor" strokeWidth="2" strokeDasharray={`${fraction * 100} 100`} />
          <line x1={8 + 4.25 * Math.cos(tick)} y1={8 + 4.25 * Math.sin(tick)} x2={8 + 7.75 * Math.cos(tick)} y2={8 + 7.75 * Math.sin(tick)} stroke="currentColor" strokeWidth="1.25" className="text-ink" data-compact-mark="" />
        </svg>
      }>
        <p>{contextLabel}</p>
        <p className="text-caption text-muted">Compacts at {Math.round(threshold * 100)}% ({compactTokens(Math.round(context.limit * threshold))} tokens)</p>
        {context?.prompt !== undefined && <p className="text-caption text-muted">Latest prompt: {compactTokens(context.prompt)} tokens</p>}
        {context?.cached !== undefined && <p className="text-caption text-muted">Cached in latest call: {compactTokens(context.cached)} tokens</p>}
        {session.capabilities.usage && <p className="text-caption">This task: {session.usage ? `${formatCredits(session.usage.ai_units)} AI units` : 'not reported yet'}</p>}
        {cost !== null && <p className="text-caption">≈ {formatCredits(cost)} credits per turn at {compactTokens(context.used)} context tokens, input only. Reported cached tokens use the cache-read price; actual usage may differ.</p>}
      </Value>}
    </>
  );
}
