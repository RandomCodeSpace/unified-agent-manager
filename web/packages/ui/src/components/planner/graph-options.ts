import type { EChartsOption, GraphicComponentOption, GraphSeriesOption } from 'echarts';
import type { Card } from '../../api';
import { GRAPH_NODE, KIND_LABEL, MAP_NODE_H, MAP_NODE_W, STATUS_LABEL, shownProgress, wrapText, type GraphLayout, type MapEdge, type MapNode } from '../../lib/board';
import { STATUS_WORD, type TaskPlan } from './TaskPlan';

export const PLAN_GRAPH_PAD = 8;
// Blocker curves reach up to 36 + 80px past the final card; include their stroke too.
export const MAP_GRAPH_GUTTER = 120;
type Drawing = GraphicComponentOption;
type MapLayout = { nodes: MapNode[]; edges: MapEdge[]; width: number; height: number };

/** SVG still uses the app's one theme; resolve tokens before ECharts processes colours. */
function theme() {
  const css = getComputedStyle(document.documentElement);
  return {
    color: (name: string) => css.getPropertyValue(`--color-${name}`).trim() || 'currentColor',
    font: css.getPropertyValue('--font-sans').trim() || 'Figtree Variable, sans-serif',
  };
}

/** Keep the existing pixel layouts, including nodes at zero, instead of stretching each graph to its data bounds. */
function graphOption(width: number, height: number, graph: GraphSeriesOption, drawing: Drawing[]): EChartsOption {
  return {
    animation: false,
    tooltip: { show: false },
    grid: { left: 0, top: 0, right: 0, bottom: 0, outerBoundsMode: 'none' },
    xAxis: { type: 'value', min: 0, max: width, show: false },
    yAxis: { type: 'value', min: 0, max: height, inverse: true, show: false },
    series: [{ type: 'graph', coordinateSystem: 'cartesian2d', layout: 'none', roam: false, silent: true, label: { show: false }, ...graph }],
    graphic: drawing,
  };
}

/** A graph symbol with the same corner radius as its accessible button. */
function roundedBox(w: number, h: number, r: number): string {
  return `path://M${r} 0H${w - r}Q${w} 0 ${w} ${r}V${h - r}Q${w} ${h} ${w - r} ${h}H${r}Q0 ${h} 0 ${h - r}V${r}Q0 0 ${r} 0Z`;
}

export function mapNodeMeta(card: Card): string {
  return `#${card.seq} · ${KIND_LABEL[card.kind]} · ${STATUS_LABEL[card.status]}${card.progress ? ` · ${shownProgress(card.progress).short}` : ''}`;
}

