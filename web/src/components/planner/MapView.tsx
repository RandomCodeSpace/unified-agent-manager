import { Maximize, Minus, Plus } from 'lucide-react';
import { memo, useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, type KeyboardEvent, type PointerEvent } from 'react';
import type { Card, CardStatus } from '../../api';
import { KIND_LABEL, MAP_NODE_H, MAP_NODE_W, STATUS_LABEL, layoutMap, type MapEdge, type MapNode } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Button } from '../ui/button';
import { useShownBoard } from './context';
import { ProgressRing } from './parts';

const MIN_K = 0.2;
const MAX_K = 2;
const PAD = 24;
/** The smallest scale the first fit uses, where node text still reads. */
const READABLE_K = 0.6;

// Only doing cards take a coloured outline; the dot carries every status.
const DOT: Record<CardStatus, string> = { planned: 'bg-raised border-faint', todo: 'bg-raised border-muted', doing: 'bg-accent border-accent', done: 'bg-success border-success', cancelled: 'bg-hairline-strong border-hairline-strong' };

/**
 * The Map (ADR 0005 §10): the plan as a tree, epic → story → subtask left to right, with
 * blocker links as dashed secondary edges. Nodes carry their status colour and word, and
 * containers a progress ring. Pan (drag, wheel, arrows) and zoom (pinch, Ctrl+wheel, + and −)
 * move one layer through a CSS transform written straight to the CSSOM, one frame at a time,
 * so a large plan never re-renders or lays out while it moves: nodes are HTML over an SVG of
 * the edges, because SVG text lays out again at every scale. A click opens the card.
 */
