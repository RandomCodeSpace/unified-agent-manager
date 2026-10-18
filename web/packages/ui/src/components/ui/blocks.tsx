import type { LucideIcon } from 'lucide-react';
import type { ComponentProps, ReactNode } from 'react';
import { cn } from '../../lib/cn';

/**
 * DESIGN.md blocks: a 20px row of joined tone cells, each a glyph and a number (or a short word).
 * The status strip's todo and subagent counts, the footer's phase and time split and a turn's
 * outcome (Todo, GitLab state, a step's result) all speak this one language, so a count reads
 * the same wherever it appears. The glyph keeps a cell readable without its colour.
 */
export type BlockTone = 'success' | 'accent' | 'warning' | 'error' | 'none';

export interface Block {
  key: string;
  tone: BlockTone;
  Icon?: LucideIcon;
  label: ReactNode;
  /** What the cell means in words, for screen readers and the hover title. */
  title: string;
}

const TONES: Record<BlockTone, string> = {
  success: 'bg-success-wash text-success',
  accent: 'bg-accent-wash text-accent',
  warning: 'bg-warning-wash text-warning',
  error: 'bg-error-wash text-error',
  none: 'bg-tint-well text-muted shadow-[inset_0_0_0_1px_var(--color-tint-hover)]',
};

export function Blocks({ items, className, ...props }: { items: Block[] } & ComponentProps<'span'>) {
  if (!items.length) return null;
  return (
    <span className={cn('inline-flex h-5 shrink-0 gap-px overflow-hidden rounded-xs align-middle', className)} {...props}>
      {items.map(({ key, tone, Icon, label, title }) => (
        <span key={key} title={title} className={cn('inline-flex items-center gap-1 px-1.5 text-caption font-semibold whitespace-nowrap tabular-nums', TONES[tone])}>
          {Icon && <Icon aria-hidden className="size-2.75 shrink-0" strokeWidth={2.5} />}
          <span aria-hidden>{label}</span>
          <span className="sr-only">{title}</span>
        </span>
      ))}
    </span>
  );
}
