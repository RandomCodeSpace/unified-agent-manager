import { useApi } from '../../ApiContext';
import { Pencil } from 'lucide-react';
import { memo, useMemo, useState } from 'react';
import { type Card, type CardStatus } from '../../api';
import { BOARD_COLUMNS, STATUS_LABEL, childIndex, epicOf, lockedReason, openBlockerSeqs, pauseLabel } from '../../lib/board';
import { cn } from '../../lib/cn';
import type { ActionItem } from '../ui/menu';
import { CardMenuButton, CardMenus, useCardActions, useCardMenuHandle, type CardMenuHandle } from './actions';
import { useShownBoard } from './context';
import { CardMarkers, ProgressText, StatusGlyph, TaskChip } from './parts';
import { CardEditor } from './TreeView';

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
 * In a narrow container (a phone) the columns stack: each lane lists its statuses
 * with cards, under their names, one after another. Each card's "…" button and context menu hold
 * its actions.
 * Cards are memoised on their card object, so a `board` frame re-renders only the ones it changed.
 * Check at HEAD reads the Project's default command, `projectCmd`.
 */
export function BoardView({ projectCmd }: Readonly<{ projectCmd?: string }> = {}) {
  const api = useApi();
  const { ui, cards, openCard } = useShownBoard();
  const byId = useMemo(() => new Map(cards.map((c) => [c.id, c])), [cards]);
  const lanes = useMemo(() => lanesOf(cards, ui.epic, ui.showCancelled), [cards, ui.epic, ui.showCancelled]);
  const [editing, setEditing] = useState<string | null>(null);
  // Check at HEAD shows its run in the card panel, so a card's check opens it there.
  const cardActions = useCardActions({ onCheck: (c) => openCard(c.id), projectCmd });
  const menuHandle = useCardMenuHandle();
  const columns: CardStatus[] = ui.showCancelled ? [...BOARD_COLUMNS, 'cancelled'] : [...BOARD_COLUMNS];
  const counts = Object.fromEntries(columns.map((s) => [s, lanes.reduce((n, l) => n + l.leaves.filter((c) => c.status === s).length, 0)]));
  const grid = columns.length === 5 ? '@2xl:grid-cols-[repeat(5,minmax(152px,1fr))]' : '@2xl:grid-cols-[repeat(4,minmax(160px,1fr))]';

  /** A card's menu: Edit (in place, like the Tree's), then its actions. An Unassigned card is read-only. */
  function menu(c: Card): ActionItem[] {
    const locked = c.project_id ? lockedReason(c) : 'An Unassigned card is read-only until it moves into a Project.';
    const edit: ActionItem = locked
      ? { key: 'edit', label: 'Edit', icon: <Pencil />, disabled: true, reason: locked, onSelect: () => {} }
      : { key: 'edit', label: 'Edit', icon: <Pencil />, takesFocus: true, onSelect: () => setEditing(c.id) };
    return cardActions.menuOf(c, [edit]);
  }

  if (!lanes.length) {
    // Nothing confirmed to show: proposals (under the epic filter, if one is set), or filters that hide the rest.
    const proposals = cards.some((c) => !c.confirmed && c.status !== 'cancelled' && (!ui.epic || epicOf(c, byId)?.id === ui.epic));
    const hidden = lanesOf(cards, null, true).length > 0;
    return <p className="px-4 py-6 text-ui text-muted">{proposals ? 'Only proposals so far: they stay in the Tree until you confirm them or approve their epic.' : hidden ? 'No subtasks match the filters.' : 'No subtasks yet.'}</p>;
  }
  // A labelled scroll region, which must accept keyboard scrolling.
  const region = { role: 'region', 'aria-label': 'Board', tabIndex: 0, className: '@container min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain' };
  const lanesGrid = (
    <div className={cn('grid grid-cols-1 gap-x-2 px-3 pb-6', grid)}>
      {/* Column heads stay in view while the lanes scroll under them; stacked, each lane names its own. */}
      {columns.map((s) => (
        <div key={s} className="sticky top-0 z-10 hidden h-9 items-center gap-1.5 bg-canvas px-1 text-caption text-muted @2xl:flex">
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
          {columns.map((s) => {
            const here = lane.leaves.filter((c) => c.status === s);
            return (
              // An empty cell is only room in the grid (no well), and stacked it leaves.
              <div key={s} className={cn('flex flex-col gap-1', !here.length && '@max-2xl:hidden')}>
                <p className="flex items-center gap-1.5 px-1 pt-1 text-caption text-muted @2xl:hidden">
                  <StatusGlyph status={s} />
                  <span className="font-medium text-body">{STATUS_LABEL[s]}</span>
                  <span className="tabular-nums">{here.length}</span>
                </p>
                <ul aria-label={`${lane.title}, ${STATUS_LABEL[s]}`} className={cn('flex min-h-12 flex-1 flex-col gap-1 rounded-md p-1', here.length > 0 && 'bg-sunken')}>
                  {here.map((c) => (
                    <li key={c.id}>
                      {editing === c.id ? (
                        <CardEditor
                          card={c}
                          onCancel={() => setEditing(null)}
                          onSave={async (patch) => {
                            setEditing(null);
                            await cardActions.run(c.id, 'edit', 'save the card', () => api.planner.edit(c.id, patch));
                          }}
                        />
                      ) : (
                        <BoardCard card={c} blockers={openBlockerSeqs(c, byId)} pause={pauseLabel(c, byId)} selected={ui.selected === c.id} onOpen={openCard} menu={menuHandle} />
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            );
          })}
        </section>
      ))}
    </div>
  );
  return (
    <>
      <CardMenus handle={menuHandle} items={(id) => (byId.has(id) ? menu(byId.get(id)!) : [])} render={<div />} {...region}>
        {lanesGrid}
      </CardMenus>
      {cardActions.dialogs}
    </>
  );
}

const BoardCard = memo(function BoardCard({ card: c, blockers, pause, selected, onOpen, menu }: Readonly<{ card: Card; blockers: string; pause: string; selected: boolean; onOpen: (id: string) => void; menu?: CardMenuHandle }>) {
  return (
    // A div, not a button: the Task chip and the "…" inside are ones. It is reached and opened by keyboard all the same.
    <div
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      aria-label={`#${c.seq} ${c.title}`}
      data-card={c.id}
      className={cn(
        'group/card relative flex cursor-pointer flex-col gap-1 rounded-sm bg-raised px-2 py-1.5 text-left text-ui transition-colors duration-100 hover:bg-surface focus-visible:-outline-offset-2',
        selected && 'bg-tint-selected hover:bg-tint-selected',
        c.status === 'cancelled' && 'opacity-60',
      )}
      onClick={() => onOpen(c.id)}
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget || (e.key !== 'Enter' && e.key !== ' ')) return;
        e.preventDefault();
        onOpen(c.id);
      }}
    >
      <span className={cn('flex min-w-0 items-start gap-1.5', menu && 'pr-5')}>
        <span className="shrink-0 text-caption tabular-nums text-muted">#{c.seq}</span>
        <span className={cn('min-w-0 flex-1 text-ink [overflow-wrap:anywhere]', c.status === 'cancelled' && 'line-through')}>{c.title}</span>
      </span>
      {(c.held_by || c.pending_requests > 0 || c.stale || c.blocked || blockers || pause || c.effort) && (
        <span className="flex min-w-0 flex-wrap items-center gap-1">
          {c.held_by && <TaskChip taskId={c.held_by} />}
          <CardMarkers card={c} blockers={blockers} pause={pause} compact />
          {c.effort && <span className="text-caption text-muted">Effort {c.effort}</span>}
        </span>
      )}
      {menu && (
        <CardMenuButton
          handle={menu}
          card={c.id}
          label={`Actions for #${c.seq}`}
          className="absolute top-1 right-1 opacity-0 transition-opacity duration-100 group-focus-within/card:opacity-100 group-hover/card:opacity-100 data-popup-open:opacity-100 pointer-coarse:opacity-100"
        />
      )}
    </div>
  );
});
