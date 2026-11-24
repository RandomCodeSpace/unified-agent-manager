// Lays a flowchart (flowGraph.ts) out with ELK for the React Flow view: orthogonal edges, subgraphs
// as compound nodes, label boxes measured in the page's font. ELK runs in its own worker script,
// a hashed asset from this origin, so a layout never blocks the page.

import ELK, { type ElkExtendedEdge, type ElkNode } from 'elkjs/lib/elk-api.js';
import workerUrl from 'elkjs/lib/elk-worker.min.js?url';
import type { Edge, Node } from '@xyflow/react';
import type { FlowGraph, FlowHead, FlowKind } from './flowGraph';

let elk: InstanceType<typeof ELK> | undefined;

export interface Pt {
  x: number;
  y: number;
}
export interface FlowNodeData extends Record<string, unknown> {
  label: string;
  kind: FlowKind;
  dir: 'LR' | 'TB';
}
export interface FlowEdgeData extends Record<string, unknown> {
  points: Pt[];
  label?: string;
  labelPos?: Pt;
  dashed: boolean;
  thick: boolean;
  head: FlowHead;
  /** The selected node; its edges light up and the rest dim. */
  hot?: string | null;
  /** An end was dragged: the edge follows its handles instead of ELK's route. */
  free?: boolean;
}
export interface FlowLayout {
  nodes: Node[];
  edges: Edge<FlowEdgeData>[];
  width: number;
  height: number;
}

const FONT = '500 13px "Figtree Variable", system-ui, sans-serif';
const LABEL_FONT = '500 11px "Figtree Variable", system-ui, sans-serif';
const LINE = 17;
let measurer: CanvasRenderingContext2D | null | undefined;
function measure(text: string, font: string): number {
  measurer ??= document.createElement('canvas').getContext('2d');
  if (!measurer) return text.length * 7;
  measurer.font = font;
  return Math.ceil(Math.max(...text.split('\n').map((line) => measurer!.measureText(line).width)));
}

function nodeSize(n: FlowGraph['nodes'][number]) {
  const text = measure(n.label, FONT);
  const lines = n.label.split('\n').length;
  if (n.kind === 'decision') return { width: Math.max(140, Math.round(text * 1.6 + 48)), height: 72 + (lines - 1) * LINE };
  // padding 14 + (store icon 22 + gap 8) + text + padding 14
  return { width: Math.max(96, 28 + (n.kind === 'store' ? 30 : 0) + text), height: 26 + lines * LINE };
}

