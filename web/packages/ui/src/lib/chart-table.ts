import type { Chart } from '../api';

type Cell = string | number | null;
export interface ChartTable { columns: string[]; rows: Cell[][] }
const object = (value: unknown): Record<string, unknown> | null => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null;
const list = (value: unknown): unknown[] => Array.isArray(value) ? value : value == null ? [] : [value];
const cell = (value: unknown): value is Cell => value === null || typeof value === 'string' || typeof value === 'number' && Number.isFinite(value);
const name = (value: unknown): string => typeof value === 'string' ? value : '';
const dimensions = (value: unknown) => list(value).map(dimension => name(dimension) || name(object(dimension)?.name));

/** Tabular source formats only. Do not reverse engineer arbitrary chart geometry. */
function datasetTable(dataset: Record<string, unknown>): ChartTable | null {
  const source = dataset.source;
  if (!Array.isArray(source) || !source.length || dataset.transform) return null;
  let columns = dimensions(dataset.dimensions);
  if (object(source[0])) {
    if (!columns.length) columns = Object.keys(source[0]);
    const rows: Cell[][] = [];
    for (const entry of source) {
      const row = object(entry);
      if (!row || Object.keys(row).some(key => !columns.includes(key))) return null;
      const values = columns.map(column => row[column] ?? null);
      if (!values.every(cell)) return null;
      rows.push(values);
    }
    return columns.every(Boolean) ? { columns, rows } : null;
  }
  if (!source.every(row => Array.isArray(row) && row.every(cell))) return null;
  const first = source[0] as Cell[];
  const header = dataset.sourceHeader === true || dataset.sourceHeader !== false && first.every(value => typeof value === 'string') && source.slice(1).some(row => row.some((value: Cell) => typeof value === 'number'));
  if (!columns.length && header) columns = first.map(String);
  if (!columns.length || !columns.every(Boolean)) return null;
  const rows = source.slice(header ? 1 : 0) as Cell[][];
  return rows.every(row => row.length === columns.length) ? { columns, rows } : null;
}

function category(axis: Record<string, unknown> | null): (value: Cell) => Cell | undefined {
  if (!axis || axis.type !== 'category' && !Array.isArray(axis.data)) return value => value;
  if (!Array.isArray(axis.data)) return value => typeof value === 'string' ? value : undefined;
  const labels = axis.data.map(label => object(label)?.value ?? label);
  const known = new Set(labels);
  return value => {
    if (typeof value === 'number') {
      const label = Number.isInteger(value) ? labels[value] : undefined;
      return cell(label) ? label : undefined;
    }
    return value !== null && known.has(value) ? value : undefined;
  };
}

/** Reuses the saved source. Unknown, mixed or non-tabular options retain the JSON view. */
export function chartTable(chart: Pick<Chart, 'kind' | 'x' | 'x_label' | 'labels' | 'series' | 'options'>): ChartTable | null {
  if (chart.kind !== 'echarts') return {
    columns: [chart.x_label || chart.x, ...chart.series.map(series => series.name)],
    rows: chart.labels.map((label, index) => [label, ...chart.series.map(series => series.values[index] ?? null)]),
  };
  const options = object(chart.options);
  if (!options) return null;
  const series = list(options.series).map(object);
  if (!series.length || series.some(entry => !entry)) return null;
  const datasets = list(options.dataset);
  const dataset = datasets.length === 1 ? object(datasets[0]) : null;
  if (dataset && series.every(entry => entry && entry.data == null && entry.seriesLayoutBy !== 'row' && (entry.datasetIndex == null || entry.datasetIndex === 0) && (entry.datasetId == null || entry.datasetId === dataset.id))) return datasetTable(dataset);
  const spec = series[0];
  if (series.length !== 1 || !spec || spec.type !== 'heatmap' || !Array.isArray(spec.data)) return null;
  const calendar = spec.coordinateSystem === 'calendar';
  if (spec.coordinateSystem && spec.coordinateSystem !== 'cartesian2d' && !calendar) return null;
  const encode = object(spec.encode);
  // Non-default dimension mappings need an explicit dataset to expose their columns.
  if (encode && (encode.x != null && encode.x !== 0 || encode.y != null && encode.y !== 1 || encode.value != null && encode.value !== 2)) return null;
  const x = object(list(options.xAxis)[typeof spec.xAxisIndex === 'number' ? spec.xAxisIndex : 0]);
  const y = object(list(options.yAxis)[typeof spec.yAxisIndex === 'number' ? spec.yAxisIndex : 0]);
  const xValue = category(x), yValue = category(y);
  const names = dimensions(spec.dimensions);
  const columns = calendar ? ['Date', name(spec.name) || 'Value'] : [name(x?.name) || names[0] || 'X', name(y?.name) || names[1] || 'Y', names[2] || name(spec.name) || 'Value'];
  const rows: Cell[][] = [];
  for (const entry of spec.data) {
    const values = object(entry)?.value ?? entry;
    if (!Array.isArray(values) || values.length !== columns.length || !values.every(cell)) return null;
    if (calendar) rows.push(values);
    else {
      const xv = xValue(values[0]), yv = yValue(values[1]);
      if (xv === undefined || yv === undefined) return null;
      rows.push([xv, yv, values[2]]);
    }
  }
  return { columns, rows };
}
