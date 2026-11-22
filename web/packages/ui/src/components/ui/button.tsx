import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/cn';
import { Spinner } from './spinner';

/**
 * Continuous corners where the browser draws them (`corner-shape`, Chromium 139+): the token radius a step rounder
 * (×1.3), since a squircle reads tighter than an arc; plain arcs at the token radius elsewhere. A pill or a circle
 * stays round: a caller's `rounded-full` (or a split button's `rounded-l-full` / `rounded-r-full`) keeps the
 * element out, and tailwind-merge lets a caller's `rounded-*` replace the radius as before.
 */
const SQUIRCLE = '[--sq:1] supports-[corner-shape:squircle]:[&:not(.rounded-full,.rounded-l-full,.rounded-r-full)]:[--sq:1.3] supports-[corner-shape:squircle]:[&:not(.rounded-full,.rounded-l-full,.rounded-r-full)]:[corner-shape:squircle]';
export const corner = {
  xs: `${SQUIRCLE} rounded-[calc(var(--radius-xs)*var(--sq))]`,
  sm: `${SQUIRCLE} rounded-[calc(var(--radius-sm)*var(--sq))]`,
  md: `${SQUIRCLE} rounded-[calc(var(--radius-md)*var(--sq))]`,
  lg: `${SQUIRCLE} rounded-[calc(var(--radius-lg)*var(--sq))]`,
  xl: `${SQUIRCLE} rounded-[calc(var(--radius-xl)*var(--sq))]`,
};

/**
 * One row of a list outside a popup (a Thread row, a todo, a reference, a listbox option): the tonal ramp of
 * DESIGN.md's Tactile controls on `md` corners, with no height of its own. Hover and the keyboard highlight are
 * `fill-hover`, a press `fill-strong`, the chosen row (`aria-selected`, `aria-pressed`, `aria-current`,
 * `data-selected`) the resting `fill` under the tonal highlight. Fills switch at once; nothing transitions.
 */
export const rowClass = `relative flex w-full min-w-0 items-center gap-2 ${corner.md} px-2 py-1 text-left text-ui text-body select-none hover:bg-fill-hover hover:text-ink active:bg-fill-strong data-highlighted:bg-fill-hover data-highlighted:text-ink aria-selected:bg-fill aria-selected:text-ink aria-selected:shadow-highlight-tonal aria-pressed:bg-fill aria-pressed:text-ink aria-pressed:shadow-highlight-tonal aria-[current=true]:bg-fill aria-[current=true]:text-ink aria-[current=true]:shadow-highlight-tonal data-selected:bg-fill data-selected:text-ink data-selected:shadow-highlight-tonal disabled:pointer-events-none disabled:opacity-45 data-disabled:opacity-45 [&_svg]:shrink-0`;

/**
 * DESIGN.md buttons (Tactile): 8px continuous rectangles at every size, `ui` 13/500. Primary is the `accent-fill`
 * with a top highlight; `ink` is the neutral strong fill (the composer's Stop); secondary is the grey tonal `fill`
 * under the tonal highlight; `tinted` and `danger` are the accent and error tints; ghost and subtle have no fill at
 * rest. Each steps up its own ramp on hover and press, at once (fills never transition).
 * Every button answers a press on its scale: 0.97, an icon button 0.92, in over `fast` and back out on the spring.
 * Sizes: xs 24 (`caption`), sm 28, md 32, lg 36.
 * The small sizes keep their look and grow an invisible hit area instead (`after:`):
 * 32px on a fine pointer, 44px on a coarse one; md and lg grow to 44px themselves.
 * `aria-disabled` looks disabled but keeps its tooltip (the reason stays reachable).
 * `link` is a 24px `caption` action in words, the same at every size; it does not scale.
 */
const pressIcon = 'not-aria-disabled:active:scale-[0.92]';

export const buttonVariants = cva(
  `relative inline-flex shrink-0 items-center justify-center gap-1.5 ${corner.sm} whitespace-nowrap text-ui font-medium select-none transition-[scale] duration-(--spring-fast) ease-spring not-aria-disabled:active:scale-[0.97] active:duration-100 active:ease-app after:absolute after:content-[''] disabled:pointer-events-none disabled:opacity-45 aria-disabled:cursor-not-allowed aria-disabled:opacity-45 [&_svg]:size-4 [&_svg]:shrink-0`,
  {
    variants: {
      variant: {
        primary: 'bg-accent-fill text-on-accent-fill shadow-highlight not-aria-disabled:hover:bg-accent-fill-hover not-aria-disabled:active:bg-accent-fill-press',
        ink: 'bg-primary text-on-primary shadow-highlight not-aria-disabled:hover:bg-body not-aria-disabled:active:bg-ink',
        secondary: 'bg-fill text-ink shadow-highlight-tonal not-aria-disabled:hover:bg-fill-strong not-aria-disabled:active:bg-fill-press',
        // The quieter call to action beside a primary ("Open diff", a popover's "Compact").
        tinted: 'bg-accent-tint text-accent not-aria-disabled:hover:bg-accent-tint-hover not-aria-disabled:active:bg-accent-tint-press',
        ghost:
          'text-body not-aria-disabled:hover:bg-fill-hover not-aria-disabled:hover:text-ink not-aria-disabled:active:bg-fill-strong data-open:bg-fill-strong data-open:text-ink aria-pressed:bg-fill-strong aria-pressed:text-ink',
        danger: 'bg-error-tint text-error not-aria-disabled:hover:bg-error-tint-hover not-aria-disabled:active:bg-error-tint-press',
        // Sits inside a raised surface: the fills are translucent, so the same steps read on white too.
        subtle: 'text-body not-aria-disabled:hover:bg-fill-hover not-aria-disabled:hover:text-ink not-aria-disabled:active:bg-fill-strong data-open:bg-fill-strong data-open:text-ink',
        // An inline action in words ("Show all 12 lines"): `accent`, underlined on hover; its look is in compoundVariants, over any size.
        link: '',
      },
      size: {
        xs: 'h-6 px-1.5 text-caption after:inset-x-0 after:-inset-y-1 pointer-coarse:after:-inset-y-2.5 [&_svg]:size-3.5',
        sm: 'h-7 px-2 after:inset-x-0 after:-inset-y-0.5 pointer-coarse:after:-inset-y-2',
        md: 'h-8 px-3 pointer-coarse:min-h-11',
        lg: 'h-9 px-3.5 pointer-coarse:min-h-11',
        'icon-sm': `size-6 ${pressIcon} after:-inset-1 pointer-coarse:after:-inset-2.5`,
        icon: `size-7 ${pressIcon} after:-inset-0.5 pointer-coarse:after:-inset-2`,
        'icon-md': `size-8 ${pressIcon} pointer-coarse:size-11`,
      },
      loading: {
        // Working, not refused: full opacity, the label hidden in place and a ring over it, so the width holds.
        true: 'disabled:opacity-100',
      },
    },
    // 24px `caption` at any size, no padding, with the small sizes' invisible hit area instead of a taller box.
    compoundVariants: [{ variant: 'link', className: 'h-6 gap-1 rounded-xs px-0 text-caption font-normal text-accent not-aria-disabled:active:scale-100 hover:underline after:inset-x-0 after:-inset-y-1 pointer-coarse:min-h-0 pointer-coarse:after:-inset-y-2.5 [&_svg]:size-3' }],
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
          {/* Transparent, not `invisible`: hidden content leaves the accessible name, and the button would go unnamed. */}
          <span className="inline-flex items-center justify-center gap-[inherit] opacity-0">{children}</span>
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
