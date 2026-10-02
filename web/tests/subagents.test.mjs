import assert from 'node:assert/strict';
import test from 'node:test';
import { countParts, countSubagents, identities, indexGroups, inFilter, liveRows, liveSet, matches, spawnedBy, statusGroups } from '../src/lib/subagents.ts';

const at = (m) => `2026-10-02T17:${String(m).padStart(2, '0')}:00Z`;
const user = (id, m, text = `Message ${id}`) => ({ id, kind: 'user', time: at(m), text });
const call = (id, m) => ({ id, kind: 'tool', time: at(m), tool: { name: 'task', status: 'completed' } });
const prose = (id, m) => ({ id, kind: 'assistant', time: at(m), text: 'Done.' });
const agent = (id, parent, status = 'completed', extra = {}) => ({ id, name: `Agent ${id}`, parent_tool_call_id: parent, status, ...extra });

test('a reply counts its subagents: done is completed or idle, stopped is cancelled, only the parts that are not zero', () => {
  const c = countSubagents([agent('a', 'c1'), agent('b', 'c2', 'idle'), agent('c', 'c3', 'failed'), agent('d', 'c4', 'running'), agent('e', 'c5', 'cancelled')]);
  assert.deepEqual(c, { total: 5, running: 1, done: 2, failed: 1, stopped: 1 });
  assert.deepEqual(countParts(c), [{ text: '2 done', tone: 'success' }, { text: '1 failed', tone: 'error' }, { text: '1 running', tone: 'accent' }, { text: '1 stopped', tone: 'muted' }]);
  assert.deepEqual(countParts(countSubagents([agent('a', 'c1')])), [{ text: '1 done', tone: 'success' }]);
});

test('a reply\'s subagents are the ones its calls spawned, in spawn order; a reused one stays with its first call', () => {
  const reply = [user('u1', 0), call('c2', 2), prose('m', 3), call('c1', 4)];
  const subagents = [agent('first', 'c1'), agent('second', 'c2'), agent('elsewhere', 'c9'), agent('orphan', undefined)];
  assert.deepEqual(spawnedBy(reply, subagents).map((s) => s.id), ['second', 'first']);
  assert.deepEqual(spawnedBy([user('u2', 5), prose('m2', 6)], subagents), []);
});

test('identity tones follow spawn order within a reply of at most five; a bigger reply stays neutral', () => {
  const items = [user('u1', 0), call('c1', 1), call('c2', 2), user('u2', 3), ...Array.from({ length: 6 }, (_, n) => call(`d${n}`, 4 + n))];
  const subagents = [agent('a2', 'c2'), agent('a1', 'c1'), ...Array.from({ length: 6 }, (_, n) => agent(`b${n}`, `d${n}`))];
  const tones = identities(items, subagents);
  assert.equal(tones.get('a1'), 'violet');
  assert.equal(tones.get('a2'), 'pink');
  for (let n = 0; n < 6; n++) assert.equal(tones.has(`b${n}`), false);
  // Five is still coloured: violet, pink, cyan, amber, teal (no red, green or blue: those are states).
  const five = [user('u', 0), ...Array.from({ length: 5 }, (_, n) => call(`e${n}`, n + 1))];
  assert.deepEqual([...identities(five, Array.from({ length: 5 }, (_, n) => agent(`f${n}`, `e${n}`))).values()], ['violet', 'pink', 'cyan', 'amber', 'teal']);
  // A steer stays in its reply.
  const steered = [user('u', 0), call('c1', 1), { ...user('s', 2), delivery: 'steer' }, call('c2', 3)];
  assert.deepEqual([...identities(steered, [agent('x', 'c1'), agent('y', 'c2')]).values()], ['violet', 'pink']);
});

