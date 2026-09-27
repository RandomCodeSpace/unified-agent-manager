import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp, sidebar, type User } from './render';

async function addProjectDialog(user: User) {
  const side = await sidebar();
  await user.click(side.getByRole('button', { name: 'Add project' }));
  return within(await screen.findByRole('dialog', { name: 'Add a project' }));
}

async function editProjectDialog(user: User, name: string) {
  const side = await sidebar();
  await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
  const picker = within(await screen.findByRole('dialog'));
  await user.click(picker.getByRole('button', { name: `Edit ${name}` }));
  return within(await screen.findByRole('dialog', { name: /Edit project/ }));
}

describe('add project', () => {
  test('a typed directory becomes a project with a badge', async () => {
    const { user } = renderApp();
    const dialog = await addProjectDialog(user);
    const add = dialog.getByRole('button', { name: 'Add project' });
    expect(add).toHaveProperty('disabled', true);
    await user.type(dialog.getByRole('textbox', { name: 'Directory on the host' }), '/home/user/projects/scratch');
    await user.type(dialog.getByRole('textbox', { name: 'Name' }), 'Scratch pad');
    expect(dialog.getByText(/Badge assigned when added/)).toBeTruthy();
    await user.click(add);
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Add a project' })).toBeNull());
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
    expect(await screen.findByRole('option', { name: /Scratch pad/ })).toBeTruthy();
  });

  test('a directory that already has a project selects that project', async () => {
    const { user } = renderApp();
    const dialog = await addProjectDialog(user);
    await user.type(dialog.getByRole('textbox', { name: 'Directory on the host' }), '/home/user/dotfiles');
    await user.click(dialog.getByRole('button', { name: 'Add project' }));
    expect(await screen.findByRole('button', { name: 'Project filter: dotfiles' })).toBeTruthy();
    expect(localStorage.getItem('uam.projectFilter')).toBe('"p2"');
  });

  test('a relative directory is refused with the reason', async () => {
    const { user } = renderApp();
    const dialog = await addProjectDialog(user);
    await user.type(dialog.getByRole('textbox', { name: 'Directory on the host' }), 'relative/path');
    await user.click(dialog.getByRole('button', { name: 'Add project' }));
    expect((await dialog.findByRole('alert')).textContent).toMatch(/absolute path/);
    await user.click(dialog.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Add a project' })).toBeNull());
  });

  test('Browse walks the folders and Use this folder fills the directory', async () => {
    const { user } = renderApp();
    const dialog = await addProjectDialog(user);
    await user.click(dialog.getByRole('button', { name: 'Browse' }));
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
    await user.click(folders.getByRole('option', { name: /^notes-site/ }));
    expect(folders.getByRole('option', { name: /^notes-site/ }).getAttribute('aria-selected')).toBe('true');
    await user.click(dialog.getByRole('button', { name: 'Use this folder' }));
    await waitFor(() => expect(dialog.getByRole('textbox', { name: 'Directory on the host' })).toHaveProperty('value', '/home/user/projects/notes-site'));
  });

  test('the folder picker goes up, makes a folder, and moves by keyboard', async () => {
    const { user } = renderApp();
    const dialog = await addProjectDialog(user);
    await user.type(dialog.getByRole('textbox', { name: 'Directory on the host' }), '/home/user/projects');
    await user.click(dialog.getByRole('button', { name: 'Browse' }));
    const list = await dialog.findByRole('listbox', { name: 'Folders' });
    const folders = within(list);
    await folders.findByRole('option', { name: /^archive/ });
    await user.click(dialog.getByRole('button', { name: 'New folder' }));
    const name = await dialog.findByRole('textbox', { name: 'New folder name' });
    await user.type(name, 'archive{Enter}');
    expect((await dialog.findByRole('alert')).textContent).toBe('A folder with this name already exists');
    await user.clear(name);
    await user.type(name, 'fresh');
    await user.click(dialog.getByRole('button', { name: 'Create folder' }));
    expect(await folders.findByRole('option', { name: /^fresh/, selected: true })).toBeTruthy();
    list.focus();
    await user.keyboard('{Home}');
    expect(folders.getByRole('option', { name: /^archive/ }).getAttribute('aria-selected')).toBe('true');
    await user.keyboard('{ArrowDown}');
    expect(folders.getByRole('option', { name: /^fresh/ }).getAttribute('aria-selected')).toBe('true');
    await user.keyboard('u');
    expect(folders.getByRole('option', { name: /^unified-agent-manager/ }).getAttribute('aria-selected')).toBe('true');
    await user.keyboard('{Backspace}');
    expect(await folders.findByRole('option', { name: /^projects/ })).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Up' }));
    expect(await folders.findByRole('option', { name: /^user/ })).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Root' }));
    await user.click(await folders.findByRole('option', { name: /^root/ }));
    await user.keyboard('{Enter}');
    expect((await dialog.findByRole('status')).textContent).toBe("You don't have access to this folder");
    await user.keyboard('{Escape}');
    await waitFor(() => expect(dialog.queryByRole('listbox', { name: 'Folders' })).toBeNull());
    expect(screen.getByRole('dialog', { name: 'Add a project' })).toBeTruthy();
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
    expect(confirm.getByText(/tasks are not archived\. Archive every task before removing this project\./)).toBeTruthy();
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
    await user.type(side.getByRole('searchbox', { name: 'Search tasks' }), 'zsh');
    await waitFor(() => expect(side.queryByRole('button', { name: /Fix re-attach redraw regression/ })).toBeNull());
    expect(side.getByRole('button', { name: /Tidy zsh startup/ })).toBeTruthy();
  });
});
