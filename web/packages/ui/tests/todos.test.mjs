import assert from 'node:assert/strict';
import test from 'node:test';
import { KEPT_SNAPSHOTS, METER_ROWS, keepSnapshot, recallSnapshot, snapshotFacts, snapshotSections, tallyWords, todoCue, todoLine, todoSections, todoSentence, todoTally, todoWord, turnTodoName } from '../src/lib/todos.ts';

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

test('the live reader\'s sections: In progress (the Now row first), Blocked, To do and Done; empty ones left out', () => {
  const todos = [row('a', 'done'), row('b', 'in_progress'), row('c', 'pending'), row('d', 'in_progress'), row('e', 'done'), row('f', 'blocked')];
  const sections = todoSections(view(todos, { now: 'd' }));
  assert.deepEqual(sections.map((s) => [s.status, s.label, s.todos.map((t) => t.id)]), [['in_progress', 'In progress', ['d', 'b']], ['blocked', 'Blocked', ['f']], ['pending', 'To do', ['c']], ['done', 'Done', ['a', 'e']]]);
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

test('the kept list: the live reader\'s stages in the kept order; its facts say when, how many were left open or all done, and the rows not kept', () => {
  const todos = [row('z', 'blocked'), row('y', 'in_progress'), row('p', 'pending'), row('w', 'done'), row('x', 'done')];
  assert.deepEqual(snapshotSections(todos).map((s) => [s.label, s.todos.map((t) => t.id)]), [['In progress', ['y']], ['Blocked', ['z']], ['To do', ['p']], ['Done', ['w', 'x']]]);
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
