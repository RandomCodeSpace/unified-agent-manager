import assert from 'node:assert/strict';
import test from 'node:test';

// The archive cache touches storage only through these globals; each test sees what it used.
const storage = new Map();
globalThis.localStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, String(value)), removeItem: key => storage.delete(key) };
const idb = { opened: 0, deleted: [] };
// A database that never opens: every cache read must fall back to the network.
globalThis.indexedDB = {
  open() { idb.opened++; const request = {}; setTimeout(() => request.onerror?.()); return request; },
  deleteDatabase(name) { idb.deleted.push(name); },
};

const { budgetFor, cacheKey, evictions, REPRESENTATION, SCHEMA, stamp, TASK_BYTES, TTL } = await import('../src/lib/historyCache.ts');
const { ARCHIVE_DB, clearArchive, historyPage, retainArchive } = await import('../src/lib/historyArchive.ts');
const { initialState, reducer } = await import('../src/state.ts');

const MiB = 1024 * 1024;
const DAY = 24 * 60 * 60 * 1000;
const NOW = 100 * DAY;
// `used` counts from an hour ago unless it is a full timestamp, so small values order recency within the TTL.
const entry = (task, cursor, mib, used) => ({ key: cacheKey(task, '', cursor), bytes: mib * MiB, used: used > DAY ? used : NOW - 3600_000 + used });
const cursors = keys => keys.map(key => `${key[0]}/${key[3]}`).sort();
const marks = () => JSON.parse(storage.get('uam.history-archive') ?? '{}');
const item = (id, text = id) => ({ id, kind: 'assistant', text, time: '2026-09-25T10:00:00Z' });
const page = (items, before, extra = {}) => ({ seq: 40, epoch: 'e1', representation: 'compact-v1', items, before, after: '', ...extra });

test('keys name the task, agent, representation and cursor; the schema is versioned', () => {
  assert.deepEqual(cacheKey('task', 'agent', 'arc.x'), ['task', 'agent', 'compact-v1', 'arc.x']);
  assert.equal(REPRESENTATION, 'compact-v1');
  assert.ok(Number.isInteger(SCHEMA) && SCHEMA >= 1);
});

test('the budget is a tenth of the quota within 64 MiB, and 32 MiB without an estimate', () => {
  assert.equal(budgetFor(undefined), 32 * MiB);
  assert.equal(budgetFor(100 * MiB), 10 * MiB);
  assert.equal(budgetFor(10 * 1024 * MiB), 64 * MiB);
});

test('a task over its cap loses its least recently used pages first', () => {
  const entries = [entry('a', 'p2', 7, 2), entry('a', 'p1', 7, 1), entry('a', 'p3', 7, 3), entry('b', 'q', 7, 0.5)];
  assert.equal(TASK_BYTES, 16 * MiB);
  assert.deepEqual(cursors(evictions(entries, 64 * MiB, NOW)), ['a/p1']);
  // A small budget caps a task at half of it.
  assert.deepEqual(cursors(evictions([entry('a', 'p1', 2, 1), entry('a', 'p2', 2, 2), entry('a', 'p3', 2, 3)], 10 * MiB, NOW)), ['a/p1']);
});

test('over the budget, whole least recently used tasks go until 90% of it is left', () => {
  const entries = [entry('a', 'p', 2, 1), entry('b', 'p1', 2.5, 2), entry('b', 'p2', 2.5, 0), entry('c', 'p', 15, 3), entry('d', 'p', 12, 4)];
  assert.deepEqual(cursors(evictions(entries, 32 * MiB, NOW)), ['a/p', 'b/p1', 'b/p2']);
  assert.deepEqual(evictions(entries.slice(2), 32 * MiB, NOW), []);
});

test('a task unused for 14 days goes whole; its newest page decides', () => {
  const entries = [entry('old', 'p1', 1, NOW - 15 * DAY), entry('old', 'p2', 1, NOW - 20 * DAY), entry('mixed', 'p1', 1, NOW - 20 * DAY), entry('mixed', 'p2', 1, NOW - DAY)];
  assert.equal(TTL, 14 * DAY);
  assert.deepEqual(cursors(evictions(entries, 64 * MiB, NOW)), ['old/p1', 'old/p2']);
});

