import { ChevronDown, ChevronRight, Lock } from 'lucide-react';
import { useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import type { Card } from '../../api';
import { GRAPH_NODE, cardPath, layoutLevel, waitsOf, wrapText, type GraphNode } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Button } from '../ui/button';
import { STATUS_WORD, WaitList, type TaskPlan } from './TaskPlan';

/** Room around the drawing, so focus rings and arrowheads stay inside it. */
const PAD = 8;
/** Characters per title line and lines per title, for the node's width at caption size. */
const LINE_CHARS = 24;
const TITLE_LINES = 2;

const KIND_WORDS = { epic: 'epics', story: 'stories', subtask: 'subtasks' } as const;

/**
 * The plan's dependencies one level at a time (ADR 0005 §3: links never mix levels): the
 * Project's epics, one epic's stories, or one story's subtasks. A breadcrumb moves between
 * levels and a container's node opens its level; a subtask's node opens its details under the
 * graph (`details`). Arrows run from what has to finish first. Drawn in plain SVG (shapes and
 * text, no HTML inside), left to right when the layers fit the panel's width, else top to
 * bottom; a level wider than the panel scrolls inside its own box.
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
  const [room, setRoom] = useState(0);
  const [headOpen, setHeadOpen] = useState(false);
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
  const layout = lr.width + PAD * 2 <= room ? lr : tb;
  const uid = useId().replaceAll(':', '');
  const path = container ? cardPath(container, plan.byId) : [];
  const mineIds = new Set([plan.mine?.id, ...plan.path.map((c) => c.id)]);
  const shown = selected ? cards.find((c) => c.id === selected && c.kind === 'subtask') : undefined;
  const waits = container ? waitsOf(container, plan.byId) : [];
  const kinds = [...new Set(cards.map((c) => c.kind))].map((k) => KIND_WORDS[k]).join(' and ');
  const progress = container?.progress;

  const open = (card: Card) => (card.kind === 'subtask' ? onSelect(card.id) : onLevel(card.id));
  const onKey = (e: KeyboardEvent, card: Card) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    open(card);
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
        {progress && ` · ${progress.done} of ${progress.total} subtasks done`}
        {layout.edges.length > 0 && ' · arrows point from what finishes first'}
      </p>
      <div ref={box} className="min-w-0 overflow-x-auto overscroll-x-contain">
        {cards.length > 0 && (
          <svg
            width={layout.width + PAD * 2}
            height={layout.height + PAD * 2}
            viewBox={`0 0 ${layout.width + PAD * 2} ${layout.height + PAD * 2}`}
            role="group"
            aria-label={`Dependencies between the ${kinds}`}
            className="block"
          >
            <defs>
              {(['muted', 'done', 'mine'] as const).map((k) => (
                <marker key={k} id={`${uid}-${k}`} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                  <path d="M0 0 L8 4 L0 8 z" className={k === 'mine' ? 'fill-accent' : k === 'done' ? 'fill-hairline-strong' : 'fill-muted'} />
                </marker>
              ))}
            </defs>
            {layout.edges.map((e) => {
              const tone = mineIds.has(e.from.card.id) || mineIds.has(e.to.card.id) ? 'mine' : e.from.card.status === 'done' ? 'done' : 'muted';
              return (
                <path
                  key={`${e.from.card.id}>${e.to.card.id}`}
                  d={edgePath(e.from, e.to, layout.dir)}
                  markerEnd={`url(#${uid}-${tone})`}
                  strokeWidth={1.5}
                  strokeDasharray={!e.from.card.confirmed || !e.to.card.confirmed ? '4 3' : undefined}
                  className={cn('fill-none', tone === 'mine' ? 'stroke-accent' : tone === 'done' ? 'stroke-hairline-strong' : 'stroke-muted')}
                />
              );
            })}
            {layout.nodes.map((n) => (
              <Node key={n.card.id} node={n} mine={n.card.id === plan.mine?.id} onPath={mineIds.has(n.card.id)} selected={n.card.id === selected} onOpen={open} onKey={onKey} />
            ))}
          </svg>
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

/** A curve from the blocker's far side to the waiting card's near side, ending just short of it for the arrowhead. */
function edgePath(a: GraphNode, b: GraphNode, dir: 'lr' | 'tb'): string {
  const { w, h } = GRAPH_NODE;
  if (dir === 'lr') {
    const sx = a.x + w + PAD, sy = a.y + h / 2 + PAD, ex = b.x - 2 + PAD, ey = b.y + h / 2 + PAD, d = (ex - sx) / 2;
    return `M${sx} ${sy} C${sx + d} ${sy} ${ex - d} ${ey} ${ex} ${ey}`;
  }
  const sx = a.x + w / 2 + PAD, sy = a.y + h + PAD, ex = b.x + w / 2 + PAD, ey = b.y - 2 + PAD, d = (ey - sy) / 2;
  return `M${sx} ${sy} C${sx} ${sy + d} ${ex} ${ey - d} ${ex} ${ey}`;
}

