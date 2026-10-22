import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { StatusLine, statusLine } from '../../src/components/StatusLine';
import type { Item, SessionDetail, Subagent, Todo, TodoView, TurnTiming } from '../../src/api';
import { shownState } from '../../src/lib/tasks';

afterEach(() => vi.useRealTimers());

const start = Date.parse('2026-09-28T12:00:00Z');
const timing = { id: 'turn', user_item_id: 'u1', state: 'working', started_at: new Date(start).toISOString() } as TurnTiming;
const task = (extra: Partial<SessionDetail> = {}) => ({ id: 't', state: 'working', items: [], interactions: [], subagents: [], turn_timings: [timing], ...extra }) as SessionDetail;
const objective = { id: 1, objective: 'Notes', status: 'paused' as const, turn_count: 6, credits_used: 300, credit_limit: 300 };

function draw(session: SessionDetail, { working = session.state === 'working', since = undefined as string | undefined, onJump = () => {}, connected = true, jump = false } = {}) {
  return <StatusLine line={statusLine(session, working, !!session.compacting, connected)} session={session} since={since} hidden={false} jump={jump} onJump={onJump} />;
}

/** The progress line's sentence, as a screen reader hears it, or null with no line. */
const said = () => document.querySelector('.animate-fade-in + .sr-only')?.textContent ?? null;

test('a working turn with nothing of its own draws no strip, its time being the turn card\'s; the intent, a retry, the list each bring it back, without a clock', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 125_000);
  const onJump = vi.fn();
  const view = render(draw(task(), { onJump }));
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.queryByText('Working')).toBeNull();
  view.rerender(draw(task({ turn_activity: { intent: 'Building the index page', todos: { known: false } } }), { onJump }));
  expect(screen.getByText('Building the index page', { ignore: '.sr-only' })).toBeTruthy();
  // The intent is plain words, not a button: nothing to peek at, and the arrow is the way to the bottom.
  expect(said()).toBe('Building the index page');
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.queryByText('2m')).toBeNull();
  // Nothing on the line moves: no ring and no breathing dot.
  expect(view.container.querySelector('.animate-spin, .animate-pulse-dot')).toBeNull();
  view.rerender(draw(task({ turn_activity: { intent: 'Building the index page', todos: { known: false } } }), { onJump, jump: true }));
  fireEvent.click(screen.getByRole('button', { name: 'Jump to bottom' }));
  expect(onJump).toHaveBeenCalledTimes(1);
  // A retry of the main agent's call stands beside the intent.
  view.rerender(draw(task({ turn_activity: { intent: 'Building the index page', retry: { count: 1, status: 429, reason: 'rate_limited', at: timing.started_at }, todos: { known: false } } })));
  expect(screen.getByText('Retrying, attempt 2')).toBeTruthy();
  expect(screen.getByText('· HTTP 429 · rate limited')).toBeTruthy();
  expect(said()).toBe('Building the index page. Retrying, attempt 2: HTTP 429, rate limited');
});

test('the strip is three sections in order: the list, the progress line, then the arrow while the reader is away from the bottom', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 125_000);
  const running = task({ turn_activity: { intent: 'Working on the site', todos: listOf(rows()), phase: { kind: 'local', since: new Date(start + 90_000).toISOString(), tool: 'bash', detail: 'go test ./...' } }, items: [{ id: 'c', kind: 'tool', time: timing.started_at, tool: { name: 'bash', status: 'running', tail: [{ text: 'ok' }] } } as Item] });
  const view = render(draw(running));
  const names = () => screen.getAllByRole('button').map((b) => b.getAttribute('aria-label') ?? b.querySelector(':scope > .sr-only')!.textContent);
  expect(names()).toEqual(['Todo 1 of 4 done, 2 in progress, 1 blocked. Show the list', 'Running go test ./..., under a minute. Show the output']);
  view.rerender(draw(running, { jump: true }));
  expect(names()).toEqual(['Todo 1 of 4 done, 2 in progress, 1 blocked. Show the list', 'Running go test ./..., under a minute. Show the output', 'Jump to bottom']);
  // The arrow stands alone once the strip has nothing to show.
  view.rerender(draw(task({ state: 'idle' }), { jump: true }));
  expect(names()).toEqual(['Jump to bottom']);
});

