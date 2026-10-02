import assert from 'node:assert/strict';
import test from 'node:test';
import { chartCsv, chartSource, headline, isChartCall, niceCeil, seriesColorIndexes, thinLabels } from '../src/lib/chart.ts';
import { callProduct } from '../src/lib/transcript.ts';

const chart = {
  title: 'Commits "per" day',
  kind: 'line',
  x_label: 'Day',
  y_label: 'Commits',
  x: 'day',
  labels: ['09-01', '09-02', '09-03'],
  series: [{ name: 'commits', values: [3, 7, 2] }],
};

test('a completed uam_chart call stands in the answer as a chart card; a refused one folds', () => {
  const call = { id: 'call-1', kind: 'tool', time: '2026-10-01T10:00:00Z', tool: { name: 'uam_chart', status: 'completed' } };
  assert.equal(isChartCall(call), true);
  assert.equal(callProduct(call, new Set()), 'chart');
  for (const status of ['running', 'failed']) assert.equal(callProduct({ ...call, tool: { ...call.tool, status } }, new Set()), null);
});

test('the Mermaid source is an xychart from zero to a round top, with quotes kept out of its strings', () => {
  const source = chartSource(chart, { width: 800, height: 300 });
  assert.match(source, /^---\nconfig: \{.*"width":800,"height":300.*\}\n---\nxychart-beta\n/);
  assert.match(source, /x-axis "Day" \["09-01", "09-02", "09-03"\]/);
  assert.match(source, /y-axis "Commits" 0 --> 8/);
  assert.match(source, /\n {2}line \[3, 7, 2\]$/);
  assert.doesNotMatch(source, /\n {2}title /);
  const spark = chartSource(chart, { width: 300, height: 64, spark: true });
  assert.match(spark, /"showLabel":false/);
  assert.doesNotMatch(spark, /"Day"|"Commits"/);
  // A bar chart's first series is its bars; the rest are lines, never stacked bars.
  const bars = chartSource({ ...chart, kind: 'bar', series: [...chart.series, { name: 'files', values: [1, 2, -1] }] }, { width: 800, height: 300 });
  assert.match(bars, /y-axis "Commits" -1 --> 8/);
  assert.match(bars, /\n {2}bar \[3, 7, 2\]\n {2}line \[1, 2, -1\]$/);
  assert.match(chartSource({ ...chart, x_label: 'a "b"\nc' }, { width: 800, height: 300 }), /x-axis "a 'b' c"/);
});

test('series colours are fixed: several take the palette in order, a lone one the tone its name hashes to', () => {
  const palette = (series) => JSON.parse(chartSource({ ...chart, series }, { width: 800, height: 300 }).split('\n')[1].slice('config: '.length)).themeVariables.xyChart.plotColorPalette;
  const named = (...names) => names.map((name) => ({ name, values: [1, 2, 3] }));
  // Blue, orange, bluish green, reddish purple, then violet: the badge tones, never red.
  assert.equal(palette(named('a', 'b')), '#3260c4, #a84c12');
  assert.equal(palette(named('a', 'b', 'c', 'd', 'e')), '#3260c4, #a84c12, #13756b, #b0347c, #6c4fc6');
  // One series: its name decides, the same on every draw and refresh, so measures differ.
  assert.equal(palette(named('commits')), palette(named('commits')));
  assert.equal(palette(named('commits')), '#a84c12');
  assert.equal(palette(named('lines')), '#6c4fc6');
  assert.equal(palette(named('open')), '#0e7089');
  assert.deepEqual(seriesColorIndexes(named('lines')), [4]);
});

test('crowded x labels thin out to every n-th, the rest distinct and invisible', () => {
  const labels = Array.from({ length: 30 }, (_, i) => `2026-09-${String(i + 1).padStart(2, '0')}`);
  assert.deepEqual(thinLabels(labels.slice(0, 5), 800), labels.slice(0, 5));
  const thin = thinLabels(labels, 400);
  assert.equal(thin.length, 30);
  assert.equal(new Set(thin).size, 30);
  const shown = thin.filter((l) => /\d/.test(l));
  assert.ok(shown.length > 1 && shown.length < 8, shown.join());
  assert.equal(thin[0], labels[0]);
});

test('CSV quotes what needs it; the headline is a line\'s latest value or a bar\'s total', () => {
  assert.equal(chartCsv({ x: 'day', labels: ['a,b', 'c'], series: [{ name: 'n "1"', values: [1, 2.5] }] }), 'day,"n ""1"""\n"a,b",1\nc,2.5\n');
  assert.deepEqual(headline(chart), { value: '2', note: 'commits at 09-03' });
  assert.deepEqual(headline({ ...chart, kind: 'bar' }), { value: '12', note: 'total commits, 3 bars' });
  assert.equal(headline({ ...chart, labels: [], series: [{ name: 'x', values: [] }] }), undefined);
  assert.equal(niceCeil(7), 8);
  assert.equal(niceCeil(41280), 50000);
  assert.equal(niceCeil(0), 0);
});
