import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { StatusLine, statusLine, stripHasTodos } from '../../src/components/StatusLine';
import { ApiContext } from '../../src/ApiContext';
import { api, type Item, type SessionDetail, type Subagent, type Todo, type TodoHistory, type TodoView, type TurnTiming } from '../../src/api';
import { shownState } from '../../src/lib/tasks';

afterEach(() => vi.useRealTimers());

const start = Date.parse('2026-09-28T12:00:00Z');
const timing = { id: 'turn', user_item_id: 'u1', state: 'working', started_at: new Date(start).toISOString() } as TurnTiming;
const task = (extra: Partial<SessionDetail> = {}) => ({ id: 't', state: 'working', items: [], interactions: [], subagents: [], turn_timings: [timing], ...extra }) as SessionDetail;
const objective = { id: 1, objective: 'Notes', status: 'paused' as const, turn_count: 6, credits_used: 300, credit_limit: 300 };

/** The kept records, unread unless a test gives them: the readers keep their sections by stage then. */
const unread = () => Promise.reject(new Error('offline'));

function draw(session: SessionDetail, { working = session.state === 'working', since = undefined as string | undefined, onJump = () => {}, connected = true, jump = false, todoHistory = unread as (id: string, signal?: AbortSignal) => Promise<TodoHistory> } = {}) {
  return (
    <ApiContext.Provider value={{ ...api, todoHistory }}>
      <StatusLine line={statusLine(session, working, !!session.compacting, connected)} session={session} since={since} hidden={false} jump={jump} onJump={onJump} />
    </ApiContext.Provider>
  );
}

/** The progress line's sentence, as a screen reader hears it, or null with no line. */
const said = () => document.querySelector('[data-progress-words] + span')?.textContent ?? null;

test('a working turn with nothing of its own says "Working", never a blank strip; the intent, a retry, the list each take its place, without a clock', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 125_000);
  const onJump = vi.fn();
  const view = render(draw(task(), { onJump }));
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.getByText('Working', { ignore: '.sr-only' })).toBeTruthy();
  expect(said()).toBe('Working');
  view.rerender(draw(task({ turn_activity: { intent: 'Building the index page', todos: { known: false } } }), { onJump }));
  expect(screen.getByText('Building the index page', { ignore: '.sr-only' })).toBeTruthy();
  // The intent is plain words, not a button: nothing to peek at, and the arrow is the way to the bottom.
  expect(said()).toBe('Building the index page');
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.queryByText('2m')).toBeNull();
  // Nothing on the line moves: no ring and no breathing dot.
  expect(view.container.querySelector('[data-spinner]')).toBeNull();
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

test('a turn starting says nothing of the last one: "Working" alone until its timing comes, and while it has nothing of its own', () => {
  vi.useFakeTimers();
  vi.setSystemTime(start + 20_000);
  const before = { id: 'before', user_item_id: 'u0', state: 'cancelled', started_at: new Date(start - 30_000).toISOString(), ended_at: new Date(start - 10_000).toISOString() } as TurnTiming;
  const next = { ...timing, user_item_id: undefined, started_at: new Date(start + 19_800).toISOString() };
  const view = render(draw(task({ state: 'cancelled', stop_reason: 'owner', turn_timings: [before] })));
  expect(said()).toBe('Stopped');
  // The Thread works before the turn's timing comes: nothing of the last turn stands for this one.
  view.rerender(draw(task({ turn_timings: [before] })));
  expect(said()).toBe('Working');
  view.rerender(draw(task({ turn_timings: [before, next] })));
  expect(said()).toBe('Working');
  expect(statusLine(task({ turn_timings: [before, next] }), true, false)).toEqual({ kind: 'working', lead: '', compacting: false, retry: undefined, todo: undefined, live: null });
});

