// A Mermaid flowchart read as a graph for the React Flow view (FlowDiagram.tsx): Pele
// (https://pele.run) parses the source without a DOM; layout and drawing are the view's.
// Loaded only with that view, so the page's first load carries none of it.

import { parse, register } from 'pele/core';
import flowchart from 'pele/diagrams/flowchart';

register(flowchart);

/** How a node is drawn: Mermaid's many shapes fold into the few the view tells apart. */
export type FlowKind = 'step' | 'store' | 'decision' | 'terminal';
export type FlowHead = 'end' | 'both' | 'none';

export interface FlowGraph {
  direction: 'LR' | 'TB';
  /** Subgraphs; `group` is the subgraph a nested one sits in. */
  groups: { id: string; label: string; group?: string }[];
  nodes: { id: string; label: string; kind: FlowKind; group?: string }[];
  edges: { from: string; to: string; label?: string; dashed: boolean; thick: boolean; head: FlowHead }[];
}

/** Past this many nodes a fitted diagram is too small to read; the Mermaid view draws it instead. */
export const FLOW_MAX_NODES = 60;

const KINDS: Record<string, FlowKind> = {
  diamond: 'decision', rhombus: 'decision', diam: 'decision', decision: 'decision', question: 'decision',
  cylinder: 'store', cyl: 'store', 'lin-cyl': 'store', 'h-cyl': 'store', das: 'store', db: 'store', database: 'store', 'disk': 'store',
  stadium: 'terminal', pill: 'terminal', terminal: 'terminal', circle: 'terminal', circ: 'terminal', doublecircle: 'terminal', 'dbl-circ': 'terminal', 'sm-circ': 'terminal', 'fr-circ': 'terminal', start: 'terminal', stop: 'terminal',
};

/** A label as plain text: line breaks kept, tags and Markdown emphasis dropped. */
export function plainLabel(text: string): string {
  let out = text.replaceAll(/<br\s*\/?>/gi, '\n');
  // Repeated until nothing changes, so no removal can join the rest into a tag (labels render as text anyway).
  for (let prev = ''; prev !== out; ) {
    prev = out;
    out = out.replaceAll(/<[^>]*>/g, '');
  }
  return out
    .replaceAll(/(\*\*|__|\*|_|`)(\S(?:.*?\S)?)\1/g, '$2')
    .replaceAll('&nbsp;', ' ')
    .trim();
}

interface PeleNode { id: string; text?: string; type?: string }
interface PeleEdge { start: string; end: string; type?: string; stroke?: string; text?: string }
interface PeleFlowchart { type: string; direction?: string; nodes: Map<string, PeleNode>; edges: PeleEdge[]; subgraphs: { id: string; title?: string; nodes: string[] }[] }

/** The flowchart as a graph; 'too-big' past FLOW_MAX_NODES; null for any other diagram or a source Pele cannot read. */
export function flowGraph(source: string): FlowGraph | 'too-big' | null {
  let model: PeleFlowchart;
  try {
    model = parse(source) as unknown as PeleFlowchart;
  } catch {
    return null;
  }
  if (model.type !== 'flowchart') return null;
  const groupOf = new Map<string, string>();
  for (const g of model.subgraphs) for (const member of g.nodes) groupOf.set(member, g.id);
  const groupIds = new Set(model.subgraphs.map((g) => g.id));
  // An edge to a subgraph also lists it as a node; it is drawn as the group.
  const nodes = [...model.nodes.values()]
    .filter((n) => !groupIds.has(n.id))
    .map((n) => ({ id: n.id, label: plainLabel(n.text ?? n.id) || n.id, kind: KINDS[n.type ?? ''] ?? 'step', group: groupOf.get(n.id) }));
  if (nodes.length === 0) return null;
  if (nodes.length > FLOW_MAX_NODES) return 'too-big';
  const edges = model.edges
    // An invisible link only spaces Mermaid's layout.
    .filter((e) => e.stroke !== 'invisible')
    .map((e) => ({
      from: e.start,
      to: e.end,
      label: plainLabel(e.text ?? '') || undefined,
      dashed: e.stroke === 'dotted',
      thick: e.stroke === 'thick',
      head: (e.type?.startsWith('double_') ? 'both' : e.type === 'arrow_open' ? 'none' : 'end') as FlowHead,
    }));
  return {
    // RL and BT read as LR and TB; the view keeps the reading order, not the mirror.
    direction: model.direction === 'LR' || model.direction === 'RL' ? 'LR' : 'TB',
    groups: model.subgraphs.map((g) => ({ id: g.id, label: plainLabel(g.title ?? g.id), group: groupOf.get(g.id) })),
    nodes,
    edges,
  };
}