export function mapGraphOption(layout: MapLayout, selected: string | null): EChartsOption {
  const { color, font } = theme();
  const at = new Map(layout.nodes.map((node) => [node.card.id, node]));
  // GraphChart shortens arrowed links using circular radii. These rectangular cards need
  // their existing edge ports, drawn through ECharts graphics instead.
  const edges: Drawing[] = layout.edges.flatMap((edge) => {
    const from = at.get(edge.from)!, to = at.get(edge.to)!;
    const x1 = from.x + MAP_NODE_W, y1 = from.y + MAP_NODE_H / 2, y2 = to.y + MAP_NODE_H / 2;
    if (edge.kind === 'parent') {
      const x2 = to.x, bend = (x2 - x1) / 2;
      return [{ type: 'bezierCurve', z: 1, silent: true, shape: { x1, y1, x2, y2, cpx1: x1 + bend, cpy1: y1, cpx2: x2 - bend, cpy2: y2 }, style: { fill: 'none', stroke: color('hairline-strong'), lineWidth: 1.25 } }];
    }
    const x2 = to.x + MAP_NODE_W + 2;
    const out = Math.max(x1, x2 - 2) + 36 + Math.min(80, Math.abs(y2 - y1) / 6);
    const open = from.card.status !== 'done' && from.card.status !== 'cancelled';
    const stroke = color(open ? 'warning' : 'hairline-strong');
    const link: Drawing[] = [{ type: 'bezierCurve', z: 1, silent: true, shape: { x1, y1, x2, y2, cpx1: out, cpy1: y1, cpx2: out, cpy2: y2 }, style: { fill: 'none', stroke, lineWidth: 1.5, lineDash: [4, 4] } }];
    if (open) link.push({ type: 'polygon', z: 1, silent: true, shape: { points: [[x2, y2], [x2 + 7, y2 - 3.5], [x2 + 7, y2 + 3.5]] }, style: { fill: stroke } });
    return link;
  });
  const drawing: Drawing[] = layout.nodes.flatMap(({ card, x, y }) => {
    const opacity = card.status === 'cancelled' ? 0.5 : 1;
    const container = card.kind !== 'subtask';
    const left = x + (container ? 40 : 34);
    const width = MAP_NODE_W - (left - x) - 12;
    const marks: Drawing[] = [
      { type: 'text', x: left, y: y + 10, z: 3, silent: true, style: { text: mapNodeMeta(card), fontFamily: font, fontSize: 11, fill: color('muted'), width, overflow: 'truncate', opacity } },
      { type: 'text', x: left, y: y + 25, z: 3, silent: true, style: { text: card.title, fontFamily: font, fontSize: 13, fontWeight: container ? 600 : 400, fill: color('ink'), width, overflow: 'truncate', opacity } },
    ];
    if (container) {
      marks.push({ type: 'circle', z: 3, silent: true, shape: { cx: x + 21, cy: y + 24, r: 6 }, style: { fill: 'none', stroke: color('hairline-strong'), lineWidth: 2.5, opacity } });
      const fraction = card.progress ? shownProgress(card.progress).fraction : 0;
      if (fraction > 0) marks.push({ type: 'arc', z: 3, silent: true, shape: { cx: x + 21, cy: y + 24, r: 6, startAngle: -Math.PI / 2, endAngle: -Math.PI / 2 + 2 * Math.PI * fraction, clockwise: true }, style: { fill: 'none', stroke: color('success'), lineWidth: 2.5, lineCap: 'round', opacity } });
    } else {
      const tone = { planned: 'faint', todo: 'muted', doing: 'accent', done: 'success', cancelled: 'hairline-strong' }[card.status];
      marks.push({ type: 'circle', z: 3, silent: true, shape: { cx: x + 18, cy: y + 24, r: 4.25 }, style: { fill: color(card.status === 'planned' || card.status === 'todo' ? 'raised' : tone), stroke: color(tone), lineWidth: 1.5, opacity } });
    }
    if (card.held_by) marks.push({ type: 'circle', z: 3, silent: true, shape: { cx: x + MAP_NODE_W - (card.pending_requests > 0 ? 23 : 11), cy: y + 10, r: 3 }, style: { fill: color('accent'), opacity } });
    if (card.pending_requests > 0) marks.push({ type: 'circle', z: 3, silent: true, shape: { cx: x + MAP_NODE_W - 12, cy: y + 10, r: 4 }, style: { fill: color('attention'), opacity } });
    return marks;
  });
  return graphOption(layout.width + MAP_GRAPH_GUTTER, layout.height, {
    symbol: roundedBox(MAP_NODE_W, MAP_NODE_H, 10),
    symbolSize: [MAP_NODE_W, MAP_NODE_H],
    data: layout.nodes.map(({ card, x, y }) => ({
      id: card.id,
      value: [x + MAP_NODE_W / 2, y + MAP_NODE_H / 2],
      itemStyle: {
        color: color('raised'), borderColor: color(selected === card.id || card.status === 'doing' ? 'accent' : 'hairline-strong'),
        borderWidth: selected === card.id ? 2 : 1.5, borderType: card.confirmed ? 'solid' : 'dashed', opacity: card.status === 'cancelled' ? 0.5 : 1,
      },
    })),
  }, [...edges, ...drawing]);
}

export function planNodeLabel(card: Card, mine: boolean): string {
  const container = card.kind !== 'subtask';
  const progress = shownProgress(card.progress ?? { done: 0, total: 0, proposed: 0 });
  const tag = mine ? 'this task' : card.confirmed ? '' : 'proposed';
  return `#${card.seq} ${card.title}, ${container ? progress.text : STATUS_WORD[card.status]}${tag ? `, ${tag}` : ''}. ${container ? `Show its ${card.kind === 'epic' ? 'stories' : 'subtasks'}` : 'Show its details'}`;
}

