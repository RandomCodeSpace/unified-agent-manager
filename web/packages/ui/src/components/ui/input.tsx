import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/cn';
import { corner } from './button';

/**
 * DESIGN.md text input (Tactile): a tonal well (the grey `fill` under the inset `well` shadow), 8px continuous
 * corners, `ink` text, no drawn edge at rest; a step darker under the pointer. Focus turns it `raised` inside the
 * 2px `focus` ring; `aria-invalid` draws an `error` ring. The fill switches at once. Sizes follow Button: sm 28
 * (in-place rename), md 32, lg 36 (forms, the DESIGN.md default); md and lg reach 44px on a coarse pointer.
 */
export const inputVariants = cva(
  `w-full min-w-0 ${corner.sm} bg-fill text-ink shadow-well placeholder:text-muted enabled:hover:bg-fill-strong focus-visible:shadow-[inset_0_0_0_1px_var(--color-hairline)] focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-focus enabled:focus-visible:bg-raised aria-invalid:shadow-[inset_0_0_0_1.5px_var(--color-error)] disabled:cursor-not-allowed disabled:opacity-45`,
  {
    variants: {
      size: {
        sm: 'h-7 rounded-[calc(var(--radius-xs)*var(--sq))] px-1.5',
        md: 'h-8 px-2 text-ui pointer-coarse:min-h-11',
        lg: 'h-9 px-2.5 text-ui pointer-coarse:min-h-11',
      },
    },
    defaultVariants: { size: 'lg' },
  },
);

export type InputProps = Omit<ComponentProps<'input'>, 'size'> & VariantProps<typeof inputVariants>;

export function Input({ className, size, type = 'text', ...props }: InputProps) {
  return <input type={type} className={cn(inputVariants({ size }), className)} {...props} />;
}
