import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { X } from 'lucide-react';
import { useLayoutEffect, useRef, type ComponentProps, type ReactNode } from 'react';
import { cn } from '../../lib/cn';
import { Button } from './button';
import { backdropClass, readerPopupClass } from './dialog';

/**
 * Anchored readers (DESIGN.md Floating panels): a modal panel beside what opened it over the dim,
 * with that row lifted above the dim, or on a phone a bottom sheet. The parts are here, and
 * `AnchoredReader` puts them together.
 */

/**
 * The row an open panel belongs to, drawn again above the dim at the row's place: a still copy
 * (nothing in it is live or focusable), so the panel reads as anchored to it. The conversation
 * holds still under the modal panel; a resize places it again.
 */
export function LiftedRow({ row, className }: Readonly<{ row: Element; className?: string }>) {
  const host = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = host.current;
    if (!el) return;
    const copyRow = () => {
      const copy = row.cloneNode(true) as Element;
      for (const node of [copy, ...copy.querySelectorAll('*')]) {
        for (const attr of [...node.attributes]) if (attr.name === 'id' || attr.name.startsWith('data-') || attr.name.startsWith('aria-') || attr.name === 'tabindex') node.removeAttribute(attr.name);
      }
      el.replaceChildren(copy);
    };
    copyRow();
    // The copy sits in a portal, away from the row's parents: it takes the type they give the row (a turn foot's small stamp text), so it fits the row's box.
    const from = getComputedStyle(row.parentElement ?? row);
    Object.assign(el.style, { fontFamily: from.fontFamily, fontSize: from.fontSize, fontWeight: from.fontWeight, lineHeight: from.lineHeight, letterSpacing: from.letterSpacing, fontVariantNumeric: from.fontVariantNumeric, color: from.color });
    const place = () => {
      const r = row.getBoundingClientRect();
      Object.assign(el.style, { left: `${r.left}px`, top: `${r.top}px`, width: `${r.width}px`, height: `${r.height}px` });
    };
    place();
    // The row's time, tokens and line go on changing: the copy shows them, at the row's place and size (a chip's text can change its width).
    const changes = new MutationObserver(() => { copyRow(); place(); });
    changes.observe(row, { subtree: true, childList: true, characterData: true });
    window.addEventListener('resize', place);
    // A neighbour arriving beside the row (the strip's "Jump to bottom" arrow) moves it without a mutation inside it.
    const moves = new ResizeObserver(place);
    if (row.parentElement) moves.observe(row.parentElement);
    return () => {
      changes.disconnect();
      moves.disconnect();
      window.removeEventListener('resize', place);
    };
  }, [row]);
  return <div ref={host} aria-hidden="true" inert className={cn('pointer-events-none fixed z-40 overflow-hidden rounded-md bg-raised shadow-float [&>*]:m-0 [&>*]:size-full', className)} />;
}

/**
 * A sheet from the bottom over a dimmed page, at most 85% of the screen, under a grab handle:
 * dragging the handle down closes it, as do Esc and a close button inside. A phone opens a
 * subagent and the todo list in it. While the on-screen keyboard is open the sheet rests on it,
 * within what it leaves visible (lib/viewport).
 */
