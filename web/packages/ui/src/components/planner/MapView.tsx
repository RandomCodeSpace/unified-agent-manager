import { Maximize, Minus, Plus } from 'lucide-react';
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, type KeyboardEvent, type PointerEvent } from 'react';
import type { Card } from '../../api';
import { MAP_MAX_K, MAP_MIN_K, MAP_NODE_H, MAP_NODE_W, fitView, layoutMap, openingView, type MapNode } from '../../lib/board';
import { EChart } from '../EChart';
import { Button } from '../ui/button';
import { useShownBoard } from './context';
import { MAP_GRAPH_GUTTER, mapGraphOption, mapNodeMeta } from './graph-options';

const PAD = 24;

/**
 * The Map (ADR 0005 §10): the plan as a tree, epic → story → subtask left to right, with
 * blocker links as dashed secondary edges. Nodes carry their status colour and word, and
 * containers a progress ring. Pan (drag, wheel, arrows) and zoom (pinch, Ctrl+wheel, + and −)
 * move one layer through a CSS transform written straight to the CSSOM, one frame at a time,
 * so a large plan never re-renders or lays out while it moves. ECharts draws the graph, with
 * transparent native buttons above it for keyboard focus and opening cards.
 */
export function MapView() {
  const { ui, cards, openCard, board } = useShownBoard();
  const layout = useMemo(() => layoutMap(cards, { epic: ui.epic, showCancelled: ui.showCancelled }), [cards, ui.epic, ui.showCancelled]);
  const viewport = useRef<HTMLDivElement>(null);
  const layer = useRef<HTMLDivElement>(null);
  const view = useRef({ x: PAD, y: PAD, k: 1 });
  const frame = useRef(0);
  const idle = useRef(0);
  /** The window the pending frame and timer belong to: the pop-out's own when the Map is in one. */
  const clock = useRef<Window | null>(null);
  const revealed = useRef<string | null>(null);
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<{ x: number; y: number; vx: number; vy: number; moved: boolean; distance?: number; k?: number } | null>(null);
  const dragged = useRef(false);
  const opened = useRef<string | null>(null);
  const option = useMemo(() => mapGraphOption(layout, ui.selected), [layout, ui.selected]);

  const paint = useCallback(() => {
    frame.current = 0;
    const el = layer.current;
    // 2D on purpose: a 3D transform would keep the layer composited at rest (will-change promotes it only while it moves).
    if (el) el.style.transform = `translate(${view.current.x}px, ${view.current.y}px) scale(${view.current.k})`;
  }, []);
  /**
   * Schedules one write per frame; the layer is promoted only while it moves, then settles back
   * to crisp text. Frames and timers run on the Map's own window: in a Picture-in-Picture
   * pop-out, the main page's may be throttled while its tab is hidden.
   */
  const schedule = useCallback(() => {
    const el = layer.current;
    const win = el?.ownerDocument.defaultView ?? window;
    clock.current = win;
    el?.classList.add('will-change-transform');
    win.clearTimeout(idle.current);
    idle.current = win.setTimeout(() => layer.current?.classList.remove('will-change-transform'), 200);
    frame.current ||= win.requestAnimationFrame(paint);
  }, [paint]);
  const zoomAt = useCallback((factor: number, cx: number, cy: number) => {
    const v = view.current;
    const k = Math.min(MAP_MAX_K, Math.max(MAP_MIN_K, v.k * factor));
    v.x = cx - ((cx - v.x) * k) / v.k;
    v.y = cy - ((cy - v.y) * k) / v.k;
    v.k = k;
    schedule();
  }, [schedule]);
  /** Fit: the whole plan in view, within the zoom limits. */
  const fit = useCallback(() => {
    const el = viewport.current;
    if (!el || !layout.width) return;
    view.current = fitView(layout, el.clientWidth, el.clientHeight, PAD);
    paint();
  }, [layout, paint]);

  // Each Board and filter opens at scale 1 from the roots, so labels read; later updates keep the reader's view.
  const openKey = `${ui.project}:${ui.epic}:${ui.showCancelled}:${!!board?.data}`;
  useLayoutEffect(() => {
    if (opened.current === openKey || !layout.width) return;
    opened.current = openKey;
    view.current = openingView(layout, PAD);
    // Written now, not on the next frame: a remount (StrictMode's included) must not lose it.
    paint();
  }, [openKey, layout, paint]);
  useEffect(() => () => {
    const win = clock.current;
    if (!win) return;
    win.cancelAnimationFrame(frame.current);
    frame.current = 0;
    win.clearTimeout(idle.current);
  }, []);

  /** Pans a node into view when any of it is outside, centring it. */
  const reveal = useCallback((n: MapNode) => {
    const el = viewport.current;
    if (!el) return;
    const v = view.current;
    const left = v.x + n.x * v.k, top = v.y + n.y * v.k;
    if (left >= 0 && top >= 0 && left + MAP_NODE_W * v.k <= el.clientWidth && top + MAP_NODE_H * v.k <= el.clientHeight) return;
    v.x = el.clientWidth / 2 - (n.x + MAP_NODE_W / 2) * v.k;
    v.y = el.clientHeight / 2 - (n.y + MAP_NODE_H / 2) * v.k;
    schedule();
  }, [schedule]);

  // A card chosen elsewhere (the Tree, the Board, another window) is brought into view, once per choice.
  useEffect(() => {
    const n = layout.nodes.find((x) => x.card.id === ui.selected);
    if (!n || revealed.current === ui.selected) return;
    revealed.current = ui.selected;
    reveal(n);
  }, [ui.selected, layout.nodes, reveal]);

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
      const k = Math.min(MAP_MAX_K, Math.max(MAP_MIN_K, (g.k * Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y)) / g.distance));
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
        // Clipped, never a scroll container: focusing a node off screen pans the layer (Node's onFocus), it never scrolls the viewport.
        className="absolute inset-0 cursor-grab touch-none overflow-clip bg-canvas select-none focus-visible:-outline-offset-2 active:cursor-grabbing"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        onKeyDown={onKeyDown}
      >
        <div ref={layer} className="absolute top-0 left-0 origin-top-left" style={{ width: layout.width + MAP_GRAPH_GUTTER, height: layout.height }}>
          <EChart option={option} width={layout.width + MAP_GRAPH_GUTTER} height={layout.height} className="pointer-events-none [&_svg]:overflow-visible" />
          {layout.nodes.map((n) => (
            <Node key={n.card.id} node={n} selected={ui.selected === n.card.id} onOpen={open} onFocusNode={reveal} />
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

const Node = memo(function Node({ node, selected, onOpen, onFocusNode }: Readonly<{ node: MapNode; selected: boolean; onOpen: (id: string) => void; onFocusNode: (n: MapNode) => void }>) {
  const c: Card = node.card;
  const meta = mapNodeMeta(c);
  return (
    <button
      type="button"
      aria-pressed={selected}
      aria-label={`${meta}: ${c.title}`}
      title={`${meta}: ${c.title}`}
      // Placed through the CSSOM, like the layer's transform.
      style={{ left: node.x, top: node.y, width: MAP_NODE_W, height: MAP_NODE_H }}
      className="absolute cursor-pointer rounded-[10px] bg-transparent focus-visible:outline-offset-1"
      onClick={() => onOpen(c.id)}
      onFocus={() => onFocusNode(node)}
    />
  );
});
