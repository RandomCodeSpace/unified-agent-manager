import { cn } from '../../lib/cn';

/** A short meter: a 6px `fill-strong` track, the `fill` class over `share` (0 to 1, clamped) of it, drawn with `scaleX` so a change animates on the transform alone. */
export function Meter({ share, fill, className }: Readonly<{ share: number; fill: string; className?: string }>) {
  return (
    <span aria-hidden="true" className={cn('relative block h-1.5 w-full overflow-hidden rounded-full bg-fill-strong', className)}>
      <span className={cn('absolute inset-0 origin-left rounded-full transition-transform duration-240', fill)} style={{ transform: `scaleX(${Math.min(1, Math.max(0, share))})` }} />
    </span>
  );
}