test('a turn a restart or close cut off gives the next one "Working" alone while it starts; a dropped stream keeps the running turn\'s', () => {
  // The service marks such a turn unknown, without an end.
  const cut = { ...timing, user_item_id: 'u0', state: 'unknown' } as TurnTiming;
  // You reopen the Thread with a message: it starts, then works, before the new turn's timing comes.
  for (const state of ['starting', 'working'] as const) {
    const view = render(draw(task({ state, turn_timings: [cut] }), { working: true }));
    expect(said()).toBe('Working');
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
const blocks = (root: HTMLElement) => [...root.querySelectorAll('[title]')].map((b) => `${b.getAttribute('title')} ${(b as HTMLElement).dataset.tone} ${b.querySelector('svg') ? 'glyph' : 'no glyph'}`);

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
  // "1 of 4" on a wide screen, "1/4" on a phone (css.test.mjs).
  expect(within(button).getByText('1 of 4')).toBeTruthy();
  expect(within(button).getByText('1/4')).toBeTruthy();
  expect(screen.getByText('Now:').parentElement!.textContent).toBe('Now: Build the index page');
  expect(said()).toBe('Now: Build the index page');
  // The intent is not on screen while a row is in progress, nor the counts in words, nor the meter.
  expect(screen.queryByText(/Working on the site/)).toBeNull();
  expect(screen.queryByText(/in progress$/, { ignore: '.sr-only' })).toBeNull();
  expect(view.container.querySelector('[data-todo-meter]')).toBeNull();
  expect(view.container.querySelector('[data-spinner]')).toBeNull();
  // A row to do adds its block.
  view.rerender(draw(withTodos(listOf(rows([{}, {}, { status: 'pending' }])))));
  expect(blocks(screen.getByRole('button'))).toEqual(['1 done success glyph', '1 in progress accent glyph', '1 blocked warning glyph', '1 to do none glyph']);
  // No row in progress: the agent's words take Now's place.
  view.rerender(draw(withTodos(listOf(rows([{}, { status: 'pending' }, { status: 'pending' }]), { now: undefined }))));
  expect(screen.getByText('Working on the site', { ignore: '.sr-only' })).toBeTruthy();
  expect(screen.queryByText('Now:')).toBeNull();
  // The last turn's list, not touched yet: the same blocks, dimmed (css.test.mjs), "From the last turn", and the reader still opens.
  view.rerender(draw(withTodos(listOf(rows(), { touched: false }))));
  expect(screen.getByText('From the last turn')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Todo from the last turn: 1 of 4 done, 2 in progress, 1 blocked. Show the list' })).toBeTruthy();
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
  expect(within(reader).getByText('now')).toBeTruthy();
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

test('open rows that wait on others are one list by stage with rails beside it, Done its own section; each row says what it waits on and how often it was reopened', async () => {
  const user = userEvent.setup();
  const todos = rows([{}, { depends_on: ['a'] }, { depends_on: ['a', 'd'], reopened: 2 }, { depends_on: ['c'] }]);
  render(draw(withTodos(listOf(todos))));
  await user.click(screen.getByRole('button', { name: /Show the list$/ }));
  const reader = await screen.findByRole('dialog', { name: 'Todo' });
  // The open rows by stage on rails; a dependency on a done row is met and draws no rail.
  expect(within(reader).getAllByRole('region').map((r) => r.getAttribute('aria-label'))).toEqual(['Done']);
  expect(within(reader).getAllByRole('listitem').map((li) => li.textContent)).toEqual([
    'Build the index pagenow, by Index page writer, In progress',
    'Build the about page2, reopened 2 times, In progress, waits on Sign in to the registry',
    'Sign in to the registryNo token on this machine, Blocked, waits on Build the about page',
    'Plan the pages, Done',
  ]);
  expect(within(reader).getByText(/1 loop/)).toBeTruthy();
  // Two links, both the loop (about ⇄ registry).
  const rails = reader.querySelectorAll('svg[aria-hidden] > g');
  expect(rails.length).toBe(2);
  expect([...rails].map((g) => g.getAttribute('data-kind'))).toEqual(['loop', 'loop']);
});

test('Done shows its newest rows first and only the newest few until Show all', async () => {
  const user = userEvent.setup();
  const done = Array.from({ length: 10 }, (_, i): Todo => ({ id: `d${i}`, title: `Done ${i}`, status: 'done' }));
  render(draw(withTodos(listOf([...done, ...rows().slice(1)]))));
  await user.click(screen.getByRole('button', { name: /Show the list$/ }));
  const reader = await screen.findByRole('dialog', { name: 'Todo' });
  const section = within(within(reader).getByRole('region', { name: 'Done' }));
  expect(section.getAllByRole('listitem').map((li) => li.textContent)).toEqual([9, 8, 7, 6, 5, 4, 3, 2].map((i) => `Done ${i}, Done`));
  await user.click(section.getByRole('button', { name: 'Show all 10' }));
  expect(section.getAllByRole('listitem')).toHaveLength(10);
});

test('with the Board setting the reader draws the rows on a board: turns across from the kept records, agents down, a card opening its row', async () => {
  localStorage.setItem('uam.todoGraph', 'graph');
  try {
    const user = userEvent.setup();
    const first = { id: 'turn-0', user_item_id: 'u0', state: 'completed', started_at: new Date(start - 600_000).toISOString(), ended_at: new Date(start - 300_000).toISOString() } as TurnTiming;
    const todos = rows([{}, { depends_on: ['a'] }, { depends_on: ['a', 'd'] }, { depends_on: ['c'] }]);
    const turns: TodoHistory['turns'] = [{ timing_id: 'turn-0', ended_at: first.ended_at!, intent: 'Planning the pages', todos: [todos[0], todos[1]], counts: {} }];
    const todoHistory = vi.fn(async () => ({ live: listOf(todos), turns }));
    render(draw(withTodos(listOf(todos), 'Working on the site', { turn_timings: [first, timing] }), { todoHistory }));
    await user.click(screen.getByRole('button', { name: /Show the list$/ }));
    const reader = await screen.findByRole('dialog', { name: 'Todo' });
    expect(todoHistory).toHaveBeenCalledTimes(1);
    const board = await within(reader).findByRole('group', { name: 'Todo board' });
    await waitFor(() => expect(within(board).getByText(`Turn 1 · ${clock(first.started_at)}`)).toBeTruthy());
    expect(within(board).getByText('“Planning the pages”')).toBeTruthy();
    expect(within(board).getByText(`Turn 2 · ${clock(timing.started_at)}`)).toBeTruthy();
    // The grid in reading order after the arrows and the corner: the turn heads, then per lane its name and one cell per turn.
    const cells = [...board.children].slice(2).map((c) => [...c.querySelectorAll('[data-card]')].map((card) => card.getAttribute('data-card')).join(',') || c.textContent);
    expect(cells).toEqual([`Turn 1 · ${clock(first.started_at)}“Planning the pages”`, `Turn 2 · ${clock(timing.started_at)}`, 'Main agent', 'a', 'c,d', 'Index page writersubagent', 'b', '']);
    expect(within(reader).getByText('2 turns · 4 links')).toBeTruthy();
    // No rails, no sections; each card names what it waits on.
    expect(within(reader).queryAllByRole('region')).toEqual([]);
    const card = within(board).getByRole('button', { name: /^Sign in to the registry/ });
    expect(card.textContent).toContain(', Blocked, waits on Build the about page');
    await user.click(card);
    expect(card.getAttribute('aria-expanded')).toBe('true');
    expect([...reader.querySelectorAll('dt')].map((dt) => dt.textContent)).toEqual(['Waits on', 'Unblocks', 'History']);
    // A link in the detail picks that card.
    await user.click(within(reader.querySelector('dd')!).getByRole('button', { name: /^Build the about page/ }));
    expect(within(board).getByRole('button', { name: /^Build the about page/ }).getAttribute('aria-expanded')).toBe('true');
  } finally {
    localStorage.removeItem('uam.todoGraph');
  }
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

const clock = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
const at = (m: number) => new Date(start - m * 60_000).toISOString();
const quiet = { id: 'quiet-turn', user_item_id: 'u1', state: 'completed', started_at: at(4), ended_at: at(3) } as TurnTiming;
const ended = { id: 'done-turn', user_item_id: 'u0', state: 'completed', started_at: at(10), ended_at: at(5), todo: { done: 1, total: 4, in_progress: 2, blocked: 1, open: 2 } } as TurnTiming;
/** The names of the strip's buttons, in order. */
const names = () => screen.getAllByRole('button').map((b) => b.getAttribute('aria-label') ?? b.querySelector(':scope > .sr-only')!.textContent);

test('idle, the list and its history stay on the strip as two buttons alone; the list opens as the last turn left it; hidden, the arrow stands alone', async () => {
  const user = userEvent.setup();
  // A later turn left the list alone, so no turn card states these counts.
  const idle = task({ state: 'completed', turn_timings: [ended, quiet], turn_activity: { todos: listOf(rows(), { touched: false }) } });
  expect(stripHasTodos(idle)).toBe(true);
  expect(stripHasTodos(task({ state: 'completed' }))).toBe(false);
  const view = render(draw(idle));
  expect(names()).toEqual(['Todo 1 of 4 done, 2 in progress, 1 blocked. Show the list', 'Todo history: 1 turn changed the list. Show the history']);
  // No progress line, and the blocks undimmed: the list is the Thread's, not a running turn's carry-over.
  expect(said()).toBeNull();
  const todo = screen.getByRole('button', { name: /^Todo 1 of 4 done.*Show the list$/ });
  expect(blocks(todo)).toEqual(['1 done success glyph', '2 in progress accent glyph', '1 blocked warning glyph']);
  // "1 turn" on a wide screen, "1" on a phone (css.test.mjs), behind the History glyph.
  const history = screen.getByRole('button', { name: /Show the history$/ });
  expect(history.getAttribute('aria-haspopup')).toBe('dialog');
  expect(within(history).getByText('1 turn')).toBeTruthy();
  expect(within(history).getByText('1')).toBeTruthy();
  // Away from the bottom the arrow joins the row.
  view.rerender(draw(idle, { jump: true }));
  expect(names()).toEqual(['Todo 1 of 4 done, 2 in progress, 1 blocked. Show the list', 'Todo history: 1 turn changed the list. Show the history', 'Jump to bottom']);
  // The live reader: the same sections, its facts line says when the last turn left it, and no row is "now".
  await user.click(todo);
  const reader = await screen.findByRole('dialog', { name: 'Todo' });
  expect(within(reader).getByText(`As the last turn left it, ${clock(ended.ended_at!)}`)).toBeTruthy();
  expect(within(reader).queryByText('now')).toBeNull();
  expect(within(reader).getAllByRole('region').map((r) => r.getAttribute('aria-label'))).toEqual(['In progress', 'Blocked', 'Done']);
  expect(within(reader).getByText('Read only · to change it, ask in the chat')).toBeTruthy();
  await user.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  // Hidden (editing a message), both buttons go with the rest; the arrow stays.
  view.rerender(<StatusLine line={null} session={idle} hidden jump onJump={() => {}} />);
  expect(names()).toEqual(['Jump to bottom']);
});

test('idle, a list the latest turn\'s card states with the same counts is not repeated on the strip; its history stays, and a working turn brings the list back', () => {
  const idle = task({ state: 'completed', turn_timings: [ended], turn_activity: { todos: listOf(rows(), { touched: false }) } });
  const view = render(draw(idle));
  expect(names()).toEqual(['Todo history: 1 turn changed the list. Show the history']);
  // The list changed since the turn ended: the strip shows the list as it is now.
  view.rerender(draw(task({ state: 'completed', turn_timings: [ended], turn_activity: { todos: listOf(rows([{}, { status: 'done' }]), { touched: false }) } })));
  expect(names()).toEqual(['Todo 2 of 4 done, 1 in progress, 1 blocked. Show the list', 'Todo history: 1 turn changed the list. Show the history']);
  // A turn that works has the list on its strip.
  view.rerender(draw(task({ turn_timings: [ended, timing], turn_activity: { todos: listOf(rows()) } })));
  expect(names()[0]).toBe('Todo 1 of 4 done, 2 in progress, 1 blocked. Show the list');
});

test('a closed Thread shows the last list uam kept; the history button opens the ledger: every row with its stamp, the removed ones marked', async () => {
  const user = userEvent.setup();
  const t1 = { id: 'turn-1', user_item_id: 'u1', state: 'completed', started_at: at(60), ended_at: at(50), todo: { done: 1, total: 2, pending: 1, open: 1 } } as TurnTiming;
  const t2 = { id: 'turn-2', user_item_id: 'u2', state: 'completed', started_at: at(40), ended_at: at(30), todo: { done: 1, total: 3, in_progress: 1, blocked: 1, open: 1 } } as TurnTiming;
  const live = listOf(
    [
      { id: 'a', title: 'Plan the pages', status: 'done', changed_at: at(55) },
      { id: 'b', title: 'Build the index page', status: 'in_progress', agent_id: 'sub-1', changed_at: at(35) },
      { id: 'd', title: 'Sign in to the registry', status: 'blocked', note: 'No token on this machine', changed_at: at(33) },
    ],
    { touched: false, now: undefined },
  );
  const history: TodoHistory = {
    live,
    turns: [
      { timing_id: 'turn-1', ended_at: at(50), counts: t1.todo!, todos: [{ id: 'a', title: 'Plan the pages', status: 'done', changed_at: at(55) }, { id: 'b', title: 'Build the index page', status: 'pending' }] },
      {
        timing_id: 'turn-2',
        ended_at: at(30),
        counts: t2.todo!,
        todos: [
          { id: 'd', title: 'Sign in to the registry', status: 'blocked', note: 'No token on this machine', changed_at: at(33) },
          { id: 'b', title: 'Build the index page', status: 'in_progress', agent_id: 'sub-1', agent: 'Index page writer', changed_at: at(35) },
          { id: 'c', title: 'Write the README', status: 'done', changed_at: at(34) },
        ],
      },
    ],
  };
  const todoHistory = vi.fn(async () => history);
  const closed = task({ state: 'completed', turn_timings: [t1, t2, { id: 'turn-3', user_item_id: 'u3', state: 'completed', started_at: at(20), ended_at: at(10) } as TurnTiming], turn_activity: { todos: { known: false, touched: false, todos: [], counts: {} } } });
  render(draw(closed, { todoHistory }));
  // With no conversation open the list is not known: the strip reads the last one uam kept, once.
  await screen.findByRole('button', { name: 'Todo 1 of 3 done, 1 in progress, 1 blocked. Show the list' });
  expect(todoHistory).toHaveBeenCalledWith('t', expect.any(AbortSignal));
  expect(todoHistory).toHaveBeenCalledTimes(1);
  const button = screen.getByRole('button', { name: 'Todo history: 2 turns changed the list. Show the history' });
  await user.click(button);
  const reader = await screen.findByRole('dialog', { name: 'Todo history' });
  expect(button.getAttribute('aria-expanded')).toBe('true');
  expect(button.getAttribute('aria-controls')).toBe(reader.id);
  const heading = within(reader).getByRole('heading', { name: 'Todo history' });
  await waitFor(() => expect(document.activeElement).toBe(heading));
  // Opening reads the records afresh; the head counts the rows and turns.
  expect(todoHistory).toHaveBeenCalledTimes(2);
  expect(await within(reader).findByText('4 rows · 2 turns')).toBeTruthy();
  expect(within(reader).getByText('Every row the agents kept, with when it changed; rows they removed stay here')).toBeTruthy();
  // Sections by the turn that added each row, newest first, each with its tally; rows by stage in a section.
  const regions = within(reader).getAllByRole('region');
  expect(regions.map((r) => r.getAttribute('aria-label'))).toEqual([`Turn 2 · ${clock(at(40))}`, `Turn 1 · ${clock(at(60))}`]);
  expect(within(regions[0]).getByText('1 done · 1 blocked')).toBeTruthy();
  expect(within(regions[1]).getByText('1 done · 1 open')).toBeTruthy();
  // Each row's stamp under it: its state, when, which turn; newest change first in a stage; a row the live list lacks says so.
  expect(within(reader).getAllByRole('listitem').map((li) => li.textContent)).toEqual([
    `Sign in to the registryNo token on this machineblocked ${clock(at(33))} · turn 2, Blocked`,
    `Write the READMEdone ${clock(at(34))} · turn 2 · removed from the list since, Done`,
    `Build the index pagein progress since ${clock(at(35))} · turn 2, by Index page writer, In progress`,
    `Plan the pagesdone ${clock(at(55))} · turn 1, Done`,
  ]);
  // A row's title opens its detail: its history from the records, one row at a time.
  const index = within(reader).getByRole('button', { name: /^Build the index page/ });
  await user.click(index);
  expect(index.getAttribute('aria-expanded')).toBe('true');
  expect(index.closest('li')!.querySelector('dd')!.textContent).toBe(`added ${clock(at(50))} · in progress ${clock(at(35))}`);
  await user.click(within(reader).getByRole('button', { name: /^Plan the pages/ }));
  expect(index.getAttribute('aria-expanded')).toBe('false');
  expect(within(reader).getByText('· removed from the list since')).toBeTruthy();
  expect(within(reader).getByText('Kept by uam when each turn ended')).toBeTruthy();
  expect(within(reader).getByText('Esc')).toBeTruthy();
  await user.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(document.activeElement).toBe(button);
  expect(button.getAttribute('aria-expanded')).toBe('false');
  // A read that fails says why in the facts line.
  todoHistory.mockRejectedValueOnce(new Error('the service is away'));
  await user.click(button);
  expect(await screen.findByText('the service is away')).toBeTruthy();
});

test('with the kept records the live reader groups rows by the turn that added them; a turn all done folds to one line that opens', async () => {
  const user = userEvent.setup();
  const first = { id: 'turn-1', user_item_id: 'u0', state: 'completed', started_at: at(30), ended_at: at(20), todo: { done: 1, total: 1 } } as TurnTiming;
  const todos = rows();
  const todoHistory = vi.fn(async (): Promise<TodoHistory> => ({ live: null, turns: [{ timing_id: 'turn-1', ended_at: at(20), intent: 'Planning the pages', todos: [todos[0]], counts: {} }] }));
  render(draw(withTodos(listOf(todos), 'Working on the site', { turn_timings: [first, timing] }), { todoHistory }));
  await user.click(screen.getByRole('button', { name: /Show the list$/ }));
  const reader = await screen.findByRole('dialog', { name: 'Todo' });
  await waitFor(() => expect(within(reader).getAllByRole('region').map((r) => r.getAttribute('aria-label'))).toEqual([`Turn 2 · ${clock(timing.started_at)}`, `Turn 1 · ${clock(first.started_at)}`]));
  expect(within(reader).getByText('2 turns', { selector: 'p' })).toBeTruthy();
  // The running turn's rows: no record holds them yet.
  const latest = within(reader).getAllByRole('region')[0];
  expect(within(latest).getByText('2 open · 1 blocked')).toBeTruthy();
  expect(within(latest).getAllByRole('listitem').map((li) => li.getAttribute('data-row'))).toEqual(['b', 'c', 'd']);
  // Turn 1 is all done: one line with its tally and the record's intent, its rows hidden until it opens.
  const folded = within(reader).getByRole('button', { name: /^Turn 1/ });
  expect(folded.getAttribute('aria-expanded')).toBe('false');
  expect(folded.textContent).toBe(`Turn 1 · ${clock(first.started_at)}1 done“Planning the pages”`);
  expect(within(reader).queryByText('Plan the pages')).toBeNull();
  await user.click(folded);
  expect(folded.getAttribute('aria-expanded')).toBe('true');
  expect(within(reader).getByText('Plan the pages')).toBeTruthy();
});
