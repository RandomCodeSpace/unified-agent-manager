// Which columns of a reply's table get in-cell bars. Pure: no DOM, no React, so the unit tests run
// in node. Any doubt returns null for that column, and the table reads as it does today.

type Value = string | number | null;

// Numbers as tables print them: a sign, a leading currency symbol, thousands separators and a
// trailing percent. Leading zeros (IDs), dates and other units remain text.
const formatted = /^([+-]?)(\p{Sc}?)((?:0|[1-9]\d{0,2}(?:,\d{3})+|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)(%?)$/u;
export function numeric(v: Value): number | null {
  if (typeof v === 'number') return Number.isFinite(v) ? v : null;
  const match = v == null ? null : formatted.exec(v);
  const n = match ? Number(match[1] + match[3].replaceAll(',', '')) : NaN;
  return Number.isFinite(n) ? n : null;
}

/** The unit a numeric cell prints with: its currency symbol and percent sign, if any. */
function unit(v: Value): string {
  const match = typeof v === 'string' ? formatted.exec(v) : null;
  return match ? match[2] + match[4] : '';
}

/** Headers that name a label rather than a quantity. */
const LABEL_HEADER = /(?:^|[^a-z])(?:id|ids|port|pid|line|year|version)(?:$|[^a-z])|#/i;
const MAX_ROWS = 500;
const MAX_COLUMNS = 12;
const MIN_ROWS = 3;

/**
 * Each column's largest value when its cells read as one quantity, else null: every non-empty cell
 * numeric and ≥ 0, at least 3 of them, one unit (the same currency symbol and percent sign, or
 * none), not a label header, not all years (1900–2100 integers), not the row numbers 1..n, and a
 * largest value above 0. Tables past 500 rows or 12 columns get no bars.
 */
export function barColumns(columns: readonly { name: string }[], rows: readonly { values: readonly Value[] }[]): (number | null)[] | null {
  if (rows.length > MAX_ROWS || columns.length > MAX_COLUMNS) return null;
  return columns.map((column, index) => {
    if (LABEL_HEADER.test(column.name)) return null;
    const cells = rows.map(row => row.values[index]).filter(v => v != null && v !== '');
    if (cells.length < MIN_ROWS || cells.some(v => unit(v) !== unit(cells[0]))) return null;
    const values: number[] = [];
    for (const cell of cells) {
      const n = numeric(cell);
      if (n === null || n < 0) return null;
      values.push(n);
    }
    if (values.every(n => Number.isInteger(n) && n >= 1900 && n <= 2100)) return null;
    if (values.every((n, i) => n === i + 1)) return null;
    const max = Math.max(...values);
    return max > 0 ? max : null;
  });
}
