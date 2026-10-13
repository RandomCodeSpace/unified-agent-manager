import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { renderApp, sidebar, type User } from './render';

async function addProjectDialog(user: User) {
  const side = await sidebar();
  await user.click(side.getByRole('button', { name: 'Add project' }));
  return within(await screen.findByRole('dialog', { name: 'Choose a project folder' }));
}

async function editProjectDialog(user: User, name: string) {
  const side = await sidebar();
  await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
  const picker = within(await screen.findByRole('dialog'));
  await user.click(picker.getByRole('button', { name: `Edit ${name}` }));
  return within(await screen.findByRole('dialog', { name: /Edit project/ }));
}

describe('add project', () => {
  test('a typed directory is reviewed before creation and Back preserves its name and folder', async () => {
    const { user } = renderApp();
    const requests = vi.spyOn(window, 'fetch');
    const projectWrites = () => requests.mock.calls.filter(([url, init]) => url === '/api/projects' && init?.method === 'POST');
    const dialog = await addProjectDialog(user);
    const path = await dialog.findByRole('textbox', { name: 'Folder path' });
    await user.clear(path);
    await user.type(path, '/home/user/projects/scratch');
    await user.click(dialog.getByRole('button', { name: 'Open folder path' }));
    await dialog.findByText('No folders here');
    await user.click(dialog.getByRole('button', { name: 'Continue' }));
    await screen.findByRole('dialog', { name: 'Add a project' });
    expect(projectWrites()).toHaveLength(0);
    expect(dialog.getByText('/home/user/projects/scratch')).toBeTruthy();
    await user.type(dialog.getByRole('textbox', { name: 'Project name' }), 'Scratch pad');
    await user.click(dialog.getByRole('button', { name: 'Back' }));
    await screen.findByRole('dialog', { name: 'Choose a project folder' });
    await waitFor(() => expect(dialog.getByRole('button', { name: 'Continue' })).toHaveProperty('disabled', false));
    expect(dialog.getByRole('textbox', { name: 'Folder path' })).toHaveProperty('value', '/home/user/projects/scratch');
    await user.click(dialog.getByRole('button', { name: 'Continue' }));
    expect(dialog.getByRole('textbox', { name: 'Project name' })).toHaveProperty('value', 'Scratch pad');
    expect(projectWrites()).toHaveLength(0);
    await user.click(dialog.getByRole('button', { name: 'Add project' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Add a project' })).toBeNull());
    expect(projectWrites()).toHaveLength(1);
    requests.mockRestore();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
    expect(await screen.findByRole('option', { name: /Scratch pad/ })).toBeTruthy();
  });

  test('a recent directory that already has a project selects that project after review', async () => {
    const { user } = renderApp();
    const dialog = await addProjectDialog(user);
    await user.click(dialog.getByRole('combobox', { name: 'Recent folders' }));
    await user.click(await screen.findByRole('option', { name: '/home/user/dotfiles' }));
    await waitFor(() => expect(dialog.getByRole('button', { name: 'Continue' })).toHaveProperty('disabled', false));
    expect(dialog.getByRole('textbox', { name: 'Folder path' })).toHaveProperty('value', '/home/user/dotfiles');
    await user.click(dialog.getByRole('button', { name: 'Continue' }));
    await user.click(dialog.getByRole('button', { name: 'Add project' }));
    expect(await screen.findByRole('button', { name: 'Project filter: dotfiles' })).toBeTruthy();
    expect(localStorage.getItem('uam.projectFilter')).toBe('"p2"');
  });

  test('a relative folder path is refused with the reason and cannot be selected', async () => {
    const { user } = renderApp();
    const dialog = await addProjectDialog(user);
    const path = await dialog.findByRole('textbox', { name: 'Folder path' });
    await user.clear(path);
    await user.type(path, 'relative/path');
    expect(dialog.getByRole('button', { name: 'Open folder path' })).toHaveProperty('disabled', true);
    expect(dialog.getByRole('button', { name: 'Continue' })).toHaveProperty('disabled', true);
    expect(dialog.getByRole('textbox', { name: 'Folder path', description: 'Enter an absolute folder path.' }).getAttribute('aria-invalid')).toBe('true');
    await user.click(dialog.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Choose a project folder' })).toBeNull());
  });

  test('in a folder past the listing cap, the filter asks the server and finds a folder the listing left out', async () => {
    const original = api.listDirs;
    // The cap left archive out of the projects folder's listing.
    const list = vi.spyOn(api, 'listDirs').mockImplementation(async (path, hidden, filter) => {
      const listing = await original(path, hidden, filter);
      return filter || !path?.endsWith('/projects') ? listing : { ...listing, entries: listing.entries.filter((e) => e.name !== 'archive'), truncated: true };
    });
    try {
      const { user } = renderApp();
      const dialog = await addProjectDialog(user);
      const folders = within(await dialog.findByRole('listbox', { name: 'Folders' }));
      await user.dblClick(await folders.findByRole('option', { name: /^projects/ }));
      expect(await dialog.findByText('Showing the first 1,000. Filter folders to search them all.')).toBeTruthy();
      expect(folders.queryByRole('option', { name: /^archive/ })).toBeNull();
      await user.type(dialog.getByRole('textbox', { name: 'Filter folders' }), 'arch');
      expect(await folders.findByRole('option', { name: /^archive/ })).toBeTruthy();
      expect(list).toHaveBeenLastCalledWith('/home/user/projects', false, 'arch');
    } finally {
      list.mockRestore();
    }
  });

  test('folders open immediately, filtering preserves browsing, and a selected folder supplies the default project name', async () => {
    const { user } = renderApp();
    const dialog = await addProjectDialog(user);
    const folders = within(await dialog.findByRole('listbox', { name: 'Folders' }));
    // Home first; folders sorted, dot-folders hidden.
    const projects = await folders.findByRole('option', { name: /^projects/ });
    expect(folders.getByRole('option', { name: /^dotfiles.*git/ })).toBeTruthy();
    expect(folders.queryByRole('option', { name: /^\.config/ })).toBeNull();
    await user.click(dialog.getByRole('button', { name: 'Show hidden' }));
    expect(await folders.findByRole('option', { name: /^\.config/ })).toBeTruthy();
    await user.dblClick(projects);
    expect(await folders.findByRole('option', { name: /^archive.*link/ })).toBeTruthy();
    const path = within(dialog.getByRole('navigation', { name: 'Folder path' }));
    expect(path.getByRole('button', { name: 'projects' }).getAttribute('aria-current')).toBe('location');
    await user.type(dialog.getByRole('textbox', { name: 'Filter folders' }), 'arch');
    expect(folders.queryByRole('option', { name: /^notes-site/ })).toBeNull();
    await user.click(folders.getByRole('option', { name: /^archive/ }));
    expect(folders.getByRole('option', { name: /^archive/ }).getAttribute('aria-selected')).toBe('true');
    await user.click(dialog.getByRole('button', { name: 'Continue' }));
    expect(dialog.getByText('/home/user/projects/archive')).toBeTruthy();
    const name = dialog.getByRole('textbox', { name: 'Project name' });
    // Leaving the optional name empty lets the folder's own name become the Project name.
    await user.clear(name);
    await user.click(dialog.getByRole('button', { name: 'Add project' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Add a project' })).toBeNull());
    await user.click((await sidebar()).getByRole('button', { name: 'Project filter: all projects' }));
    expect(await screen.findByRole('option', { name: /archive/ })).toBeTruthy();
  });

  test('folder creation and keyboard navigation preserve the selection and refuse unreadable folders', async () => {
    const { user } = renderApp();
    const dialog = await addProjectDialog(user);
    const list = await dialog.findByRole('listbox', { name: 'Folders' });
    const folders = within(list);
    await user.dblClick(await folders.findByRole('option', { name: /^projects/ }));
    await folders.findByRole('option', { name: /^archive/ });
    await user.click(dialog.getByRole('button', { name: 'New folder' }));
    const name = await dialog.findByRole('textbox', { name: 'New folder name' });
    await user.type(name, 'archive{Enter}');
    expect((await dialog.findByRole('alert')).textContent).toBe('A folder with this name already exists');
    await user.clear(name);
    await user.type(name, 'fresh');
    expect(dialog.getByRole('button', { name: 'Continue' })).toHaveProperty('disabled', true);
    fireEvent.keyDown(name, { key: 'Enter', ctrlKey: true });
    expect(screen.queryByRole('dialog', { name: 'Add a project' })).toBeNull();
    await user.click(dialog.getByRole('button', { name: 'Create folder' }));
    expect(await folders.findByRole('option', { name: /^fresh/, selected: true })).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Continue' }));
    expect(dialog.getByText('/home/user/projects/fresh')).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Back' }));
    const resumed = await dialog.findByRole('listbox', { name: 'Folders' });
    await waitFor(() => expect(resumed.getAttribute('aria-busy')).not.toBe('true'));
    const current = within(resumed);
    await current.findByRole('option', { name: /^archive/ });
    resumed.focus();
    await user.keyboard('{Home}');
    expect(current.getByRole('option', { name: /^archive/ }).getAttribute('aria-selected')).toBe('true');
    await user.keyboard('{ArrowDown}');
    expect(current.getByRole('option', { name: /^fresh/ }).getAttribute('aria-selected')).toBe('true');
    await user.keyboard('u');
    expect(current.getByRole('option', { name: /^unified-agent-manager/ }).getAttribute('aria-selected')).toBe('true');
    await user.keyboard('{Backspace}');
    expect(await current.findByRole('option', { name: /^projects/ })).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Up' }));
    expect(await current.findByRole('option', { name: /^user/ })).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Root' }));
    await user.click(await current.findByRole('option', { name: /^root/ }));
    await user.keyboard('{Enter}');
    expect((await dialog.findByRole('status')).textContent).toBe("You don't have access to this folder");
    expect(dialog.getByRole('button', { name: 'Continue' })).toHaveProperty('disabled', true);
    await user.keyboard('{Control>}{Enter}{/Control}');
    expect(screen.queryByRole('dialog', { name: 'Add a project' })).toBeNull();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Choose a project folder' })).toBeNull());
  });

  test('loading another folder blocks Continue and its keyboard shortcut until that listing arrives', async () => {
    const { user } = renderApp();
    const dialog = await addProjectDialog(user);
    const list = await dialog.findByRole('listbox', { name: 'Folders' });
    const projects = await within(list).findByRole('option', { name: /^projects/ });
    const fetch = window.fetch;
    let release!: () => void;
    const pending = new Promise<void>(resolve => { release = resolve; });
    const requests = vi.spyOn(window, 'fetch').mockImplementation(async (input, init) => {
      if (String(input).includes('/api/fs/dirs?path=%2Fhome%2Fuser%2Fprojects')) await pending;
      return fetch(input, init);
    });
    try {
      await user.dblClick(projects);
      await waitFor(() => expect(list.getAttribute('aria-busy')).toBe('true'));
      expect(dialog.getByRole('button', { name: 'Continue' })).toHaveProperty('disabled', true);
      fireEvent.keyDown(list, { key: 'Enter', ctrlKey: true });
      expect(screen.queryByRole('dialog', { name: 'Add a project' })).toBeNull();
      release();
      await within(list).findByRole('option', { name: /^archive/ });
      await waitFor(() => expect(dialog.getByRole('button', { name: 'Continue' })).toHaveProperty('disabled', false));
      await user.click(dialog.getByRole('button', { name: 'Continue' }));
      expect(dialog.getByText('/home/user/projects')).toBeTruthy();
    } finally {
      release();
      requests.mockRestore();
    }
  });
});

describe('edit project', () => {
  test('renaming a project updates the sidebar', async () => {
    const { user } = renderApp();
    const dialog = await editProjectDialog(user, 'notes-site');
    const input = dialog.getByRole('textbox', { name: 'Name' });
    await user.clear(input);
    await user.type(input, 'blog');
    await user.click(dialog.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Edit project/ })).toBeNull());
    const side = await sidebar();
    expect(side.getAllByRole('button', { name: /blog/ }).length).toBeGreaterThan(0);
  });

  test('a project with unarchived tasks cannot be removed', async () => {
    const { user } = renderApp();
    const dialog = await editProjectDialog(user, 'notes-site');
    await user.click(dialog.getByRole('button', { name: 'Remove project' }));
    const confirm = within(await screen.findByRole('alertdialog', { name: 'Remove notes-site?' }));
    expect(confirm.getByText(/threads are not archived\. Archive every thread before removing this project\./)).toBeTruthy();
    expect(confirm.getByRole('button', { name: 'Remove project' })).toHaveProperty('disabled', true);
  });

  test('previous sessions import one as a task and open it', async () => {
    const { user } = renderApp();
    const dialog = await editProjectDialog(user, 'dotfiles');
    await user.click(dialog.getByRole('button', { name: 'Previous sessions' }));
    const previous = within(await screen.findByRole('dialog', { name: 'Previous sessions in dotfiles' }));
    await user.click(await previous.findByRole('button', { name: 'Import Trim the zsh prompt' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Previous sessions in dotfiles' })).toBeNull());
    // Closing Edit project lands on the imported task, already open behind it.
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Trim the zsh prompt'));
    expect(await screen.findByText('Imported from the CLI history: the conversation ended here.')).toBeTruthy();
  });

  test('Import all reports the conversations that could not be imported', async () => {
    const { user } = renderApp();
    const dialog = await editProjectDialog(user, 'unified-agent-manager');
    await user.click(dialog.getByRole('button', { name: 'Previous sessions' }));
    const previous = within(await screen.findByRole('dialog', { name: 'Previous sessions in unified-agent-manager' }));
    expect(await previous.findByText('In use by another client. Close it there, then refresh.')).toBeTruthy();
    expect(previous.getByRole('button', { name: 'Import Rename the tmux package' })).toHaveProperty('disabled', true);
    await user.click(previous.getByRole('button', { name: 'Import all (5)' }));
    expect(await previous.findByText(/Importing \d of 5…/)).toBeTruthy();
    const alert = await previous.findByRole('alert', {}, { timeout: 8000 });
    expect(alert.textContent).toMatch(/^1 of 5 could not be imported\. Sketch the web attach flow: the recorded history could not be read/);
    expect(await previous.findByRole('button', { name: 'Import Rename the tmux package' })).toBeTruthy();
    expect(previous.queryByRole('button', { name: 'Import Explain the vterm replay order' })).toBeNull();
    await user.click(previous.getByRole('button', { name: 'Refresh' }));
    expect(await previous.findByRole('button', { name: 'Import Sketch the web attach flow' })).toBeTruthy();
  });
});

describe('project filter', () => {
  test('filtering to one project keeps only its tasks and is remembered', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
    const search = await screen.findByRole('combobox', { name: 'Search projects' });
    await user.type(search, 'notes');
    await user.keyboard('{Enter}');
    expect(await side.findByRole('button', { name: 'Project filter: notes-site' })).toBeTruthy();
    expect(side.queryByRole('button', { name: /Fix re-attach redraw regression/ })).toBeNull();
    expect(side.getByRole('button', { name: /Accessibility pass on the post template/ })).toBeTruthy();
    expect(localStorage.getItem('uam.projectFilter')).toBe('"p3"');
  });

  test('the task search narrows the list', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.type(side.getByRole('searchbox', { name: 'Search threads' }), 'zsh');
    await waitFor(() => expect(side.queryByRole('button', { name: /Fix re-attach redraw regression/ })).toBeNull());
    expect(side.getByRole('button', { name: /Tidy zsh startup/ })).toBeTruthy();
  });
});
