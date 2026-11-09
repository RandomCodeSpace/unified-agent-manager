import { render, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { Markdown } from '../../src/components/common';
import * as visual from '../../src/lib/table-visual';

vi.mock('../../src/lib/table-visual', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../src/lib/table-visual')>();
  return { ...original, barColumns: vi.fn(original.barColumns) };
});

const source = '| package | year | requests |\n|---|---:|---:|\n| router | 2024 | 48,210 |\n| auth | 2025 | 31,904 |\n| search | 2025 | 0 |\n| health | 2026 | 864 |';
/** Each body row's cells as [text, bar width or null]. */
const cells = (table: HTMLElement) => within(table).getAllByRole('row').slice(1).map(row => [...row.children].map(cell => [cell.textContent, cell.querySelector('rect')?.getAttribute('width') ?? null]));

test('a numeric column draws one bar under each nonzero number; a year column and a zero do not', () => {
  const view = render(<Markdown text={source} />);
  expect(cells(view.getByRole('table'))).toEqual([
    [['router', null], ['2024', null], ['48,210', '100%']],
    [['auth', null], ['2025', null], ['31,904', `${(31904 / 48210) * 100}%`]],
    [['search', null], ['2025', null], ['0', null]],
    [['health', null], ['2026', null], ['864', `${(864 / 48210) * 100}%`]],
  ]);
  // The bar is decoration: the right-aligned column's bar grows from the right, and the number is read as text.
  const rect = view.getByRole('table').querySelector('rect')!;
  const svg = rect.closest('svg')!;
  expect(svg.getAttribute('aria-hidden')).toBe('true');
  expect(rect.getAttribute('x')).toBe('0%');
  // On a slim track below the number's line, not behind its digits, where near-equal values read as a stuck selection.
  expect(svg.previousSibling?.textContent).toBe('48,210');
  expect(svg.getAttribute('class')).toMatch(/(^| )top-\[calc\(1lh\+2px\)\] h-0\.75 .*bg-accent-wash/);
  expect(svg.closest('td')!.className).toContain('whitespace-nowrap');
  expect(rect.getAttribute('class')).toBe('fill-accent/50');
  expect(view.getByRole('cell', { name: '48,210' })).toBeTruthy();
});

test('a detector that throws leaves today\'s plain table', () => {
  const quiet = vi.spyOn(console, 'error').mockImplementation(() => {});
  vi.mocked(visual.barColumns).mockImplementation(() => { throw new Error('bad detector'); });
  try {
    const view = render(<Markdown text={source} />);
    const table = view.getByRole('table');
    expect(visual.barColumns).toHaveBeenCalled();
    expect(table.querySelector('rect')).toBeNull();
    expect(cells(table).map(row => row.map(([text]) => text))).toEqual([['router', '2024', '48,210'], ['auth', '2025', '31,904'], ['search', '2025', '0'], ['health', '2026', '864']]);
    expect(view.getByRole('button', { name: 'Sort or filter requests' })).toBeTruthy();
  } finally { vi.mocked(visual.barColumns).mockReset(); quiet.mockRestore(); }
});
