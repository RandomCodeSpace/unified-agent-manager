// The todo reader in the app (DESIGN.md Strip sheet and todo reader): rows grouped by the turn that added them, a row's detail, the way to the turn.
import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { renderApp } from './render';

afterEach(() => vi.restoreAllMocks());

const openReader = async () => {
  const { user } = renderApp('#task=t8');
  await user.click(await screen.findByRole('button', { name: /^Todo .*Show the list$/ }));
  const reader = await screen.findByRole('dialog', { name: 'Todo' });
  // The sections wait for the kept records.
  await waitFor(() => expect(within(reader).getAllByRole('region')[0].getAttribute('aria-label')).toMatch(/^Turn 4 · /));
  return { user, reader };
};

test('the live reader groups the rows by the turn that added them, newest first, each with its tally and prompt', async () => {
  const { reader } = await openReader();
  const regions = within(reader).getAllByRole('region');
  expect(regions.map((r) => r.getAttribute('aria-label')!.replace(/ · .*/, ''))).toEqual(['Turn 4', 'Turn 2', 'Turn 1']);
  expect(reader.querySelector('p.pl-6')!.textContent).toBe('3 turns');
  // One gutter across the sections links the open rows: the linter's two waits and the summary's three (a wait on a done row is met).
  await waitFor(() => expect(reader.querySelectorAll('svg[aria-hidden] > g')).toHaveLength(5));
  const [latest, second, first] = regions.map((r) => within(r));
  // A row no record holds yet is the running turn's; the prompt is the held user message's first line, else the record's intent.
  expect(latest.getAllByRole('listitem').map((li) => li.getAttribute('data-row'))).toEqual(['install', 'tokens']);
  expect(latest.getByText('1 done · 1 open')).toBeTruthy();
  expect(latest.getByText(/^“Audit templates\/post.html/)).toBeTruthy();
  expect(second.getAllByRole('listitem').map((li) => li.getAttribute('data-row'))).toEqual(['contrast', 'lint', 'headings']);
  expect(second.getByText('1 done · 1 open · 1 blocked')).toBeTruthy();
  expect(second.getByText('“Fixing the markup”')).toBeTruthy();
  expect(first.getAllByRole('listitem').map((li) => li.getAttribute('data-row'))).toEqual(['alt', 'summary', 'plan']);
});

test('the list waits for the kept records, so it opens by turn without showing the stages first', async () => {
  const { user } = renderApp('#task=t8');
  // The mock service installs fetch with the app; the history read waits on it.
  const original = globalThis.fetch;
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    if (String(input instanceof Request ? input.url : input).endsWith('/todos/history')) await held;
    return original(input, init);
  });
  await user.click(await screen.findByRole('button', { name: /^Todo .*Show the list$/ }));
  const reader = await screen.findByRole('dialog', { name: 'Todo' });
  expect(within(reader).queryAllByRole('region')).toHaveLength(0);
  expect(within(reader).queryAllByRole('listitem')).toHaveLength(0);
  release();
  await waitFor(() => expect(within(reader).getAllByRole('region')[0].getAttribute('aria-label')).toMatch(/^Turn 4 · /));
});

test('a row opens its detail: what it waits on and unblocks, each opening its own row, its history and the subagent', async () => {
  const { user, reader } = await openReader();
  const lint = within(reader).getByRole('button', { name: /^Run the accessibility linter/ });
  await user.click(lint);
  const detail = within(lint.closest('li')!);
  const terms = [...lint.closest('li')!.querySelectorAll('dt')].map((dt) => dt.textContent);
  expect(terms).toEqual(['Waits on', 'Unblocks', 'History']);
  expect(detail.getByRole('button', { name: /^Install axe-core/ })).toBeTruthy();
  expect(detail.getByRole('button', { name: /^Summarise the fixes in the reply/ })).toBeTruthy();
  expect(detail.getByRole('button', { name: /^Go to turn 2/ })).toBeTruthy();
  expect(detail.getByRole('button', { name: 'Open Run the accessibility linter' })).toBeTruthy();
  // A dependency opens its own row, and only that one.
  await user.click(detail.getByRole('button', { name: /^Install axe-core/ }));
  const install = within(reader).getByRole('button', { name: /^Install axe-core/ });
  expect(install.getAttribute('aria-expanded')).toBe('true');
  await waitFor(() => expect(document.activeElement).toBe(install));
  expect(lint.getAttribute('aria-expanded')).toBe('false');
});

test('Go to turn closes the reader and lands on the prompt that started the turn', async () => {
  const { user, reader } = await openReader();
  await user.click(within(within(reader).getAllByRole('region')[0]).getByRole('button', { name: /^Go to turn/ }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Todo' })).toBeNull());
  await waitFor(() => expect(document.querySelector('[data-reply="i1"] .animate-flash, [data-reply="i1"].animate-flash')).toBeTruthy());
});

test('with the Board setting the reader is a board: a card sits in its turn\'s column and its agent\'s lane, and a lane opens its subagent', async () => {
  localStorage.setItem('uam.todoGraph', 'graph');
  try {
    const { user } = renderApp('#task=t8');
    await user.click(await screen.findByRole('button', { name: /^Todo .*Show the list$/ }));
    const reader = await screen.findByRole('dialog', { name: 'Todo' });
    const board = await within(reader).findByRole('group', { name: 'Todo board' });
    await waitFor(() => expect(within(board).getAllByText(/^Turn \d/).map((h) => h.textContent!.replace(/ · .*/, ''))).toEqual(['Turn 1', 'Turn 2', 'Turn 4']));
    // After the arrows, rows of four cells (the lane name, then three turns), the head first; lanes: the main agent, then subagents in their rows' order.
    const cells = [...board.children].slice(1);
    const at = (id: string) => {
      const i = cells.findIndex((c) => c.querySelector(`[data-card="${id}"]`));
      return [Math.floor(i / 4), i % 4];
    };
    expect(at('lint')).toEqual([5, 2]);
    expect(at('install')).toEqual([1, 3]);
    expect(at('plan')).toEqual([1, 1]);
    await user.click(within(board).getByRole('button', { name: 'Open subagent Run the accessibility linter' }));
    await waitFor(() => expect(window.location.hash).toBe('#task=t8&agent=a3'));
  } finally {
    localStorage.removeItem('uam.todoGraph');
  }
});
