import { Tooltip as BaseTooltip } from '@base-ui/react/tooltip';
import { useRef, type ReactElement, type ReactNode } from 'react';

export const TooltipProvider = BaseTooltip.Provider;

/**
 * A hint for sighted users; the trigger must carry its own accessible name. `label` may
 * hold a second, muted line (pass a fragment). Opens after 400ms, 0 when another tip is up.
 */
export function Tip({ label, children, side = 'top', disabled = false }: { label: ReactNode; children: ReactElement; side?: 'top' | 'bottom' | 'left' | 'right'; disabled?: boolean }) {
  const shown = useRef(false);
  if (disabled || !label) return children;
  return (
    <BaseTooltip.Root
      onOpenChange={(open, details) => {
        // A view transition puts a snapshot over the page, so the browser reports the pointer leaving a
        // hovered trigger and Base UI closes on its hover path, which flushes synchronously and cancels
        // the transition (the pane and the Task list would snap). A tip that is not open has nothing to close.
        if (!open && !shown.current && details.reason === 'trigger-hover') {
          details.cancel();
          return;
        }
        shown.current = open;
      }}
    >
      <BaseTooltip.Trigger render={children} />
      <BaseTooltip.Portal>
        <BaseTooltip.Positioner side={side} sideOffset={6} collisionPadding={8} className="z-60">
          <BaseTooltip.Popup
            data-popup=""
            className="max-w-64 rounded-sm bg-ink px-2 py-1 text-caption text-on-primary shadow-float transition-[opacity,transform] duration-100 data-starting-style:translate-y-0.5 data-starting-style:opacity-0 data-ending-style:opacity-0 data-instant:transition-none"
          >
            {label}
          </BaseTooltip.Popup>
        </BaseTooltip.Positioner>
      </BaseTooltip.Portal>
    </BaseTooltip.Root>
  );
}
