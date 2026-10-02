// How the composer's control row gives up room (DESIGN.md, Composer). Only reads widths, so the unit tests run in node.

/**
 * The row's folds in the order they apply as it narrows; each keeps the ones before it, so the Model
 * picker keeps its name longest and the actions never change:
 * - `execution`: Permissions and execution reads the permission alone ("Safe"); its glyph still says autopilot.
 * - `credits`: the AI credits value becomes its glyph, as on a phone.
 * - `tuning`: Effort and context size becomes its glyph.
 * - `permissions`: Permissions and execution becomes its glyph.
 * - `more`: both move into the More menu, as on a phone.
 * - `model`: the Model picker becomes its glyph.
 * - `wrap`: the actions take a row of their own.
 * Each fold is a word in the row's `data-fold`, which the controls style with `in-data-[fold~=…]`.
 */
export const FOLDS = ['execution', 'credits', 'tuning', 'permissions', 'more', 'model', 'wrap'] as const;

/** A label marked `data-squeeze` may truncate, but never below this (about five letters): its picker folds instead. */
export const SQUEEZE_MIN = 48;

interface Box {
  scrollWidth: number;
  clientWidth: number;
}
export interface FoldRow extends Box {
  dataset: { fold?: string };
  querySelectorAll(selector: '[data-squeeze]'): Iterable<Box>;
}

function fits(row: FoldRow): boolean {
  if (row.scrollWidth > row.clientWidth) return false;
  for (const label of row.querySelectorAll('[data-squeeze]')) if (label.clientWidth < Math.min(label.scrollWidth, SQUEEZE_MIN)) return false;
  return true;
}

/** Applies the fewest folds with which the row fits, all of them when none does; returns how many. */
export function foldToFit(row: FoldRow): number {
  for (let n = 0; ; n++) {
    row.dataset.fold = FOLDS.slice(0, n).join(' ');
    if (n === FOLDS.length || fits(row)) return n;
  }
}
