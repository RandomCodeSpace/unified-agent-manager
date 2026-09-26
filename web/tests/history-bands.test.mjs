import assert from 'node:assert/strict';
import test from 'node:test';
import { bandHeight, retainBands, windowEdges } from '../src/lib/historyBands.ts';

test('paging either direction identifies only the discarded edge', () => {
  assert.deepEqual(windowEdges(['b', 'c', 'd'], ['a', 'b', 'c']), { first: 'b', last: 'c', droppedBefore: [], droppedAfter: ['d'], addedBefore: ['a'], addedAfter: [] });
  assert.deepEqual(windowEdges(['a', 'b', 'c'], ['b', 'c', 'd']), { first: 'b', last: 'c', droppedBefore: ['a'], droppedAfter: [], addedBefore: [], addedAfter: ['d'] });
});

test('reloading and source trims release spacer identity and height', () => {
  const bands = [{ ids: ['a', 'b'], height: 80 }, { ids: ['c', 'd'], height: 120 }];
  const kept = retainBands(bands, id => id === 'd');
  assert.deepEqual(kept, [{ ids: ['d'], height: 60 }]);
  assert.equal(bandHeight(kept), 60);
  assert.deepEqual(retainBands(kept, () => false), []);
  assert.equal(bandHeight([]), 0);
});

test('collapsed rows contribute zero spacer height and no payload storage', () => {
  const band = { ids: ['tool-1', 'tool-2'], height: 0 };
  assert.deepEqual(retainBands([band], id => id === 'tool-2'), [{ ids: ['tool-2'], height: 0 }]);
  assert.deepEqual(Object.keys(band), ['ids', 'height']);
});

test('disjoint windows are identified without inventing an overlap', () => {
  const edges = windowEdges(['a', 'b'], ['c', 'd']);
  assert.equal(edges.first, undefined);
  assert.equal(edges.last, undefined);
  assert.deepEqual(edges.droppedBefore, []);
  assert.deepEqual(edges.droppedAfter, []);
});
