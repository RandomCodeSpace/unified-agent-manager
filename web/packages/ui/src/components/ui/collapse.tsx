import { useEffect, useEffectEvent, useRef, useState, type ReactNode } from 'react';
import { cn } from '../../lib/cn';

/** How long an exit takes (DESIGN.md `slow`), plus a frame so the last transition step has painted. */
export const EXIT_MS = 260;

/** How long a disclosure's closing fade takes (DESIGN.md `fast`), plus a frame, before its track steps to 0fr. */
const FADE_MS = 120;

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
 * One reveal for every disclosure (DESIGN.md: shelves, tool details, cards): the rows track
 * switches 0fr ↔ 1fr at once, so the layout changes in one step and never per frame, and the
 * content fades and slides in over `slow` (opacity and transform only; the height never
 * animates). On close the track holds at 1fr while the content fades out over `fast`, then
 * steps to 0fr (at once under reduced motion). Closed content is inert. `onClosed` fires with
 * that step, so the owner can unmount.
 */
export function Collapse({ open, appear = false, onClosed, className, inner, children }: Readonly<{ open: boolean; /** Mounted open, reveal on the next frame (the first paint is at 0fr). */ appear?: boolean; onClosed?: () => void; className?: string; inner?: string; children: ReactNode }>) {
  const closed = useEffectEvent(() => onClosed?.());
  // Its own window's timer and frames: a disclosure in a Picture-in-Picture pop-out must not wait on the main page's.
  const box = useRef<HTMLDivElement>(null);
  const [shut, setShut] = useState(!open);
  if (open && shut) setShut(false);
  useEffect(() => {
    if (open) return;
    const win = box.current?.ownerDocument.defaultView ?? window;
    const timer = win.setTimeout(() => {
      setShut(true);
      closed();
    }, FADE_MS);
    return () => win.clearTimeout(timer);
  }, [open]);
  const [appeared, setAppeared] = useState(!appear);
  useEffect(() => {
    if (appeared) return;
    const win = box.current?.ownerDocument.defaultView ?? window;
    const frame = win.requestAnimationFrame(() => setAppeared(true));
    return () => win.cancelAnimationFrame(frame);
  }, [appeared]);
  const shown = open && appeared;
  // Closed, not yet stepped: the box keeps its room while the content fades on opacity alone.
  const fading = !open && !shut;
  return (
    <div ref={box} className={cn('grid', shown || fading ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]', fading && 'motion-reduce:grid-rows-[0fr]', className)}>

      <div className={cn('min-h-0 overflow-hidden', shown && 'transition-[opacity,translate] duration-240 ease-app', fading && 'opacity-0 transition-opacity duration-100 ease-app', !shown && !fading && '-translate-y-1 opacity-0', inner)} inert={!open} aria-hidden={!open}>
        {children}
      </div>
    </div>
  );
}
