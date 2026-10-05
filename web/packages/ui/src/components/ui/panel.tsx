import { ChevronDown } from 'lucide-react';
import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { cn } from '../../lib/cn';

/**
 * The parts of a floating panel (DESIGN.md Floating panels): a head on the panel's surface that
 * fades into the body once it scrolls beneath (`scrolled`), labelled sections with the most useful
 * first, long inputs collapsed to a few lines, and a quiet foot for key hints and side actions.
 */
export function PanelHead({ scrolled, className, children }: Readonly<{ scrolled?: boolean; className?: string; children: ReactNode }>) {
  return (
    <div className={cn('panel-head flex shrink-0 flex-col', className)} data-scrolled={scrolled || undefined}>
      {children}
    </div>
  );
}

/** One section of a panel's body: an eyebrow label, a muted line beside it, an action at its end. */
export function PanelSection({ label, meta, action, className, children }: Readonly<{ label: string; meta?: ReactNode; action?: ReactNode; className?: string; children: ReactNode }>) {
  return (
    <section aria-label={label} className={cn('flex min-w-0 flex-col gap-1.5', className)}>
      <div className="flex h-6 min-w-0 items-center gap-2">
        <h3 className="shrink-0 text-eyebrow uppercase text-muted">{label}</h3>
        {meta && <span className="min-w-0 truncate text-caption text-muted">{meta}</span>}
        <span className="flex-1" />
        {action}
      </div>
      {children}
    </section>
  );
}

/** A panel's foot: key hints at the start, side actions at the end. */
export function PanelFoot({ className, children }: Readonly<{ className?: string; children: ReactNode }>) {
  return <div className={cn('panel-foot flex h-10 shrink-0 items-center gap-3.5 pr-3 pl-4 text-meta text-muted', className)}>{children}</div>;
}

/**
 * A long text collapsed to `height` with its end fading into `surface`; "Show the whole <noun>"
 * opens it, and the text is never cut when it fits.
 */
export function Clamp({ noun, height = 84, surface, className, children }: Readonly<{ noun: string; height?: number; /** The colour under the fade: the text's own background. */ surface?: string; className?: string; children: ReactNode }>) {
  const box = useRef<HTMLDivElement>(null);
  const [long, setLong] = useState(false);
  const [open, setOpen] = useState(false);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const measure = () => setLong(el.scrollHeight > height + 8);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [height]);
  const clamped = long && !open;
  return (
    <div className="flex flex-col items-start gap-1">
      <div ref={box} className={cn('relative w-full overflow-hidden', clamped && 'clamp-fade', className)} style={{ maxHeight: clamped ? height : undefined, ...(surface && { '--clamp-surface': surface }) }}>
        {children}
      </div>
      {long && (
        <button type="button" aria-expanded={open} className="flex h-6 items-center gap-1 rounded-xs text-caption text-accent hover:underline pointer-coarse:min-h-11" onClick={() => setOpen((o) => !o)}>
          {open ? `Show less` : `Show the whole ${noun}`}
          <ChevronDown aria-hidden="true" className={cn('size-3 transition-transform duration-160', open && 'rotate-180')} />
        </button>
      )}
    </div>
  );
}
