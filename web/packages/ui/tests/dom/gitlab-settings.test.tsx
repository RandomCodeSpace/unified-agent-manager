import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { api, type GitLabStatus, type Project } from '../../src/api';
import { ApiContext } from '../../src/ApiContext';
import { GitLabSettings } from '../../src/components/GitLabSettings';
import { renderApp } from './render';

const status: GitLabStatus = { installed: true, checked_at: '2026-10-11T15:00:00Z', hostname: 'gitlab.example.test', authenticated: true, username: 'alex', name: 'Alex Morgan', token: { scopes: ['api', 'read_repository'], expires_at: '2027-01-31' } };
const projects = [{ id: 'project', name: 'Platform', dir: '/workspace/platform' }] as Project[];

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

test('switching projects hides the previous access while the new check runs', async () => {
  let resolve!: (value: GitLabStatus) => void;
  const check = vi.spyOn(api, 'gitlabStatus').mockResolvedValueOnce(status).mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
  try {
    const user = userEvent.setup();
    render(<GitLabSettings projects={projects} />);
    await screen.findByText('@alex');
    await user.click(screen.getByRole('combobox', { name: 'Check access for' }));
    await user.click(await screen.findByRole('option', { name: 'Platform' }));
    expect(screen.queryByText('@alex')).toBeNull();
    expect(check).toHaveBeenLastCalledWith('project');
    resolve({ ...status, hostname: 'work.example.test', project: { path: 'group/platform', readable: true, access_level: 40 } });
    expect(await screen.findByText('Maintainer')).toBeTruthy();
    expect(screen.getByText('work.example.test')).toBeTruthy();
  } finally { check.mockRestore(); }
});

test('unavailable scopes and an unreported role are not presented as permissions', async () => {
  const check = vi.spyOn(api, 'gitlabStatus').mockResolvedValue({ ...status, token: undefined, project: { path: 'group/platform', readable: true } });
  try {
    render(<GitLabSettings projects={projects} />);
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
  const { rerender } = render(<ApiContext.Provider value={first}><GitLabSettings projects={[]} /></ApiContext.Provider>);
  await screen.findByText('@alex');
  rerender(<ApiContext.Provider value={second}><GitLabSettings projects={[]} /></ApiContext.Provider>);
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
    render(<GitLabSettings projects={[]} />);
    expect(await screen.findByText(label)).toBeTruthy();
    expect(screen.getByText(message)).toBeTruthy();
    expect(screen.queryByText('Token scopes')).toBeNull();
  } finally { check.mockRestore(); }
});

test('a failed check can be retried without retaining a successful status', async () => {
  const check = vi.spyOn(api, 'gitlabStatus').mockResolvedValueOnce(status).mockRejectedValueOnce(new Error('Server is unavailable')).mockResolvedValueOnce(status);
  try {
    const user = userEvent.setup();
    render(<GitLabSettings projects={[]} />);
    await screen.findByText('@alex');
    await user.click(screen.getByRole('button', { name: 'Check access' }));
    expect((await screen.findByRole('alert')).textContent).toContain('Server is unavailable');
    expect(screen.queryByText('@alex')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Check access' }));
    expect(await screen.findByText('@alex')).toBeTruthy();
  } finally { check.mockRestore(); }
});