export function MapView() {
  const { ui, cards, openCard, board } = useShownBoard();
  const layout = useMemo(() => layoutMap(cards, { epic: ui.epic, showCancelled: ui.showCancelled }), [cards, ui.epic, ui.showCancelled]);
  const viewport = useRef<HTMLDivElement>(null);
  const layer = useRef<HTMLDivElement>(null);
  const view = useRef({ x: PAD, y: PAD, k: 1 });
  const frame = useRef(0);
  const idle = useRef(0);
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<{ x: number; y: number; vx: number; vy: number; moved: boolean; distance?: number; k?: number } | null>(null);
  const dragged = useRef(false);
  const fitted = useRef<string | null>(null);
  const marker = useId();

  const paint = useCallback(() => {
    frame.current = 0;
    const el = layer.current;
    if (el) el.style.transform = `translate3d(${view.current.x}px, ${view.current.y}px, 0) scale(${view.current.k})`;
  }, []);
  /** Schedules one write per frame; the layer is promoted only while it moves, then settles back to crisp text. */
  const schedule = useCallback(() => {
    layer.current?.classList.add('will-change-transform');
    window.clearTimeout(idle.current);
    idle.current = window.setTimeout(() => layer.current?.classList.remove('will-change-transform'), 200);
    frame.current ||= requestAnimationFrame(paint);
  }, [paint]);
  const zoomAt = useCallback((factor: number, cx: number, cy: number) => {
    const v = view.current;
    const k = Math.min(MAX_K, Math.max(MIN_K, v.k * factor));
    v.x = cx - ((cx - v.x) * k) / v.k;
    v.y = cy - ((cy - v.y) * k) / v.k;
    v.k = k;
    schedule();
  }, [schedule]);
  /** Fits the plan in view, no smaller than `least`: the first fit keeps a large plan readable, from its top. */
  const fit = useCallback((least = MIN_K) => {
    const el = viewport.current;
    if (!el || !layout.width) return;
    const { clientWidth: w, clientHeight: h } = el;
    const k = Math.min(1, Math.max(least, Math.min((w - PAD * 2) / layout.width, (h - PAD * 2) / Math.max(layout.height, 1))));
    view.current = { k, x: Math.max(PAD, (w - layout.width * k) / 2), y: PAD };
    // Written now, not on the next frame: a remount (StrictMode's included) must not lose the first fit.
    paint();
  }, [layout.width, layout.height, paint]);

  // Fit once per Board and filter; later updates keep the reader's view.
  const fitKey = `${ui.project}:${ui.epic}:${ui.showCancelled}:${!!board?.data}`;
  useLayoutEffect(() => {
    if (fitted.current === fitKey || !layout.width) return;
    fitted.current = fitKey;
    fit(READABLE_K);
  }, [fitKey, fit, layout.width]);
  useEffect(() => () => {
    cancelAnimationFrame(frame.current);
    frame.current = 0;
    window.clearTimeout(idle.current);
  }, []);

  // A card chosen elsewhere (the Tree, the Board, another window) is brought into view.
  useEffect(() => {
    const el = viewport.current;
    const n = layout.nodes.find((x) => x.card.id === ui.selected);
    if (!el || !n) return;
    const v = view.current;
    const left = v.x + n.x * v.k, top = v.y + n.y * v.k;
    if (left >= 0 && top >= 0 && left + MAP_NODE_W * v.k <= el.clientWidth && top + MAP_NODE_H * v.k <= el.clientHeight) return;
    v.x = el.clientWidth / 2 - (n.x + MAP_NODE_W / 2) * v.k;
    v.y = el.clientHeight / 2 - (n.y + MAP_NODE_H / 2) * v.k;
    schedule();
  }, [ui.selected, layout.nodes, schedule]);

  // Wheel pans; Ctrl or Cmd + wheel (and a trackpad pinch) zooms at the pointer. Not passive, so the page never scrolls.
  useEffect(() => {
    const el = viewport.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      if (e.ctrlKey || e.metaKey) {
        const r = el.getBoundingClientRect();
        zoomAt(Math.exp(-e.deltaY * 0.01), e.clientX - r.left, e.clientY - r.top);
        return;
      }
      view.current.x -= e.deltaX;
      view.current.y -= e.deltaY;
      schedule();
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, [zoomAt, schedule]);

  function onPointerDown(e: PointerEvent<HTMLDivElement>) {
    if (e.pointerType === 'mouse' && e.button !== 0) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    dragged.current = false;
    const pts = [...pointers.current.values()];
    const v = view.current;
    gesture.current = pts.length === 2
      ? { x: (pts[0].x + pts[1].x) / 2, y: (pts[0].y + pts[1].y) / 2, vx: v.x, vy: v.y, moved: true, distance: Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y), k: v.k }
      : { x: e.clientX, y: e.clientY, vx: v.x, vy: v.y, moved: false };
  }
  function onPointerMove(e: PointerEvent<HTMLDivElement>) {
    const g = gesture.current;
    if (!g || !pointers.current.has(e.pointerId)) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const pts = [...pointers.current.values()];
    if (pts.length === 2 && g.distance && g.k) {
      const r = viewport.current!.getBoundingClientRect();
      const mid = { x: (pts[0].x + pts[1].x) / 2 - r.left, y: (pts[0].y + pts[1].y) / 2 - r.top };
      const k = Math.min(MAX_K, Math.max(MIN_K, (g.k * Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y)) / g.distance));
      const v = view.current;
      v.x = mid.x - ((mid.x - v.x) * k) / v.k;
      v.y = mid.y - ((mid.y - v.y) * k) / v.k;
      v.k = k;
      dragged.current = true;
      schedule();
      return;
    }
    const dx = e.clientX - g.x, dy = e.clientY - g.y;
    if (!g.moved && Math.hypot(dx, dy) < 4) return;
    if (!g.moved) {
      g.moved = true;
      e.currentTarget.setPointerCapture(e.pointerId);
    }
    dragged.current = true;
    view.current.x = g.vx + dx;
    view.current.y = g.vy + dy;
    schedule();
  }
  function onPointerUp(e: PointerEvent<HTMLDivElement>) {
    pointers.current.delete(e.pointerId);
    const v = view.current;
    const rest = [...pointers.current.values()][0];
    gesture.current = rest ? { x: rest.x, y: rest.y, vx: v.x, vy: v.y, moved: true } : null;
  }
  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.target !== e.currentTarget) return;
    const el = e.currentTarget;
    const step = e.shiftKey ? 160 : 40;
    const v = view.current;
    switch (e.key) {
      case 'ArrowLeft': v.x += step; break;
      case 'ArrowRight': v.x -= step; break;
      case 'ArrowUp': v.y += step; break;
      case 'ArrowDown': v.y -= step; break;
      case '+':
      case '=':
        zoomAt(1.2, el.clientWidth / 2, el.clientHeight / 2);
        break;
      case '-':
        zoomAt(1 / 1.2, el.clientWidth / 2, el.clientHeight / 2);
        break;
      case '0':
        fit();
        break;
      default:
        return;
    }
    e.preventDefault();
    schedule();
  }

  const open = useCallback((id: string) => {
    if (!dragged.current) openCard(id);
  }, [openCard]);

  if (!layout.nodes.length) return <p className="px-4 py-6 text-ui text-muted">No cards match the filters.</p>;
  return (
    <div className="relative min-h-0 flex-1">
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-element-interactions -- A pan-and-zoom surface: pointer drags and arrow keys move it once focused. */}
      <div
        ref={viewport}
        role="group"
        aria-roledescription="map"
        aria-label="Plan map. Arrow keys pan, plus and minus zoom, 0 fits the plan."
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- focusable so the keyboard can pan and zoom it.
        tabIndex={0}
        className="absolute inset-0 cursor-grab touch-none overflow-hidden bg-canvas select-none focus-visible:-outline-offset-2 active:cursor-grabbing"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        onKeyDown={onKeyDown}
      >
        <div ref={layer} className="absolute top-0 left-0 origin-top-left">
          <svg width={layout.width} height={layout.height} viewBox={`0 0 ${layout.width} ${layout.height}`} className="absolute top-0 left-0 overflow-visible">
            <defs>
              <marker id={`${marker}-arrow`} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M0,0 L8,4 L0,8 z" className="fill-warning" />
              </marker>
            </defs>
            <Edges edges={layout.edges} nodes={layout.nodes} arrow={`url(#${marker}-arrow)`} />
          </svg>
          {layout.nodes.map((n) => (
            <Node key={n.card.id} node={n} selected={ui.selected === n.card.id} onOpen={open} />
          ))}
        </div>
      </div>
      <div className="absolute right-3 bottom-3 flex flex-col gap-1 rounded-md bg-raised p-1 shadow-float">
        <Button size="icon" aria-label="Zoom in" title="Zoom in (+)" onClick={() => viewport.current && zoomAt(1.25, viewport.current.clientWidth / 2, viewport.current.clientHeight / 2)}>
          <Plus />
        </Button>
        <Button size="icon" aria-label="Zoom out" title="Zoom out (−)" onClick={() => viewport.current && zoomAt(0.8, viewport.current.clientWidth / 2, viewport.current.clientHeight / 2)}>
          <Minus />
        </Button>
        <Button size="icon" aria-label="Fit the plan" title="Fit (0)" onClick={() => fit()}>
          <Maximize />
        </Button>
      </div>
    </div>
  );
}

