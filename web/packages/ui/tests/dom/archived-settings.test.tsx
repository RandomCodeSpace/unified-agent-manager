// Settings → Archived: the archived Tasks the sidebar no longer shelves (mock t12, archived a day ago, and t13, five days ago).
import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import * as data from '../../src/mock/data';
import { openMenu, renderApp, sidebar } from './render';

afterEach(() => vi.restoreAllMocks());

async function openArchived(hash = '#settings') {
  const rendered = renderApp(hash);
  await screen.findByRole('heading', { level: 1, name: 'Settings' });
  await rendered.user.click(screen.getByRole('button', { name: 'Archived', exact: true }));
  const list = await screen.findByRole('list', { name: 'Archived tasks' });
  return { ...rendered, list: within(list) };
}

const titles = (list: ReturnType<typeof within>) => list.getAllByRole('listitem').map((li) => li.querySelector('button')!.textContent);

describe('Settings → Archived', () => {
  test('lists the archived Tasks newest archived first, each with its Project and when', async () => {
    const { list } = await openArchived();
    const rows = titles(list);
    expect(rows).toHaveLength(2);
    expect(rows[0]).toMatch(/Pin GitHub Actions to Node 24.*1d ago/);
    expect(rows[1]).toMatch(/Migrate the RSS template to Atom.*5d ago/);
    // Few rows: no filter box.
    expect(screen.queryByRole('searchbox', { name: 'Filter archived tasks' })).toBeNull();
  });

  test('a row opens its Task and closes Settings', async () => {
    const { user, list } = await openArchived();
    await user.click(list.getByRole('button', { name: /Node 24.*archived/ }));
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Pin GitHub Actions to Node 24'));
    expect(window.location.hash).toBe('#task=t12');
    expect(await screen.findByText('Archived. This task is read-only.')).toBeTruthy();
  });

  test('a row\'s menu holds the Task actions; Delete confirms, then the row leaves', async () => {
    const { user, list } = await openArchived();
    const menu = await openMenu(user, 'Actions for Pin GitHub Actions to Node 24');
    expect(menu.getByRole('menuitem', { name: 'Run again' })).toBeTruthy();
    expect(menu.getByRole('menuitem', { name: 'Export as Markdown' })).toBeTruthy();
    await user.click(menu.getByRole('menuitem', { name: 'Delete' }));
    const confirm = await screen.findByRole('alertdialog', { name: /Delete “Pin GitHub Actions to Node 24”\?/ });
    await user.click(within(confirm).getByRole('button', { name: 'Delete task' }));
    await waitFor(() => expect(list.queryByRole('button', { name: /Node 24.*archived/ })).toBeNull());
    expect(screen.getByRole('heading', { level: 1, name: 'Settings' })).toBeTruthy();
  });

  test('a long list offers a filter', async () => {
    const state = data.seed();
    const base = state.tasks.find((t) => t.id === 't13')!;
    for (let i = 0; i < 9; i++) state.tasks.push({ ...base, id: `x${i}`, name: `Old chore ${i}`, title: '', archived_at: new Date(Date.now() - (10 + i) * 86400000).toISOString() });
    vi.spyOn(data, 'seed').mockReturnValue(state);
    const { user, list } = await openArchived();
    expect(titles(list)).toHaveLength(11);
    const filter = screen.getByRole('searchbox', { name: 'Filter archived tasks' });
    await user.type(filter, 'Atom');
    await waitFor(() => expect(titles(list)).toHaveLength(1));
    expect(screen.getByText('1 of 11 tasks')).toBeTruthy();
    await user.clear(filter);
    await user.type(filter, 'zzzz');
    expect(await screen.findByText('No archived tasks match “zzzz”.')).toBeTruthy();
  });

  test('the sidebar has no Archived shelf, and its search still finds an archived Task', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    expect(side.queryByRole('button', { name: /^Archived/ })).toBeNull();
    expect(side.queryByRole('button', { name: /Pin GitHub Actions to Node 24/ })).toBeNull();
    await user.type(side.getByRole('searchbox', { name: 'Search tasks' }), 'Node 24');
    expect(await side.findByText('1 matching task')).toBeTruthy();
    await user.click(side.getByRole('button', { name: /Pin GitHub Actions to Node 24/ }));
    await waitFor(() => expect(window.location.hash).toBe('#task=t12'));
  });
});
