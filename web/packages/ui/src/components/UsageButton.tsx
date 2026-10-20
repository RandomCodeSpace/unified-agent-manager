import { Cpu, Terminal } from 'lucide-react';
import { lazy, Suspense, useContext, useEffect, useId, useRef, useState } from 'react';
import type { ModelCalls, Quota } from '../api';
import { cn } from '../lib/cn';
import { accountQuota, quotaFace, quotaLabel, quotaPace } from '../lib/cost';
import { Skeleton, useApp } from './common';
import { ModelCallsContext, modelCallsLines } from './ModelPhase';
import { Button } from './ui/button';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

// The popover's content (Usage.tsx) loads apart from the app: with its first opening, or once the page is
// idle. The module, once here, is rendered directly: it never suspends, so an opening never waits for
// React's fallback throttle (about 300 ms) the lazy wrapper incurs on its first render.
let usageModule: typeof import('./Usage') | null = null;
const loadUsage = () => import('./Usage').then((m) => { usageModule = m; return m; });
const UsageContentLazy = lazy(() => loadUsage().then((m) => ({ default: m.UsageContent })));

export const QUOTA_TONES = { healthy: 'text-success', attention: 'text-warning', danger: 'text-error', muted: 'text-muted' };

export function daysText(days: number, unit = 'day'): string {
  if (days < 1) return `less than 1 ${unit}`;
  const count = Math.round(days);
  return `${count} ${unit}${count === 1 ? '' : 's'}`;
}

export const QUOTA_FILLS = { healthy: 'bg-success', attention: 'bg-warning', danger: 'bg-error', muted: 'bg-faint' };

