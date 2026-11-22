import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/cn';

/**
 * DESIGN.md chip (Tactile): a 20px capsule, `caption`, a glyph and a word. Text-only in its tone;
 * `attention` is the one filled chip, `well` sits a file name on the well, `outline` is
 * the read-only stage tag (the grey tonal `fill`; nothing in the chrome draws an edge). Its colours
 * switch at once.
 */
export const chipVariants = cva('inline-flex h-5 shrink-0 items-center gap-1.5 rounded-full px-1.5 text-caption whitespace-nowrap', {
  variants: {
    tone: {
      muted: 'text-muted',
      accent: 'text-accent',
      success: 'text-success',
      error: 'text-error',
      warning: 'text-warning',
      attention: 'bg-attention-wash text-attention',
    },
    fill: {
      none: '',
      well: 'bg-tint-well',
      outline: 'bg-fill text-body',
    },
  },
  defaultVariants: { tone: 'muted', fill: 'none' },
});

export type ChipProps = ComponentProps<'span'> & VariantProps<typeof chipVariants>;

export function Chip({ className, tone, fill, ...props }: ChipProps) {
  return <span className={cn(chipVariants({ tone, fill }), className)} {...props} />;
}
