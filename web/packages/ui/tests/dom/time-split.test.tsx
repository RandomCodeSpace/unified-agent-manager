import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type SessionDetail, type SessionSummary, type TaskUsageMetrics, type TimeSplit } from '../../src/api';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { ComposerTools } from '../../src/components/ComposerTools';
import { TimeSplitSummary } from '../../src/components/TimeSplit';

afterEach(() => { vi.restoreAllMocks(); });
const session = { id: 'task-1', conversation_id: 'conv-1', provider: 'copilot', model: 'auto', state: 'idle', open: true, usage: { ai_units: 9 }, context: { used: 50, limit: 200 }, capabilities: { usage: true, usage_metrics: true } } as unknown as SessionDetail;
const app = { settings: {}, usage: null } as unknown as AppContextValue;
const metrics: TaskUsageMetrics = { started_at: '2026-10-09T00:00:00Z', user_requests: 1, premium_request_cost: 0, api_duration_ms: 700, last_input: 1, last_output: 1, code_changes: { files: 0, added: 0, removed: 0 }, token_details: [], models: [], agents: [] };
const split: TimeSplit = {
  model_ms: 252_000, model_calls: 47, local_ms: 411_000, local_runs: 112, waiting_ms: 178_000, waits: 3,
  tools: [{ name: 'bash', ms: 340_000, runs: 38 }, { name: 'edit', ms: 38_000, runs: 41 }, { name: 'view', ms: 21_000, runs: 27 }, { name: 'uam_receipts', ms: 12_000, runs: 6 }, { name: 'glob', ms: 4_000, runs: 3 }, { name: 'web_fetch', ms: 2_000, runs: 1 }],
};

function summary(current: SessionSummary) {
  return <ApiContext.Provider value={api}><TimeSplitSummary session={current} /></ApiContext.Provider>;
}

test('the Usage tab leads with the time split, its counts and each tool, the rest folded', async () => {
  const read = vi.spyOn(api, 'taskTimeSplit').mockResolvedValue(split);
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  render(<ApiContext.Provider value={api}><AppContext.Provider value={app}><ComposerTools session={session} onAgent={() => {}} /></AppContext.Provider></ApiContext.Provider>);
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  await userEvent.click(await screen.findByRole('tab', { name: 'Usage' }));
  const time = await screen.findByRole('region', { name: 'Time in this Thread' });
  await within(time).findByText('4m 12s · 47 calls');
  expect(read).toHaveBeenCalledWith('task-1', expect.any(AbortSignal));
  expect(time.textContent).toContain('38% model');
  expect(time.textContent).toContain('62% local');
  expect(time.textContent).toContain('waiting on you, not counted');
  expect(within(time).getByText('6m 51s · 112 runs')).toBeTruthy();
  expect(within(time).getByText('2m 58s · 3 requests')).toBeTruthy();
  // It comes before the existing report and the native metrics.
  const regions = screen.getAllByRole('region').map((r) => r.getAttribute('aria-label'));
  expect(regions.indexOf('Time in this Thread')).toBeLessThan(regions.indexOf('Recorded Thread report'));
  const tools = screen.getByRole('region', { name: 'Local time by tool' });
  expect(within(tools).getByText('bash')).toBeTruthy();
  expect(within(tools).getByText('5m 40s')).toBeTruthy();
  expect(within(tools).getByText('38 runs')).toBeTruthy();
  expect(within(tools).getByText('1 more tool')).toBeTruthy();
  expect(within(tools).getByText('web_fetch').closest('details')).toBeTruthy();
  expect(within(tools).getByText('glob').closest('details')).toBeNull();
});

test('a journal without model call records still shows local time and says model time is not recorded', async () => {
  vi.spyOn(api, 'taskTimeSplit').mockResolvedValue({ ...split, model_ms: null, model_calls: 0, waiting_ms: 0, waits: 0, tools: [] });
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  render(<ApiContext.Provider value={api}><AppContext.Provider value={app}><ComposerTools session={session} onAgent={() => {}} /></AppContext.Provider></ApiContext.Provider>);
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  await userEvent.click(await screen.findByRole('tab', { name: 'Usage' }));
  const time = await screen.findByRole('region', { name: 'Time in this Thread' });
  await within(time).findByText("Model time not recorded for this Thread's CLI version.");
  expect(within(time).getByText('Not recorded')).toBeTruthy();
  expect(within(time).getByText('6m 51s · 112 runs')).toBeTruthy();
  expect(time.textContent).not.toContain('% model');
  expect(screen.queryByRole('region', { name: 'Local time by tool' })).toBeNull();
});

test('the composer summary shows the shares, both times once idle, the whole sentence in its name, and reads again on a state change', async () => {
  const read = vi.spyOn(api, 'taskTimeSplit').mockResolvedValue(split);
  const view = render(summary({ ...session, state: 'working' }));
  const button = await screen.findByRole('button', { name: /^Time in this Thread/ });
  expect(button.textContent).toBe('38% model · 62% local');
  expect(button.getAttribute('aria-label')).toBe('Time in this Thread: 4m 12s in the model (47 calls) and 6m 51s running local tools and scripts (112 runs). 2m 58s waiting on you is not counted.');
  view.rerender(summary({ ...session, state: 'idle' }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  expect(button.textContent).toBe('38% model · 62% local· 4m 12s / 6m 51s');
});

test('a connected instance without the split shows neither the section nor the summary, and is never asked', async () => {
  const read = vi.fn().mockResolvedValue(split);
  const client = { ...api, supports: (capability: string) => capability !== 'time-split-v1', taskTimeSplit: read, taskUsageMetrics: vi.fn().mockResolvedValue(metrics) } as typeof api;
  render(<ApiContext.Provider value={client}><AppContext.Provider value={app}><ComposerTools session={session} onAgent={() => {}} /><TimeSplitSummary session={session} /></AppContext.Provider></ApiContext.Provider>);
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  await userEvent.click(await screen.findByRole('tab', { name: 'Usage' }));
  await screen.findByRole('region', { name: 'Recorded Thread report' });
  expect(screen.queryByRole('region', { name: 'Time in this Thread' })).toBeNull();
  expect(screen.queryByText('Reading the session journal…')).toBeNull();
  expect(screen.queryByRole('button', { name: /^Time in this Thread/ })).toBeNull();
  expect(read).not.toHaveBeenCalled();
});

test('the composer summary is absent until something is recorded', async () => {
  const read = vi.spyOn(api, 'taskTimeSplit').mockResolvedValue({ model_ms: null, model_calls: 0, local_ms: 0, local_runs: 0, waiting_ms: 0, waits: 0, tools: [] });
  const view = render(summary(session));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  expect(view.container.textContent).toBe('');
  view.rerender(summary({ ...session, id: '' }));
  expect(read).toHaveBeenCalledTimes(1);
});
