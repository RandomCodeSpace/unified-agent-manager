import assert from 'node:assert/strict';
import test from 'node:test';
import { KEPT_SNAPSHOTS, MAX_LANES, METER_ROWS, keepSnapshot, ledgerSections, ledgerStamp, recallSnapshot, snapshotFacts, snapshotSections, tallyWords, todoCue, todoLedger, todoLine, todoLinks, todoSections, todoSentence, todoTally, todoWord, turnTodoName } from '../src/lib/todos.ts';

const row = (id, status, extra = {}) => ({ id, title: `Row ${id}`, status, ...extra });
const view = (todos, extra = {}) => {
  const count = (s) => todos.filter((t) => t.status === s).length;
  return {
    known: true,
    touched: true,
    todos,
    counts: { total: todos.length, done: count('done'), blocked: count('blocked'), in_progress: count('in_progress'), pending: count('pending'), open: count('pending') + count('in_progress') },
    ...extra,
  };
};

test('the line says nothing of a list it does not know, an empty one, or one untouched with nothing open', () => {
  assert.equal(todoLine(undefined), undefined);
  assert.equal(todoLine({ known: false, touched: true, todos: [row('a', 'pending')], counts: { total: 1, open: 1 } }), undefined);
  assert.equal(todoLine(view([])), undefined);
  assert.equal(todoLine(view([row('a', 'done')], { touched: false })), undefined);
});

test('a list the turn has not touched yet reads as the last turn\'s, with every open stage counted', () => {
  const line = todoLine(view([row('a', 'done'), row('b', 'pending'), row('c', 'blocked'), row('d', 'in_progress')], { touched: false }));
  const stages = [{ status: 'in_progress', n: 1 }, { status: 'blocked', n: 1 }, { status: 'pending', n: 1 }];
  assert.deepEqual(line, { kind: 'carried', done: 1, total: 4, stages, open: 0, meter: ['done', 'pending', 'blocked', 'in_progress'] });
  assert.equal(todoSentence(line), '. Todo from the last turn: 1 of 4 done, 1 in progress, 1 blocked, 1 to do');
});

test('a touched list: done of total, each open stage in the readers\' order, the Now row, the meter up to a dozen rows', () => {
  const todos = [row('a', 'done'), row('b', 'in_progress'), row('c', 'in_progress'), row('d', 'blocked'), row('e', 'pending'), row('f', 'pending')];
  const line = todoLine(view(todos, { now: 'c' }));
  const stages = [{ status: 'in_progress', n: 2 }, { status: 'blocked', n: 1 }, { status: 'pending', n: 2 }];
  assert.deepEqual(line, { kind: 'list', done: 1, total: 6, stages, open: 0, now: 'Row c', meter: ['done', 'in_progress', 'in_progress', 'blocked', 'pending', 'pending'], cue: undefined });
  // The in-progress count says how many more there are; the sentence says it once.
  assert.equal(todoSentence(line), '. Todo 1 of 6 done, 2 in progress, 1 blocked, 2 to do. Now: Row c');
  // No row named Now: the counts alone.
  assert.equal(todoSentence(todoLine(view(todos))), '. Todo 1 of 6 done, 2 in progress, 1 blocked, 2 to do');
  // Stages with no rows are left out.
  assert.equal(todoSentence(todoLine(view([row('a', 'done')]))), '. Todo 1 of 1 done');
  assert.equal(todoSentence(todoLine(view([row('a', 'done'), row('b', 'pending')]))), '. Todo 1 of 2 done, 1 to do');
  // Past a dozen rows, or with rows left out, the words stand alone.
  assert.equal(todoLine(view(Array.from({ length: METER_ROWS + 1 }, (_, i) => row(`r${i}`, 'pending')))).meter, undefined);
  assert.equal(todoLine({ ...view([row('a', 'pending')]), counts: { total: 101, pending: 101, open: 101 }, omitted: 100 }).meter, undefined);
});

