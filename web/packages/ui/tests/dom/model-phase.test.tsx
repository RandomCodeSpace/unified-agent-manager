// When the LLM is engaged and when work runs locally: the open Thread's phase as the strip's live line (r13 A), and
// the counts of model calls in flight across the service (the sidebar's Usage and AI button shows them:
// usage-ai.test.tsx). Mock: t1 runs a tool locally, t8 waits on the model with one subagent, t6 waits on you, t7
// compacts, and t22's audits run on after its turn ended.
import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { Item, Phase, SessionDetail, SnapshotData } from '../../src/api';
import { LIVE_HOLD_MS, StatusLine, WAIT_STILL_MS, liveClock, liveLine, statusLine } from '../../src/components/StatusLine';
import { initialState, reducer } from '../../src/state';
import { openTask } from './render';

afterEach(() => vi.useRealTimers());

const NOW = Date.parse('2026-10-17T12:00:00Z');
const ago = (s: number) => new Date(NOW - s * 1000).toISOString();
const task = (phase: Phase | undefined, extra: Partial<SessionDetail> = {}) =>
  ({ id: 't', state: 'working', items: [], interactions: [], subagents: [], turn_activity: { todos: { known: false }, phase }, ...extra }) as unknown as SessionDetail;
const tool = (id: string, name: string, status = 'running', extra: Partial<Item['tool']> = {}) => ({ id, kind: 'tool', time: ago(3), tool: { name, status, ...extra } }) as unknown as Item;

/** The strip's live line as it draws: its words, and what moves (`breathe`, `spin` or nothing). */
function strip(session: SessionDetail) {
  const view = render(<StatusLine line={statusLine(session, session.state === 'working', !!session.compacting)} session={session} hidden={false} onJump={() => {}} />);
  const moving = view.container.querySelectorAll('.animate-breathe, .animate-spin');
  const shown = view.container.querySelector('button > span[aria-hidden]')?.textContent ?? '';
  return { view, shown, moves: [...moving].map((n) => (n.className.includes('animate-breathe') ? 'breathe' : 'spin')) };
}

