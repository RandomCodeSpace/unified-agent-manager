import { Switch as BaseSwitch } from '@base-ui/react/switch';
import { cn } from '../../lib/cn';

/**
 * A 34×20 on/off switch on Base UI (DESIGN.md `switch`, Tactile): a capsule `track` (an inner shadow,
 * `accent-fill` when on, switched at once) under a round 16px thumb that slides on its transform (the spring;
 * still under Motion: Match system) and stretches a quarter towards the other side while it is pressed, so the
 * press reads before the state changes. The thumb's position carries the state, not the colour alone. On a
 * coarse pointer an invisible 44px hit area surrounds it, so the control itself never grows.
 */
export function Switch({
  checked,
  onCheckedChange,
  disabled,
  className,
  'aria-label': ariaLabel,
  'aria-describedby': describedBy,
}: Readonly<{
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  disabled?: boolean;
  className?: string;
  'aria-label': string;
  'aria-describedby'?: string;
}>) {
  return (
    <BaseSwitch.Root
      checked={checked}
      onCheckedChange={onCheckedChange}
      disabled={disabled}
      aria-label={ariaLabel}
      aria-describedby={describedBy}
      className={cn(
        'group/switch relative inline-flex h-5 w-[34px] shrink-0 items-center rounded-full bg-track p-0.5 shadow-well data-checked:bg-accent-fill data-disabled:cursor-not-allowed data-disabled:opacity-45',
        "pointer-coarse:after:absolute pointer-coarse:after:-inset-x-[5px] pointer-coarse:after:-inset-y-3 pointer-coarse:after:content-['']",
        className,
      )}
    >
      <BaseSwitch.Thumb className="size-4 origin-left rounded-full bg-on-accent-fill shadow-thumb transition-[translate,scale] duration-(--spring-fast) ease-spring group-active/switch:not-data-disabled:scale-x-125 data-checked:origin-right data-checked:translate-x-3.5" />
    </BaseSwitch.Root>
  );
}
