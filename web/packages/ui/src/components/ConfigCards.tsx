import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { X } from 'lucide-react';
import { useId, useRef, useState, type ReactNode } from 'react';
import { cn } from '../lib/cn';
import { Key } from './InlinePicker';
import { BottomSheet, LiftedRow } from './Subagents';
import { PHONE } from './Todos';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead } from './ui/panel';

/**
 * Settings → Agents, Skills, Hooks, Instructions and MCP servers (DESIGN.md Settings view): each
 * item is a card in a grid that fills the Settings card, the cards grouped by source under eyebrow
 * headings with their counts. A card opens its anchored reader (a bottom sheet on a phone) with the
 * item's full view and its actions; closing it gives focus back to the card.
 */

/** One source's cards: an eyebrow heading with the count, then the grid (one column on a phone, as many 16rem columns as fit). */
export function CardGroup({ label, count, children }: Readonly<{ label: string; count: number; children: ReactNode }>) {
  const id = useId();
  return (
    <div role="group" aria-labelledby={id} className="flex min-w-0 flex-col gap-2">
      <h3 id={id} className="text-eyebrow uppercase text-muted">
        {label} · <span className="tabular-nums">{count}</span>
      </h3>
      <ul className="grid grid-cols-[repeat(auto-fill,minmax(min(16rem,100%),1fr))] gap-2">{children}</ul>
    </div>
  );
}

/** The card a reader is open over, and what to run once it has closed (Edit opens its form then). */
export interface Opened<T> {
  item: T;
  card: HTMLElement;
  button: HTMLElement;
  open: boolean;
  phone: boolean;
  then?: () => void;
}

/** One reader at a time: `show` opens it over the card whose button was pressed; `close(then)` runs `then` after the exit, with focus back on the card. */
export function useCardReader<T>() {
  const id = useId();
  const [reader, setReader] = useState<Opened<T> | null>(null);
  return {
    id,
    reader,
    show: (item: T, button: HTMLElement) => setReader({ item, button, card: button.closest('li') ?? button, open: true, phone: window.matchMedia(PHONE).matches }),
    close: (then?: () => void) => setReader((r) => r && { ...r, open: false, then }),
    closed: () => {
      const r = reader;
      setReader(null);
      if (!r?.then) return;
      if (r.button.isConnected) r.button.focus();
      r.then();
    },
  };
}

/**
 * An item's card: a button over the whole card (Enter or Space opens the reader) with the name,
 * one or two lines of detail and state chips; `aside` (a switch) sits above the button's area.
 */
export function ItemCard({ name, label, detail, detailId, chips, aside, expanded, controls, onOpen }: Readonly<{
  name: ReactNode;
  /** The button's accessible name; it contains the visible name. */
  label: string;
  detail?: ReactNode;
  /** The detail line's id, for a control in `aside` that it describes. */
  detailId?: string;
  chips?: ReactNode;
  aside?: ReactNode;
  expanded: boolean;
  controls: string;
  onOpen: (button: HTMLButtonElement) => void;
}>) {
  const generated = useId();
  const describe = detailId ?? generated;
  return (
    <li className="lift flex min-w-0 flex-col gap-1 rounded-md bg-tint-well px-3 py-2.5">
      <div className="flex min-h-6 min-w-0 items-start gap-2">
        <button
          type="button"
          aria-label={label}
          aria-describedby={detail ? describe : undefined}
          aria-haspopup="dialog"
          aria-expanded={expanded}
          aria-controls={expanded ? controls : undefined}
          className="min-w-0 flex-1 text-left text-ui font-medium text-ink outline-hidden [overflow-wrap:anywhere] after:absolute after:inset-0 after:rounded-md after:content-[''] focus-visible:after:shadow-focus"
          onClick={(e) => onOpen(e.currentTarget)}
        >
          {name}
        </button>
        {aside && <div className="card-aside relative z-10 flex shrink-0 items-center">{aside}</div>}
      </div>
      {detail && <p id={describe} className="line-clamp-2 text-caption text-muted [overflow-wrap:anywhere]">{detail}</p>}
      {chips && <div className="-ml-0.5 flex min-w-0 flex-wrap gap-1">{chips}</div>}
    </li>
  );
}

/**
 * The anchored reader over a card (DESIGN.md Floating panels): a compact head with the name and a
 * line of facts, the body in eyebrow sections, and a foot with the Esc hint and the item's actions;
 * the card is lifted above the dimmed page. On a phone it is a bottom sheet with the actions under
 * the body. `children` may hold confirmations, so they nest inside it.
 */
export function ItemReader({ reader, id, label, title, facts, actions, onClose, onClosed, children }: Readonly<{
  reader: Opened<unknown>;
  id: string;
  label: string;
  title: ReactNode;
  facts?: ReactNode;
  actions?: ReactNode;
  onClose: () => void;
  onClosed: () => void;
  children: ReactNode;
}>) {
  const heading = useRef<HTMLHeadingElement>(null);
  const { card, button, open, phone } = reader;
  const sheet = phone;
  // Closing for a follow-up (Edit) leaves focus to it: `closed` puts it on the card, then the form takes it.
  const back = () => (!reader.then && button.isConnected ? button : false);
  const body = (close: ReactNode) => (
    <div className="flex min-h-0 flex-1 flex-col">
      <PanelHead className={cn('gap-0.5 pt-2 pb-2 pl-4', sheet ? 'pr-2' : 'pr-3')}>
        <div className="flex min-h-7 min-w-0 items-center gap-2">
          <h2 ref={heading} tabIndex={-1} className="min-w-0 flex-1 text-title text-ink outline-hidden [overflow-wrap:anywhere]">
            {title}
          </h2>
          {close}
        </div>
        {facts && <p className="text-caption text-muted [overflow-wrap:anywhere]">{facts}</p>}
      </PanelHead>
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-4">{children}</div>
      {sheet ? (
        actions && <div className="flex shrink-0 flex-wrap items-center gap-2 px-4 pt-1 pb-3">{actions}</div>
      ) : (
        <PanelFoot>
          <span className="flex items-center gap-1.5">
            <Key>Esc</Key>close
          </span>
          <span className="flex-1" />
          {actions}
        </PanelFoot>
      )}
    </div>
  );
  if (sheet) {
    return (
      <BottomSheet id={id} open={open} onClose={onClose} onClosed={onClosed} label={label} initialFocus={heading} finalFocus={back} className="duration-160" backdropClassName="duration-160">
        {body(
          <BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}>
            <X />
          </BaseDialog.Close>,
        )}
      </BottomSheet>
    );
  }
  return (
    <BasePopover.Root open={open} modal onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
      <BasePopover.Portal>
        <BasePopover.Backdrop className={`${backdropClass} duration-160`} />
        {/* The copy loses a switch's state attributes, so its switch is left out rather than shown wrong. */}
        {open && card.isConnected && <LiftedRow row={card} className="[&_.card-aside]:invisible" />}
        <BasePopover.Positioner anchor={card} side="bottom" align="start" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup
            id={id}
            data-popup=""
            aria-label={label}
            aria-modal="true"
            initialFocus={heading}
            finalFocus={back}
            className="flex max-h-[min(80vh,var(--available-height))] w-[560px] max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0"
          >
            {body(
              <BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}>
                <X />
              </BasePopover.Close>,
            )}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}