test('counts from before the split: what they say, never a guess at in progress or to do', () => {
  // A service that counts only `open`: the open rows stay one count.
  assert.deepEqual(todoTally({ done: 5, total: 7, blocked: 1, open: 1 }), { done: 5, total: 7, stages: [{ status: 'blocked', n: 1 }], open: 1 });
  assert.equal(tallyWords(todoTally({ done: 5, total: 7, blocked: 1, open: 1 })), '5 of 7 done, 1 blocked, 1 open');
  assert.equal(todoSentence(todoLine({ known: true, touched: false, todos: [], counts: { done: 1, total: 3, open: 2 } })), '. Todo from the last turn: 1 of 3 done, 2 open');
  // Split counts carry `open` too: nothing is left over.
  assert.equal(todoTally({ done: 1, total: 4, in_progress: 1, pending: 2, open: 3 }).open, 0);
});

test('the cue is the turn\'s latest row done or blocked; a blocked one wins a tie; older changes are not news', () => {
  const since = '2026-10-08T12:00:00Z';
  const at = (s) => `2026-10-08T12:00:${String(s).padStart(2, '0')}Z`;
  const done = [row('a', 'done', { changed_at: at(10) }), row('b', 'done', { changed_at: at(20) }), row('c', 'pending')];
  // A row done says how far the list got, every open stage included.
  assert.deepEqual(todoCue(view(done), since), { key: 'done 2', words: '2 of 3 done, 1 to do' });
  const blocked = [...done, row('d', 'blocked', { changed_at: at(20), title: 'Sign in' })];
  assert.deepEqual(todoCue(view(blocked), since), { key: 'blocked d', words: 'Blocked: Sign in' });
  // Changes from before the turn and rows without a time say nothing.
  assert.equal(todoCue(view([row('a', 'done', { changed_at: '2026-10-08T11:59:00Z' }), row('b', 'blocked')]), since), undefined);
  assert.equal(todoLine(view(done, { touched: false }), since)?.kind, 'carried');
});

test('the live reader\'s sections: In progress (the Now row first), Blocked, To do and Done, each newest first; empty ones left out', () => {
  const todos = [row('a', 'done'), row('b', 'in_progress'), row('c', 'pending'), row('d', 'in_progress'), row('e', 'done'), row('f', 'blocked')];
  const sections = todoSections(view(todos, { now: 'b' }));
  assert.deepEqual(sections.map((s) => [s.status, s.label, s.todos.map((t) => t.id)]), [['in_progress', 'In progress', ['b', 'd']], ['blocked', 'Blocked', ['f']], ['pending', 'To do', ['c']], ['done', 'Done', ['e', 'a']]]);
  // A row seen changing comes before those never seen to change, the latest change first.
  const changed = [row('g', 'done', { changed_at: '2026-10-28T10:00:00Z' }), row('h', 'done', { changed_at: '2026-10-28T11:00:00Z' }), row('i', 'done')];
  assert.deepEqual(todoSections(view(changed))[0].todos.map((t) => t.id), ['h', 'g', 'i']);
  assert.deepEqual(todoSections(view([row('a', 'done'), row('c', 'pending')])).map((s) => s.label), ['To do', 'Done']);
  assert.deepEqual(['in_progress', 'blocked', 'pending', 'done'].map(todoWord), ['In progress', 'Blocked', 'To do', 'Done']);
});

test('a reply\'s foot names every count it has: done of total, then each open stage with rows', () => {
  assert.equal(turnTodoName({ done: 7, total: 7 }), 'Todo at the end of this turn: 7 of 7 done');
  assert.equal(turnTodoName({ total: 3, pending: 3, open: 3 }), 'Todo at the end of this turn: 0 of 3 done, 3 to do');
  assert.equal(turnTodoName({ done: 4, total: 7, blocked: 1, in_progress: 1, pending: 1, open: 2 }), 'Todo at the end of this turn: 4 of 7 done, 1 in progress, 1 blocked, 1 to do');
  // Kept before the split: the open rows stay one count.
  assert.equal(turnTodoName({ done: 5, total: 7, blocked: 1, open: 1 }), 'Todo at the end of this turn: 5 of 7 done, 1 blocked, 1 left open');
  assert.equal(turnTodoName({ total: 3, open: 3 }), 'Todo at the end of this turn: 0 of 3 done, 3 left open');
});

