import assert from 'node:assert/strict';
import test from 'node:test';
import { bodyKey, detailReducer as reduce, emptyDetails } from '../src/lib/detail-state.ts';
const ref = { agentId: '', itemId: 'tool' }, key = bodyKey(ref);
const tool = (output, status = 'running') => ({ id: 'tool', kind: 'tool', time: '', tool: { name: 'shell', status, input: 'echo example', output } });
const interests = (state, bodies = [ref], agentId = '') => reduce(state, { type: 'interests', bodies, agentId });
const frame = (state, name, seq, data = {}) => reduce(state, { type: 'frame', data: { name, seq, epoch: 'one', session_id: 'task', ...data } });
const body = (state, seq, output, status = 'running') => frame(state, 'body', seq, { item: tool(output, status) });

test('summary first keeps an open body refreshing until its authoritative replacement', () => {
  let state = body(interests(emptyDetails('one')), 5, 'partial');
  state = reduce(state, { type: 'invalidate', versions: { [key]: 6 } });
  assert.equal(state.bodies[key].status, 'refreshing');
  assert.equal(state.bodies[key].item.tool.output, 'partial');
  state = body(state, 7, 'final-only output', 'completed');
  assert.equal(state.bodies[key].status, 'loaded');
  assert.equal(state.bodies[key].item.tool.output, 'final-only output');
});
test('detail first is never rolled back by the older summary and completed bodies accept corrections', () => {
  let state = body(interests(emptyDetails('one')), 7, 'final', 'completed');
  state = reduce(state, { type: 'invalidate', versions: { [key]: 6 } });
  assert.equal(state.bodies[key].status, 'loaded');
  state = body(state, 9, 'rewritten final', 'completed');
  assert.equal(state.bodies[key].item.tool.output, 'rewritten final');
});
test('suffixes append once and full completion preserves images and replacement content', () => {
  let state = body(interests(emptyDetails('one')), 5, 'a');
  state = frame(state, 'body_output', 6, { item_id: 'tool', text: 'b' });
  state = frame(state, 'body_output', 6, { item_id: 'tool', text: 'b' });
  assert.equal(state.bodies[key].item.tool.output, 'ab');
  state = frame(state, 'body', 7, { item: { ...tool('abc', 'completed'), images: [{ id: 'image', mime: 'image/png', size: 10 }] } });
  assert.equal(state.bodies[key].item.tool.output, 'abc');
  assert.equal(state.bodies[key].item.images[0].id, 'image');
});
test('unchanged equal-sequence snapshot and conditional current marker settle a reconfiguration', () => {
  let state = body(interests(emptyDetails('one')), 5, 'done', 'completed');
  state = interests(state);
  assert.equal(state.bodies[key].status, 'refreshing');
  state = body(state, 5, 'done', 'completed');
  assert.equal(state.bodies[key].status, 'loaded');
  state = interests(state);
  state = frame(state, 'body_current', 5, { item_id: 'tool' });
  assert.equal(state.bodies[key].status, 'loaded');
  state = reduce(state, { type: 'invalidate', versions: { [key]: 8 } });
  state = frame(state, 'body_current', 7, { item_id: 'tool' });
  assert.equal(state.bodies[key].status, 'refreshing');
});
test('agent and main item IDs are separate resources; closed bodies are released', () => {
  const agentRef = { agentId: 'helper', itemId: 'tool' };
  let state = interests(emptyDetails('one'), [ref, agentRef]);
  state = body(state, 5, 'main');
  state = frame(state, 'body', 5, { agent_id: 'helper', item: { ...tool('agent'), agent_id: 'helper' } });
  assert.equal(state.bodies[key].item.tool.output, 'main');
  assert.equal(state.bodies[bodyKey(agentRef)].item.tool.output, 'agent');
  state = interests(state, []);
  assert.deepEqual(state.bodies, {});
  assert.equal(frame(state, 'body', 10, { item: tool('late') }), state);
});
test('a current marker without retained complete content cannot synthesize a loaded body', () => {
  const state = interests(emptyDetails('one'));
  assert.equal(frame(state, 'body_current', 5, { item_id: 'tool' }), state);
});
test('different epoch frames cannot merge, and history reset clears all detailed references', () => {
  const state = body(interests(emptyDetails('one')), 5, 'old');
  assert.equal(reduce(state, { type: 'frame', data: { name: 'body', epoch: 'two', seq: 99, session_id: 'task', item: tool('wrong') } }), state);
  assert.deepEqual(frame(state, 'detail_reset', 8), emptyDetails('one'));
});
test('trim and unavailable preserve an explicit state without keeping full body strings', () => {
  let state = body(interests(emptyDetails('one')), 5, 'discard me');
  state = frame(state, 'items_trimmed', 6, { items: [{ id: 'tool' }] });
  assert.equal(state.bodies[key].status, 'unavailable');
  assert.equal(state.bodies[key].item, undefined);
  state = interests(state, []);
  assert.equal(state.bodies[key].status, 'unavailable');
  state = reduce(state, { type: 'release', key });
  assert.deepEqual(state.bodies, {});
});
const chat = (id, text = id) => ({ id, kind: 'assistant', time: '', agent_id: 'helper', text });
function agentState() {
  let state = interests(emptyDetails('one'), [], 'helper');
  state = frame(state, 'detail_snapshot', 5, { agent_id: 'helper', items: [chat('new')], before: 'cursor' });
  return frame(state, 'detail_ready', 5);
}
test('subagent initialization applies bounded resource pages atomically at readiness', () => {
  let state = interests(agentState(), [], 'helper');
  state = frame(state, 'detail_snapshot', 9, { agent_id: 'helper', items: [chat('new', 'corrected')], before: 'more' });
  assert.deepEqual(state.agent.items.map(i => i.text), ['new']);
  state = frame(state, 'detail_page', 9, { agent_id: 'helper', items: [chat('old')], before: '' });
  assert.deepEqual(state.agent.items.map(i => i.text), ['new']);
  state = frame(state, 'detail_ready', 9);
  assert.deepEqual(state.agent.items.map(i => i.text), ['old', 'corrected']);
  assert.equal(state.agent.before, '');
});
test('authoritative range refresh removes a previously loaded item trimmed during the connection gap', () => {
  let state = agentState();
  state = reduce(state, { type: 'page_start', agentId: 'helper', before: 'cursor', token: 1 });
  state = reduce(state, { type: 'page_done', agentId: 'helper', before: 'cursor', token: 1, page: { seq: 5, items: [chat('old')], before: '' } });
  state = interests(state, [], 'helper');
  state = frame(state, 'detail_snapshot', 9, { agent_id: 'helper', items: [chat('new')], before: '' });
  state = frame(state, 'detail_ready', 9);
  assert.deepEqual(state.agent.items.map(i => i.id), ['new']);
});
test('older agent pages replay newer trims and ignore callbacks from a cancelled generation', () => {
  let state = agentState();
  state = reduce(state, { type: 'page_start', agentId: 'helper', before: 'cursor', token: 1 });
  state = frame(state, 'items_trimmed', 7, { items: [{ id: 'old', agent_id: 'helper' }] });
  state = reduce(state, { type: 'page_done', agentId: 'helper', before: 'cursor', token: 1, page: { seq: 6, items: [chat('old')], before: '' } });
  assert.deepEqual(state.agent.items.map(i => i.id), ['new']);
  const reset = interests(state, [], 'different');
  assert.equal(reduce(reset, { type: 'page_done', agentId: 'helper', before: 'cursor', token: 1, page: { seq: 6, items: [chat('old')], before: '' } }), reset);
});
test('an opened agent summary advances its own body floor independently of main sequence', () => {
  const agentRef = { agentId: 'helper', itemId: 'tool' }, agentKey = bodyKey(agentRef);
  let state = interests(agentState(), [agentRef], 'helper');
  state = frame(state, 'body', 5, { agent_id: 'helper', item: tool('partial') });
  state = frame(state, 'item', 6, { agent_id: 'helper', item: { ...tool(undefined, 'completed'), compact: { has_reasoning: false, has_text: false } } });
  assert.equal(state.bodies[agentKey].floor, 6);
  assert.equal(state.bodies[agentKey].status, 'refreshing');
});

