import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { StatusLine, statusLine } from '../../src/components/StatusLine';
import type { SessionDetail, Subagent, Todo, TodoView, TurnTiming } from '../../src/api';
import { shownState } from '../../src/lib/tasks';

afterEach(() => vi.useRealTimers());

const start = Date.parse('2026-09-28T12:00:00Z');
const timing = { id: 'turn', user_item_id: 'u1', state: 'working', started_at: new Date(start).toISOString() } as TurnTiming;
const task = (extra: Partial<SessionDetail> = {}) => ({ id: 't', state: 'working', items: [], interactions: [], subagents: [], turn_timings: [timing], ...extra }) as SessionDetail;
const objective = { id: 1, objective: 'Notes', status: 'paused' as const, turn_count: 6, credits_used: 300, credit_limit: 300 };

function draw(session: SessionDetail, { working = session.state === 'working', since = undefined as string | undefined, onJump = () => {}, connected = true } = {}) {
  return <StatusLine line={statusLine(session, working, !!session.compacting, connected)} session={session} since={since} hidden={false} onJump={onJump} />;
}

test('a working turn with nothing of its own draws no strip, its time being the turn card\'s; the intent, a retry, the list each bring it back, without a clock', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 125_000);
  const onJump = vi.fn();
  const view = render(draw(task(), { onJump }));
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.queryByText('Working')).toBeNull();
  view.rerender(draw(task({ turn_activity: { intent: 'Building the index page', todos: { known: false } } }), { onJump }));
  expect(screen.getByText('Building the index page')).toBeTruthy();
  const button = screen.getByRole('button', { name: 'Building the index page. Jump to bottom' });
  expect(screen.queryByText('2m')).toBeNull();
  // Nothing on the line moves: no ring and no breathing dot.
  expect(view.container.querySelector('.animate-spin, .animate-pulse-dot')).toBeNull();
  fireEvent.click(button);
  expect(onJump).toHaveBeenCalledTimes(1);
  // A retry of the main agent's call stands beside the intent.
  view.rerender(draw(task({ turn_activity: { intent: 'Building the index page', retry: { count: 1, status: 429, reason: 'rate_limited', at: timing.started_at }, todos: { known: false } } })));
  expect(screen.getByText('Retrying, attempt 2')).toBeTruthy();
  expect(screen.getByText('· HTTP 429 · rate limited')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Building the index page. Retrying, attempt 2: HTTP 429, rate limited. Jump to bottom' })).toBeTruthy();
});

test('subagents that outlived the turn keep "Working" and their own clock on the strip', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 125_000);
  render(draw(task(), { since: new Date(start + 5000).toISOString() }));
  expect(screen.getByText('Working')).toBeTruthy();
  expect(screen.getByText('2m')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Working, 2 minutes. Jump to bottom' })).toBeTruthy();
});

test('after a stop the line says why until the next turn; nothing to say, no line', () => {
  const view = render(draw(task({ state: 'cancelled', stop_reason: 'credit_limit', execution: { known: true, mode: 'autopilot', objective } })));
  expect(screen.getByText('Stopped: credit limit')).toBeTruthy();
  expect(screen.getByText('Autopilot stopped: credit limit reached')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Autopilot stopped: credit limit reached, 300 of 300 credits used, 6 turns. Jump to bottom' })).toBeTruthy();
  view.rerender(draw(task({ state: 'cancelled', stop_reason: 'owner' })));
  expect(screen.getByRole('button', { name: 'Stopped. Jump to bottom' })).toBeTruthy();
  view.rerender(draw(task({ state: 'completed', execution: { known: true, mode: 'autopilot', objective: { ...objective, pause_reason: 'Waiting for a token' } } })));
  expect(screen.getByRole('button', { name: 'Autopilot paused: Waiting for a token. Jump to bottom' })).toBeTruthy();
  for (const quiet of [task({ state: 'cancelled' }), task({ state: 'completed' }), task({ state: 'cancelled', stop_reason: 'owner', stage: 'settled' })]) {
    view.rerender(draw(quiet));
    expect(screen.queryByRole('button')).toBeNull();
  }
});

test('the live region speaks a turn start, a retry and a stop, at most one every 5s, never what was already shown', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start);
  const region = () => screen.getByRole('status').textContent;
  // Already working when it mounts: nothing to announce.
  const mounted = render(draw(task()));
  act(() => vi.advanceTimersByTime(10_000));
  expect(region()).toBe('');
  mounted.unmount();

  const view = render(draw(task({ state: 'idle' })));
  view.rerender(draw(task()));
  act(() => vi.advanceTimersByTime(0));
  expect(region()).toBe('Working');
  // The intent changing is not news.
  view.rerender(draw(task({ turn_activity: { intent: 'Reading', todos: { known: false } } })));
  act(() => vi.advanceTimersByTime(10));
  expect(region()).toBe('Working');
  // A retry waits for the slot.
  view.rerender(draw(task({ turn_activity: { intent: 'Reading', retry: { count: 1, reason: 'rate_limited', at: '2026-09-28T12:00:01Z' }, todos: { known: false } } })));
  act(() => vi.advanceTimersByTime(4000));
  expect(region()).toBe('');
  act(() => vi.advanceTimersByTime(1000));
  expect(region()).toBe('Retrying: rate limited');
  // Back to work after the retry: "Working" is not said twice in a turn.
  view.rerender(draw(task({ turn_activity: { intent: 'Reading', todos: { known: false } } })));
  act(() => vi.advanceTimersByTime(10_000));
  expect(region()).toBe('');
  view.rerender(draw(task({ state: 'cancelled', stop_reason: 'remote' })));
  act(() => vi.advanceTimersByTime(0));
  expect(region()).toBe('Stopped: remote command');
});

