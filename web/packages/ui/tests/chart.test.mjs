import assert from 'node:assert/strict';
import test from 'node:test';
import { chartCsv, chartHeight, chartOption, chartTheme, headline, isChartCall, niceCeil, seriesColorIndexes, labelInterval } from '../src/lib/chart.ts';
import { callProduct } from '../src/lib/transcript.ts';
import { init } from '../src/lib/echarts.ts';
import * as charts from 'echarts/charts';
import { registerTheme, use } from 'echarts/core';

// The app loads each chart type with its first drawing (EChart.tsx); these tests draw with init directly.
use(Object.values(charts));

const chart = {
  title: 'Commits "per" day',
  kind: 'line',
  x_label: 'Day',
  y_label: 'Commits',
  x: 'day',
  labels: ['09-01', '09-02', '09-03'],
  series: [{ name: 'commits', values: [3, 7, 2] }],
};

const pie = {
  ...chart, kind: 'echarts', options: {
    legend: { left: 'left', orient: 'vertical', top: 'middle' },
    series: [{ type: 'pie', center: ['62%', '52%'], radius: ['38%', '68%'],
      label: { formatter: '{b}\\n{d}%' },
      data: [{ name: 'TypeScript', value: 70 }, { name: 'Go', value: 29 }, { name: 'Other', value: 1 }],
    }],
  },
};

test('saved label formatters display escaped newlines without rewriting chart data', () => {
  const options = { ...pie.options, dataset: { source: [{ label: { formatter: '{b}\\n{d}%' }, name: 'literal\\nname' }] } };
  const before = structuredClone(options);
  const drawing = chartOption({ ...pie, options }, { width: 360, height: 260 });
  assert.equal(drawing.series[0].label.formatter, '{b}\n{d}%');
  assert.deepEqual(drawing.dataset.source, options.dataset.source);
  assert.deepEqual(options, before);
});

test('a single pie moves its side legend below on phones and restores the desktop layout', () => {
  const drawing = init(null, undefined, { renderer: 'svg', ssr: true, width: 360, height: 260 });
  const before = structuredClone(pie.options);
  try {
    drawing.setOption(chartOption(pie, { width: 360, height: 260 }));
    const mobile = drawing.getOption();
    assert.equal(mobile.legend[0].orient, 'horizontal');
    assert.equal(mobile.legend[0].bottom, 0);
    assert.deepEqual(mobile.series[0].center, ['50%', '50%']);
    assert.equal(mobile.series[0].label.overflow, 'break');
    assert.ok(!drawing.renderToSVGString().includes('\\n'));
    assert.deepEqual(mobile.series[0].data, pie.options.series[0].data);
    drawing.resize({ width: 840, height: 320 });
    drawing.setOption(chartOption(pie, { width: 840, height: 320 }), { notMerge: true });
    const desktop = drawing.getOption();
    assert.equal(desktop.legend[0].orient, 'vertical');
    assert.deepEqual(desktop.series[0].center, pie.options.series[0].center);
    assert.deepEqual(desktop.series[0].radius, pie.options.series[0].radius);
    assert.deepEqual(pie.options, before);
  } finally { drawing.dispose(); }
  const media = [{ query: { maxWidth: 480 }, option: { series: [{ center: ['40%', '40%'] }] } }];
  const authored = chartOption({ ...pie, options: { ...pie.options, media } }, { width: 360, height: 260 });
  assert.deepEqual(authored.media, media);
  assert.deepEqual(authored.series[0].center, pie.options.series[0].center);
});

