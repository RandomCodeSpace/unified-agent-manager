import assert from 'node:assert/strict';
import test from 'node:test';
import { ACTIVE_BYTES, ACTIVE_ITEMS, TAIL_ITEMS, accountItem, boundItems, itemCursor, mergeItems } from '../src/lib/historyWindow.ts';

const item = (id, text = id) => ({ id, kind: 'assistant', time: '', text });
const ids = items => items.map(item => item.id);
const accounted = items => items.reduce((bytes, item) => bytes + accountItem(item), 0);

test('a long single turn is bounded by items even without user-message boundaries', () => {
  const transcript = Array.from({ length: 1800 }, (_, index) => item(String(index)));
  const older = boundItems(transcript, 'older');
  assert.equal(older.items.length, ACTIVE_ITEMS);
  assert.deepEqual(ids(older.items), ids(transcript.slice(0, ACTIVE_ITEMS)));
  assert.deepEqual(older.droppedBefore, []);
  assert.equal(older.droppedAfter.length, 1650);
  const newer = boundItems(transcript, 'newer');
  assert.equal(newer.items.length, ACTIVE_ITEMS);
  assert.deepEqual(ids(newer.items), ids(transcript.slice(-ACTIVE_ITEMS)));
  assert.equal(newer.droppedBefore.length, 1650);
  assert.deepEqual(newer.droppedAfter, []);
  assert.equal(transcript.length, 1800);
  assert.equal(TAIL_ITEMS, 50);
});

test('byte accounting includes UTF-16 strings and nested tool/image records without serialization', () => {
  const short = item('text', 'x');
  const large = { ...short, text: '界🙂'.repeat(100_000) };
  assert.equal(accountItem(large) - accountItem(short), (large.text.length - short.text.length) * 2);
  const tool = { ...short, kind: 'tool', tool: { name: 'shell', input: 'echo test', output: 'y'.repeat(100_000), status: 'completed' }, images: [{ id: 'image', mime: 'image/png', size: 1234 }] };
  assert.ok(accountItem(tool) > accountItem(short) + 200_000);
  assert.doesNotThrow(() => accountItem({ ...large, toJSON: () => { throw new Error('must not serialize'); } }));
});

test('the byte ceiling can limit a page before the item cap in either direction', () => {
  const transcript = Array.from({ length: 10 }, (_, index) => item(String(index), 'x'.repeat(400_000)));
  for (const direction of ['older', 'newer']) {
    const result = boundItems(transcript, direction);
    assert.ok(result.items.length > 1 && result.items.length < transcript.length);
    assert.ok(accounted(result.items) <= ACTIVE_BYTES);
    const adjacent = direction === 'older' ? result.droppedAfter[0] : result.droppedBefore.at(-1);
    assert.ok(accounted(result.items) + accountItem(adjacent) > ACTIVE_BYTES);
    assert.deepEqual([...result.droppedBefore, ...result.items, ...result.droppedAfter], transcript);
  }
});

test('an oversized edge item stays intact, while a later oversized item remains outside the contiguous window', () => {
  const huge = item('huge', 'x'.repeat(ACTIVE_BYTES));
  const small = item('small');
  for (const [direction, transcript] of [['older', [huge, small]], ['newer', [small, huge]]]) {
    const result = boundItems(transcript, direction);
    assert.equal(result.items.length, 1);
    assert.equal(result.items[0], huge);
    assert.equal(result.items[0].text.length, ACTIVE_BYTES);
    assert.ok(accounted(result.items) > ACTIVE_BYTES);
  }
  assert.deepEqual(ids(boundItems([small, huge], 'older').items), ['small']);
  assert.deepEqual(ids(boundItems([huge, small], 'newer').items), ['small']);
});

test('empty pages and exact byte boundaries make progress without retaining dropped records internally', () => {
  assert.deepEqual(boundItems([], 'older'), { items: [], droppedBefore: [], droppedAfter: [] });
  const first = item('first'), second = item('second');
  const result = boundItems([first, second], 'older', 150, accountItem(first));
  assert.deepEqual(result.items, [first]);
  assert.deepEqual(result.droppedAfter, [second]);
  // A separate later call depends only on its explicit inputs.
  assert.deepEqual(boundItems([second], 'newer').items, [second]);
  assert.deepEqual(boundItems([first, second], 'newer', 1).items, [second]);
});

test('item cursors preserve Unicode and use unpadded base64url', () => {
  for (const id of ['ASCII/id+', '項目-🙂-é', 'boundary-\u{10ffff}', 'x'.repeat(100_000)]) {
    const cursor = itemCursor(id);
    assert.match(cursor, /^[A-Za-z0-9_-]*$/);
    assert.equal(Buffer.from(cursor, 'base64url').toString('utf8'), id);
  }
});

