import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { Package, X } from 'lucide-react';
import { useId, useRef, type ReactNode } from 'react';
import type { TurnTiming } from '../api';
import { cn } from '../lib/cn';
import { compactTokens, formatCredits } from '../lib/cost';
import { completedDuration, formatMs } from '../lib/transcript';
import { clockTime } from './common';
import { Key } from './InlinePicker';
import { BottomSheet, LiftedRow } from './Subagents';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead } from './ui/panel';

const plural = (n: number, one: string) => `${n.toLocaleString('en-US')} ${n === 1 ? one : `${one}s`}`;

/** One line per section from the timing alone; a section without a figure is left out. `nano_aiu` is in billionths of an AI credit. */
function lines(timing: TurnTiming): { label: string; value: ReactNode }[] {
  const input = timing.input_tokens ?? 0;
  const cached = timing.cache_read_tokens ?? 0;
  const share = input > 0 && cached > 0 ? Math.round((cached / input) * 100) : 0;
  const weight = [`${compactTokens(input)} in`, cached > 0 ? `${compactTokens(cached)} cache reads (${share}%)` : '', `${compactTokens(timing.output_tokens ?? 0)} out`].filter(Boolean).join(' · ');
  const credits = (timing.nano_aiu ?? 0) / 1e9;
  const premium = timing.premium_cost ?? 0;
  const postage = credits > 0
    ? <>{formatCredits(credits)} AI credits{premium > 0 && <> · {plural(premium, 'premium request')}</>}</>
    : <>Unpriced <span className="text-muted">· the provider reported no cost for this turn</span></>;
  const route = [
    timing.calls ? plural(timing.calls, 'model call') : '',
    timing.generation_ms ? `${formatMs(timing.generation_ms)} generating` : '',
    timing.paused_ms ? `${formatMs(timing.paused_ms)} paused` : '',
  ].filter(Boolean).join(' · ');
  return [
    { label: 'Weight', value: weight },
    ...(timing.model ? [{ label: 'Carrier', value: <span className="font-mono text-code-sm text-ink">{timing.model}</span> }] : []),
    { label: 'Postage', value: postage },
    ...(route ? [{ label: 'Route', value: route }] : []),
  ];
}

/** What the turn weighed, which model carried it, what it cost and how it went: uam's own count from the provider's usage events, no fetch. */
export function TurnWaybill({ timing, anchor, phone, open, onClose, onClosed }: Readonly<{
  timing: TurnTiming;
  anchor: HTMLElement | null;
  phone: boolean;
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
}>) {
  const heading = useRef<HTMLHeadingElement>(null);
  const id = useId();
  const took = completedDuration(timing);
  const facts = [`Ended ${clockTime(timing.ended_at ?? timing.started_at)}`, took ? `took ${took}` : ''].filter(Boolean).join(' · ');
  const sheet = phone || !anchor;
  const body = (close: ReactNode) => <div className="flex min-h-0 flex-col">
    <PanelHead className={cn('gap-1 pt-2 pb-2 pl-4', sheet ? 'pr-2' : 'pr-3')}>
      <div className="flex min-h-7 min-w-0 items-center gap-2">
        <Package aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <h2 ref={heading} tabIndex={-1} className="min-w-0 flex-1 truncate text-title text-ink outline-hidden">Waybill</h2>
        {close}
      </div>
      <p className="pl-6 text-caption tabular-nums text-muted">{facts}</p>
    </PanelHead>
    <dl className="grid grid-cols-[5.5rem_minmax(0,1fr)] items-baseline gap-x-3 gap-y-2.5 px-4 pt-1 pb-3">
      {lines(timing).map(({ label, value }) => <div key={label} className="contents">
        <dt className="text-eyebrow uppercase text-muted">{label}</dt>
        <dd className="min-w-0 text-ui tabular-nums text-body [overflow-wrap:anywhere]">{value}</dd>
      </div>)}
    </dl>
    {sheet
      ? <p className="flex min-h-11 shrink-0 items-center px-4 pb-2 text-caption text-muted">Counted by uam from the provider’s usage</p>
      : <PanelFoot><span className="flex items-center gap-1.5"><Key>Esc</Key>close</span><span className="flex-1" /><span>Counted by uam from the provider’s usage</span></PanelFoot>}
  </div>;
  const back = () => anchor;
  if (sheet) return <BottomSheet id={id} open={open} onClose={onClose} onClosed={onClosed} label="This turn's waybill" initialFocus={heading} finalFocus={back} className="duration-160" backdropClassName="duration-160">
    {body(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
  </BottomSheet>;
  return <BasePopover.Root open={open} modal onOpenChange={o => !o && onClose()} onOpenChangeComplete={o => !o && onClosed()}>
    <BasePopover.Portal>
      <BasePopover.Backdrop className={`${backdropClass} duration-160`} />
      {open && anchor.isConnected && <LiftedRow row={anchor} />}
      <BasePopover.Positioner anchor={anchor} side="bottom" align="start" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
        <BasePopover.Popup id={id} data-popup="" aria-label="This turn's waybill" aria-modal="true" initialFocus={heading} finalFocus={back} className="flex max-h-[min(80vh,var(--available-height))] w-[420px] max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0">
          {body(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
        </BasePopover.Popup>
      </BasePopover.Positioner>
    </BasePopover.Portal>
  </BasePopover.Root>;
}
