import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type ContextAttribution, type ContextBreakdown, type ContextInfo, type SessionDetail } from '../../src/api';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { ComposerTools } from '../../src/components/ComposerTools';

beforeEach(() => { vi.spyOn(api, 'contextHeaviest').mockResolvedValue(null); });
afterEach(() => vi.restoreAllMocks());

const session = { id: 'task-1', conversation_id: 'conv-1', provider: 'copilot', model: 'auto', open: true, context: { used: 50, limit: 200 }, capabilities: { context_breakdown: true } } as unknown as SessionDetail;
const app = { settings: {}, usage: null } as unknown as AppContextValue;
const info: ContextInfo = { model: 'resolved-model', total_tokens: 75, limit: 200, prompt_token_limit: 160, compaction_threshold: 128, buffer_tokens: 40, system_tokens: 10, conversation_tokens: 50, tool_definition_tokens: 15, mcp_tools_tokens: 5 };
const attribution: ContextAttribution = { model: 'resolved-model', model_source: 'autoResolved', total_tokens: 75, limit: 200, prompt_token_limit: 160, compaction_threshold: 128, buffer_tokens: 40, compactions: 2, categories: { system_prompt: 5, custom_instructions: 5, system_tools: 10, mcp_tools: 5, messages: 50, free_space: 85, buffer: 40 }, entries: [
  { id: 'plugin:tools', kind: 'plugin', label: 'Shared tools', tokens: 30 },
  { id: 'future:child', kind: 'future-kind', label: 'Nested tool', parent_id: 'plugin:tools', tokens: 20 },
] };

function draw(selected = session, client = api) {
  const tree = (current: SessionDetail, owner = client) => <ApiContext.Provider value={owner}><AppContext.Provider value={app}><ComposerTools session={current} onAgent={() => {}} /></AppContext.Provider></ApiContext.Provider>;
  const view = render(tree(selected));
  return { ...view, select: (current: SessionDetail) => view.rerender(tree(current)), selectOwner: (owner: typeof api) => view.rerender(tree(selected, owner)) };
}

test('context categories and sources read only when requested and never sum capacity or parents', async () => {
  const read = vi.spyOn(api, 'contextBreakdown').mockImplementation(async (_id, sources) => sources ? { attribution } : { info });
  const view = draw({ ...session, context: { used: 50, limit: 200, prompt: 60, cached: 20 } });
  expect(read).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  expect(await screen.findByRole('dialog', { name: 'Tools' })).toBeTruthy();
  expect(await screen.findByText('75 of 200 tokens')).toBeTruthy();
  expect(screen.getByText('Latest prompt: 60 tokens')).toBeTruthy();
  expect(screen.getByText('Cached in latest call: 20 tokens')).toBeTruthy();
  // One Context section: no second "Latest report" disagreeing with the breakdown.
  expect(screen.getAllByRole('region').map((r) => r.getAttribute('aria-label'))).toEqual(['Context', 'Capacity', 'Sources', 'Largest messages']);
  expect(screen.queryByText(/not reported yet|occupied|MCP tools are included/)).toBeNull();
  expect(read).toHaveBeenCalledTimes(1);
  expect(read.mock.calls[0].slice(0, 2)).toEqual(['task-1', false]);
  // Token churn without a new context report neither polls nor restarts the open reader.
  view.select({ ...session, context: { used: 50, limit: 200, prompt: 61, cached: 20 } });
  expect(screen.getByRole('dialog', { name: 'Tools' })).toBeTruthy();
  expect(read).toHaveBeenCalledTimes(1);
  // A new context report reads the breakdown again while the reader stays open, keeping the last counts meanwhile.
  view.select({ ...session, context: { used: 51, limit: 204, prompt: 61, cached: 20 } });
  await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  expect(screen.getByRole('dialog', { name: 'Tools' })).toBeTruthy();
  expect(screen.getByText('75 of 200 tokens')).toBeTruthy();
  await userEvent.click(screen.getByRole('button', { name: 'Show sources' }));
  expect(await screen.findByText('Included in Shared tools')).toBeTruthy();
  expect(screen.getByText(/Counted for resolved-model \(Auto resolved\)/)).toBeTruthy();
  expect(screen.getByText('Free space')).toBeTruthy();
  expect(screen.getByText('85 tokens')).toBeTruthy();
  expect(screen.getByText(/Source counts can overlap/)).toBeTruthy();
  expect(screen.queryByText(/^125 of|^50 of/)).toBeNull();
  await userEvent.click(screen.getByText('future-kind', { exact: false }));
  expect(read).toHaveBeenCalledTimes(3);
  expect(read.mock.calls[2].slice(0, 2)).toEqual(['task-1', true]);
  await userEvent.click(screen.getByRole('button', { name: 'Close', exact: true }));
  expect(screen.queryByText('Nested tool')).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: /^Tools.*25%/ })));
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  await screen.findByText('Including MCP tools');
  expect(read).toHaveBeenCalledTimes(4);
});

test('closed and unsupported providers keep quick totals without resuming or requesting metadata', async () => {
  const read = vi.spyOn(api, 'contextBreakdown').mockResolvedValue({ info });
  const view = draw({ ...session, open: false });
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  expect(await screen.findByText('Context breakdown is unavailable for a closed Thread.')).toBeTruthy();
  expect(read).not.toHaveBeenCalled();
  view.select({ ...session, capabilities: {} as SessionDetail['capabilities'] });
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  expect(await screen.findByText(/Compacts at 80%/)).toBeTruthy();
  expect(screen.queryByText('Show sources')).toBeNull();
  expect(read).not.toHaveBeenCalled();
});

