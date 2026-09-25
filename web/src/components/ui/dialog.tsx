import { AlertDialog as BaseAlertDialog } from '@base-ui/react/alert-dialog';
import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { X } from 'lucide-react';
import { useRef, type ComponentProps, type ReactNode, type RefObject } from 'react';
import { cn } from '../../lib/cn';
import { Button } from './button';

/**
 * Modal surfaces on Base UI (Level 3 in DESIGN.md: raised, `lg` corners, modal shadow with its
 * 1px ring, on the backdrop; they scale in from 0.97).
 * Focus is trapped, Esc closes, focus returns to the opener. `data-popup` marks every
 * popup so app-level Esc handlers stand back while one is open.
 */

const backdropClass = 'fixed inset-0 z-40 bg-backdrop transition-opacity duration-240 data-starting-style:opacity-0 data-ending-style:opacity-0';

const viewportClass = 'fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4 max-sm:items-end max-sm:p-0';

// Capped at the viewport less its 16px gutters: the title row stays and the body scrolls (Dialog) when the content is taller.
const popupClass =
  'relative flex max-h-[calc(100dvh-32px)] w-full max-w-sheet flex-col rounded-lg bg-raised p-5 text-body shadow-modal outline-hidden transition-[opacity,scale] duration-240 ease-app data-starting-style:scale-[0.97] data-starting-style:opacity-0 data-ending-style:scale-[0.97] data-ending-style:opacity-0 max-sm:max-w-none max-sm:rounded-b-none max-sm:pb-[max(20px,env(safe-area-inset-bottom))]';

export interface DialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Called once the exit transition has finished; the owner unmounts the dialog then. */
  onClosed?: () => void;
  /** The control that takes focus when the dialog opens (default: the first field or the close button). */
  initialFocus?: RefObject<HTMLElement | null>;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  className?: string;
  /** Rendered after the body, right-aligned. */
  footer?: ReactNode;
}

export function Dialog({ open, onOpenChange, onClosed, initialFocus, title, description, children, footer, className }: DialogProps) {
  return (
    <BaseDialog.Root open={open} onOpenChange={onOpenChange} onOpenChangeComplete={(o) => !o && onClosed?.()}>
      <BaseDialog.Portal>
        <BaseDialog.Backdrop className={backdropClass} />
        <BaseDialog.Viewport className={viewportClass}>
          <BaseDialog.Popup data-popup="" className={cn(popupClass, className)} initialFocus={initialFocus}>
            <div className="mb-4 flex shrink-0 items-start gap-3">
              <div className="min-w-0 flex-1">
                <BaseDialog.Title className="text-display-sm text-ink">{title}</BaseDialog.Title>
                {description && <BaseDialog.Description className="mt-1 text-ui text-muted">{description}</BaseDialog.Description>}
              </div>
              <BaseDialog.Close render={<Button size="icon" aria-label="Close" className="-mt-1 -mr-1 text-muted" />}>
                <X />
              </BaseDialog.Close>
            </div>
            {/* The scrolling body reaches the popup's edges (negative margins, padding back) so focus rings at the edges are not clipped. */}
            <div className="-mx-5 -my-1 min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 py-1">{children}</div>
            {footer && <div className="mt-5 flex shrink-0 flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">{footer}</div>}
          </BaseDialog.Popup>
        </BaseDialog.Viewport>
      </BaseDialog.Portal>
    </BaseDialog.Root>
  );
}

/**
 * An image viewer: no surface, the content on a dark see-through scrim, the title and close
 * in light text above it and `footer` right-aligned under it. A press outside the content closes it.
 */
export function ViewerDialog({ open, onOpenChange, onClosed, title, description, children, footer }: Omit<DialogProps, 'initialFocus' | 'className'>) {
  return (
    <BaseDialog.Root open={open} onOpenChange={onOpenChange} onOpenChangeComplete={(o) => !o && onClosed?.()}>
      <BaseDialog.Portal>
        <BaseDialog.Backdrop className={cn(backdropClass, 'bg-scrim')} />
        <BaseDialog.Viewport className="fixed inset-0 z-50 grid place-items-center p-4">
          <BaseDialog.Popup
            data-popup=""
            className="flex max-h-[calc(100dvh-32px)] max-w-[min(94vw,1400px)] min-w-0 flex-col gap-3 outline-hidden transition-[opacity,scale] duration-240 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0"
          >
            <div className="flex min-w-0 items-start gap-3 text-on-primary">
              <div className="min-w-0 flex-1">
                <BaseDialog.Title className="truncate text-title">{title}</BaseDialog.Title>
                {description && <BaseDialog.Description className="truncate text-caption text-on-primary/70">{description}</BaseDialog.Description>}
              </div>
              <BaseDialog.Close render={<Button size="icon" aria-label="Close" className="-mt-1 -mr-1 text-on-primary hover:bg-on-primary/15 active:bg-on-primary/25" />}>
                <X />
              </BaseDialog.Close>
            </div>
            {children}
            {footer && <div className="flex justify-end">{footer}</div>}
          </BaseDialog.Popup>
        </BaseDialog.Viewport>
      </BaseDialog.Portal>
    </BaseDialog.Root>
  );
}

