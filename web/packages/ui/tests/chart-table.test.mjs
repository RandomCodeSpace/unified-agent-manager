import assert from 'node:assert/strict';
import test from 'node:test';
import { chartTable } from '../src/lib/chart-table.ts';

const heatmap = (options) => ({ kind: 'echarts', options, labels: [], series: [] });
test('heatmap tuples resolve category indexes and keep zero and missing values', () => {
  const chart = heatmap({ xAxis: { name: 'Day', type: 'category', data: ['Mon', 'Tue'] }, yAxis: { name: 'Hour', type: 'category', data: [{ value: '09:00' }] }, series: [{ type: 'heatmap', name: 'Count', data: [[0, 0, 0], { value: [1, 0, null] }] }] });
  const before = JSON.stringify(chart);
  assert.deepEqual(chartTable(chart), { columns: ['Day', 'Hour', 'Count'], rows: [['Mon', '09:00', 0], ['Tue', '09:00', null]] });
  assert.equal(JSON.stringify(chart), before);
});
test('explicit dataset tables reuse named columns and rows', () => {
  const dataset = { sourceHeader: true, source: [['Day', 'Hour', 'Count'], ['Mon', '09:00', 3], ['Tue', '09:00', 0]] };
  assert.deepEqual(chartTable(heatmap({ dataset, series: [{ type: 'heatmap', encode: { x: 'Day', y: 'Hour', value: 'Count' } }] })), { columns: ['Day', 'Hour', 'Count'], rows: dataset.source.slice(1) });
  const objects = { dimensions: [{ name: 'Day' }, 'Count'], source: [{ Day: 'Mon', Count: 1 }, { Day: 'Tue', Count: 0 }] };
  assert.deepEqual(chartTable(heatmap({ dataset: objects, series: [{ type: 'bar' }] })), { columns: ['Day', 'Count'], rows: [['Mon', 1], ['Tue', 0]] });
});
test('data rows without a header are not consumed as column labels', () => {
  const dataset = { sourceHeader: false, dimensions: ['Day', 'Hour', 'Count'], source: [['Mon', '09:00', 3]] };
  assert.deepEqual(chartTable(heatmap({ dataset, series: [{ type: 'heatmap' }] })), { columns: dataset.dimensions, rows: dataset.source });
});
test('unsupported or ambiguous specifications keep their JSON view instead of a misleading table', () => {
  for (const options of [
    { series: [{ type: 'graph', data: [{ name: 'a' }], links: [] }] },
    { series: [{ type: 'heatmap', coordinateSystem: 'geo', data: [[1, 2, 3]] }] },
    { series: [{ type: 'heatmap', data: [[1, 2, 3]] }, { type: 'heatmap', data: [[4, 5, 6]] }] },
    { dataset: { source: [[1, 2]] }, series: [{ type: 'bar', data: [9] }] },
    { dataset: { sourceHeader: true, source: [['Name', 'Value'], ['a', { nested: 1 }]] }, series: [{ type: 'bar' }] },
    { xAxis: { type: 'category', data: ['Mon'] }, yAxis: { type: 'category', data: ['09:00'] }, series: [{ type: 'heatmap', data: [[7, 0, 1]] }] },
  ]) assert.equal(chartTable(heatmap(options)), null);
});
test('line and bar tables use the existing stored labels and series', () => {
  assert.deepEqual(chartTable({ kind: 'bar', x: 'day', x_label: 'Day', labels: ['Mon'], series: [{ name: 'Count', values: [0] }] }), { columns: ['Day', 'Count'], rows: [['Mon', 0]] });
});

test('calendar heatmaps expose dates without parsing or changing their timezone', () => {
  assert.deepEqual(chartTable(heatmap({ series: [{ type: 'heatmap', coordinateSystem: 'calendar', name: 'Commits', data: [['2026-10-04', 0], { value: ['2026-10-05', 7] }] }] })), { columns: ['Date', 'Commits'], rows: [['2026-10-04', 0], ['2026-10-05', 7]] });
});