export function BottomSheet({ open, onClose, onClosed, label, className, backdropClassName, initialFocus, finalFocus, id, children }: Readonly<{ open: boolean; onClose: () => void; onClosed: () => void; label: string; className?: string; backdropClassName?: string; children: ReactNode } & Pick<ComponentProps<typeof BaseDialog.Popup>, 'initialFocus' | 'finalFocus' | 'id'>>) {
  const popup = useRef<HTMLDivElement>(null);
  const drag = useRef<{ y: number; dy: number } | null>(null);
  const follow = (dy: number) => {
    if (popup.current) popup.current.style.transform = dy > 0 ? `translateY(${dy}px)` : '';
  };
  return (
    <BaseDialog.Root open={open} onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
      <BaseDialog.Portal>
        <BaseDialog.Backdrop className={cn(backdropClass, backdropClassName)} />
        <BaseDialog.Popup
          ref={popup}
          id={id}
          data-popup=""
          aria-label={label}
          aria-modal="true"
          initialFocus={initialFocus}
          finalFocus={finalFocus}
          className={cn('fixed inset-x-0 bottom-[var(--app-bottom,0px)] z-50 flex max-h-[min(85dvh,var(--app-height,100dvh))] flex-col overflow-hidden rounded-t-lg bg-raised pb-[env(safe-area-inset-bottom)] text-body shadow-modal outline-hidden transition-transform duration-240 ease-app data-starting-style:translate-y-full data-ending-style:translate-y-full starting:translate-y-full', className)}
        >
          <div
            aria-hidden="true"
            className="flex h-6 shrink-0 touch-none items-center justify-center"
            onPointerDown={(e) => {
              drag.current = { y: e.clientY, dy: 0 };
              e.currentTarget.setPointerCapture(e.pointerId);
            }}
            onPointerMove={(e) => {
              if (!drag.current) return;
              drag.current.dy = e.clientY - drag.current.y;
              follow(drag.current.dy);
            }}
            onPointerUp={() => {
              const dy = drag.current?.dy ?? 0;
              drag.current = null;
              follow(0);
              if (dy > 80) onClose();
            }}
            onPointerCancel={() => {
              drag.current = null;
              follow(0);
            }}
          >
            <span className="h-1 w-9 rounded-full bg-hairline-strong" />
          </div>
          {children}
        </BaseDialog.Popup>
      </BaseDialog.Portal>
    </BaseDialog.Root>
  );
}

type VirtualAnchor = { getBoundingClientRect: () => DOMRect };

/** The reader's surface: a column at most 80% of the viewport tall and the room beside its anchor wide. */
const READER_SURFACE = cn('flex max-h-[min(80vh,var(--available-height))] max-w-(--available-width)', readerPopupClass);

/**
 * One anchored reader: on `sheet` (a phone, or no anchor) the bottom sheet, else a modal popover
 * placed against `anchor` on `side`, over the dim, with `lift` (the anchor by default) drawn above
 * the dim. Both are named `label`, carry `id`, take focus to `initialFocus` and give it back to
 * `finalFocus`; Esc, a press outside and the close button `children` is handed close it. The
 * popup is the reader surface capped at 80% of the viewport (`surface` replaces it), sized by
 * `className`; `sheetClassName` sizes the sheet.
 */
export function AnchoredReader({ id, open, sheet, anchor, lift = anchor instanceof Element ? anchor : null, liftClassName, label, side = 'bottom', align = 'start', sideOffset = 10, surface = READER_SURFACE, className, sheetClassName, initialFocus, finalFocus, onClose, onClosed, children }: Readonly<{
  id: string;
  open: boolean;
  sheet: boolean;
  /** An element or a virtual one (the strip's end); null opens the sheet. */
  anchor: Element | VirtualAnchor | (() => Element | VirtualAnchor | null) | null;
  lift?: Element | null;
  liftClassName?: string;
  label: string;
  side?: 'top' | 'bottom' | 'right';
  align?: 'start' | 'end';
  sideOffset?: number;
  surface?: string;
  className?: string;
  sheetClassName?: string;
  initialFocus?: ComponentProps<typeof BasePopover.Popup>['initialFocus'];
  finalFocus?: ComponentProps<typeof BasePopover.Popup>['finalFocus'];
  onClose: () => void;
  onClosed: () => void;
  children: (close: ReactNode) => ReactNode;
}>) {
  if (sheet || !anchor) {
    return (
      <BottomSheet id={id} open={open} onClose={onClose} onClosed={onClosed} label={label} className={cn('duration-160', sheetClassName)} backdropClassName="duration-160" initialFocus={initialFocus} finalFocus={finalFocus}>
        {children(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
      </BottomSheet>
    );
  }
  return (
    <BasePopover.Root open={open} modal onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
      <BasePopover.Portal>
        <BasePopover.Backdrop className={cn(backdropClass, 'duration-160')} />
        {lift?.isConnected && <LiftedRow row={lift} className={liftClassName} />}
        <BasePopover.Positioner anchor={anchor} side={side} align={align} sideOffset={sideOffset} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup id={id} data-popup="" aria-label={label} aria-modal="true" initialFocus={initialFocus} finalFocus={finalFocus} className={cn(surface, className)}>
            {children(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}
