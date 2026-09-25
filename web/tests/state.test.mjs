import assert from 'node:assert/strict';
import test from 'node:test';
import { initialState, reducer } from '../src/state.ts';

const running = { id: 'helper', name: 'Helper', status: 'running' };
const completed = { ...running, status: 'completed', ended_at: '2026-09-24T12:00:00Z' };
const item = (text) => ({ id: 'reply', agent_id: 'helper', kind: 'assistant', text, time: '2026-09-24T11:59:00Z' });
const tool = (status) => ({ id: 'tool', agent_id: 'helper', kind: 'tool', tool: { name: 'read', status }, time: '2026-09-24T11:59:00Z' });
const delta = (seq, text) => ({ name: 'delta', seq, session_id: 'task', agent_id: 'helper', item_id: 'reply', kind: 'assistant', text });
const update = (state, data) => reducer(state, { type: 'update', data });
const loaded = (state, seq, items, subagent = running) => reducer(state, { type: 'agent_loaded', agentId: 'helper', seq, items, subagent });

test('usage follows snapshots and frames while retaining explicit stale state', () => {
  const usage = { quotas: [{ provider: 'copilot', remaining_percent: 88 }], stale: false };
  let state = reducer(initialState, { type: 'snapshot', data: { seq: 10, projects: [], sessions: [], session: null, usage } });
  assert.deepEqual(state.usage, usage);
  state = update(state, { name: 'usage', seq: 9, usage: { quotas: [], stale: true } });
  assert.deepEqual(state.usage, usage);
  state = update(state, { name: 'usage', seq: 11, usage: { ...usage, stale: true } });
  assert.equal(state.usage.stale, true);
  assert.deepEqual(state.usage.quotas, usage.quotas);
  state = reducer(state, { type: 'snapshot', data: { seq: 12, projects: [], sessions: [], session: null } });
  assert.equal(state.usage, null);
});

test('a summary without usage clears the previous task estimate', () => {
  let state = { ...initialState, selectedId: 'task', detail: { id: 'task', usage: { ai_units: 3 }, context: { used: 31, limit: 200 } } };
  state = reducer(state, { type: 'upsert_session', session: { id: 'task', state: 'closed' } });
  assert.equal(state.detail.usage, undefined);
  assert.equal(state.detail.context, undefined);
});

function loading() {
  let state = reducer(initialState, { type: 'select', id: 'task' });
  state = reducer(state, {
    type: 'snapshot',
    data: { seq: 10, projects: [], sessions: [], session: { id: 'task', items: [], interactions: [], subagents: [running] } },
  });
  return reducer(state, { type: 'agent_loading', agentId: 'helper' });
}

test('subagent fetch replays only the part of buffered output newer than its snapshot', () => {
  let state = update(loading(), delta(11, 'a'));
  state = update(state, delta(12, 'b'));
  state = loaded(state, 11, [item('a')]);
  assert.equal(state.agents.helper.items[0].text, 'ab');
  assert.deepEqual(state.detail.items, []);
});

test('subagent fetch keeps later tool replacements and the order of text replacements and deltas', () => {
  let state = update(loading(), { name: 'item', seq: 12, session_id: 'task', agent_id: 'helper', item: tool('completed') });
  state = update(state, delta(13, 'draft'));
  state = update(state, { name: 'item', seq: 14, session_id: 'task', agent_id: 'helper', item: item('final') });
  state = update(state, delta(15, '!'));
  state = loaded(state, 11, [tool('running'), item('old')]);
  assert.equal(state.agents.helper.items[0].tool.status, 'completed');
  assert.equal(state.agents.helper.items[1].text, 'final!');
});

test('subagent completion received while fetching survives the older running response', () => {
  let state = update(loading(), { name: 'subagent', seq: 12, session_id: 'task', subagent: completed });
  state = loaded(state, 11, []);
  assert.deepEqual(state.detail.subagents, [completed]);
});

