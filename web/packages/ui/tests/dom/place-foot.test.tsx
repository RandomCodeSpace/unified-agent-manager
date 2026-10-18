// Where a Thread runs, under the composer (footer A, blocks). Mock p1: t1 works in its folder, t3 is finished there, t15 runs in
// its own worktree; the folder is on feat/web-project-defaults-and-sidebar-revamp.
import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api, type BranchList, type SessionSummary } from '../../src/api';
import { composer, openTask, renderApp, sidebar, type User } from './render';

afterEach(() => vi.restoreAllMocks());

const FOLDER = 'feat/web-project-defaults-and-sidebar-revamp';

async function draft(project: RegExp) {
  const r = renderApp();
  const side = await sidebar();
  await r.user.click(side.getByRole('button', { name: 'New thread' }));
  const palette = await screen.findByRole('dialog');
  await r.user.click(within(palette).getByRole('option', { name: project }));
  await screen.findByRole('textbox', { name: 'Message' });
  return r;
}

/** The strip under the composer: the element after the composer's form. */
const strip = () => screen.getByRole('button', { name: /^Branch / }).parentElement!;

async function openBranches(user: User) {
  await user.click(screen.getByRole('button', { name: /^Branch / }));
  const menu = within(await screen.findByRole('dialog', { name: /^(Switch the branch|Worktree from)$/ }));
  await waitFor(() => expect(menu.queryByText('Reading branches…')).toBeNull());
  return menu;
}

const off = (el: HTMLElement) => el.getAttribute('aria-disabled') === 'true';

/** The service's branch list with the folder's guard replaced. */
function guard(over: Partial<BranchList>) {
  const real = api.projectBranches.bind(api);
  return vi.spyOn(api, 'projectBranches').mockImplementation(async (id, signal) => ({ ...(await real(id, signal)), busy: undefined, uncommitted: 0, ...over }));
}

/** Sends the first message to a service that refuses it (the mock's t1 works in p1's folder), for the place it carries. */
async function sendRefused(user: User, create: ReturnType<typeof vi.spyOn<typeof api, 'createSession'>>, text: string) {
  create.mockRejectedValue(new Error('refused'));
  await user.type(composer(), text);
  await user.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(create).toHaveBeenCalled());
}

