/**
 * Rows an agent prints in a fence (```csv, ```tsv, ```json) as a table's columns and rows.
 * Anything that does not read as one regular table is null: the fence stays a code block.
 */
import type { ChartTable } from './chart-table';

/** The highlighter's bound (common.tsx MAX_HIGHLIGHT): a longer fence is not parsed. */
const MAX_SOURCE = 100_000;
const MAX_COLUMNS = 50;

/**
 * RFC 4180 records: a quoted field may hold the delimiter, line breaks and `""` for a quote;
 * lines end in CRLF or LF, and blank lines are skipped. A quote inside an unquoted field is
 * kept as written. Null when a quoted field is not closed or is followed by more text.
 */
export function parseDelimited(text: string, delimiter: string): string[][] | null {
  const rows: string[][] = [];
  let row: string[] = [];
  let i = 0;
  const end = () => {
    if (row.length > 1 || row[0] !== '') rows.push(row);
    row = [];
  };
  for (;;) {
    let field = '';
    if (text[i] === '"') {
      for (i++; ; i++) {
        if (i >= text.length) return null;
        if (text[i] !== '"') field += text[i];
        else if (text[i + 1] === '"') { field += '"'; i++; }
        else break;
      }
      i++;
      if (i < text.length && text[i] !== delimiter && text[i] !== '\n' && text[i] !== '\r') return null;
    } else {
      const from = i;
      while (i < text.length && text[i] !== delimiter && text[i] !== '\n' && text[i] !== '\r') i++;
      field = text.slice(from, i);
    }
    row.push(field);
    if (i >= text.length) { end(); return rows; }
    if (text[i] === delimiter) { i++; continue; }
    if (text[i] === '\r' && text[i + 1] === '\n') i++;
    i++;
    end();
    if (i >= text.length) return rows;
  }
}

/** A header row of two or more named columns, then one or more rows of exactly as many fields. */
function delimitedTable(source: string, delimiter: string): ChartTable | null {
  const records = parseDelimited(source, delimiter);
  if (!records || records.length < 2) return null;
  const [header, ...rows] = records;
  const columns = header.map(name => name.trim());
  if (columns.length < 2 || columns.length > MAX_COLUMNS || !columns.every(Boolean)) return null;
  return rows.every(row => row.length === columns.length) ? { columns, rows } : null;
}

/** An array of two or more objects with the same keys, each value a string, number, boolean or null. */
function jsonTable(source: string): ChartTable | null {
  let parsed: unknown;
  try { parsed = JSON.parse(source); } catch { return null; }
  if (!Array.isArray(parsed) || parsed.length < 2) return null;
  const flat = (entry: unknown): entry is Record<string, string | number | boolean | null> => !!entry && typeof entry === 'object' && !Array.isArray(entry)
    && Object.values(entry).every(value => value === null || ['string', 'number', 'boolean'].includes(typeof value));
  if (!parsed.every(flat)) return null;
  const columns = Object.keys(parsed[0]);
  if (!columns.length || columns.length > MAX_COLUMNS) return null;
  if (!parsed.every(entry => Object.keys(entry).length === columns.length && columns.every(column => Object.hasOwn(entry, column)))) return null;
  return { columns, rows: parsed.map(entry => columns.map((column) => { const value = entry[column]; return typeof value === 'boolean' ? String(value) : value; })) };
}

export const TABLE_FENCES: ReadonlySet<string> = new Set(['csv', 'tsv', 'json']);

export function fenceTable(language: string, source: string): ChartTable | null {
  if (source.length > MAX_SOURCE) return null;
  if (language === 'json') return jsonTable(source);
  if (language === 'csv') return delimitedTable(source, ',');
  return language === 'tsv' ? delimitedTable(source, '\t') : null;
}