test('deadline failure releases a pending page while preserving transcript and allowing a retry', () => {
  let state = agentState();
  state = reduce(state, { type: 'page_start', agentId: 'helper', before: 'cursor', token: 1 });
  state = reduce(state, { type: 'page_failed', agentId: 'helper', token: 1, error: 'Timed out' });
  assert.equal(state.agent.page, undefined);
  assert.deepEqual(state.agent.items.map(item => item.id), ['new']);
  assert.equal(state.agent.pageError, 'Timed out');
  state = reduce(state, { type: 'page_start', agentId: 'helper', before: 'cursor', token: 2 });
  assert.equal(state.agent.page.token, 2);
  assert.equal(state.agent.pageError, undefined);
});

test('overflow fails only page hydration and still applies the triggering live frame', () => {
  let state = agentState();
  state = reduce(state, { type: 'page_start', agentId: 'helper', before: 'cursor', token: 1 });
  for (let seq = 6; seq <= 262; seq++) state = frame(state, 'delta', seq, { agent_id: 'helper', item_id: 'new', kind: 'assistant', text: '.' });
  assert.equal(state.agent.page, undefined);
  assert.match(state.agent.pageError, /too quickly/);
  assert.equal(state.agent.items[0].text, 'new' + '.'.repeat(257));
});
