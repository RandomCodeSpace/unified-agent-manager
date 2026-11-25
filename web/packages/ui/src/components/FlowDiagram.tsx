// The React Flow view of a mermaid diagram (Diagram.tsx loads it on demand): Pele reads a
// flowchart's source (lib/flowGraph.ts), or the Utility model converts a diagram it cannot read;
// ELK lays it out (lib/flowLayout.ts), and the nodes are the page's own components in its tokens.
// Selection lights a node's edges; nodes drag with a mouse; nothing is saved.

import '@xyflow/react/dist/style.css';
import './FlowDiagram.css';
import { Background, BackgroundVariant, ControlButton, Controls, EdgeLabelRenderer, getSmoothStepPath, Handle, Position, ReactFlow, useNodesState, type Edge, type EdgeProps, type Node, type NodeProps } from '@xyflow/react';
import { memo, useEffect, useMemo, useRef, useState } from 'react';
import { flowGraph, type FlowGraph } from '../lib/flowGraph';
import { Spinner } from './common';
import type { FlowMiss } from './Diagram';
import { layoutFlow, type FlowEdgeData, type FlowLayout, type FlowNodeData, type Pt } from '../lib/flowLayout';

/** The card's height bounds; the diagram is fitted to the card's width inside them. */
const MIN_HEIGHT = 120;
const MAX_HEIGHT = 560;
const PAD = 24;

// Edges are drawn from ELK's points; the handles exist only because React Flow anchors every edge end.
function Anchors({ dir }: Readonly<{ dir: 'LR' | 'TB' }>) {
  return (
    <>
      <Handle type="target" position={dir === 'LR' ? Position.Left : Position.Top} isConnectable={false} className="flow-anchor" />
      <Handle type="source" position={dir === 'LR' ? Position.Right : Position.Bottom} isConnectable={false} className="flow-anchor" />
    </>
  );
}

const Box = memo(function Box({ data, selected }: NodeProps<Node<FlowNodeData>>) {
  return (
    <div className={`flow-node flow-${data.kind}${selected ? ' is-selected' : ''}`}>
      {data.kind === 'store' && (
        <svg viewBox="0 0 24 24" width="14" height="14" className="flow-icon" aria-hidden="true">
          <ellipse cx="12" cy="6" rx="7" ry="2.8" />
          <path d="M5 6v12c0 1.5 3.1 2.8 7 2.8s7-1.3 7-2.8V6M5 12c0 1.5 3.1 2.8 7 2.8s7-1.3 7-2.8" />
        </svg>
      )}
      <Label text={data.label} />
      <Anchors dir={data.dir} />
    </div>
  );
});

/** A label's first line is the node's name; in a converted graph the lines under it are its details (an entity's columns, a class's members). */
function Label({ text }: Readonly<{ text: string }>) {
  const [name, ...rest] = text.split('\n');
  return (
    <span className="flow-label">
      {name}
      {rest.length > 0 && <span className="flow-detail">{rest.join('\n')}</span>}
    </span>
  );
}

const Decision = memo(function Decision({ data, selected, width = 140, height = 72 }: NodeProps<Node<FlowNodeData>>) {
  return (
    <div className={`flow-decision${selected ? ' is-selected' : ''}`}>
      <svg width={width} height={height} aria-hidden="true">
        <path d={`M${width / 2} 1 L${width - 1} ${height / 2} L${width / 2} ${height - 1} L1 ${height / 2} Z`} />
      </svg>
      <span className="flow-label">{data.label}</span>
      <Anchors dir={data.dir} />
    </div>
  );
});

const Group = memo(function Group({ data }: NodeProps<Node<{ label: string; dir: 'LR' | 'TB' }>>) {
  return (
    <div className="flow-group">
      <span className="flow-group-title">{data.label}</span>
      <Anchors dir={data.dir} />
    </div>
  );
});