test('subagents that outlived the turn keep "Working" and their own clock on the strip', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 125_000);
  render(draw(task(), { since: new Date(start + 5000).toISOString() }));
  expect(screen.getByText('Working')).toBeTruthy();
  expect(screen.getByText('2m')).toBeTruthy();
  expect(said()).toBe('Working, 2 minutes');
});

test('after a stop the line says why until the next turn; nothing to say, no line', () => {
  const view = render(draw(task({ state: 'cancelled', stop_reason: 'credit_limit', execution: { known: true, mode: 'autopilot', objective } })));
  expect(screen.getByText('Stopped: credit limit')).toBeTruthy();
  expect(screen.getByText('Autopilot stopped: credit limit reached')).toBeTruthy();
  expect(said()).toBe('Autopilot stopped: credit limit reached, 300 of 300 credits used, 6 turns');
  view.rerender(draw(task({ state: 'cancelled', stop_reason: 'owner' })));
  expect(said()).toBe('Stopped');
  view.rerender(draw(task({ state: 'completed', execution: { known: true, mode: 'autopilot', objective: { ...objective, pause_reason: 'Waiting for a token' } } })));
  expect(said()).toBe('Autopilot paused: Waiting for a token');
  for (const quiet of [task({ state: 'cancelled' }), task({ state: 'completed' }), task({ state: 'cancelled', stop_reason: 'owner', stage: 'settled' })]) {
    view.rerender(draw(quiet));
    expect(said()).toBeNull();
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
    expect(line(1)).toEqual({ kind: 'working', lead: '', compacting: false, retry: undefined, todo: undefined, live: null });
  }
});

test('a turn starting says nothing of the last one: no line until its timing comes, then still none while it has nothing of its own', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 20_000);
  const before = { id: 'before', user_item_id: 'u0', state: 'cancelled', started_at: new Date(start - 30_000).toISOString(), ended_at: new Date(start - 10_000).toISOString() } as TurnTiming;
  const next = { ...timing, user_item_id: undefined, started_at: new Date(start + 19_800).toISOString() };
  const view = render(draw(task({ state: 'cancelled', stop_reason: 'owner', turn_timings: [before] })));
  expect(said()).toBe('Stopped');
  // The Thread works before the turn's timing comes: nothing of the last turn stands for this one.
  view.rerender(draw(task({ turn_timings: [before] })));
  expect(said()).toBeNull();
  view.rerender(draw(task({ turn_timings: [before, next] })));
  expect(said()).toBeNull();
  expect(statusLine(task({ turn_timings: [before, next] }), true, false)).toEqual({ kind: 'working', lead: '', compacting: false, retry: undefined, todo: undefined, live: null });
});

