import assert from 'node:assert/strict';
import test from 'node:test';
import { KEPT_SNAPSHOTS, METER_ROWS, keepSnapshot, recallSnapshot, snapshotFacts, snapshotSections, todoCue, todoLine, todoSections, todoSentence, todoWord, turnTodoName } from '../src/lib/todos.ts';

const row = (id, status, extra = {}) => ({ id, title: `Row ${id}`, status, ...extra });
const view = (todos, extra = {}) => {
  const count = (s) => todos.filter((t) => t.status === s).length;
  return {
    known: true,
    touched: true,
    todos,
    counts: { total: todos.length, done: count('done'), blocked: count('blocked'), open: count('pending') + count('in_progress') },
    ...extra,
  };
};

test('the line says nothing of a list it does not know, an empty one, or one untouched with nothing open', () => {
  assert.equal(todoLine(undefined), undefined);
  assert.equal(todoLine({ known: false, touched: true, todos: [row('a', 'pending')], counts: { total: 1, open: 1 } }), undefined);
  assert.equal(todoLine(view([])), undefined);
  assert.equal(todoLine(view([row('a', 'done')], { touched: false })), undefined);
});

test('a list the turn has not touched yet reads as the last turn\'s open rows, blocked ones included', () => {
  assert.deepEqual(todoLine(view([row('a', 'done'), row('b', 'pending'), row('c', 'blocked'), row('d', 'in_progress')], { touched: false })), { kind: 'carried', open: 3 });
  assert.equal(todoSentence({ kind: 'carried', open: 3 }), '. Todo: 3 open from the last turn');
});

test('a touched list: counts, the Now row with how many more are in progress, the meter up to a dozen rows', () => {
  const todos = [row('a', 'done'), row('b', 'in_progress'), row('c', 'in_progress'), row('d', 'blocked'), row('e', 'pending')];
  const line = todoLine(view(todos, { now: 'c' }));
  assert.deepEqual(line, { kind: 'list', done: 1, total: 5, blocked: 1, now: 'Row c', more: 1, meter: ['done', 'in_progress', 'in_progress', 'blocked', 'pending'], cue: undefined });
  assert.equal(todoSentence(line), '. Todo 1 of 5 done, 1 blocked. Now: Row c, and 1 more in progress');
  // No row named Now: no "+N" either.
  assert.equal(todoLine(view(todos)).more, 0);
  assert.equal(todoSentence(todoLine(view([row('a', 'done')]))), '. Todo 1 of 1 done');
  // Past a dozen rows, or with rows left out, the words stand alone.
  assert.equal(todoLine(view(Array.from({ length: METER_ROWS + 1 }, (_, i) => row(`r${i}`, 'pending')))).meter, undefined);
  assert.equal(todoLine({ ...view([row('a', 'pending')]), counts: { total: 101, open: 101 }, omitted: 100 }).meter, undefined);
});

test('the cue is the turn\'s latest row done or blocked; a blocked one wins a tie; older changes are not news', () => {
  const since = '2026-10-08T12:00:00Z';
  const at = (s) => `2026-10-08T12:00:${String(s).padStart(2, '0')}Z`;
  const done = [row('a', 'done', { changed_at: at(10) }), row('b', 'done', { changed_at: at(20) }), row('c', 'pending')];
  assert.deepEqual(todoCue(view(done), since), { key: 'done 2', words: '2 of 3 done' });
  const blocked = [...done, row('d', 'blocked', { changed_at: at(20), title: 'Sign in' })];
  assert.deepEqual(todoCue(view(blocked), since), { key: 'blocked d', words: 'Blocked: Sign in' });
  // Changes from before the turn and rows without a time say nothing.
  assert.equal(todoCue(view([row('a', 'done', { changed_at: '2026-10-08T11:59:00Z' }), row('b', 'blocked')]), since), undefined);
  assert.equal(todoLine(view(done, { touched: false }), since)?.kind, 'carried');
});

test('the reader\'s sections: Now first by the Now row, then Blocked, Next and Done; empty ones left out', () => {
  const todos = [row('a', 'done'), row('b', 'in_progress'), row('c', 'pending'), row('d', 'in_progress'), row('e', 'done')];
  const sections = todoSections(view(todos, { now: 'd' }));
  assert.deepEqual(sections.map((s) => [s.label, s.todos.map((t) => t.id)]), [['Now', ['d', 'b']], ['Next', ['c']], ['Done', ['a', 'e']]]);
  assert.deepEqual(['in_progress', 'blocked', 'pending', 'done'].map(todoWord), ['Now', 'Blocked', 'Next', 'Done']);
});

test('a reply\'s foot names every count it has: done of total, then blocked and left open only when there are some', () => {
  assert.equal(turnTodoName({ done: 7, total: 7 }), 'Todo at the end of this turn: 7 of 7 done');
  assert.equal(turnTodoName({ total: 3, open: 3 }), 'Todo at the end of this turn: 0 of 3 done, 3 left open');
  assert.equal(turnTodoName({ done: 5, total: 7, blocked: 1, open: 1 }), 'Todo at the end of this turn: 5 of 7 done, 1 blocked, 1 left open');
  assert.equal(turnTodoName({ done: 1, total: 3, open: 2 }), 'Todo at the end of this turn: 1 of 3 done, 2 left open');
});

test('the kept list: Blocked, Left open and Done in the kept order; its facts say when, all done, and the rows not kept', () => {
  const todos = [row('z', 'blocked'), row('y', 'in_progress'), row('p', 'pending'), row('w', 'done'), row('x', 'done')];
  assert.deepEqual(snapshotSections(todos).map((s) => [s.label, s.todos.map((t) => t.id)]), [['Blocked', ['z']], ['Left open', ['y', 'p']], ['Done', ['w', 'x']]]);
  assert.deepEqual(snapshotSections([row('w', 'done')]).map((s) => s.label), ['Done']);
  assert.deepEqual(snapshotFacts({ done: 2, total: 5, blocked: 1, open: 2 }, '14:02'), ['As this turn left it, 14:02']);
  assert.deepEqual(snapshotFacts({ done: 7, total: 7 }, '9:15'), ['As this turn left it, 9:15', 'all done']);
  assert.deepEqual(snapshotFacts({ done: 0, total: 62, open: 62, omitted: 12 }, '9:15'), ['As this turn left it, 9:15', '12 more not shown']);
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
