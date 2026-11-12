import { render, screen, waitFor, within } from '@testing-library/react';
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
  // A labelled section, not a div with the region role.
  expect(region.tagName).toBe('SECTION');
  await user.tab();
  expect(document.activeElement).toBe(region);
  // It scrolls down and, when the table is wider than the message, sideways in its own box (css.test.mjs): long cells still wrap.
  expect(table.tagName).toBe('TABLE');
  expect(table.getAttribute('role')).toBeNull();
  expect(table.style.display).not.toBe('block');
  expect(table.querySelectorAll(':scope > thead > tr')).toHaveLength(1);
  expect(table.querySelectorAll(':scope > tbody > tr')).toHaveLength(1);
  const headers = within(table).getAllByRole('columnheader');
  expect(headers.map(header => header.tagName)).toEqual(['TH', 'TH']);
  expect(headers.map(header => header.textContent)).toEqual(['Name', 'Value']);
  // Streaming, the column's own alignment; whole, the table's (css.test.mjs).
  expect(streaming ? headers[1].style.textAlign : 'right').toBe('right');
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

test('an overflowing table pins a bar naming the rows below its visible bottom, which scrolls to the end', async () => {
  const user = userEvent.setup();
  // happy-dom has no layout: the region shows 300px of a 360px table whose rows are 30px apart, below a 30px header.
  let scrollTop = 0;
  const geometry: Record<string, PropertyDescriptor> = {
    scrollHeight: { get() { return this.tagName === 'SECTION' ? 360 : 0; } },
    clientHeight: { get() { return this.tagName === 'SECTION' ? 300 : 0; } },
    scrollTop: { get() { return this.tagName === 'SECTION' ? scrollTop : 0; } },
  };
  const saved = Object.keys(geometry).map(key => [key, Object.getOwnPropertyDescriptor(HTMLElement.prototype, key)] as const);
  for (const [key, descriptor] of Object.entries(geometry)) Object.defineProperty(HTMLElement.prototype, key, { configurable: true, ...descriptor });
  const rect = vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    const index = this.tagName === 'TR' && this.parentElement?.tagName === 'TBODY' ? [...this.parentElement.children].indexOf(this) : -1;
    return { top: (index + 1) * 30 - scrollTop } as DOMRect;
  });
  const scrollTo = vi.spyOn(HTMLElement.prototype, 'scrollTo').mockImplementation(() => {});
  try {
    const rows = Array.from({ length: 11 }, (_, index) => `| row ${index + 1} | ${index} |`).join('\n');
    const { unmount } = render(<Markdown text={`| Name | Count |\n| --- | ---: |\n${rows}`} />);
    const region = screen.getByRole('region', { name: 'Table: Name, Count' });
    // Rows 10 and 11 start at 300px and 330px: at or below the visible bottom.
    const bar = within(region).getByRole('button', { name: 'Scroll to the end: 2 more rows · 11 total' });
    expect(bar.textContent).toBe('Scroll to the end: 2 more rows · 11 total');
    await user.click(bar);
    expect(scrollTo).toHaveBeenCalledWith({ top: 360 });
    expect(document.activeElement).toBe(region);
    scrollTop = 60;
    region.dispatchEvent(new Event('scroll'));
    await waitFor(() => expect(within(region).queryByRole('button', { name: /^Scroll to the end/ })).toBeNull());
    unmount();
    // A table that fits has no bar.
    scrollTop = 0;
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', { configurable: true, get() { return this.tagName === 'SECTION' ? 300 : 0; } });
    render(<Markdown text={`| Name | Count |\n| --- | ---: |\n${rows}`} />).unmount();
    expect(screen.queryByRole('button', { name: /^Scroll to the end/ })).toBeNull();
    // Paged, the bar counts the rows on this page, not the table's total: rows 10 to 100 of the first page are below.
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', { configurable: true, get() { return this.tagName === 'SECTION' ? 360 : 0; } });
    const many = Array.from({ length: 150 }, (_, index) => `| row ${index + 1} | ${index} |`).join('\n');
    render(<Markdown text={`| Name | Count |\n| --- | ---: |\n${many}`} />);
    expect(await screen.findByRole('button', { name: 'Scroll to the end: 91 more rows · 100 on this page' })).toBeTruthy();
    expect(screen.getByRole('status').textContent).toBe('1–100 of 150 rows');
  } finally {
    rect.mockRestore();
    scrollTo.mockRestore();
    for (const [key, descriptor] of saved) if (descriptor) Object.defineProperty(HTMLElement.prototype, key, descriptor); else delete (HTMLElement.prototype as unknown as Record<string, unknown>)[key];
  }
});

