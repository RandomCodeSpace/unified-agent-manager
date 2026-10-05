import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { Markdown } from '../../src/components/common';

const source = '| File | Count | Status |\n| --- | ---: | --- |\n| [ten](https://example.test/ten) | 10 | Ready |\n| `two` | 2 | Ready |\n| zero | 0 | Pending |';
const names = () => within(screen.getByRole('table')).getAllByRole('row').slice(1).map(row => row.firstElementChild?.textContent);
type User = ReturnType<typeof userEvent.setup>;
const choose = async (user: User, column: string, choice: string, scope: Pick<typeof screen, 'getByRole'> = screen) => {
  await user.click(scope.getByRole('button', { name: `Sort or filter ${column}` }));
  await user.click(screen.getByRole('button', { name: choice }));
};

test.each([false, true])('Markdown preserves native table semantics and keyboard scrolling (streaming: %s)', async streaming => {
  const user = userEvent.setup();
  const value = 'very-long-value'.repeat(20);
  render(<Markdown text={`| Name | Value |\n| --- | ---: |\n| original path | ${value} |`} streaming={streaming} />);
  const region = screen.getByRole('region', { name: streaming ? 'Table' : 'Table: Name, Value' });
  const table = within(region).getByRole('table');
  expect(region.tabIndex).toBe(0);
  await user.tab();
  expect(document.activeElement).toBe(region);
  // It scrolls down, never sideways: long cells wrap.
  expect(region.classList.contains('overflow-y-auto')).toBe(true);
  expect(region.classList.contains('overflow-x-hidden')).toBe(true);
  expect(table.tagName).toBe('TABLE');
  expect(table.getAttribute('role')).toBeNull();
  expect(table.style.display).not.toBe('block');
  expect(table.classList.contains('w-full')).toBe(true);
  expect(table.querySelectorAll(':scope > thead > tr')).toHaveLength(1);
  expect(table.querySelectorAll(':scope > tbody > tr')).toHaveLength(1);
  const headers = within(table).getAllByRole('columnheader');
  expect(headers.map(header => header.tagName)).toEqual(['TH', 'TH']);
  expect(headers.map(header => header.textContent)).toEqual(['Name', 'Value']);
  expect(headers[1].style.textAlign === 'right' || headers[1].classList.contains('text-right')).toBe(true);
  const cells = within(table).getAllByRole('cell');
  expect(cells.map(cell => cell.tagName)).toEqual(['TD', 'TD']);
  expect(cells.map(cell => cell.textContent)).toEqual(['original path', value]);
});

test('Markdown tables sort numbers, keep formatted cells and return to source order', async () => {
  const user = userEvent.setup();
  const fetch = vi.spyOn(globalThis, 'fetch');
  try {
    render(<Markdown text={source} />);
    await choose(user, 'Count', 'Sort ascending');
    expect(names()).toEqual(['zero', 'two', 'ten']);
    expect(screen.getByRole('columnheader', { name: /Count/ }).getAttribute('aria-sort')).toBe('ascending');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.getByText('two').tagName).toBe('CODE');
    expect(screen.getByRole('link', { name: 'ten' }).getAttribute('href')).toBe('https://example.test/ten');
    await choose(user, 'Count', 'Sort descending');
    expect(names()).toEqual(['ten', 'two', 'zero']);
    await choose(user, 'Count', 'Original order');
    expect(names()).toEqual(['ten', 'two', 'zero']);
    expect(screen.getByRole('columnheader', { name: /Count/ }).getAttribute('aria-sort')).toBe('none');
    expect(fetch).not.toHaveBeenCalled();
  } finally { fetch.mockRestore(); }
});

