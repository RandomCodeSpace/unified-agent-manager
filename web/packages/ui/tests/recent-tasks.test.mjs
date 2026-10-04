import assert from 'node:assert/strict';
import test from 'node:test';
import { RecentTasks } from '../src/lib/recentTasks.ts';
import { initialState, reducer } from '../src/state.ts';

const task = (id, items = [], epoch = 'epoch') => ({ id, project_id: 'project', representation: 'compact-v1', epoch, detail_stream: true, items, interactions: [], subagents: [], history_before: '' });
const item = (id, text = id) => ({ id, kind: 'assistant', text });
const snapshot = (session, sessions = [session], epoch = session?.epoch ?? 'epoch') => ({ seq: 20, representation: 'compact-v1', epoch, detail_stream: true, session, sessions, projects: [{ id: 'project' }] });

test('five-entry cache evicts the least recently viewed task', () => {
  const cache = new RecentTasks();
  for (let i = 0; i < 5; i++) cache.remember(task(String(i)), 'epoch');
  cache.get('0', 'epoch');
  cache.remember(task('5'), 'epoch');
  assert.equal(cache.size, 5);
  assert.equal(cache.get('1', 'epoch'), undefined);
  assert.equal(cache.get('0', 'epoch').id, '0');
});

test('only the recent page is retained and its cursor preserves Unicode identity', () => {
  const cache = new RecentTasks();
  const detail = task('task', Array.from({ length: 100 }, (_, i) => item(`項目-${i}`)));
  cache.remember(detail, 'epoch');
  const saved = cache.get('task', 'epoch');
  assert.equal(saved.items.length, 50);
  assert.equal(saved.items[0].id, '項目-50');
  assert.equal(Buffer.from(saved.history_before, 'base64url').toString(), '項目-50');
  assert.equal(detail.items.length, 100);
});

test('page bytes are soft: one oversized message remains whole', () => {
  const cache = new RecentTasks();
  const text = '界'.repeat(100_000);
  cache.remember(task('task', [item('old'), item('big', text)]), 'epoch');
  const saved = cache.get('task', 'epoch');
  assert.equal(saved.items.length, 1);
  assert.equal(saved.items[0].text, text);
  assert.equal(Buffer.from(saved.history_before, 'base64url').toString(), 'big');
});

test('shared byte budget evicts entries and an oversized replacement is not admitted', () => {
  const cache = new RecentTasks();
  const large = 'x'.repeat(4 * 1024 * 1024);
  cache.remember(task('a', [item('large', large)]), 'epoch');
  cache.remember(task('b', [item('large', large)]), 'epoch');
  assert.equal(cache.size, 1);
  assert.ok(cache.bytes <= 16 * 1024 * 1024);
  assert.equal(cache.get('a', 'epoch'), undefined);
  cache.remember(task('b', [item('huge', large.repeat(3))]), 'epoch');
  assert.equal(cache.size, 0);
  assert.equal(cache.bytes, 0);
});

test('restart, deletion and logout release entries and their accounting', () => {
  const cache = new RecentTasks();
  cache.confirm(snapshot(task('a', [], 'old')));
  assert.equal(cache.get('a', 'new'), undefined);
  cache.confirm(snapshot(task('b', [], 'new')));
  assert.equal(cache.get('a', 'old'), undefined);
  cache.remember(task('c', [], 'new'));
  cache.retain(new Set(['c']));
  assert.equal(cache.size, 1);
  cache.clear();
  assert.equal(cache.size, 0);
  assert.equal(cache.bytes, 0);
  assert.equal(cache.get('c', 'new'), undefined);
});

test('a large loaded history can still contribute its small recent page', () => {
  const cache = new RecentTasks();
  cache.remember(task('task', [item('old', 'x'.repeat(9 * 1024 * 1024)), item('recent')]), 'epoch');
  const saved = cache.get('task', 'epoch');
  assert.deepEqual(saved.items.map(item => item.id), ['recent']);
  assert.ok(cache.bytes < 64 * 1024);
});

test('capability negotiation clears old entries and ignores unconfirmed representations', () => {
  const cache = new RecentTasks();
  cache.confirm(snapshot(task('a')));
  assert.equal(cache.get('a').id, 'a');
  cache.confirm({ ...snapshot(task('b')), epoch: undefined });
  assert.equal(cache.size, 0);
  cache.confirm(snapshot(task('a')));
  cache.confirm({ ...snapshot(task('b')), representation: undefined });
  assert.equal(cache.size, 0);
  cache.remember({ ...task('legacy'), representation: undefined });
  cache.remember({ ...task('missing-epoch'), epoch: undefined });
  assert.equal(cache.size, 0);
  cache.confirm(snapshot(task('new', [], 'new')));
  cache.remember(task('old', [], 'old'));
  assert.equal(cache.get('old'), undefined);
  assert.equal(cache.get('new').id, 'new');
});

