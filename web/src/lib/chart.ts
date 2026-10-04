/**
 * Charts an agent draws with `uam_chart` (docs/web.md "Charts"). The rows come from the service;
 * this supplies ECharts options, CSV and a headline value for a pinned chart.
 */
import type { EChartsOption } from 'echarts';
import type { Chart, Item } from '../api';

export const CHART_TOOL = 'uam_chart';

/** A completed `uam_chart` call has a chart card; a refused one is an ordinary failed call. */
export function isChartCall(item: Item): boolean {
  return item.kind === 'tool' && item.tool?.name === CHART_TOOL && item.tool.status === 'completed';
}

/**
 * Series colours: the badge tones (DESIGN.md), each at least 4.5:1 on `raised`. The first four
 * are the Okabe-Ito hues (blue, orange, bluish green, reddish purple), which stay apart under
 * the common colour-blindness types; red is left out so no chart reads as an error.
 */
export const SERIES_TOKENS = ['badge-blue', 'badge-orange', 'badge-teal', 'badge-pink', 'badge-violet', 'badge-cyan', 'badge-green'] as const;
const SERIES_FALLBACK = ['#3260c4', '#a84c12', '#13756b', '#b0347c', '#6c4fc6', '#0e7089', '#287541'];

/**
 * Each series' colour, as an index into SERIES_TOKENS. Several series take them in order, so
 * the first four always differ. A lone series takes the tone its name hashes to, so charts of
 * different measures differ and one measure keeps its colour. Both are stable across
 * refreshes: a pinned chart re-runs the same command with the same `y` fields in order.
 */
export function seriesColorIndexes(series: readonly { name: string }[]): number[] {
  if (series.length !== 1) return series.map((_, i) => i % SERIES_TOKENS.length);
  let hash = 0x811c9dc5; // FNV-1a
  for (const c of series[0].name) hash = Math.imul(hash ^ c.codePointAt(0)!, 0x01000193);
  return [(hash >>> 0) % SERIES_TOKENS.length];
}

let tokens: Record<string, string> | null = null;
/** A colour token's value from the live page, or the fallback (node tests have no page). */
function token(name: string, fallback: string): string {
  tokens ??= {};
  if (!(name in tokens)) tokens[name] = (typeof document === 'undefined' ? '' : getComputedStyle(document.documentElement).getPropertyValue(`--color-${name}`).trim()) || fallback;
  return tokens[name];
}
const seriesColors = () => SERIES_TOKENS.map((name, i) => token(name, SERIES_FALLBACK[i]));

/** A round upper bound for the y axis, so the top gridline reads as a plain number. */
export function niceCeil(v: number): number {
  if (v <= 0) return 0;
  const step = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (m * step >= v) return m * step;
  return 10 * step;
}

export interface ChartLook {
  width: number;
  height: number;
  /** A sparkline: no axes or labels, only the marks. */
  spark?: boolean;
  /** Compact axis padding for full conversation charts. */
  compact?: boolean;
}

/** Keep every category intact; ECharts skips crowded axis labels without changing the data. */
export function labelInterval(labels: string[], width: number): number {
  const longest = Math.max(1, ...labels.map((l) => l.length));
  const room = Math.max(1, Math.floor((width - 80) / (longest * 7 + 12)));
  return Math.max(0, Math.ceil(labels.length / room) - 1);
}

