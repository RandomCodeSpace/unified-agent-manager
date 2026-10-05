import { Tooltip as BaseTooltip } from '@base-ui/react/tooltip';
import { Info } from 'lucide-react';
import { useId, useRef, useState, type ReactElement, type ReactNode } from 'react';
import { Button } from './button';

export const TooltipProvider = BaseTooltip.Provider;

/**
 * A hint for sighted users; the trigger must carry its own accessible name. `label` may
 * hold a second, muted line (pass a fragment). Opens after 400ms, 0 when another tip is up.
 */
export function Tip({ label, children, side = 'top', disabled = false, openOnClick = false }: Readonly<{ label: ReactNode; children: ReactElement; side?: 'top' | 'bottom' | 'left' | 'right'; disabled?: boolean; openOnClick?: boolean }>) {
  const shown = useRef(false);
  const openAtPress = useRef(false);
  const [open, setOpen] = useState(false);
  if (!label) return children;
  return (
    <BaseTooltip.Root
      disabled={disabled}
      open={openOnClick ? open : undefined}
      onOpenChange={(open, details) => {
        // A view transition puts a snapshot over the page, so the browser reports the pointer leaving a
        // hovered trigger and Base UI closes on its hover path, which flushes synchronously and cancels
        // the transition (the pane and the Task list would snap). A tip that is not open has nothing to close.
        if (!open && !shown.current && details.reason === 'trigger-hover') {
          details.cancel();
          return;
        }
        shown.current = open;
        if (openOnClick) setOpen(open);
      }}
    >
      <BaseTooltip.Trigger
        render={children}
        closeOnClick={!openOnClick}
        onPointerDown={openOnClick ? () => { openAtPress.current = shown.current; } : undefined}
        onClick={openOnClick ? (event) => {
          // Focus may open the tip between pointerdown and click. A first tap must still open it.
          const next = !(event.detail === 0 ? shown.current : openAtPress.current);
          shown.current = next;
          setOpen(next);
        } : undefined}
      />
      <BaseTooltip.Portal>
        <BaseTooltip.Positioner side={side} sideOffset={6} collisionPadding={8} className="z-60">
          <BaseTooltip.Popup
            data-popup="tooltip"
            className="max-w-64 origin-(--transform-origin) rounded-sm bg-ink px-2 py-1 text-caption text-on-primary shadow-float transition-[opacity,scale] duration-100 data-starting-style:scale-[0.97] data-starting-style:opacity-0 data-ending-style:opacity-0 data-instant:transition-none"
          >
            {label}
          </BaseTooltip.Popup>
        </BaseTooltip.Positioner>
      </BaseTooltip.Portal>
    </BaseTooltip.Root>
  );
}

/** Optional explanation beside a label. The persistent description keeps a field's aria-describedby valid. */
export function HelpTip({ label, children, id }: Readonly<{ label: string; children: ReactNode; id?: string }>) {
  const generatedId = useId();
  const descriptionId = id ?? generatedId;
  return (
    <>
      <Tip label={children} openOnClick>
        <Button size="icon-sm" aria-label={`About ${label}`} aria-describedby={descriptionId} className="text-muted">
          <Info aria-hidden="true" />
        </Button>
      </Tip>
      <span id={descriptionId} className="sr-only">{children}</span>
    </>
  );
}
