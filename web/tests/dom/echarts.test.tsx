import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { getInstanceByDom } from 'echarts/core';
import type { EChartsOption } from 'echarts';
import { StrictMode } from 'react';
import { expect, test, vi } from 'vitest';
import { EChart } from '../../src/components/EChart';
import { chartOption } from '../../src/lib/chart';

test('the actual SVG renderer draws chart data, replaces refreshed rows, resizes and disposes', async () => {
  const chart = { title: 'Commits', kind: 'bar' as const, labels: ['Monday', 'Tuesday'], series: [{ name: 'commits', values: [2, 8] }, { name: 'files', values: [1, 3] }] };
  const { rerender, unmount } = render(<StrictMode><EChart option={chartOption(chart, { width: 480, height: 240 })} width={480} height={240} label="Commits chart" /></StrictMode>);
  const host = screen.getByRole('img', { name: 'Commits chart' });
  await waitFor(() => expect(host.querySelector('svg path')).toBeTruthy());
  expect(host.textContent).toContain('Monday');
  expect(screen.queryByRole('status')).toBeNull();
  const wheel = vi.fn();
  host.addEventListener('wheel', wheel);
  const plain = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: 100 });
  host.dispatchEvent(plain);
  expect(plain.defaultPrevented).toBe(false);
  expect(wheel).not.toHaveBeenCalled();
  // happy-dom's WheelEvent extends UIEvent and omits MouseEvent's modifier fields.
  for (const key of ['ctrlKey', 'metaKey']) {
    const event = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: 100 });
    Object.defineProperty(event, key, { value: true });
    host.dispatchEvent(event);
  }
  expect(wheel).toHaveBeenCalledTimes(2);
  host.removeEventListener('wheel', wheel);
  const drawing = getInstanceByDom(host)!;
  drawing.dispatchAction({ type: 'dataZoom', start: 25, end: 75 });
  expect((drawing.getOption().dataZoom as { start: number; end: number }[])[0]).toMatchObject({ start: 25, end: 75 });
  const dispose = vi.spyOn(drawing, 'dispose');
  const next = { ...chart, labels: ['<img src=x onerror=alert(1)>'], series: [{ name: 'commits', values: [7] }] };
  rerender(<StrictMode><EChart option={chartOption(next, { width: 640, height: 200 })} width={640} height={200} label="Commits chart" /></StrictMode>);
  await waitFor(() => expect(host.querySelector('svg')?.getAttribute('width')).toBe('640'));
  expect(host.querySelector('svg')?.getAttribute('height')).toBe('200');
  expect(host.textContent).toContain('<img src=x onerror=alert(1)>');
  expect(host.textContent).not.toContain('Monday');
  expect(host.querySelector('img')).toBeNull();
  expect(getInstanceByDom(host)).toBe(drawing);
  expect((drawing.getOption().series as { data: number[] }[]).map((s) => s.data)).toEqual([[7]]);
  unmount();
  expect(dispose).toHaveBeenCalledOnce();
  expect(drawing.isDisposed()).toBe(true);
});

test('zoom buttons use the current range, stay within the data and reset after panning', async () => {
  const user = userEvent.setup();
  const chart = { title: 'Trend', kind: 'line' as const, labels: Array.from({ length: 21 }, (_, i) => String(i)), series: [{ name: 'commits', values: Array.from({ length: 21 }, (_, i) => i) }] };
  render(<EChart option={chartOption(chart, { width: 800, height: 320 })} width={800} height={320} label="Trend chart" zoomControls legend={<ul aria-label="Series"><li>commits</li></ul>} />);
  const host = screen.getByRole('img', { name: 'Trend chart' });
  const zoomIn = screen.getByRole('button', { name: 'Zoom in on chart' });
  await waitFor(() => expect(zoomIn.hasAttribute('disabled')).toBe(false));
  const drawing = getInstanceByDom(host)!;
  const range = () => (drawing.getOption().dataZoom as { type: string; start: number; end: number }[])[0];
  expect(range()).toMatchObject({ type: 'inside', start: 0, end: 100 });
  zoomIn.focus();
  await user.keyboard('{Enter}');
  expect(range()).toMatchObject({ start: 25, end: 75 });
  await user.click(screen.getByRole('button', { name: 'Zoom out on chart' }));
  expect(range()).toMatchObject({ start: 0, end: 100 });
  // Wheel zoom and drag-pan change the same ECharts range, independently of the buttons.
  drawing.dispatchAction({ type: 'dataZoom', start: 80, end: 100 });
  await user.click(screen.getByRole('button', { name: 'Zoom out on chart' }));
  expect(range()).toMatchObject({ start: 60, end: 100 });
  await user.click(zoomIn);
  expect(range()).toMatchObject({ start: 70, end: 90 });
  await user.click(screen.getByRole('button', { name: 'Reset chart zoom' }));
  expect(range()).toMatchObject({ start: 0, end: 100 });
});

