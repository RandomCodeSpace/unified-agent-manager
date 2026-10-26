// Settings → Worktrees (src/mock/worktrees.ts): the disk, the Thread worktrees biggest first, and the removal log.
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp } from './render';

async function openWorktrees(search = '') {
  const rendered = renderApp(`${search}#settings`);
  await screen.findByRole('heading', { level: 1, name: 'Settings' });
  await rendered.user.click(screen.getByRole('button', { name: 'Projects & worktrees', exact: true }));
  const list = within(await screen.findByRole('list', { name: 'Worktrees' }));
  return { ...rendered, list };
}

const rowOf = (list: ReturnType<typeof within>, thread: RegExp) => within(list.getAllByRole('listitem').find((li) => thread.test(li.textContent ?? ''))!);

describe('Settings → Worktrees', () => {
  test('shows the disk, the worktrees biggest first with what is not saved, and what settling removed', async () => {
    const { list } = await openWorktrees();
    expect(screen.getByRole('heading', { name: 'Disk' })).toBeTruthy();
    expect(screen.getByText('64 GB free')).toBeTruthy();
    const rows = list.getAllByRole('listitem').map((li) => li.textContent);
    expect(rows).toHaveLength(4);
    expect(rows[0]).toMatch(/1\.9 GB.*Fix re-attach redraw regression.*uam\/fix-reattach-redraw.*2 not committed.*3 not pushed/);
    expect(rows[1]).toMatch(/1\.7 GB.*Doctor: add terminal line.*Saved/);
    expect(rows[2]).toMatch(/Pin GitHub Actions to Node 24.*Archived/);
    expect(rows[3]).toMatch(/412 MB/);
    const removed = within(screen.getByRole('list', { name: 'Removed on settling' }));
    expect(removed.getAllByRole('listitem')[0].textContent).toMatch(/Drop the vterm build tag from CI.*uam\/drop-vterm-build-tag.*1\.6 GB freed/);
    expect(screen.queryByText(/below 10%/)).toBeNull();
  });

  test('Remove takes a saved worktree at once; only one with changes asks first, and is the only red one', async () => {
    const { user, list } = await openWorktrees();
    const saved = rowOf(list, /Doctor/).getByRole('button', { name: 'Remove worktree uam/doctor-terminal-line' });
    const unsaved = rowOf(list, /Fix re-attach/).getByRole('button', { name: 'Remove worktree uam/fix-reattach-redraw' });
    expect(unsaved.className).toContain('text-error');
    expect(saved.className).not.toContain('text-error');

    await user.click(saved);
    await waitFor(() => expect(list.queryByText('Doctor: add terminal line')).toBeNull());
    expect(screen.queryByRole('alertdialog')).toBeNull();

    await user.click(unsaved);
    const confirm = within(await screen.findByRole('alertdialog', { name: 'Remove this worktree?' }));
    expect(confirm.getByText('2 changed files are not committed and will be lost.')).toBeTruthy();
    expect(confirm.getByText(/stays, with 3 commits not pushed/)).toBeTruthy();
    await user.click(confirm.getByRole('button', { name: 'Remove worktree' }));
    await waitFor(() => expect(list.queryByText('Fix re-attach redraw regression')).toBeNull());
  });

  test('the folder for new worktrees changes with the folder picker and resets to the default', async () => {
    const { user } = await openWorktrees();
    const card = within(screen.getByRole('region', { name: 'Folders' }));
    expect(card.getByText('~/.uam/worktrees')).toBeTruthy();
    expect(card.getByText('Existing worktrees stay where they are.')).toBeTruthy();
    const reset = card.getByRole('button', { name: 'Reset to default' }) as HTMLButtonElement;
    expect(reset.disabled).toBe(true);
    await user.click(card.getByRole('button', { name: 'Change…' }));
    const folders = within(await card.findByRole('listbox', { name: 'Folders' }));
    await user.click(await folders.findByRole('option', { name: /^projects/ }));
    await user.click(card.getByRole('button', { name: 'Use this folder' }));
    await card.findByText('~/projects');
    await waitFor(() => expect(document.getElementById('worktrees-folder-picker')).toBeNull());
    expect(reset.disabled).toBe(false);
    await user.click(reset);
    await card.findByText('~/.uam/worktrees');
  });

  test('under 10% free, the page, its rail row and the Settings gear warn', async () => {
    await openWorktrees('?disk=low');
    expect(screen.getByText(/below 10%/)).toBeTruthy();
    expect(screen.getAllByText('Disk space low: 31 GB free (8%)').length).toBeGreaterThanOrEqual(2);
  });
});