/**
 * A command palette: the modal surface near the top of the screen, without the title row
 * or padding, so the body lays out its own search row, list and footer. `label` names it.
 * `finalFocus` follows Base UI: `false` leaves focus where the chosen action put it.
 */
export function CommandDialog({ open, onOpenChange, onClosed, initialFocus, finalFocus, label, className, children }: Omit<DialogProps, 'title' | 'description' | 'footer'> & { label: string; finalFocus?: ComponentProps<typeof BaseDialog.Popup>['finalFocus'] }) {
  return (
    <BaseDialog.Root open={open} onOpenChange={onOpenChange} onOpenChangeComplete={(o) => !o && onClosed?.()}>
      <BaseDialog.Portal>
        <BaseDialog.Backdrop className={backdropClass} />
        <BaseDialog.Viewport className={cn(viewportClass, 'sm:items-start sm:pt-[12dvh]')}>
          <BaseDialog.Popup data-popup="" aria-label={label} className={cn(popupClass, 'max-w-sheet-wide p-0', className)} initialFocus={initialFocus} finalFocus={finalFocus}>
            {children}
          </BaseDialog.Popup>
        </BaseDialog.Viewport>
      </BaseDialog.Portal>
    </BaseDialog.Root>
  );
}

export interface AlertDialogProps extends Omit<DialogProps, 'footer' | 'initialFocus'> {
  confirmLabel: string;
  danger?: boolean;
  busy?: boolean;
  disabled?: boolean;
  onConfirm: () => void;
  cancelLabel?: string;
}

/** Confirmation with the safe action focused first (DESIGN.md: initial focus on the least destructive button). */
export function AlertDialog({ open, onOpenChange, onClosed, title, description, children, className, confirmLabel, danger = true, busy = false, disabled = false, onConfirm, cancelLabel = 'Cancel' }: AlertDialogProps) {
  const cancel = useRef<HTMLButtonElement>(null);
  return (
    <BaseAlertDialog.Root open={open} onOpenChange={onOpenChange} onOpenChangeComplete={(o) => !o && onClosed?.()}>
      <BaseAlertDialog.Portal>
        <BaseAlertDialog.Backdrop className={backdropClass} />
        <BaseAlertDialog.Viewport className={viewportClass}>
          <BaseAlertDialog.Popup data-popup="" className={cn(popupClass, className)} initialFocus={cancel}>
            <BaseAlertDialog.Title className="text-display-sm text-ink">{title}</BaseAlertDialog.Title>
            {description && <BaseAlertDialog.Description className="mt-2 text-ui text-body">{description}</BaseAlertDialog.Description>}
            {children}
            <div className="mt-5 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
              <BaseAlertDialog.Close render={<Button variant="secondary" ref={cancel} />}>
                {cancelLabel}
              </BaseAlertDialog.Close>
              <Button variant={danger ? 'danger' : 'primary'} className={danger ? 'bg-sunken' : undefined} loading={busy} disabled={disabled} onClick={onConfirm}>
                {confirmLabel}
              </Button>
            </div>
          </BaseAlertDialog.Popup>
        </BaseAlertDialog.Viewport>
      </BaseAlertDialog.Portal>
    </BaseAlertDialog.Root>
  );
}

/** A panel sliding in from one edge: the narrow-layout drawer and the overlay side panels. `onClosed` fires after the exit. */
export function Sheet({
  open,
  onOpenChange,
  onClosed,
  side = 'left',
  label,
  className,
  children,
  ...props
}: Omit<ComponentProps<typeof BaseDialog.Popup>, 'render'> & { open: boolean; onOpenChange: (open: boolean) => void; onClosed?: () => void; side?: 'left' | 'right'; label: string }) {
  return (
    <BaseDialog.Root open={open} onOpenChange={onOpenChange} onOpenChangeComplete={(o) => !o && onClosed?.()}>
      <BaseDialog.Portal>
        <BaseDialog.Backdrop className={backdropClass} />
        <BaseDialog.Popup
          data-popup=""
          aria-label={label}
          className={cn(
            'fixed inset-y-0 z-50 flex w-drawer max-w-[calc(100vw-44px)] flex-col bg-rail pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] shadow-modal outline-hidden transition-transform duration-240 ease-app',
            side === 'left'
              ? 'left-0 pl-[env(safe-area-inset-left)] data-starting-style:-translate-x-full data-ending-style:-translate-x-full'
              : 'right-0 pr-[env(safe-area-inset-right)] data-starting-style:translate-x-full data-ending-style:translate-x-full',
            className,
          )}
          {...props}
        >
          {children}
        </BaseDialog.Popup>
      </BaseDialog.Portal>
    </BaseDialog.Root>
  );
}
