import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { StatusLine, statusLine } from '../../src/components/StatusLine';
import type { Item, SessionDetail, Subagent, Todo, TodoView, TurnTiming } from '../../src/api';
import { shownState } from '../../src/lib/tasks';
import { turnVerb } from '../../src/lib/verbs';

afterEach(() => vi.useRealTimers());

const start = Date.parse('2026-09-28T12:00:00Z');
const timing = { id: 'turn', user_item_id: 'u1', state: 'working', started_at: new Date(start).toISOString() } as TurnTiming;
const task = (extra: Partial<SessionDetail> = {}) => ({ id: 't', state: 'working', items: [], interactions: [], subagents: [], turn_timings: [timing], ...extra }) as SessionDetail;
const objective = { id: 1, objective: 'Notes', status: 'paused' as const, turn_count: 6, credits_used: 300, credit_limit: 300 };

function draw(session: SessionDetail, { working = session.state === 'working', items = [] as Item[], since = undefined as string | undefined, onJump = () => {} } = {}) {
  return <StatusLine line={statusLine(session, working, !!session.compacting, items)} session={session} since={since} hidden={false} onJump={onJump} />;
}

test('the clock excludes approval and question waits, including after remount', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 5000);
  const at = (value: TurnTiming) => draw(task({ turn_timings: [value] }));
  const view = render(at(timing));
  expect(screen.getByText('5s')).toBeTruthy();
  view.rerender(at({ ...timing, paused_at: new Date(start + 5000).toISOString() }));
  act(() => vi.advanceTimersByTime(60_000));
  expect(screen.getByText('5s')).toBeTruthy();
  view.rerender(at({ ...timing, paused_ms: 60_000 }));
  expect(screen.getByText('5s')).toBeTruthy();
  act(() => vi.advanceTimersByTime(1000));
  expect(screen.getByText('6s')).toBeTruthy();
  view.unmount();
  const resumed = render(at({ ...timing, paused_ms: 60_000 }));
  expect(screen.getByText('6s')).toBeTruthy();
  act(() => vi.advanceTimersByTime(1000));
  expect(screen.getByText('7s')).toBeTruthy();
  resumed.rerender(at({ ...timing, paused_ms: 60_000, paused_at: new Date(start + 67_000).toISOString() }));
  act(() => vi.advanceTimersByTime(30_000));
  expect(screen.getByText('7s')).toBeTruthy();
});

