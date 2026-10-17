import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type SessionDetail, type TaskUsageMetrics, type UsageMetricModel } from '../../src/api';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { ComposerTools } from '../../src/components/ComposerTools';

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });
const session = { id: 'task-1', conversation_id: 'conv-1', provider: 'copilot', model: 'auto', open: true, usage: { ai_units: 9 }, context: { used: 50, limit: 200 }, capabilities: { usage: true, usage_metrics: true } } as unknown as SessionDetail;
const app = { settings: {}, usage: null } as unknown as AppContextValue;
const model: UsageMetricModel = { model: 'model-a', requests: 3, premium_request_cost: .5, input: 100, output: 20, cache_read: 80, cache_write: 10, token_details: [{ type: 'future', tokens: 11 }] };
const metrics: TaskUsageMetrics = { started_at: '2026-10-09T00:00:00Z', current_model: 'native-current', user_requests: 1, premium_request_cost: .5, api_duration_ms: 700, ai_units: 2, last_input: 100, last_output: 20, code_changes: { files: 1, added: 5, removed: 2 }, token_details: [], models: [model], agents: [{ id: 'main', ai_units: 2, api_duration_ms: 700, models: [model] }] };

/** Opens Tools on its Usage tab; the panel keeps the last tab, so a reopen lands there too. */
async function openUsage() {
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  const tab = await screen.findByRole('tab', { name: 'Usage' });
  if (tab.getAttribute('aria-selected') !== 'true') await userEvent.click(tab);
}

function draw(selected = session, client = api) {
  const tree = (current: SessionDetail, owner = client) => <ApiContext.Provider value={owner}><AppContext.Provider value={app}><ComposerTools session={current} onAgent={() => {}} /></AppContext.Provider></ApiContext.Provider>;
  const view = render(tree(selected));
  return { ...view, select: (current: SessionDetail) => view.rerender(tree(current)), selectOwner: (owner: typeof api) => view.rerender(tree(selected, owner)) };
}

test('native usage reads on open, on pushed usage changes and on Refresh, preserving recorded and overlapping counter scopes', async () => {
  const read = vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  const view = draw();
  expect(read).not.toHaveBeenCalled();
  await openUsage();
  expect(await screen.findByRole('dialog', { name: 'Tools' })).toBeTruthy();
  await screen.findByText('Current model: native-current');
  expect(screen.getByText('9.0 AI credits')).toBeTruthy();
  expect(screen.getAllByText('2.0 AI credits').length).toBe(2);
  expect(screen.getByText('User-initiated requests')).toBeTruthy();
  // The cost unit is the premium row's tip; the counter caveats live in docs/web.md.
  expect(screen.getByRole('button', { name: 'About Premium request cost', description: 'A multiplier, not USD.' })).toBeTruthy();
  expect(screen.queryByText(/Do not add the two views together|not added to input or output here/)).toBeNull();
  expect(screen.getAllByText('Not reported').length).toBe(4); // model AI credits/reasoning remain unknown in both projections
  expect(read).toHaveBeenCalledTimes(1);
  expect(read.mock.calls[0][0]).toBe('task-1');
  // A pushed usage report reads the snapshot again while the reader stays open; nothing polls.
  view.select({ ...session, usage: { ai_units: 10 }, context: { used: 60, limit: 200 } });
  expect(screen.getByText('10 AI credits')).toBeTruthy();
  await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  expect(screen.getByText('Current model: native-current')).toBeTruthy();
  vi.useFakeTimers();
  act(() => vi.advanceTimersByTime(60_000));
  expect(read).toHaveBeenCalledTimes(2);
  vi.useRealTimers();
  await userEvent.click(screen.getByText('Main agent'));
  expect(read).toHaveBeenCalledTimes(2);
  await userEvent.click(screen.getByRole('button', { name: 'Refresh', exact: true }));
  await screen.findByText('Current model: native-current');
  expect(read).toHaveBeenCalledTimes(3);
  await userEvent.click(screen.getByRole('button', { name: 'Close', exact: true }));
  expect(screen.queryByText('Current model: native-current')).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: /^Tools/ })));
  await openUsage();
  await screen.findByText('Current model: native-current');
  expect(read).toHaveBeenCalledTimes(4);
});

