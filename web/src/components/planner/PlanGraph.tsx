import { ChevronDown, ChevronRight, Lock, Minus, Plus, RotateCcw } from 'lucide-react';
import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react';
import type { Card } from '../../api';
import { GRAPH_NODE, MAP_MAX_K, MAP_MIN_K, cardPath, layoutLevel, shownProgress, waitsOf } from '../../lib/board';
import { EChart } from '../EChart';
import { Button } from '../ui/button';
import { WaitList, type TaskPlan } from './TaskPlan';
import { PLAN_GRAPH_PAD, planGraphOption, planNodeLabel } from './graph-options';

const KIND_WORDS = { epic: 'epics', story: 'stories', subtask: 'subtasks' } as const;

/**
 * The plan's dependencies one level at a time (ADR 0005 §3: links never mix levels): the
 * Project's epics, one epic's stories, or one story's subtasks. A breadcrumb moves between
 * levels and a container's node opens its level; a subtask's node opens its details under the
 * graph (`details`). Arrows run from what has to finish first. ECharts draws the existing
 * layout left to right when it fits the panel, else top to bottom. Its own viewport pans
 * and zooms without changing those positions.
 */
export function PlanGraph({ plan, level, selected, onLevel, onSelect, details }: Readonly<{
  plan: TaskPlan;
  /** The container whose children are drawn; '' for the Project's top level. */
  level: string;
  selected: string | null;
  onLevel: (id: string) => void;
  onSelect: (id: string) => void;
  details: (card: Card) => ReactNode;
}>) {
  const container = level ? plan.byId.get(level) : undefined;
  const cards = plan.index.get(container ? level : '') ?? EMPTY;
  const box = useRef<HTMLDivElement>(null);
  const help = useId();
  const [room, setRoom] = useState(0);
  const [headOpen, setHeadOpen] = useState(false);
  const [zoom, setZoom] = useState(1);
  const drag = useRef<{ x: number; y: number; left: number; top: number } | null>(null);
  const dragged = useRef(false);
  const zoomBy = (factor: number) => setZoom((current) => Math.max(MAP_MIN_K, Math.min(MAP_MAX_K, current * factor)));
  const reset = () => {
    setZoom(1);
    if (box.current) { box.current.scrollLeft = 0; box.current.scrollTop = 0; }
  };
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const wheel = (event: WheelEvent) => {
      if (!event.ctrlKey && !event.metaKey) return;
      event.preventDefault();
      setZoom((current) => Math.max(MAP_MIN_K, Math.min(MAP_MAX_K, current * Math.exp(-event.deltaY * 0.01))));
    };
    el.addEventListener('wheel', wheel, { passive: false });
    return () => el.removeEventListener('wheel', wheel);
  }, []);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    const measure = () => setRoom(el.clientWidth);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  const lr = useMemo(() => layoutLevel(cards, 'lr'), [cards]);
  const tb = useMemo(() => layoutLevel(cards, 'tb'), [cards]);
  const layout = lr.width + PLAN_GRAPH_PAD * 2 <= room ? lr : tb;
  const path = container ? cardPath(container, plan.byId) : [];
  const option = useMemo(() => planGraphOption(layout, plan, selected), [layout, plan, selected]);
  const shown = selected ? cards.find((c) => c.id === selected && c.kind === 'subtask') : undefined;
  const waits = container ? waitsOf(container, plan.byId) : [];
  const kinds = [...new Set(cards.map((c) => c.kind))].map((k) => KIND_WORDS[k]).join(' and ');
  const progress = container?.progress && shownProgress(container.progress);

  const open = (card: Card) => (card.kind === 'subtask' ? onSelect(card.id) : onLevel(card.id));

  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (!event.isPrimary || (event.pointerType === 'mouse' && event.button !== 0)) return;
    drag.current = { x: event.clientX, y: event.clientY, left: event.currentTarget.scrollLeft, top: event.currentTarget.scrollTop };
    dragged.current = false;
  };
  const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const start = drag.current;
    if (!start) return;
    const dx = event.clientX - start.x, dy = event.clientY - start.y;
    if (!dragged.current && Math.hypot(dx, dy) < 4) return;
    if (!dragged.current) event.currentTarget.setPointerCapture(event.pointerId);
    dragged.current = true;
    event.currentTarget.scrollLeft = start.left - dx;
    event.currentTarget.scrollTop = start.top - dy;
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget) return;
    const el = event.currentTarget;
    const step = event.shiftKey ? 160 : 40;
    switch (event.key) {
      case 'ArrowLeft': el.scrollLeft -= step; break;
      case 'ArrowRight': el.scrollLeft += step; break;
      case 'ArrowUp': el.scrollTop -= step; break;
      case 'ArrowDown': el.scrollTop += step; break;
      case '+': case '=': zoomBy(1.25); break;
      case '-': zoomBy(0.8); break;
      case '0': reset(); break;
      default: return;
    }
    event.preventDefault();
  };

  return (
    <div className="flex min-w-0 flex-col gap-2">
      <nav aria-label="Plan level" className="flex min-w-0 flex-wrap items-center gap-x-1 text-caption text-muted">
        {[{ id: '', title: plan.project.name }, ...path.map((c) => ({ id: c.id, title: `#${c.seq} ${c.title}` }))].map((crumb, i, all) => (
          <span key={crumb.id || 'project'} className="flex min-w-0 items-center gap-1">
            {i > 0 && <ChevronRight aria-hidden="true" className="size-3 shrink-0 text-faint" />}
            {i === all.length - 1 ? (
              <span aria-current="location" className="truncate font-medium text-ink">{crumb.title}</span>
            ) : (
              <button type="button" className="min-h-6 truncate underline decoration-hairline-strong underline-offset-2 hover:text-ink pointer-coarse:min-h-11" onClick={() => onLevel(crumb.id)}>
                {crumb.title}
              </button>
            )}
          </span>
        ))}
      </nav>
      {container && waits.length > 0 && (
        <p className="flex items-start gap-1.5 rounded-sm bg-warning-wash px-2 py-1.5 text-caption text-body">
          <Lock aria-hidden="true" className="mt-0.5 size-3 shrink-0 text-warning" />
          <span>
            This whole {container.kind} waits for <WaitList waits={waits} onOpen={(id) => onLevel(levelFor(plan, id))} />
          </span>
        </p>
      )}
      <p className="text-caption text-muted">
        {cards.length ? `${cards.length} ${kinds}` : 'Nothing at this level yet'}
        {progress && ` · ${progress.text}`}
        {layout.edges.length > 0 && ' · arrows point from what finishes first'}
      </p>
      {cards.length > 0 && (
        <div className="flex items-center gap-1">
          <Button size="icon" aria-label="Zoom in on dependencies" disabled={zoom >= MAP_MAX_K} onClick={() => zoomBy(1.25)}><Plus /></Button>
          <Button size="icon" aria-label="Zoom out of dependencies" disabled={zoom <= MAP_MIN_K} onClick={() => zoomBy(0.8)}><Minus /></Button>
          <Button size="icon" aria-label="Reset dependency view" onClick={reset}><RotateCcw /></Button>
          <span className="text-meta text-muted">{Math.round(zoom * 100)}%</span>
        </div>
      )}
      <p id={help} className="sr-only">Drag or use arrow keys to pan. Plus and minus zoom; 0 resets the view.</p>
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-element-interactions -- Focused graph viewport supports keyboard and pointer panning. */}
      <div
        ref={box}
        role="group"
        aria-label={`Dependencies between the ${kinds}`}
        aria-describedby={help}
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The viewport itself supports keyboard pan and zoom.
        tabIndex={cards.length ? 0 : undefined}
        className="max-h-[60dvh] min-w-0 cursor-grab touch-none overflow-auto overscroll-contain select-none focus-visible:-outline-offset-2 active:cursor-grabbing"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={() => { drag.current = null; }}
        onPointerCancel={() => { drag.current = null; }}
        onClickCapture={(event) => { if (dragged.current && event.detail > 0) { event.preventDefault(); event.stopPropagation(); } }}
        onKeyDown={onKeyDown}
      >
        {cards.length > 0 && (
          <div style={{ width: (layout.width + PLAN_GRAPH_PAD * 2) * zoom, height: (layout.height + PLAN_GRAPH_PAD * 2) * zoom }}>
            <div className="relative origin-top-left" style={{ width: layout.width + PLAN_GRAPH_PAD * 2, height: layout.height + PLAN_GRAPH_PAD * 2, transform: `scale(${zoom})` }}>
              <EChart option={option} width={layout.width + PLAN_GRAPH_PAD * 2} height={layout.height + PLAN_GRAPH_PAD * 2} className="pointer-events-none" />
              {layout.nodes.map(({ card, x, y }) => (
                <button
                  key={card.id}
                  type="button"
                  data-plan-card={card.id}
                  aria-label={planNodeLabel(card, card.id === plan.mine?.id)}
                  aria-pressed={card.kind === 'subtask' ? card.id === selected : undefined}
                  title={`#${card.seq} ${card.title}`}
                  style={{ left: x + PLAN_GRAPH_PAD, top: y + PLAN_GRAPH_PAD, width: GRAPH_NODE.w, height: GRAPH_NODE.h }}
                  className="absolute cursor-pointer rounded-sm bg-transparent hover:bg-tint-hover/30 focus-visible:outline-offset-2"
                  onClick={() => open(card)}
                />
              ))}
            </div>
          </div>
        )}
      </div>
      {shown && details(shown)}
      {container && (
        <>
          <Button size="sm" className="self-start text-muted" aria-expanded={headOpen} onClick={() => setHeadOpen(!headOpen)}>
            {headOpen ? <ChevronDown /> : <ChevronRight />}
            {container.kind === 'epic' ? 'Epic' : 'Story'} details and dependencies
          </Button>
          {headOpen && details(container)}
        </>
      )}
    </div>
  );
}

const EMPTY: Card[] = [];

/** The level that shows card `id`: its parent's. */
function levelFor(plan: TaskPlan, id: string): string {
  const card = plan.byId.get(id);
  return card?.parent_id && plan.byId.has(card.parent_id) ? card.parent_id : '';
}
