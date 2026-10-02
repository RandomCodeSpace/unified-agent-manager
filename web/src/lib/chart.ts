/**
 * Charts an agent draws with `uam_chart` (docs/web.md "Charts"). The rows come from the service;
 * this turns them into Mermaid `xychart-beta` source, which the diagram frame renders like any
 * diagram (lib/diagram.ts), and into CSV and a headline value for a pinned chart.
 */
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

/** Text inside a Mermaid string: no quotes or line breaks, which would end it. */
const quoted = (s: string) => `"${Array.from(s.replace(/["\\]/g, "'"), (c) => (c < ' ' || c === '\u007f' ? ' ' : c)).join('').trim()}"`;

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
}

/**
 * The x labels that fit the width, every `step`-th, as Mermaid shows every category's label.
 * The others become distinct invisible strings (a word joiner, then the index in zero-width
 * characters), since the categories must stay distinct; the table lists every label.
 */
export function thinLabels(labels: string[], width: number): string[] {
  const longest = Math.max(1, ...labels.map((l) => l.length));
  const room = Math.max(1, Math.floor((width - 80) / (longest * 7 + 12)));
  const step = Math.ceil(labels.length / room);
  if (step <= 1) return labels;
  return labels.map((label, i) => (i % step === 0 ? label : '\u2060' + Array.from(i.toString(2), (bit) => (bit === '1' ? '\u200c' : '\u200b')).join('')));
}

/**
 * Mermaid `xychart-beta` source for a chart. The y axis starts at zero (or the lowest value, when
 * negative) so bars are not cut; a bar chart draws its first series as bars and any others as
 * lines, since Mermaid stacks bars of several series on top of one another.
 */
export function chartSource(chart: Pick<Chart, 'title' | 'kind' | 'x_label' | 'y_label' | 'labels' | 'series'>, look: ChartLook): string {
  const values = chart.series.flatMap((s) => s.values);
  const lo = Math.min(0, ...values);
  let hi = niceCeil(Math.max(0, ...values));
  if (hi <= lo) hi = lo + 1;
  const axis = { showLabel: !look.spark, showTitle: !look.spark, showTick: !look.spark, showAxisLine: !look.spark };
  const config = {
    xyChart: {
      width: look.width,
      height: look.height,
      // The card or panel heads the chart with its title.
      showTitle: false,
      plotReservedSpacePercent: look.spark ? 100 : 50,
      xAxis: { ...axis, labelFontSize: 12, titleFontSize: 12 },
      yAxis: { ...axis, labelFontSize: 12, titleFontSize: 12 },
    },
    themeVariables: {
      xyChart: {
        backgroundColor: token('raised', '#ffffff'),
        plotColorPalette: seriesColorIndexes(chart.series).map((i) => seriesColors()[i]).join(', '),
        ...Object.fromEntries(['x', 'y'].flatMap((a) => [
          [`${a}AxisLabelColor`, token('muted', '#686b77')],
          [`${a}AxisTitleColor`, token('body', '#494b53')],
          [`${a}AxisTickColor`, token('hairline-strong', '#c9ccd5')],
          [`${a}AxisLineColor`, token('hairline-strong', '#c9ccd5')],
        ])),
      },
    },
  };
  const lines = ['---', `config: ${JSON.stringify(config)}`, '---', 'xychart-beta'];
  const xTitle = !look.spark && chart.x_label ? `${quoted(chart.x_label)} ` : '';
  lines.push(`  x-axis ${xTitle}[${(look.spark ? chart.labels : thinLabels(chart.labels, look.width)).map(quoted).join(', ')}]`);
  const yTitle = !look.spark && chart.y_label ? `${quoted(chart.y_label)} ` : '';
  lines.push(`  y-axis ${yTitle}${lo} --> ${hi}`);
  chart.series.forEach((s, i) => {
    lines.push(`  ${chart.kind === 'bar' && i === 0 ? 'bar' : 'line'} [${s.values.join(', ')}]`);
  });
  return lines.join('\n');
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