test('the live set is the latest reply\'s subagents plus earlier ones running again, and empty once none runs', () => {
  const latest = [user('u2', 10), call('c3', 11), call('c4', 12)];
  const subagents = [agent('old', 'c1', 'running'), agent('older', 'c0'), agent('new', 'c3', 'running'), agent('done', 'c4', 'failed')];
  const set = liveSet(latest, subagents);
  assert.deepEqual(set.map((x) => [x.subagent.id, x.resumed]), [['new', false], ['done', false], ['old', true]]);
  // Nothing running: the card goes, even with the latest reply's settled subagents.
  assert.deepEqual(liveSet(latest, [agent('new', 'c3'), agent('done', 'c4', 'failed')]), []);
  // A resumed subagent alone keeps it up.
  assert.deepEqual(liveSet(latest, [agent('old', 'c1', 'running')]).map((x) => x.resumed), [true]);
});

test('the live card shows failed then running rows, five at most, and says what the rest are until Show all', () => {
  const set = [
    ...['r1', 'r2', 'r3', 'r4', 'r5'].map((id) => ({ subagent: agent(id, `c-${id}`, 'running'), resumed: false })),
    { subagent: agent('f1', 'c-f1', 'failed'), resumed: false },
    { subagent: agent('d1', 'c-d1'), resumed: false },
    { subagent: agent('d2', 'c-d2', 'idle'), resumed: false },
    { subagent: agent('s1', 'c-s1', 'cancelled'), resumed: false },
  ];
  const capped = liveRows(set, false);
  assert.deepEqual(capped.rows.map((x) => x.subagent.id), ['f1', 'r1', 'r2', 'r3', 'r4']);
  assert.equal(capped.rest, '1 more running · 2 done · 1 stopped');
  const all = liveRows(set, true);
  assert.deepEqual(all.rows.map((x) => x.subagent.id), ['f1', 'r1', 'r2', 'r3', 'r4', 'r5', 'd2', 'd1', 's1']);
  assert.equal(all.rest, '');
  assert.equal(liveRows(set.slice(0, 2), false).rest, '');
});

test('a long list groups by status, Failed and Running open, Done and Stopped folded; the filter reads name and summary', () => {
  const list = [agent('a', 'c1'), agent('b', 'c2', 'failed'), agent('c', 'c3', 'idle'), agent('d', 'c4', 'running')];
  assert.deepEqual(statusGroups(list).map((g) => [g.key, g.open, g.subagents.map((s) => s.id)]), [['failed', true, ['b']], ['running', true, ['d']], ['done', false, ['a', 'c']]]);
  assert.equal(matches(agent('a', 'c', 'completed', { name: 'Audit internal/Store' }), '', 'store'), true);
  assert.equal(matches(agent('a', 'c'), 'go vet: undefined: pageCursor', 'PAGECURSOR'), true);
  assert.equal(matches(agent('a', 'c'), 'nothing', 'store'), false);
  assert.equal(matches(agent('a', 'c'), 'nothing', '  '), true);
  assert.equal(inFilter(agent('a', 'c', 'idle'), 'done'), true);
  assert.equal(inFilter(agent('a', 'c', 'cancelled'), 'done'), false);
  assert.equal(inFilter(agent('a', 'c', 'cancelled'), 'all'), true);
});

test('the header index groups subagents by the user message that started their reply, newest first, never by turn number', () => {
  const index = [user('u1', 0), call('c1', 1), call('c2', 2), user('u2', 5), { ...user('s', 6), delivery: 'steer' }, call('c3', 7)];
  const items = [user('u1', 0, 'Audit every package.\nOne subagent each.'), user('u2', 5, '  Retry the failed ones.  ')];
  const subagents = [agent('a3', 'c3', 'running'), agent('a1', 'c1'), agent('a2', 'c2', 'failed'), agent('lost', 'c-gone')];
  const groups = indexGroups(index, items, subagents);
  assert.deepEqual(groups.map((g) => [g.key, g.text, g.time, g.subagents.map((s) => s.id)]), [
    ['u2', 'Retry the failed ones.', at(5), ['a3']],
    ['u1', 'Audit every package.', at(0), ['a1', 'a2']],
    ['', '', undefined, ['lost']],
  ]);
  // A message whose text is not loaded keeps its time; the caller words it.
  assert.deepEqual(indexGroups(index, [], [agent('a1', 'c1')]).map((g) => [g.key, g.text, g.time]), [['u1', '', at(0)]]);
});
