import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { StatusLine, statusLine } from '../../src/components/StatusLine';
import type { Item, SessionDetail, TurnTiming } from '../../src/api';
import { shownState } from '../../src/lib/tasks';
import { turnVerb } from '../../src/lib/verbs';

afterEach(() => vi.useRealTimers());

const start = Date.parse('2026-09-28T12:00:00Z');
const timing = { id: 'turn', state: 'working', started_at: new Date(start).toISOString() } as TurnTiming;
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
    const line = (subagents_running: number) => statusLine(task({ state, subagents_running }), shownState({ state, subagents_running }) === 'working', false, []);
    expect(line(0)).toBeNull();
    expect(line(1)).toEqual({ kind: 'working', lead: turnVerb('start'), compacting: false, retry: undefined });
  }
});