test('the kept list: the live reader\'s stages, newest first; its facts say when, how many were left open or all done, and the rows not kept', () => {
  const todos = [row('z', 'blocked'), row('y', 'in_progress'), row('p', 'pending'), row('w', 'done'), row('x', 'done')];
  assert.deepEqual(snapshotSections(todos).map((s) => [s.label, s.todos.map((t) => t.id)]), [['In progress', ['y']], ['Blocked', ['z']], ['To do', ['p']], ['Done', ['x', 'w']]]);
  assert.deepEqual(snapshotSections([row('w', 'done')]).map((s) => s.label), ['Done']);
  assert.deepEqual(snapshotFacts({ done: 2, total: 5, blocked: 1, in_progress: 1, pending: 1, open: 2 }, '14:02'), ['As this turn left it, 14:02', '2 left open']);
  assert.deepEqual(snapshotFacts({ done: 2, total: 3, blocked: 1 }, '14:02'), ['As this turn left it, 14:02']);
  assert.deepEqual(snapshotFacts({ done: 7, total: 7 }, '9:15'), ['As this turn left it, 9:15', 'all done']);
  assert.deepEqual(snapshotFacts({ done: 0, total: 62, pending: 62, open: 62, omitted: 12 }, '9:15'), ['As this turn left it, 9:15', '62 left open', '12 more not shown']);
});

test('kept lists stay in memory up to a few, the least recently read going first', () => {
  const snap = (id) => ({ timing_id: id, ended_at: '', todos: [], counts: {} });
  for (let i = 0; i < KEPT_SNAPSHOTS; i++) keepSnapshot('task', `t${i}`, snap(`t${i}`));
  // Reading t0 makes t1 the oldest.
  assert.equal(recallSnapshot('task', 't0')?.timing_id, 't0');
  keepSnapshot('task', 'new', snap('new'));
  assert.equal(recallSnapshot('task', 't1'), undefined);
  assert.equal(recallSnapshot('task', 't0')?.timing_id, 't0');
  assert.equal(recallSnapshot('task', 'new')?.timing_id, 'new');
  // Keyed by Task too.
  assert.equal(recallSnapshot('other', 'new'), undefined);
});