test('the line leads with the intent, else the turn\'s verb, with a still dot, and its one button reads the sentence once', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 125_000);
  const onJump = vi.fn();
  const items = [{ id: 'u1', kind: 'user', time: timing.started_at }] as Item[];
  const view = render(draw(task(), { items, onJump }));
  expect(screen.getByText(`${turnVerb('u1')}…`)).toBeTruthy();
  const button = screen.getByRole('button', { name: `${turnVerb('u1')}, 2 minutes. Jump to bottom` });
  // Nothing on the line moves: no ring and no breathing dot.
  expect(view.container.querySelector('.animate-spin, .animate-pulse-dot')).toBeNull();
  fireEvent.click(button);
  expect(onJump).toHaveBeenCalledTimes(1);
  view.rerender(draw(task({ turn_activity: { intent: 'Building the index page', todos: { known: false } } }), { items }));
  expect(screen.getByText('Building the index page…')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Building the index page, 2 minutes. Jump to bottom' })).toBeTruthy();
  // A retry of the main agent's call stands beside the clock.
  view.rerender(draw(task({ turn_activity: { intent: 'Building the index page', retry: { count: 1, status: 429, reason: 'rate_limited', at: timing.started_at }, todos: { known: false } } }), { items }));
  expect(screen.getByText('Retrying, attempt 2')).toBeTruthy();
  expect(screen.getByText('· HTTP 429 · rate limited')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Building the index page, 2 minutes. Retrying, attempt 2: HTTP 429, rate limited. Jump to bottom' })).toBeTruthy();
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

test('the line stays while a subagent outlives the turn, with the verb', () => {
  for (const state of ['idle', 'completed'] as const) {
    const line = (subagents_running: number) => statusLine(task({ state, subagents_running, turn_timings: [{ ...timing, ended_at: new Date(start + 9000).toISOString(), state: 'completed' }] }), shownState({ state, subagents_running }) === 'working', false, []);
    expect(line(0)).toBeNull();
    expect(line(1)).toEqual({ kind: 'working', lead: turnVerb('u1'), compacting: false, retry: undefined });
  }
});

test('a turn starting says nothing of the last one: no line until its timing comes, no verb until it links its message', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 20_000);
  const before = { id: 'before', user_item_id: 'u0', state: 'cancelled', started_at: new Date(start - 30_000).toISOString(), ended_at: new Date(start - 10_000).toISOString() } as TurnTiming;
  const old = [{ id: 'u0', kind: 'user', time: before.started_at }] as Item[];
  const next = { ...timing, user_item_id: undefined, started_at: new Date(start + 19_800).toISOString() };
  expect(turnVerb('u0')).not.toBe(turnVerb('u1'));
  const view = render(draw(task({ state: 'cancelled', stop_reason: 'owner', turn_timings: [before] }), { items: old }));
  expect(screen.getByRole('button', { name: 'Stopped. Jump to bottom' })).toBeTruthy();
  // The Task works before the turn's timing comes: the last turn's verb is not this one's.
  view.rerender(draw(task({ turn_timings: [before] }), { items: old }));
  expect(screen.queryByRole('button')).toBeNull();
  // Its timing came, its message not yet: "Working…" and the clock.
  view.rerender(draw(task({ turn_timings: [before, next] }), { items: old }));
  expect(screen.queryByText(`${turnVerb('u0')}…`)).toBeNull();
  expect(screen.getByText('Working…')).toBeTruthy();
  expect(screen.getByText('<1s')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Working, under a minute. Jump to bottom' })).toBeTruthy();
  // The message landed and the timing links it: its own verb.
  view.rerender(draw(task({ turn_timings: [before, { ...next, user_item_id: 'u1' }] }), { items: [...old, { id: 'u1', kind: 'user', time: next.started_at }] as Item[] }));
  expect(screen.getByText(`${turnVerb('u1')}…`)).toBeTruthy();
});

test('a turn that came without a message (Copilot going on after a background shell) reads "Working…", still, as its name says', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 5000);
  const before = { ...timing, id: 'before', state: 'completed', ended_at: timing.started_at } as TurnTiming;
  const view = render(draw(task({ turn_timings: [before, { ...timing, user_item_id: undefined }] }), { items: [{ id: 'u1', kind: 'user', time: timing.started_at }] as Item[] }));
  expect(screen.getByText('Working…')).toBeTruthy();
  expect(screen.getByText('5s')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Working, under a minute. Jump to bottom' })).toBeTruthy();
  expect(view.container.querySelector('.animate-spin, .animate-pulse-dot')).toBeNull();
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
  counts: { total: todos.length, done: todos.filter((t) => t.status === 'done').length, blocked: todos.filter((t) => t.status === 'blocked').length, open: todos.filter((t) => t.status === 'pending' || t.status === 'in_progress').length },
  now: 'b',
  ...extra,
});
const withTodos = (todos: TodoView, intent = 'Working on the site', extra: Partial<SessionDetail> = {}) => task({ turn_activity: { intent, todos }, subagents: [{ id: 'sub-1', name: 'Index page writer', status: 'running' } as Subagent], ...extra });

