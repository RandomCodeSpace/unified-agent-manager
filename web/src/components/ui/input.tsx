import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/cn';

/**
 * DESIGN.md text input: `raised` on a `hairline-strong` edge, 6px corners, `ink` text. Focus
 * is the app's one ring (2px `focus`, 2px out, the same as Button); `aria-invalid` turns
 * the edge and the ring `error`. Sizes follow Button: sm 28 (in-place rename), md 32,
 * lg 36 (forms, the DESIGN.md default); md and lg reach 44px on a coarse pointer.
 */
export const inputVariants = cva(
  'w-full min-w-0 rounded-sm border border-hairline-strong bg-raised text-ink transition-[border-color,color] placeholder:text-muted aria-invalid:border-error aria-invalid:focus-visible:outline-error disabled:cursor-not-allowed disabled:opacity-45',
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