test('the line stays while a subagent outlives the turn', () => {
  for (const state of ['idle', 'completed'] as const) {
    const line = (subagents_running: number) => statusLine(task({ state, subagents_running, turn_timings: [{ ...timing, ended_at: new Date(start + 9000).toISOString(), state: 'completed' }] }), shownState({ state, subagents_running }) === 'working', false);
    expect(line(0)).toBeNull();
    expect(line(1)).toEqual({ kind: 'working', lead: '', compacting: false, retry: undefined, todo: undefined });
  }
});

test('a turn starting says nothing of the last one: no line until its timing comes, then still none while it has nothing of its own', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 20_000);
  const before = { id: 'before', user_item_id: 'u0', state: 'cancelled', started_at: new Date(start - 30_000).toISOString(), ended_at: new Date(start - 10_000).toISOString() } as TurnTiming;
  const next = { ...timing, user_item_id: undefined, started_at: new Date(start + 19_800).toISOString() };
  const view = render(draw(task({ state: 'cancelled', stop_reason: 'owner', turn_timings: [before] })));
  expect(screen.getByRole('button', { name: 'Stopped. Jump to bottom' })).toBeTruthy();
  // The Thread works before the turn's timing comes: nothing of the last turn stands for this one.
  view.rerender(draw(task({ turn_timings: [before] })));
  expect(screen.queryByRole('button')).toBeNull();
  view.rerender(draw(task({ turn_timings: [before, next] })));
  expect(screen.queryByRole('button')).toBeNull();
  expect(statusLine(task({ turn_timings: [before, next] }), true, false)).toEqual({ kind: 'working', lead: '', compacting: false, retry: undefined, todo: undefined });
});

test('a turn a restart or close cut off gives the next one no line while it starts; a dropped stream keeps the running turn\'s', () => {
  // The service marks such a turn unknown, without an end.
  const cut = { ...timing, user_item_id: 'u0', state: 'unknown' } as TurnTiming;
  // You reopen the Thread with a message: it starts, then works, before the new turn's timing comes.
  for (const state of ['starting', 'working'] as const) {
    const view = render(draw(task({ state, turn_timings: [cut] }), { working: true }));
    expect(screen.queryByRole('button')).toBeNull();
    view.unmount();
  }
  // The page lost its stream and marked the running turn unknown itself: the turn may still run.
  expect(statusLine(task({ turn_timings: [cut] }), true, false, false)?.kind).toBe('working');
});

const rows = (extra: Partial<Todo>[] = []): Todo[] =>
  [
    { id: 'a', title: 'Plan the pages', status: 'done' },
    { id: 'b', title: 'Build the index page', status: 'in_progress', agent_id: 'sub-1' },
    { id: 'c', title: 'Build the about page', status: 'in_progress' },
    { id: 'd', title: 'Sign in to the registry', status: 'blocked', note: 'No token on this machine' },
  ].map((row, i) => ({ ...row, ...extra[i] }) as Todo);
const listOf = (todos: Todo[], extra: Partial<TodoView> = {}): TodoView => ({
  known: true,
  touched: true,
  todos,
  counts: {
    total: todos.length,
    done: todos.filter((t) => t.status === 'done').length,
    blocked: todos.filter((t) => t.status === 'blocked').length,
    in_progress: todos.filter((t) => t.status === 'in_progress').length,
    pending: todos.filter((t) => t.status === 'pending').length,
    open: todos.filter((t) => t.status === 'pending' || t.status === 'in_progress').length,
  },
  now: 'b',
  ...extra,
});
const withTodos = (todos: TodoView, intent = 'Working on the site', extra: Partial<SessionDetail> = {}) => task({ turn_activity: { intent, todos }, subagents: [{ id: 'sub-1', name: 'Index page writer', status: 'running' } as Subagent], ...extra });

