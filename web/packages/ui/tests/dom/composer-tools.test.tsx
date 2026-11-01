import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type ContextInfo, type SessionDetail, type TaskUsageMetrics } from '../../src/api';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { ComposerTools } from '../../src/components/ComposerTools';
import { composer, renderApp } from './render';

afterEach(() => vi.restoreAllMocks());

const app = { settings: {}, usage: null } as unknown as AppContextValue;
const session = {
  id: 'task-1', project_id: 'p1', conversation_id: 'conv-1', provider: 'copilot', model: 'auto', open: true, usage: { ai_units: 9 },
  context: { used: 50, limit: 200 }, capabilities: { usage: true, usage_metrics: true, context_breakdown: true, custom_agents: true },
} as unknown as SessionDetail;
const info: ContextInfo = { model: 'resolved-model', total_tokens: 75, limit: 200, prompt_token_limit: 160, compaction_threshold: 128, buffer_tokens: 40, system_tokens: 10, conversation_tokens: 50, tool_definition_tokens: 15, mcp_tools_tokens: 5 };
const metrics: TaskUsageMetrics = { started_at: '2026-10-09T00:00:00Z', current_model: 'native-current', user_requests: 1, premium_request_cost: .5, api_duration_ms: 700, ai_units: 2, last_input: 100, last_output: 20, code_changes: { files: 1, added: 5, removed: 2 }, token_details: [], models: [], agents: [] };

function draw(selected: SessionDetail = session, onAgent = vi.fn()) {
  vi.spyOn(api, 'contextBreakdown').mockResolvedValue({ info });
  vi.spyOn(api, 'contextHeaviest').mockResolvedValue(null);
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  vi.spyOn(api, 'taskAgents').mockResolvedValue({ agents: [{ id: 'reviewer', name: 'reviewer', display_name: 'Reviewer', description: 'Reviews a change', source: 'project' }] } as Awaited<ReturnType<typeof api.taskAgents>>);
  const view = render(<ApiContext.Provider value={api}><AppContext.Provider value={app}><ComposerTools session={selected} onAgent={onAgent} /></AppContext.Provider></ApiContext.Provider>);
  return { ...view, onAgent };
}