/** The flowchart laid out in `dir`, as React Flow nodes and edges in ELK's coordinates. */
export async function layoutFlow(graph: FlowGraph, dir: 'LR' | 'TB'): Promise<FlowLayout> {
  const root: ElkNode = {
    id: '__root',
    layoutOptions: {
      'elk.algorithm': 'layered',
      'elk.direction': dir === 'LR' ? 'RIGHT' : 'DOWN',
      'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
      'elk.edgeRouting': 'ORTHOGONAL',
      'elk.layered.spacing.nodeNodeBetweenLayers': '44',
      'elk.spacing.nodeNode': '28',
      'elk.spacing.edgeNode': '20',
      'elk.spacing.edgeEdge': '12',
      'elk.layered.spacing.edgeNodeBetweenLayers': '20',
      'elk.layered.spacing.edgeEdgeBetweenLayers': '12',
      'elk.spacing.edgeLabel': '6',
      'elk.edgeLabels.inline': 'true',
      // Network simplex places nodes best but costs about 10x on compound graphs.
      'elk.layered.nodePlacement.strategy': graph.nodes.length <= 30 ? 'NETWORK_SIMPLEX' : 'BRANDES_KOEPF',
      // The source's order decides which edge of a cycle points back, as Mermaid's does.
      'elk.layered.considerModelOrder.strategy': 'NODES_AND_EDGES',
      'elk.layered.cycleBreaking.strategy': 'GREEDY_MODEL_ORDER',
      'elk.padding': '[top=16,left=16,bottom=16,right=16]',
    },
    children: [],
    edges: [],
  };
  const elkNodes = new Map<string, ElkNode>();
  for (const g of graph.groups) elkNodes.set(g.id, { id: g.id, children: [], layoutOptions: { 'elk.padding': '[top=36,left=14,bottom=14,right=14]' } });
  const [inSide, outSide] = dir === 'LR' ? ['WEST', 'EAST'] : ['NORTH', 'SOUTH'];
  for (const n of graph.nodes) {
    const node: ElkNode = { id: n.id, ...nodeSize(n) };
    if (n.kind === 'decision') {
      // Edges meet a diamond at its tips, not anywhere along its box.
      node.layoutOptions = { 'elk.portConstraints': 'FIXED_SIDE', 'elk.portAlignment.default': 'CENTER' };
      node.ports = [
        { id: `${n.id}:in`, width: 1, height: 1, layoutOptions: { 'elk.port.side': inSide } },
        { id: `${n.id}:out`, width: 1, height: 1, layoutOptions: { 'elk.port.side': outSide } },
      ];
    }
    elkNodes.set(n.id, node);
  }
  for (const item of [...graph.groups, ...graph.nodes]) ((item.group && elkNodes.get(item.group)) || root).children!.push(elkNodes.get(item.id)!);
  const decisions = new Set(graph.nodes.filter((n) => n.kind === 'decision').map((n) => n.id));
  graph.edges.forEach((e, i) => {
    const edge: ElkExtendedEdge = { id: `e${i}`, sources: [decisions.has(e.from) ? `${e.from}:out` : e.from], targets: [decisions.has(e.to) ? `${e.to}:in` : e.to] };
    if (e.label) edge.labels = [{ text: e.label, width: measure(e.label, LABEL_FONT) + 12, height: 4 + e.label.split('\n').length * 14 }];
    root.edges!.push(edge);
  });

  elk ??= new ELK({ workerUrl });
  const out = await elk.layout(root);

  // ELK places children relative to their parent and edges relative to their container.
  const abs = new Map<string, Pt>([['__root', { x: 0, y: 0 }]]);
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const groupLabel = new Map(graph.groups.map((g) => [g.id, g.label]));
  const nodes: Node[] = [];
  const walk = (parent: ElkNode, origin: Pt) => {
    for (const c of parent.children ?? []) {
      const position = { x: c.x ?? 0, y: c.y ?? 0 };
      abs.set(c.id, { x: origin.x + position.x, y: origin.y + position.y });
      const n = byId.get(c.id);
      // A node dragged to its group's edge grows the group.
      const common = { id: c.id, position, width: c.width, height: c.height, ...(parent.id === '__root' ? {} : { parentId: parent.id, expandParent: true }) };
      nodes.push(n
        ? { ...common, type: n.kind === 'decision' ? 'decision' : 'box', data: { label: n.label, kind: n.kind, dir } satisfies FlowNodeData, ariaLabel: n.label.replaceAll('\n', ' ') }
        : { ...common, type: 'group', data: { label: groupLabel.get(c.id) ?? c.id, dir }, selectable: false, focusable: false });
      walk(c, abs.get(c.id)!);
    }
  };
  walk(out, { x: 0, y: 0 });

  const edges = (out.edges ?? []).map((e, i): Edge<FlowEdgeData> => {
    const source = graph.edges[i];
    const o = abs.get((e as { container?: string }).container ?? '__root') ?? { x: 0, y: 0 };
    const s = e.sections?.[0];
    const points = s ? [s.startPoint, ...(s.bendPoints ?? []), s.endPoint].map((p) => ({ x: p.x + o.x, y: p.y + o.y })) : [];
    const l = e.labels?.[0];
    return {
      id: e.id,
      source: source.from,
      target: source.to,
      type: 'elk',
      selectable: false,
      focusable: false,
      data: {
        points,
        label: source.label,
        labelPos: l ? { x: (l.x ?? 0) + o.x + (l.width ?? 0) / 2, y: (l.y ?? 0) + o.y + (l.height ?? 0) / 2 } : undefined,
        dashed: source.dashed,
        thick: source.thick,
        head: source.head,
      },
    };
  });
  return { nodes, edges, width: out.width ?? 0, height: out.height ?? 0 };
}
