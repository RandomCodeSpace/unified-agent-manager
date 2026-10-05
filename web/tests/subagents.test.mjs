import assert from 'node:assert/strict';
import test from 'node:test';
import { countParts, countSubagents, families, indexGroups, inFilter, liveSet, mainCall, matches, mergeOutline, parentMap, replyIndex, runCount, runLines, spawnedBy, totalTokens } from '../src/lib/subagents.ts';

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

test('the filter reads name and summary; the index filters by status', () => {
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
  // A message whose text is neither loaded nor outlined keeps its time; the caller words it.
  const bare = index.map((i) => (i.kind === 'user' ? { ...i, text: undefined } : i));
  assert.deepEqual(indexGroups(bare, [], [agent('a1', 'c1')]).map((g) => [g.key, g.text, g.time]), [['u1', '', at(0)]]);
});

test('a full run record says "50+ runs" and marks where earlier runs were dropped', () => {
  const run = (m, trigger = 'agent') => ({ started_at: at(m % 60), ended_at: at(m % 60), status: 'completed', trigger });
  assert.equal(runCount(agent('a', 'c')), '');
  assert.equal(runCount(agent('a', 'c', 'completed', { runs: [run(1, 'spawn'), run(2)] })), '2 runs');
  const full = agent('a', 'c', 'completed', { runs: [run(0, 'spawn'), ...Array.from({ length: 49 }, (_, i) => run(i + 1))] });
  assert.equal(runCount(full), '50+ runs');
  const lines = runLines(full);
  assert.deepEqual(lines.slice(0, 3).map((l) => [l.n, l.gapBefore]), [[1, false], [null, true], [null, false]]);
  assert.equal(lines.at(-1).n, null);
});

test('a run without a recorded start keeps its line without a time', () => {
  const lines = runLines(agent('a', 'c', 'running', { runs: [{ status: 'completed', trigger: 'spawn' }, { started_at: at(20), status: 'running', trigger: 'user' }] }));
  assert.deepEqual(lines.map((l) => [l.n, l.started_at, l.outcome]), [[1, undefined, '✓'], [2, at(20), 'running']]);
});

test('a subagent another one spawned joins its top-level ancestor\'s reply and index group, its call with it', () => {
  const index = [user('u1', 0), call('c1', 1), user('u2', 5), call('c2', 6)];
  const outer = agent('outer', 'c1');
  const inner = agent('inner', 'k1', 'completed', { parent_agent_id: 'outer' });
  const deeper = agent('deeper', 'k2', 'failed', { parent_agent_id: 'inner' });
  const lost = agent('lost', 'k3', 'completed', { parent_agent_id: 'not-loaded' });
  const subagents = [outer, agent('later', 'c2'), inner, deeper, lost];
  assert.equal(mainCall(deeper, subagents), 'c1');
  assert.equal(mainCall(lost, subagents), undefined);
  const r = replyIndex(index, subagents);
  assert.deepEqual(r.byKey.get('u1').subagents.map((s) => s.id), ['outer', 'inner', 'deeper']);
  assert.deepEqual(r.byKey.get('u1').calls, ['c1', 'k1', 'k2']);
  assert.equal(r.ofCall.get('k2').key, 'u1');
  const groups = indexGroups(index, index, subagents);
  assert.deepEqual(groups.map((g) => [g.key, g.subagents.map((s) => s.id)]), [['u2', ['later']], ['u1', ['outer', 'inner', 'deeper']], ['', ['lost']]]);
});

test('the server\'s outline places subagents whose reply is not loaded; loaded items it lacks go by time', () => {
  const server = [{ id: 'u1', kind: 'user', time: at(10), text: 'Audit every package' }, { id: 'c1', kind: 'tool', time: at(11), tool: { name: 'task' } }, { id: 'u2', kind: 'user', time: at(15), text: 'Thanks' }];
  const held = [{ ...server[2], text: 'Thanks, all of it' }, call('c2', 16)];
  const older = user('u0', 1);
  const merged = mergeOutline(server, [older, ...held]);
  assert.deepEqual(merged.map((i) => i.id), ['u0', 'u1', 'c1', 'u2', 'c2']);
  assert.equal(mergeOutline(undefined, held), held);
  const subagents = [agent('a1', 'c1'), agent('a2', 'c2')];
  assert.deepEqual(replyIndex(merged, subagents).byKey.get('u1').subagents.map((s) => s.id), ['a1']);
  const groups = indexGroups(merged, held, subagents);
  assert.deepEqual(groups.map((g) => [g.key, g.text]), [['u2', 'Thanks, all of it'], ['u1', 'Audit every package']]);
});

test('a list draws families: each subagent then the ones it spawned, failed families first, then running ones, else in spawn order', () => {
  const list = [
    agent('done1', 'c1'),
    agent('outer', 'c2'),
    agent('run1', 'c3', 'running'),
    agent('inner', 'k1', 'failed', { parent_agent_id: 'outer' }),
    agent('deeper', 'k2', 'completed', { parent_agent_id: 'inner' }),
    agent('orphan', 'k3', 'completed', { parent_agent_id: 'gone' }),
  ];
  assert.deepEqual(families(list).map((f) => f.rows.map((r) => `${r.subagent.id}:${r.depth}`)), [['outer:0', 'inner:1', 'deeper:2'], ['run1:0'], ['done1:0'], ['orphan:0']]);
  // The header index nests them the same way but keeps spawn order.
  assert.deepEqual(families(list, false).map((f) => f.head.id), ['done1', 'outer', 'run1', 'orphan']);
  assert.equal(totalTokens([agent('a', 'c', 'completed', { tokens: 1200 }), agent('b', 'c'), agent('d', 'c', 'running', { tokens: 800 })]), 2000);
});