test('native plan progress labels only the approved current touched list, and opens a Plan tab', async () => {
  const user = userEvent.setup();
  const current = (todos: TodoView) => withTodos(todos, 'Implementing the plan', { turn_activity: { plan: true, intent: 'Implementing the plan', todos } });
  const view = render(draw(current(listOf(rows(), { touched: false }))));
  expect(screen.queryByText('Plan')).toBeNull();
  expect(screen.getByText('From the last turn')).toBeTruthy();
  view.rerender(draw(current(listOf(rows()))));
  const button = screen.getByRole('button', { name: /Plan 1 of 4 done/ });
  await user.click(button);
  const reader = within(await screen.findByRole('dialog', { name: 'Plan' }));
  expect(reader.getByRole('tab', { name: 'Plan' })).toBeTruthy();
});

/** The blocks on the strip, as their glyph-less words: "1 done", "2 in progress"… in order. */
const blocks = (root: HTMLElement) => [...root.querySelectorAll('[title]')].map((b) => `${b.getAttribute('title')} ${b.className.match(/text-(accent|warning|muted|success|error)/)?.[1]} ${b.querySelector('svg') ? 'glyph' : 'no glyph'}`);

test('the todo list is stage blocks with their glyph and number, then Now; no meter, no counts in words, no made-up verb', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 125_000);
  const view = render(draw(withTodos(listOf(rows()))));
  const button = screen.getByRole('button', { name: 'Now: Build the index page. Todo 1 of 4 done, 2 in progress, 1 blocked. Show the list' });
  expect(button.getAttribute('aria-haspopup')).toBe('dialog');
  expect(button.getAttribute('aria-expanded')).toBe('false');
  // ✓ done, ◉ in progress, ⊘ blocked: each its number behind its glyph in its tone; a stage with no rows is left out.
  expect(blocks(button)).toEqual(['1 done success glyph', '2 in progress accent glyph', '1 blocked warning glyph']);
  expect([...button.querySelectorAll('[title]')].map((b) => b.textContent?.replace(b.getAttribute('title')!, ''))).toEqual(['1', '2', '1']);
  expect(screen.getByText('Now:').parentElement!.textContent).toBe('Now: Build the index page');
  // The intent is not on screen while a row is in progress, nor the counts in words, nor the meter.
  expect(screen.queryByText(/Working on the site/)).toBeNull();
  expect(screen.queryByText(/in progress$/, { ignore: '.sr-only' })).toBeNull();
  expect(view.container.querySelectorAll('.h-1.w-3').length).toBe(0);
  expect(view.container.querySelector('.animate-spin, .animate-pulse-dot')).toBeNull();
  // A row to do adds its block.
  view.rerender(draw(withTodos(listOf(rows([{}, {}, { status: 'pending' }])))));
  expect(blocks(screen.getByRole('button'))).toEqual(['1 done success glyph', '1 in progress accent glyph', '1 blocked warning glyph', '1 to do muted glyph']);
  // No row in progress: the agent's words take Now's place.
  view.rerender(draw(withTodos(listOf(rows([{}, { status: 'pending' }, { status: 'pending' }]), { now: undefined }))));
  expect(screen.getByText('Working on the site')).toBeTruthy();
  expect(screen.queryByText('Now:')).toBeNull();
  // The last turn's list, not touched yet: the same blocks, dimmed, "From the last turn", and the reader still opens.
  view.rerender(draw(withTodos(listOf(rows(), { touched: false }))));
  expect(screen.getByText('From the last turn')).toBeTruthy();
  expect(screen.getByText('From the last turn').previousElementSibling!.className).toContain('opacity-55');
  expect(screen.getByRole('button', { name: 'Working on the site. Todo from the last turn: 1 of 4 done, 2 in progress, 1 blocked. Show the list' })).toBeTruthy();
  // No list: the line jumps to the bottom as before.
  view.rerender(draw(withTodos({ known: false, touched: false, todos: [], counts: {} })));
  expect(screen.getByRole('button', { name: 'Working on the site. Jump to bottom' }).getAttribute('aria-haspopup')).toBeNull();
});