describe('the live line at the strip', () => {
  test('each phase in words, with one mark: model work breathes, local work turns, you and subagents are still', () => {
    expect(liveLine(task({ kind: 'model', since: ago(12), calls: 1 }, { items: [{ id: 'r', kind: 'reasoning', time: ago(10) } as Item] }), false)).toEqual({ mark: 'dot', phase: 'Thinking', since: ago(12) });
    const writing = task({ kind: 'model', since: ago(5), calls: 1 }, { items: [{ id: 'a', kind: 'assistant', time: ago(2) } as Item], turn_timings: [{ id: 'x', state: 'working', started_at: ago(60), output_tokens: 520, generation_ms: 10_000 }] as SessionDetail['turn_timings'] });
    expect(liveLine(writing, false)).toEqual({ mark: 'dot', phase: 'Writing the reply', detail: '52 tok/s' });
    // A newest item from before this call began is the last call's: nothing back yet is a wait.
    expect(liveLine(task({ kind: 'model', since: ago(64), calls: 1 }, { items: [{ id: 'a', kind: 'assistant', time: ago(90) } as Item] }), false)).toEqual({ mark: 'wait', phase: 'Waiting for the model', since: ago(64) });
    const command = task({ kind: 'local', since: ago(41), tool: 'bash', detail: 'go test ./internal/vterm/...' }, { items: [tool('c', 'bash', 'running', { tail: [{ text: 'ok' }, { text: '=== RUN   TestRedrawReplaysFocusEvents' }, { text: '  ' }] })] });
    expect(liveLine(command, false)).toEqual({ mark: 'ring', phase: 'Running go test ./internal/vterm/...', since: ago(41), tail: '=== RUN   TestRedrawReplaysFocusEvents' });
    const reads = task({ kind: 'local', since: ago(2), tool: 'view', detail: 'redraw_test.go' }, { items: [tool('1', 'view'), tool('2', 'view'), tool('3', 'view', 'pending')] });
    expect(liveLine(reads, false)?.phase).toBe('Reading 3 files');
    expect(liveLine(task({ kind: 'local', since: ago(2), tool: 'view', detail: 'redraw_test.go' }, { items: [tool('1', 'view')] }), false)?.phase).toBe('Reading redraw_test.go');
    const you = task({ kind: 'waiting', since: ago(72), detail: 'approve bash' }, { state: 'awaiting_permission', interactions: [{ id: 'p', kind: 'permission', title: 'Run shell command', detail: 'rm -rf build/', state: 'pending', time: ago(72) }] as SessionDetail['interactions'] });
    expect(liveLine(you, false)).toEqual({ mark: 'you', phase: 'Needs you', detail: 'allow rm -rf build/', since: ago(72) });
    const agents = task({ kind: 'model', since: ago(5), calls: 3, subagents: 3 }, { subagents: [{ id: 'a', status: 'running' }, { id: 'b', status: 'running' }, { id: 'c', status: 'running' }, { id: 'd', status: 'completed' }] as SessionDetail['subagents'] });
    expect(liveLine(agents, false)).toEqual({ mark: 'subagents', phase: 'Waiting on 3 subagents' });
    expect(liveLine(task({ kind: 'model', by: 'compaction', since: ago(18), calls: 1 }), false)).toEqual({ mark: 'dot', phase: 'Compacting the conversation', since: ago(18) });
    // Nothing holds: no live line, so the slot's "Now:" or the intent comes back.
    expect(liveLine(task(undefined), false)).toBeNull();
  });

  test('the line draws [mark] phase · detail · elapsed with exactly one moving mark, or none', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    const command = strip(task({ kind: 'local', since: ago(41), tool: 'bash', detail: 'go test ./...' }, { items: [tool('c', 'bash', 'running', { tail: [{ text: 'PASS' }] })] }));
    expect(command.shown).toBe('Running go test ./...·41sPASS');
    expect(command.moves).toEqual(['spin']);
    await act(async () => { vi.advanceTimersByTime(1000); });
    expect(command.view.container.textContent).toContain('42s');
    // The output line is quieter and gives way first; a phone hides it.
    expect(screen.getByText('PASS').className).toEqual(expect.stringContaining('max-sm:hidden'));
    command.view.unmount();
    vi.setSystemTime(NOW);
    const thinking = strip(task({ kind: 'model', since: ago(12), calls: 1 }, { items: [{ id: 'r', kind: 'reasoning', time: ago(10) } as Item] }));
    expect(thinking.shown).toBe('Thinking·12s');
    expect(thinking.moves).toEqual(['breathe']);
    thinking.view.unmount();
    const you = strip(task({ kind: 'waiting', since: ago(72), detail: 'approve bash' }, { state: 'awaiting_permission' }));
    expect(you.shown).toBe('Needs you·approve bash·1m 12s');
    expect(you.moves).toEqual([]);
    expect(screen.getByText('Needs you').closest('.text-attention')).not.toBeNull();
    you.view.unmount();
  });

  test('a model call silent past the wait threshold stops breathing: a still clock in muted words', () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    const early = strip(task({ kind: 'model', since: ago(WAIT_STILL_MS / 1000 - 5), calls: 1 }));
    expect(early.moves).toEqual(['breathe']);
    early.view.unmount();
    const late = strip(task({ kind: 'model', since: ago(64), calls: 1 }));
    expect(late.shown).toBe('Waiting for the model·1m 04s');
    expect(late.moves).toEqual([]);
    expect(late.view.container.querySelector('.lucide-clock')).not.toBeNull();
    expect(screen.getByText('Waiting for the model').className).toContain('text-muted');
  });

  test('a subagent\'s tool mid-turn (a plain local phase) is not the main agent\'s: with only its task call running, the main agent waits on it', () => {
    const subagents = [{ id: 'a', status: 'running' }] as SessionDetail['subagents'];
    const session = task({ kind: 'local', since: ago(41), tool: 'bash', detail: 'go test ./...' }, { subagents, items: [tool('t', 'task', 'running'), { ...tool('c', 'bash'), agent_id: 'a' } as Item] });
    expect(liveLine(session, false)).toEqual({ mark: 'subagents', phase: 'Waiting on 1 subagent' });
    // A running call of the main agent's own is its work.
    expect(liveLine({ ...session, items: [...session.items, tool('m', 'bash')] }, false)?.phase).toBe('Running go test ./...');
  });

  test('the output line is the oldest running command\'s, the one the phase names', () => {
    const session = task({ kind: 'local', since: ago(41), tool: 'bash', detail: 'go test ./...' }, { items: [tool('old', 'bash', 'running', { tail: [{ text: 'ok  pkg/a' }] }), tool('new', 'bash', 'running', { tail: [{ text: 'newer' }] })] });
    expect(liveLine(session, false)?.tail).toBe('ok  pkg/a');
  });

  test('the strip\'s name counts the live line\'s own time, as it shows, not the outlived subagents\'', () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    const session = task({ kind: 'waiting', since: ago(72), detail: 'approve bash' }, { state: 'awaiting_permission' });
    render(<StatusLine line={statusLine(session, false, false)} session={session} since={ago(540)} hidden={false} onJump={() => {}} />);
    expect(screen.getByRole('button', { name: 'Needs you, approve bash, 1 minute. Jump to bottom' })).toBeTruthy();
    expect(screen.getByText('1m 12s')).toBeTruthy();
  });

  test('between a tool\'s end and the next model call the last live line holds a moment, so the strip does not blink', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    const draw = (s: SessionDetail) => <StatusLine line={statusLine(s, true, false)} session={s} hidden={false} onJump={() => {}} />;
    const running = task({ kind: 'local', since: ago(5), tool: 'bash', detail: 'go test ./...' });
    const view = render(draw(running));
    expect(screen.getByRole('button', { name: /^Running go test \.\/\.\.\., / })).toBeTruthy();
    // The tool ended; no model call yet: nothing is live, and the turn has nothing else to say.
    view.rerender(draw(task(undefined)));
    expect(screen.getByRole('button', { name: /^Running go test \.\/\.\.\., / })).toBeTruthy();
    await act(async () => { vi.advanceTimersByTime(LIVE_HOLD_MS - 100); });
    expect(screen.queryByRole('button')).not.toBeNull();
    // The next call comes within the hold: the line moves on without the strip leaving.
    view.rerender(draw(task({ kind: 'model', since: ago(0), calls: 1 })));
    expect(screen.getByRole('button', { name: /^Waiting for the model, / })).toBeTruthy();
    // Nothing comes: once the hold passes, the bare strip goes.
    view.rerender(draw(task(undefined)));
    await act(async () => { vi.advanceTimersByTime(LIVE_HOLD_MS); });
    expect(screen.queryByRole('button')).toBeNull();
    // A turn that ended drops it at once.
    view.rerender(draw(running));
    view.rerender(<StatusLine line={null} session={running} hidden={false} onJump={() => {}} />);
    expect(screen.queryByRole('button')).toBeNull();
  });

  test('the clock reads seconds, then minutes and seconds, then hours and minutes', () => {
    expect([liveClock(0), liveClock(41_999), liveClock(64_000), liveClock(605_000), liveClock(3_729_000), liveClock(-5)]).toEqual(['0s', '41s', '1m 04s', '10m 05s', '1h 02m', '0s']);
  });

  test('the footer under the composer has no phase block any more: Files, the branch, then the time split', async () => {
    await openTask('t1');
    await waitFor(() => expect(document.querySelector('[data-composer-foot] [aria-label^="Time in this Thread"]')).not.toBeNull());
    expect(document.querySelector('[data-phase]')).toBeNull();
    const foot = document.querySelector('[data-composer-foot]')!;
    expect(foot.textContent).not.toContain('Current branch');
    expect(foot.textContent).not.toContain('Local');
    expect(foot.children[0].textContent).toBe('Files');
    const place = foot.querySelector('[data-place]')!;
    expect(place.className.split(' ')).not.toContain('flex-1');
    expect(place.className.split(' ')).toContain('min-w-0');
  });

  test.each([
    ['t1', /^Running go test \.\/internal\/vterm\/\.\.\. -run Redraw -count=1, /],
    ['t8', /^Waiting for the model, /],
    ['t6', /^Needs you, allow chmod 0644/],
    ['t7', /^Compacting the conversation, /],
    ['t22', /^Waiting on 4 subagents, /],
  ])('mock Thread %s says its own phase on the strip', async (id, words) => {
    await openTask(id);
    await waitFor(() => expect(screen.getByRole('button', { name: words })).toBeTruthy());
  });
});

describe('the model calls in flight', () => {
  test('follows the snapshot and the model_calls frames', () => {
    const snap = { seq: 1, projects: [], sessions: [], session: null, model_calls: { threads: 1, subagents: 0, background: 0, local: 0 } } as unknown as SnapshotData;
    let state = reducer(initialState, { type: 'snapshot', data: snap });
    expect(state.modelCalls).toEqual({ threads: 1, subagents: 0, background: 0, local: 0 });
    state = reducer(state, { type: 'update', data: { name: 'model_calls', seq: 2, model_calls: { threads: 0, subagents: 0, background: 1, local: 2 } } });
    expect(state.modelCalls).toEqual({ threads: 0, subagents: 0, background: 1, local: 2 });
    // An older service's snapshot carries none.
    expect(reducer(state, { type: 'snapshot', data: { ...snap, seq: 3, model_calls: undefined } }).modelCalls).toBeNull();
  });
});
