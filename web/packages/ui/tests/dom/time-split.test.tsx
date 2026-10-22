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

/** A tile's whole text: its label, figure and sub line. */
const tile = (label: string) => screen.getByText(label, { selector: 'p' }).parentElement!.textContent;

test('the Usage tab shows the split as the model and local tiles, the waiting in the section line, and each tool folded', async () => {
  const read = vi.spyOn(api, 'taskTimeSplit').mockResolvedValue(split);
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  render(<ApiContext.Provider value={api}><AppContext.Provider value={app}><ComposerTools session={session} onAgent={() => {}} /></AppContext.Provider></ApiContext.Provider>);
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  await userEvent.click(await screen.findByRole('tab', { name: 'Usage' }));
  const time = await screen.findByRole('region', { name: 'This Thread' });
  await waitFor(() => expect(tile('Model time')).toBe('Model time4m 12s47 calls · 38%'));
  expect(read).toHaveBeenCalledWith('task-1', expect.any(AbortSignal));
  expect(tile('Local time')).toBe('Local time6m 51s112 runs · 62%');
  expect(time.textContent).toContain('2m 58s waiting on you (3 requests), not counted');
  const tools = screen.getByText('Local time by tool').closest('details')!;
  expect(tools.open).toBe(false);
  expect(tools.querySelector('summary')!.textContent).toBe('Local time by tool6 tools');
  expect(within(tools).getByText('bash')).toBeTruthy();
  expect(within(tools).getByText('5m 40s')).toBeTruthy();
  expect(within(tools).getByText('38 runs')).toBeTruthy();
  expect(within(tools).getByText('1 more tool')).toBeTruthy();
  expect(within(tools).getByText('web_fetch').closest('details')).not.toBe(tools);
  expect(within(tools).getByText('glob').closest('details')).toBe(tools);
});

test('a journal without model call records still shows local time and says model time is not recorded', async () => {
  vi.spyOn(api, 'taskTimeSplit').mockResolvedValue({ ...split, model_ms: null, model_calls: 0, waiting_ms: 0, waits: 0, tools: [] });
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  render(<ApiContext.Provider value={api}><AppContext.Provider value={app}><ComposerTools session={session} onAgent={() => {}} /></AppContext.Provider></ApiContext.Provider>);
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  await userEvent.click(await screen.findByRole('tab', { name: 'Usage' }));
  const time = await screen.findByRole('region', { name: 'This Thread' });
  await waitFor(() => expect(tile('Model time')).toBe('Model timeNot recorded0 calls'));
  expect(tile('Local time')).toBe('Local time6m 51s112 runs');
  expect(time.textContent).not.toContain('%');
  expect(time.textContent).not.toContain('waiting on you');
  expect(screen.queryByText('Local time by tool')).toBeNull();
});

test('the composer summary shows the shares, both times once idle, the whole sentence in its name, and reads again on a state change', async () => {
  const read = vi.spyOn(api, 'taskTimeSplit').mockResolvedValue(split);
  const view = render(summary({ ...session, state: 'working' }));
  const button = await screen.findByRole('button', { name: /^Time in this Thread/ });
  // Two blocks, model then local: their shares while the Thread works, each named in words.
  const cells = () => [...button.querySelectorAll('[title]')].map((c) => [c.getAttribute('title'), c.querySelector(':scope > span[aria-hidden]')!.textContent, c.querySelector('svg')!.getAttribute('class')!.match(/lucide-(cpu|terminal)\b/)![1]]);
  expect(cells()).toEqual([['38% model', '38%', 'cpu'], ['62% local', '62%', 'terminal']]);
  expect(button.getAttribute('aria-label')).toBe('Time in this Thread: 4m 12s in the model (47 calls, compaction included) and 6m 51s running local tools and scripts (112 runs). 2m 58s waiting on you is not counted.');
  view.rerender(summary({ ...session, state: 'idle' }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  // Once idle, the times take their place, labelled by glyph and name: no bare "4m 12s / 6m 51s".
  expect(cells()).toEqual([['4m 12s model', '4m 12s', 'cpu'], ['6m 51s local', '6m 51s', 'terminal']]);
  expect(button.textContent).not.toContain('/');
});

test('without recorded model time the summary is the local block alone', async () => {
  vi.spyOn(api, 'taskTimeSplit').mockResolvedValue({ ...split, model_ms: null });
  render(summary({ ...session, state: 'working' }));
  const button = await screen.findByRole('button', { name: /^Time in this Thread/ });
  expect([...button.querySelectorAll('[title]')].map((c) => c.getAttribute('title'))).toEqual(['6m 51s local']);
});

test('a connected instance without the split shows neither the section nor the summary, and is never asked', async () => {
  const read = vi.fn().mockResolvedValue(split);
  const client = { ...api, supports: (capability: string) => capability !== 'time-split-v1', taskTimeSplit: read, taskUsageMetrics: vi.fn().mockResolvedValue(metrics) } as typeof api;
  render(<ApiContext.Provider value={client}><AppContext.Provider value={app}><ComposerTools session={session} onAgent={() => {}} /><TimeSplitSummary session={session} /></AppContext.Provider></ApiContext.Provider>);
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  await userEvent.click(await screen.findByRole('tab', { name: 'Usage' }));
  await screen.findByRole('region', { name: 'This Thread' });
  expect(tile('Model time')).toBe('Model timeNot available');
  expect(tile('Local time')).toBe('Local timeNot available');
  expect(screen.queryByText('Local time by tool')).toBeNull();
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
