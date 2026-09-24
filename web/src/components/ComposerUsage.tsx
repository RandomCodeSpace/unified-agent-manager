import { useEffect, useState, type ReactNode } from 'react';
import type { Model, SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { compactTokens, contextText, creditsTone, estimateTurnCost, formatCredits, providerQuota, quotaFace, quotaText, ringFraction, ringTone } from '../lib/cost';
import { useApp } from './common';
import { Button } from './ui/button';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

const tones = { accent: 'text-accent', attention: 'text-attention', danger: 'text-error', muted: 'text-muted' };

function Value({ label, title, face, children, className }: { label: string; title: string; face: ReactNode; children: ReactNode; className?: string }) {
  return (
    <Popover.Root>
      <Tip label={label}>
        <Popover.Trigger render={<Button size="sm" variant="subtle" aria-label={label} className={cn('px-1.5 text-caption tabular-nums pointer-coarse:min-w-11', className)} />}>
          {face}
        </Popover.Trigger>
      </Tip>
      <Popover.Content>
        <Popover.Title>{title}</Popover.Title>
        {children}
      </Popover.Content>
    </Popover.Root>
  );
}

/** Usage belongs beside the model; unknown values never become a zero estimate. */
export function ComposerUsage({ session, model }: { session: SessionDetail; model?: Model }) {
  const { usage } = useApp();
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60000);
    return () => window.clearInterval(timer);
  }, []);
  const context = session.context;
  const fraction = ringFraction(context);
  const contextLabel = context && context.limit > 0 ? contextText(context) : 'Context usage not reported yet';
  const quota = providerQuota(usage?.quotas, session.provider);
  const cost = estimateTurnCost(model, context, session.context_size);
  const quotaLabel = quota ? `${quotaFace(quota)}${usage?.stale ? ' · stale' : ''}` : 'Usage unavailable';
  const reset = quota?.reset_at && Date.parse(quota.reset_at) > now ? new Date(quota.reset_at).toLocaleString() : null;
  return (
    <>
      <Value label={contextLabel} title="Context usage" className={tones[ringTone(fraction)]} face={
        <svg viewBox="0 0 16 16" aria-hidden="true" className="size-4 -rotate-90" fill="none">
          <circle cx="8" cy="8" r="6" stroke="currentColor" strokeWidth="2" className="text-hairline-strong" />
          <circle cx="8" cy="8" r="6" pathLength="100" stroke="currentColor" strokeWidth="2" strokeDasharray={`${fraction * 100} 100`} />
        </svg>
      }>
        <p>{contextLabel}</p>
        {context?.prompt !== undefined && <p className="text-caption text-muted">Latest prompt: {compactTokens(context.prompt)} tokens</p>}
        {context?.cached !== undefined && <p className="text-caption text-muted">Cached in latest call: {compactTokens(context.cached)} tokens</p>}
      </Value>
      {session.capabilities.usage && <>
        <Value label={`AI credits: ${quotaLabel}`} title="AI credits" face={quotaLabel} className={quota ? tones[creditsTone(quota)] : 'text-muted'}>
          <p>{quota ? quotaText(quota) : 'The provider has not reported account usage.'}</p>
          {reset && <p className="text-caption text-muted">Resets {reset}</p>}
          <p className="text-caption">This task: {session.usage ? `${formatCredits(session.usage.ai_units)} AI units` : 'not reported yet'}</p>
          {usage?.stale && <p className="text-caption text-attention">The last refresh failed. Showing the previous quota.</p>}
        </Value>
        {cost !== null && <Value label={`Estimated input cost: ${formatCredits(cost)} credits per turn, excludes output`} title="Estimated input cost" className="text-muted" face={`≈ ${formatCredits(cost)} credits / turn`}>
          <p>At {compactTokens(context!.used)} context tokens. Excludes output.</p>
          <p className="text-caption text-muted">Reported cached tokens use the cache-read price. Actual usage may differ.</p>
        </Value>}
      </>}
    </>
  );
}
