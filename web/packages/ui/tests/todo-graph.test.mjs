import assert from 'node:assert/strict';
import test from 'node:test';
import { parseTodoGraph, todoBoard } from '../src/lib/todoGraph.ts';
import { todoLedger, todoOrigins } from '../src/lib/todos.ts';

const row = (id, status, extra = {}) => ({ id, title: `Row ${id}`, status, ...extra });

test('the setting: "graph" (the board, the value the Mermaid graph stored) as chosen, anything else the rails', () => {
  assert.equal(parseTodoGraph('graph'), 'graph');
  for (const raw of [null, '', 'rails', 'GRAPH', 'mermaid']) assert.equal(parseTodoGraph(raw), 'rails');
});

test('the board: columns are the turns that added the rows, oldest first; lanes the main agent then each subagent; arrows per dependency', () => {
  const timings = [{ id: 't1' }, { id: 't2' }, { id: 't3' }];
  const history = {
    live: null,
    turns: [
      { timing_id: 't1', ended_at: 'T1', todos: [row('a', 'done'), row('b', 'pending', { agent_id: 's2' })], counts: {} },
      { timing_id: 't2', ended_at: 'T2', todos: [row('c', 'in_progress', { agent_id: 's1' })], counts: {} },
    ],
  };
  const todos = [
    row('a', 'done'),
    row('b', 'pending', { agent_id: 's2', depends_on: ['a', 'a', 'gone'] }),
    row('c', 'in_progress', { agent_id: 's1', depends_on: ['a'] }),
    row('d', 'blocked', { depends_on: ['c', 'a', 'd'] }),
  ];
  const board = todoBoard(todos, todoOrigins(history, timings));
  assert.deepEqual(board.columns.map((c) => c.key), ['t1', 't2', 't3']);
  assert.deepEqual(board.lanes, [undefined, 's2', 's1']);
  assert.deepEqual(board.cards.map((c) => [c.todo.id, c.col, c.lane]), [['a', 0, 0], ['b', 0, 1], ['c', 1, 2], ['d', 2, 0]]);
  // One arrow per dependency held on the board; a blocked row's wait on a row not done is a wait.
  assert.deepEqual(board.edges, [
    { from: 'a', to: 'b', kind: 'link' },
    { from: 'a', to: 'c', kind: 'link' },
    { from: 'c', to: 'd', kind: 'wait' },
    { from: 'a', to: 'd', kind: 'link' },
  ]);
  // Only subagents' rows: no main lane.
  assert.deepEqual(todoBoard([row('x', 'pending', { agent_id: 's1' })], todoOrigins(history, timings)).lanes, ['s1']);
});

test('the ledger says which turn first held each row, whatever changed it since', () => {
  const rec = (id, todos) => ({ timing_id: id, ended_at: '2026-10-27T10:00:00Z', todos, counts: {} });
  const timings = [{ id: 't1' }, { id: 't2' }];
  const history = { live: { known: true, touched: true, todos: [row('a', 'done'), row('b', 'in_progress'), row('c', 'pending')], counts: {} }, turns: [rec('t1', [row('a', 'pending')]), rec('t2', [row('a', 'done'), row('b', 'pending')])] };
  const added = Object.fromEntries(todoLedger(history, timings).map((r) => [r.todo.id, r.added]));
  assert.deepEqual(added, { a: 1, b: 2, c: undefined });
});