export function planGraphOption(layout: GraphLayout, plan: TaskPlan, selected: string | null): EChartsOption {
  const { color, font } = theme();
  const { w, h } = GRAPH_NODE;
  const mineIds = new Set([plan.mine?.id, ...plan.path.map((card) => card.id)]);
  const edges: Drawing[] = layout.edges.flatMap(({ from, to }) => {
    const stroke = color(mineIds.has(from.card.id) || mineIds.has(to.card.id) ? 'accent' : from.card.status === 'done' ? 'hairline-strong' : 'muted');
    const horizontal = layout.dir === 'lr';
    const x1 = from.x + PLAN_GRAPH_PAD + (horizontal ? w : w / 2), y1 = from.y + PLAN_GRAPH_PAD + (horizontal ? h / 2 : h);
    const x2 = to.x + PLAN_GRAPH_PAD + (horizontal ? -2 : w / 2), y2 = to.y + PLAN_GRAPH_PAD + (horizontal ? h / 2 : -2);
    const bend = (horizontal ? x2 - x1 : y2 - y1) / 2;
    return [
      { type: 'bezierCurve', z: 1, silent: true, shape: { x1, y1, x2, y2, cpx1: horizontal ? x1 + bend : x1, cpy1: horizontal ? y1 : y1 + bend, cpx2: horizontal ? x2 - bend : x2, cpy2: horizontal ? y2 : y2 - bend }, style: { fill: 'none', stroke, lineWidth: 1.5, lineDash: from.card.confirmed && to.card.confirmed ? undefined : [4, 3] } },
      { type: 'polygon', z: 1, silent: true, shape: { points: horizontal ? [[x2, y2], [x2 - 7, y2 - 3.5], [x2 - 7, y2 + 3.5]] : [[x2, y2], [x2 - 3.5, y2 - 7], [x2 + 3.5, y2 - 7]] }, style: { fill: stroke } },
    ];
  });
  const drawing: Drawing[] = layout.nodes.flatMap(({ card, x, y }) => {
    const state = card.kind !== 'subtask' ? shownProgress(card.progress ?? { done: 0, total: 0, proposed: 0 }).short : STATUS_WORD[card.status];
    const mine = card.id === plan.mine?.id;
    const tag = mine ? 'This task' : card.confirmed ? '' : 'Proposed';
    const marks: Drawing[] = wrapText(`#${card.seq} ${card.title}`, 24, 2).map((text, i) => ({
      type: 'text', x: x + PLAN_GRAPH_PAD + 8, y: y + PLAN_GRAPH_PAD + 7 + i * 15, z: 3, silent: true,
      style: { text, fontFamily: font, fontSize: 12, fill: color('ink') },
    }));
    marks.push({ type: 'text', x: x + PLAN_GRAPH_PAD + 8, y: y + PLAN_GRAPH_PAD + h - 18, z: 3, silent: true, style: { text: state, fontFamily: font, fontSize: 11, fill: color(card.status === 'done' ? 'success' : card.status === 'doing' ? 'accent' : 'muted') } });
    if (tag) marks.push({ type: 'text', x: x + PLAN_GRAPH_PAD + w - 8, y: y + PLAN_GRAPH_PAD + h - 18, z: 3, silent: true, style: { text: tag, align: 'right', fontFamily: font, fontSize: 11, fontWeight: mine ? 500 : 400, fill: color(mine ? 'accent' : 'muted') } });
    return marks;
  });
  return graphOption(layout.width + PLAN_GRAPH_PAD * 2, layout.height + PLAN_GRAPH_PAD * 2, {
    symbol: roundedBox(w, h, 6), symbolSize: [w, h],
    data: layout.nodes.map(({ card, x, y }) => ({
      id: card.id, value: [x + PLAN_GRAPH_PAD + w / 2, y + PLAN_GRAPH_PAD + h / 2],
      itemStyle: {
        color: color(mineIds.has(card.id) ? 'tint-selected' : card.status === 'done' ? 'surface' : card.confirmed ? 'raised' : 'canvas'),
        borderColor: color(card.id === plan.mine?.id ? 'accent' : card.id === selected ? 'ink' : 'hairline-strong'),
        borderWidth: card.id === plan.mine?.id ? 2 : 1, borderType: card.confirmed ? 'solid' : 'dashed',
      },
    })),
  }, [...edges, ...drawing]);
}