/** A bar chart keeps its first series as bars and the others as lines, matching saved charts. */
export function chartOption(chart: Pick<Chart, 'title' | 'kind' | 'x_label' | 'y_label' | 'labels' | 'series' | 'options'>, look: ChartLook, hidden?: ReadonlySet<string>): EChartsOption {
  if (chart.kind === 'echarts') return specOption(chart.options ?? {}, look);
  const values = chart.series.flatMap((s) => s.values);
  const lo = Math.min(0, ...values);
  const hi = Math.max(lo + 1, niceCeil(Math.max(0, ...values)));
  const muted = token('muted', '#686b77');
  const body = token('body', '#494b53');
  const line = token('hairline-strong', '#c9ccd5');
  return {
    animation: false,
    color: seriesColorIndexes(chart.series).map((i) => seriesColors()[i]),
    legend: { show: false, selected: Object.fromEntries(chart.series.map((s) => [s.name, !hidden?.has(s.name)])) },
    textStyle: { fontFamily: 'Figtree Variable, system-ui, sans-serif', fontSize: 12, color: body },
    grid: { left: look.spark ? 0 : 8, right: look.spark ? 0 : 16, top: look.spark ? 4 : look.compact ? 8 : 28, bottom: 4, outerBoundsMode: look.spark ? 'none' : 'same', outerBoundsContain: 'all' },
    tooltip: { show: !look.spark, trigger: 'axis', renderMode: 'richText', confine: true },
    dataZoom: look.spark ? [] : [
      { type: 'inside', xAxisIndex: 0, filterMode: 'none', zoomOnMouseWheel: true, moveOnMouseWheel: false, moveOnMouseMove: true },
    ],
    xAxis: {
      type: 'category',
      data: chart.labels,
      show: !look.spark,
      name: look.spark ? '' : chart.x_label,
      nameLocation: 'middle',
      nameGap: 28,
      boundaryGap: chart.kind === 'bar',
      axisLabel: { color: muted, interval: labelInterval(chart.labels, look.width) },
      axisLine: { lineStyle: { color: line } },
      axisTick: { alignWithLabel: true, lineStyle: { color: line } },
    },
    yAxis: {
      type: 'value',
      min: lo,
      max: hi,
      show: !look.spark,
      name: look.spark ? '' : chart.y_label,
      axisLabel: { color: muted },
      splitLine: { lineStyle: { color: token('hairline', '#e6e7ec') } },
    },
    series: chart.series.map((s, i) => chart.kind === 'bar' && i === 0
      ? { type: 'bar', name: s.name, data: s.values, barMaxWidth: 48, emphasis: { disabled: true } }
      : { type: 'line', name: s.name, data: s.values, showSymbol: s.values.length === 1, symbol: 'circle', symbolSize: 5, lineStyle: { width: 2 }, emphasis: { disabled: true } }),
  };
}

