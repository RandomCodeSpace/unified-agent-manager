import assert from 'node:assert/strict';
import test from 'node:test';
import { countParts, countSubagents, earlierTag, indexGroups, inFilter, liveRows, liveSet, matches, parentMap, ranAgain, replyIndex, runLines, spawnedBy, statusGroups } from '../src/lib/subagents.ts';

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

test('a reply\'s subagents are the ones its calls spawned, in spawn order; one call shared by two records keeps the later', () => {
  const reply = [user('u1', 0), call('c2', 2), prose('m', 3), call('c1', 4)];
  const subagents = [agent('first', 'c1'), agent('second', 'c2'), agent('elsewhere', 'c9'), agent('orphan', undefined)];
  assert.deepEqual(spawnedBy(reply, subagents).map((s) => s.id), ['second', 'first']);
  assert.deepEqual(spawnedBy([user('u2', 5), prose('m2', 6)], subagents), []);
  const twice = [agent('old', 'c1'), agent('new', 'c1')];
  assert.equal(parentMap(twice).get('c1').id, 'new');
  assert.equal(parentMap(twice), parentMap(twice));
});

test('replies come from the identity index: every call counts, loaded or not, and a steer stays in its reply', () => {
  const index = [call('c0', 0), user('u1', 1), call('c1', 2), { ...user('s', 3), delivery: 'steer' }, call('c2', 4), call('c3', 5), user('u2', 6)];
  const r = replyIndex(index, [agent('a1', 'c1'), agent('a3', 'c3'), agent('a0', 'c0')]);
  assert.deepEqual(r.list.map((x) => [x.key, x.time, x.calls, x.subagents.map((s) => s.id)]), [
    ['start', undefined, ['c0'], ['a0']],
    ['u1', at(1), ['c1', 'c2', 'c3'], ['a1', 'a3']],
    ['u2', at(6), [], []],
  ]);
  assert.equal(r.ofCall.get('c2').key, 'u1');
  assert.equal(r.byKey.get('u2').calls.length, 0);
});

test('identity tones follow the call order within a reply of at most five calls; a bigger reply stays neutral', () => {
  const items = [user('u1', 0), call('c1', 1), call('c2', 2), user('u2', 3), ...Array.from({ length: 6 }, (_, n) => call(`d${n}`, 4 + n))];
  const subagents = [agent('a2', 'c2'), agent('a1', 'c1'), ...Array.from({ length: 6 }, (_, n) => agent(`b${n}`, `d${n}`))];
  const { tones } = replyIndex(items, subagents);
  assert.equal(tones.get('a1'), 'violet');
  assert.equal(tones.get('a2'), 'pink');
  for (let n = 0; n < 6; n++) assert.equal(tones.has(`b${n}`), false);
  // Five is still coloured: violet, pink, cyan, amber, teal (no red, green or blue: those are states).
  const five = [user('u', 0), ...Array.from({ length: 5 }, (_, n) => call(`e${n}`, n + 1))];
  assert.deepEqual([...replyIndex(five, Array.from({ length: 5 }, (_, n) => agent(`f${n}`, `e${n}`))).tones.values()], ['violet', 'pink', 'cyan', 'amber', 'teal']);
  // Six calls with only two loaded: the full count decides, so no colours.
  const six = [user('u', 0), ...Array.from({ length: 6 }, (_, n) => call(`g${n}`, n + 1))];
  assert.equal(replyIndex(six, [agent('h0', 'g0'), agent('h1', 'g1')]).tones.size, 0);
});

test('the live set is the latest reply\'s subagents plus earlier running ones; empty once none runs, unless a row is open', () => {
  const index = [user('u1', 0), call('c0', 1), call('c1', 2), user('u2', 10), call('c3', 11), call('c4', 12)];
  const subagents = [agent('old', 'c1', 'running'), agent('older', 'c0'), agent('new', 'c3', 'running'), agent('done', 'c4', 'failed')];
  const latest = replyIndex(index, subagents).list.at(-1);
  assert.deepEqual(liveSet(latest, subagents).map((x) => [x.subagent.id, x.earlier]), [['new', false], ['done', false], ['old', true]]);
  // Nothing running: the card goes, even with the latest reply's settled subagents.
  const settled = [agent('new', 'c3'), agent('done', 'c4', 'failed')];
  assert.deepEqual(liveSet(replyIndex(index, settled).list.at(-1), settled), []);
  // An earlier one alone keeps it up.
  const alone = [agent('old', 'c1', 'running')];
  assert.deepEqual(liveSet(replyIndex(index, alone).list.at(-1), alone).map((x) => x.earlier), [true]);
  // The open row keeps the card (and itself) after the last one finished.
  const after = [agent('old', 'c1'), agent('new', 'c3'), agent('done', 'c4', 'failed')];
  const kept = liveSet(replyIndex(index, after).list.at(-1), after, 'old');
  assert.deepEqual(kept.map((x) => [x.subagent.id, x.earlier]), [['new', false], ['done', false], ['old', true]]);
  assert.deepEqual(liveSet(replyIndex(index, after).list.at(-1), after, 'new').map((x) => x.subagent.id), ['new', 'done']);
});

