import assert from 'node:assert/strict';
import test from 'node:test';
import { initialState, reducer } from '../src/state.ts';

const item = (id, text = id) => ({ id, kind: 'assistant', text, time: '2026-09-25T10:00:00Z' });
const loaded = () => reducer({ ...initialState, selectedId: 'task' }, { type: 'snapshot', data: { seq: 10, sessions: [], projects: [], session: { id: 'task', items: [item('recent')], interactions: [], subagents: [], history_before: 'cursor' } } });
const loading = state => reducer(state, { type: 'history_loading', sessionId: 'task', before: 'cursor' });
const page = (state, seq, items, before = '') => reducer(state, { type: 'history_loaded', sessionId: 'task', before: 'cursor', page: { seq, items, before } });
const frame = (state, data) => reducer(state, { type: 'update', data: { session_id: 'task', ...data } });

test('older pages prepend without duplicating live items or replaying captured output', () => {
  let state = loading(loaded());
  state = frame(state, { name: 'delta', seq: 11, item_id: 'older', kind: 'assistant', text: ' captured' });
  state = frame(state, { name: 'item', seq: 13, item: item('new'), append: true });
  state = frame(state, { name: 'delta', seq: 14, item_id: 'older', kind: 'assistant', text: ' later' });
  state = page(state, 12, [item('older', 'old captured'), item('recent', 'stale')]);
  assert.deepEqual(state.detail.items.map(i => [i.id, i.text]), [['older', 'old captured later'], ['recent', 'recent'], ['new', 'new']]);
  assert.equal(state.detail.history_before, '');
  assert.equal(state.historyRequest, null);
});

test('HTTP arriving ahead of SSE suppresses only output already in older-page items', () => {
  let state = page(loading(loaded()), 15, [item('older', 'snapshot')]);
  state = frame(state, { name: 'delta', seq: 12, item_id: 'older', kind: 'assistant', text: 'duplicate' });
  state = frame(state, { name: 'delta', seq: 13, item_id: 'recent', kind: 'assistant', text: ' live' });
  state = frame(state, { name: 'delta', seq: 16, item_id: 'older', kind: 'assistant', text: ' later' });
  assert.deepEqual(state.detail.items.map(i => i.text), ['snapshot later', 'recent live']);
});

test('updates to unfetched items stay out of the tail; new items still append', () => {
  let state = loaded();
  state = frame(state, { name: 'item', seq: 11, item: item('older') });
  state = frame(state, { name: 'delta', seq: 12, item_id: 'older', kind: 'assistant', text: 'more' });
  state = frame(state, { name: 'item', seq: 13, item: item('new'), append: true });
  assert.deepEqual(state.detail.items.map(i => i.id), ['recent', 'new']);
});

test('an eviction during a page fetch cannot resurrect old items', () => {
  let state = loading(loaded());
  state = frame(state, { name: 'items_trimmed', seq: 14, items: [{ id: 'older' }] });
  state = page(state, 12, [item('older')]);
  assert.deepEqual(state.detail.items.map(i => i.id), ['recent']);
  assert.equal(state.historyItemSeq.older, undefined);
});

test('switches, reconnect snapshots and history replacements invalidate pending pages', () => {
  for (const action of [
    { type: 'select', id: 'other' },
    { type: 'snapshot', data: { seq: 20, projects: [], sessions: [], session: loaded().detail } },
    { type: 'update', data: { name: 'history', seq: 20, session_id: 'task', items: [item('replacement')], subagents: [], history: 'loaded', history_before: 'cursor' } },
  ]) {
    const state = reducer(loading(loaded()), action);
    assert.equal(page(state, 12, [item('older')]), state);
  }
});

test('in-flight history buffering is bounded and a failed request can retry', () => {
  let state = loading(loaded());
  for (let seq = 11; seq <= 267; seq++) state = frame(state, { name: 'delta', seq, item_id: 'older', kind: 'assistant', text: 'x' });
  assert.equal(state.historyRequest.loading, false);
  assert.equal(state.historyRequest.buffered.length, 0);
  assert.match(state.historyRequest.error, /retry/);
  assert.equal(page(state, 12, [item('older')]), state);
  state = page(loading(state), 268, [item('older', 'fresh')]);
  assert.equal(state.detail.items[0].text, 'fresh');
});
