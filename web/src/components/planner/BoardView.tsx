import { useMemo } from 'react';
import type { Card, CardStatus } from '../../api';
import { BOARD_COLUMNS, STATUS_LABEL, childIndex } from '../../lib/board';
import { cn } from '../../lib/cn';
import { useShownBoard } from './context';
import { CardMarkers, ProgressText, StatusGlyph, TaskChip } from './parts';

interface Lane {
  key: string;
  title: string;
  /** The story (or epic) the lane is, when it is one: its progress and a click to open it. */
  card?: Card;
  context?: string;
  leaves: Card[];
}

/**
 * Lanes by story, in outline order: each story's confirmed subtasks, then an epic's own
 * subtasks, then the root's. The epic filter keeps one epic; cancelled cards leave unless shown.
 */
function lanesOf(cards: readonly Card[], epic: string | null, showCancelled: boolean): Lane[] {
  const index = childIndex(cards);
  const shown = (c: Card) => c.confirmed && (showCancelled || c.status !== 'cancelled');
  const lanes: Lane[] = [];
  const direct = (id: string) => (index.get(id) ?? []).filter((c) => c.kind === 'subtask' && shown(c));
  const story = (s: Card, context?: string) => {
    const leaves = direct(s.id);
    for (const nested of (index.get(s.id) ?? []).filter((c) => c.kind === 'story' && shown(c))) story(nested, context);
    if (leaves.length || s.status !== 'cancelled') lanes.push({ key: s.id, title: s.title, card: s, context, leaves });
  };
  for (const top of (index.get('') ?? []).filter(shown)) {
    if (epic && top.id !== epic) continue;
    if (top.kind === 'epic') {
      for (const s of (index.get(top.id) ?? []).filter((c) => c.kind === 'story' && shown(c))) story(s, top.title);
      const own = direct(top.id);
      if (own.length) lanes.push({ key: `${top.id}:own`, title: `${top.title}, no story`, card: top, leaves: own });
    } else if (top.kind === 'story') story(top);
  }
  if (!epic) {
    const loose = (index.get('') ?? []).filter((c) => c.kind === 'subtask' && shown(c));
    if (loose.length) lanes.push({ key: 'root', title: 'No story', leaves: loose });
  }
  return lanes;
}

/**
 * The Board (ADR 0005 §10): kanban over subtasks, a column per status and a swimlane per
 * story. Held subtasks carry their Task's chip, which opens the Task. Suggestions stay in the Tree.
 */
export function BoardView() {
  const { ui, cards, sessions, openCard, openTask } = useShownBoard();
  const byId = useMemo(() => new Map(cards.map((c) => [c.id, c])), [cards]);
  const lanes = useMemo(() => lanesOf(cards, ui.epic, ui.showCancelled), [cards, ui.epic, ui.showCancelled]);
  const columns: CardStatus[] = ui.showCancelled ? [...BOARD_COLUMNS, 'cancelled'] : [...BOARD_COLUMNS];
  const counts = Object.fromEntries(columns.map((s) => [s, lanes.reduce((n, l) => n + l.leaves.filter((c) => c.status === s).length, 0)]));
  const grid = columns.length === 5 ? 'grid-cols-[repeat(5,minmax(152px,1fr))]' : 'grid-cols-[repeat(4,minmax(160px,1fr))]';

  if (!lanes.length) return <p className="px-4 py-6 text-ui text-muted">No subtasks match the filters.</p>;
  return (
    // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A labelled scroll region must accept keyboard scrolling.
    <div className="min-h-0 flex-1 overflow-auto overscroll-contain" role="region" aria-label="Board" tabIndex={0}>
      <div className={cn('grid gap-x-2 px-3 pb-6', grid)}>
        {/* Column heads stay in view while the lanes scroll under them. */}
        {columns.map((s) => (
          <div key={s} className="sticky top-0 z-10 flex h-9 items-center gap-1.5 bg-canvas px-1 text-caption text-muted">
            <StatusGlyph status={s} />
            <span className="font-medium text-body">{STATUS_LABEL[s]}</span>
            <span className="tabular-nums">{counts[s]}</span>
          </div>
        ))}
        {lanes.map((lane) => (
          <section key={lane.key} aria-label={`${lane.title} lane`} className="col-span-full grid grid-cols-subgrid gap-y-1 pt-3">
            <header className="col-span-full flex min-w-0 items-center gap-2 px-1 pb-1">
              {lane.card ? (
                <button type="button" className="min-w-0 truncate text-left text-ui font-medium text-ink hover:underline focus-visible:outline-offset-0" onClick={() => openCard(lane.card!.id)}>
                  #{lane.card.seq} {lane.title}
                </button>
              ) : (
                <span className="text-ui font-medium text-ink">{lane.title}</span>
              )}
              {lane.context && <span className="min-w-0 truncate text-caption text-muted">{lane.context}</span>}
              {lane.card && <ProgressText card={lane.card} />}
              <span className="fade-rule min-w-8 flex-1" aria-hidden="true" />
            </header>
            {columns.map((s) => (
              <ul key={s} aria-label={`${lane.title}, ${STATUS_LABEL[s]}`} className="flex min-h-12 flex-col gap-1 rounded-md bg-sunken p-1">
                {lane.leaves
                  .filter((c) => c.status === s)
                  .map((c) => (
                    <li key={c.id}>
                      <BoardCard card={c} byId={byId} selected={ui.selected === c.id} onOpen={() => openCard(c.id)} task={c.held_by ? <TaskChip taskId={c.held_by} sessions={sessions} onOpen={openTask} /> : null} />
                    </li>
                  ))}
              </ul>
            ))}
          </section>
        ))}
      </div>
    </div>
  );
}

function BoardCard({ card: c, byId, selected, onOpen, task }: Readonly<{ card: Card; byId: ReadonlyMap<string, Card>; selected: boolean; onOpen: () => void; task: React.ReactNode }>) {
  return (
    // A div, not a button: the Task chip inside is one. It is reached and opened by keyboard all the same.
    <div
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      aria-label={`#${c.seq} ${c.title}`}
      className={cn(
        'flex cursor-pointer flex-col gap-1 rounded-sm bg-raised px-2 py-1.5 text-left text-ui transition-colors duration-100 hover:bg-surface focus-visible:-outline-offset-2',
        selected && 'bg-tint-selected hover:bg-tint-selected',
        c.status === 'cancelled' && 'opacity-60',
      )}
      onClick={onOpen}
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget || (e.key !== 'Enter' && e.key !== ' ')) return;
        e.preventDefault();
        onOpen();
      }}
    >
      <span className="flex min-w-0 items-start gap-1.5">
        <span className="shrink-0 text-caption tabular-nums text-muted">#{c.seq}</span>
        <span className={cn('min-w-0 flex-1 text-ink [overflow-wrap:anywhere]', c.status === 'cancelled' && 'line-through')}>{c.title}</span>
      </span>
      {(task || c.pending_requests > 0 || c.stale || c.blocked || c.blocked_by.length > 0 || c.effort) && (
        <span className="flex min-w-0 flex-wrap items-center gap-1">
          {task}
          <CardMarkers card={c} byId={byId} compact />
          {c.effort && <span className="text-caption text-muted" title="Effort">{c.effort}</span>}
        </span>
      )}
    </div>
  );
}