test('subagent snapshot ignores captured SSE frames delivered after the response', () => {
  let state = loaded(loading(), 15, [item('a'), tool('completed')], completed);
  state = update(state, { name: 'subagent', seq: 11, session_id: 'task', subagent: running });
  state = update(state, { name: 'item', seq: 12, session_id: 'task', agent_id: 'helper', item: tool('running') });
  state = update(state, delta(13, 'a'));
  assert.equal(state.agents.helper.items[0].text, 'a');
  assert.equal(state.agents.helper.items[1].tool.status, 'completed');
  assert.deepEqual(state.detail.subagents, [completed]);
  state = update(state, { name: 'item', seq: 14, session_id: 'task', agent_id: 'helper', item: tool('completed') });
  state = update(state, { name: 'subagent', seq: 15, session_id: 'task', subagent: completed });
  state = update(state, delta(16, 'a'));
  assert.equal(state.agents.helper.items[0].text, 'aa');
  assert.equal(state.agents.helper.items[1].tool.status, 'completed');
  assert.deepEqual(state.detail.subagents, [completed]);
});

test('later repeated delta text is appended even when it matches the snapshot suffix', () => {
  let state = update(loading(), delta(12, 'a'));
  state = loaded(state, 11, [item('a')]);
  assert.equal(state.agents.helper.items[0].text, 'aa');
});

const history = (seq, items, extra = {}) => ({ name: 'history', seq, session_id: 'task', history: 'loaded', history_truncated: false, items, subagents: [completed], ...extra });

test('read-only history replaces the transcript and invalidates subagent fetches without changing stage', () => {
  let state = loading();
  state = { ...state, detail: { ...state.detail, stage: 'archived', open: false, history: 'loading' } };
  state = update(state, history(12, [item('recorded')], { history_truncated: true }));
  assert.equal(state.detail.items[0].text, 'recorded');
  assert.equal(state.detail.history, 'loaded');
  assert.equal(state.detail.history_truncated, true);
  assert.equal(state.detail.stage, 'archived');
  assert.equal(state.detail.open, false);
  assert.equal(state.detail.seq, 12);
  assert.deepEqual(state.agents, {});
});

test('history frames do not replace newer output or a different selected task', () => {
  let state = update(loading(), { name: 'item', seq: 14, session_id: 'task', item: { ...item('new'), agent_id: undefined } });
  state = update(state, history(12, [item('old')]));
  assert.equal(state.detail.items[0].text, 'new');
  state = update(state, history(15, [], { session_id: 'other' }));
  assert.equal(state.detail.items[0].text, 'new');
});

test('history reload ignores stale detail, and ignores SSE already captured by a newer detail', () => {
  let state = update(loading(), history(12, [item('recorded')]));
  const reload = (seq, text) => ({ type: 'detail_loaded', detail: { ...state.detail, seq, items: [item(text)] } });
  state = reducer(state, reload(11, 'stale'));
  assert.equal(state.detail.items[0].text, 'recorded');
  state = reducer(state, reload(15, 'newer'));
  assert.equal(state.sessions.find((s) => s.id === 'task').id, state.detail.id);
  state = update(state, history(14, [item('old')]));
  assert.equal(state.detail.items[0].text, 'newer');
  state = reducer(state, { type: 'select', id: 'other' });
  state = reducer(state, { type: 'detail_loaded', detail: { id: 'task', seq: 99, items: [] } });
  assert.equal(state.detail, null);
});

test('failed history remains a quiet status and a successful load clears the reason', () => {
  let state = update(loading(), history(12, [], { history: 'unavailable', history_reason: 'Provider offline' }));
  assert.equal(state.detail.history_reason, 'Provider offline');
  assert.deepEqual(state.detail.items, []);
  state = update(state, history(13, [item('restored')]));
  assert.equal(state.detail.history_reason, undefined);
  assert.equal(state.detail.items[0].text, 'restored');
});

