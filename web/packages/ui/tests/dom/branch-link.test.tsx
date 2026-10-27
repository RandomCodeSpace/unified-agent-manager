import { screen, waitFor, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { openTask, sidebar, type User } from './render';

const GITHUB_PATH = 'M9 19c';
const GITLAB_PATH = 'M21 14l';

async function editProject(user: User, name: string) {
  const side = await sidebar();
  await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
  await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: `Edit ${name}` }));
  return within(await screen.findByRole('dialog', { name: /Edit project/ }));
}

const mark = (link: HTMLElement) => link.querySelector('path')?.getAttribute('d') ?? '';

test("the header links a pushed branch on GitHub with GitHub's mark", async () => {
  await openTask('t3');
  const link = screen.getByRole('link', { name: 'Open feat/web-project-defaults-and-sidebar-revamp on the web' });
  expect(link.getAttribute('href')).toBe('https://github.com/RandomCodeSpace/unified-agent-manager/tree/feat/web-project-defaults-and-sidebar-revamp');
  expect(link.getAttribute('target')).toBe('_blank');
  expect(link.getAttribute('rel')).toBe('noopener noreferrer');
  expect(mark(link)).toMatch(new RegExp(`^${GITHUB_PATH}`));
});

test('an unpushed branch links the default branch and says so', async () => {
  await openTask('t15');
  const link = screen.getByRole('link', { name: 'uam/remove-unused-exports is not pushed: open main' });
  expect(link.getAttribute('href')).toBe('https://github.com/RandomCodeSpace/unified-agent-manager/tree/main');
});

test('a Project without a branch link has no header link', async () => {
  await openTask('t6');
  expect(screen.queryByRole('link', { name: /on the web|is not pushed/ })).toBeNull();
});

test('Edit project detects, refuses and saves the branch link, and the header follows it', async () => {
  const { user } = await openTask('t8');
  const header = screen.getByRole('link', { name: 'Open main on the web' });
  expect(header.getAttribute('href')).toBe('https://gitlab.example.com/web/notes-site/-/tree/main');
  expect(mark(header)).toMatch(new RegExp(`^${GITLAB_PATH}`));

  const requests = vi.spyOn(window, 'fetch');
  let dialog = await editProject(user, 'notes-site');
  const field = dialog.getByRole('textbox', { name: 'Branch link' });
  expect(field).toHaveProperty('value', 'https://gitlab.example.com/web/notes-site/-/tree/{branch}');
  expect(dialog.getByText('An https address with {branch} where the branch name goes.')).toBeTruthy();
  await user.clear(field);
  await user.type(field, 'https://code.example.com/notes');
  await user.click(dialog.getByRole('button', { name: 'Save' }));
  expect((await dialog.findByRole('alert')).textContent).toBe('the branch link must contain {branch} exactly once, where the branch name goes');
  await user.click(dialog.getByRole('button', { name: 'Detect from git remote' }));
  await waitFor(() => expect(field).toHaveProperty('value', 'https://github.com/example/notes-site/tree/{branch}'));
  await user.clear(field);
  await user.type(field, 'https://code.example.com/notes/tree/{{branch}');
  await user.click(dialog.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: /Edit project/ })).toBeNull());
  const patch = requests.mock.calls.filter(([, init]) => init?.method === 'PATCH').at(-1);
  expect(JSON.parse(String(patch?.[1]?.body))).toEqual({ name: 'notes-site', branch_link: 'https://code.example.com/notes/tree/{branch}' });
  const plain = await screen.findByRole('link', { name: 'Open main on the web' });
  await waitFor(() => expect(plain.getAttribute('href')).toBe('https://code.example.com/notes/tree/main'));
  expect(plain.querySelector('svg')?.classList.contains('lucide-external-link')).toBe(true);

  // A rename leaves the link out; clearing it turns the header link off.
  dialog = await editProject(user, 'notes-site');
  await user.click(dialog.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: /Edit project/ })).toBeNull());
  expect(JSON.parse(String(requests.mock.calls.filter(([, init]) => init?.method === 'PATCH').at(-1)?.[1]?.body))).toEqual({ name: 'notes-site' });
  dialog = await editProject(user, 'notes-site');
  await user.clear(dialog.getByRole('textbox', { name: 'Branch link' }));
  await user.click(dialog.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(screen.queryByRole('link', { name: /on the web/ })).toBeNull());
  requests.mockRestore();
});

test('Detect from git remote says why when there is no remote', async () => {
  const { user } = await openTask('t6');
  const dialog = await editProject(user, 'dotfiles');
  await user.click(dialog.getByRole('button', { name: 'Detect from git remote' }));
  expect((await dialog.findByRole('alert')).textContent).toBe('git has no origin remote here that uam can read');
  expect(dialog.getByRole('textbox', { name: 'Branch link' })).toHaveProperty('value', '');
});