test('a draft Thread is not closed: its context waits for the Thread to start', async () => {
  const read = vi.spyOn(api, 'contextBreakdown').mockResolvedValue({ info });
  draw({ ...session, id: '', conversation_id: '', open: false, context: undefined });
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  expect(await screen.findByText('Context breakdown is reported once the Thread starts.')).toBeTruthy();
  expect(screen.queryByText(/closed Thread/)).toBeNull();
  expect(read).not.toHaveBeenCalled();
});

test('missing native context is unavailable and an error remains an error', async () => {
  const read = vi.spyOn(api, 'contextBreakdown').mockResolvedValue({});
  const view = draw({ ...session, context: undefined });
  await userEvent.click(screen.getByRole('button', { name: 'Tools' }));
  expect(await screen.findByText('Context breakdown has not been initialized by this conversation.')).toBeTruthy();
  expect(screen.queryByText(/^0 of/)).toBeNull();
  await userEvent.click(screen.getByRole('button', { name: 'Close', exact: true }));
  read.mockRejectedValue(new Error('Context service unavailable'));
  await userEvent.click(screen.getByRole('button', { name: 'Tools' }));
  expect(await screen.findByText('Context service unavailable')).toBeTruthy();
  view.unmount();
});

test('selection changes and closing abort reads and ignore late results', async () => {
  let deliver!: (data: ContextBreakdown) => void;
  const read = vi.spyOn(api, 'contextBreakdown').mockImplementationOnce(() => new Promise(resolve => { deliver = resolve; })).mockResolvedValue({ info });
  const view = draw();
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  const signal = read.mock.calls[0][2]!;
  view.select({ ...session, model: 'different' });
  expect(signal.aborted).toBe(true);
  await act(async () => deliver({ attribution }));
  expect(screen.queryByText('Shared tools')).toBeNull();
  expect(screen.queryByRole('dialog', { name: 'Tools' })).toBeNull();
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  await screen.findByText('Including MCP tools');
  const secondSignal = read.mock.calls[1][2]!;
  await userEvent.click(screen.getByRole('button', { name: 'Close', exact: true }));
  expect(secondSignal.aborted).toBe(true);
});

test('a phone opens the shared sheet and reads from its owning API client', async () => {
  vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: true } as MediaQueryList);
  const localRead = vi.spyOn(api, 'contextBreakdown');
  const remoteRead = vi.fn(async () => ({ info }));
  draw(session, { ...api, contextBreakdown: remoteRead });
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  expect(await screen.findByRole('dialog', { name: 'Tools' })).toBeTruthy();
  await screen.findByText('75 of 200 tokens');
  expect(remoteRead).toHaveBeenCalledTimes(1);
  expect(localRead).not.toHaveBeenCalled();
  await userEvent.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Tools' })).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: /^Tools.*25%/ })));
});

test('changing the owning API closes and disposes the old reader before another source read', async () => {
  const local = vi.spyOn(api, 'contextBreakdown').mockResolvedValue({ info });
  const remote = vi.fn(async () => ({ info: { ...info, model: 'remote-model' } }));
  const view = draw();
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  await screen.findByText('Including MCP tools');
  const previousSignal = local.mock.calls[0][2]!;
  view.selectOwner({ ...api, contextBreakdown: remote });
  expect(previousSignal.aborted).toBe(true);
  expect(screen.queryByRole('dialog', { name: 'Tools' })).toBeNull();
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  await screen.findByText(/Counted for remote-model/);
  expect(remote).toHaveBeenCalledTimes(1);
  expect(local).toHaveBeenCalledTimes(1);
});

test('the largest messages read once per opening and Compact now runs /compact only between turns', async () => {
  const command = vi.spyOn(api, 'command').mockResolvedValue({ request_id: 'r', status: 'accepted', time: '' });
  vi.spyOn(api, 'contextBreakdown').mockImplementation(async () => ({ info: { ...info, total_tokens: command.mock.calls.length ? 30 : 75 } }));
  const heaviest = vi.spyOn(api, 'contextHeaviest').mockResolvedValue({ total_tokens: 75, messages: [{ id: 'm1', role: 'tool', label: 'tool: bash', tokens: 40 }] });
  const view = draw({ ...session, state: 'working' } as SessionDetail);
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  expect(await screen.findByText('tool: bash')).toBeTruthy();
  expect(screen.getByText('tool: bash').closest('li')?.textContent).toBe('tool: bash40 tokens');
  expect(heaviest).toHaveBeenCalledTimes(1);
  const compact = screen.getByRole('button', { name: 'Compact now' });
  expect(compact.hasAttribute('disabled')).toBe(true);
  expect(screen.getByText(/uses model tokens/)).toBeTruthy();
  // Token churn while open does not poll the list.
  view.select({ ...session, state: 'completed', context: { used: 50, limit: 200, prompt: 61 } } as SessionDetail);
  expect(heaviest).toHaveBeenCalledTimes(1);
  await userEvent.click(screen.getByRole('button', { name: 'Compact now' }));
  expect(command.mock.calls[0].slice(0, 3)).toEqual(['task-1', 'compact', '']);
  expect(await screen.findByText('Compacted: 75 → 30 tokens in context.')).toBeTruthy();
  await waitFor(() => expect(heaviest).toHaveBeenCalledTimes(2));
  command.mockResolvedValue({ request_id: 'r', status: 'rejected', error: 'compaction failed', time: '' });
  await userEvent.click(screen.getByRole('button', { name: 'Compact now' }));
  expect(await screen.findByText('compaction failed')).toBeTruthy();
});
