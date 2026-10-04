import assert from 'node:assert/strict';
import test from 'node:test';

// The archive cache touches storage only through these globals; each test sees what it used.
const storage = new Map();
let storageReads = 0;
globalThis.localStorage = { getItem: key => { storageReads++; return storage.get(key) ?? null; }, setItem: (key, value) => storage.set(key, String(value)), removeItem: key => storage.delete(key) };
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
  assert.deepEqual(cacheKey('task', 'agent', 'a.eA'), ['task', 'agent', 'compact-v1', 'a.eA']);
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
  const stamped = stamp({ items: [item('old'), item('held', 'stale')], before: 'a.2', after: 'x' }, 'e2');
  assert.deepEqual({ ...stamped, items: stamped.items.length }, { items: 2, before: 'a.2', after: 'x', seq: -1, epoch: 'e2', representation: 'compact-v1', archive: true });
  let state = reducer({ ...initialState, selectedId: 'task' }, { type: 'snapshot', data: { seq: 10, sessions: [], projects: [], session: { id: 'task', representation: 'compact-v1', epoch: 'e2', items: [item('held')], interactions: [], subagents: [], history_before: 'a.1' } } });
  state = reducer(state, { type: 'history_loading', sessionId: 'task', before: 'a.1' });
  state = reducer(state, { type: 'update', data: { name: 'delta', seq: 11, session_id: 'task', item_id: 'held', kind: 'assistant', text: ' live' } });
  state = reducer(state, { type: 'history_loaded', sessionId: 'task', before: 'a.1', page: stamped });
  assert.deepEqual(state.detail.items.map(i => [i.id, i.text]), [['old', 'old'], ['held', 'held live']]);
  assert.equal(state.detail.history_before, 'a.2');
  assert.equal(state.historyItemSeq.held, 11);
});

test('held pages pass through without storage or a database', async () => {
  let fetched = 0;
  const held = page([item('h1')], 'a.aDE');
  const reads = storageReads;
  assert.equal(await historyPage('held-task', '', 'aDI', 'older', 'e1', async () => { fetched++; return held; }), held);
  // An archive cursor paged newer passes through too.
  assert.equal(await historyPage('held-task', '', 'a.aDE', 'newer', 'e1', async () => { fetched++; return held; }), held);
  assert.equal(fetched, 2);
  assert.equal(storageReads, reads);
  assert.deepEqual(marks(), {});
  assert.equal(idb.opened, 0);
});

test('archive cursors are known by their prefix, and a failing cache reads from the network', async () => {
  let fetched = 0;
  const first = page([item('a2')], 'a.YTI', { archive: true });
  // The first retained item's archive cursor leads into the record.
  assert.equal(await historyPage('t1', 'agent', 'a.aDE', 'older', 'e1', async () => { fetched++; return first; }), first);
  assert.ok(idb.opened >= 1);
  // A Task is marked before its first write; pages in any other representation are not stored.
  assert.deepEqual(Object.keys(marks()), ['t1']);
  await historyPage('t9', '', 'a.eA', 'older', 'e1', async () => ({ ...first, representation: undefined }));
  assert.equal('t9' in marks(), false);
  const second = page([item('a1')], '', { archive: true });
  assert.equal(await historyPage('t1', 'agent', 'a.YTI', 'older', 'e1', async () => { fetched++; return second; }), second);
  assert.equal(fetched, 2);
});

test('a 409 under an archive cursor forgets the task; one under a retained cursor does not', async () => {
  await historyPage('t2', '', 'a.NQ', 'older', 'e1', async () => page([], 'a.NA', { archive: true }));
  const conflict = Object.assign(new Error('history changed; reload the task'), { status: 409 });
  await assert.rejects(historyPage('t2', '', 'NQ', 'older', 'e1', async () => { throw conflict; }), conflict);
  assert.equal('t2' in marks(), true);
  await assert.rejects(historyPage('t2', '', 'a.NA', 'older', 'e1', async () => { throw conflict; }), conflict);
  assert.equal('t2' in marks(), false);
});

test('tasks the service no longer lists are forgotten, and a sign-out deletes everything', async () => {
  await historyPage('kept', '', 'a.aw', 'older', 'e1', async () => page([], 'a.a2s', { archive: true }));
  await historyPage('gone', '', 'a.Zw', 'older', 'e1', async () => page([], 'a.Z2c', { archive: true }));
  // Marks of the earlier format, which kept cursors per Task, still name their Tasks.
  storage.set('uam.history-archive', JSON.stringify({ ...marks(), old: [['', 'b2xk']], stale: [['', 'c3Rh']] }));
  retainArchive(['kept', 't1', 'old']);
  assert.deepEqual(Object.keys(marks()).sort(), ['kept', 'old', 't1']);
  clearArchive();
  assert.equal(storage.has('uam.history-archive'), false);
  assert.deepEqual(idb.deleted, [ARCHIVE_DB]);
});
