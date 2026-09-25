import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/cn';

/**
 * DESIGN.md text input: a filled `sunken` surface with the inset `well` ring, 6px corners,
 * `ink` text, no drawn edge (its label, placeholder and caret identify it). Focus is the
 * `focus` edge (a 1px `focus` ring, no halo, no outline); `aria-invalid` turns the
 * ring `error`. Sizes follow Button: sm 28 (in-place rename), md 32,
 * lg 36 (forms, the DESIGN.md default); md and lg reach 44px on a coarse pointer.
 */
export const inputVariants = cva(
  'w-full min-w-0 rounded-sm bg-sunken text-ink shadow-well transition-[background-color,color] placeholder:text-muted focus-visible:outline-none focus-visible:shadow-focus aria-invalid:shadow-[inset_0_0_0_1px_var(--color-error)] aria-invalid:focus-visible:shadow-[0_0_0_1px_var(--color-error),0_0_0_4px_var(--color-error-wash)] disabled:cursor-not-allowed disabled:opacity-45',
  {
    variants: {
      size: {
        sm: 'h-7 rounded-xs px-1.5',
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