test('the reader opens on its Todo tab beside the line: rows in plan order, no repeated counts; Esc gives focus back', async () => {
  const user = userEvent.setup();
  render(draw(withTodos(listOf(rows()))));
  const line = screen.getByRole('button', { name: /Show the list$/ });
  await user.click(line);
  const reader = await screen.findByRole('dialog', { name: 'Todo' });
  expect(line.getAttribute('aria-expanded')).toBe('true');
  expect(line.getAttribute('aria-controls')).toBe(reader.id);
  const tab = within(reader).getByRole('tab', { name: 'Todo' });
  await waitFor(() => expect(document.activeElement).toBe(tab));
  // Without subagents (no Thread scope here), one tab and no "switch" hint.
  expect(within(reader).getAllByRole('tab').length).toBe(1);
  expect(within(reader).queryByText('switch')).toBeNull();
  // Plan order, states and the subagent in words; the row the work is at says "now"; the blocked reason under its row.
  const items = within(reader).getAllByRole('listitem').map((li) => li.textContent);
  expect(items).toEqual([
    'Plan the pages, Done',
    'Build the index pagenow, by Index page writer, In progress',
    'Build the about page, In progress',
    'Sign in to the registryNo token on this machine, Blocked',
  ]);
  // No count repeats the strip's blocks: no sections, no "1 done of 4".
  expect(within(reader).queryAllByRole('region').length).toBe(0);
  expect(within(reader).queryByText(/done/)).toBeNull();
  expect(within(reader).getByText('Esc')).toBeTruthy();
  expect(within(reader).getByText('Read only')).toBeTruthy();
  // The page behind is out of reach: focus stays in the reader.
  for (let i = 0; i < 3; i++) {
    await user.tab();
    expect(reader.contains(document.activeElement)).toBe(true);
  }
  // Focus may rest on the foot's "Read only" help, whose tip the first Esc closes.
  await user.keyboard('{Escape}');
  if (screen.queryByRole('dialog')) await user.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(document.activeElement).toBe(line);
  expect(line.getAttribute('aria-expanded')).toBe('false');
});

test('a phone gets the tabs as a sheet that repeats the blocks, with a close button and no key hints', async () => {
  const happy = (window as unknown as { happyDOM: { setViewport: (v: { width: number; height: number }) => void } }).happyDOM;
  happy.setViewport({ width: 390, height: 844 });
  try {
    const user = userEvent.setup();
    render(draw(withTodos(listOf(rows()))));
    await user.click(screen.getByRole('button', { name: /Show the list$/ }));
    const sheet = await screen.findByRole('dialog', { name: 'Todo' });
    // Modal, as the desktop reader: Tab stays in the sheet and the page behind is hidden from assistive tech.
    expect(sheet.getAttribute('aria-modal')).toBe('true');
    expect(screen.queryByRole('button', { name: /Show the list$/ })).toBeNull();
    for (let i = 0; i < 3; i++) {
      await user.tab();
      expect(sheet.contains(document.activeElement)).toBe(true);
    }
    // The sheet covers the strip, so it repeats the blocks.
    expect(blocks(sheet)).toEqual(['1 done success glyph', '2 in progress accent glyph', '1 blocked warning glyph']);
    expect(within(sheet).queryByText('Esc')).toBeNull();
    await user.click(within(sheet).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  } finally {
    happy.setViewport({ width: 1024, height: 768 });
  }
});

test('the live region says a row done or newly blocked, at most one every 5s', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start);
  const region = () => screen.getByRole('status').textContent;
  const at = (s: number) => new Date(start + s * 1000).toISOString();
  const view = render(draw(task({ state: 'idle' })));
  view.rerender(draw(withTodos(listOf(rows(), { touched: false }))));
  act(() => vi.advanceTimersByTime(0));
  expect(region()).toBe('Working');
  // Writing the list is not news; a row done is.
  view.rerender(draw(withTodos(listOf(rows()))));
  act(() => vi.advanceTimersByTime(6000));
  expect(region()).toBe('Working');
  view.rerender(draw(withTodos(listOf(rows([{ changed_at: at(7) }])))));
  act(() => vi.advanceTimersByTime(0));
  expect(region()).toBe('1 of 4 done, 2 in progress, 1 blocked');
  // A row blocked right after waits for the slot.
  view.rerender(draw(withTodos(listOf(rows([{ changed_at: at(7) }, {}, {}, { changed_at: at(8) }])))));
  act(() => vi.advanceTimersByTime(4000));
  expect(region()).toBe('');
  act(() => vi.advanceTimersByTime(1000));
  expect(region()).toBe('Blocked: Sign in to the registry');
});
