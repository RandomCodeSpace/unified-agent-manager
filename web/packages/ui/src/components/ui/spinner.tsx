import { cn } from '../../lib/cn';

/**
 * A 12px ring, 2px wide (`accent` by default; a caller's `border-*` recolours it), open over a quarter. The quarter
 * shows its track: a copy of the ring at 22%, turned a quarter, so it takes whatever colour the ring has. Still under
 * reduced motion.
 */
export function Spinner({ className }: Readonly<{ className?: string }>) {
  return <span aria-hidden="true" data-spinner="" className={cn("relative inline-block size-3 shrink-0 animate-spin rounded-full border-2 border-accent border-r-transparent motion-reduce:animate-none before:absolute before:-inset-0.5 before:rotate-90 before:rounded-full before:border-2 before:border-inherit before:opacity-22 before:content-['']", className)} />;
}

/**
 * A running step's mark: three `accent` dots that light in turn (opacity only), so it moves without
 * reading as a second ring; still under reduced motion.
 */
export function StepDots({ className }: Readonly<{ className?: string }>) {
  return (
    <span aria-hidden="true" data-step-mark="" className={cn('inline-flex size-3.5 shrink-0 items-center justify-center gap-[2px] text-accent', className)}>
      <span className="size-[3px] animate-step rounded-full bg-current motion-reduce:animate-none" />
      <span className="size-[3px] animate-step rounded-full bg-current [animation-delay:160ms] motion-reduce:animate-none" />
      <span className="size-[3px] animate-step rounded-full bg-current [animation-delay:320ms] motion-reduce:animate-none" />
    </span>
  );
}