test('a subagent row is named by what it was asked to do, with its kind, not its ID', async () => {
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue({ ...metrics, agents: [{ id: 'agent-1', name: 'general-purpose', ai_units: 1, api_duration_ms: 5, models: [] }, { id: 'agent-2', name: 'explore', ai_units: 1, api_duration_ms: 5, models: [] }] });
  draw({ ...session, subagents: [{ id: 'agent-1', name: 'general-purpose', description: 'Explore the auth flow', status: 'completed' }] } as unknown as SessionDetail);
  await openUsage();
  const named = await screen.findByText('Explore the auth flow');
  expect(named.closest('summary')!.textContent).toBe('Explore the auth flow(general-purpose)');
  // Without a recorded subagent the reported name and ID stay the only identity.
  expect(screen.getByText('explore').closest('summary')!.textContent).toBe('explore(agent-2)');
});

test('a draft Thread is not closed: its native metrics wait for the Thread to start', async () => {
  const read = vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  draw({ ...session, id: '', conversation_id: '', open: false, usage: undefined });
  await openUsage();
  expect(await screen.findByText('Native usage metrics are reported once the Thread starts.')).toBeTruthy();
  expect(screen.queryByText(/closed or inactive/)).toBeNull();
  expect(read).not.toHaveBeenCalled();
});

test('closed and inactive Tasks never read or resume, while unsupported services keep the existing quick report', async () => {
  const read = vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  const view = draw({ ...session, open: false });
  await openUsage();
  expect(await screen.findByText('Native usage metrics are unavailable for a closed or inactive Thread.')).toBeTruthy();
  expect(screen.getByText('9.0 AI credits')).toBeTruthy();
  expect(read).not.toHaveBeenCalled();
  view.select({ ...session, stage: 'settled' });
  await openUsage();
  await screen.findByText('Native usage metrics are unavailable for a closed or inactive Thread.');
  expect(read).not.toHaveBeenCalled();
  view.select({ ...session, capabilities: { ...session.capabilities, usage_metrics: undefined } });
  // Without native metrics the Usage tab keeps the recorded report alone.
  await openUsage();
  expect(await screen.findByText('9.0 AI credits')).toBeTruthy();
  expect(screen.queryByText(/Native usage metrics|Loading native/)).toBeNull();
  expect(read).not.toHaveBeenCalled();
});

test('native optional counters remain unknown and failures never become zero totals', async () => {
  const read = vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue({ ...metrics, ai_units: undefined, premium_request_cost: .000001, agents: [], truncated: true });
  draw({ ...session, usage: undefined });
  await openUsage();
  await screen.findByText('Native AI credits');
  expect(screen.getByText('AI credits not reported yet')).toBeTruthy();
  expect(screen.queryByText('0 AI credits')).toBeNull();
  expect(screen.getByText('0.000001')).toBeTruthy();
  expect(screen.getByText(/Some breakdown rows are not shown/)).toBeTruthy();
  await userEvent.click(screen.getByRole('button', { name: 'Close', exact: true }));
  read.mockRejectedValue(new Error('Native metrics unavailable'));
  await openUsage();
  expect(await screen.findByText('Native metrics unavailable')).toBeTruthy();
  expect(screen.queryByText('Native AI credits')).toBeNull();
});

test('Task selection and close abort native reads and ignore late snapshots', async () => {
  let deliver!: (data: TaskUsageMetrics) => void;
  const read = vi.spyOn(api, 'taskUsageMetrics').mockImplementationOnce(() => new Promise(resolve => { deliver = resolve; })).mockResolvedValue(metrics);
  const view = draw();
  await openUsage();
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  const signal = read.mock.calls[0][1]!;
  view.select({ ...session, model: 'next' });
  expect(signal.aborted).toBe(true);
  await act(async () => deliver({ ...metrics, current_model: 'obsolete-snapshot' }));
  expect(screen.queryByText('Current model: obsolete-snapshot')).toBeNull();
  expect(screen.queryByRole('dialog', { name: 'Tools' })).toBeNull();
  await openUsage();
  await screen.findByText('Current model: native-current');
  const secondSignal = read.mock.calls[1][1]!;
  await userEvent.click(screen.getByRole('button', { name: 'Close', exact: true }));
  expect(secondSignal.aborted).toBe(true);
});