/** Saved specifications are validated JSON; text tooltips and static rendering apply at every scope. */
function specOption(options: EChartsOption, look: ChartLook): EChartsOption {
  const zoomOptions = (value: unknown) => {
    const zooms = (Array.isArray(value) ? value : [value]).filter((z): z is Record<string, unknown> => !!z && typeof z === 'object' && !Array.isArray(z));
    const hasInside = zooms.some((z) => z.type === 'inside');
    // The card owns controls. Keep linked sliders hidden; a slider-only spec
    // becomes an inside zoom so the header buttons and guarded wheel still work.
    return zooms.map((z) => z.type === 'inside' || !hasInside
      ? { ...z, type: 'inside', zoomOnMouseWheel: true, moveOnMouseWheel: false }
      : { ...z, show: false });
  };
  const safe = (value: unknown, dataset = false): unknown => {
    if (Array.isArray(value)) return value.map((item) => safe(item, dataset));
    if (!value || typeof value !== 'object') return value;
    const object = Object.fromEntries(Object.entries(value).map(([key, v]) => [key,
      // Dataset columns may share option names; their values are data, not rendering controls.
      dataset && key === 'source' ? structuredClone(v) : safe(v, key === 'dataset'),
    ]));
    if ('animation' in object) object.animation = false;
    if ('dataZoom' in object) object.dataZoom = zoomOptions(object.dataZoom);
    // Some saved model-authored templates escaped the JSON newline twice.
    // Decode label templates only; names, values and dataset source stay literal.
    const label = object.label as { formatter?: unknown } | undefined;
    if (label && typeof label.formatter === 'string') label.formatter = label.formatter.replaceAll('\\n', '\n');
    if (object.tooltip && typeof object.tooltip === 'object' && !Array.isArray(object.tooltip)) {
      object.tooltip = { ...object.tooltip, renderMode: 'richText', confine: true };
    }
    if (object.rippleEffect && typeof object.rippleEffect === 'object') object.rippleEffect = { ...object.rippleEffect, number: 0 };
    if (object.type === 'effectScatter') object.rippleEffect = { ...(object.rippleEffect as object | undefined), number: 0 };
    return object;
  };
  const option = safe(options) as EChartsOption;
  const series = option.series ? Array.isArray(option.series) ? option.series : [option.series] : [];
  const legends = option.legend ? Array.isArray(option.legend) ? option.legend : [option.legend] : [];
  // Reuse the measured chart width and ECharts' label bounds. Preserve authored
  // responsive layouts and multi-pie/coordinate layouts instead of rearranging them.
  const smallPie = !look.spark && look.width < 480 && !option.media && legends.length <= 1 && series.length === 1
    && series[0].type === 'pie' && (!series[0].coordinateSystem || series[0].coordinateSystem === 'none');
  const xAxes = option.xAxis ? Array.isArray(option.xAxis) ? option.xAxis : [option.xAxis] : [];
  const yAxes = option.yAxis ? Array.isArray(option.yAxis) ? option.yAxis : [option.yAxis] : [];
  const grids = option.grid ? Array.isArray(option.grid) ? option.grid : [option.grid] : [];
  const valueAxis = xAxes[0]?.type === 'value' ? xAxes[0] : undefined;
  const categoryAxis = yAxes[0]?.type === 'category' ? yAxes[0] : undefined;
  const horizontalBar = !option.media && !option.baseOption && !option.title && legends.length === 0 && grids.length <= 1
    && xAxes.length === 1 && yAxes.length === 1 && !!valueAxis && !!categoryAxis
    && series.length === 1 && series[0].type === 'bar' && (!series[0].coordinateSystem || series[0].coordinateSystem === 'cartesian2d');
  const fitBar = !look.spark && horizontalBar;
  const zoom = option.dataZoom ? Array.isArray(option.dataZoom) ? option.dataZoom : [option.dataZoom]
    : option.xAxis ? [{ type: 'inside' as const, ...(horizontalBar ? { yAxisIndex: 0 } : { xAxisIndex: 0 }), filterMode: 'none' as const, moveOnMouseMove: true, zoomOnMouseWheel: true, moveOnMouseWheel: false }] : [];
  return {
    ...option,
    // Authored percentage margins plus containLabel can leave only a few pixels
    // for bars. Bound the whole drawing and give labels a limited share.
    grid: fitBar ? { ...grids[0], left: 8, right: 8, top: 8, bottom: 8, width: 'auto', height: 'auto', containLabel: false, outerBoundsMode: 'same', outerBoundsContain: 'all' } : option.grid,
    xAxis: fitBar && valueAxis ? { ...valueAxis, nameLocation: 'middle', nameGap: 28, splitNumber: look.width < 480 ? 2 : valueAxis.splitNumber ?? 5, axisLabel: { ...valueAxis.axisLabel, hideOverlap: true } } : option.xAxis,
    yAxis: fitBar && categoryAxis ? { ...categoryAxis, axisLabel: { ...categoryAxis.axisLabel, width: Math.min(look.width < 480 ? 140 : 200, Math.floor(look.width * 0.38)), overflow: 'truncate' } } : option.yAxis,
    legend: smallPie ? legends.map((legend) => ({ ...legend, type: 'scroll', orient: 'horizontal', left: 8, right: 8, top: 'auto', bottom: 0, width: 'auto', height: 'auto' })) : option.legend,
    animation: false,
    textStyle: { fontFamily: 'Figtree Variable, system-ui, sans-serif', color: token('body', '#494b53'), ...option.textStyle },
    tooltip: { ...(Array.isArray(option.tooltip) ? option.tooltip[0] : option.tooltip), renderMode: 'richText', confine: true },
    series: series.map((s) => s.type === 'graph' || s.type === 'tree' || s.type === 'treemap'
      ? { ...s, animation: false, roam: s.roam ?? true }
      : smallPie && s.type === 'pie'
        ? { ...s, animation: false, center: ['50%', '50%'], left: 0, right: 0, top: 8, bottom: legends.some((legend) => legend.show !== false) ? 36 : 8, width: 'auto', height: 'auto',
          label: { ...s.label, alignTo: 'edge', edgeDistance: 8, overflow: 'break' },
          labelLine: { ...s.labelLine, length: 8, length2: 8 } }
        : { ...s, animation: false }),
    dataZoom: zoom,
  };
}

/** The rows as CSV, x first: what Copy CSV puts on the clipboard. */
export function chartCsv(chart: Pick<Chart, 'x' | 'labels' | 'series'>): string {
  const cell = (v: string) => (/[",\n\r]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);
  const head = [chart.x, ...chart.series.map((s) => s.name)].map(cell).join(',');
  const rows = chart.labels.map((label, i) => [cell(label), ...chart.series.map((s) => String(s.values[i]))].join(','));
  return [head, ...rows].join('\n') + '\n';
}

const number = new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 });
export const formatNumber = (v: number) => number.format(v);

/**
 * The headline of a pinned chart: a line chart's latest value of its first series, a bar chart's
 * total of it. Undefined without rows.
 */
export function headline(chart: Pick<Chart, 'kind' | 'labels' | 'series'>): { value: string; note: string } | undefined {
  const first = chart.series[0];
  if (!first?.values.length) return undefined;
  if (chart.kind === 'bar') {
    return { value: formatNumber(first.values.reduce((a, b) => a + b, 0)), note: `total ${first.name}, ${chart.labels.length} ${chart.labels.length === 1 ? 'bar' : 'bars'}` };
  }
  return { value: formatNumber(first.values.at(-1)!), note: `${first.name} at ${chart.labels.at(-1)}` };
}
