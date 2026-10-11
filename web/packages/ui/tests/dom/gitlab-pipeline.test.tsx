import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { api, createApiClient, type GitLabPipelineState } from '../../src/api';
import { ApiContext } from '../../src/ApiContext';
import { GitLabPipelinePanel } from '../../src/components/GitLabPipeline';
import { TaskActionsContext, useTaskActions } from '../../src/components/taskActions';
import { renderApp } from './render';

const sha = 'c81f4a2c81f4a2c81f4a2c81f4a2c81f4a2c81f4a2';
const state: GitLabPipelineState = {
  supported: true, auto_investigate: false, can_investigate: true,
  commit: { sha, branch: 'feat/pipeline', task_id: 't3', task_name: 'Fix pipeline', created_at: '2026-10-11T15:00:00Z' },
  pipeline: { id: 1842, status: 'failed', sha, ref: 'feat/pipeline', web_url: 'https://gitlab.example.test/platform/pipelines/1842' },
  merge_requests: [{ iid: 42, title: 'Track UAM commits', state: 'opened', draft: true, web_url: 'https://gitlab.example.test/platform/merge_requests/42' }],
  jobs: [{ id: 22, name: 'unit-test', stage: 'test', status: 'failed', allow_failure: false, web_url: 'https://gitlab.example.test/platform/jobs/22' }],
  checked_at: '2026-10-11T15:00:00Z',
};

beforeEach(() => vi.spyOn(api, 'supports').mockImplementation((feature) => feature !== 'gitlab-reviews-v1'));
afterEach(() => vi.restoreAllMocks());

function fixture(value: GitLabPipelineState = state) {
  const read = vi.spyOn(api, 'gitlabPipeline').mockResolvedValue(value);
  const refresh = vi.spyOn(api, 'refreshGitlabPipeline').mockResolvedValue(value);
  const investigate = vi.spyOn(api, 'investigateGitlabPipeline').mockResolvedValue({ task_id: 'investigation' });
  const select = vi.fn();
  function View() {
    const actions = useTaskActions();
    return <TaskActionsContext.Provider value={{ ...actions, select }}><GitLabPipelinePanel sessionId="t3" refreshKey={0} /></TaskActionsContext.Provider>;
  }
  return { ...render(<View />), read, refresh, investigate, select, user: userEvent.setup() };
}

test('the Task GitLab panel shows the exact commit, visible job rows and merge request details', async () => {
  vi.spyOn(api, 'gitlabPipeline').mockResolvedValue(state);
  const { user } = renderApp('?gitidle#task=t3');
  await user.click(await screen.findByRole('button', { name: 'GitLab pipelines and merge requests' }));
  const panel = within(await screen.findByRole('dialog', { name: 'GitLab' }));
  expect(await panel.findByText(sha.slice(0, 8))).toBeTruthy();
  expect(panel.getByText('feat/pipeline')).toBeTruthy();
  expect(panel.getByRole('heading', { name: 'Pipeline' })).toBeTruthy();
  expect(panel.getByRole('heading', { name: 'Jobs' })).toBeTruthy();
  expect(panel.getByRole('heading', { name: 'Merge requests' })).toBeTruthy();
  expect(panel.getByText('unit-test')).toBeTruthy();
  expect(panel.getByRole('link', { name: /Track UAM commits/ })).toBeTruthy();
  expect(panel.getByText('Open', { exact: true })).toBeTruthy();
  expect(panel.getByText('Draft', { exact: true })).toBeTruthy();
  expect(panel.getByRole('button', { name: 'Investigate' })).toBeTruthy();
});

