// The sidebar foot's one button for the account allowance and the AI work in flight, and the Now section its Usage
// popover opens with. Mock: t8's main agent and t7's compaction call the model, t8's subagent and t22's three audits
// run after its turn, t1 runs a tool locally, and one Background AI call runs.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { api, type ModelCalls, type Quota, type SessionSummary } from '../../src/api';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { ModelCallsContext } from '../../src/components/ModelPhase';
import { TaskList } from '../../src/components/taskActions';
import { UsageButton } from '../../src/components/UsageButton';
import { tokenPriceFixture, tokenUsageFixture } from '../../src/mock/token-usage';
import { renderApp, sidebar } from './render';

beforeEach(() => {
  vi.spyOn(api, 'tokenPrices').mockResolvedValue(tokenPriceFixture());
  vi.spyOn(api, 'tokenUsage').mockResolvedValue(tokenUsageFixture());
});
afterEach(() => vi.restoreAllMocks());

const QUOTA: Quota = { provider: 'copilot', type: 'ai_credits', used: 280, entitlement: 7000, remaining_percent: 96, unlimited: false, overage: 0 };
const IDLE: ModelCalls = { threads: 0, subagents: 0, background: 0, local: 0, model_threads: [], subagent_threads: [], local_threads: [] };
const BUSY: ModelCalls = { threads: 2, subagents: 1, background: 0, local: 1, model_threads: ['b', 'a'], subagent_threads: ['a'], local_threads: ['c'] };
const SESSIONS = [
  { id: 'a', name: 'Fix the login page', title: '' },
  { id: 'b', name: '', title: 'Survey the docs' },
  { id: 'c', name: 'Run the suite', title: '' },
  { id: 'd', name: 'Quiet thread', title: '' },
] as unknown as SessionSummary[];

function setup({ calls = IDLE as ModelCalls | null, quotas = [QUOTA], variant = 'chip' as 'chip' | 'rail', openTask = vi.fn() } = {}) {
  const tree = (c: ModelCalls | null, q: Quota[]) => (
    <AppContext.Provider value={{ usage: { quotas: q, stale: false }, meta: null } as unknown as AppContextValue}>
      <TaskList.Provider value={{ sessions: SESSIONS, projects: [], openTask }}>
        <ModelCallsContext.Provider value={c}><UsageButton variant={variant} /></ModelCallsContext.Provider>
      </TaskList.Provider>
    </AppContext.Provider>
  );
  const view = render(tree(calls, quotas));
  const button = () => screen.getByRole('button', { name: calls ? 'Usage and AI' : 'Usage', exact: true });
  const said = () => document.getElementById(button().getAttribute('aria-describedby')!)!.textContent;
  return { view, button, said, openTask, rerender: (c: ModelCalls | null, q: Quota[] = quotas) => view.rerender(tree(c, q)) };
}

