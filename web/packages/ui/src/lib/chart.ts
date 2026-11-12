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

let tokens: { scheme: string | undefined; values: Record<string, string> } | null = null;
/** A colour token's value from the live page in its current scheme, or the fallback (node tests have no page). */
function token(name: string, fallback: string): string {
  const scheme = typeof document === 'undefined' ? undefined : document.documentElement.dataset.theme;
  if (!tokens || tokens.scheme !== scheme) tokens = { scheme, values: {} };
  const values = tokens.values;
  if (!(name in values)) values[name] = (typeof document === 'undefined' ? '' : getComputedStyle(document.documentElement).getPropertyValue(`--color-${name}`).trim()) || fallback;
  return values[name];
}
/** A colour token's value for drawings built outside the theme (the todo graph). */
export const chartToken = token;
const seriesColors = () => SERIES_TOKENS.map((name, i) => token(name, SERIES_FALLBACK[i]));

/** The side of a legend mark; ECharts' own is 25 by 14. */
const LEGEND_MARK = 10;

/**
 * The ECharts theme for the scheme on screen, from the same tokens: every chart, line/bar or
 * agent-authored, takes its palette, text, grid and tooltip colours from here. Colours a saved
 * specification sets still win (ECharts merges a theme under the option).
 */
export function chartTheme(): Record<string, unknown> {
  const ink = token('ink', '#25262b');
  const body = token('body', '#494b53');
  const muted = token('muted', '#686b77');
  const hairline = token('hairline', '#e6e7ec');
  const line = token('hairline-strong', '#c9ccd5');
  const raised = token('raised', '#ffffff');
  const axis = { axisLine: { lineStyle: { color: line } }, axisTick: { lineStyle: { color: line } }, axisLabel: { color: muted }, splitLine: { lineStyle: { color: hairline } }, nameTextStyle: { color: muted } };
  return {
    color: seriesColors(),
    backgroundColor: 'transparent',
    // A two-stop ramp for visual maps and heatmaps, low to high.
    gradientColor: [token('selection', '#cfdaf3'), token('badge-blue', SERIES_FALLBACK[0])],
    textStyle: { fontFamily: 'Figtree Variable, system-ui, sans-serif', color: body },
    title: { textStyle: { color: ink, fontSize: 14, fontWeight: 600 }, subtextStyle: { color: muted, fontSize: 12 } },
    // Every series' legend mark is the 10px swatch a row chart's bar toggle has.
    legend: { icon: 'roundRect', itemWidth: LEGEND_MARK, itemHeight: LEGEND_MARK, textStyle: { color: body }, inactiveColor: line, pageTextStyle: { color: muted }, pageIconColor: body, pageIconInactiveColor: line },
    // A category axis names its bands: its baseline is enough, without a tick between each pair.
    categoryAxis: { ...axis, axisTick: { show: false } }, valueAxis: axis, logAxis: axis, timeAxis: axis,
    tooltip: { backgroundColor: raised, borderColor: line, borderRadius: 6, padding: [6, 10], textStyle: { color: ink }, axisPointer: { lineStyle: { color: line }, crossStyle: { color: line } } },
    visualMap: { textStyle: { color: body } },
    // The marks: a 2px line with solid points, bars with eased corners, and slices and cells parted by the card's own colour.
    line: { symbol: 'circle', symbolSize: 5, lineStyle: { width: 2 } },
    bar: { itemStyle: { borderRadius: 2 } },
    pie: { label: { color: body }, itemStyle: { borderColor: raised, borderWidth: 2, borderRadius: 3 } },
    heatmap: { itemStyle: { borderColor: raised, borderWidth: 2, borderRadius: 2 } },
    // Treemap labels sit on the badge fills, which carry `raised` text at 4.5:1 or more.
    treemap: { label: { color: raised }, upperLabel: { color: raised }, itemStyle: { borderColor: raised }, breadcrumb: { itemStyle: { color: hairline, textStyle: { color: body } }, emphasis: { itemStyle: { color: line } } } },
    sankey: { label: { color: body } },
    // A radar's web is hairlines on the card, without ECharts' grey bands, and its names read as axis labels do.
    radar: { axisName: { color: muted }, axisLine: { lineStyle: { color: line } }, splitLine: { lineStyle: { color: hairline } }, splitArea: { show: false } },
  };
}

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
  return {
    animation: false,
    color: seriesColorIndexes(chart.series).map((i) => seriesColors()[i]),
    legend: { show: false, selected: Object.fromEntries(chart.series.map((s) => [s.name, !hidden?.has(s.name)])) },
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
      axisLabel: { interval: labelInterval(chart.labels, look.width) },
      axisTick: { alignWithLabel: true },
    },
    yAxis: {
      type: 'value',
      min: lo,
      max: hi,
      show: !look.spark,
      name: look.spark ? '' : chart.y_label,
    },
    series: chart.series.map((s, i) => chart.kind === 'bar' && i === 0
      ? { type: 'bar', name: s.name, data: s.values, barMaxWidth: 48, emphasis: { disabled: true } }
      // A lone line stands on a faint wash of its own colour; several would only muddy each other.
      : { type: 'line', name: s.name, data: s.values, showSymbol: s.values.length === 1, symbol: 'circle', symbolSize: 5, lineStyle: { width: 2 }, emphasis: { disabled: true }, ...(chart.series.length === 1 && { areaStyle: { opacity: 0.1 } }) }),
  };
}