test('horizontal bars use the available width, contain labels and zoom categories', () => {
  const options = {
    grid: { left: '28%', right: '12%', top: '5%', bottom: '7%', containLabel: true },
    xAxis: { type: 'value', name: 'Million tokens' },
    yAxis: { type: 'category', inverse: true, axisLabel: { fontSize: 10 },
      data: ['claude-opus-5-5', 'gpt-6-astra', 'claude-haiku-4-5-20251001', 'ollama/deepseek-v4.1-flash'] },
    series: [{ type: 'bar', data: [5430.5, 2115.6, 0.7, 0.1] }],
  };
  const before = structuredClone(options);
  for (const width of [280, 360, 840, 1600]) {
    const drawing = init(null, undefined, { renderer: 'svg', ssr: true, width, height: 260 });
    try {
      drawing.setOption(chartOption({ ...chart, kind: 'echarts', options }, { width, height: 260 }));
      const rect = drawing.getModel().getComponent('grid').coordinateSystem.getRect();
      assert.ok(rect.width >= width * (width < 480 ? 0.45 : 0.7), `only ${rect.width} plot pixels at width ${width}`);
      const mobile = drawing.getOption();
      assert.equal(mobile.xAxis[0].nameLocation, 'middle');
      assert.equal(mobile.xAxis[0].axisLabel.hideOverlap, true);
      assert.deepEqual(mobile.yAxis[0].data, options.yAxis.data);
      assert.deepEqual(mobile.series[0].data, options.series[0].data);
      assert.equal(mobile.dataZoom[0].yAxisIndex, 0);
      drawing.dispatchAction({ type: 'dataZoom', start: 0, end: 50 });
      assert.deepEqual(drawing.getModel().getComponent('yAxis').axis.scale.getExtent(), [0, 2]);
      assert.equal(drawing.getModel().getComponent('xAxis').axis.scale.getExtent()[1], 6000);
    } finally { drawing.dispose(); }
  }
  assert.deepEqual(options, before);
  const saved = { ...chart, kind: 'echarts', options };
  const media = [{ query: { maxWidth: 480 }, option: { grid: { left: 10 } } }];
  const authored = chartOption({ ...saved, options: { ...options, media } }, { width: 360, height: 260 });
  assert.deepEqual(authored.grid, options.grid);
  const zoom = [{ type: 'inside', xAxisIndex: 0, start: 10, end: 90 }];
  assert.equal(chartOption({ ...saved, options: { ...options, dataZoom: zoom } }, { width: 360, height: 260 }).dataZoom[0].xAxisIndex, 0);
});