test('phone reader uses the owning API and returns focus after Escape', async () => {
  vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: true } as MediaQueryList);
  const local = vi.spyOn(api, 'taskUsageMetrics');
  const remote = vi.fn(async () => metrics);
  draw(session, { ...api, taskUsageMetrics: remote });
  await openUsage();
  await screen.findByRole('dialog', { name: 'Tools' });
  await screen.findByText('Current model: native-current');
  expect(remote).toHaveBeenCalledTimes(1);
  expect(local).not.toHaveBeenCalled();
  await userEvent.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Tools' })).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: /^Tools/ })));
});

test('changing API owner disposes the reader before a new native read', async () => {
  const local = vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  const remote = vi.fn(async () => ({ ...metrics, current_model: 'remote-model' }));
  const view = draw();
  await openUsage();
  await screen.findByText('Current model: native-current');
  const signal = local.mock.calls[0][1]!;
  view.selectOwner({ ...api, taskUsageMetrics: remote });
  expect(signal.aborted).toBe(true);
  expect(screen.queryByRole('dialog', { name: 'Tools' })).toBeNull();
  await openUsage();
  await screen.findByText('Current model: remote-model');
  expect(remote).toHaveBeenCalledTimes(1);
  expect(local).toHaveBeenCalledTimes(1);
});


test('an active wire Task with omitted stage reads native metrics', async () => {
  const read = vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  expect(Object.hasOwn(session, 'stage')).toBe(false);
  draw();
  await openUsage();
  await screen.findByText('Current model: native-current');
  expect(read).toHaveBeenCalledTimes(1);
});

test('oversized connected errors are bounded and closing disposes the reader error', async () => {
  const message = 'Connected failure: ' + 'x'.repeat(2_000_000);
  const read = vi.spyOn(api, 'taskUsageMetrics').mockRejectedValueOnce(new Error(message)).mockResolvedValue(metrics);
  draw({ ...session, stage: 'active' }); // independently exercises the error bound before the stage correction
  await openUsage();
  await waitFor(() => expect(screen.getByRole('status').textContent?.startsWith('Connected failure: ')).toBe(true));
  expect(screen.getByRole('status').textContent?.length).toBe(512);
  expect(read).toHaveBeenCalledTimes(1);
  const signal = read.mock.calls[0][1]!;
  await userEvent.click(screen.getByRole('button', { name: 'Close', exact: true }));
  expect(signal.aborted).toBe(true);
  expect(screen.queryByRole('status')).toBeNull();
  await openUsage();
  await screen.findByText('Current model: native-current');
  expect(screen.queryByRole('status')).toBeNull();
});

test('a running Thread takes a budget in AI credits and quota percent, shows what is spent, and can drop it', async () => {
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  const settings = vi.spyOn(api, 'settings').mockResolvedValue(session);
  const view = draw({ ...session, state: 'working' } as SessionDetail);
  await openUsage();
  const setBudget = await screen.findByRole('button', { name: 'Set budget' }) as HTMLButtonElement;
  expect(setBudget.disabled).toBe(true);
  await userEvent.type(screen.getByLabelText(/Stop after/), '2.5');
  await userEvent.type(screen.getByLabelText(/Stop at/), '80');
  await userEvent.click(setBudget);
  expect(settings).toHaveBeenCalledWith('task-1', { budget: { credits: 2.5, quota_percent: 80 } });

  view.select({ ...session, state: 'cancelled', budget: { credits: 2.5, spent: 2.6, quota_percent: 80, quota_used: 41, reached: true } } as SessionDetail);
  expect(screen.getByText('2.6 of 2.5 AI credits')).toBeTruthy();
  expect(screen.getByText('41% · limit 80%')).toBeTruthy();
  expect(screen.getByText(/Budget reached: uam stopped this Thread/)).toBeTruthy();
  await userEvent.click(screen.getByRole('button', { name: 'Remove' }));
  expect(settings).toHaveBeenLastCalledWith('task-1', { budget: { credits: 0, quota_percent: 0 } });
});