describe('the Usage and AI button', () => {
  test('idle: the share left, a hairline, a faint sparkle and "idle"; flat until hovered', () => {
    const { button, said } = setup();
    expect(button().textContent).toBe('96% leftidle');
    expect(button().getAttribute('data-model-calls')).toBe('0');
    expect(button().querySelector('.lucide-sparkle')!.getAttribute('class')).toContain('text-faint');
    expect(said()).toMatch(/^AI credits: 96% left\. .*No model calls now$/);
    // No surface or shadow at rest; the Settings gear's tint on hover and while open.
    const classes = button().className.split(' ');
    expect(classes).toEqual(expect.arrayContaining(['not-aria-disabled:hover:bg-tint-hover', 'data-popup-open:bg-tint-selected', 'h-7']));
    expect(classes.some((c) => c.startsWith('bg-raised') || c.startsWith('shadow-'))).toBe(false);
    // Still 44px tall to a touch.
    expect(classes).toContain('pointer-coarse:after:-inset-y-2');
    expect(document.querySelector('.animate-spin, .animate-pulse')).toBeNull();
  });

  test('busy: the model calls in flight in the accent, the Threads running locally in green, all said in words', () => {
    const { button, said } = setup({ calls: BUSY });
    expect(button().textContent).toBe('96% left31');
    expect(button().getAttribute('data-model-calls')).toBe('3');
    expect(button().querySelector('.lucide-sparkle')!.getAttribute('class')).toContain('text-accent');
    expect(button().querySelector('.lucide-terminal')!.getAttribute('class')).toContain('text-success');
    expect(said()).toMatch(/Model working now\. 2 Threads · 1 subagent · Background AI idle\. Local: 1 Thread running a tool$/);
  });

  test('a low or spent allowance keeps its pace word; unknown and unlimited say so', () => {
    const { button, rerender } = setup();
    rerender(IDLE, [{ ...QUOTA, used: 7000, remaining_percent: 0 }]);
    expect(button().textContent).toBe('0% leftused upidle');
    rerender(IDLE, [{ ...QUOTA, unlimited: true, remaining_percent: 100 }]);
    expect(button().textContent).toBe('Unlimitedidle');
    rerender(IDLE, []);
    expect(button().textContent).toBe('Usageidle');
  });

  test('a service that does not count model calls shows the allowance alone', () => {
    const { button, said } = setup({ calls: null });
    expect(button().textContent).toBe('96% left');
    expect(button().hasAttribute('data-model-calls')).toBe(false);
    expect(button().querySelector('.lucide-sparkle')).toBeNull();
    expect(said()).not.toMatch(/model/i);
  });

  test('the rail stacks the share over its meter, then the work in flight', () => {
    const { button, rerender } = setup({ variant: 'rail' });
    // Idle, the rail has room for the faint sparkle alone; the description says "No model calls now".
    expect(button().textContent).toBe('96%');
    expect(button().querySelector('.lucide-sparkle')!.getAttribute('class')).toContain('text-faint');
    expect(button().className).toContain('flex-col');
    rerender(BUSY);
    expect(button().textContent).toBe('96%31');
  });

  test('Now names the Threads at work in the list’s order, each opening its Thread', async () => {
    const user = userEvent.setup();
    const { button, openTask } = setup({ calls: { ...BUSY, background: 2 } });
    await user.click(button());
    const now = within(await screen.findByRole('region', { name: 'Now' }));
    expect(now.getByText('Model working now')).toBeTruthy();
    const rows = now.getAllByRole('button');
    expect(rows.map((r) => r.textContent)).toEqual(['Fix the login pageModel · subagents', 'Survey the docsModel', 'Run the suiteLocal tool']);
    expect(now.getByText('Background AI').parentElement!.textContent).toBe('Background AI2 calls');
    await user.click(rows[2]);
    expect(openTask).toHaveBeenCalledWith('c');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Usage' })).toBeNull());
  });

  test('Now says "Nothing running" while idle; an older service’s counts in words, with no Threads named', async () => {
    const user = userEvent.setup();
    const { button, rerender } = setup();
    await user.click(button());
    const now = within(await screen.findByRole('region', { name: 'Now' }));
    expect(now.getByText('Nothing running')).toBeTruthy();
    rerender({ threads: 1, subagents: 0, background: 0, local: 1 });
    expect(now.queryByRole('button')).toBeNull();
    expect(now.getByText('1 Thread · Background AI idle')).toBeTruthy();
    expect(now.getByText('Local: 1 Thread running a tool')).toBeTruthy();
  });

  test('Threads past the service’s list are counted, not dropped', async () => {
    const user = userEvent.setup();
    const { button } = setup({ calls: { ...IDLE, threads: 3, model_threads: ['a'] } });
    await user.click(button());
    const now = within(await screen.findByRole('region', { name: 'Now' }));
    expect(now.getAllByRole('button')).toHaveLength(1);
    expect(now.getByText('2 more threads')).toBeTruthy();
  });

  test('a service with no model_calls opens Usage with no Now section', async () => {
    const user = userEvent.setup();
    const { button } = setup({ calls: null });
    await user.click(button());
    await screen.findByRole('region', { name: 'Allowance' });
    expect(screen.queryByRole('region', { name: 'Now' })).toBeNull();
  });

  test('is the one button in the sidebar foot and the rail, from the service snapshot; Now opens a Thread', async () => {
    const { user } = renderApp();
    const nav = await sidebar();
    const foot = within(screen.getByRole('navigation', { name: 'Threads' }).querySelector('footer')!);
    const button = await foot.findByRole('button', { name: 'Usage and AI' });
    expect(foot.queryByRole('button', { name: /^AI/ })).toBeNull();
    await waitFor(() => expect(button.textContent).toBe('96% left71'));
    expect(document.getElementById(button.getAttribute('aria-describedby')!)!.textContent).toMatch(/Model working now\. 2 Threads · 4 subagents · Background AI: 1 call\. Local: 1 Thread running a tool$/);
    await user.click(button);
    const now = within(await screen.findByRole('region', { name: 'Now' }));
    expect(now.getByRole('button', { name: /^Audit every package for dead exports/ }).textContent).toMatch(/Subagents$/);
    await user.click(now.getByRole('button', { name: /^Fix re-attach redraw regression/ }));
    await waitFor(() => expect(location.hash).toBe('#task=t1'));
    await user.click(nav.getByRole('button', { name: /^Hide sidebar/ }));
    const rail = within(screen.getByRole('navigation', { name: 'Sidebar' }));
    expect(rail.getAllByRole('button', { name: 'Usage and AI' })).toHaveLength(1);
    expect(rail.queryByRole('button', { name: /^AI/ })).toBeNull();
  });
});