test('one Tools button carries the context ring and opens a tab per tool, switched with the arrow keys', async () => {
  const { onAgent } = draw();
  const button = screen.getByRole('button', { name: 'Tools. Context 50 of 200 tokens · 25%' });
  expect(button.querySelector('[data-compact-mark]')).toBeTruthy();
  await userEvent.click(button);
  const panel = within(await screen.findByRole('dialog', { name: 'Tools' }));
  expect(panel.getAllByRole('tab').map((t) => t.textContent)).toEqual(['Context', 'Usage', 'Agent']);
  const context = panel.getByRole('tab', { name: 'Context' });
  expect(context.getAttribute('aria-selected')).toBe('true');
  await waitFor(() => expect(document.activeElement).toBe(context));
  // Context: one section with the breakdown's total, the latest report's lines and the native breakdown; AI credits live on Usage.
  expect(await panel.findByText('75 of 200 tokens')).toBeTruthy();
  expect(panel.queryByText(/not reported yet/)).toBeNull();
  expect(panel.queryByText(/AI credits/)).toBeNull();
  expect(panel.getByText('switch tab')).toBeTruthy();
  // One fixed height for every tab: the body never takes its content's height.
  const body = panel.getByRole('tabpanel');
  expect(body.className).toMatch(/\bflex-none\b/);
  expect(body.className).toMatch(/\bh-\[/);
  // Usage: the Thread's figures (the recorded AI credits first) and the native metrics.
  await userEvent.keyboard('{ArrowRight}');
  expect(panel.getByRole('tab', { name: 'Usage' }).getAttribute('aria-selected')).toBe('true');
  expect(await panel.findByText('Current model: native-current')).toBeTruthy();
  expect(panel.getByText('AI credits', { selector: 'p' }).parentElement!.textContent).toBe('AI credits9.01 request');
  expect(panel.getByRole('tabpanel')).toBe(body);
  // Agent: the Project's agents as radios; picking one changes the Task's agent.
  await userEvent.keyboard('{ArrowRight}');
  expect(panel.getByRole('tab', { name: 'Agent' }).getAttribute('aria-selected')).toBe('true');
  await userEvent.click(await panel.findByRole('radio', { name: /Reviewer.*Reviews a change/ }));
  expect(onAgent).toHaveBeenCalledWith('reviewer');
  // The arrows wrap; Esc closes and gives focus back to Tools.
  panel.getByRole('tab', { name: 'Agent' }).focus();
  await userEvent.keyboard('{ArrowRight}');
  expect(panel.getByRole('tab', { name: 'Context' }).getAttribute('aria-selected')).toBe('true');
  await userEvent.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Tools' })).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(button));
});

test('a tool the Task cannot show has no tab, and a Task with no tools has no button', async () => {
  const view = draw({ ...session, context: undefined, capabilities: { usage_metrics: true } as SessionDetail['capabilities'] });
  await userEvent.click(screen.getByRole('button', { name: 'Tools' }));
  const panel = within(await screen.findByRole('dialog', { name: 'Tools' }));
  expect(panel.getAllByRole('tab').map((t) => t.textContent)).toEqual(['Usage']);
  expect(panel.queryByRole('radio')).toBeNull();
  // The foot names no arrow keys for a single tab.
  expect(panel.queryByText('switch tab')).toBeNull();
  view.unmount();
  draw({ ...session, context: undefined, capabilities: {} as SessionDetail['capabilities'] });
  expect(screen.queryByRole('button', { name: /^Tools/ })).toBeNull();
});

test('the agent cannot change while a reason holds', async () => {
  vi.spyOn(api, 'taskAgents').mockResolvedValue({ agents: [{ id: 'reviewer', name: 'reviewer', source: 'project' }] } as unknown as Awaited<ReturnType<typeof api.taskAgents>>);
  render(<ApiContext.Provider value={api}><AppContext.Provider value={app}><ComposerTools session={{ ...session, capabilities: { custom_agents: true } as SessionDetail['capabilities'], context: undefined }} agentReason="The agent changes between turns." onAgent={() => {}} /></AppContext.Provider></ApiContext.Provider>);
  await userEvent.click(await screen.findByRole('button', { name: 'Tools' }));
  const panel = within(await screen.findByRole('dialog', { name: 'Tools' }));
  expect(panel.getByText('The agent changes between turns.')).toBeTruthy();
  expect(panel.getByRole('radio', { name: 'Default' }).getAttribute('aria-disabled')).toBe('true');
});

test('a Project without custom agents has no Agent tab, unless the Task names an agent', async () => {
  const caps = { usage: true, custom_agents: true } as SessionDetail['capabilities'];
  const tree = (selected: SessionDetail) => <ApiContext.Provider value={api}><AppContext.Provider value={app}><ComposerTools session={selected} onAgent={() => {}} /></AppContext.Provider></ApiContext.Provider>;
  const read = vi.spyOn(api, 'taskAgents').mockResolvedValue({ agents: [] } as unknown as Awaited<ReturnType<typeof api.taskAgents>>);
  const view = render(tree({ ...session, context: undefined, capabilities: caps }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  await userEvent.click(screen.getByRole('button', { name: 'Tools' }));
  const panel = within(await screen.findByRole('dialog', { name: 'Tools' }));
  expect(panel.getAllByRole('tab').map((t) => t.textContent)).toEqual(['Usage']);
  view.unmount();
  // A Task whose agent is gone keeps the tab, so the owner can pick again.
  render(tree({ ...session, context: undefined, capabilities: caps, agent: 'gone' }));
  await userEvent.click(screen.getByRole('button', { name: 'Tools' }));
  const again = within(await screen.findByRole('dialog', { name: 'Tools' }));
  expect(again.getAllByRole('tab').map((t) => t.textContent)).toEqual(['Usage', 'Agent']);
  await userEvent.click(again.getByRole('tab', { name: 'Agent' }));
  expect(await again.findByText('Not found in this project')).toBeTruthy();
});

test('skills the Thread loaded show on the Agent tab, even without custom agents', async () => {
  const skills_loaded = [
    { name: 'tdd', description: 'Build the next change test-first', first_at: '2026-10-12T03:28:00Z', count: 1 },
    { name: 'unslop', first_at: '2026-10-12T03:41:00Z', count: 2 },
  ];
  const tree = (selected: SessionDetail) => <ApiContext.Provider value={api}><AppContext.Provider value={app}><ComposerTools session={selected} onAgent={() => {}} /></AppContext.Provider></ApiContext.Provider>;
  const read = vi.spyOn(api, 'taskAgents').mockResolvedValue({ agents: [] } as unknown as Awaited<ReturnType<typeof api.taskAgents>>);
  const view = render(tree({ ...session, context: undefined, capabilities: { usage: true, custom_agents: true } as SessionDetail['capabilities'], skills_loaded }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  await userEvent.click(screen.getByRole('button', { name: 'Tools' }));
  const panel = within(await screen.findByRole('dialog', { name: 'Tools' }));
  expect(panel.getAllByRole('tab').map((t) => t.textContent)).toEqual(['Usage', 'Agent']);
  await userEvent.click(panel.getByRole('tab', { name: 'Agent' }));
  // No custom agents: no agent choices, only the skills, first loaded first, with how often each loaded.
  expect(panel.queryByRole('radio')).toBeNull();
  expect(panel.queryByRole('region', { name: 'Agent' })).toBeNull();
  const section = within(panel.getByRole('region', { name: 'Skills' }));
  expect(section.getByText('2 loaded')).toBeTruthy();
  const rows = within(section.getByRole('list', { name: 'Skills loaded' })).getAllByRole('listitem');
  expect(rows.map((r) => r.querySelector('.font-mono')?.textContent)).toEqual(['tdd', 'unslop']);
  expect(within(rows[0]).getByText('Build the next change test-first')).toBeTruthy();
  expect(within(rows[0]).queryByText(/loads$/)).toBeNull();
  expect(within(rows[1]).getByText('2 loads')).toBeTruthy();
  view.unmount();
  // With custom agents, the agent choices come first under their own label.
  draw({ ...session, skills_loaded });
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  const both = within(await screen.findByRole('dialog', { name: 'Tools' }));
  await userEvent.click(both.getByRole('tab', { name: 'Agent' }));
  expect(await within(both.getByRole('region', { name: 'Agent' })).findByRole('radio', { name: /Reviewer/ })).toBeTruthy();
  expect(both.getAllByRole('region').map((r) => r.getAttribute('aria-label'))).toEqual(['Agent', 'Skills']);
});

test('/context opens Tools on its Context tab', async () => {
  vi.spyOn(api, 'commands').mockResolvedValue([{ name: 'context', description: 'Show context', kind: 'command', input_hint: '' }]);
  vi.spyOn(api, 'command').mockResolvedValue({ request_id: 'ctx', status: 'accepted', time: new Date().toISOString(), command_result: { kind: 'action', action: 'context' } });
  const { user } = renderApp('#task=t3');
  await screen.findByRole('region', { name: 'Conversation' });
  await waitFor(() => expect(composer()?.disabled).toBe(false));
  await user.type(composer(), '/context');
  await within(await screen.findByRole('listbox', { name: 'Commands' })).findByRole('option', { name: /^\/context/ });
  await user.keyboard('{Enter}{Enter}');
  const panel = within(await screen.findByRole('dialog', { name: 'Tools' }));
  expect(panel.getByRole('tab', { name: 'Context' }).getAttribute('aria-selected')).toBe('true');
});

test('with a style library, the Style tab picks the Thread\'s output style', async () => {
  vi.spyOn(api, 'contextBreakdown').mockResolvedValue({ info });
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  vi.spyOn(api, 'taskAgents').mockResolvedValue({ agents: [] } as Awaited<ReturnType<typeof api.taskAgents>>);
  const styled = { ...app, settings: { output_styles: [{ name: 'Terse', text: 'Be terse.\nNo filler.' }], output_style: 'Terse' } } as unknown as AppContextValue;
  const onStyle = vi.fn();
  render(<ApiContext.Provider value={api}><AppContext.Provider value={styled}><ComposerTools session={session} onAgent={vi.fn()} onStyle={onStyle} /></AppContext.Provider></ApiContext.Provider>);
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  const panel = within(await screen.findByRole('dialog', { name: 'Tools' }));
  await userEvent.click(panel.getByRole('tab', { name: 'Style' }));
  const inherit = panel.getByRole('radio', { name: /Project or default.*\(Terse\)/ });
  expect(inherit.getAttribute('aria-checked')).toBe('true');
  await userEvent.click(panel.getByRole('radio', { name: /Terse.*Be terse\./ }));
  expect(onStyle).toHaveBeenCalledWith('Terse');
  await userEvent.click(panel.getByRole('radio', { name: /Copilot's own/ }));
  expect(onStyle).toHaveBeenCalledWith('Copilot');
});

test('a started Thread shows its pinned style, read only', async () => {
  vi.spyOn(api, 'contextBreakdown').mockResolvedValue({ info });
  vi.spyOn(api, 'taskUsageMetrics').mockResolvedValue(metrics);
  vi.spyOn(api, 'taskAgents').mockResolvedValue({ agents: [] } as Awaited<ReturnType<typeof api.taskAgents>>);
  const styled = { ...app, settings: { output_styles: [{ name: 'Terse', text: 'Be terse.' }] } } as unknown as AppContextValue;
  render(<ApiContext.Provider value={api}><AppContext.Provider value={styled}><ComposerTools session={{ ...session, output_style: 'Terse' }} onAgent={vi.fn()} onStyle={vi.fn()} styleReason="Fixed when the Thread started." /></AppContext.Provider></ApiContext.Provider>);
  await userEvent.click(screen.getByRole('button', { name: /^Tools/ }));
  const panel = within(await screen.findByRole('dialog', { name: 'Tools' }));
  await userEvent.click(panel.getByRole('tab', { name: 'Style' }));
  const terse = panel.getByRole('radio', { name: /Terse/ });
  expect(terse.getAttribute('aria-checked')).toBe('true');
  expect(terse.hasAttribute('data-disabled')).toBe(true);
  expect(panel.getByText('Fixed when the Thread started.')).toBeTruthy();
});
