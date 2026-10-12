import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import * as data from '../../src/mock/data';
import { renderApp, sidebar } from './render';

// In the mock, unified-agent-manager (p1) has 4 files not committed and 1 commit not pushed; notes-site (p3) is clean.
describe('settling or archiving with unsaved git work', () => {
  const files = data.seed().changes.p1.map((c) => c.path);

  test('the row says Settle…, asks with the counts and the first files, and Settle anyway settles', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const settle = side.getByRole('button', { name: 'Settle Doctor: add terminal line' });
    expect(settle.textContent).toBe('Settle…');
    await user.click(settle);
    const guard = within(await screen.findByRole('alertdialog', { name: 'Settle with unsaved work?' }));
    expect(guard.getByText(/Settling moves this Thread out of Active; this work is not saved anywhere else/)).toBeTruthy();
    expect(guard.getByText('4 not committed')).toBeTruthy();
    expect(guard.getByText('1 not pushed')).toBeTruthy();
    expect(within(guard.getByRole('list', { name: 'Not committed' })).getAllByRole('listitem').map((li) => li.textContent)).toEqual([...files.slice(0, 3), `and ${files.length - 3} more`]);
    // Nothing moved yet.
    expect(document.querySelector('ul[aria-label="Unsettled tasks"] [data-task-row="t3"] [data-settle]')).not.toBeNull();
    await user.click(guard.getByRole('button', { name: 'Settle anyway' }));
    await waitFor(() => expect(side.queryByRole('button', { name: 'Settle Doctor: add terminal line' })).toBeNull());
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
    const { user } = renderApp('#task=t3');
    await screen.findByRole('region', { name: 'Conversation' });
    await user.click(await screen.findByRole('button', { name: 'Task actions' }));
    const menu = within(await screen.findByRole('menu'));
    expect(menu.getByRole('menuitem', { name: 'Settle…' })).toBeTruthy();
    await user.click(menu.getByRole('menuitem', { name: 'Archive' }));
    const guard = within(await screen.findByRole('alertdialog', { name: 'Archive with unsaved work?' }));
    expect(screen.queryByRole('alertdialog', { name: 'Archive this task?' })).toBeNull();
    await user.click(guard.getByRole('button', { name: 'Open Changes' }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    await waitFor(() => expect(sheet.getByRole('radio', { name: /^All changes/ }).getAttribute('aria-checked')).toBe('true'));
  });
});