async function send(user: User, text: string) {
  await user.type(screen.getByRole('textbox', { name: 'Message' }), text);
  await user.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(window.location.hash).toMatch(/^#task=t\d+$/));
}

describe('a draft Thread', () => {
  test('shows only the place and the branch, and the menu names the Thread that keeps the folder on its branch', async () => {
    const { user } = await draft(/unified-agent-manager/);
    const where = within(screen.getByRole('radiogroup', { name: 'Where this Thread runs' }));
    expect(where.getByRole('radio', { name: 'Project folder' }).getAttribute('aria-checked')).toBe('true');
    expect(where.getByRole('radio', { name: 'New worktree' }).getAttribute('aria-checked')).toBe('false');
    expect(strip().textContent).toBe(`Project folderNew worktree${FOLDER}`);
    const menu = await openBranches(user);
    expect(menu.getByText('Fix re-attach redraw regression').closest('p')!.textContent).toBe('Fix re-attach redraw regression is working');
    const options = menu.getAllByRole('option');
    expect(options[0].textContent).toBe(`New branch from ${FOLDER}`);
    expect(options.every(off)).toBe(true);
    expect(menu.getByText('Local')).toBeTruthy();
    expect(menu.getByText('Remote')).toBeTruthy();
  });

  test('uncommitted files turn off only the existing branches; New branch is sent with the first message', async () => {
    guard({ uncommitted: 4 });
    const create = vi.spyOn(api, 'createSession');
    const { user } = await draft(/unified-agent-manager/);
    const menu = await openBranches(user);
    expect(menu.getByText('4 files not committed')).toBeTruthy();
    const [fresh, ...existing] = menu.getAllByRole('option');
    expect(off(fresh)).toBe(false);
    expect(existing.every(off)).toBe(true);
    await user.click(fresh);
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Switch the branch' })).toBeNull());
    expect(screen.getByRole('button', { name: 'Branch New branch' })).toBeTruthy();
    await sendRefused(user, create, 'Start the parser work');
    expect(create.mock.calls[0][0].place).toEqual({ mode: 'branch' });
  });

  test('an existing local or remote branch is switched to by the first message, and the @ picker lists it', async () => {
    guard({});
    const create = vi.spyOn(api, 'createSession');
    const files = vi.spyOn(api, 'projectFiles');
    const { user } = await draft(/unified-agent-manager/);
    let menu = await openBranches(user);
    await user.click(menu.getByRole('option', { name: /^feat\/settings-rail/ }));
    expect(screen.getByRole('button', { name: 'Branch feat/settings-rail' })).toBeTruthy();
    menu = await openBranches(user);
    expect(menu.getByRole('option', { name: /^feat\/settings-rail/ }).getAttribute('aria-selected')).toBe('true');
    await user.type(menu.getByRole('textbox', { name: 'Find a branch' }), 'release/1.4{Enter}');
    expect(screen.getByRole('button', { name: 'Branch origin/release/1.4' })).toBeTruthy();
    await user.type(composer(), '@');
    await waitFor(() => expect(files).toHaveBeenCalledWith(expect.any(String), '', 50, 'origin/release/1.4'));
    await user.keyboard('{Escape}');
    await user.clear(composer());
    // Nothing switched yet: the draft only reads.
    expect(create).not.toHaveBeenCalled();
    await sendRefused(user, create, 'Port the fix to 1.4');
    expect(create.mock.calls[0][0].place).toEqual({ mode: 'switch', branch: 'origin/release/1.4', remote: true });
  });

  test('a new worktree picks its source branch, shows no name before the first message, and its Thread shows it after', async () => {
    const create = vi.spyOn(api, 'createSession');
    const { user } = await draft(/unified-agent-manager/);
    await user.click(screen.getByRole('radio', { name: 'New worktree' }));
    await waitFor(() => expect(screen.getByRole('radio', { name: 'New worktree' }).getAttribute('aria-checked')).toBe('true'));
    const menu = await openBranches(user);
    // A source branch: no New branch row, no guard.
    expect(menu.queryByText(/^New branch/)).toBeNull();
    expect(menu.queryByText(/is working/)).toBeNull();
    await user.click(menu.getByRole('option', { name: /^origin\/main/ }));
    expect(strip().textContent).toBe('Project folderNew worktreeorigin/main');
    await send(user, 'Try the new parser');
    expect(create.mock.calls[0][0].place).toEqual({ mode: 'worktree', base: 'origin/main' });
    // Once it exists: the worktree and its branch, without the folder named for that branch.
    const wt = await screen.findByLabelText(/^Worktree, branch uam\/[0-9a-f]{8}$/);
    expect(wt.textContent).toMatch(/^Worktreeuam\/[0-9a-f]{8}$/);
  });

  test('the current branch sends no place', async () => {
    const create = vi.spyOn(api, 'createSession');
    const { user } = await draft(/notes-site/);
    await send(user, 'Add a sitemap');
    expect(create.mock.calls[0][0].place).toBeUndefined();
  });

  test('a failed branch read says why', async () => {
    vi.spyOn(api, 'projectBranches').mockRejectedValue(new Error('git is not answering'));
    const { user } = await draft(/unified-agent-manager/);
    await user.click(screen.getByRole('button', { name: /^Branch / }));
    expect((await screen.findByRole('alert')).textContent).toBe('Could not read the branches: git is not answering');
  });
});

describe('a started Thread', () => {
  test('in its own worktree: worktree and branch as plain text, the folder that repeats the branch left out, the path on hover', async () => {
    const { user } = await openTask('t15');
    const wt = screen.getByRole('button', { name: 'Worktree, branch uam/remove-unused-exports' });
    expect(wt.textContent).toBe('Worktreeuam/remove-unused-exports');
    expect(wt.parentElement!.textContent).toBe(wt.textContent);
    expect(screen.queryByRole('button', { name: /^Branch / })).toBeNull();
    await user.hover(wt);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toBe('/home/user/.uam/worktrees/unified-agent-manager/uam-remove-unused-exports'));
  });

  test('in the Project folder: the branch alone, and a switch that waits for the folder', async () => {
    const { user } = await openTask('t3');
    expect(strip().textContent).toBe(FOLDER);
    expect(screen.queryByText('Current branch')).toBeNull();
    const menu = await openBranches(user);
    expect(menu.getByText(/is working/).textContent).toBe('Fix re-attach redraw regression is working');
    expect(menu.getAllByRole('option').every(off)).toBe(true);
  });

  test('switches at once when the folder is free', async () => {
    guard({});
    const switchBranch = vi.spyOn(api, 'switchBranch').mockResolvedValue({} as SessionSummary);
    const { user } = await openTask('t3');
    let menu = await openBranches(user);
    await user.click(menu.getByRole('option', { name: /^fix\/vterm-focus-replay/ }));
    expect(switchBranch).toHaveBeenCalledWith('t3', { mode: 'switch', branch: 'fix/vterm-focus-replay' });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Switch the branch' })).toBeNull());
    menu = await openBranches(user);
    await user.click(menu.getByRole('option', { name: /^New branch/ }));
    expect(switchBranch).toHaveBeenLastCalledWith('t3', { mode: 'branch' });
  });

  test('a Thread that is working blocks its own switch', async () => {
    guard({});
    const { user } = await openTask('t1');
    const menu = await openBranches(user);
    expect(menu.getByText('This Thread is working')).toBeTruthy();
    expect(menu.getAllByRole('option').every(off)).toBe(true);
  });

  test('a refused switch says why in the menu', async () => {
    guard({});
    vi.spyOn(api, 'switchBranch').mockRejectedValue(new Error('2 files not committed'));
    const { user } = await openTask('t3');
    const menu = await openBranches(user);
    await user.click(menu.getByRole('option', { name: /^main/ }));
    expect((await menu.findByRole('alert')).textContent).toBe('2 files not committed');
  });
});
