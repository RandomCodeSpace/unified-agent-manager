import { Switch as BaseSwitch } from '@base-ui/react/switch';
import { cn } from '../../lib/cn';

/**
 * A 32×18 on/off switch on Base UI (DESIGN.md `switch`): a `hairline-strong` track that
 * turns `accent` when on, a `raised` thumb, `sm` and `xs` corners rather than a pill. On a
 * coarse pointer an invisible 44px hit area surrounds it, so the control itself never grows.
 */
export function Switch({
  checked,
  onCheckedChange,
  disabled,
  className,
  'aria-label': ariaLabel,
  'aria-describedby': describedBy,
}: {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  disabled?: boolean;
  className?: string;
  'aria-label': string;
  'aria-describedby'?: string;
}) {
  return (
    <BaseSwitch.Root
      checked={checked}
      onCheckedChange={onCheckedChange}
      disabled={disabled}
      aria-label={ariaLabel}
      aria-describedby={describedBy}
      className={cn(
        'relative inline-flex h-[18px] w-8 shrink-0 items-center rounded-sm bg-hairline-strong p-0.5 transition-[background-color] duration-100 ease-app data-checked:bg-accent data-disabled:cursor-not-allowed data-disabled:opacity-45',
        "pointer-coarse:after:absolute pointer-coarse:after:-inset-x-1.5 pointer-coarse:after:-inset-y-[13px] pointer-coarse:after:content-['']",
        className,
      )}
    >
      <BaseSwitch.Thumb className="size-3.5 rounded-xs bg-raised shadow-raised transition-transform duration-100 ease-app data-checked:translate-x-3.5" />
    </BaseSwitch.Root>
  );
}