test('the live card shows failed then running rows, five at most, plus the open row; it says what the rest are until Show all', () => {
  const set = [
    ...['r1', 'r2', 'r3', 'r4', 'r5'].map((id) => ({ subagent: agent(id, `c-${id}`, 'running'), earlier: false })),
    { subagent: agent('f1', 'c-f1', 'failed'), earlier: false },
    { subagent: agent('d1', 'c-d1'), earlier: false },
    { subagent: agent('d2', 'c-d2', 'idle'), earlier: false },
    { subagent: agent('s1', 'c-s1', 'cancelled'), earlier: false },
  ];
  const capped = liveRows(set, false);
  assert.deepEqual(capped.rows.map((x) => x.subagent.id), ['f1', 'r1', 'r2', 'r3', 'r4']);
  assert.equal(capped.rest, '1 more running · 2 done · 1 stopped');
  const all = liveRows(set, true);
  assert.deepEqual(all.rows.map((x) => x.subagent.id), ['f1', 'r1', 'r2', 'r3', 'r4', 'r5', 'd2', 'd1', 's1']);
  assert.equal(all.rest, '');
  assert.equal(liveRows(set.slice(0, 2), false).rest, '');
  // An open row that has finished stays where it was read.
  const kept = liveRows(set, false, 'd1');
  assert.deepEqual(kept.rows.map((x) => x.subagent.id), ['f1', 'r1', 'r2', 'r3', 'r4', 'd1']);
  assert.equal(kept.rest, '1 more running · 1 done · 1 stopped');
});

test('an earlier reply\'s subagent on the live card says how it came back only when its runs tell', () => {
  const run = (m, trigger, status = 'completed') => ({ started_at: at(m), ...(status === 'running' ? {} : { ended_at: at(m + 1) }), status, trigger });
  assert.deepEqual(earlierTag(agent('a', 'c', 'running'), at(1)), { text: 'from an earlier reply' });
  assert.deepEqual(earlierTag(agent('a', 'c', 'running', { runs: [run(1, 'spawn', 'running')] })), { text: 'from an earlier reply' });
  assert.deepEqual(earlierTag(agent('a', 'c', 'running', { runs: [run(1, 'spawn'), run(20, 'agent', 'running')] })), { text: 'resumed by the agent', first: at(1) });
  assert.deepEqual(earlierTag(agent('a', 'c', 'running', { runs: [run(1, 'spawn'), run(20, 'user', 'running')] })), { text: 'your follow-up', first: at(1) });
  // Without a start on the first run, the call's time.
  assert.deepEqual(earlierTag(agent('a', 'c', 'running', { runs: [{ ...run(1, 'spawn'), started_at: '' }, run(20, 'user', 'running')] }), at(0)), { text: 'your follow-up', first: at(0) });
});

test('"ran again" names the latest run after the reply and the reply it started in; never for the latest reply or without runs', () => {
  const run = (m, trigger) => ({ started_at: at(m), ended_at: at(m + 1), status: 'completed', trigger });
  const index = [user('u1', 0), call('c1', 1), user('u2', 10), call('c2', 11), user('u3', 20)];
  const replies = replyIndex(index, [agent('a1', 'c1'), agent('a2', 'c2')]);
  assert.equal(ranAgain(agent('a1', 'c1'), replies), null);
  assert.equal(ranAgain(agent('a1', 'c1', 'completed', { runs: [run(1, 'spawn'), run(5, 'agent')] }), replies), null);
  assert.deepEqual(ranAgain(agent('a1', 'c1', 'completed', { runs: [run(1, 'spawn'), run(12, 'user'), run(25, 'agent')] }), replies), { at: at(25), reply: 'u3' });
  assert.deepEqual(ranAgain(agent('a1', 'c1', 'completed', { runs: [run(1, 'spawn'), run(12, 'user')] }), replies), { at: at(12), reply: 'u2' });
  assert.equal(ranAgain(agent('a2', 'c2', 'completed', { runs: [run(11, 'spawn'), run(15, 'agent')] }), replies), null);
});

test('a subagent with several runs lists each: when, who started it, and how it ended', () => {
  assert.deepEqual(runLines(agent('a', 'c', 'running', { runs: [{ started_at: at(5), status: 'running', trigger: 'spawn' }] })), []);
  const lines = runLines(agent('a', 'c', 'running', { runs: [
    { started_at: at(5), ended_at: '2026-10-02T17:07:40Z', status: 'completed', trigger: 'spawn' },
    { started_at: at(14), ended_at: at(15), status: 'failed', trigger: 'user' },
    { started_at: at(20), ended_at: at(21), status: 'cancelled', trigger: 'agent' },
    { started_at: at(26), status: 'running', trigger: 'agent' },
  ] }));
  assert.deepEqual(lines.map((l) => [l.n, l.started_at, l.trigger, l.outcome, l.tone]), [
    [1, at(5), 'started by the agent', '✓ 2m 40s', 'success'],
    [2, at(14), 'your follow-up', '✗ failed · 1m 0s', 'error'],
    [3, at(20), 'resumed by the agent', 'stopped', 'muted'],
    [4, at(26), 'resumed by the agent', 'running', 'accent'],
  ]);
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
