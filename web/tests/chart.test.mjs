import assert from 'node:assert/strict';
import test from 'node:test';
import { chartCsv, chartOption, headline, isChartCall, niceCeil, seriesColorIndexes, labelInterval } from '../src/lib/chart.ts';
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

test('ECharts draws data directly, with zero-based axes, mixed series, and axes-free sparklines', () => {
  const option = chartOption(chart, { width: 800, height: 300 });
  assert.deepEqual(option.xAxis.data, chart.labels);
  assert.equal(option.xAxis.name, 'Day');
  assert.equal(option.yAxis.name, 'Commits');
  assert.equal(option.yAxis.min, 0);
  assert.equal(option.yAxis.max, 8);
  assert.equal(option.series[0].type, 'line');
  assert.deepEqual(option.series[0].data, [3, 7, 2]);
  assert.equal(option.tooltip.renderMode, 'richText');
  assert.equal(option.dataZoom[0].zoomOnMouseWheel, true);
  assert.equal(option.dataZoom[0].moveOnMouseWheel, false);
  assert.deepEqual(option.dataZoom.map((z) => z.type), ['inside']);
  const spark = chartOption(chart, { width: 300, height: 64, spark: true });
  assert.equal(spark.xAxis.show, false);
  assert.equal(spark.yAxis.show, false);
  assert.equal(spark.tooltip.show, false);
  assert.deepEqual(spark.dataZoom, []);
  const bars = chartOption({ ...chart, kind: 'bar', series: [...chart.series, { name: 'files', values: [1, 2, -1] }] }, { width: 800, height: 300 });
  assert.equal(bars.yAxis.min, -1);
  assert.deepEqual(bars.series.map((s) => s.type), ['bar', 'line']);
  // Text remains data, including quotes and markup; there is no Mermaid/HTML interpolation.
  const unsafe = '<img src=x onerror=alert(1)> "quoted"';
  assert.equal(chartOption({ ...chart, x_label: unsafe, labels: [unsafe] }, { width: 800, height: 300 }).xAxis.data[0], unsafe);
});

test('advanced chart options keep literal data, disable animation and use text-only tooltips at every scope', () => {
  const options = {
    animation: true,
    tooltip: { renderMode: 'html' },
    series: [{ type: 'effectScatter', animation: true, rippleEffect: { number: 3 }, tooltip: { renderMode: 'html' }, data: [{ value: [2, 4], rippleEffect: { number: 5 } }] }],
    dataZoom: [{ type: 'inside', zoomOnMouseWheel: true, moveOnMouseWheel: true }],
  };
  const drawing = chartOption({ ...chart, kind: 'echarts', options }, { width: 800, height: 300 });
  assert.equal(drawing.animation, false);
  assert.equal(drawing.series[0].animation, false);
  assert.equal(drawing.tooltip.renderMode, 'richText');
  assert.equal(drawing.series[0].tooltip.renderMode, 'richText');
  assert.equal(drawing.series[0].rippleEffect.number, 0);
  assert.equal(drawing.series[0].data[0].rippleEffect.number, 0);
  assert.equal(drawing.dataZoom[0].zoomOnMouseWheel, true);
  assert.equal(drawing.dataZoom[0].moveOnMouseWheel, false);
  assert.equal(options.animation, true, 'rendering does not mutate saved data');
  const plotted = chartOption({ ...chart, kind: 'echarts', options: { xAxis: {}, yAxis: {}, series: [{ type: 'scatter', data: [[1, 2]] }] } }, { width: 800, height: 300 });
  assert.equal(plotted.dataZoom[0].type, 'inside');
  const graph = chartOption({ ...chart, kind: 'echarts', options: { series: [{ type: 'graph' }, { type: 'tree', roam: false }] } }, { width: 800, height: 300 });
  assert.equal(graph.series[0].roam, true);
  assert.equal(graph.series[1].roam, false);
});

test('dataset columns named like rendering controls remain unchanged', () => {
  const source = [{ day: 'Mon', animation: 5, tooltip: { renderMode: 'html' }, rippleEffect: { number: 4 }, type: 'effectScatter' }];
  for (const dataset of [{ source }, [{ source }]]) {
    const options = { dataset, xAxis: { type: 'category' }, yAxis: {}, series: [{ type: 'bar', encode: { x: 'day', y: 'animation' } }] };
    const drawing = chartOption({ ...chart, kind: 'echarts', options }, { width: 800, height: 300 });
    const drawn = Array.isArray(drawing.dataset) ? drawing.dataset[0].source : drawing.dataset.source;
    assert.deepEqual(drawn, source);
    assert.notEqual(drawn, source, 'the render options keep a separate copy of the stored data');
    assert.equal(drawing.series[0].animation, false, 'actual rendering options are still normalized');
  }
});

test('series colours are fixed: several take the palette in order, a lone one the tone its name hashes to', () => {
  const palette = (series) => chartOption({ ...chart, series }, { width: 800, height: 300 }).color.join(', ');
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

test('crowded axis labels thin out while categories and tooltip labels stay intact', () => {
  const labels = Array.from({ length: 30 }, (_, i) => `2026-09-${String(i + 1).padStart(2, '0')}`);
  assert.equal(labelInterval(labels.slice(0, 5), 800), 0);
  const option = chartOption({ ...chart, labels }, { width: 400, height: 300 });
  assert.deepEqual(option.xAxis.data, labels);
  const shown = Math.ceil(labels.length / (option.xAxis.axisLabel.interval + 1));
  assert.ok(shown > 1 && shown < 8);
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
