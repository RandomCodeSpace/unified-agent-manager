import { cn } from '../../lib/cn';

/** A 12px ring in the current colour (`accent` by default); still under reduced motion. */
export function Spinner({ className }: { className?: string }) {
  return <span aria-hidden="true" className={cn('inline-block size-3 shrink-0 animate-spin rounded-full border-[1.5px] border-accent border-r-transparent motion-reduce:animate-none', className)} />;
}
