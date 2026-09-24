import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/cn';

/**
 * DESIGN.md buttons: 6px rectangles at every size, `ui` 13/500. Primary is ink, never a
 * colour. Hover is one surface step; press adds ink. Sizes: sm 28, md 32, lg 36; on a
 * coarse pointer every size grows to a 44px target.
 */
export const buttonVariants = cva(
  'inline-flex shrink-0 items-center justify-center gap-1.5 rounded-sm whitespace-nowrap text-ui font-medium select-none transition-[background-color,color,border-color] disabled:pointer-events-none disabled:opacity-45 [&_svg]:size-4 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        primary: 'bg-primary text-on-primary hover:bg-body active:bg-ink',
        secondary: 'border border-hairline-strong bg-raised text-ink hover:bg-surface active:bg-canvas',
        ghost: 'text-body hover:bg-canvas hover:text-ink active:bg-sunken data-open:bg-canvas data-open:text-ink aria-pressed:bg-canvas aria-pressed:text-ink',
        danger: 'text-error hover:bg-error-wash active:bg-error-wash',
        // Sits inside a raised surface: the step goes down, not up.
        subtle: 'text-body hover:bg-canvas hover:text-ink active:bg-sunken data-open:bg-canvas data-open:text-ink',
      },
      size: {
        sm: 'h-7 px-2 pointer-coarse:min-h-11',
        md: 'h-8 px-3 pointer-coarse:min-h-11',
        lg: 'h-9 px-3.5 pointer-coarse:min-h-11',
        icon: 'size-7 pointer-coarse:size-11',
        'icon-md': 'size-8 pointer-coarse:size-11',
      },
    },
    defaultVariants: { variant: 'ghost', size: 'md' },
  },
);

export type ButtonProps = ComponentProps<'button'> & VariantProps<typeof buttonVariants>;

/** Forwards every prop to the <button>, so Base UI parts can `render={<Button />}`. */
export function Button({ className, variant, size, type = 'button', ...props }: ButtonProps) {
  return <button type={type} className={cn(buttonVariants({ variant, size }), className)} {...props} />;
}