test('pages of a task without a mark go; unreadable marks spare them', () => {
  const entries = [entry('marked', 'p', 1, NOW), entry('lost', 'p', 1, NOW)];
  assert.deepEqual(cursors(evictions(entries, 64 * MiB, NOW, new Set(['marked']))), ['lost/p']);
  assert.deepEqual(evictions(entries, 64 * MiB, NOW, null), []);
});

test('a cached page keeps its cursors and claims no frames, so live updates survive it', () => {
  const stamped = stamp({ items: [item('old'), item('held', 'stale')], before: 'arc.2', after: 'x' }, 'e2');
  assert.deepEqual({ ...stamped, items: stamped.items.length }, { items: 2, before: 'arc.2', after: 'x', seq: -1, epoch: 'e2', representation: 'compact-v1', archive: true });
  let state = reducer({ ...initialState, selectedId: 'task' }, { type: 'snapshot', data: { seq: 10, sessions: [], projects: [], session: { id: 'task', representation: 'compact-v1', epoch: 'e2', items: [item('held')], interactions: [], subagents: [], history_before: 'arc.1' } } });
  state = reducer(state, { type: 'history_loading', sessionId: 'task', before: 'arc.1' });
  state = reducer(state, { type: 'update', data: { name: 'delta', seq: 11, session_id: 'task', item_id: 'held', kind: 'assistant', text: ' live' } });
  state = reducer(state, { type: 'history_loaded', sessionId: 'task', before: 'arc.1', page: stamped });
  assert.deepEqual(state.detail.items.map(i => [i.id, i.text]), [['old', 'old'], ['held', 'held live']]);
  assert.equal(state.detail.history_before, 'arc.2');
  assert.equal(state.historyItemSeq.held, 11);
});

test('held pages pass through without a mark or a database', async () => {
  let fetched = 0;
  const held = page([item('h1')], 'arc.entry');
  assert.equal(await historyPage('held-task', '', 'c1', 'older', 'e1', async () => { fetched++; return held; }), held);
  assert.equal(await historyPage('held-task', '', 'c2', 'newer', 'e1', async () => { fetched++; return held; }), held);
  assert.equal(fetched, 2);
  assert.deepEqual(marks(), {});
  assert.equal(idb.opened, 0);
});

test('archive cursors are learned from pages, and a failing cache reads from the network', async () => {
  const first = page([item('a2')], 'arc.2', { archive: true });
  assert.equal(await historyPage('t1', 'agent', 'arc.1', 'older', 'e1', async () => first), first);
  // The way in from held history is marked for the next page load; pages in any other representation are not.
  assert.deepEqual(marks(), { t1: [['agent', 'arc.1']] });
  await historyPage('t9', '', 'arc.x', 'older', 'e1', async () => ({ ...first, representation: undefined }));
  assert.equal('t9' in marks(), false);
  let fetched = 0;
  const second = page([item('a1')], '', { archive: true });
  assert.equal(await historyPage('t1', 'agent', 'arc.2', 'older', 'e1', async () => { fetched++; return second; }), second);
  assert.equal(fetched, 1);
  assert.ok(idb.opened >= 1);
});

test('a task keeps its four latest ways in; a 409 under an archive cursor forgets the task', async () => {
  for (const n of [1, 2, 3, 4, 5]) await historyPage('t2', '', `entry${n}`, 'older', 'e1', async () => page([], `arc.${n}`, { archive: true }));
  assert.deepEqual(marks().t2.map(([, cursor]) => cursor), ['entry2', 'entry3', 'entry4', 'entry5']);
  const conflict = Object.assign(new Error('history changed; reload the task'), { status: 409 });
  await assert.rejects(historyPage('t2', '', 'arc.5', 'older', 'e1', async () => { throw conflict; }), conflict);
  assert.equal('t2' in marks(), false);
});

test('tasks the service no longer lists are forgotten, and a sign-out deletes everything', async () => {
  await historyPage('kept', '', 'k1', 'older', 'e1', async () => page([], 'arc.k', { archive: true }));
  await historyPage('gone', '', 'g1', 'older', 'e1', async () => page([], 'arc.g', { archive: true }));
  retainArchive(['kept', 't1']);
  assert.deepEqual(Object.keys(marks()).sort(), ['kept', 't1']);
  clearArchive();
  assert.equal(storage.has('uam.history-archive'), false);
  assert.deepEqual(idb.deleted, [ARCHIVE_DB]);
});