test('column filters combine locally, show empty results and clear without changing the source', async () => {
  const user = userEvent.setup();
  render(<Markdown text={source} />);
  expect(screen.queryByRole('button', { name: 'Clear filters' })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Sort or filter Status' }));
  await user.type(screen.getByRole('textbox', { name: 'Status contains' }), 'ready');
  await user.keyboard('{Escape}');
  expect(names()).toEqual(['ten', 'two']);
  expect(screen.getByText('2 of 3 rows')).toBeTruthy();
  await user.click(screen.getByRole('button', { name: 'Sort or filter File' }));
  await user.type(screen.getByRole('textbox', { name: 'File contains' }), 'two');
  expect(names()).toEqual(['two']);
  await user.type(screen.getByRole('textbox', { name: 'File contains' }), ' absent');
  expect(screen.getByText('No matching rows')).toBeTruthy();
  await user.keyboard('{Escape}');
  await user.click(screen.getByRole('button', { name: 'Clear filters' }));
  expect(names()).toEqual(['ten', 'two', 'zero']);
});

test('a streaming table keeps its plain rows and gains controls only when complete', () => {
  const view = render(<Markdown text={source} streaming />);
  expect(screen.getAllByRole('row')).toHaveLength(4);
  expect(screen.queryByRole('button', { name: 'Sort or filter Count' })).toBeNull();
  view.rerender(<Markdown text={source} />);
  expect(screen.getByRole('button', { name: 'Sort or filter Count' })).toBeTruthy();
});

test('text identifiers keep their leading zeros and a table filter leaves its neighbor alone', async () => {
  const user = userEvent.setup();
  render(<Markdown text={'| ID |\n| --- |\n| 10 |\n| 002 |\n| 2 |\n\nAnother table:\n\n| Name |\n| --- |\n| unchanged |'} />);
  const [first, second] = screen.getAllByRole('table');
  await choose(user, 'ID', 'Sort ascending', within(first));
  // Text with leading zeros stays text, in natural order.
  expect(within(first).getAllByRole('cell').map(cell => cell.textContent)).toEqual(['002', '2', '10']);
  await user.click(within(first).getByRole('button', { name: 'Sort or filter ID' }));
  await user.type(screen.getByRole('textbox', { name: 'ID contains' }), '002');
  expect(within(first).getAllByRole('cell').map(cell => cell.textContent)).toEqual(['002']);
  expect(within(second).getByRole('cell', { name: 'unchanged' })).toBeTruthy();
});

test('large completed tables bound their DOM rows while filtering and sorting all data', async () => {
  const user = userEvent.setup();
  const text = '| Entry | Count |\n| --- | --- |\n' + Array.from({ length: 1000 }, (_, i) => `| row-${i} | ${1000 - i} |`).join('\n');
  render(<Markdown text={text} />);
  expect(screen.getAllByRole('row')).toHaveLength(101);
  await user.click(screen.getByRole('button', { name: 'Next rows' }));
  expect(names()[0]).toBe('row-100');
  await choose(user, 'Count', 'Sort ascending');
  expect(names()[0]).toBe('row-999');
  await user.click(screen.getByRole('button', { name: 'Sort or filter Entry' }));
  await user.type(screen.getByRole('textbox', { name: 'Entry contains' }), 'row-998');
  expect(names()).toEqual(['row-998']);
});

test('formatted numbers sort by value; dates and IDs stay text in natural order', async () => {
  const user = userEvent.setup();
  render(<Markdown text={'| Model | Spend | Share | Day | Run |\n| --- | ---: | ---: | --- | --- |\n| a | 1,160.1 | 12% | 2026-10-02 | run-10 |\n| b | 111.2 | -3.5% | 2026-09-30 | run-9 |\n| c | 0 | 100% | 2026-10-01 | run-100 |\n| d | 63.9 | +0.5% | 2026-09-28 | run-1 |\n| e | $613.76 | 7% | 2026-09-29 | run-2 |\n| f | 1,293.0 | 0% | 2026-10-03 | run-20 |'} />);
  const column = (index: number) => within(screen.getByRole('table')).getAllByRole('row').slice(1).map(row => row.children[index].textContent);
  await choose(user, 'Spend', 'Sort ascending');
  expect(column(1)).toEqual(['0', '63.9', '111.2', '$613.76', '1,160.1', '1,293.0']);
  await choose(user, 'Share', 'Sort descending');
  expect(column(2)).toEqual(['100%', '12%', '7%', '+0.5%', '0%', '-3.5%']);
  await choose(user, 'Day', 'Sort ascending');
  expect(column(3)).toEqual(['2026-09-28', '2026-09-29', '2026-09-30', '2026-10-01', '2026-10-02', '2026-10-03']);
  await choose(user, 'Run', 'Sort ascending');
  expect(column(4)).toEqual(['run-1', 'run-2', 'run-9', 'run-10', 'run-20', 'run-100']);
});

test('a table is named by the heading above it, and its header controls are one tab stop', async () => {
  const user = userEvent.setup();
  render(<><Markdown text={'**copilot**\n\n| Date | Tokens (M) |\n| --- | ---: |\n| Sep 14 | 0 |\n\n| Tool | Tokens |\n| --- | ---: |\n| codex | 1 |'} /><button>After</button></>);
  const named = screen.getByRole('region', { name: 'Table: copilot' });
  expect(screen.getByRole('region', { name: 'Table: Tool, Tokens' })).toBeTruthy();
  const date = within(named).getByRole('button', { name: 'Sort or filter Date' });
  const tokens = within(named).getByRole('button', { name: 'Sort or filter Tokens (M)' });
  expect([date.tabIndex, tokens.tabIndex]).toEqual([0, -1]);
  date.focus();
  await user.keyboard('{ArrowRight}');
  expect(document.activeElement).toBe(tokens);
  expect([date.tabIndex, tokens.tabIndex]).toEqual([-1, 0]);
  await user.keyboard('{Home}');
  expect(document.activeElement).toBe(date);
  await user.tab();
  // The next stop is the other table's region, not this table's second control.
  expect(document.activeElement).toBe(screen.getByRole('region', { name: 'Table: Tool, Tokens' }));
});