test('a column of figures left unaligned is right-aligned; text and an alignment the author chose stay', () => {
  render(<Markdown text={'| Name | Tests | Time | Note | Count |\n| --- | --- | --- | --- | :--- |\n| a | 412 | 48 s | 12 files | 7 |\n| b | 1,880 | 3 s | | 9 |'} />);
  const headers = within(screen.getByRole('table')).getAllByRole('columnheader');
  expect(headers.map(header => header.className)).toEqual(['text-left', 'text-right', 'text-right', 'text-left', 'text-left']);
  // The figures line up digit under digit.
  expect(within(screen.getByRole('table')).getAllByRole('cell').slice(0, 5).map(cell => cell.className.includes('tabular-nums'))).toEqual([false, true, true, false, false]);
});

test('rows group under each value of a column, fold, keep the sort inside a group and ungroup', async () => {
  const user = userEvent.setup();
  render(<Markdown text={'| Package | Area | Tests |\n| --- | --- | --- |\n| web | ui | 30 |\n| store | core | 5 |\n| chart | ui | 12 |\n| queue | core | 40 |\n| docs |  | 1 |'} />);
  const table = screen.getByRole('table');
  const heads = () => within(table).getAllByRole('rowheader').map(head => head.textContent);
  // A column where every row differs, or none does, has nothing to group.
  await user.click(screen.getByRole('button', { name: 'Sort or filter Package' }));
  expect(screen.getByRole<HTMLButtonElement>('button', { name: 'Group rows by this column' }).disabled).toBe(true);
  await user.keyboard('{Escape}');
  await choose(user, 'Area', 'Group rows by this column');
  // Groups come in the order their value first appears; a row without a value has a group of its own.
  expect(heads()).toEqual(['ui2 rows', 'core2 rows', 'No value1 row']);
  expect(names()).toEqual(['ui2 rows', 'web', 'chart', 'core2 rows', 'store', 'queue', 'No value1 row', 'docs']);
  expect(screen.getByRole('status').textContent).toBe('5 rows in 3 groups');
  expect(screen.getByRole('button', { name: 'Sort or filter Area' }).title).toBe('Rows grouped');
  // A sort orders the rows inside each group, and the groups by their first row.
  await choose(user, 'Tests', 'Sort descending');
  expect(names()).toEqual(['core2 rows', 'queue', 'store', 'ui2 rows', 'web', 'chart', 'No value1 row', 'docs']);
  // Folding a group hides its rows and keeps its head and count.
  const core = within(table).getByRole('button', { name: /^core/ });
  expect(core.getAttribute('aria-expanded')).toBe('true');
  await user.click(core);
  expect(core.getAttribute('aria-expanded')).toBe('false');
  expect(names()).toEqual(['core2 rows', 'ui2 rows', 'web', 'chart', 'No value1 row', 'docs']);
  await user.click(screen.getByRole('button', { name: 'Fold all' }));
  expect(names()).toEqual(['core2 rows', 'ui2 rows', 'No value1 row']);
  await user.click(screen.getByRole('button', { name: 'Unfold all' }));
  expect(names()).toHaveLength(8);
  // A filter narrows the groups with the rows.
  await user.click(screen.getByRole('button', { name: 'Sort or filter Package' }));
  await user.type(screen.getByRole('textbox', { name: 'Package contains' }), 'o');
  await user.keyboard('{Escape}');
  expect(names()).toEqual(['core1 row', 'store', 'No value1 row', 'docs']);
  expect(screen.getByRole('status').textContent).toBe('2 rows in 2 groups (5 total)');
  await user.click(screen.getByRole('button', { name: 'Clear filters' }));
  await user.click(screen.getByRole('button', { name: 'Ungroup' }));
  expect(within(table).queryAllByRole('rowheader')).toHaveLength(0);
  expect(names()).toEqual(['queue', 'web', 'chart', 'store', 'docs']);
});

test('a long grouped table pages by line, and a group that runs on repeats its head', async () => {
  const user = userEvent.setup();
  const rows = Array.from({ length: 150 }, (_, i) => `| item ${i} | ${i < 120 ? 'first' : 'second'} |`).join('\n');
  render(<Markdown text={`| Name | Group |\n| --- | --- |\n${rows}`} />);
  await choose(user, 'Group', 'Group rows by this column');
  expect(screen.getByRole('status').textContent).toBe('150 rows in 2 groups, page 1 of 2');
  expect(names().slice(0, 2)).toEqual(['first120 rows', 'item 0']);
  await user.click(screen.getByRole('button', { name: 'Next rows' }));
  // The second page opens inside the first group, under its head again.
  expect(names().slice(0, 2)).toEqual(['first120 rows', 'item 99']);
  expect(names()).toContain('second30 rows');
});