test('Investigate sends only the displayed commit and pipeline, then opens the ordinary task', async () => {
  const { user, investigate, select } = fixture();
  await user.click(await screen.findByRole('button', { name: 'Investigate' }));
  await waitFor(() => expect(investigate).toHaveBeenCalledWith('t3', sha, 1842));
  await waitFor(() => expect(select).toHaveBeenCalledWith('investigation'));
  expect(await screen.findByRole('button', { name: 'Open investigation' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Investigate' })).toBeNull();
});

test.each([
  { ...state, commit: undefined, pipeline: undefined },
  { ...state, pipeline: { ...state.pipeline!, sha: 'unrelated' } },
  { ...state, pipeline: { ...state.pipeline!, ref: 'other-branch' } },
  { ...state, pipeline: { ...state.pipeline!, status: 'success' } },
  { ...state, can_investigate: false, reason: 'The Utility model is unavailable.' },
  { ...state, auto_investigate: true },
])('does not offer investigation outside the eligible manual failure', async (value) => {
  const { read } = fixture(value);
  await waitFor(() => expect(read).toHaveBeenCalled());
  await waitFor(() => expect(screen.queryByLabelText('Loading pipeline…')).toBeNull());
  expect(screen.queryByRole('button', { name: 'Investigate' })).toBeNull();
  if (value.reason) expect(screen.getByText(value.reason)).toBeTruthy();
});

test('existing and automatic investigations reopen their task without another creation request', async () => {
  const { user, investigate, select } = fixture({ ...state, auto_investigate: true, investigation_task_id: 'existing' });
  await user.click(await screen.findByRole('button', { name: 'Open investigation' }));
  expect(select).toHaveBeenCalledWith('existing');
  expect(investigate).not.toHaveBeenCalled();
});

test('a failed request shows the recovery error and refresh reads the live endpoint', async () => {
  const { user, investigate, refresh } = fixture();
  investigate.mockRejectedValueOnce(new Error('The branch changed.'));
  await user.click(await screen.findByRole('button', { name: 'Investigate' }));
  expect((await screen.findByRole('alert')).textContent).toContain('The branch changed.');
  expect((screen.getByRole('button', { name: 'Investigate' }) as HTMLButtonElement).disabled).toBe(true);
  await user.click(screen.getByRole('button', { name: 'Refresh' }));
  await waitFor(() => expect(refresh).toHaveBeenCalledWith('t3', expect.any(AbortSignal)));
  await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
});

test('job logs load on demand with their exact commit and pipeline, as plain text', async () => {
  const trace = vi.spyOn(api, 'gitlabJobTrace').mockResolvedValue({ text: '<script>private()</script>\nexpected true', truncated: true });
  const { user } = fixture();
  expect(await screen.findByText('1 job · 1 failed')).toBeTruthy();
  expect(screen.getByText('unit-test')).toBeTruthy();
  expect(trace).not.toHaveBeenCalled();
  await user.click(screen.getByText('unit-test'));
  expect((await screen.findByLabelText('unit-test log')).textContent).toContain('<script>private()</script>');
  expect(trace).toHaveBeenCalledWith('t3', 22, sha, 1842, expect.any(AbortSignal));
  expect(screen.getByText(/Showing part of the log/)).toBeTruthy();
});

test('an older connected server explains unavailable monitoring without a request', () => {
  const client = createApiClient({ id: 'remote', instance_id: 'instance', generation: 1, capabilities: [] } as Parameters<typeof createApiClient>[0]);
  const read = vi.spyOn(client, 'gitlabPipeline');
  render(<ApiContext.Provider value={client}><GitLabPipelinePanel sessionId="t3" refreshKey={0} /></ApiContext.Provider>);
  expect(screen.getByText('This UAM instance does not support GitLab pipeline monitoring.')).toBeTruthy();
  expect(read).not.toHaveBeenCalled();
});

test('merge requests remain visible while waiting for a pipeline', async () => {
  fixture({ ...state, pipeline: undefined, jobs: [], can_investigate: false });
  const merges = within(await screen.findByRole('region', { name: 'Merge requests' }));
  expect(merges.getByRole('link', { name: 'Track UAM commits' })).toBeTruthy();
  expect(merges.getByText('Draft')).toBeTruthy();
  expect(screen.getByText(/Waiting for a pipeline for this commit/)).toBeTruthy();
});

test('the merge request section has an honest empty state', async () => {
  fixture({ ...state, merge_requests: [] });
  const merges = within(await screen.findByRole('region', { name: 'Merge requests' }));
  expect(merges.getByRole('heading', { name: 'Merge requests' })).toBeTruthy();
  expect(merges.getByText('No merge request is associated with this commit and branch.')).toBeTruthy();
});

test.each([
  [{ ...state, merge_requests: [], checked_at: undefined }, 'Merge requests have not been checked yet. Refresh to check.'],
  [{ ...state, merge_requests: [], error: 'GitLab unavailable' }, 'Merge request status could not be checked.'],
] satisfies [GitLabPipelineState, string][])('an unchecked or failed merge request lookup is not called empty', async (value, message) => {
  fixture(value);
  expect(await screen.findByText(message)).toBeTruthy();
});

test('a repository without GitLab shows its recovery message in the opened panel', async () => {
  fixture({ supported: false, auto_investigate: false, can_investigate: false, message: 'Sign in with glab on this server.' });
  expect(await screen.findByText('Sign in with glab on this server.')).toBeTruthy();
  expect(screen.getByRole('region', { name: 'Merge requests' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Investigate' })).toBeNull();
});

test('the investigation task links to its source without offering to reopen itself', async () => {
  const { user, select } = fixture({ ...state, commit: { ...state.commit!, task_id: 'source-task' }, investigation_task_id: 't3' });
  expect(await screen.findByText('This is the investigation task.')).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Open investigation' })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Fix pipeline' }));
  expect(select).toHaveBeenCalledWith('source-task');
});

test('empty and invalid GitLab URLs remain plain text', async () => {
  fixture({ ...state, pipeline: { ...state.pipeline!, web_url: '' }, merge_requests: [{ ...state.merge_requests![0], web_url: 'javascript:alert(1)' }] });
  expect(await screen.findByText('#1842')).toBeTruthy();
  expect(screen.getByText('Track UAM commits')).toBeTruthy();
  expect(screen.queryByRole('link', { name: /Open pipeline/ })).toBeNull();
  expect(screen.queryByRole('link', { name: 'Track UAM commits' })).toBeNull();
});

test('switching task discards an earlier pending pipeline response', async () => {
  let resolve!: (value: GitLabPipelineState) => void;
  vi.spyOn(api, 'gitlabPipeline').mockImplementationOnce(() => new Promise((done) => { resolve = done; })).mockResolvedValueOnce({ supported: true, auto_investigate: false, can_investigate: false, message: 'No UAM commit for the second task.' });
  const { rerender } = render(<GitLabPipelinePanel sessionId="t3" refreshKey={0} />);
  rerender(<GitLabPipelinePanel sessionId="other" refreshKey={0} />);
  expect(await screen.findByText('No UAM commit for the second task.')).toBeTruthy();
  resolve(state);
  await waitFor(() => expect(screen.queryByText(sha.slice(0, 8))).toBeNull());
});
