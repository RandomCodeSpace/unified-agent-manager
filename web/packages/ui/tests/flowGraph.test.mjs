import assert from 'node:assert/strict';
import test from 'node:test';
import { FLOW_MAX_NODES, flowGraph, plainLabel } from '../src/lib/flowGraph.ts';

test('a flowchart reads as kinds, groups and edges: shapes fold, a subgraph an edge names stays a group, an invisible link drops', () => {
  const graph = flowGraph(`---
title: T
---
%% a comment
graph TD
  subgraph Outer[Outer group]
    subgraph Inner
      A["Line one<br/>line two"]
    end
    B@{ shape: cyl, label: "DB" }
  end
  A & B --> C((Circle))
  C ==> Outer
  C -.-> D{Ok?}
  D <-- "**bold** text" --> E[Plain]
  E --- F([Stop])
  C ~~~ F`);
  assert.ok(graph);
  assert.equal(graph.direction, 'TB');
  assert.deepEqual(graph.groups, [{ id: 'Inner', label: 'Inner', group: 'Outer' }, { id: 'Outer', label: 'Outer group', group: undefined }]);
  assert.deepEqual(graph.nodes.map((n) => [n.id, n.label, n.kind, n.group]), [
    ['A', 'Line one\nline two', 'step', 'Inner'],
    ['B', 'DB', 'store', 'Outer'],
    ['C', 'Circle', 'terminal', undefined],
    ['D', 'Ok?', 'decision', undefined],
    ['E', 'Plain', 'step', undefined],
    ['F', 'Stop', 'terminal', undefined],
  ]);
  assert.deepEqual(graph.edges.map((e) => [e.from, e.to, e.label, e.dashed, e.thick, e.head]), [
    ['A', 'C', undefined, false, false, 'end'],
    ['B', 'C', undefined, false, false, 'end'],
    ['C', 'Outer', undefined, false, true, 'end'],
    ['C', 'D', undefined, true, false, 'end'],
    ['D', 'E', 'bold text', false, false, 'both'],
    ['E', 'F', undefined, false, false, 'none'],
  ]);
});

test('LR and RL read left to right; other diagrams and unreadable sources are no graph, and an oversized chart says so', () => {
  assert.equal(flowGraph('flowchart RL\n  a --> b')?.direction, 'LR');
  assert.equal(flowGraph('sequenceDiagram\n  A->>B: hi'), null);
  assert.equal(flowGraph('flowchart LR\n  a -->'), null);
  const big = ['flowchart LR', ...Array.from({ length: FLOW_MAX_NODES }, (_, i) => `  n${i} --> n${i + 1}`)].join('\n');
  assert.equal(flowGraph(big), 'too-big');
});

test('labels lose tags and emphasis but keep their line breaks', () => {
  assert.equal(plainLabel('<b>Load</b> <i>cached</i><br>data'), 'Load cached\ndata');
  assert.equal(plainLabel('`code` and __under__ and *em*'), 'code and under and em');
});