test('history replacement, trims, task deletion and project deletion invalidate only affected entries', () => {
  const cache = new RecentTasks();
  for (const id of ['history', 'trim', 'deleted', 'project-task']) cache.remember(task(id));
  cache.remember({ ...task('kept'), project_id: 'other' });
  for (const [name, session_id] of [['history', 'history'], ['items_trimmed', 'trim'], ['session_removed', 'deleted']]) {
    cache.invalidate({ name, session_id });
    assert.equal(cache.get(session_id), undefined);
  }
  cache.invalidate({ name: 'project_removed', project_id: 'project' });
  assert.equal(cache.size, 1);
  assert.equal(cache.get('kept').id, 'kept');
  cache.confirm(snapshot(null, []));
  assert.equal(cache.size, 0);
});

test('cached rapid A-B-C selection never confirms status or reuses another task page', () => {
  const cache = new RecentTasks();
  const a = task('a'), b = task('b'), c = task('c');
  for (const detail of [a, b, c]) cache.remember(detail);
  let state = reducer({ ...initialState, selectedId: 'a' }, { type: 'snapshot', data: snapshot(a, [a, b, c]) });
  for (const id of ['b', 'c']) {
    state = reducer(state, { type: 'select', id, cached: cache.get(id) });
    assert.equal(state.previous.id, id);
    assert.equal(state.previousCached, true);
    assert.equal(state.detail, null);
    assert.equal(state.detailSeq, -1);
    assert.equal(state.snapshotSeq, 20);
    assert.deepEqual(state.agents, {});
    state = reducer(state, { type: 'connection', status: 'offline' });
    assert.equal(state.previous.id, id);
    assert.equal(state.previousCached, true);
  }
  // A point-in-time response for the task left behind cannot confirm C.
  const stale = reducer(state, { type: 'detail_loaded', detail: { ...b, seq: 999 } });
  assert.equal(stale, state);
  // Even a late response for C cannot activate a cached pane ahead of its stream.
  assert.equal(reducer(state, { type: 'detail_loaded', detail: { ...c, seq: 999 } }), state);
  state = reducer(state, { type: 'snapshot', data: { ...snapshot({ ...c, state: 'completed' }, [a, b, c]), seq: 21 } });
  assert.equal(state.detail.id, 'c');
  assert.equal(state.detail.state, 'completed');
  assert.equal(state.detailSeq, 21);
  assert.equal(state.previous, null);
  assert.equal(state.previousCached, false);
  assert.equal(state.connection, 'connected');
  const wrong = reducer(state, { type: 'select', id: 'b', cached: cache.get('a') });
  assert.equal(wrong.previous.id, 'c');
  assert.equal(wrong.previousCached, false);
});

test('cached presentation is released on history invalidation, removal and auth reset', () => {
  const cached = task('cached');
  const selected = () => reducer(initialState, { type: 'select', id: cached.id, cached });
  for (const name of ['history', 'items_trimmed']) {
    const state = reducer(selected(), { type: 'update', data: { name, seq: 1, session_id: cached.id } });
    assert.equal(state.previous, null);
    assert.equal(state.previousCached, false);
    assert.equal(state.detail, null);
  }
  for (const action of [{ type: 'remove_session', id: cached.id }, { type: 'reset' }]) {
    const state = reducer(selected(), action);
    assert.equal(state.previous, null);
    assert.equal(state.previousCached, false);
    assert.equal(state.selectedId, null);
  }
});

test('HTTP history and reload from a different instance wait for the main snapshot', () => {
  const detail = { ...task('task', [item('recent')], 'old'), history_before: 'cursor' };
  let state = reducer({ ...initialState, selectedId: detail.id }, { type: 'snapshot', data: snapshot(detail) });
  state = reducer(state, { type: 'history_loading', sessionId: detail.id, before: 'cursor' });
  const page = { seq: 999, epoch: 'new', items: [item('new-instance')], before: '' };
  assert.equal(reducer(state, { type: 'history_loaded', sessionId: detail.id, before: 'cursor', page }), state);
  assert.equal(state.detail.history_before, 'cursor');
  assert.equal(reducer(state, { type: 'detail_loaded', detail: { ...detail, epoch: 'new', seq: 999 } }), state);
  state = reducer(state, { type: 'snapshot', data: { ...snapshot(task('task', [item('confirmed')], 'new')), seq: 1 } });
  assert.equal(state.historyRequest, null);
  assert.equal(state.detail.epoch, 'new');
  assert.deepEqual(state.detail.items.map(item => item.id), ['confirmed']);
  assert.equal(state.snapshotSeq, 1);
});
