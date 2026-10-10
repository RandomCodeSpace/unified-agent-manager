import assert from 'node:assert/strict';
import test from 'node:test';
import { barColumns, numeric } from '../src/lib/table-visual.ts';

// A table as MarkdownTable builds it: column names and each row's cell text.
const table = (names, ...rows) => [names.map(name => ({ name })), rows.map(values => ({ values }))];
const bars = (names, ...rows) => barColumns(...table(names, ...rows));

test('numeric reads numbers as tables print them and leaves IDs, dates and units as text', () => {
  assert.equal(numeric('1,160.1'), 1160.1);
  assert.equal(numeric('$613.76'), 613.76);
  assert.equal(numeric('-3.5%'), -3.5);
  assert.equal(numeric('+0.5%'), 0.5);
  assert.equal(numeric(42), 42);
  for (const text of ['002', '2026-10-02', '12 ms', 'run-10', '', null]) assert.equal(numeric(text), null);
});

test('quantity columns get their largest value; label and zero columns get none', () => {
  assert.deepEqual(bars(['package', 'requests', 'p95 ms', 'errors', 'retries'],
    ['router', '48,210', '182', '37', '0'],
    ['auth', '31,904', '41', '3', '0'],
    ['health', '864', '2', '0', '0'],
  ), [null, 48210, 182, 37, null]);
  // Empty cells are skipped, not counted as zero or as doubt.
  assert.deepEqual(bars(['a'], ['5'], [''], ['7'], ['2']), [7]);
});

test('ID, port, pid, line, year, version and # headers stay plain', () => {
  const rows = [['4127'], ['4213'], ['4388']];
  for (const name of ['id', 'ID', 'user_id', 'Ticket IDs', 'port', 'PID', 'line', 'year', 'Version', '#', '# of runs']) {
    assert.deepEqual(bars([name], ...rows), [null], name);
  }
  // A word that merely contains those letters is a quantity.
  assert.deepEqual(bars(['valid'], ...rows), [4388]);
});

test('years, leading zeros, negatives, mixed units and 1..n row numbers stay plain', () => {
  assert.deepEqual(bars(['opened'], ['2024'], ['2025'], ['1999']), [null]);
  // One value outside 1900–2100 means these are not all years.
  assert.deepEqual(bars(['opened'], ['2024'], ['2025'], ['12,480']), [12480]);
  assert.deepEqual(bars(['code'], ['007'], ['12'], ['30']), [null]);
  assert.deepEqual(bars(['delta'], ['4'], ['-3'], ['9']), [null]);
  assert.deepEqual(bars(['share'], ['12%'], ['7'], ['30%']), [null]);
  assert.deepEqual(bars(['spend'], ['$12'], ['7'], ['$30']), [null]);
  assert.deepEqual(bars(['spend'], ['$12'], ['€7'], ['$30']), [null]);
  assert.deepEqual(bars(['share'], ['12%'], ['7%'], ['30%']), [30]);
  assert.deepEqual(bars(['spend'], ['$12'], ['$7'], ['$1,030']), [1030]);
  assert.deepEqual(bars(['n'], ['1'], ['2'], ['3'], ['4']), [null]);
  // The same numbers in another order are a quantity.
  assert.deepEqual(bars(['n'], ['2'], ['1'], ['3'], ['4']), [4]);
  assert.deepEqual(bars(['latency'], ['12 ms'], ['9 ms'], ['30 ms']), [null]);
});

test('fewer than 3 numbers, more than 500 rows or more than 12 columns get no bars', () => {
  assert.deepEqual(bars(['count'], ['5'], ['7']), [null]);
  assert.deepEqual(bars(['count'], ['5'], [''], ['7']), [null]);
  const many = Array.from({ length: 600 }, (_, i) => [String((i * 7) % 600 + 3)]);
  assert.equal(bars(['count'], ...many), null);
  const capped = many.slice(0, 500);
  assert.deepEqual(bars(['count'], ...capped), [Math.max(...capped.map(([v]) => Number(v)))]);
  const wide = Array.from({ length: 13 }, (_, i) => `c${i}`);
  assert.equal(bars(wide, wide.map(() => '5'), wide.map(() => '6'), wide.map(() => '8')), null);
});