/**
 * One card as a node: #seq and its title wrapped to two lines, then its state (a container's
 * progress), and "This task" or "Proposed". A focusable SVG group; Enter or Space opens it like a click.
 */
function Node({ node: { card: c, x, y }, mine, onPath, selected, onOpen, onKey }: Readonly<{
  node: GraphNode;
  mine: boolean;
  onPath: boolean;
  selected: boolean;
  onOpen: (c: Card) => void;
  onKey: (e: KeyboardEvent, c: Card) => void;
}>) {
  const { w, h } = GRAPH_NODE;
  const container = c.kind !== 'subtask';
  const lines = wrapText(`#${c.seq} ${c.title}`, LINE_CHARS, TITLE_LINES);
  const state = container ? `${c.progress?.done ?? 0} of ${c.progress?.total ?? 0} done` : STATUS_WORD[c.status];
  const tag = mine ? 'This task' : c.confirmed ? '' : 'Proposed';
  return (
    // An SVG group as a button: SVG has no button element, and the drawing must stay plain SVG.
    <g
      role="button"
      tabIndex={0}
      data-plan-card={c.id}
      aria-label={`#${c.seq} ${c.title}, ${state}${tag ? `, ${tag.toLowerCase()}` : ''}. ${container ? `Show its ${c.kind === 'epic' ? 'stories' : 'subtasks'}` : 'Show its details'}`}
      aria-pressed={container ? undefined : selected}
      transform={`translate(${x + PAD} ${y + PAD})`}
      className="group/node cursor-pointer outline-none"
      onClick={() => onOpen(c)}
      onKeyDown={(e) => onKey(e, c)}
    >
      <title>{`#${c.seq} ${c.title}`}</title>
      <rect
        width={w}
        height={h}
        rx={6}
        strokeWidth={mine ? 2 : 1}
        strokeDasharray={c.confirmed ? undefined : '4 3'}
        className={cn(
          'transition-colors group-hover/node:fill-tint-hover',
          onPath ? 'fill-tint-selected' : c.confirmed ? 'fill-raised' : 'fill-canvas',
          mine ? 'stroke-accent' : selected ? 'stroke-ink' : 'stroke-hairline-strong',
          c.status === 'done' && !onPath && 'fill-surface',
        )}
      />
      <rect x={-3} y={-3} width={w + 6} height={h + 6} rx={8} strokeWidth={2} className="fill-none stroke-focus opacity-0 group-focus-visible/node:opacity-100" />
      <text className="fill-ink text-caption">
        {lines.map((line, i) => (
          <tspan key={i} x={8} y={18 + i * 15}>
            {line}
          </tspan>
        ))}
      </text>
      <text x={8} y={h - 9} className={cn('text-meta', c.status === 'done' ? 'fill-success' : c.status === 'doing' ? 'fill-accent' : 'fill-muted')}>
        {state}
      </text>
      {tag && (
        <text x={w - 8} y={h - 9} textAnchor="end" className={cn('text-meta', mine ? 'fill-accent font-medium' : 'fill-muted')}>
          {tag}
        </text>
      )}
    </g>
  );
}
