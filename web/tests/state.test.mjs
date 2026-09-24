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
