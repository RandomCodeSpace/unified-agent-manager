import type { LucideIcon } from 'lucide-react';
import type { ComponentProps, ReactNode } from 'react';
import { cn } from '../../lib/cn';

/**
 * DESIGN.md blocks: a 20px row of joined neutral cells, each a glyph and a number (or a short word).
 * A cell is a fact, so its fill is neutral and its tone is on the glyph; a broken or act-now cell (warning, error,
 * attention) also sets its word in the tone, and a cell with no glyph carries the tone on its word.
 * The status strip's todo and subagent counts, the footer's phase and time split and a turn's
 * outcome (Todo, GitLab state, a step's result) all speak this one language, so a count reads
 * the same wherever it appears. The glyph keeps a cell readable without its colour.
 */
export type BlockTone = 'success' | 'accent' | 'warning' | 'error' | 'attention' | 'none';

export interface Block {
  key: string;
  tone: BlockTone;
  Icon?: LucideIcon;
  label: ReactNode;
  /** What the cell means in words, for screen readers and the hover title. */
  title: string;
}

const TONES: Record<BlockTone, string> = {
  success: 'text-success',
  accent: 'text-accent',
  warning: 'text-warning',
  error: 'text-error',
  attention: 'text-attention',
  none: 'text-muted',
};
/** The tones whose word stays `ink` beside a toned glyph: what is done and what is in motion. */
const QUIET: ReadonlySet<BlockTone> = new Set<BlockTone>(['success', 'accent']);

export function Blocks({ items, className, ...props }: { items: Block[] } & ComponentProps<'span'>) {
  if (!items.length) return null;
  return (
    <span className={cn('inline-flex h-5 shrink-0 gap-px overflow-hidden rounded-sm align-middle', className)} {...props}>
      {items.map(({ key, tone, Icon, label, title }) => (
        <span key={key} title={title} data-tone={tone} className={cn('inline-flex items-center gap-1 bg-tint-hover px-1.5 text-caption font-medium whitespace-nowrap tabular-nums', Icon && QUIET.has(tone) ? 'text-ink' : TONES[tone])}>
          {Icon && <Icon aria-hidden className={cn('size-2.75 shrink-0', TONES[tone])} strokeWidth={2.5} />}
          <span aria-hidden>{label}</span>
          <span className="sr-only">{title}</span>
        </span>
      ))}
    </span>
  );
}
