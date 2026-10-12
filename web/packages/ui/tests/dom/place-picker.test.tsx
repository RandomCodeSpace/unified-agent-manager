// A draft Thread chooses where it runs at the composer's foot (mock p1: t1 is working in its folder).
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api, type Project } from '../../src/api';
import { PlaceChip, projectSlug } from '../../src/components/PlacePicker';
import { renderApp, sidebar, type User } from './render';

afterEach(() => vi.restoreAllMocks());

async function draft(project: RegExp) {
  const r = renderApp();
  const side = await sidebar();
  await r.user.click(side.getByRole('button', { name: 'New thread' }));
  const palette = await screen.findByRole('dialog');
  await r.user.click(within(palette).getByRole('option', { name: project }));
  await screen.findByRole('textbox', { name: 'Message' });
  return r;
}

async function openPicker(user: User) {
  await user.click(await screen.findByRole('button', { name: /^Where this Thread runs:/ }));
  return within(await screen.findByRole('dialog', { name: 'Where this Thread runs' }));
}

async function send(user: User, text: string) {
  await user.type(screen.getByRole('textbox', { name: 'Message' }), text);
  await user.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(window.location.hash).toMatch(/^#task=t\d+$/));
}

describe('the place picker', () => {
  test('New branch is locked while a Thread works in the folder, and the reader names it', async () => {
    const { user } = await draft(/unified-agent-manager/);
    expect(screen.getByRole('button', { name: /^Where this Thread runs:/ }).textContent).toContain('current branch');
    const reader = await openPicker(user);
    expect(reader.getByRole('radio', { name: 'Current branch' }).getAttribute('aria-checked')).toBe('true');
    expect(reader.getByRole('radio', { name: 'New branch' }).hasAttribute('data-disabled')).toBe(true);
    expect(reader.getByText('Fix re-attach redraw regression').closest('p')!.textContent).toBe('New branch is off while Fix re-attach redraw regression works in this folder.');
  });

  test('a new worktree from a searched remote base is sent with the first message', async () => {
    const create = vi.spyOn(api, 'createSession');
    const { user } = await draft(/unified-agent-manager/);
    const reader = await openPicker(user);
    await user.click(reader.getByRole('radio', { name: 'New worktree' }));
    const list = within(reader.getByRole('listbox', { name: 'Base branch' }));
    await list.findByRole('option', { name: /origin\/release\/1\.4/ });
    expect(reader.getByRole('region', { name: 'Creates' }).textContent).toContain('~/.uam/worktrees/unified-agent-manager/uam-<8 hex>');
    await user.type(reader.getByRole('combobox', { name: 'Find a base branch' }), 'release/1.4{Enter}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Where this Thread runs' })).toBeNull());
    expect(screen.getByRole('button', { name: /^Where this Thread runs:/ }).textContent).toBe('New worktreefromorigin/release/1.4');
    await send(user, 'Port the fix to 1.4');
    expect(create.mock.calls[0][0].place).toEqual({ mode: 'worktree', base: 'origin/release/1.4' });
    // The new Thread's chip is read-only and says it runs in a worktree.
    expect((await screen.findByRole('button', { name: /^Branch uam\/[0-9a-f]{8}, worktree$/ }))).toBeTruthy();
  });

  test('in worktree mode the @ picker lists the base branch', async () => {
    const files = vi.spyOn(api, 'projectFiles');
    const { user } = await draft(/unified-agent-manager/);
    const reader = await openPicker(user);
    await user.click(reader.getByRole('radio', { name: 'New worktree' }));
    await user.click(await reader.findByRole('option', { name: /origin\/main/ }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Where this Thread runs' })).toBeNull());
    await user.type(screen.getByRole('textbox', { name: 'Message' }), '@');
    await waitFor(() => expect(files).toHaveBeenCalledWith(expect.any(String), '', 50, 'origin/main'));
  });

  test('a new branch starts from the folder branch: no base list, no base sent', async () => {
    const onPlace = vi.fn();
    const project = { id: 'p1', name: 'Unified Agent Manager!', dir: '/p', branch: 'main' } as Project;
    const user = userEvent.setup();
    const { rerender } = render(<PlaceChip project={project} place={{ mode: 'current' }} onPlace={onPlace} />);
    await user.click(screen.getByRole('button', { name: 'Where this Thread runs: main, current branch' }));
    let reader = within(await screen.findByRole('dialog', { name: 'Where this Thread runs' }));
    await user.click(reader.getByRole('radio', { name: 'New branch' }));
    expect(onPlace).toHaveBeenLastCalledWith({ mode: 'branch' });
    rerender(<PlaceChip project={project} place={{ mode: 'branch' }} onPlace={onPlace} />);
    reader = within(screen.getByRole('dialog', { name: 'Where this Thread runs' }));
    expect(reader.queryByRole('listbox')).toBeNull();
    expect(reader.getByRole('region', { name: 'From' }).textContent).toContain('main');
    expect(reader.getByRole('region', { name: 'Creates' }).textContent).not.toContain('Folder');
    expect(screen.getByRole('button', { name: 'Where this Thread runs: new branch from main' })).toBeTruthy();
    await user.click(reader.getByRole('radio', { name: 'New worktree' }));
    expect(onPlace).toHaveBeenLastCalledWith({ mode: 'worktree', base: 'main' });
    expect(projectSlug(project.name)).toBe('unified-agent-manager');
  });

  test('the current branch sends no place', async () => {
    const create = vi.spyOn(api, 'createSession');
    const { user } = await draft(/notes-site/);
    await send(user, 'Add a sitemap');
    expect(create.mock.calls[0][0].place).toBeUndefined();
  });

  test('a refused place keeps the draft and says which Thread is in the way', async () => {
    vi.spyOn(api, 'createSession').mockRejectedValue(new Error('“Fix re-attach redraw regression” is working in this folder'));
    const { user } = await draft(/unified-agent-manager/);
    const reader = await openPicker(user);
    await user.click(reader.getByRole('radio', { name: 'New worktree' }));
    await user.keyboard('{Escape}');
    await user.type(screen.getByRole('textbox', { name: 'Message' }), 'Port the fix');
    await user.click(screen.getByRole('button', { name: 'Send' }));
    expect(await screen.findByText(/Could not start the thread: “Fix re-attach redraw regression” is working in this folder/)).toBeTruthy();
    expect((screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement).value).toBe('Port the fix');
  });

  test('a failed branch read says why and keeps the modes', async () => {
    vi.spyOn(api, 'projectBranches').mockRejectedValue(new Error('git is not answering'));
    const { user } = await draft(/unified-agent-manager/);
    const reader = await openPicker(user);
    await user.click(reader.getByRole('radio', { name: 'New worktree' }));
    expect((await reader.findByRole('alert')).textContent).toBe('Could not read the branches: git is not answering');
  });
});