const Edges = memo(function Edges({ edges, nodes, arrow }: Readonly<{ edges: MapEdge[]; nodes: MapNode[]; arrow: string }>) {
  const at = new Map(nodes.map((n) => [n.card.id, n]));
  const h = MAP_NODE_H / 2;
  return (
    <g fill="none">
      {edges.map((e) => {
        const a = at.get(e.from), b = at.get(e.to);
        if (!a || !b) return null;
        if (e.kind === 'parent') {
          const x1 = a.x + MAP_NODE_W, y1 = a.y + h, x2 = b.x, y2 = b.y + h;
          const bend = (x2 - x1) / 2;
          return <path key={`${e.from}>${e.to}`} d={`M${x1},${y1} C${x1 + bend},${y1} ${x2 - bend},${y2} ${x2},${y2}`} strokeWidth="1.25" className="stroke-hairline-strong" />;
        }
        // A blocker link loops out to the right of both cards, so it never crosses the tree's own edges.
        const x1 = a.x + MAP_NODE_W, y1 = a.y + h, x2 = b.x + MAP_NODE_W, y2 = b.y + h;
        const out = Math.max(x1, x2) + 36 + Math.min(80, Math.abs(y2 - y1) / 6);
        const open = a.card.status !== 'done' && a.card.status !== 'cancelled';
        return (
          <path
            key={`${e.from}~${e.to}`}
            d={`M${x1},${y1} C${out},${y1} ${out},${y2} ${x2 + 2},${y2}`}
            strokeWidth="1.5"
            strokeDasharray="4 4"
            markerEnd={open ? arrow : undefined}
            className={open ? 'stroke-warning' : 'stroke-hairline-strong'}
          >
            <title>{`#${a.card.seq} blocks #${b.card.seq}${open ? '' : ' (closed)'}`}</title>
          </path>
        );
      })}
    </g>
  );
});

const Node = memo(function Node({ node, selected, onOpen }: Readonly<{ node: MapNode; selected: boolean; onOpen: (id: string) => void }>) {
  const c: Card = node.card;
  const meta = `#${c.seq} · ${KIND_LABEL[c.kind]} · ${STATUS_LABEL[c.status]}${c.progress ? ` · ${c.progress.done}/${c.progress.total}` : ''}`;
  return (
    <button
      type="button"
      aria-pressed={selected}
      aria-label={`${meta}: ${c.title}`}
      title={`${meta}: ${c.title}`}
      // Placed through the CSSOM, like the layer's transform.
      style={{ left: node.x, top: node.y, width: MAP_NODE_W, height: MAP_NODE_H }}
      className={cn(
        'absolute flex cursor-pointer items-center gap-2.5 rounded-[10px] border-[1.5px] bg-raised pr-3 pl-3 text-left focus-visible:outline-offset-1',
        selected || c.status === 'doing' ? 'border-accent' : 'border-hairline-strong',
        selected && 'border-2',
        !c.confirmed && 'border-dashed',
        c.status === 'cancelled' && 'opacity-50',
      )}
      onClick={() => onOpen(c.id)}
    >
      {c.kind === 'subtask' ? <span aria-hidden="true" className={cn('size-2.5 shrink-0 rounded-full border-[1.5px]', DOT[c.status])} /> : <ProgressRing card={c} />}
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-meta tabular-nums text-muted">{meta}</span>
        <span className={cn('truncate text-caption text-ink', c.kind !== 'subtask' && 'font-semibold')}>{c.title}</span>
      </span>
      {(c.pending_requests > 0 || c.held_by) && (
        <span aria-hidden="true" className="absolute top-1.5 right-2 flex items-center gap-1">
          {c.held_by && <span className="size-1.5 rounded-full bg-accent" />}
          {c.pending_requests > 0 && <span className="size-2 rounded-full bg-attention" />}
        </span>
      )}
    </button>
  );
});