test('a turn a restart or close cut off gives the next one no line while it starts; a dropped stream keeps the running turn\'s', () => {
  // The service marks such a turn unknown, without an end.
  const cut = { ...timing, user_item_id: 'u0', state: 'unknown' } as TurnTiming;
  // You reopen the Thread with a message: it starts, then works, before the new turn's timing comes.
  for (const state of ['starting', 'working'] as const) {
    const view = render(draw(task({ state, turn_timings: [cut] }), { working: true }));
    expect(said()).toBeNull();
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

test('native plan progress labels only the approved current touched list, and opens a Plan reader', async () => {
  const user = userEvent.setup();
  const current = (todos: TodoView) => withTodos(todos, 'Implementing the plan', { turn_activity: { plan: true, intent: 'Implementing the plan', todos } });
  const view = render(draw(current(listOf(rows(), { touched: false }))));
  expect(screen.queryByText('Plan')).toBeNull();
  expect(screen.getByText('From the last turn')).toBeTruthy();
  view.rerender(draw(current(listOf(rows()))));
  const button = screen.getByRole('button', { name: /Plan 1 of 4 done/ });
  await user.click(button);
  const reader = within(await screen.findByRole('dialog', { name: 'Plan' }));
  expect(reader.getByRole('heading', { name: 'Plan' })).toBeTruthy();
});

/** The blocks on the strip, as their glyph-less words: "1 done", "2 in progress"… in order. */
const blocks = (root: HTMLElement) => [...root.querySelectorAll('[title]')].map((b) => `${b.getAttribute('title')} ${b.className.match(/text-(accent|warning|muted|success|error)/)?.[1]} ${b.querySelector('svg') ? 'glyph' : 'no glyph'}`);

test('the todo list is stage blocks with their glyph and number and "1 of 4", then Now on the progress line; no meter, no counts in words, no made-up verb', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 125_000);
  const view = render(draw(withTodos(listOf(rows()))));
  const button = screen.getByRole('button', { name: 'Todo 1 of 4 done, 2 in progress, 1 blocked. Show the list' });
  expect(button.getAttribute('aria-haspopup')).toBe('dialog');
  expect(button.getAttribute('aria-expanded')).toBe('false');
  // ✓ done, ◉ in progress, ⊘ blocked: each its number behind its glyph in its tone; a stage with no rows is left out.
  expect(blocks(button)).toEqual(['1 done success glyph', '2 in progress accent glyph', '1 blocked warning glyph']);
  expect([...button.querySelectorAll('[title]')].map((b) => b.textContent?.replace(b.getAttribute('title')!, ''))).toEqual(['1', '2', '1']);
  // "1 of 4" on a wide screen, "1/4" on a phone.
  expect(within(button).getByText('1 of 4').className).toContain('max-sm:hidden');
  expect(within(button).getByText('1/4').className).toContain('sm:hidden');
  expect(screen.getByText('Now:').parentElement!.textContent).toBe('Now: Build the index page');
  expect(said()).toBe('Now: Build the index page');
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
  expect(screen.getByText('Working on the site', { ignore: '.sr-only' })).toBeTruthy();
  expect(screen.queryByText('Now:')).toBeNull();
  // The last turn's list, not touched yet: the same blocks, dimmed, "From the last turn", and the reader still opens.
  view.rerender(draw(withTodos(listOf(rows(), { touched: false }))));
  expect(screen.getByText('From the last turn')).toBeTruthy();
  const carried = screen.getByRole('button', { name: 'Todo from the last turn: 1 of 4 done, 2 in progress, 1 blocked. Show the list' });
  expect(carried.querySelector('.opacity-55')).not.toBeNull();
  expect(said()).toBe('Working on the site');
  // No list: no button, the intent alone.
  view.rerender(draw(withTodos({ known: false, touched: false, todos: [], counts: {} })));
  expect(screen.queryByRole('button')).toBeNull();
  expect(said()).toBe('Working on the site');
});

test('the todo button opens a reader of its own beside it: sections by state, the row the work is at marked now; Esc gives focus back', async () => {
  const user = userEvent.setup();
  render(draw(withTodos(listOf(rows()))));
  const line = screen.getByRole('button', { name: /Show the list$/ });
  await user.click(line);
  const reader = await screen.findByRole('dialog', { name: 'Todo' });
  expect(line.getAttribute('aria-expanded')).toBe('true');
  expect(line.getAttribute('aria-controls')).toBe(reader.id);
  const heading = within(reader).getByRole('heading', { name: 'Todo' });
  await waitFor(() => expect(document.activeElement).toBe(heading));
  // A reader, not the sheet: no tabs; the head counts the rows.
  expect(within(reader).queryByRole('tab')).toBeNull();
  expect(within(reader).getByText('1 done').parentElement!.textContent).toBe('1 done of 4');
  // Sections by state, in the stages' order; the row the work is at first and marked "now"; the blocked reason under its row; the subagent in words.
  expect(within(reader).getAllByRole('region').map((r) => r.getAttribute('aria-label'))).toEqual(['In progress', 'Blocked', 'Done']);
  const items = within(reader).getAllByRole('listitem').map((li) => li.textContent);
  expect(items).toEqual([
    'Build the index pagenow, by Index page writer, In progress',
    'Build the about page, In progress',
    'Sign in to the registryNo token on this machine, Blocked',
    'Plan the pages, Done',
  ]);
  expect(within(reader).getByText('now').className).toContain('text-accent');
  expect(within(reader).getByText('Esc')).toBeTruthy();
  expect(within(reader).getByText('Read only · to change it, ask in the chat')).toBeTruthy();
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

test('a phone gets the list as a sheet that repeats the blocks, with a close button and no key hints', async () => {
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
    expect(within(sheet).getByText('Read only · to change it, ask in the chat')).toBeTruthy();
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
