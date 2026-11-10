// The composer as the next cell (the notebook layout, lib/layout): indented by the cells' gutter,
// which says the number the next turn will take over "next". The composer itself, its queue and
// its attachments are `children`, untouched; in the current layout they render alone.

import type { ReactNode } from 'react';
import { useLayout } from '../../lib/layout';

export function ComposerCell({ turn, children }: Readonly<{ /** The number the next turn will take (lib/turns `threadTurns`). */ turn: number; children: ReactNode }>) {
  if (useLayout() !== 'notebook') return children;
  return (
    // The cells' own container and gutter rule (index.css): this gutter is there exactly when theirs are.
    <div className="nb-cell flex min-w-0 items-start">
      <p className="nb-gutter pt-2">
        <span className="block text-title tabular-nums text-ink">Turn {turn}</span>
        <span className="block text-caption text-muted">next</span>
      </p>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
