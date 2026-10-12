import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { api, DEFAULT_MR_PROMPT, type GitLabStatus } from '../../src/api';
import { ApiContext } from '../../src/ApiContext';
import { GitLabSettings } from '../../src/components/GitLabSettings';
import { renderApp } from './render';

const status: GitLabStatus = { installed: true, checked_at: '2026-10-11T15:00:00Z', hostname: 'gitlab.example.test', authenticated: true, username: 'alex', name: 'Alex Morgan', token: { scopes: ['api', 'read_repository'], expires_at: '2027-01-31' } };

test('GitLab is discoverable in Settings and only checks when its page opens', async () => {
  const check = vi.spyOn(api, 'gitlabStatus').mockResolvedValue(status);
  try {
    const { user } = renderApp('#settings');
    const rail = within(await screen.findByRole('navigation', { name: 'Settings sections' }));
    expect(check).not.toHaveBeenCalled();
    await user.click(rail.getByRole('button', { name: 'GitLab' }));
    expect(await screen.findByText('@alex')).toBeTruthy();
    expect(screen.getByText('gitlab.example.test')).toBeTruthy();
    expect(screen.getByText('api', { selector: 'code' })).toBeTruthy();
    expect(screen.getByText('2027-01-31')).toBeTruthy();
    const initialChecks = check.mock.calls.length;
    await user.click(screen.getByRole('button', { name: 'Check access' }));
    await waitFor(() => expect(check).toHaveBeenCalledTimes(initialChecks + 1));
  } finally { check.mockRestore(); }
});

test('one global switch saves both modes without a project selector', async () => {
  const save = vi.spyOn(api, 'updateWebSettings');
  try {
    const { user } = renderApp('#settings');
    const rail = within(await screen.findByRole('navigation', { name: 'Settings sections' }));
    await user.click(rail.getByRole('button', { name: 'GitLab' }));
    const toggle = await screen.findByRole('switch', { name: 'Automatic investigation' });
    expect(toggle.getAttribute('aria-checked')).toBe('false');
    expect(screen.queryByRole('combobox', { name: 'Check access for' })).toBeNull();
    await user.click(toggle);
    await waitFor(() => expect(save).toHaveBeenLastCalledWith({ gitlab_auto_investigate: true }));
    await waitFor(() => expect(toggle.getAttribute('aria-checked')).toBe('true'));
    await waitFor(() => expect(toggle.hasAttribute('data-disabled')).toBe(false));
    await user.click(toggle);
    await waitFor(() => expect(save).toHaveBeenLastCalledWith({ gitlab_auto_investigate: false }));
    await waitFor(() => expect(toggle.getAttribute('aria-checked')).toBe('false'));
  } finally { save.mockRestore(); }
});

test('the merge request description prompt shows the default until one is saved, and resets to it', async () => {
  const save = vi.spyOn(api, 'updateWebSettings');
  try {
    const { user } = renderApp('#settings');
    const rail = within(await screen.findByRole('navigation', { name: 'Settings sections' }));
    await user.click(rail.getByRole('button', { name: 'GitLab' }));
    const prompt = await screen.findByRole<HTMLTextAreaElement>('textbox', { name: 'Merge request description' });
    expect(prompt.value).toBe('');
    expect(prompt.placeholder).toBe(DEFAULT_MR_PROMPT);
    const saveButton = screen.getByRole('button', { name: 'Save' });
    const reset = screen.getByRole('button', { name: 'Reset to default' });
    expect(saveButton.hasAttribute('disabled') || saveButton.getAttribute('aria-disabled') === 'true').toBe(true);
    await user.type(prompt, '  Be brief.  ');
    await user.click(saveButton);
    await waitFor(() => expect(save).toHaveBeenLastCalledWith({ gitlab_mr_prompt: 'Be brief.' }));
    await user.click(reset);
    await waitFor(() => expect(save).toHaveBeenLastCalledWith({ gitlab_mr_prompt: '' }));
    await waitFor(() => expect(prompt.value).toBe(''));
    expect(save).toHaveBeenCalledTimes(2);
  } finally { save.mockRestore(); }
});

