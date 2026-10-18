import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { composer, openTask, type User } from './render';

/** Go to file, opened with Alt+P; its search box keeps focus while the keyboard walks the list. */
async function goToFile(user: User) {
  await user.keyboard('{Alt>}p{/Alt}');
  const dialog = within(await screen.findByRole('dialog', { name: 'Go to file' }));
  const box = dialog.getByRole('combobox', { name: 'Go to file' });
  await waitFor(() => expect(document.activeElement).toBe(box));
  return { dialog, box };
}

describe('Go to file', () => {
  test('Alt+P lists the changed files with their status in words, then a folder to browse, and previews the highlighted file', async () => {
    const { user } = await openTask('t3');
    const { dialog } = await goToFile(user);
    const changed = within(await dialog.findByRole('group', { name: 'Changed' }));
    expect(changed.getByRole('option', { name: 'internal/vterm/redraw.go, Modified' }).textContent).toContain('Modified');
    // The first row is highlighted and previews beside the list.
    await waitFor(() => expect(dialog.getByRole('region', { name: 'Preview' }).textContent).toContain('const line1 = "internal/vterm/redraw.go";'));
    // Browse: a folder drills in, Backspace on the empty box goes back up.
    const browse = within(await dialog.findByRole('group', { name: 'Browse' }));
    await user.click(await browse.findByRole('option', { name: /^Folder cmd/ }));
    const cmd = within(await dialog.findByRole('group', { name: 'Browse cmd' }));
    expect(await cmd.findByRole('option', { name: 'cmd/doctor.go' })).toBeTruthy();
    await user.keyboard('{Backspace}');
    expect(await dialog.findByRole('group', { name: 'Browse' })).toBeTruthy();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Go to file' })).toBeNull());
  });

  test('typing matches letters in order; Enter opens the file in the side panel', async () => {
    const { user } = await openTask('t3');
    const { dialog, box } = await goToFile(user);
    await user.type(box, 'dctr');
    const matches = within(await dialog.findByRole('group', { name: 'Matches' }));
    const first = await matches.findByRole('option', { name: 'cmd/doctor.go' });
    expect(first.getAttribute('aria-selected')).toBe('true');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Go to file' })).toBeNull());
    const sheet = await screen.findByRole('dialog', { name: 'File doctor.go' });
    const panel = within(sheet);
    await waitFor(() => expect(sheet.textContent).toContain('const line1 = "cmd/doctor.go";'));
    // An unchanged file has no Diff to switch to.
    expect(panel.queryByRole('radio', { name: 'Diff' })).toBeNull();
    await user.click(panel.getByRole('button', { name: 'Close the file' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'File doctor.go' })).toBeNull());
  });

  test('a changed file opens with a File | Diff switch', async () => {
    const { user } = await openTask('t3');
    const { box } = await goToFile(user);
    await user.type(box, 'redraw.go');
    await screen.findByRole('option', { name: 'internal/vterm/redraw.go, Modified' });
    await user.keyboard('{Enter}');
    const panel = within(await screen.findByRole('dialog', { name: 'File redraw.go' }));
    expect(await panel.findByText('Modified')).toBeTruthy();
    await user.click(await panel.findByRole('radio', { name: 'Diff' }));
    await waitFor(() => expect(panel.getByRole('table').textContent).toContain('replayFocusEvents'));
    await user.click(panel.getByRole('radio', { name: 'File' }));
    await waitFor(() => expect(panel.queryByRole('table')).toBeNull());
  });

  test('Alt+Enter mentions the highlighted file in the composer', async () => {
    const { user } = await openTask('t3');
    const { box } = await goToFile(user);
    await user.type(box, 'redraw.go');
    await screen.findByRole('option', { name: 'internal/vterm/redraw.go, Modified' });
    await user.keyboard('{Alt>}{Enter}{/Alt}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Go to file' })).toBeNull());
    await waitFor(() => expect(composer().value).toBe('@internal/vterm/redraw.go '));
    await waitFor(() => expect(document.activeElement).toBe(composer()));
  });

  test('the Files button left of the branch under the composer opens it; the header has none', async () => {
    const { user } = await openTask('t3');
    // Labelled "Files" (its name is what it shows); Go to file and Alt+P are its tip.
    const files = screen.getByRole('button', { name: 'Files' });
    expect(files.getAttribute('aria-keyshortcuts')).toBe('Alt+P');
    expect(files.nextElementSibling?.textContent).toContain(screen.getByRole('button', { name: /^Branch / }).textContent ?? '-');
    expect(within(screen.getByRole('heading', { level: 1 }).closest('header')!).queryByRole('button', { name: /^(Files|Go to file)$/ })).toBeNull();
    await user.click(files);
    expect(await screen.findByRole('dialog', { name: 'Go to file' })).toBeTruthy();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Go to file' })).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(files));
  });
});
