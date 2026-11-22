import { Checkbox as BaseCheckbox } from '@base-ui/react/checkbox';
import { Radio as BaseRadio } from '@base-ui/react/radio';
import { Check } from 'lucide-react';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/cn';

export { RadioGroup } from '@base-ui/react/radio-group';

/**
 * The 16px box shared by Checkbox and Radio (DESIGN.md Tactile): a tonal fill inside a 1.5px `track` ring
 * (3:1 on every surface), the `accent-fill` with a top highlight when checked. Fills switch instantly; a press
 * scales it to .92 (transform only). On a coarse pointer an invisible 44px hit area surrounds it.
 */
const box = cn(
  'relative inline-flex size-4 shrink-0 items-center justify-center bg-fill text-on-accent-fill shadow-[inset_0_0_0_1.5px_var(--color-track)] transition-[scale] duration-100 ease-app select-none',
  'not-data-disabled:hover:bg-fill-strong not-data-disabled:active:scale-[0.92]',
  'data-checked:bg-accent-fill data-checked:shadow-highlight not-data-disabled:data-checked:hover:bg-accent-fill-hover not-data-disabled:data-checked:active:bg-accent-fill-press',
  'data-disabled:cursor-not-allowed data-disabled:opacity-45',
  "pointer-coarse:after:absolute pointer-coarse:after:-inset-3.5 pointer-coarse:after:content-['']",
);

export function Checkbox({ className, ...props }: Omit<ComponentProps<typeof BaseCheckbox.Root>, 'className'> & { className?: string }) {
  return (
    <BaseCheckbox.Root className={cn(box, 'rounded-xs', className)} {...props}>
      <BaseCheckbox.Indicator className="flex">
        <Check aria-hidden="true" strokeWidth={3} className="size-3" />
      </BaseCheckbox.Indicator>
    </BaseCheckbox.Root>
  );
}

/** One option of a RadioGroup: the same box, round, with a 6px dot when chosen. */
export function Radio({ className, ...props }: Omit<ComponentProps<typeof BaseRadio.Root>, 'className'> & { className?: string }) {
  return (
    <BaseRadio.Root className={cn(box, 'rounded-full', className)} {...props}>
      <BaseRadio.Indicator className="size-1.5 rounded-full bg-current" />
    </BaseRadio.Root>
  );
}
