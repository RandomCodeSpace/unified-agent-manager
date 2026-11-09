import assert from 'node:assert/strict';
import test from 'node:test';
import { countParts, countSubagents, families, followUps, indexGroups, inFilter, liveSet, mainCall, matches, mergeOutline, namedSubagent, parentMap, replyIndex, runLines, subagentName, totalTokens } from '../src/lib/subagents.ts';

const at = (m) => `2026-10-02T17:${String(m).padStart(2, '0')}:00Z`;
const user = (id, m, text = `Message ${id}`) => ({ id, kind: 'user', time: at(m), text });
const call = (id, m) => ({ id, kind: 'tool', time: at(m), tool: { name: 'task', status: 'completed' } });
const agent = (id, parent, status = 'completed', extra = {}) => ({ id, name: `Agent ${id}`, parent_tool_call_id: parent, status, ...extra });

test('a reply counts its subagents: done is completed or idle, stopped is cancelled, only the parts that are not zero', () => {
  const c = countSubagents([agent('a', 'c1'), agent('b', 'c2', 'idle'), agent('c', 'c3', 'failed'), agent('d', 'c4', 'running'), agent('e', 'c5', 'cancelled')]);
  assert.deepEqual(c, { total: 5, running: 1, done: 2, failed: 1, stopped: 1 });
  assert.deepEqual(countParts(c), [{ text: '2 done', tone: 'success' }, { text: '1 failed', tone: 'error' }, { text: '1 running', tone: 'accent' }, { text: '1 stopped', tone: 'muted' }]);
  assert.deepEqual(countParts(countSubagents([agent('a', 'c1')])), [{ text: '1 done', tone: 'success' }]);
});

