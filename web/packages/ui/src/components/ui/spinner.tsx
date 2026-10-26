import { cn } from '../../lib/cn';

/** A 12px ring in the current colour (`accent` by default); still under reduced motion. */
export function Spinner({ className }: Readonly<{ className?: string }>) {
  return <span aria-hidden="true" className={cn('inline-block size-3 shrink-0 animate-spin rounded-full border-[1.5px] border-accent border-r-transparent motion-reduce:animate-none', className)} />;
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
