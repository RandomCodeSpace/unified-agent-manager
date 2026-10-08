import { lazy, Suspense, useEffect, useId, useRef, useState } from 'react';
import { cn } from '../lib/cn';
import { accountQuota, quotaFace, quotaLabel, quotaPace } from '../lib/cost';
import { Skeleton, useApp } from './common';
import { Button } from './ui/button';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

/** The popover's content (Usage.tsx) loads with its first opening, never with the app. */
const UsageContent = lazy(() => import('./Usage').then((m) => ({ default: m.UsageContent })));

export const QUOTA_TONES = { healthy: 'text-success', attention: 'text-warning', danger: 'text-error', muted: 'text-muted' };

export function daysText(days: number, unit = 'day'): string {
  if (days < 1) return `less than 1 ${unit}`;
  const count = Math.round(days);
  return `${count} ${unit}${count === 1 ? '' : 's'}`;
}

export const QUOTA_FILLS = { healthy: 'bg-success', attention: 'bg-warning', danger: 'bg-error', muted: 'bg-faint' };

/** "Premium requests", "AI credits": the allowance's unit, as a label. */
export function allowanceName(type: string): string {
  const label = quotaLabel(type).replace(/\bai\b/, 'AI');
  return label.charAt(0).toUpperCase() + label.slice(1);
}

/** Pace words worth a place on the chip: anything but the calm ones, which the tip carries. */
const CHIP_PACE: Record<string, string> = { 'Ahead of pace': 'ahead', 'Allowance exhausted': 'used up', 'Previous quota; refresh failed': 'stale', 'Waiting for the refreshed allowance': 'resetting' };

/** A short meter of what is left: `hairline` track, the fill in the pace tone, drawn with `scaleX`. */
function Meter({ remaining, tone, className }: Readonly<{ remaining: number; tone: keyof typeof QUOTA_FILLS; className?: string }>) {
  return (
    <span aria-hidden="true" className={cn('relative block h-1 overflow-hidden rounded-full bg-hairline', className)}>
      <span className={cn('absolute inset-0 origin-left rounded-full transition-transform duration-300', QUOTA_FILLS[tone])} style={{ transform: `scaleX(${remaining / 100})` }} />
    </span>
  );
}

/**
 * Account allowance is live from SSE; recorded usage reads only while open. `chip` is the sidebar
 * footer's small pill (a meter and the share left, the pace word when it needs saying); `rail` is
 * the collapsed rail's stacked share and meter. Both carry the whole sentence in their tip.
 */
export function UsageButton({ side = 'top', variant = 'rail', className, onAddPrices }: Readonly<{ side?: 'top' | 'right'; variant?: 'chip' | 'rail'; className?: string; onAddPrices?: () => void }>) {
  const { usage } = useApp();
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60000);
    return () => window.clearInterval(timer);
  }, []);
  const quota = accountQuota(usage?.quotas);
  const pace = quotaPace(quota, now, usage?.stale);
  const known = !!quota && (quota.unlimited || pace.remaining !== null);
  const share = !quota || !known ? '—' : quota.unlimited ? '∞' : `${Math.round(pace.remaining ?? 0)}%`;
  const word = quota ? CHIP_PACE[pace.label] : undefined;
  const description = quota ? `${allowanceName(quota.type)}: ${quotaFace(quota)}. ${pace.label}${!quota.unlimited && pace.daysUntilReset !== null ? `. Resets in ${daysText(pace.daysUntilReset)}` : ''}` : 'Account usage unavailable';
  const descriptionId = useId();
  const [open, setOpen] = useState(false);
  const popup = useRef<HTMLDivElement>(null);
  const onOpenChange = (next: boolean) => setOpen(next);
  const meter = known && !quota.unlimited;
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Tip label={`Usage · ${description}`} side={side} disabled={open}>
      {variant === 'chip' ? (
        // A small pill like a chip: it reads as something to press, and lifts on hover.
        <Popover.Trigger render={<Button data-account-usage size="sm" aria-label="Usage" aria-describedby={descriptionId} className={cn('lift h-7 min-w-0 gap-1.5 rounded-full bg-raised px-2.5 font-normal shadow-raised hover:bg-raised data-popup-open:bg-tint-selected', className)} />}>
          {meter && <Meter remaining={pace.remaining ?? 0} tone={pace.tone} className="w-8 shrink-0" />}
          <span className={cn('shrink-0 text-caption font-medium tabular-nums', known ? 'text-ink' : 'text-muted')}>{known ? (quota.unlimited ? 'Unlimited' : `${share} left`) : 'Usage'}</span>
          {word && <span className={cn('min-w-0 truncate text-caption', QUOTA_TONES[pace.tone])}>{word}</span>}
        </Popover.Trigger>
      ) : (
        // The rail's: the share left in words over a short meter, as wide as the rail allows.
        <Popover.Trigger render={<Button data-account-usage aria-label="Usage" aria-describedby={descriptionId} className={cn('h-auto w-10 flex-col gap-1 px-0 py-1.5', className)} />}>
          <span className={cn('text-caption font-medium leading-none tabular-nums', pace.tone === 'healthy' ? 'text-ink' : QUOTA_TONES[pace.tone])}>{share}</span>
          {meter ? <Meter remaining={pace.remaining ?? 0} tone={pace.tone} className="w-6" /> : <span aria-hidden="true" className="block h-1" />}
        </Popover.Trigger>
      )}
    </Tip>
    <span id={descriptionId} className="sr-only">{description}</span>
    {/* As tall as its content up to 46rem, so a short model list leaves no blank space; past that only the model list scrolls. */}
    <Popover.Content ref={popup} initialFocus={popup} side={side} align={side === 'right' ? 'end' : 'start'} sideOffset={side === 'right' ? 12 : 6} className="w-[36rem] max-w-[calc(100vw-1rem)] max-h-[min(46rem,calc(100dvh-2rem),var(--available-height))] gap-0 overflow-hidden p-0 ">
      {open && <Suspense fallback={<Skeleton label="Loading usage…" rows={3} className="p-4" />}><UsageContent now={now} onClose={() => setOpen(false)} onAddPrices={onAddPrices} /></Suspense>}
    </Popover.Content>
  </Popover.Root>;
}
