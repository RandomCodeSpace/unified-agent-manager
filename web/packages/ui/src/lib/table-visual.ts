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

// A figure with one unit from a closed set (owner, 2026-10-28): times, sizes and counts, each scaled
// within its family so a column of "812 ms" and "41 s" compares. "B" is billions, as in token counts.
const UNITS: Record<string, { family: string; scale: number }> = {
  ms: { family: 'time', scale: 1e-3 }, s: { family: 'time', scale: 1 }, min: { family: 'time', scale: 60 }, h: { family: 'time', scale: 3600 },
  KB: { family: 'bytes', scale: 1024 }, MB: { family: 'bytes', scale: 1024 ** 2 }, GB: { family: 'bytes', scale: 1024 ** 3 }, TB: { family: 'bytes', scale: 1024 ** 4 },
  KiB: { family: 'bytes', scale: 1024 }, MiB: { family: 'bytes', scale: 1024 ** 2 }, GiB: { family: 'bytes', scale: 1024 ** 3 },
  K: { family: 'count', scale: 1e3 }, M: { family: 'count', scale: 1e6 }, B: { family: 'count', scale: 1e9 },
};
const withUnit = /^((?:0|[1-9]\d{0,2}(?:,\d{3})+|[1-9]\d*)(?:\.\d+)?|0?\.\d+)\s?([a-zA-Z]{1,3})$/;

/** A figure with a known unit, scaled within its family ("812 ms" → 0.812, time), else null. */
function unitFigure(v: Value): { n: number; family: string } | null {
  const match = typeof v === 'string' ? withUnit.exec(v.trim()) : null;
  const u = match && UNITS[match[2]];
  return u ? { n: Number(match[1].replaceAll(',', '')) * u.scale, family: u.family } : null;
}

/** A cell's size for bars and sorting: its number, or its unit figure scaled within its family. */
export function magnitude(v: Value): number | null {
  return numeric(v) ?? unitFigure(v)?.n ?? null;
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
    if (cells.length < MIN_ROWS) return null;
    // Every cell a figure of one unit family ("41 s", "812 ms"), or every cell a plain number in one unit.
    const figures = cells.map(unitFigure);
    if (figures.every(f => f && f.family === figures[0]!.family)) {
      const max = Math.max(...figures.map(f => f!.n));
      return max > 0 ? max : null;
    }
    if (cells.some(v => unit(v) !== unit(cells[0]))) return null;
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

export type CellStatus = 'ok' | 'fail' | 'warn' | 'skip';
const STATUS_WORDS: Record<string, CellStatus> = {
  pass: 'ok', passed: 'ok', ok: 'ok', success: 'ok', succeeded: 'ok', '✓': 'ok', '✔': 'ok', '✅': 'ok',
  fail: 'fail', failed: 'fail', failure: 'fail', error: 'fail', '✗': 'fail', '✖': 'fail', '❌': 'fail',
  warn: 'warn', warning: 'warn', '⚠': 'warn',
  skip: 'skip', skipped: 'skip', '⏭': 'skip',
};

/** A cell's status when its whole text is a status word or mark ("pass", "Failed", "✓", "⚠️"), else null. */
export function cellStatus(v: Value): CellStatus | null {
  return typeof v === 'string' ? STATUS_WORDS[v.trim().toLowerCase().replaceAll('\uFE0F', '')] ?? null : null;
}

/** Each column whose every non-empty cell, two or more, is a status (`cellStatus`): drawn as toned labels. */
export function statusColumns(columns: readonly { name: string }[], rows: readonly { values: readonly Value[] }[]): boolean[] | null {
  if (rows.length > MAX_ROWS || columns.length > MAX_COLUMNS) return null;
  return columns.map((_, index) => {
    const cells = rows.map(row => row.values[index]).filter(v => v != null && v !== '');
    return cells.length >= 2 && cells.every(v => cellStatus(v) !== null);
  });
}
