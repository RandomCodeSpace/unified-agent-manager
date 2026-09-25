import { useEffect, useEffectEvent, useState, type ReactNode } from 'react';
import { cn } from '../../lib/cn';
import { EXIT_MS, usePresence } from './collapse';

/**
 * Enter and exit for a small control that comes and goes in a fixed slot (the Stop button,
 * the "New output" button): it fades and scales from 0.9 over `base`, back out the same
 * way, and stays mounted through the exit, inert. The wrapper is an inline flex item, so
 * it takes the control's place in a row without moving its neighbours.
 */
export function Appear({ show, className, children }: { show: boolean; className?: string; children: ReactNode }) {
  const { mounted, onClosed } = usePresence(show);
  const closed = useEffectEvent(onClosed);
  // The first paint is the hidden state, so the enter has somewhere to start; a new exit resets it.
  const [entered, setEntered] = useState(false);
  const [prevShow, setPrevShow] = useState(show);
  if (show !== prevShow) {
    setPrevShow(show);
    if (!show) setEntered(false);
  }
  useEffect(() => {
    if (!show) {
      const timer = window.setTimeout(closed, EXIT_MS);
      return () => window.clearTimeout(timer);
    }
    const frame = requestAnimationFrame(() => setEntered(true));
    return () => cancelAnimationFrame(frame);
  }, [show]);
  if (!mounted) return null;
  return (
    <span inert={!show} className={cn('inline-flex transition-[opacity,scale] duration-160 ease-app', !(show && entered) && 'scale-90 opacity-0', className)}>
      {children}
    </span>
  );
}