/** Each category of a category y axis (horizontal bars) needs this many pixels for its label. */
const CATEGORY_ROW = 20;
const categoryHeight = (count: number) => count * CATEGORY_ROW + 80;
/**
 * A phone legend page: the pager and padding plus two entries' marks and gaps at itemGap 24
 * (measured with the theme's 10px marks). What remains is two names' text boxes, each as wide as
 * the other whatever its name (a text with a background keeps its set width), so a page's pair
 * fits whole and the third entry starts about 12px past the clip.
 */
const LEGEND_CHROME = 148;
const firstOf = <T,>(value: T | T[] | undefined) => (Array.isArray(value) ? value[0] : value);

/**
 * The height a chart card draws at: a chart with a category y axis grows until every
 * category has a labelled row, so ECharts need not skip every other label.
 */
export function chartHeight(chart: Pick<Chart, 'kind' | 'options'>, height: number): number {
  const axis = chart.kind === 'echarts' ? firstOf(chart.options?.yAxis) : undefined;
  return axis?.type === 'category' && Array.isArray(axis.data) ? Math.max(height, categoryHeight(axis.data.length)) : height;
}

/** A pixel length from a number or a percentage of `total`; undefined for anything else. */
function pixels(value: unknown, total: number): number | undefined {
  if (typeof value === 'number') return value;
  const match = typeof value === 'string' ? /^(\d+(?:\.\d+)?)(%|px)?$/.exec(value) : null;
  return match ? Number(match[1]) * (match[2] === '%' ? total / 100 : 1) : undefined;
}

/**
 * A filled line's area is a wash, so the lines and grid under it still read; ECharts' own is
 * nearly solid. A specification that sets the area's colour or opacity keeps its own.
 */
function washed<T extends { type?: unknown; areaStyle?: unknown }>(series: T): T {
  const area = series.areaStyle;
  if (series.type !== 'line' || !area || typeof area !== 'object' || 'opacity' in area || 'color' in area) return series;
  return { ...series, areaStyle: { ...area, opacity: 0.16 } };
}