test('the ledger merges every row the turns kept by id: the latest state wins, the stamp is the turn that changed its status, the live list is newest, and its missing rows read as removed', () => {
  const at = (m) => `2026-10-22T13:${String(m).padStart(2, '0')}:00Z`;
  const timings = [{ id: 'tt1' }, { id: 'tt2' }, { id: 'tt3' }];
  const rec = (timing_id, ended_at, todos) => ({ timing_id, ended_at, todos, counts: {} });
  const turns = [
    // A row without a time stamps the turn's end.
    rec('tt1', at(41), [row('a', 'pending', { changed_at: at(40) }), row('b', 'done')]),
    rec('tt2', at(58), [row('a', 'in_progress', { title: 'Row a, renamed', changed_at: at(52) }), row('c', 'blocked', { note: 'no token', changed_at: at(53) }), row('d', 'done', { changed_at: at(57) })]),
  ];
  // The live list confirms a, b and c (their stamps stay), lacks d (removed) and adds e (the running turn's, no turn yet).
  const live = view([row('a', 'in_progress', { title: 'Row a, renamed', changed_at: at(52) }), row('b', 'done'), row('c', 'blocked', { note: 'no token' }), row('e', 'in_progress', { changed_at: at(63) })]);
  const flat = (rows) => rows.map((r) => [r.todo.id, r.todo.status, r.todo.title, r.at, r.turn, r.removed]);
  assert.deepEqual(flat(todoLedger({ live, turns }, timings)), [
    ['a', 'in_progress', 'Row a, renamed', at(52), 2, false],
    ['b', 'done', 'Row b', at(41), 1, false],
    ['c', 'blocked', 'Row c', at(53), 2, false],
    // In the live list's order, then the rows it no longer has.
    ['e', 'in_progress', 'Row e', at(63), undefined, false],
    ['d', 'done', 'Row d', at(57), 2, true],
  ]);
  assert.equal(todoLedger({ live, turns }, timings)[2].todo.note, 'no token');
  // The live list changing a kept row's status stamps its time without a turn.
  const moved = view([row('b', 'in_progress', { changed_at: at(64) })]);
  assert.deepEqual(flat(todoLedger({ live: moved, turns }, timings)).find((r) => r[0] === 'b'), ['b', 'in_progress', 'Row b', at(64), undefined, false]);
  // No live list, or one not known or cut short: no row reads as removed.
  for (const live of [null, { ...view([]), known: false }, { ...view([row('a', 'in_progress')]), omitted: 3 }]) {
    assert.ok(todoLedger({ live, turns }, timings).every((r) => !r.removed), JSON.stringify(live));
  }
  // A record of a turn the Thread no longer holds stamps no turn; the held turns count from one.
  assert.deepEqual(todoLedger({ live: null, turns }, [{ id: 'tt2' }]).map((r) => r.turn), [1, undefined, 1, 1]);
  assert.deepEqual(todoLedger({ live: null, turns: [] }), []);
});

test('a ledger row\'s stamp: its state and time, the turn that changed it; the sections by stage, newest change first', () => {
  const stamp = (status, at, turn) => ledgerStamp({ todo: row('a', status), at, turn, removed: false }, at ? '13:58' : '');
  assert.equal(stamp('done', 'x', 2), 'done 13:58 · turn 2');
  assert.equal(stamp('in_progress', 'x', 3), 'in progress since 13:58 · turn 3');
  assert.equal(stamp('blocked', 'x', 2), 'blocked 13:58 · turn 2');
  assert.equal(stamp('pending', 'x', 1), 'added 13:58 · turn 1');
  assert.equal(stamp('in_progress', 'x'), 'in progress since 13:58');
  assert.equal(stamp('pending'), 'added');
  const at = (m) => `2026-10-22T13:${String(m).padStart(2, '0')}:00Z`;
  const ledger = (id, status, at, turn) => ({ todo: row(id, status), at, turn, removed: false });
  const sections = ledgerSections([ledger('a', 'done', at(41), 1), ledger('b', 'done', at(58), 2), ledger('c', 'in_progress', at(52), 2), ledger('d', 'in_progress', undefined), ledger('e', 'pending', at(40), 1)]);
  assert.deepEqual(sections.map((s) => [s.label, s.todos.map((t) => t.id)]), [['In progress', ['d', 'c']], ['To do', ['e']], ['Done', ['b', 'a']]]);
});

test('rails: none without a dependency between rows of the list, nor for one on itself or a row not there', () => {
  assert.equal(todoLinks([row('a', 'done'), row('b', 'pending')]), null);
  assert.equal(todoLinks([row('a', 'done', { depends_on: ['a', 'gone'] })]), null);
});