test('a rejected automatic setting restores its saved value and explains the failure', async () => {
  const save = vi.spyOn(api, 'updateWebSettings').mockRejectedValueOnce(new Error('The setting could not be persisted.'));
  try {
    const { user } = renderApp('#settings');
    const rail = within(await screen.findByRole('navigation', { name: 'Settings sections' }));
    await user.click(rail.getByRole('button', { name: 'GitLab' }));
    const toggle = await screen.findByRole('switch', { name: 'Automatic investigation' });
    await user.click(toggle);
    expect((await screen.findByRole('alert')).textContent).toContain('The setting could not be persisted.');
    expect(toggle.getAttribute('aria-checked')).toBe('false');
  } finally { save.mockRestore(); }
});

test('unavailable scopes and an unreported role are not presented as permissions', async () => {
  const check = vi.spyOn(api, 'gitlabStatus').mockResolvedValue({ ...status, token: undefined, project: { path: 'group/platform', readable: true } });
  try {
    render(<GitLabSettings />);
    await screen.findByText('@alex');
    expect(screen.getAllByText('Not available')).toHaveLength(2);
    expect(screen.getByText('Readable · role not reported')).toBeTruthy();
    expect(screen.getByText(/GitLab did not return token details/)).toBeTruthy();
    expect(screen.queryByText('Maintainer')).toBeNull();
  } finally { check.mockRestore(); }
});

test('changing the UAM instance hides the previous account until that instance answers', async () => {
  let resolve!: (value: GitLabStatus) => void;
  const first = { ...api, gitlabStatus: vi.fn().mockResolvedValue(status) };
  const second = { ...api, gitlabStatus: vi.fn(() => new Promise<GitLabStatus>((done) => { resolve = done; })) };
  const { rerender } = render(<ApiContext.Provider value={first}><GitLabSettings /></ApiContext.Provider>);
  await screen.findByText('@alex');
  rerender(<ApiContext.Provider value={second}><GitLabSettings /></ApiContext.Provider>);
  expect(screen.queryByText('@alex')).toBeNull();
  resolve({ ...status, username: 'sam', name: 'Sam', hostname: 'other.example.test' });
  expect(await screen.findByText('@sam')).toBeTruthy();
  expect(screen.queryByText('gitlab.example.test')).toBeNull();
});

test.each([
  { installed: false, label: 'glab not installed', message: 'Install glab on the UAM server.' },
  { installed: true, label: 'Not verified', message: 'Could not verify authentication.' },
])('shows $label with the recovery message', async ({ installed, label, message }) => {
  const check = vi.spyOn(api, 'gitlabStatus').mockResolvedValue({ installed, authenticated: false, checked_at: status.checked_at, message });
  try {
    render(<GitLabSettings />);
    expect(await screen.findByText(label)).toBeTruthy();
    expect(screen.getByText(message)).toBeTruthy();
    expect(screen.queryByText('Token scopes')).toBeNull();
  } finally { check.mockRestore(); }
});

test('a failed check can be retried without retaining a successful status', async () => {
  const check = vi.spyOn(api, 'gitlabStatus').mockResolvedValueOnce(status).mockRejectedValueOnce(new Error('Server is unavailable')).mockResolvedValueOnce(status);
  try {
    const user = userEvent.setup();
    render(<GitLabSettings />);
    await screen.findByText('@alex');
    await user.click(screen.getByRole('button', { name: 'Check access' }));
    expect((await screen.findByRole('alert')).textContent).toContain('Server is unavailable');
    expect(screen.queryByText('@alex')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Check access' }));
    expect(await screen.findByText('@alex')).toBeTruthy();
  } finally { check.mockRestore(); }
});
