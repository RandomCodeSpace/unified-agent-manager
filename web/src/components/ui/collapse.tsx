import { useEffect, useEffectEvent, useState, type ReactNode } from 'react';
import { cn } from '../../lib/cn';

/** How long an exit takes (DESIGN.md `slow`), plus a frame so the last transition step has painted. */
export const EXIT_MS = 260;

/**
 * Keeps something mounted through its exit: `mounted` holds from the first `open` until the
 * animated component reports `onClosed` after `open` turned false. Under reduced motion the
 * components still report, on a timer, so nothing lingers.
 */
export function usePresence(open: boolean): { mounted: boolean; onClosed: () => void } {
  const [mounted, setMounted] = useState(open);
  if (open && !mounted) setMounted(true);
  return { mounted: mounted || open, onClosed: () => setMounted(false) };
}

/**
 * One height animation for every disclosure (DESIGN.md: shelves, tool details, cards): the
 * rows track goes 0fr ↔ 1fr over `slow`, so nothing is measured and nothing snaps. Closed
 * content is inert. `onClosed` fires once the exit is over, so the owner can unmount.
 */
export function Collapse({ open, onClosed, className, inner, children }: { open: boolean; onClosed?: () => void; className?: string; inner?: string; children: ReactNode }) {
  const closed = useEffectEvent(() => onClosed?.());
  useEffect(() => {
    if (open) return;
    const timer = window.setTimeout(closed, EXIT_MS);
    return () => window.clearTimeout(timer);
  }, [open]);
  return (
    <div className={cn('grid transition-[grid-template-rows] duration-240 ease-app', open ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]', className)}>
      <div className={cn('min-h-0 overflow-hidden', inner)} inert={!open} aria-hidden={!open}>
        {children}
      </div>
    </div>
  );
}
