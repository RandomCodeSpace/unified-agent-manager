import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/cn';
import { Spinner } from './spinner';

/**
 * DESIGN.md buttons: 6px rectangles at every size, `ui` 13/500. Primary is ink, never a
 * colour; secondary is a filled `sunken` surface with no edge. Hover is the `tint-hover`
 * step; press adds ink. Sizes: sm 28, md 32, lg 36.
 * The small sizes keep their look and grow an invisible hit area instead (`after:`):
 * 32px on a fine pointer, 44px on a coarse one; md and lg grow to 44px themselves.
 * `aria-disabled` looks disabled but keeps its tooltip (the reason stays reachable).
 * The filled and outlined buttons (an action) also press to 0.97 (`fast`); the quiet ones only tint.
 */
const press = 'not-aria-disabled:active:scale-[0.97]';

export const buttonVariants = cva(
  "relative inline-flex shrink-0 items-center justify-center gap-1.5 rounded-sm whitespace-nowrap text-ui font-medium select-none transition-[background-color,color,scale] after:absolute after:content-[''] disabled:pointer-events-none disabled:opacity-45 aria-disabled:cursor-not-allowed aria-disabled:opacity-45 [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        primary: `bg-primary text-on-primary hover:bg-body active:bg-ink ${press}`,
        secondary: `bg-sunken text-ink hover:bg-tint-hover active:bg-hairline ${press}`,
        ghost:
          'text-body not-aria-disabled:hover:bg-tint-hover not-aria-disabled:hover:text-ink active:bg-hairline data-open:bg-tint-hover data-open:text-ink aria-pressed:bg-tint-hover aria-pressed:text-ink',
        danger: `text-error hover:bg-error-wash active:bg-error-wash ${press}`,
        // Sits inside a raised surface: the same step, so the hover reads on white too.
        subtle: 'text-body not-aria-disabled:hover:bg-tint-hover not-aria-disabled:hover:text-ink active:bg-hairline data-open:bg-tint-hover data-open:text-ink',
      },
      size: {
        sm: 'h-7 px-2 after:inset-x-0 after:-inset-y-0.5 pointer-coarse:after:-inset-y-2',
        md: 'h-8 px-3 pointer-coarse:min-h-11',
        lg: 'h-9 px-3.5 pointer-coarse:min-h-11',
        'icon-sm': 'size-6 after:-inset-1 pointer-coarse:after:-inset-2.5',
        icon: 'size-7 after:-inset-0.5 pointer-coarse:after:-inset-2',
        'icon-md': 'size-8 pointer-coarse:size-11',
      },
      loading: {
        // Working, not refused: full opacity, the label hidden in place and a ring over it, so the width holds.
        true: 'disabled:opacity-100',
      },
    },
    defaultVariants: { variant: 'ghost', size: 'md' },
  },
);

export type ButtonProps = ComponentProps<'button'> & VariantProps<typeof buttonVariants>;

/** Forwards every prop to the <button>, so Base UI parts can `render={<Button />}`. */
export function Button({ className, variant, size, loading = false, type = 'button', disabled, children, ...props }: ButtonProps) {
  return (
    <button type={type} className={cn(buttonVariants({ variant, size, loading }), className)} disabled={disabled || !!loading} aria-busy={loading || undefined} {...props}>
      {loading ? (
        <>
          <span className="invisible contents">{children}</span>
          <span aria-hidden="true" className="absolute inset-0 flex items-center justify-center">
            <Spinner className="border-current border-r-transparent" />
          </span>
        </>
      ) : (
        children
      )}
    </button>
  );
}
