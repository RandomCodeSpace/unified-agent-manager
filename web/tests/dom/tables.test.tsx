import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { Markdown } from '../../src/components/common';

const source = '| File | Count | Status |\n| --- | ---: | --- |\n| [ten](https://example.test/ten) | 10 | Ready |\n| `two` | 2 | Ready |\n| zero | 0 | Pending |';
const names = () => within(screen.getByRole('table')).getAllByRole('row').slice(1).map(row => row.firstElementChild?.textContent);

test('Markdown tables sort numbers, keep formatted cells and return to source order', async () => {
  const user = userEvent.setup();
  const fetch = vi.spyOn(globalThis, 'fetch');
  try {
    render(<Markdown text={source} />);
    const sort = screen.getByRole('button', { name: 'Sort by Count' });
    await user.click(sort);
    expect(names()).toEqual(['zero', 'two', 'ten']);
    expect(screen.getByRole('columnheader', { name: /Count/ }).getAttribute('aria-sort')).toBe('ascending');
    expect(screen.getByText('two').tagName).toBe('CODE');
    expect(screen.getByRole('link', { name: 'ten' }).getAttribute('href')).toBe('https://example.test/ten');
    await user.click(sort);
    expect(names()).toEqual(['ten', 'two', 'zero']);
    await user.click(sort);
    expect(names()).toEqual(['ten', 'two', 'zero']);
    expect(screen.getByRole('columnheader', { name: /Count/ }).getAttribute('aria-sort')).toBe('none');
    expect(fetch).not.toHaveBeenCalled();
  } finally { fetch.mockRestore(); }
});

test('column filters combine locally, show empty results and clear without changing the source', async () => {
  const user = userEvent.setup();
  render(<Markdown text={source} />);
  expect(screen.queryByRole('button', { name: 'Clear filters' })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Filter Status' }));
  await user.type(screen.getByRole('textbox', { name: 'Status contains' }), 'ready');
  await user.keyboard('{Escape}');
  expect(names()).toEqual(['ten', 'two']);
  expect(screen.getByText('2 of 3 rows')).toBeTruthy();
  await user.click(screen.getByRole('button', { name: 'Filter File' }));
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
  expect(screen.queryByRole('button', { name: 'Sort by Count' })).toBeNull();
  view.rerender(<Markdown text={source} />);
  expect(screen.getByRole('button', { name: 'Sort by Count' })).toBeTruthy();
});

test('text identifiers keep their leading zeros and a table filter leaves its neighbor alone', async () => {
  const user = userEvent.setup();
  render(<Markdown text={'| ID |\n| --- |\n| 10 |\n| 002 |\n| 2 |\n\nAnother table:\n\n| Name |\n| --- |\n| unchanged |'} />);
  const [first, second] = screen.getAllByRole('table');
  await user.click(within(first).getByRole('button', { name: 'Sort by ID' }));
  expect(within(first).getAllByRole('cell').map(cell => cell.textContent)).toEqual(['002', '10', '2']);
  await user.click(within(first).getByRole('button', { name: 'Filter ID' }));
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
  await user.click(screen.getByRole('button', { name: 'Sort by Count' }));
  expect(names()[0]).toBe('row-999');
  await user.click(screen.getByRole('button', { name: 'Filter Entry' }));
  await user.type(screen.getByRole('textbox', { name: 'Entry contains' }), 'row-998');
  expect(names()).toEqual(['row-998']);
});
