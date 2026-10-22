import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type SessionDetail, type TaskUsageMetrics, type TimeSplit, type UsageMetricModel } from '../../src/api';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { ComposerTools } from '../../src/components/ComposerTools';

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });
const session = { id: 'task-1', conversation_id: 'conv-1', provider: 'copilot', model: 'auto', open: true, usage: { ai_units: 9 }, context: { used: 50, limit: 200 }, capabilities: { usage: true, usage_metrics: true } } as unknown as SessionDetail;
const app = { settings: {}, usage: null } as unknown as AppContextValue;
const model: UsageMetricModel = { model: 'model-a', requests: 3, premium_request_cost: .5, input: 100, output: 20, cache_read: 80, cache_write: 10, token_details: [{ type: 'future', tokens: 11 }] };
const metrics: TaskUsageMetrics = { started_at: '2026-10-09T00:00:00Z', current_model: 'native-current', user_requests: 1, premium_request_cost: .5, api_duration_ms: 700, ai_units: 2, last_input: 100, last_output: 20, code_changes: { files: 1, added: 5, removed: 2 }, token_details: [], models: [model], agents: [{ id: 'main', ai_units: 2, api_duration_ms: 700, models: [model] }] };
const split: TimeSplit = { model_ms: 252_000, model_calls: 47, local_ms: 411_000, local_runs: 112, waiting_ms: 178_000, waits: 3, tools: [{ name: 'bash', ms: 340_000, runs: 38 }, { name: 'edit', ms: 38_000, runs: 41 }] };

/** Opens Tools on its Usage tab; the panel keeps the last tab, so a reopen lands there too. */
async function openUsage() {
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  const tab = await screen.findByRole('tab', { name: 'Usage' });
  if (tab.getAttribute('aria-selected') !== 'true') await userEvent.click(tab);
}

/** A tile's whole text: its label, figure and sub line. */
const tile = (label: string) => screen.getByText(label, { selector: 'p' }).parentElement!.textContent;

function draw(selected = session, client = api) {
  const tree = (current: SessionDetail, owner = client) => <ApiContext.Provider value={owner}><AppContext.Provider value={app}><ComposerTools session={current} onAgent={() => {}} /></AppContext.Provider></ApiContext.Provider>;
  const view = render(tree(selected));
  return { ...view, select: (current: SessionDetail) => view.rerender(tree(current)), selectOwner: (owner: typeof api) => view.rerender(tree(selected, owner)) };
}