test('a sparkline uses the same real renderer without axes, labels or zoom controls', async () => {
  const chart = { title: 'Trend', kind: 'line' as const, labels: ['Monday', 'Tuesday'], series: [{ name: 'commits', values: [2, 8] }] };
  render(<EChart option={chartOption(chart, { width: 300, height: 64, spark: true })} width={300} height={64} label="Trend chart" zoomControls />);
  const host = screen.getByRole('img', { name: 'Trend chart' });
  await waitFor(() => expect(host.querySelector('svg path')).toBeTruthy());
  expect(host.querySelector('svg')?.getAttribute('height')).toBe('64');
  expect(host.textContent).not.toContain('Monday');
  expect(host.querySelectorAll('text')).toHaveLength(0);
  expect(screen.queryByRole('group', { name: 'Chart zoom' })).toBeNull();
});

const category = { xAxis: { type: 'category' as const, data: ['a', 'b'] }, yAxis: { type: 'value' as const } };
const cartesian = { xAxis: { type: 'value' as const }, yAxis: { type: 'value' as const } };
const hierarchy = [{ name: 'root', value: 5, children: [{ name: 'first', value: 2 }, { name: 'second', value: 3 }] }];
const examples: Record<string, EChartsOption> = {
  line: { ...category, series: [{ type: 'line', data: [2, 3] }] },
  bar: { ...category, series: [{ type: 'bar', data: [2, 3] }] },
  pie: { series: [{ type: 'pie', data: [{ name: 'a', value: 2 }, { name: 'b', value: 3 }] }] },
  scatter: { ...cartesian, series: [{ type: 'scatter', data: [[1, 2], [2, 3]] }] },
  effectScatter: { ...cartesian, series: [{ type: 'effectScatter', data: [[1, 2], [2, 3]] }] },
  radar: { radar: { indicator: [{ name: 'a', max: 5 }, { name: 'b', max: 5 }, { name: 'c', max: 5 }] }, series: [{ type: 'radar', data: [{ value: [2, 3, 4] }] }] },
  tree: { series: [{ type: 'tree', data: hierarchy }] },
  treemap: { series: [{ type: 'treemap', data: hierarchy }] },
  sunburst: { series: [{ type: 'sunburst', data: hierarchy }] },
  boxplot: { ...category, series: [{ type: 'boxplot', data: [[1, 2, 3, 4, 5]] }] },
  candlestick: { ...category, series: [{ type: 'candlestick', data: [[2, 3, 1, 4]] }] },
  heatmap: { xAxis: { type: 'category', data: ['a', 'b'] }, yAxis: { type: 'category', data: ['c', 'd'] }, visualMap: { min: 0, max: 5 }, series: [{ type: 'heatmap', data: [[0, 0, 3], [1, 1, 5]] }] },
  parallel: { parallel: {}, parallelAxis: [{ dim: 0 }, { dim: 1 }], series: [{ type: 'parallel', data: [[1, 2], [2, 3]] }] },
  lines: { ...cartesian, series: [{ type: 'lines', coordinateSystem: 'cartesian2d', data: [{ coords: [[1, 2], [3, 4]] }] }] },
  graph: { series: [{ type: 'graph', layout: 'circular', data: [{ name: 'a' }, { name: 'b' }], links: [{ source: 'a', target: 'b' }] }] },
  sankey: { series: [{ type: 'sankey', data: [{ name: 'a' }, { name: 'b' }], links: [{ source: 'a', target: 'b', value: 2 }] }] },
  funnel: { series: [{ type: 'funnel', data: [{ name: 'a', value: 10 }, { name: 'b', value: 5 }] }] },
  gauge: { series: [{ type: 'gauge', data: [{ value: 50 }] }] },
  pictorialBar: { ...category, series: [{ type: 'pictorialBar', symbol: 'rect', data: [2, 3] }] },
  themeRiver: { singleAxis: { type: 'time' }, series: [{ type: 'themeRiver', data: [['2026-01-01', 10, 'a'], ['2026-01-02', 20, 'a'], ['2026-01-01', 5, 'b'], ['2026-01-02', 10, 'b']] }] },
  chord: { series: [{ type: 'chord', data: [{ name: 'a' }, { name: 'b' }], links: [{ source: 'a', target: 'b', value: 2 }] }] },
};

test.each(Object.entries(examples))('%s draws actual series geometry deterministically', async (kind, options) => {
  const option = chartOption({ kind: 'echarts', title: kind, labels: [], series: [], options }, { width: 480, height: 300 });
  render(<EChart option={option} width={480} height={300} label={kind} />);
  const host = screen.getByRole('img', { name: kind });
  await waitFor(() => expect(host.querySelector('svg path')).toBeTruthy());
  expect(screen.queryByRole('status')).toBeNull();
  const drawing = getInstanceByDom(host)!;
  const paths = () => [...host.querySelectorAll('svg path')].filter((p) => !p.closest('defs')).map((p) => p.getAttribute('d'));
  const first = paths();
  drawing.setOption({ ...option, series: [] }, { notMerge: true });
  drawing.getZr().flush();
  expect(paths()).not.toEqual(first);
  drawing.setOption(option, { notMerge: true });
  drawing.getZr().flush();
  expect(paths()).toEqual(first);
});
