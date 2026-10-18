import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api, ApiError } from '../../src/api';
import * as data from '../../src/mock/data';
import { renderApp, sidebar } from './render';

afterEach(() => vi.restoreAllMocks());

/** The confirmation hangs beside the row that asked (an anchored popover), never centred over the page. */
function expectAnchoredTo(dialog: HTMLElement, id: string) {
  const row = document.querySelector(`nav [data-task-row="${id}"]`)!;
  expect(dialog.hasAttribute('data-side')).toBe(true);
  expect(row.isConnected).toBe(true);
}

// In the mock, unified-agent-manager (p1) has 4 files not committed and 1 commit not pushed, and its Thread t15 runs in
// its own worktree with 2 not committed and 3 not pushed; notes-site (p3) is clean. The mock refuses on those counts.
describe('settling or archiving with unsaved git work', () => {
  const files = data.seed().changes.p1.map((c) => c.path);

  test('a Thread in its own worktree: the row says Settle…, asks at once beside the row with the counts, and Settle anyway settles', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const stage = vi.spyOn(api, 'stage');
    const settle = side.getByRole('button', { name: 'Settle Remove unused exports across packages' });
    expect(settle.textContent).toBe('Settle…');
    await user.click(settle);
    const dialog = await screen.findByRole('alertdialog', { name: 'Settle with unsaved work?' });
    // The worktree's work is all the Thread's own: it asks without a request the service would refuse, beside the row.
    expect(stage).not.toHaveBeenCalled();
    expectAnchoredTo(dialog, 't15');
    const guard = within(dialog);
    expect(guard.getByText(/Settling moves this Thread out of Active; this work is not saved anywhere else/)).toBeTruthy();
    expect(guard.getByText('2 not committed')).toBeTruthy();
    expect(guard.getByText('3 not pushed')).toBeTruthy();
    await user.click(guard.getByRole('button', { name: 'Settle anyway' }));
    await waitFor(() => expect(side.queryByRole('button', { name: 'Settle Remove unused exports across packages' })).toBeNull());
    expect(stage.mock.calls).toEqual([['t15', 'settle', { force: true }]]);
  });

  test('a Thread in a shared folder: the folder\'s counts do not decide, the service\'s refusal opens the confirmation with its files', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const stage = vi.spyOn(api, 'stage');
    const settle = side.getByRole('button', { name: 'Settle Doctor: add terminal line' });
    // The row still shows the folder's counts, but they are not this Thread's changes.
    expect(settle.textContent).toBe('Settle');
    await user.click(settle);
    const dialog = await screen.findByRole('alertdialog', { name: 'Settle with unsaved work?' });
    expect(stage.mock.calls).toEqual([['t3', 'settle']]);
    expectAnchoredTo(dialog, 't3');
    const guard = within(dialog);
    expect(guard.getByText('4 not committed')).toBeTruthy();
    expect(within(guard.getByRole('list', { name: 'Not committed' })).getAllByRole('listitem').map((li) => li.textContent)).toEqual([...files.slice(0, 3), `and ${files.length - 3} more`]);
    // Nothing moved yet.
    expect(document.querySelector('ul[aria-label="Unsettled threads"] [data-task-row="t3"] [data-settle]')).not.toBeNull();
    await user.click(guard.getByRole('button', { name: 'Settle anyway' }));
    await waitFor(() => expect(side.queryByRole('button', { name: 'Settle Doctor: add terminal line' })).toBeNull());
    expect(stage.mock.calls).toEqual([['t3', 'settle'], ['t3', 'settle', { force: true }]]);
  });

  test('work the summary did not know yet: the refusal opens the confirmation beside the row, and Escape keeps the Thread active', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const real = api.stage.bind(api);
    const stage = vi.spyOn(api, 'stage').mockImplementationOnce(async () => {
      throw new ApiError(409, 'This Thread\'s folder has unsaved git work.', { code: 'unsaved_work', git_pending: { uncommitted: 1, unpushed: 0 }, refs: ['notes.md'] });
    }).mockImplementation(real);
    await user.click(side.getByRole('button', { name: 'Settle Bump dependencies' }));
    const dialog = await screen.findByRole('alertdialog', { name: 'Settle with unsaved work?' });
    expectAnchoredTo(dialog, 't10');
    expect(within(dialog).getByText('notes.md')).toBeTruthy();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(stage).toHaveBeenCalledTimes(1);
    expect(document.querySelector('ul[aria-label="Unsettled threads"] [data-task-row="t10"]')).not.toBeNull();
  });

  test('Cancel keeps the Thread active; a clean Thread settles with no question', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'Settle Doctor: add terminal line' }));
    const guard = within(await screen.findByRole('alertdialog', { name: 'Settle with unsaved work?' }));
    await user.click(guard.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(side.getByRole('button', { name: 'Settle Doctor: add terminal line' })).toBeTruthy();

    const clean = side.getByRole('button', { name: 'Settle Bump dependencies' });
    expect(clean.textContent).toBe('Settle');
    await user.click(clean);
    await waitFor(() => expect(side.queryByRole('button', { name: 'Settle Bump dependencies' })).toBeNull());
    expect(screen.queryByRole('alertdialog')).toBeNull();
  });

  test('Archive from the Thread menu asks the same way, and Open Changes shows All changes', async () => {
    const { user } = renderApp('#task=t15');
    await screen.findByRole('region', { name: 'Conversation' });
    await user.click(await screen.findByRole('button', { name: 'Thread actions' }));
    const menu = within(await screen.findByRole('menu'));
    expect(menu.getByRole('menuitem', { name: 'Settle…' })).toBeTruthy();
    await user.click(menu.getByRole('menuitem', { name: 'Archive' }));
    const guard = within(await screen.findByRole('alertdialog', { name: 'Archive with unsaved work?' }));
    expect(screen.queryByRole('alertdialog', { name: 'Archive this thread?' })).toBeNull();
    await user.click(guard.getByRole('button', { name: 'Open Changes' }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    await waitFor(() => expect(sheet.getByRole('radio', { name: /^All changes/ }).getAttribute('aria-checked')).toBe('true'));
  });

  test('a clean-looking Archive refused over new work replaces today\'s dialog with the confirmation beside the Thread\'s row', async () => {
    const { user } = renderApp('#task=t10');
    await screen.findByRole('region', { name: 'Conversation' });
    const real = api.stage.bind(api);
    vi.spyOn(api, 'stage').mockImplementationOnce(async () => {
      throw new ApiError(409, 'This Thread\'s folder has unsaved git work.', { code: 'unsaved_work', git_pending: { uncommitted: 0, unpushed: 2 }, refs: [] });
    }).mockImplementation(real);
    await user.click(await screen.findByRole('button', { name: 'Thread actions' }));
    await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Archive' }));
    await user.click(within(await screen.findByRole('alertdialog', { name: 'Archive this thread?' })).getByRole('button', { name: 'Archive thread' }));
    const dialog = await screen.findByRole('alertdialog', { name: 'Archive with unsaved work?' });
    // The asking button was inside the closing dialog: the Thread's row stands for it.
    expectAnchoredTo(dialog, 't10');
    expect(within(dialog).getByText('2 not pushed')).toBeTruthy();
    expect(within(dialog).queryByRole('button', { name: 'Open Changes' })).toBeNull();
  });

  test('any other refusal is the notice line, not the confirmation', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    vi.spyOn(api, 'stage').mockRejectedValueOnce(new ApiError(409, 'stop the turn first'));
    await user.click(side.getByRole('button', { name: 'Settle Bump dependencies' }));
    expect(await screen.findByText(/Could not settle the thread: stop the turn first/)).toBeTruthy();
    expect(screen.queryByRole('alertdialog')).toBeNull();
  });
});