test('the todo segment: counts, meter, Now with how many more, blocked in words; Now gives way to an intent that says it', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 125_000);
  const view = render(draw(withTodos(listOf(rows()))));
  const button = screen.getByRole('button', { name: 'Working on the site, 2 minutes. Todo 1 of 4 done, 1 blocked. Now: Build the index page, and 1 more in progress. Show the list' });
  expect(button.getAttribute('aria-haspopup')).toBe('dialog');
  expect(button.getAttribute('aria-expanded')).toBe('false');
  expect(button.textContent).toContain('Todo 1/4');
  expect(screen.getByText('Build the index page')).toBeTruthy();
  expect(screen.getByText('+1')).toBeTruthy();
  // Blocked is a glyph and a word, never colour alone.
  expect(screen.getByText('1 blocked')).toBeTruthy();
  // The meter: one segment per row.
  expect(view.container.querySelectorAll('.h-1.w-3').length).toBe(4);
  expect(view.container.querySelector('.animate-spin, .animate-pulse-dot')).toBeNull();
  // Copilot's intent is the row in progress: it leads, and "Now:" goes; how many more are in progress follows the lead.
  view.rerender(draw(withTodos(listOf(rows()), 'Build the index page')));
  expect(screen.getByRole('button', { name: 'Build the index page, and 1 more in progress, 2 minutes. Todo 1 of 4 done, 1 blocked. Show the list' })).toBeTruthy();
  expect(screen.queryByText('Now:')).toBeNull();
  expect(screen.getByText('Build the index page…').nextElementSibling?.textContent).toBe('+1');
  // The only row in progress: nothing follows the lead.
  view.rerender(draw(withTodos(listOf(rows([{}, {}, { status: 'pending' }])), 'Build the index page')));
  expect(screen.getByRole('button', { name: 'Build the index page, 2 minutes. Todo 1 of 4 done, 1 blocked. Show the list' })).toBeTruthy();
  expect(screen.queryByText('+1')).toBeNull();
  // The last turn's list, not touched yet: its open rows, and the reader still opens.
  view.rerender(draw(withTodos(listOf(rows(), { touched: false }))));
  expect(screen.getByText('3 open from the last turn')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Working on the site, 2 minutes. Todo: 3 open from the last turn. Show the list' })).toBeTruthy();
  // No list: the line jumps to the bottom as before.
  view.rerender(draw(withTodos({ known: false, touched: false, todos: [], counts: {} })));
  expect(screen.getByRole('button', { name: 'Working on the site, 2 minutes. Jump to bottom' }).getAttribute('aria-haspopup')).toBeNull();
});

test('the reader opens on its heading beside the line, keeps focus inside, and Esc gives it back to the line', async () => {
  const user = userEvent.setup();
  render(draw(withTodos(listOf(rows()))));
  const line = screen.getByRole('button', { name: /Show the list$/ });
  await user.click(line);
  const reader = await screen.findByRole('dialog', { name: 'Todo' });
  expect(line.getAttribute('aria-expanded')).toBe('true');
  expect(line.getAttribute('aria-controls')).toBe(reader.id);
  await waitFor(() => expect(document.activeElement).toBe(within(reader).getByRole('heading', { name: 'Todo' })));
  // Sections with their rows; states and the subagent in words.
  expect(within(reader).getByRole('region', { name: 'Now' }).textContent).toBe('Now2Build the index page, by Index page writer, NowBuild the about page, Now');
  expect(within(reader).getByRole('region', { name: 'Blocked' }).textContent).toContain('No token on this machine');
  expect(within(reader).getByText('Esc')).toBeTruthy();
  expect(within(reader).getByText('To change it, ask in the chat')).toBeTruthy();
  // The page behind is out of reach: focus stays in the reader.
  for (let i = 0; i < 3; i++) {
    await user.tab();
    expect(reader.contains(document.activeElement)).toBe(true);
  }
  await user.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(document.activeElement).toBe(line);
  expect(line.getAttribute('aria-expanded')).toBe('false');
});

test('a phone gets the list as a sheet with a close button and no key hints', async () => {
  const happy = (window as unknown as { happyDOM: { setViewport: (v: { width: number; height: number }) => void } }).happyDOM;
  happy.setViewport({ width: 390, height: 844 });
  try {
    const user = userEvent.setup();
    render(draw(withTodos(listOf(rows()))));
    await user.click(screen.getByRole('button', { name: /Show the list$/ }));
    const sheet = await screen.findByRole('dialog', { name: 'Todo' });
    expect(within(sheet).queryByText('Esc')).toBeNull();
    expect(within(sheet).getByText('To change it, ask in the chat')).toBeTruthy();
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
  expect(region()).toBe('1 of 4 done');
  // A row blocked right after waits for the slot.
  view.rerender(draw(withTodos(listOf(rows([{ changed_at: at(7) }, {}, {}, { changed_at: at(8) }])))));
  act(() => vi.advanceTimersByTime(4000));
  expect(region()).toBe('');
  act(() => vi.advanceTimersByTime(1000));
  expect(region()).toBe('Blocked: Sign in to the registry');
});