test('a completed uam_chart call stands in the answer as a chart card; a refused one folds', () => {
  const call = { id: 'call-1', kind: 'tool', time: '2026-10-01T10:00:00Z', tool: { name: 'uam_chart', status: 'completed' } };
  assert.equal(isChartCall(call), true);
  assert.equal(callProduct(call), 'chart');
  for (const status of ['running', 'failed']) assert.equal(callProduct({ ...call, tool: { ...call.tool, status } }), null);
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

test('saved range sliders never draw a second control bar and keep header zoom usable', () => {
  for (const dataZoom of [
    [{ type: 'inside', xAxisIndex: 0 }, { type: 'slider', xAxisIndex: 0, start: 10, end: 90, height: 24, bottom: 5 }],
    { type: 'slider', xAxisIndex: 0, start: 10, end: 90, filterMode: 'none' },
    { xAxisIndex: 0, start: 10, end: 90 },
  ]) {
    const options = { xAxis: { type: 'category', data: chart.labels }, yAxis: { type: 'value' }, series: [{ type: 'line', data: [3, 7, 2] }], dataZoom };
    const saved = structuredClone(options);
    const rendered = chartOption({ ...chart, kind: 'echarts', options }, { width: 900, height: 300 });
    assert.ok(rendered.dataZoom.every(z => z.type === 'inside' || z.show === false));
    const index = rendered.dataZoom.findIndex(z => z.type === 'inside');
    assert.ok(index >= 0, 'the header controls and wheel still have an inside zoom');
    const drawing = init(null, undefined, { renderer: 'svg', ssr: true, width: 900, height: 300 });
    try {
      drawing.setOption(rendered);
      drawing.dispatchAction({ type: 'dataZoom', dataZoomIndex: index, start: 25, end: 75 });
      assert.equal(drawing.getOption().dataZoom[index].start, 25);
      assert.equal(drawing.getOption().dataZoom[index].end, 75);
    } finally { drawing.dispose(); }
    assert.deepEqual(options, saved, 'rendering leaves saved ranges and options unchanged');
  }
});

test('a hidden slider gives its margin back to the plot unless a legend or scale sits there', () => {
  const options = {
    legend: { top: 0 }, grid: { left: 60, right: 20, top: 60, bottom: 110, containLabel: true },
    xAxis: { type: 'category', name: 'Day', data: chart.labels }, yAxis: { type: 'value' }, series: [{ type: 'line', data: [3, 7, 2] }],
    dataZoom: [{ type: 'inside' }, { type: 'slider', bottom: 10, height: 30 }],
  };
  const saved = structuredClone(options);
  const rendered = chartOption({ ...chart, kind: 'echarts', options }, { width: 900, height: 320 });
  assert.equal(rendered.grid.bottom, 8);
  assert.equal(rendered.grid.containLabel, false);
  assert.equal(rendered.grid.left, 60);
  assert.deepEqual(options, saved);
  const drawing = init(null, undefined, { renderer: 'svg', ssr: true, width: 900, height: 320 });
  try {
    drawing.setOption(rendered);
    const rect = drawing.getModel().getComponent('grid').coordinateSystem.getRect();
    assert.ok(320 - (rect.y + rect.height) < 60, `${320 - (rect.y + rect.height)}px under the plot`);
  } finally { drawing.dispose(); }
  const vertical = chartOption({ ...chart, kind: 'echarts', options: { ...options, dataZoom: [{ type: 'inside' }, { type: 'slider', yAxisIndex: 0 }] } }, { width: 900, height: 320 });
  assert.deepEqual([vertical.grid.right, vertical.grid.bottom], [8, 110]);
  for (const kept of [{ legend: {} }, { legend: { bottom: 0 } }, { visualMap: { min: 0, max: 1 } }]) {
    assert.equal(chartOption({ ...chart, kind: 'echarts', options: { ...options, ...kept } }, { width: 900, height: 320 }).grid.bottom, 110);
  }
});

test('a top legend keeps one row and the grid starts below it, clear of the y axis name', () => {
  const options = {
    legend: { top: 0, data: ['a', 'b', 'c', 'd', 'e'] }, grid: { top: '12%' },
    xAxis: { type: 'category', data: chart.labels }, yAxis: { type: 'value', name: 'Recorded USD' },
    series: ['a', 'b', 'c', 'd', 'e'].map((name) => ({ type: 'line', name, data: [1, 2, 3] })),
  };
  const phone = chartOption({ ...chart, kind: 'echarts', options }, { width: 360, height: 260 });
  assert.equal(phone.legend.type, 'scroll');
  assert.equal(phone.grid.top, 56);
  // Two equal entries fill a phone page, so none shows cut off beside the pager.
  assert.deepEqual([phone.legend.width, phone.legend.textStyle.width, phone.legend.textStyle.overflow], [344, 80, 'truncate']);
  assert.equal(chartOption({ ...chart, kind: 'echarts', options }, { width: 900, height: 320 }).legend.textStyle, undefined);
  assert.equal(chartOption({ ...chart, kind: 'echarts', options: { ...options, grid: { top: 90 } } }, { width: 900, height: 320 }).grid.top, 90);
  // ECharts puts an unplaced legend at the bottom; that layout stays the author's.
  const bottom = chartOption({ ...chart, kind: 'echarts', options: { ...options, legend: {} } }, { width: 360, height: 260 });
  assert.deepEqual([bottom.legend, bottom.grid], [{}, options.grid]);
});

test('a draggable scale on the right keeps its handle labels off the plot', () => {
  const options = {
    grid: { left: 90, right: 20 }, xAxis: { type: 'category', data: ['a', 'b'] }, yAxis: { type: 'category', data: ['x'] },
    visualMap: { min: 0, max: 1300, calculable: true, right: 0, top: 'center' }, series: [{ type: 'heatmap', data: [[0, 0, 1300], [1, 0, 0]] }],
  };
  assert.ok(chartOption({ ...chart, kind: 'echarts', options }, { width: 360, height: 260 }).grid.right >= 70);
  assert.equal(chartOption({ ...chart, kind: 'echarts', options: { ...options, visualMap: { ...options.visualMap, calculable: false } } }, { width: 360, height: 260 }).grid.right, 20);
});

test('a category y axis labels every category on a card tall enough for them', () => {
  const names = Array.from({ length: 24 }, (_, i) => `model-${i}`);
  const options = { grid: { left: '20%' }, xAxis: { type: 'value' }, yAxis: { type: 'category', data: names }, series: [{ type: 'bar', data: names.map((_, i) => i) }] };
  const saved = { ...chart, kind: 'echarts', options };
  const height = chartHeight(saved, 320);
  assert.ok(height >= 24 * 20, `${height}px for 24 categories`);
  assert.equal(chartHeight(saved, 900), 900);
  assert.equal(chartHeight(chart, 264), 264);
  const tall = chartOption(saved, { width: 840, height });
  assert.equal(tall.yAxis.axisLabel.interval, 0);
  assert.equal(tall.yAxis.axisLabel.overflow, 'truncate');
  // A pinned preview stays short and lets ECharts thin the labels.
  assert.equal(chartOption(saved, { width: 560, height: 180 }).yAxis.axisLabel.interval, undefined);
  const drawing = init(null, undefined, { renderer: 'svg', ssr: true, width: 840, height });
  try {
    drawing.setOption(tall);
    const svg = drawing.renderToSVGString();
    assert.ok(names.every((name) => svg.includes(`>${name}<`)), 'every category is labelled');
  } finally { drawing.dispose(); }
});

test('a JSON valueFormatter becomes a template function or is dropped, so tooltips still draw', () => {
  const options = {
    tooltip: { trigger: 'axis', valueFormatter: '${value}' },
    xAxis: { type: 'category', data: ['a', 'b'] }, yAxis: { type: 'value' },
    series: [{ type: 'bar', name: 'spend', data: [613.76, 1160.1], tooltip: { valueFormatter: 'USD' } }],
  };
  const saved = structuredClone(options);
  const rendered = chartOption({ ...chart, kind: 'echarts', options }, { width: 600, height: 300 });
  assert.equal(typeof rendered.tooltip.valueFormatter, 'function');
  assert.equal(rendered.tooltip.valueFormatter(1160.1), `$${(1160.1).toLocaleString(undefined, { maximumFractionDigits: 2 })}`);
  assert.equal('valueFormatter' in rendered.series[0].tooltip, false);
  assert.deepEqual(options, saved);
  const drawing = init(null, undefined, { renderer: 'svg', ssr: true, width: 600, height: 300 });
  try {
    drawing.setOption(rendered);
    assert.doesNotThrow(() => drawing.dispatchAction({ type: 'showTip', seriesIndex: 0, dataIndex: 1 }));
  } finally { drawing.dispose(); }
});

test('responsive and base options cannot restore visible range sliders', () => {
  const options = { baseOption: { dataZoom: { type: 'slider', start: 20, end: 80 } }, media: [{ query: { maxWidth: 480 }, option: { dataZoom: [{ type: 'inside' }, { type: 'slider', show: true }] } }] };
  const rendered = chartOption({ ...chart, kind: 'echarts', options }, { width: 360, height: 260 });
  assert.equal(rendered.baseOption.dataZoom[0].type, 'inside');
  assert.equal(rendered.baseOption.dataZoom[0].start, 20);
  assert.equal(rendered.media[0].option.dataZoom[1].show, false);
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

test('the chart theme takes its colours from the tokens of the scheme on screen', () => {
  const schemes = {
    light: { 'badge-blue': '#3260c4', ink: '#25262b', body: '#494b53', hairline: '#e6e7ec', raised: '#ffffff', selection: '#cfdaf3' },
    dark: { 'badge-blue': '#86a6ff', ink: '#ededed', body: '#c2c2c2', hairline: '#1f1f1f', raised: '#121212', selection: '#213a6b' },
  };
  const root = { dataset: {} };
  globalThis.document = { documentElement: root };
  globalThis.getComputedStyle = () => ({ getPropertyValue: (name) => schemes[root.dataset.theme][name.replace('--color-', '')] ?? '' });
  try {
    for (const [scheme, t] of Object.entries(schemes)) {
      root.dataset.theme = scheme;
      const theme = chartTheme();
      assert.equal(theme.color[0], t['badge-blue']);
      assert.equal(theme.textStyle.color, t.body);
      assert.equal(theme.title.textStyle.color, t.ink);
      assert.equal(theme.valueAxis.splitLine.lineStyle.color, t.hairline);
      assert.equal(theme.tooltip.backgroundColor, t.raised);
      assert.deepEqual(theme.gradientColor, [t.selection, t['badge-blue']]);
      assert.equal(theme.pie.label.color, t.body);
      assert.equal(theme.treemap.label.color, t.raised);
      assert.equal(theme.sankey.label.color, t.body);
    }
  } finally {
    delete globalThis.document;
    delete globalThis.getComputedStyle;
  }
});

test('a saved specification\'s own colours win over the theme; the rest, line and bar charts included, take it', () => {
  const theme = chartTheme();
  registerTheme('uam-test', theme);
  const drawing = init(null, 'uam-test', { renderer: 'svg', ssr: true, width: 480, height: 300 });
  try {
    drawing.setOption(chartOption({ ...pie, options: { ...pie.options, title: { text: 'Languages' } } }, { width: 480, height: 300 }));
    assert.equal(drawing.getOption().title[0].textStyle.color, theme.title.textStyle.color);
    assert.deepEqual(drawing.getOption().color, theme.color);
    const own = { ...pie.options, color: ['#111111', '#222222'], title: { text: 'Languages', textStyle: { color: '#333333' } }, series: [{ ...pie.options.series[0], label: { color: '#444444' } }] };
    drawing.setOption(chartOption({ ...pie, options: own }, { width: 480, height: 300 }), { notMerge: true });
    assert.deepEqual(drawing.getOption().color, ['#111111', '#222222']);
    assert.equal(drawing.getOption().title[0].textStyle.color, '#333333');
    assert.equal(drawing.getOption().series[0].label.color, '#444444');
    drawing.setOption(chartOption(chart, { width: 480, height: 300 }), { notMerge: true });
    assert.equal(drawing.getOption().yAxis[0].splitLine.lineStyle.color, theme.valueAxis.splitLine.lineStyle.color);
    assert.equal(drawing.getOption().xAxis[0].axisLabel.color, theme.categoryAxis.axisLabel.color);
  } finally { drawing.dispose(); }
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
