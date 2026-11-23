import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test } from 'vitest';
import { Markdown } from '../../src/components/common';

const body = () => within(screen.getByRole('table')).getAllByRole('row').slice(1).map(row => [...row.children].map(cell => cell.textContent));

test('a csv fence renders as the sortable table, and Show CSV gives the fence as written', async () => {
  const user = userEvent.setup();
  const csv = 'package,requests\nrouter,"48,210"\n"auth, core",864\n';
  render(<Markdown text={`Rows:\n\n\`\`\`csv\n${csv}\`\`\`\n`} />);
  expect(screen.getByRole('region', { name: 'Table: package, requests' })).toBeTruthy();
  expect(body()).toEqual([['router', '48,210'], ['auth, core', '864']]);
  expect(document.querySelector('pre')).toBeNull();
  // The figures sort as numbers, not text.
  await user.click(screen.getByRole('button', { name: 'Sort or filter requests' }));
  await user.click(screen.getByRole('button', { name: 'Sort ascending' }));
  expect(body().map(row => row[0])).toEqual(['auth, core', 'router']);

  await user.click(screen.getByRole('button', { name: 'Show CSV' }));
  expect(screen.queryByRole('table')).toBeNull();
  expect(document.querySelector('pre')?.textContent).toBe(csv);
  await user.click(screen.getByRole('button', { name: 'Show table' }));
  expect(screen.getByRole('table')).toBeTruthy();
});

test('a tsv fence and a json array of flat objects render as tables; the json stays one press away', async () => {
  const user = userEvent.setup();
  const json = '[{"name":"a","n":1,"ok":true},{"name":"b","n":null,"ok":false}]';
  const view = render(<Markdown text={'```tsv\nname\tn\na\t1\n```'} />);
  expect(body()).toEqual([['a', '1']]);
  view.unmount();
  render(<Markdown text={`\`\`\`json\n${json}\n\`\`\``} />);
  expect(body()).toEqual([['a', '1', 'true'], ['b', '—', 'false']]);
  await user.click(screen.getByRole('button', { name: 'Show JSON' }));
  expect(document.querySelector('pre')?.textContent).toBe(`${json}\n`);
});

test.each([
  ['ragged csv', '```csv\na,b\n1,2,3\n```'],
  ['an unclosed quote', '```csv\na,b\n"1,2\n```'],
  ['a json object', '```json\n{"a":1}\n```'],
  ['json objects with different keys', '```json\n[{"a":1},{"b":2}]\n```'],
  ['invalid json', '```json\n[{"a":1},\n```'],
])('%s stays a plain code block', (_, text) => {
  render(<Markdown text={text} />);
  expect(screen.queryByRole('table')).toBeNull();
  expect(screen.queryByRole('button', { name: /^Show / })).toBeNull();
  expect(document.querySelector('pre')?.textContent).toBe(`${text.split('\n').slice(1, -1).join('\n')}\n`);
  expect(screen.getByRole('button', { name: 'Copy code' })).toBeTruthy();
});

test('a csv fence still streaming is a code block until the reply completes', () => {
  const text = '```csv\na,b\n1,2\n```';
  const view = render(<Markdown text={text} streaming />);
  expect(screen.queryByRole('table')).toBeNull();
  view.rerender(<Markdown text={text} />);
  expect(body()).toEqual([['1', '2']]);
});