test('native usage reads on open, on pushed usage changes and on Refresh, preserving recorded and overlapping counter scopes', async () => {
  const read = vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  vi.spyOn(api, 'taskTimeSplit').mockResolvedValue(split);
  const view = draw();
  expect(read).not.toHaveBeenCalled();
  await openUsage();
  expect(await screen.findByRole('dialog', { name: 'Tools' })).toBeTruthy();
  await screen.findByText('Current model: native-current');
  // The recorded figure is the tile; the native one stays apart in the caption, never added to it.
  expect(tile('AI credits')).toBe('AI credits9.01 request');
  expect(screen.getByText(/^Native AI credits 2\.0 · Premium request cost 0\.5 · Model API time 700 ms$/)).toBeTruthy();
  expect(screen.getAllByText('2.0').length).toBe(1); // the agent's row; the model's AI credits are not reported
  await userEvent.click(screen.getByText('By model'));
  // The cost unit is the premium column's tip; the counter caveats live in docs/web.md.
  expect(screen.getByRole('button', { name: 'About Premium request cost', description: 'A multiplier, not USD.' })).toBeTruthy();
  expect(screen.queryByText(/Do not add the two views together|not added to input or output here/)).toBeNull();
  expect(screen.getAllByText('Not reported').length).toBe(2); // the model's AI credits and its reasoning remain unknown
  expect(read).toHaveBeenCalledTimes(1);
  expect(read.mock.calls[0][0]).toBe('task-1');
  // A pushed usage report reads the snapshot again while the reader stays open; nothing polls.
  view.select({ ...session, usage: { ai_units: 10 }, context: { used: 60, limit: 200 } });
  expect(tile('AI credits')).toBe('AI credits101 request');
  await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  expect(screen.getByText('Current model: native-current')).toBeTruthy();
  vi.useFakeTimers();
  act(() => vi.advanceTimersByTime(60_000));
  expect(read).toHaveBeenCalledTimes(2);
  vi.useRealTimers();
  await userEvent.click(screen.getByText('By agent'));
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

test('the tab leads with the budget, then six figures for this Thread, then four folded tables', async () => {
  const rich: UsageMetricModel = { model: 'model-b', requests: 40, premium_request_cost: 1.5, ai_units: 7.5, input: 1_180_000, output: 41_900, cache_read: 900_000, cache_write: 20_000, reasoning: 12_000, cache_expires_at: '2026-10-22T10:00:00Z', token_details: [{ type: 'image', tokens: 4_321 }] };
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue({ ...metrics, user_requests: 12, token_details: [{ type: 'prompt', tokens: 1_234_567 }], code_changes: { files: 4, added: 120, removed: 33 }, models: [model, rich], agents: [{ id: 'main', ai_units: 2, api_duration_ms: 58_200, models: [model] }, { id: 'agent-1', name: 'explore', ai_units: 7.5, api_duration_ms: 5_000, models: [rich] }] });
  vi.spyOn(api, 'taskTimeSplit').mockResolvedValue(split);
  draw({ ...session, state: 'working', budget: { credits: 20, spent: 12.4, quota_percent: 80, quota_used: 41 } } as SessionDetail);
  await openUsage();
  await screen.findByText('Current model: native-current');
  const panel = screen.getByRole('tabpanel');
  expect([...panel.children].map((el) => el.getAttribute('aria-label'))).toEqual(['Budget', 'This Thread', null]);
  // The budget is a meter with its line, and the form waits behind Change.
  const budget = within(screen.getByRole('region', { name: 'Budget' }));
  expect(budget.getByText('stops at 20').parentElement!.textContent).toBe('12.4 of 20 AI credits since set · 62%stops at 20');
  expect(budget.getByText('stops at 80%').parentElement!.textContent).toBe('Account quota 41% usedstops at 80%');
  expect(budget.queryByLabelText(/Stop after/)).toBeNull();
  await userEvent.click(budget.getByRole('button', { name: 'Change' }));
  expect((budget.getByLabelText(/Stop after/) as HTMLInputElement).value).toBe('20');
  expect(budget.getByRole('button', { name: 'Set budget' })).toBeTruthy();
  // Six tiles, in order, with compact token counts and the code delta in two tones.
  const tiles = [...panel.querySelectorAll('.grid-cols-3 > div')].map((el) => el.textContent);
  expect(tiles).toEqual(['AI credits9.012 requests', 'Model time4m 12s47 calls · 38%', 'Local time6m 51s112 runs · 62%', 'Tokens in1.18M900K from cache', 'Tokens out41.9K12K reasoning', 'Code4 files+120 −33']);
  expect(screen.getByText('+120').className).toContain('text-success');
  expect(screen.getByText('−33').className).toContain('text-error');
  expect(screen.getByRole('region', { name: 'This Thread' }).textContent).toContain('2m 58s waiting on you (3 requests), not counted');
  // Four native details, closed, each with its table.
  const folds = [...panel.querySelectorAll(':scope > div > details')] as HTMLDetailsElement[];
  expect(folds.map((d) => [d.querySelector('summary')!.textContent, d.open])).toEqual([['By model2 models', false], ['By agent2 agents', false], ['Local time by tool2 tools', false], ['Tokensby model and type', false]]);
  const rows = (d: HTMLElement) => [...d.querySelectorAll('tr')].map((tr) => [...tr.children].map((c) => c.textContent));
  expect(rows(folds[0])).toEqual([['Model', 'Requests', 'PremiumA multiplier, not USD.', 'AI credits', 'In', 'Out'], ['model-a', '3', '0.5', 'Not reported', '100', '20'], ['model-b', '40', '1.5', '7.5', '1.18M', '41.9K']]);
  expect(folds[0].textContent).toContain('model-b: last reported prompt cache expiry');
  expect(rows(folds[1])).toEqual([['Agent', 'AI credits', 'Model time', 'Model'], ['Main agent', '2.0', '58s', 'model-a'], ['explore(agent-1)', '7.5', '5s', 'model-b']]);
  expect(folds[2].textContent).toContain('bash');
  expect(rows(folds[3])).toEqual([['Model', 'In', 'Out', 'Cache read', 'Cache write', 'Reasoning'], ['model-a', '100', '20', '80', '10', 'Not reported'], ['model-b', '1,180,000', '41,900', '900,000', '20,000', '12,000']]);
  expect([...folds[3].querySelectorAll('dl > div')].map((el) => el.textContent)).toEqual(['prompt1,234,567', 'model-a · future11', 'model-b · image4,321', 'Latest main input tokens100', 'Latest main output tokens20']);
});

test('a subagent row is named by what it was asked to do, with its kind, not its ID', async () => {
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue({ ...metrics, agents: [{ id: 'agent-1', name: 'general-purpose', ai_units: 1, api_duration_ms: 5, models: [] }, { id: 'agent-2', name: 'explore', ai_units: 1, api_duration_ms: 5, models: [] }] });
  draw({ ...session, subagents: [{ id: 'agent-1', name: 'general-purpose', description: 'Explore the auth flow', status: 'completed' }] } as unknown as SessionDetail);
  await openUsage();
  const named = await screen.findByText('Explore the auth flow');
  expect(named.closest('td')!.textContent).toBe('Explore the auth flow(general-purpose)');
  // Without a recorded subagent the reported name and ID stay the only identity.
  expect(screen.getByText('explore').closest('td')!.textContent).toBe('explore(agent-2)');
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
  vi.spyOn(api, 'taskTimeSplit').mockResolvedValue(split);
  const view = draw({ ...session, open: false });
  await openUsage();
  expect(await screen.findByText('Native usage metrics are unavailable for a closed or inactive Thread.')).toBeTruthy();
  // The grid never collapses: the tiles that need the native read say so, the others keep their figures.
  expect(screen.getAllByText('Not available').length).toBe(3);
  expect(tile('AI credits')).toBe('AI credits9.0');
  expect(tile('Local time')).toBe('Local time6m 51s112 runs · 62%');
  expect(screen.queryByText('By model')).toBeNull();
  expect(read).not.toHaveBeenCalled();
  view.select({ ...session, stage: 'settled' });
  await openUsage();
  await screen.findByText('Native usage metrics are unavailable for a closed or inactive Thread.');
  expect(read).not.toHaveBeenCalled();
  view.select({ ...session, capabilities: { ...session.capabilities, usage_metrics: undefined } });
  // Without native metrics the Usage tab keeps the recorded report alone.
  await openUsage();
  await waitFor(() => expect(tile('AI credits')).toBe('AI credits9.0'));
  expect(screen.queryByText(/Native usage metrics|Loading native/)).toBeNull();
  expect(read).not.toHaveBeenCalled();
});

test('native optional counters remain unknown and failures never become zero totals', async () => {
  const read = vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue({ ...metrics, ai_units: undefined, premium_request_cost: .000001, agents: [], truncated: true });
  draw({ ...session, usage: undefined });
  await openUsage();
  await screen.findByText(/^Native AI credits Not reported · Premium request cost 0\.000001 · /);
  expect(tile('AI credits')).toBe('AI creditsNot reported yet1 request');
  expect(screen.queryByText('0')).toBeNull();
  expect(screen.getByText(/Some breakdown rows are not shown/)).toBeTruthy();
  await userEvent.click(screen.getByRole('button', { name: 'Close', exact: true }));
  read.mockRejectedValue(new Error('Native metrics unavailable'));
  await openUsage();
  expect(await screen.findByText('Native metrics unavailable')).toBeTruthy();
  expect(screen.queryByText(/Native AI credits/)).toBeNull();
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
  // Without a budget: one line and Set a budget, which opens the form; a saved budget closes it again.
  expect(await screen.findByText(/^No limit\./)).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Set budget' })).toBeNull();
  await userEvent.click(screen.getByRole('button', { name: 'Set a budget' }));
  const setBudget = screen.getByRole('button', { name: 'Set budget' }) as HTMLButtonElement;
  expect(setBudget.disabled).toBe(true);
  await userEvent.type(screen.getByLabelText(/Stop after/), '2.5');
  await userEvent.type(screen.getByLabelText(/Stop at/), '80');
  await userEvent.click(setBudget);
  expect(settings).toHaveBeenCalledWith('task-1', { budget: { credits: 2.5, quota_percent: 80 } });
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Set budget' })).toBeNull());

  // At the limit a running turn wraps up first, then its handoff ends the conversation.
  view.select({ ...session, state: 'working', budget: { credits: 2.5, spent: 2.6, reached: true, wrapping_up: true } } as SessionDetail);
  expect(screen.getByText(/Budget reached: the Thread is wrapping up\. A tool already running finishes; uam denies new ones and has the agent write a handoff\./)).toBeTruthy();
  view.select({ ...session, state: 'completed', budget: { credits: 2.5, spent: 2.6, reached: true }, handoff: { at: '2026-10-17T00:00:00Z', reply_id: 'r', facts: '## Facts' } } as SessionDetail);
  expect(screen.getByText(/Budget reached: the handoff is at the end of the conversation\. Raise or remove the budget to send more\./)).toBeTruthy();

  view.select({ ...session, state: 'cancelled', budget: { credits: 2.5, spent: 2.6, quota_percent: 80, reached: true } } as SessionDetail);
  expect(screen.getByText('stops at 2.5').parentElement!.textContent).toBe('2.6 of 2.5 AI credits since set · 104%stops at 2.5');
  expect(screen.getByText('stops at 80%').parentElement!.textContent).toBe('Account quota not read yetstops at 80%');
  expect(screen.getByText(/Budget reached: uam stopped this Thread/)).toBeTruthy();
  await userEvent.click(screen.getByRole('button', { name: 'Change' }));
  await userEvent.click(screen.getByRole('button', { name: 'Remove' }));
  expect(settings).toHaveBeenLastCalledWith('task-1', { budget: { credits: 0, quota_percent: 0 } });
});