/** An orthogonal polyline with rounded bends. */
function roundedPath(p: Pt[], r = 8): string {
  let d = `M${p[0].x},${p[0].y}`;
  for (let i = 1; i < p.length - 1; i++) {
    const [a, b, c] = [p[i - 1], p[i], p[i + 1]];
    const toward = (q: Pt, t: number) => {
      const len = Math.hypot(q.x - b.x, q.y - b.y) || 1;
      return { x: b.x + ((q.x - b.x) / len) * t, y: b.y + ((q.y - b.y) / len) * t };
    };
    const bend = Math.min(r, Math.hypot(b.x - a.x, b.y - a.y) / 2, Math.hypot(c.x - b.x, c.y - b.y) / 2);
    const s = toward(a, bend);
    const e = toward(c, bend);
    d += ` L${s.x},${s.y} Q${b.x},${b.y} ${e.x},${e.y}`;
  }
  const z = p.at(-1)!;
  return `${d} L${z.x},${z.y}`;
}

function arrowHead(tip: Pt, from: Pt): string {
  const len = Math.hypot(tip.x - from.x, tip.y - from.y) || 1;
  const [ux, uy] = [(tip.x - from.x) / len, (tip.y - from.y) / len];
  const [bx, by] = [tip.x - ux * 7, tip.y - uy * 7];
  return `M${tip.x},${tip.y} L${bx - uy * 3.6},${by + ux * 3.6} L${bx + uy * 3.6},${by - ux * 3.6} Z`;
}

/** A point just outside a handle on `side`, so an arrow head at the handle points into the node. */
function behind(at: Pt, side: Position): Pt {
  const [dx, dy] = { [Position.Top]: [0, -10], [Position.Bottom]: [0, 10], [Position.Left]: [-10, 0], [Position.Right]: [10, 0] }[side];
  return { x: at.x + dx, y: at.y + dy };
}

const ElkEdge = memo(function ElkEdge({ id, data, source, target, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition }: EdgeProps<Edge<FlowEdgeData>>) {
  const { points, label, labelPos, dashed, thick, head, hot, free } = data!;
  if (!free && points.length < 2) return null;
  const state = !hot ? '' : hot === source || hot === target ? ' is-hot' : ' is-dim';
  // ELK's route holds until an end is dragged; then the edge steps between the handles.
  const [d, labelX, labelY] = free
    ? getSmoothStepPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition, borderRadius: 8 })
    : [roundedPath(points), labelPos?.x, labelPos?.y];
  const [start, end] = free ? [{ x: sourceX, y: sourceY }, { x: targetX, y: targetY }] : [points[0], points.at(-1)!];
  const [afterStart, beforeEnd] = free ? [behind(start, sourcePosition), behind(end, targetPosition)] : [points[1], points.at(-2)!];
  return (
    <>
      <g className={`flow-edge${dashed ? ' is-dashed' : ''}${thick ? ' is-thick' : ''}${state}`}>
        <path id={id} d={d} className="flow-edge-line" />
        {head !== 'none' && <path d={arrowHead(end, beforeEnd)} className="flow-edge-head" />}
        {head === 'both' && <path d={arrowHead(start, afterStart)} className="flow-edge-head" />}
      </g>
      {label && labelX !== undefined && labelY !== undefined && (
        <EdgeLabelRenderer>
          <div className={`flow-edge-label nodrag nopan${state}`} style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}>{label}</div>
        </EdgeLabelRenderer>
      )}
    </>
  );
});

const nodeTypes = { box: Box, decision: Decision, group: Group };
const edgeTypes = { elk: ElkEdge };

/** `id` and every node inside it, when it is a group. */
function withInside(nodes: Node[], id: string): string[] {
  const out = [id];
  for (let i = 0; i < out.length; i++) for (const n of nodes) if (n.parentId === out[i]) out.push(n.id);
  return out;
}

