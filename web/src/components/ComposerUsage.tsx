import { Coins } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { DEFAULT_COMPACT_THRESHOLD, type Model, type SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { compactTokens, contextText, creditsTone, estimateTurnCost, formatCredits, providerQuota, quotaFace, quotaText, ringFraction, ringTone } from '../lib/cost';
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
  const { usage, settings } = useApp();
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60000);
    return () => window.clearInterval(timer);
  }, []);
  const context = session.context;
  const fraction = ringFraction(context);
  const contextLabel = context && context.limit > 0 ? contextText(context) : 'Context usage not reported yet';
  // Where the conversation starts compacting, as a tick on the ring.
  const threshold = (settings.compact_threshold ?? DEFAULT_COMPACT_THRESHOLD) / 100;
  const tick = 2 * Math.PI * threshold;
  const quota = providerQuota(usage?.quotas, session.provider);
  const cost = estimateTurnCost(model, context, session.context_size);
  const stale = usage?.stale ? ' · stale' : '';
  const quotaLabel = quota ? `${quotaFace(quota)}${stale}` : 'Usage unavailable';
  const reset = quota?.reset_at && Date.parse(quota.reset_at) > now ? new Date(quota.reset_at).toLocaleString() : null;
  // The ring waits for a reported value; on a phone the credits are a glyph. The per-turn estimate lives in the credits
  // popover at every width, so the control row stays one row.
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
      </Value>}
      {session.capabilities.usage && (
        <Value id="composer-usage" label={`AI credits: ${quotaLabel}`} title="AI credits" face={<><Coins aria-hidden="true" className="size-4 text-faint sm:hidden" /><span className="max-sm:hidden">{quotaLabel}</span></>} className={quota ? tones[creditsTone(quota)] : 'text-muted'}>
          <p>{quota ? quotaText(quota) : 'The provider has not reported account usage.'}</p>
          {reset && <p className="text-caption text-muted">Resets {reset}</p>}
          <p className="text-caption">This task: {session.usage ? `${formatCredits(session.usage.ai_units)} AI units` : 'not reported yet'}</p>
          {cost !== null && <p className="text-caption">≈ {formatCredits(cost)} credits per turn at {compactTokens(context!.used)} context tokens, input only. Reported cached tokens use the cache-read price; actual usage may differ.</p>}
          {usage?.stale && <p className="text-caption text-attention">The last refresh failed. Showing the previous quota.</p>}
        </Value>
      )}
    </>
  );
}
