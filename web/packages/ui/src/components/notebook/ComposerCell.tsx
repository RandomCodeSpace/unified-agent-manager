// The composer as the next cell (the notebook layout, lib/layout): indented by the cells' gutter,
// which says the number the next turn will take over "next". The composer itself, its queue and
// its attachments are `children`, untouched; in the current layout they render alone.

import type { ReactNode } from 'react';
import { useLayout } from '../../lib/layout';

export function ComposerCell({ turn, children }: Readonly<{ /** The number the next turn will take (lib/tasks `nextTurn`). */ turn: number; children: ReactNode }>) {
  if (useLayout() !== 'notebook') return children;
  return (
    <div className="flex min-w-0 items-start">
      {/* A phone has no gutter: the composer keeps the whole row. */}
      <p className="flex w-gutter shrink-0 flex-col pt-2 max-sm:hidden">
        <span className="text-title tabular-nums text-ink">Turn {turn}</span>
        <span className="text-caption text-muted">next</span>
      </p>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