test('rails: one lane per row waited on, loops beside the rows, a blocked row\'s wait on an open row in its own kind', () => {
  // The r20 fixture: contrast and tokens wait on each other; lint is blocked on install and alt; summary waits on four.
  const todos = [
    row('plan', 'done'),
    row('alt', 'in_progress', { depends_on: ['plan'] }),
    row('headings', 'done', { depends_on: ['plan'] }),
    row('contrast', 'in_progress', { depends_on: ['plan', 'tokens'] }),
    row('tokens', 'done', { depends_on: ['contrast'] }),
    row('install', 'pending'),
    row('lint', 'blocked', { depends_on: ['install', 'alt'] }),
    row('summary', 'pending', { depends_on: ['alt', 'headings', 'contrast', 'lint'] }),
  ];
  const r = todoLinks(todos);
  assert.equal(r.loops, 1);
  assert.equal(r.hidden, 0);
  const kind = (from, to) => r.links.find((l) => l.from === from && l.to === to)?.kind;
  assert.equal(kind(3, 4), 'loop');
  assert.equal(kind(4, 3), 'loop');
  assert.equal(kind(5, 6), 'wait');
  assert.equal(kind(1, 6), 'wait');
  assert.equal(kind(0, 1), 'link');
  assert.equal(kind(6, 7), 'link');
  assert.equal(r.links.length, 11);
  // The loop links take the lanes beside the rows; the links from one row share a lane; overlapping spans never do.
  assert.deepEqual([...new Set(r.links.filter((l) => l.kind === 'loop').map((l) => l.lane))].sort(), [0, 1]);
  for (const from of [0, 1, 3]) assert.equal(new Set(r.links.filter((l) => l.from === from && l.kind !== 'loop').map((l) => l.lane)).size, 1, `row ${from} trunk`);
  const spans = new Map();
  for (const l of r.links) {
    const key = l.kind === 'loop' ? `${l.from}>${l.to}` : `${l.from}`;
    const s = spans.get(key) ?? { lane: l.lane, lo: l.from, hi: l.from };
    spans.set(key, { lane: l.lane, lo: Math.min(s.lo, l.to), hi: Math.max(s.hi, l.to) });
  }
  const all = [...spans.values()];
  for (const a of all) for (const b of all) if (a !== b && a.lane === b.lane) assert.ok(a.hi < b.lo || b.hi < a.lo, `lane ${a.lane} overlaps`);
  assert.equal(r.lanes, Math.max(...all.map((s) => s.lane)) + 1);
});

test('rails: a ring through three rows is one loop; links past the lanes are counted, not drawn', () => {
  const ring = todoLinks([row('a', 'pending', { depends_on: ['c'] }), row('b', 'pending', { depends_on: ['a'] }), row('c', 'pending', { depends_on: ['b'] })]);
  assert.equal(ring.loops, 1);
  assert.ok(ring.links.every((l) => l.kind === 'loop'));
  // Nested spans, each overlapping all the others: a lane each, then hidden.
  const n = MAX_LANES + 2;
  const wide = Array.from({ length: 2 * n }, (_, i) => row(`r${i}`, 'pending'));
  for (let i = 0; i < n; i++) wide[2 * n - 1 - i].depends_on = [`r${i}`];
  const r = todoLinks(wide);
  assert.equal(r.lanes, MAX_LANES);
  assert.equal(r.links.length + r.hidden, MAX_LANES + 2);
  assert.equal(r.hidden, 2);
});

test('the ledger counts a row seen done in one turn and open in a later one as reopened, unless uam counted more', () => {
  const rec = (id, todos) => ({ timing_id: id, ended_at: '2026-10-27T10:00:00Z', todos, counts: {} });
  const history = {
    live: { known: true, touched: false, todos: [row('a', 'in_progress'), row('b', 'done', { reopened: 3 })], counts: {} },
    turns: [rec('t1', [row('a', 'done'), row('b', 'done')]), rec('t2', [row('a', 'in_progress'), row('b', 'in_progress')]), rec('t3', [row('a', 'done'), row('b', 'done')])],
  };
  const ledger = new Map(todoLedger(history).map((r) => [r.todo.id, r.todo.reopened]));
  assert.equal(ledger.get('a'), 2);
  assert.equal(ledger.get('b'), 3);
  assert.equal(todoLedger({ live: null, turns: [rec('t1', [row('c', 'done')])] })[0].todo.reopened, undefined);
});
