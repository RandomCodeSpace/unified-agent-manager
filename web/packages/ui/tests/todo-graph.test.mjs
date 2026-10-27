import assert from 'node:assert/strict';
import test from 'node:test';
import { parseTodoGraph, todoMermaid } from '../src/lib/todoGraph.ts';
import { todoLedger, todoLinks } from '../src/lib/todos.ts';

const row = (id, status, extra = {}) => ({ id, title: `Row ${id}`, status, ...extra });

test('the setting: "graph" as chosen, anything else the rails', () => {
  assert.equal(parseTodoGraph('graph'), 'graph');
  for (const raw of [null, '', 'rails', 'GRAPH', 'mermaid']) assert.equal(parseTodoGraph(raw), 'rails');
});

test('the flowchart: a node per row by place, its glyph and title, reopens, the now row, and each link styled by kind', () => {
  const todos = [
    row('plan', 'done'),
    row('check', 'in_progress', { depends_on: ['plan', 'fix'], reopened: 2 }),
    row('fix', 'done', { depends_on: ['check'] }),
    row('install', 'pending'),
    row('lint', 'blocked', { depends_on: ['install'] }),
  ];
  const { links } = todoLinks(todos);
  const src = todoMermaid(todos, links, 'check');
  assert.match(src, /^%%\{init: .*\}%%\nflowchart TB\n/);
  assert.match(src, /\n {2}n0\["✓ Row plan"\]:::done\n/);
  assert.match(src, /\n {2}n1\["◉ Row check ↻2"\]:::now\n/);
  assert.match(src, /\n {2}n3\["○ Row install"\]:::pending\n/);
  assert.match(src, /\n {2}n4\["⊘ Row lint"\]:::blocked\n/);
  // Agent-written ids never reach the source.
  const odd = [row('x"] --> y', 'pending', { title: 'First' }), row('z', 'pending', { title: 'Second', depends_on: ['x"] --> y'] })];
  assert.doesNotMatch(todoMermaid(odd, todoLinks(odd).links), /x"\]|--> y/);
  // Each link's linkStyle names its own place among the links.
  const drawn = src.split('\n').filter((l) => /^ {2}n\d+ (-->|-\.->) n\d+$/.test(l));
  assert.equal(drawn.length, links.length);
  for (const [i, l] of links.entries()) {
    const style = src.split('\n').find((s) => s.startsWith(`  linkStyle ${i} `));
    if (l.kind === 'link') assert.equal(style, undefined);
    else assert.ok(style, `link ${i} (${l.kind}) has its style`);
    assert.equal(drawn[i], `  n${l.from} ${l.kind === 'loop' ? '-.->' : '-->'} n${l.to}`);
  }
});

test('the flowchart keeps agent text as text: Mermaid\'s syntax characters become entity codes on one line', () => {
  const todos = [row('a', 'pending', { title: 'Fix "quotes" & <tags>\n#1 end"] --> x' }), row('b', 'pending', { depends_on: ['a'] })];
  const src = todoMermaid(todos, todoLinks(todos).links);
  const line = src.split('\n').find((l) => l.startsWith('  n0['));
  assert.equal(line, '  n0["○ Fix #quot;quotes#quot; & #lt;tags#gt; #35;1 end#quot;] --#gt; x"]:::pending');
});

test('groups: each subagent\'s rows in a box named for it (cut to fit), the main agent\'s outside; the turn that added a row as a tag', () => {
  const todos = [row('a', 'done'), row('b', 'in_progress', { depends_on: ['a'], agent_id: 's1' }), row('c', 'pending', { depends_on: ['b'], agent_id: 's1' }), row('d', 'pending', { depends_on: ['a'] })];
  const name = 'Survey templates for missing alt text and labels';
  const group = (t) => ({ turn: { a: 1, b: 2, c: 2 }[t.id], agent: t.agent_id ? name : undefined });
  const lines = todoMermaid(todos, todoLinks(todos).links, undefined, group).split('\n');
  const at = (prefix) => lines.findIndex((l) => l.startsWith(prefix));
  assert.equal(lines[at('  subgraph g0')], '  subgraph g0["Survey templates for m…"]');
  assert.ok(at('  n0["✓ T1 · Row a"]') < at('  subgraph g0'));
  assert.ok(at('  n3["○ Row d"]') < at('  subgraph g0'), 'a row no turn holds has no tag');
  assert.ok(at('    n1["◉ T2 · Row b"]') > at('  subgraph g0') && at('    n2["○ T2 · Row c"]') < lines.indexOf('  end', at('  subgraph g0')));
  // Without groups: no boxes, no tags.
  assert.ok(!todoMermaid(todos, todoLinks(todos).links).includes('subgraph'));
});

test('the ledger says which turn first held each row, whatever changed it since', () => {
  const rec = (id, todos) => ({ timing_id: id, ended_at: '2026-10-27T10:00:00Z', todos, counts: {} });
  const timings = [{ id: 't1' }, { id: 't2' }];
  const history = { live: { known: true, touched: true, todos: [row('a', 'done'), row('b', 'in_progress'), row('c', 'pending')], counts: {} }, turns: [rec('t1', [row('a', 'pending')]), rec('t2', [row('a', 'done'), row('b', 'pending')])] };
  const added = Object.fromEntries(todoLedger(history, timings).map((r) => [r.todo.id, r.added]));
  assert.deepEqual(added, { a: 1, b: 2, c: undefined });
});
