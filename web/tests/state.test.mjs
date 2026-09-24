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