/** Series drawn on plain x and y axes: a chart of only these is laid out by the card, not by its author. */
const AXIS_SERIES = new Set(['line', 'bar', 'scatter', 'effectScatter', 'candlestick', 'boxplot', 'pictorialBar']);
/** A one-row legend's height: the 10px mark in ECharts' 5px padding, and a gap from the plot. */
const LEGEND_ROW = 24;

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
  const objects = (value: unknown) => (Array.isArray(value) ? value : [value]).filter((v): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v));
  // The margin a grid kept for the slider it no longer shows: the edge the slider sat on.
  const sliderEdges = (value: unknown) => new Set(objects(value).filter((z) => z.type !== 'inside' && z.show !== false)
    .map((z) => z.orient === 'vertical' || (z.yAxisIndex !== undefined && z.xAxisIndex === undefined) ? 'right' : 'bottom'));
  // A legend or visual map sits at the bottom unless placed elsewhere.
  const placed = (component: unknown, edge: string) => objects(component).some((c) => c.show !== false
    && (c[edge] !== undefined || c.top === edge || c.left === edge || (edge === 'bottom' && c.top === undefined)));
  const safe = (value: unknown, dataset = false): unknown => {
    if (Array.isArray(value)) return value.map((item) => safe(item, dataset));
    if (!value || typeof value !== 'object') return value;
    const object = Object.fromEntries(Object.entries(value).map(([key, v]) => [key,
      // Dataset columns may share option names; their values are data, not rendering controls.
      dataset && key === 'source' ? structuredClone(v) : safe(v, key === 'dataset'),
    ]));
    if ('animation' in object) object.animation = false;
    if ('dataZoom' in object) {
      const edges = sliderEdges(object.dataZoom);
      object.dataZoom = zoomOptions(object.dataZoom);
      // Reclaim the hidden slider's band. ECharts keeps the axis labels and names inside the
      // drawing, so the grid can reach the edge; a legend or scale placed there keeps its room.
      for (const edge of edges) {
        if (!object.grid || placed(object.legend, edge) || placed(object.visualMap, edge)) continue;
        const reclaim = (grid: unknown) => grid && typeof grid === 'object' ? { ...grid, [edge]: 8, containLabel: false } : grid;
        object.grid = Array.isArray(object.grid) ? object.grid.map(reclaim) : reclaim(object.grid);
      }
    }
    // JSON cannot hold the function ECharts calls here: a "{value}" template becomes one,
    // anything else is dropped so the default formatting applies.
    if ('valueFormatter' in object) {
      const template = object.valueFormatter;
      if (typeof template === 'string' && template.includes('{value}')) {
        object.valueFormatter = (v: unknown) => template.replaceAll('{value}', typeof v === 'number' ? formatNumber(v) : String(v ?? '-'));
      } else delete object.valueFormatter;
    }
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
  // Label every category once the card has grown to fit them (chartHeight); long names truncate.
  const labelWidth = Math.min(look.width < 480 ? 140 : 200, Math.floor(look.width * 0.38));
  const categoryLabels = <T extends { type?: unknown; data?: unknown; axisLabel?: object }>(axis: T): T => axis.type !== 'category' ? axis : {
    ...axis, axisLabel: { ...(Array.isArray(axis.data) && look.height >= categoryHeight(axis.data.length) ? { interval: 0 } : {}), ...axis.axisLabel, width: labelWidth, overflow: 'truncate' },
  };
  // A legend placed along the top (ECharts puts it at the bottom by default) keeps to one row,
  // paged when it overflows, and the grid starts below it with room for a y axis name.
  const legendTop = legends.length === 1 && legends[0].show !== false && legends[0].orient !== 'vertical' && legends[0].bottom === undefined
    ? legends[0].top === 'top' ? 0 : pixels(legends[0].top, look.height) : undefined;
  const topLegend = !look.spark && !option.media && !option.baseOption && grids.length <= 1 && xAxes.length > 0 && legendTop !== undefined && legendTop < look.height / 4;
  const gridTop = topLegend ? Math.max(pixels(grids[0]?.top, look.height) ?? 65, legendTop + 24 + (yAxes.some((axis) => axis.name) ? 32 : 8)) : undefined;
  // On a phone each legend page holds two equal entries, so the pager never shows a cut-off one:
  // the third starts past the clip, with a wide gap to absorb the pager's text width.
  const legendWidth = look.width - 16;
  const legendText = look.width < 480 ? Math.max(24, Math.floor((legendWidth - LEGEND_CHROME) / 2)) : undefined;
  const phoneLegend = (legend: { textStyle?: unknown }) => legendText ? { left: 'center' as const, width: legendWidth, itemGap: 24, textStyle: { ...(legend.textStyle as object | undefined), width: legendText, overflow: 'truncate' as const, backgroundColor: 'transparent' } } : {};
  // A draggable scale on the right labels its handles on the plot's side: keep the grid clear of them.
  const scale = !look.spark && grids.length <= 1 && xAxes.length > 0 ? objects(option.visualMap).find((map) => map.calculable && map.show !== false
    && map.orient !== 'horizontal' && (map.right !== undefined || map.left === 'right')) : undefined;
  const scaleRoom = scale ? (pixels(scale.right, look.width) ?? 0) + (pixels(scale.itemWidth, look.width) ?? 20) + 26
    + 7 * Math.max(String(scale.max ?? 100).length, String(scale.min ?? 0).length) : 0;
  // A chart of plain axes and marks is laid out here: its author guessed margins without knowing the
  // card's width, and a guess too small puts the legend on the plot or the labels off the card.
  // The title takes a band at the top, the legend one paged row above or below, and the plot fills
  // the rest with its labels and axis names inside it (ECharts' outer bounds).
  const titles = objects(option.title);
  const plain = !look.spark && !option.media && !option.baseOption && grids.length <= 1 && xAxes.length > 0 && yAxes.length > 0
    && titles.length <= 1 && legends.length <= 1 && legends[0]?.orient !== 'vertical' && (!option.visualMap || (!!scale && objects(option.visualMap).length === 1)) && !option.graphic
    && series.length > 0 && series.every((s) => AXIS_SERIES.has(String(s.type)) && (!s.coordinateSystem || s.coordinateSystem === 'cartesian2d'));
  const title = titles.length === 1 && titles[0].show !== false && (titles[0].text || titles[0].subtext) ? titles[0] : undefined;
  const titleBand = title ? 8 + (title.text ? 18 : 0) + (title.text && title.subtext ? 4 : 0) + (title.subtext ? 16 : 0) : 0;
  const rowLegend = plain && legends.length === 1 && legends[0].show !== false ? legends[0] : undefined;
  const legendAbove = !!rowLegend && legendTop !== undefined && legendTop < look.height / 4;
  // An x axis name sits under the middle of its axis, where the end of the axis cannot clip it. A y axis
  // name starts at its axis and runs inward, so a long one takes no margin; two share the width.
  const nameWidth = Math.floor(look.width / Math.min(2, yAxes.length)) - 24;
  const inward = <T extends { name?: unknown; position?: unknown; nameLocation?: unknown; nameTextStyle?: object }>(axis: T, index: number): T => !axis.name || axis.nameLocation !== undefined ? axis
    : { ...axis, nameTruncate: { maxWidth: nameWidth }, nameTextStyle: { align: axis.position === 'right' || (index > 0 && axis.position !== 'left') ? 'right' : 'left', ...axis.nameTextStyle } };
  const yAxis = <T extends { type?: unknown; data?: unknown; axisLabel?: object; name?: unknown; position?: unknown; nameLocation?: unknown; nameTextStyle?: object }>(axis: T, index: number): T => categoryLabels(plain ? inward(axis, index) : axis);
  // Slanted labels are cut to a fifth of the height, or forty long names leave no room for the bars.
  const slantWidth = Math.max(48, Math.min(120, Math.round(look.height * 0.2)));
  const under = <T extends { name?: unknown; nameLocation?: unknown; nameGap?: unknown; axisLabel?: { rotate?: unknown } }>(axis: T): T => ({
    ...axis,
    ...(axis.name && axis.nameLocation === undefined ? { nameLocation: 'middle', nameGap: axis.nameGap ?? 28 } : {}),
    ...(axis.axisLabel?.rotate ? { axisLabel: { width: slantWidth, overflow: 'truncate', hideOverlap: true, ...axis.axisLabel } } : {}),
  });
  // A radar's axis names stand outside its web: on a phone a long one is cut to a quarter of the width,
  // which only each indicator's own nameTruncate does.
  const radars = objects(option.radar);
  const radar = !look.spark && look.width < 480 && radars.length === 1 && !Array.isArray(option.radar) && Array.isArray(radars[0].indicator)
    ? { ...radars[0], indicator: objects(radars[0].indicator).map((axis) => ({ nameTruncate: { maxWidth: Math.round(look.width / 4) }, ...axis })) } as EChartsOption['radar'] : undefined;
  return {
    ...option,
    // Authored percentage margins plus containLabel can leave only a few pixels
    // for bars. Bound the whole drawing and give labels a limited share.
    grid: fitBar || plain ? { ...grids[0], left: 8, right: Math.max(8, scaleRoom), top: 8 + titleBand + (legendAbove ? LEGEND_ROW : 0), bottom: 8 + (rowLegend && !legendAbove ? LEGEND_ROW : 0), width: 'auto', height: 'auto', containLabel: false, outerBoundsMode: 'same', outerBoundsContain: 'all' }
      : topLegend || scale ? { ...grids[0], ...(topLegend && { top: gridTop }), ...(scale && { right: Math.max(pixels(grids[0]?.right, look.width) ?? 0, scaleRoom) }) } : option.grid,
    xAxis: fitBar && valueAxis ? { ...valueAxis, nameLocation: 'middle', nameGap: 28, splitNumber: look.width < 480 ? 2 : valueAxis.splitNumber ?? 5, axisLabel: { ...valueAxis.axisLabel, hideOverlap: true } }
      : plain && option.xAxis ? Array.isArray(option.xAxis) ? option.xAxis.map(under) : under(option.xAxis) : option.xAxis,
    yAxis: look.spark || !option.yAxis ? option.yAxis : Array.isArray(option.yAxis) ? option.yAxis.map(yAxis) : yAxis(option.yAxis, 0),
    legend: smallPie ? legends.map((legend) => ({ ...legend, type: 'scroll', orient: 'horizontal', left: 8, right: 8, top: 'auto', bottom: 0, width: 'auto', height: 'auto', ...phoneLegend(legend) }))
      : rowLegend ? { ...rowLegend, type: 'scroll' as const, orient: 'horizontal' as const, top: legendAbove ? titleBand : 'auto', bottom: legendAbove ? 'auto' : 0, width: legendWidth, ...phoneLegend(rowLegend) }
        : topLegend ? { ...legends[0], type: 'scroll' as const, ...phoneLegend(legends[0]) } : option.legend,
    // The band above the plot: one line each, cut to the card's width.
    title: (plain || smallPie) && title ? { ...title, top: 0, padding: [8, 8, 0, 8], itemGap: 4, textStyle: { ...(title.textStyle as object | undefined), fontSize: 14, lineHeight: 18, width: look.width - 16, overflow: 'truncate' }, subtextStyle: { ...(title.subtextStyle as object | undefined), fontSize: 12, lineHeight: 16, width: look.width - 16, overflow: 'truncate' } } : option.title,
    animation: false,
    ...(radar && { radar }),
    tooltip: { ...(Array.isArray(option.tooltip) ? option.tooltip[0] : option.tooltip), renderMode: 'richText', confine: true },
    series: series.map((s) => s.type === 'graph' || s.type === 'tree' || s.type === 'treemap'
      ? { ...s, animation: false, roam: s.roam ?? true }
      // A flow's last column is labelled on its right: on a phone it keeps a third of the width for those names.
      : s.type === 'sankey' && !look.spark && look.width < 480 && s.right === undefined
        ? { ...s, animation: false, left: s.left ?? 8, right: Math.round(look.width * 0.3), label: { width: Math.round(look.width * 0.3) - 12, overflow: 'truncate' as const, ...(s.label as object | undefined) } }
      : smallPie && s.type === 'pie'
        ? { ...s, animation: false, center: ['50%', '50%'], left: 0, right: 0, top: 8 + titleBand, bottom: legends.some((legend) => legend.show !== false) ? 36 : 8, width: 'auto', height: 'auto',
          label: { ...s.label, alignTo: 'edge', edgeDistance: 8, overflow: 'break' },
          labelLine: { ...s.labelLine, length: 8, length2: 8 } }
        : { ...washed(s), animation: false }),
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