test('background shell updates stay separate from foreground and reconnect invalidates live status', () => {
  const background_tasks = { known: true, tasks: [{ id: 'server', command: 'python3 -m http.server 8000', status: 'running' }] };
  let state = loading();
  state = update(state, { name: 'session', seq: 11, session: { id: 'task', state: 'completed' } });
  state = update(state, { name: 'background_tasks', seq: 12, session_id: 'task', background_tasks });
  assert.deepEqual(state.detail.background_tasks, background_tasks);
  assert.equal(state.detail.state, 'completed');
  assert.deepEqual(state.detail.subagents, [running]);
  assert.equal(update(state, { name: 'background_tasks', seq: 13, session_id: 'other', background_tasks: { known: true, tasks: [] } }), state);
  state = reducer(state, { type: 'connection', status: 'reconnecting' });
  assert.equal(state.detail.background_tasks.known, false);
  assert.deepEqual(state.detail.background_tasks.tasks, background_tasks.tasks);
  state = reducer(state, { type: 'snapshot', data: { seq: 14, projects: [], sessions: [], session: { ...state.detail, background_tasks } } });
  assert.equal(state.detail.background_tasks.known, true);
  assert.equal(update(state, { name: 'background_tasks', seq: 13, session_id: 'task', background_tasks: { known: false, tasks: [] } }), state);
  state = update(state, { name: 'background_tasks', seq: 15, session_id: 'task', background_tasks: { known: true, tasks: [] } });
  assert.deepEqual(state.detail.background_tasks.tasks, []);
});


test('execution snapshots clear on null and become unknown on disconnect', () => {
  const execution = { known: true, mode: 'autopilot', objective: { id: 1, objective: 'A real objective', status: 'active', turn_count: 2 } };
  let state = { ...initialState, selectedId: 'task', connection: 'connected', detail: { id: 'task', execution } };
  state = reducer(state, { type: 'connection', status: 'reconnecting' });
  assert.equal(state.detail.execution.known, false);
  assert.equal(state.detail.execution.objective.status, 'active');
  state = reducer(state, { type: 'upsert_session', session: { id: 'task', execution: null } });
  assert.equal(state.detail.execution, null);
});

test('foreground timing follows ordered live evidence and survives snapshots while disconnect becomes unknown', () => {
  const started = { id: 'turn', user_item_id: 'user', started_at: '2026-09-24T12:00:00Z', state: 'working' };
  const ended = { ...started, ended_at: '2026-09-24T12:00:42Z', state: 'completed' };
  let state = { ...initialState, selectedId: 'task', connection: 'connected', detailSeq: 10, detail: { id: 'task', items: [], interactions: [], subagents: [], turn_timings: [] } };
  state = update(state, { name: 'turn_timing', seq: 11, session_id: 'task', turn_timing: started });
  assert.deepEqual(state.detail.turn_timings, [started]);
  state = reducer(state, { type: 'connection', status: 'reconnecting' });
  assert.deepEqual(state.detail.turn_timings, [{ ...started, state: 'unknown' }]);
  state = update(state, { name: 'turn_timing', seq: 12, session_id: 'task', turn_timing: ended });
  state = update(state, { name: 'turn_timing', seq: 11, session_id: 'task', turn_timing: started });
  assert.deepEqual(state.detail.turn_timings, [ended]);
  state = reducer(state, { type: 'snapshot', data: { seq: 14, projects: [], sessions: [], session: { ...state.detail, turn_timings: [ended] } } });
  assert.deepEqual(state.detail.turn_timings, [ended]);
  state = reducer(state, { type: 'connection', status: 'offline' });
  assert.deepEqual(state.detail.turn_timings, [ended]);
});