test('one call shared by two subagent records keeps the later', () => {
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

test('a task call that failed before a subagent started is no call of its reply; one whose subagent failed is', () => {
  const refused = { ...call('c1', 2), tool: { name: 'task', status: 'failed' } };
  const crashed = { ...call('c2', 3), tool: { name: 'task', status: 'failed' } };
  const r = replyIndex([user('u1', 1), refused, crashed, call('c3', 4)], [agent('a2', 'c2', 'failed'), agent('a3', 'c3')]);
  assert.deepEqual(r.list.map((x) => [x.calls, x.subagents.map((s) => s.id)]), [[['c2', 'c3'], ['a2', 'a3']]]);
  assert.equal(r.ofCall.has('c1'), false);
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
  // The Utility name and the agent's own both match.
  const named = agent('a', 'c', 'completed', { name: 'inventory-bug-review', label: 'Inventory bug review' });
  assert.equal(matches(named, '', 'bug review'), true);
  assert.equal(matches(named, '', 'bug-review'), true);
  assert.equal(subagentName(named), 'Inventory bug review');
  assert.equal(subagentName(agent('a', 'c', 'completed', { name: 'inventory-bug-review' })), 'inventory-bug-review');
  assert.equal(inFilter(agent('a', 'c', 'idle'), 'done'), true);
  assert.equal(inFilter(agent('a', 'c', 'cancelled'), 'done'), false);
  assert.equal(inFilter(agent('a', 'c', 'cancelled'), 'all'), true);
  assert.equal(inFilter(agent('a', 'c', 'cancelled'), 'stopped'), true);
  assert.equal(inFilter(agent('a', 'c', 'failed'), 'stopped'), false);
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

test('a full run record marks where earlier runs were dropped', () => {
  const run = (m, trigger = 'agent') => ({ started_at: at(m % 60), ended_at: at(m % 60), status: 'completed', trigger });
  const full = agent('a', 'c', 'completed', { runs: [run(0, 'spawn'), ...Array.from({ length: 49 }, (_, i) => run(i + 1))] });
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

test('a list draws families: each subagent then the ones it spawned, running families first, then failed, done and stopped ones, else in spawn order', () => {
  const list = [
    agent('stop1', 'c0', 'cancelled'),
    agent('done1', 'c1'),
    agent('outer', 'c2'),
    agent('run1', 'c3', 'running'),
    agent('inner', 'k1', 'failed', { parent_agent_id: 'outer' }),
    agent('deeper', 'k2', 'completed', { parent_agent_id: 'inner' }),
    agent('orphan', 'k3', 'completed', { parent_agent_id: 'gone' }),
  ];
  assert.deepEqual(families(list).map((f) => f.rows.map((r) => `${r.subagent.id}:${r.depth}`)), [['run1:0'], ['outer:0', 'inner:1', 'deeper:2'], ['done1:0'], ['orphan:0'], ['stop1:0']]);
  // Unranked, they nest the same way but keep spawn order.
  assert.deepEqual(families(list, false).map((f) => f.head.id), ['stop1', 'done1', 'outer', 'run1', 'orphan']);
  assert.equal(totalTokens([agent('a', 'c', 'completed', { tokens: 1200 }), agent('b', 'c'), agent('d', 'c', 'running', { tokens: 800 })]), 2000);
});

test('a reply\'s span names a subagent only as its whole exact name, unique in the reply, or in code its id or the id\'s first 8 characters', () => {
  const lister = { id: '28329ff5-1111-4222-8333-444455556666', name: 'Quick lister', status: 'completed' };
  const twins = [{ id: 'd1', name: 'Reviewer', status: 'completed' }, { id: 'd2', name: 'Reviewer', status: 'completed' }];
  const all = [lister, ...twins];
  assert.equal(namedSubagent('Quick lister', all, false), lister);
  assert.equal(namedSubagent('Quick lister', all, true), lister);
  // More words, another case or a shared name: plain.
  for (const text of ['Quick lister.', 'the Quick lister', 'quick lister', 'Reviewer', '']) assert.equal(namedSubagent(text, all, false), undefined, text);
  // An id only in code: the full one or its 8-character prefix, never a shorter one.
  assert.equal(namedSubagent('28329ff5', all, true), lister);
  assert.equal(namedSubagent(lister.id, all, true), lister);
  assert.equal(namedSubagent('28329ff5', all, false), undefined);
  assert.equal(namedSubagent('28329f', all, true), undefined);
  assert.equal(namedSubagent('d1', all, true), twins[0]);
  assert.equal(namedSubagent('Reviewer', all, true), undefined);
});

test('a later run belongs to the reply whose span holds its start; the spawn run, an unrecorded start and one before the first message point nowhere', () => {
  const index = [call('c0', 0), user('u1', 1), call('c1', 2), user('u2', 10), user('u3', 20), call('c3', 21)];
  const run = (m, trigger = 'agent', status = 'completed') => ({ started_at: m === undefined ? undefined : at(m), status, trigger });
  const subagents = [
    // Spawned in turn 1, run again in turn 1 (stays), twice in turn 3 (one reference, the latest run), once without a start.
    agent('a1', 'c1', 'running', { runs: [run(2, 'spawn'), run(5), run(21, 'user'), run(undefined), run(25, 'agent', 'running')] }),
    // Before the first message: its later run points to turn 2 without a turn of its own.
    agent('a0', 'c0', 'completed', { runs: [run(0, 'spawn'), run(12, 'user')] }),
    agent('a3', 'c3', 'completed', { runs: [run(21, 'spawn')] }),
  ];
  const replies = replyIndex(index, subagents);
  assert.deepEqual([...replies.turns], [['u1', 1], ['u2', 2], ['u3', 3]]);
  const follow = followUps(replies);
  assert.deepEqual([...follow].map(([key, refs]) => [key, refs.map((f) => [f.subagent.id, f.turn, f.run.started_at, f.run.status])]), [
    ['u2', [['a0', undefined, at(12), 'completed']]],
    ['u3', [['a1', 1, at(25), 'running']]],
  ]);
  // A run before the first message, with only a reply after it, still lands nowhere.
  const early = [agent('b', 'c1', 'completed', { runs: [run(2, 'spawn'), run(0)] })];
  assert.equal(followUps(replyIndex(index, early)).size, 0);
});

test('a resumed running subagent stays a reference in the live rows, never a moved row; one still running from its spawn keeps its row', () => {
  const index = [user('u1', 0), call('c1', 1), call('c2', 2), call('c0', 3), user('u2', 10), user('u3', 20), call('c3', 21)];
  const resumed = agent('back', 'c1', 'running', { runs: [{ started_at: at(1), ended_at: at(3), status: 'completed', trigger: 'spawn' }, { started_at: at(22), status: 'running', trigger: 'user' }] });
  const between = agent('mid', 'c2', 'running', { runs: [{ started_at: at(2), ended_at: at(4), status: 'completed', trigger: 'spawn' }, { started_at: at(11), status: 'running', trigger: 'agent' }] });
  const subagents = [resumed, between, agent('long', 'c0', 'running'), agent('new', 'c3', 'completed')];
  const replies = replyIndex(index, subagents);
  const set = liveSet(replies.list.at(-1), subagents, undefined, followUps(replies));
  assert.deepEqual(set.map((x) => [x.subagent.id, x.earlier, x.ref?.turn, x.ref?.run.started_at]), [
    ['new', false, undefined, undefined],
    ['back', true, 1, at(22)],
    ['mid', true, 1, at(11)],
    ['long', true, undefined, undefined],
  ]);
  // Its run in the latest reply ended: still that reply's reference while another works.
  const done = { ...resumed, status: 'completed', runs: [resumed.runs[0], { ...resumed.runs[1], ended_at: at(23), status: 'completed' }] };
  const after = [done, agent('new', 'c3', 'running')];
  const r2 = replyIndex(index, after);
  assert.deepEqual(liveSet(r2.list.at(-1), after, undefined, followUps(r2)).map((x) => [x.subagent.id, !!x.ref]), [['new', false], ['back', true]]);
});

test('a reply\'s follow-ups join its index group, and open one for a reply that spawned none', () => {
  const index = [user('u1', 0), call('c1', 1), user('u2', 10), user('u3', 20), call('c3', 21)];
  const items = [user('u2', 10, 'Ask the reviewer again.')];
  const subagents = [agent('a1', 'c1', 'completed', { runs: [{ started_at: at(1), status: 'completed', trigger: 'spawn' }, { started_at: at(11), status: 'completed', trigger: 'user' }, { started_at: at(22), status: 'completed', trigger: 'agent' }] }), agent('a3', 'c3')];
  const groups = indexGroups(index, items, subagents, followUps(replyIndex(index, subagents)));
  assert.deepEqual(groups.map((g) => [g.key, g.text, g.subagents.map((s) => s.id), g.refs.map((f) => f.subagent.id)]), [
    ['u3', 'Message u3', ['a3'], ['a1']],
    ['u2', 'Ask the reviewer again.', [], ['a1']],
    ['u1', 'Message u1', ['a1'], []],
  ]);
});
