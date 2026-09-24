import { AlertDialog as BaseAlertDialog } from '@base-ui/react/alert-dialog';
import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { X } from 'lucide-react';
import { useRef, type ComponentProps, type ReactNode, type RefObject } from 'react';
import { cn } from '../../lib/cn';
import { Button } from './button';

/**
 * Modal surfaces on Base UI (Level 3 in DESIGN.md: raised, modal shadow, on the backdrop).
 * Focus is trapped, Esc closes, focus returns to the opener. `data-popup` marks every
 * popup so app-level Esc handlers stand back while one is open.
 */

const backdropClass = 'fixed inset-0 z-40 bg-backdrop transition-opacity duration-240 data-starting-style:opacity-0 data-ending-style:opacity-0';

const viewportClass = 'fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4 max-sm:items-end max-sm:p-0';

const popupClass =
  'relative w-full max-w-[440px] rounded-md bg-raised p-5 text-body shadow-modal outline-hidden transition-[opacity,transform] duration-240 ease-app data-starting-style:translate-y-2 data-starting-style:opacity-0 data-ending-style:translate-y-2 data-ending-style:opacity-0 max-sm:max-w-none max-sm:rounded-b-none max-sm:pb-[max(20px,env(safe-area-inset-bottom))]';

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
            <div className="mb-4 flex items-start gap-3">
              <div className="min-w-0 flex-1">
                <BaseDialog.Title className="text-display-sm text-ink">{title}</BaseDialog.Title>
                {description && <BaseDialog.Description className="mt-1 text-ui text-muted">{description}</BaseDialog.Description>}
              </div>
              <BaseDialog.Close render={<Button size="icon" aria-label="Close" className="-mt-1 -mr-1 text-muted" />}>
                <X />
              </BaseDialog.Close>
            </div>
            {children}
            {footer && <div className="mt-5 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">{footer}</div>}
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
              <Button variant={danger ? 'danger' : 'primary'} className={danger ? 'border border-error/40 bg-raised' : undefined} disabled={busy || disabled} onClick={onConfirm}>
                {busy ? 'Working…' : confirmLabel}
              </Button>
            </div>
          </BaseAlertDialog.Popup>
        </BaseAlertDialog.Viewport>
      </BaseAlertDialog.Portal>
    </BaseAlertDialog.Root>
  );
}

/** A panel sliding in from one edge: the narrow-layout drawer and other full-height sheets. */
export function Sheet({
  open,
  onOpenChange,
  side = 'left',
  label,
  className,
  children,
  ...props
}: Omit<ComponentProps<typeof BaseDialog.Popup>, 'render'> & { open: boolean; onOpenChange: (open: boolean) => void; side?: 'left' | 'right'; label: string }) {
  return (
    <BaseDialog.Root open={open} onOpenChange={onOpenChange}>
      <BaseDialog.Portal>
        <BaseDialog.Backdrop className={backdropClass} />
        <BaseDialog.Popup
          data-popup=""
          aria-label={label}
          className={cn(
            'fixed inset-y-0 z-50 flex w-drawer max-w-[calc(100vw-44px)] flex-col bg-rail shadow-modal outline-hidden transition-transform duration-240 ease-app',
            side === 'left' ? 'left-0 data-starting-style:-translate-x-full data-ending-style:-translate-x-full' : 'right-0 data-starting-style:translate-x-full data-ending-style:translate-x-full',
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