function Flow({ layout }: Readonly<{ layout: FlowLayout }>) {
  // Selection and drags change the nodes (no connect); React Flow applies them through this state.
  const [nodes, setNodes, onNodesChange] = useNodesState(layout.nodes);
  const [hot, setHot] = useState<string | null>(null);
  const [moved, setMoved] = useState<ReadonlySet<string>>(new Set());
  const edges = useMemo(
    () => layout.edges.map((e) => ({ ...e, data: { ...e.data!, hot, free: moved.has(e.source) || moved.has(e.target) } })),
    [layout.edges, hot, moved],
  );
  // A drag moves a node or pans with a mouse; on touch it scrolls the conversation, as over any other block.
  const fine = useMemo(() => globalThis.matchMedia?.('(pointer: fine)').matches ?? true, []);
  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onSelectionChange={({ nodes: selected }) => setHot(selected[0]?.id ?? null)}
      onNodeDragStart={(_, node) => setMoved((m) => (m.has(node.id) ? m : new Set([...m, ...withInside(layout.nodes, node.id)])))}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      forceColorMode="light"
      nodesDraggable={fine}
      nodesConnectable={false}
      edgesFocusable={false}
      panOnDrag={fine}
      zoomOnPinch={fine}
      zoomOnScroll={false}
      zoomOnDoubleClick={false}
      preventScrolling={false}
      fitView
      // A small chart keeps its own size; only a large one shrinks to the card.
      fitViewOptions={{ padding: `${PAD / 2}px`, maxZoom: 1 }}
      minZoom={0.2}
      maxZoom={2}
      proOptions={{ hideAttribution: true }}
    >
      <Background variant={BackgroundVariant.Dots} gap={18} size={1} className="flow-dots" />
      <Controls showInteractive={false} position="top-right" orientation="horizontal" className="flow-controls">
        {moved.size > 0 && (
          <ControlButton
            title="Reset layout"
            aria-label="Reset layout"
            onClick={() => {
              setNodes(layout.nodes);
              setMoved(new Set());
            }}
          >
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 12a8 8 0 1 0 2.4-5.7M4 4v4.5h4.5" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" /></svg>
          </ControlButton>
        )}
      </Controls>
    </ReactFlow>
  );
}

/**
 * The diagram in `source`, laid out for the card's width: a left-to-right chart far wider than
 * the card is laid out top to bottom instead. A source Pele cannot read is converted by
 * `convert` when given (in a Thread), else reported. `onUnavailable` says why the
 * view cannot draw it, so the card shows the Mermaid view.
 */
export default function FlowDiagram({ source, onUnavailable, convert }: Readonly<{ source: string; onUnavailable: (why: FlowMiss) => void; convert?: () => Promise<FlowGraph> }>) {
  const read = useMemo(() => flowGraph(source), [source]);
  const box = useRef<HTMLDivElement>(null);
  const [laid, setLaid] = useState<{ layout: FlowLayout; height: number } | null>(null);
  // A source Pele reads never waits on, or reruns for, the conversion.
  const fallback = read === null ? convert : undefined;
  const converting = !!fallback && !laid;

  useEffect(() => {
    if (read === 'too-big' || (read === null && !fallback)) {
      onUnavailable(read ?? 'unreadable');
      return;
    }
    const width = box.current?.clientWidth || 640;
    let on = true;
    (async () => {
      const graph = read ?? (await fallback!());
      let layout = await layoutFlow(graph, graph.direction);
      if (graph.direction === 'LR' && layout.width * 0.75 > width) {
        const tall = await layoutFlow(graph, 'TB');
        if (tall.width < layout.width) layout = tall;
      }
      const scale = Math.min(1, (width - PAD) / layout.width);
      if (on) setLaid({ layout, height: Math.round(Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, layout.height * scale + PAD))) });
    })().catch((err: unknown) => on && onUnavailable(read === null ? { error: err instanceof Error ? err.message : 'Conversion failed' } : 'unreadable'));
    return () => {
      on = false;
    };
  }, [read, fallback, onUnavailable]);

  return (
    // Pele's line breaks only wrap a label; the model's put details under a name.
    <div ref={box} role="figure" aria-label="Diagram" aria-busy={converting} className={read === null ? 'flow-diagram flow-details' : 'flow-diagram'} style={{ height: laid?.height ?? MIN_HEIGHT }}>
      {laid && <Flow layout={laid.layout} />}
      {converting && (
        <p className="flex h-full items-center justify-center gap-2 text-caption text-muted">
          <Spinner className="border-muted" />
          Converting with the Utility model…
        </p>
      )}
    </div>
  );
}