test('opposite-direction restoration preserves order and existing live values across page overlap', () => {
  const all = Array.from({ length: 8 }, (_, index) => item(String(index)));
  const current = all.slice(4).map(entry => entry.id === '4' ? { ...entry, text: 'live correction' } : entry);
  const older = boundItems(mergeItems(current, all.slice(0, 6), 'older'), 'older', 5);
  assert.deepEqual(ids(older.items), ['0', '1', '2', '3', '4']);
  assert.equal(older.items.at(-1).text, 'live correction');
  assert.deepEqual(ids(older.droppedAfter), ['5', '6', '7']);
  const restored = boundItems(mergeItems(older.items, all.slice(3), 'newer'), 'newer', 5);
  assert.deepEqual(ids(restored.items), ['3', '4', '5', '6', '7']);
  assert.equal(restored.items[1].text, 'live correction');
  assert.equal(new Set(ids(restored.items)).size, restored.items.length);
  assert.equal(all[4].text, '4');
});

test('duplicate page records appear once and an existing live record wins in both directions', () => {
  const live = item('shared', 'latest');
  const incoming = [item('shared', 'old'), item('shared', 'older'), item('new'), item('new')];
  for (const direction of ['older', 'newer']) {
    const result = mergeItems([live], incoming, direction);
    assert.deepEqual(ids(result), ['shared', 'new']);
    assert.equal(result[0], live);
  }
});

// Outer activity may include reasoning, but each inner tool run needs its own key.
import { groupIdentities, indexPage } from '../src/lib/historyState.ts';
test('inner tool run identities split at reasoning and questions while retained metadata keeps adjacent keys stable', () => {
  const items = [
    { id: 'a', kind: 'tool', time: '', tool: { name: 'read', status: 'completed' } },
    { id: 'b', kind: 'reasoning', time: '', compact: { has_reasoning: true } },
    { id: 'c', kind: 'tool', time: '', tool: { name: 'read', status: 'completed' } },
    { id: 'd', kind: 'tool', time: '', tool: { name: 'read', status: 'completed' } },
    { id: 'q', kind: 'tool', time: '', tool: { name: 'ask_user', status: 'completed', question_outcome: 'answered' } },
    { id: 'e', kind: 'tool', time: '', tool: { name: 'read', status: 'completed' } },
  ];
  const activity = groupIdentities(items), tools = groupIdentities(items, true);
  assert.equal(activity.get('c'), 'a');
  assert.equal(tools.get('a'), 'a');
  assert.equal(tools.get('c'), 'c');
  assert.equal(tools.get('d'), 'c');
  assert.equal(tools.get('e'), 'e');
});

test('metadata pages update known IDs in place and add only unknown boundary IDs', () => {
  const rows = ['a', 'b', 'c', 'd'].map(id => ({ id, kind: 'assistant', text: 'old text', time: '' }));
  const incoming = [rows[1], { ...rows[2], ended_at: 'fresh' }];
  const updated = indexPage(rows, incoming, 'older');
  assert.deepEqual(updated.map(item => item.id), ['a', 'b', 'c', 'd']);
  assert.equal(updated[2].ended_at, 'fresh');
  assert.equal(updated[2].text, undefined);
  assert.deepEqual(indexPage(updated, [{ id: 'earlier', kind: 'user', time: '' }, rows[0]], 'older').map(item => item.id), ['earlier', 'a', 'b', 'c', 'd']);
});
test('loose decided interactions split adjacent inner tool runs', () => {
  const items = ['a', 'b', 'c'].map((id, i) => ({ id, kind: 'tool', time: String(i + 1), tool: { name: 'read', status: 'completed' } }));
  const keys = groupIdentities(items, true, undefined, ['1.5']);
  assert.equal(keys.get('a'), 'a');
  assert.equal(keys.get('b'), 'b');
  assert.equal(keys.get('c'), 'b');
});

test('hidden empty reasoning does not split the inner tool run', () => {
  const items = [{ id: 'a', kind: 'tool', time: '' }, { id: 'empty', kind: 'reasoning', time: '' }, { id: 'b', kind: 'tool', time: '' }];
  assert.equal(groupIdentities(items, true).get('b'), 'a');
});

test('backward group extension preserves the established disclosure identity', () => {
  const items = ['a', 'b', 'c', 'd'].map(id => ({ id, kind: 'tool', time: '', tool: { name: 'read', status: 'completed' } }));
  for (const toolsOnly of [false, true]) {
    const before = groupIdentities(items.slice(2), toolsOnly);
    const after = groupIdentities(items, toolsOnly, undefined, [], before);
    assert.deepEqual([...after.values()], ['c', 'c', 'c', 'c']);
    const split = groupIdentities(items, toolsOnly, item => item.id === 'b', [], after);
    assert.notEqual(split.get('a'), split.get('c'));
  }
});

test('discovering the parent user preserves a partial compact turn disclosure key', () => {
  const content = ['c', 'd'].map(id => ({ id, kind: 'assistant', time: '' }));
  const before = groupIdentities(content, 'turn');
  const items = [{ id: 'parent', kind: 'user', time: '' }, { id: 'a', kind: 'reasoning', time: '' }, ...content, { id: 'next-user', kind: 'user', time: '' }, { id: 'next-answer', kind: 'assistant', time: '' }];
  const after = groupIdentities(items, 'turn', undefined, [], before);
  assert.equal(after.get('a'), 'c');
  assert.equal(after.get('c'), 'c');
  assert.equal(after.get('d'), 'c');
  assert.equal(after.get('next-answer'), 'next-answer');
});