/** "Premium requests", "AI credits": the allowance's unit, as a label. */
export function allowanceName(quota: Pick<Quota, 'type' | 'unit'>): string {
  const label = quotaLabel(quota).replace(/\bai\b/, 'AI');
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

/** The model calls in flight the button counts: the Threads' main agents, their subagents and Background AI. */
export const callsInFlight = (calls: ModelCalls) => calls.threads + calls.subagents + calls.background;

/**
 * The button's AI part: the chip and the model calls in flight in the accent, or "idle" by a faint chip while
 * none is (the rail, short of room, shows the chip alone); then, while any Thread runs a tool locally, the
 * terminal glyph and how many.
 */
function CallsFace({ calls, rail = false }: Readonly<{ calls: ModelCalls; rail?: boolean }>) {
  const count = callsInFlight(calls);
  return <>
    <span className="flex items-center gap-1">
      <Cpu aria-hidden="true" className={count ? 'text-accent' : 'text-faint'} />
      {count > 0 && <span className="font-medium text-accent tabular-nums">{count}</span>}
      {count === 0 && !rail && <span className="text-muted">idle</span>}
    </span>
    {calls.local > 0 && <span className="flex items-center gap-1"><Terminal aria-hidden="true" className="text-success" /><span className="font-medium text-success tabular-nums">{calls.local}</span></span>}
  </>;
}

/**
 * The sidebar foot's one button for the account allowance and the work in flight, flat like Settings beside it: no
 * surface at rest, the tint on hover and while open. `chip` is one row: a short meter and the share left (the pace
 * word when it needs saying), a hairline, then the model calls in flight and the Threads running locally. `rail` is
 * the collapsed rail's stack of the same. Without `model_calls` (an older service) it is the allowance alone. The tip
 * and the description say all of it in words. Pressing it opens Usage, whose Now section names the Threads.
 * The allowance is live from SSE; recorded usage reads only while open.
 */
export function UsageButton({ side = 'top', variant = 'rail', className, onAddPrices }: Readonly<{ side?: 'top' | 'right'; variant?: 'chip' | 'rail'; className?: string; onAddPrices?: () => void }>) {
  const { usage } = useApp();
  const calls = useContext(ModelCallsContext);
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60000);
    return () => window.clearInterval(timer);
  }, []);
  // Fetched once the page is idle, so the first opening is as fast as the next.
  useEffect(() => {
    if (usageModule) return;
    const idle = window.requestIdleCallback ?? ((cb: () => void) => window.setTimeout(cb, 1000));
    const cancel = window.cancelIdleCallback ?? window.clearTimeout;
    const handle = idle(() => { void loadUsage(); });
    return () => cancel(handle);
  }, []);
  const quota = accountQuota(usage?.quotas);
  const pace = quotaPace(quota, now, usage?.stale);
  const known = !!quota && (quota.unlimited || pace.remaining !== null);
  const share = !quota || !known ? '—' : quota.unlimited ? '∞' : `${Math.round(pace.remaining ?? 0)}%`;
  const word = quota ? CHIP_PACE[pace.label] : undefined;
  const description = quota ? `${allowanceName(quota)}: ${quotaFace(quota)}. ${pace.label}${!quota.unlimited && pace.daysUntilReset !== null ? `. Resets in ${daysText(pace.daysUntilReset)}` : ''}` : 'Account usage unavailable';
  const descriptionId = useId();
  const [open, setOpen] = useState(false);
  const popup = useRef<HTMLDivElement>(null);
  // Chosen as it opens: turning from the lazy wrapper to the module while open would remount the content.
  const [UsageContent, setUsageContent] = useState(() => usageModule?.UsageContent ?? UsageContentLazy);
  const onOpenChange = (next: boolean) => {
    if (next) setUsageContent(() => usageModule?.UsageContent ?? UsageContentLazy);
    setOpen(next);
  };
  const meter = known && !quota.unlimited;
  const lines = calls ? modelCallsLines(calls) : [];
  const count = calls ? callsInFlight(calls) : undefined;
  const label = calls ? 'Usage and AI' : 'Usage';
  const tip = <><span className="block">Usage · {description}</span>{lines.map((line) => <span key={line} className="block text-on-primary/70">{line}</span>)}</>;
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Tip label={tip} side={side} disabled={open}>
      {variant === 'chip' ? (
        // 28px like the buttons beside it; a touch's hit area grows to 44px (`sm`).
        <Popover.Trigger render={<Button data-account-usage data-model-calls={count} size="sm" aria-label={label} aria-describedby={descriptionId} className={cn('min-w-0 gap-1.5 px-2 text-caption font-normal data-popup-open:bg-tint-selected [&_svg]:size-3.5', className)} />}>
          {meter && <Meter remaining={pace.remaining ?? 0} tone={pace.tone} className="w-7 shrink-0" />}
          <span className={cn('shrink-0 font-medium tabular-nums', known ? 'text-ink' : 'text-muted')}>{known ? (quota.unlimited ? 'Unlimited' : `${share} left`) : 'Usage'}</span>
          {word && <span className={cn('min-w-0 truncate', QUOTA_TONES[pace.tone])}>{word}</span>}
          {calls && <><span aria-hidden="true" className="h-3.5 w-px shrink-0 bg-hairline-strong" /><CallsFace calls={calls} /></>}
        </Popover.Trigger>
      ) : (
        // The rail's: the share left in words over a short meter, then the work in flight, as wide as the rail allows.
        <Popover.Trigger render={<Button data-account-usage data-model-calls={count} aria-label={label} aria-describedby={descriptionId} className={cn('h-auto w-10 flex-col gap-1 px-0 py-1.5 text-caption leading-none data-popup-open:bg-tint-selected [&_svg]:size-3.5', className)} />}>
          <span className={cn('font-medium tabular-nums', pace.tone === 'healthy' ? 'text-ink' : QUOTA_TONES[pace.tone])}>{share}</span>
          {meter ? <Meter remaining={pace.remaining ?? 0} tone={pace.tone} className="w-6" /> : <span aria-hidden="true" className="block h-1" />}
          {calls && <CallsFace calls={calls} rail />}
        </Popover.Trigger>
      )}
    </Tip>
    <span id={descriptionId} className="sr-only">{[description, ...lines].join('. ')}</span>
    {/* As tall as its content up to 50rem, room for Now above the allowance and the top five models, so a short model list leaves no blank space; past that only the model list scrolls. */}
    <Popover.Content ref={popup} initialFocus={popup} side={side} align={side === 'right' ? 'end' : 'start'} sideOffset={side === 'right' ? 12 : 6} className="w-[36rem] max-w-[calc(100vw-1rem)] max-h-[min(50rem,calc(100dvh-2rem),var(--available-height))] gap-0 overflow-hidden p-0 ">
      {open && <Suspense fallback={<Skeleton label="Loading usage…" rows={3} className="p-4" />}><UsageContent now={now} onClose={() => setOpen(false)} onAddPrices={onAddPrices} /></Suspense>}
    </Popover.Content>
  </Popover.Root>;
}