test('an HTTP reply older than the live session state does not roll it back', () => {
  let state = loading();
  // The cancel reply is computed at 12:00:01; the turn end frame (12:00:02) arrives first.
  state = update(state, { name: 'session', seq: 11, session: { id: 'task', state: 'idle', name: 'Live', updated_at: '2026-09-24T12:00:02.5+02:00' } });
  const stale = { id: 'task', state: 'cancelled', name: 'Live', updated_at: '2026-09-24T10:00:01.123456789Z' };
  assert.equal(reducer(state, { type: 'upsert_session', session: stale }), state);
  assert.equal(state.detail.state, 'idle');
  // A reply as new as the live state, or newer, still applies (a rename that did not touch updated_at).
  state = reducer(state, { type: 'upsert_session', session: { id: 'task', state: 'idle', name: 'Renamed', updated_at: '2026-09-24T10:00:02.5Z' } });
  assert.equal(state.sessions[0].name, 'Renamed');
  state = reducer(state, { type: 'upsert_session', session: { id: 'task', state: 'completed', name: 'Renamed', updated_at: '2026-09-24T10:00:03Z' } });
  assert.equal(state.detail.state, 'completed');
});

test('switching Tasks keeps the previous detail until the next one arrives', () => {
  let state = loading();
  const first = state.detail;
  state = reducer(state, { type: 'select', id: 'next' });
  assert.equal(state.detail, null);
  assert.equal(state.previous, first);
  // A second switch before anything arrived still shows the Task that was on screen.
  state = reducer(state, { type: 'select', id: 'third' });
  assert.equal(state.previous, first);
  // Frames never touch the frozen Task.
  assert.equal(update(state, { name: 'item', seq: 11, session_id: 'task', item: { ...item('late'), agent_id: undefined } }).previous, first);
  const arrived = reducer(state, { type: 'snapshot', data: { seq: 12, projects: [], sessions: [], session: { id: 'third', items: [], interactions: [], subagents: [] } } });
  assert.equal(arrived.detail.id, 'third');
  assert.equal(arrived.previous, null);
  assert.equal(reducer(state, { type: 'select', id: null }).previous, null);
  assert.equal(reducer(state, { type: 'remove_session', id: 'task' }).previous, null);
});

test('a batch of frames applies in order, exactly as one frame at a time', () => {
  const frames = [
    { name: 'delta', seq: 11, session_id: 'task', item_id: 'reply', kind: 'assistant', text: 'a' },
    { name: 'delta', seq: 12, session_id: 'task', item_id: 'reply', kind: 'assistant', text: 'b' },
    { name: 'delta', seq: 12, session_id: 'task', item_id: 'reply', kind: 'assistant', text: 'dup' },
    { name: 'delta', seq: 13, session_id: 'other', item_id: 'reply', kind: 'assistant', text: 'x' },
    { name: 'delta', seq: 14, session_id: 'task', item_id: 'reply', kind: 'assistant', text: 'c' },
  ];
  const oneByOne = frames.reduce(update, loading());
  const batched = reducer(loading(), { type: 'updates', data: frames });
  assert.equal(batched.detail.items[0].text, 'abc');
  assert.deepEqual(batched.detail.items.map((i) => i.text), oneByOne.detail.items.map((i) => i.text));
  assert.equal(batched.detailSeq, 14);
});

test("a subagent's latest step is kept from live frames before its transcript is open, without its output", () => {
  let state = reducer(initialState, { type: 'select', id: 'task' });
  state = reducer(state, { type: 'snapshot', data: { seq: 10, projects: [], sessions: [], session: { id: 'task', items: [], interactions: [], subagents: [running] } } });
  state = update(state, { name: 'item', seq: 11, session_id: 'task', agent_id: 'helper', item: { ...tool('running'), tool: { name: 'read', status: 'running', input: 'x'.repeat(2000), output: 'secret' } } });
  assert.equal(state.agents.helper, undefined);
  assert.equal(state.agentSteps.helper.kind, 'tool');
  assert.equal(state.agentSteps.helper.tool.input.length, 500);
  assert.equal(state.agentSteps.helper.tool.output, undefined);
  state = update(state, delta(12, 'a'));
  assert.equal(state.agentSteps.helper.kind, 'assistant');
  const steps = state.agentSteps;
  state = update(state, delta(13, 'b'));
  assert.equal(state.agentSteps, steps, 'more deltas of the same item leave the step alone');
  state = reducer(state, { type: 'select', id: 'other' });
  assert.deepEqual(state.agentSteps, {});
});
