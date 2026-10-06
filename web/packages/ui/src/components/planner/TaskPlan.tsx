import { useApi } from '../../ApiContext';
import { BadgeCheck, ChevronDown, ChevronRight, ListTree, Lock, Pencil, Plus, X } from 'lucide-react';
import { memo, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { popupOpen } from '../../App';
import { plannerErrorText, taskName, type Card, type CardStatus, type Project } from '../../api';
import { approvedEpicOf, cardPath, childIndex, lockedReason, nextSubtask, shownProgress, taskCard, waitsOf, type Wait } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Markdown, Note } from '../common';
import { PanelHeader, SidePanel } from '../Subagents';
import { Button } from '../ui/button';
import { Chip } from '../ui/chip';
import { AlertDialog } from '../ui/dialog';
import { Input } from '../ui/input';
import { Segmented } from '../ui/segmented';
import { Select } from '../ui/select';
import { Tip } from '../ui/tooltip';
import { useCardActions } from './actions';
import { Checklist, Links } from './CardPanel';
import { INITIAL_UI, PlannerContext, usePlanner, usePlannerTasks, type PlannerContextValue, type PlannerNotice } from './context';
import { KindIcon, NoticeBar, ProgressRing, ProgressText, StatusGlyph, TaskChip, kindLabel } from './parts';
import { PlanGraph } from './PlanGraph';
import { RequestItem } from './Requests';
import { CardEditor } from './TreeView';

/** A card's state in plain words, in a Task's plan. */
export const STATUS_WORD: Record<CardStatus, string> = { planned: 'Planned', todo: 'To do', doing: 'In progress', done: 'Done', cancelled: 'Cancelled' };

/**
 * The plan as one Task sees it (ADR 0005 §10): its Project's live cards, the subtask the Task
 * works on (`mine`) and the story and epic above it. Null where the Task has no plan to show:
 * the planner is off, or the Project has no git. `status` is `none` while the plan is empty.
 */
export interface TaskPlan {
  status: 'loading' | 'error' | 'none' | 'ready';
  error?: string;
  project: Project;
  cards: Card[];
  byId: ReadonlyMap<string, Card>;
  index: ReadonlyMap<string, Card[]>;
  mine?: Card;
  /** The containers above `mine`, root first. */
  path: Card[];
}

export function useTaskPlan(taskId: string, project: Project | undefined): TaskPlan | null {
  const ctx = useContext(PlannerContext);
  const on = !!ctx?.enabled && !!project && !project.no_git;
  const board = on ? ctx.boards[project.id] : undefined;
  const data = board?.data;
  const error = board?.error;
  return useMemo(() => {
    if (!on || !project) return null;
    // Cancelled cards (dismissed and expired proposals among them) are history, not the plan.
    const cards = (data?.cards ?? []).filter((c) => c.status !== 'cancelled');
    const byId = new Map(cards.map((c) => [c.id, c]));
    const index = childIndex(cards);
    const mine = taskCard(cards, taskId);
    let status: TaskPlan['status'] = 'ready';
    if (!data) status = error ? 'error' : 'loading';
    else if (!cards.length) status = 'none';
    return { status, error, project, cards, byId, index, mine, path: mine ? cardPath(mine, byId).slice(0, -1) : [] };
  }, [on, project, data, error, taskId]);
}

/** "#18 Release automation (through its epic)" items, for a line that says what a card waits for. */
export function WaitList({ waits, onOpen }: Readonly<{ waits: Wait[]; onOpen: (id: string) => void }>) {
  return (
    <>
      {waits.map((w, i) => (
        <span key={`${w.card.id}:${w.via?.id ?? ''}`}>
          {i > 0 && (i === waits.length - 1 ? ' and ' : ', ')}
          <button type="button" className="text-left underline decoration-hairline-strong underline-offset-2 hover:text-ink" onClick={() => onOpen(w.card.id)}>
            #{w.card.seq} {w.card.title}
          </button>
          {w.via && <span className="text-muted"> (through its {w.via.kind})</span>}
        </span>
      ))}
    </>
  );
}

/* ---------- The story strip, under the Task's header ---------- */

/**
 * One line under a Task's header while its Project has a plan: where the Task sits ("Epic ›
 * Story · 1/4 done · This task #40 · next #41 · waits for …"), or, for a Task with no card, a
 * quiet offer to add it to a story. A click opens the Plan panel on the Task's card.
 */
export const StoryStrip = memo(function StoryStrip({ plan, open, onOpen }: Readonly<{ plan: TaskPlan; open: boolean; onOpen: () => void }>) {
  if (plan.status !== 'ready') return null;
  const { mine, path } = plan;
  const container = path.at(-1);
  let line: ReactNode;
  let label: string;
  if (mine) {
    const next = container ? nextSubtask(container, plan.index, plan.byId, mine.id) : undefined;
    const waits = waitsOf(mine, plan.byId);
    const progress = container?.progress && shownProgress(container.progress);
    label = `This task works on #${mine.seq} ${mine.title}${path.length ? ` in ${path.map((c) => c.title).join(' › ')}` : ''}. Show the plan`;
    line = (
      <>
        {path.length > 1 && <span className="min-w-0 shrink-[2] truncate text-muted @max-2xl:hidden">{path.slice(0, -1).map((c) => c.title).join(' › ')} ›</span>}
        {container && <span className="min-w-0 truncate font-medium text-ink">{container.title}</span>}
        {progress && (
          <span className="shrink-0 tabular-nums text-muted">
            · {progress.done}/{progress.total}
            <span className="@max-lg:hidden"> done</span>
            {progress.proposed > 0 && ` · ${progress.proposed} proposed`}
          </span>
        )}
        <span className={cn('shrink-0 text-accent', container && '@max-md:hidden')}>· This task #{mine.seq}</span>
        {!container && <span className="min-w-0 truncate text-ink">{mine.title}</span>}
        {mine.status === 'done' && <span className="shrink-0 text-success">(done)</span>}
        {next && <span className="shrink-0 text-muted @max-xl:hidden">· next #{next.seq}</span>}
        {waits.length > 0 && (
          <span className="flex min-w-0 shrink items-center gap-1 text-warning" title={`Waits for ${waits.map((w) => `#${w.card.seq} ${w.card.title}${w.via ? ` (through its ${w.via.kind})` : ''}`).join(', ')}`}>
            <span aria-hidden="true">·</span>
            <Lock aria-hidden="true" className="size-3 shrink-0" />
            <span className="truncate @max-xl:sr-only">
              waits for {waits[0].card.title}
              {waits[0].via ? ` (through its ${waits[0].via.kind})` : ''}
              {waits.length > 1 ? ` +${waits.length - 1}` : ''}
            </span>
          </span>
        )}
      </>
    );
  } else {
    label = 'This task is not part of a story. Add it to a story';
    line = (
      <>
        <span className="min-w-0 truncate text-muted">Not part of a story</span>
        <span className="flex shrink-0 items-center gap-0.5 text-body">
          <Plus aria-hidden="true" className="size-3.5" />
          Add to a story
        </span>
      </>
    );
  }
  return (
    <button
      type="button"
      aria-label={label}
      aria-expanded={open}
      aria-controls={open ? 'plan-panel' : undefined}
      className="@container flex h-9 w-full min-w-0 shrink-0 items-center gap-1.5 px-3 text-left text-caption whitespace-nowrap transition-colors hover:bg-tint-hover focus-visible:-outline-offset-2 pointer-coarse:h-11 sm:px-4 md:px-6"
      onClick={onOpen}
    >
      <ListTree aria-hidden="true" className="size-3.5 shrink-0 text-muted" />
      {line}
    </button>
  );
});

/** The header's Plan button, beside Changes and Files. */
export function PlanButton({ open, label, onToggle }: Readonly<{ open: boolean; label: boolean; onToggle: () => void }>) {
  return (
    <Tip label="The project's plan">
      <Button id="plan-link" size="md" aria-pressed={open} aria-label="Plan" className="px-2 text-muted" onClick={onToggle}>
        <ListTree />
        {label && <span>Plan</span>}
      </Button>
    </Tip>
  );
}

/* ---------- The Plan panel ---------- */

/** A card to show in the panel, from the strip (the Task's own card) or a card link; `at` tells two asks for one card apart. */
export interface PlanFocus {
  id: string | null;
  at: number;
}

const VIEWS = [
  { value: 'outline', label: 'Outline' },
  { value: 'graph', label: 'Graph' },
];

/**
 * The Plan panel (ADR 0005 §10), in the side-panel slot Files and Changes use: what this Task
 * works on, then the Project's plan as an Outline (epics › stories › subtasks, a card's details
 * opening in place) or a Graph of one level's dependencies at a time. Card links inside the panel
 * and the Task open here, never the Planner. Its view state is its own, so following Tasks never
 * moves the Planner's. Memoised: a streamed reply re-renders the Task, not this.
 */
export const PlanPanel = memo(function PlanPanel({ plan, taskId, taskName, focus, inline, open, onClose, onClosed }: Readonly<{
  plan: TaskPlan;
  taskId: string;
  taskName: string;
  focus: PlanFocus;
  inline: boolean;
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
}>) {
  const global = useContext(PlannerContext);
  const [view, setView] = useState<'outline' | 'graph'>('outline');
  // The card whose details are open, and the graph's level ('' for the Project's top level).
  const firstShown = focus.id ?? plan.mine?.id ?? null;
  const [shown, setShown] = useState<string | null>(firstShown);
  const [level, setLevel] = useState(() => levelOf(plan, firstShown));
  const [seen, setSeen] = useState(focus);
  if (seen !== focus) {
    setSeen(focus);
    const id = focus.id ?? plan.mine?.id ?? null;
    setShown(id);
    setLevel(levelOf(plan, id));
  }
  // A Task just added to a story: its new card opens, at its level, once the plan has it.
  const mineId = plan.mine?.id;
  const [seenMine, setSeenMine] = useState(mineId);
  if (seenMine !== mineId) {
    setSeenMine(mineId);
    if (mineId) {
      setShown(mineId);
      setLevel(levelOf(plan, mineId));
    }
  }
  const [notice, notify] = useState<PlannerNotice | null>(null);
  const scroller = useRef<HTMLDivElement>(null);

  const show = useCallback((id: string) => {
    setShown(id);
    setLevel(levelOf(plan, id));
  }, [plan]);
  // The actions, the checklist and the dependencies read the planner's context: here, this Project's Board, opening cards in place.
  const value = useMemo<PlannerContextValue | null>(() => global && {
    ...global,
    ui: { ...INITIAL_UI, project: plan.project.id, selected: shown },
    setUi: () => {},
    openCard: show,
    notice,
    notify,
  }, [global, plan.project.id, shown, show, notice]);

  // The card asked for comes into view in the panel's own scroller (never the page's), once, as it opens.
  useLayoutEffect(() => {
    const box = scroller.current;
    const row = shown ? box?.querySelector<HTMLElement>(`[data-plan-card="${CSS.escape(shown)}"]`) : null;
    if (!box || !row) return;
    const top = row.getBoundingClientRect().top - box.getBoundingClientRect().top;
    if (top < 0 || top > box.clientHeight - 48) box.scrollTop += top - 96;
    // Only when the ask changes, not on every card update.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seen, view]);

  // Inline, the panel is no dialog: Esc closes it unless a popup or a field being edited owns the key.
  useEffect(() => {
    if (!inline || !open) return;
    const escape = (event: KeyboardEvent) => {
      const editing = (event.target as Element | null)?.closest?.('input, textarea, select, [contenteditable="true"]');
      if (event.key === 'Escape' && !event.defaultPrevented && !editing && !popupOpen()) onClose();
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, open, onClose]);

  if (!value) return null;
  return (
    <SidePanel id="plan" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Plan" preferWidth={view === 'graph' ? 720 : 0}>
      <PanelHeader>
        <ListTree aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <h2 className="text-title text-ink">Plan</h2>
        <span className="min-w-0 flex-1 truncate text-caption text-muted">{plan.project.name}</span>
        <Button size="icon-md" aria-label="Close plan" className="text-muted" onClick={onClose}>
          <X />
        </Button>
      </PanelHeader>
      <PlannerContext.Provider value={value}>
        <div id="plan-panel" ref={scroller} className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto overflow-x-hidden overscroll-contain px-3 pt-1 pb-6">
          <NoticeBar />
          {plan.status === 'loading' && <p className="px-1 text-caption text-muted">Loading the plan…</p>}
          {plan.status === 'error' && (
            <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
              <span className="min-w-0 flex-1">Could not load the plan: {plan.error}</span>
              <Button size="sm" variant="secondary" onClick={() => value.reload(plan.project.id)}>
                Retry
              </Button>
            </Note>
          )}
          {plan.status === 'none' && (
            <div className="flex flex-col gap-1 px-1 py-6 text-center">
              <p className="text-ui text-body">No plan for {plan.project.name} yet.</p>
              <p className="text-caption text-muted">When you or an agent plan the work as epics, stories and subtasks in the Planner, they show here.</p>
            </div>
          )}
          {plan.status === 'ready' && (
            <>
              {plan.mine ? <ThisTask plan={plan} onOpen={show} /> : <AttachBox plan={plan} taskId={taskId} taskName={taskName} onAttached={show} />}
              <Segmented size="sm" aria-label="Plan view" className="w-44 shrink-0" value={view} onValueChange={(v) => setView(v as 'outline' | 'graph')} items={VIEWS} />
              {view === 'outline' ? (
                <Outline plan={plan} taskId={taskId} shown={shown} ask={seen} onShow={(id) => setShown(shown === id ? null : id)} />
              ) : (
                <PlanGraph
                  plan={plan}
                  level={level}
                  selected={shown}
                  onLevel={(id) => {
                    setLevel(id);
                    setShown(null);
                  }}
                  onSelect={(id) => setShown(shown === id ? null : id)}
                  details={(card) => <CardDetails key={card.id} card={card} plan={plan} taskId={taskId} />}
                />
              )}
            </>
          )}
        </div>
      </PlannerContext.Provider>
    </SidePanel>
  );
});

/** The graph level a card is drawn at: its parent's ('' for the Project's top level); a Task's own story without a card asked for. */
function levelOf(plan: TaskPlan, id: string | null): string {
  const card = id ? plan.byId.get(id) : undefined;
  if (card) return card.parent_id && plan.byId.has(card.parent_id) ? card.parent_id : '';
  return plan.path.at(-1)?.id ?? '';
}

/** Where this Task sits: Epic › Story, its card, and what it waits for. */
function ThisTask({ plan, onOpen }: Readonly<{ plan: TaskPlan; onOpen: (id: string) => void }>) {
  const mine = plan.mine!;
  const waits = waitsOf(mine, plan.byId);
  return (
    <section aria-label="This task works on" className="flex flex-col gap-1 rounded-md bg-tint-selected px-3 py-2">
      <h3 className="text-eyebrow text-muted uppercase">This task works on</h3>
      {plan.path.length > 0 && <p className="min-w-0 truncate text-caption text-muted">{plan.path.map((c) => c.title).join(' › ')}</p>}
      <button type="button" className="flex min-w-0 items-center gap-1.5 text-left text-ui text-ink hover:underline pointer-coarse:min-h-11" onClick={() => onOpen(mine.id)}>
        <StatusGlyph status={mine.status} />
        <span className="shrink-0 tabular-nums text-muted">#{mine.seq}</span>
        <span className="truncate font-medium">{mine.title}</span>
        <span className="ml-auto shrink-0 text-caption text-muted">{STATUS_WORD[mine.status]}</span>
      </button>
      {waits.length > 0 && (
        <p className="flex min-w-0 items-start gap-1.5 text-caption text-warning">
          <Lock aria-hidden="true" className="mt-0.5 size-3 shrink-0" />
          <span className="min-w-0">
            Waits for <WaitList waits={waits} onOpen={onOpen} />
          </span>
        </p>
      )}
    </section>
  );
}

/* ---------- The outline ---------- */

/**
 * Epics › stories › subtasks, each container with its progress. The Task's own card is marked
 * and its story open; a chevron folds a container, and a click on a card opens its details under
 * it (one at a time). Proposals are rows with a dashed edge, plannable like any other.
 */
function Outline({ plan, taskId, shown, ask, onShow }: Readonly<{ plan: TaskPlan; taskId: string; shown: string | null; ask: PlanFocus; onShow: (id: string) => void }>) {
  const pathOf = (id: string | null | undefined) => {
    const c = id ? plan.byId.get(id) : undefined;
    return c ? cardPath(c, plan.byId).slice(0, -1).map((x) => x.id) : [];
  };
  const [unfolded, setUnfolded] = useState<ReadonlySet<string>>(() => new Set([...pathOf(plan.mine?.id), ...pathOf(shown)]));
  // A new ask (the strip, a card link) opens the containers above its card.
  const [seenAsk, setSeenAsk] = useState(ask);
  if (seenAsk !== ask) {
    setSeenAsk(ask);
    const add = pathOf(shown).filter((id) => !unfolded.has(id));
    if (add.length) setUnfolded(new Set([...unfolded, ...add]));
  }
  // So does the Task's own card when the Task is added to a story.
  const [seenMine, setSeenMine] = useState(plan.mine?.id);
  if (seenMine !== plan.mine?.id) {
    setSeenMine(plan.mine?.id);
    const add = pathOf(plan.mine?.id).filter((id) => !unfolded.has(id));
    if (add.length) setUnfolded(new Set([...unfolded, ...add]));
  }
  const fold = (id: string) =>
    setUnfolded((s) => {
      const next = new Set(s);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  const node = (c: Card): ReactNode => {
    const kids = plan.index.get(c.id) ?? [];
    const container = c.kind !== 'subtask';
    const open = unfolded.has(c.id);
    const own = waitsOf(c, plan.byId).filter((w) => !w.via);
    return (
      <li key={c.id} className="flex flex-col gap-0.5">
        <div className="flex min-w-0 items-center">
          {container ? (
            <Button size="icon" aria-label={`${open ? 'Hide' : 'Show'} what #${c.seq} holds`} aria-expanded={open} className="shrink-0 text-faint" onClick={() => fold(c.id)}>
              {open ? <ChevronDown /> : <ChevronRight />}
            </Button>
          ) : (
            <span aria-hidden="true" className="w-7 shrink-0" />
          )}
          <CardRow card={c} taskId={taskId} open={shown === c.id} waits={own} onClick={() => onShow(c.id)} />
        </div>
        {shown === c.id && <CardDetails card={c} plan={plan} taskId={taskId} className="mb-1 ml-7" />}
        {container && open && kids.length > 0 && <ul className="ml-3 flex flex-col gap-0.5">{kids.map(node)}</ul>}
        {container && open && kids.length === 0 && <p className="ml-9 text-caption text-muted">Nothing under it yet.</p>}
      </li>
    );
  };
  return (
    <ul aria-label="Plan outline" className="flex flex-col gap-0.5">
      {(plan.index.get('') ?? []).map(node)}
    </ul>
  );
}

/** One card as a row: its mark, #seq and title, what it waits for at its own level, and who works on it. */
function CardRow({ card: c, taskId, open, waits, onClick }: Readonly<{ card: Card; taskId: string; open: boolean; waits: Wait[]; onClick: () => void }>) {
  const container = c.kind !== 'subtask';
  const mine = c.held_by === taskId || (c.status === 'done' && c.worked_by === taskId);
  return (
    <button
      type="button"
      data-plan-card={c.id}
      aria-expanded={open}
      aria-label={`#${c.seq} ${c.title}, ${STATUS_WORD[c.status]}${c.confirmed ? '' : ', proposed'}${mine ? ', this task' : ''}`}
      className={cn(
        'flex min-h-8 min-w-0 flex-1 items-center gap-1.5 rounded-sm px-1.5 text-left text-ui text-body transition-colors hover:bg-tint-hover pointer-coarse:min-h-11',
        !c.confirmed && 'outline-1 -outline-offset-1 outline-hairline-strong outline-dashed',
        mine && 'bg-tint-selected text-ink shadow-[inset_2px_0_0_var(--color-accent)] hover:bg-tint-selected',
        open && !mine && 'bg-tint-hover',
        c.kind === 'epic' && 'font-semibold text-ink',
        c.kind === 'story' && 'font-medium text-ink',
      )}
      onClick={onClick}
    >
      {container ? <ProgressRing card={c} /> : <StatusGlyph status={c.status} />}
      <span className="shrink-0 text-caption tabular-nums text-muted">#{c.seq}</span>
      <span className="min-w-0 flex-1 truncate">{c.title}</span>
      {waits.length > 0 && (
        <span className="flex shrink-0 items-center gap-0.5 text-caption text-warning" title={`Waits for ${waits.map((w) => `#${w.card.seq} ${w.card.title}`).join(', ')}`}>
          <Lock aria-hidden="true" className="size-3" />
          after {waits.map((w) => `#${w.card.seq}`).join(', ')}
        </span>
      )}
      {!c.confirmed && <Chip className="max-sm:hidden">Proposed</Chip>}
      {mine && <Chip tone="accent" className="font-medium">This task</Chip>}
      {!mine && c.held_by && <HolderChip taskId={c.held_by} />}
      {container && <ProgressText card={c} className="min-w-8 text-right" />}
    </button>
  );
}

/** The Task working on a card, by name; not a link here, since the row it sits in is one. */
function HolderChip({ taskId }: Readonly<{ taskId: string }>) {
  const { sessions } = usePlannerTasks();
  const s = sessions.find((x) => x.id === taskId);
  const name = s ? taskName(s) || 'New task' : 'another task';
  return (
    <Chip className="max-w-28 max-sm:hidden" title={`In progress in ${name}`}>
      <span className="truncate">{name}</span>
    </Chip>
  );
}

/* ---------- A card's details, in place ---------- */

/**
 * A card's details where it is shown (under its outline row, or under the graph): what done
 * means, its description and checklist, its dependencies at its own level and what it waits for
 * through its parents, the agents' requests on it, and Edit, Discard for a proposal, Launch
 * (with the confirm step) for a subtask, and Approve for an epic (ADR 0006 §8). Under an approved
 * epic nothing is launched here. A started subtask keeps its plan and says why.
 */
export function CardDetails({ card: c, plan, taskId, className }: Readonly<{ card: Card; plan: TaskPlan; taskId: string; className?: string }>) {
  const api = useApi();
  const { boards, openCard } = usePlanner();
  const cardActions = useCardActions();
  const [editing, setEditing] = useState(false);
  const busy = cardActions.busy?.key ?? null;
  const locked = lockedReason(c);
  const requests = (boards[plan.project.id]?.data?.requests ?? []).filter((r) => r.card_id === c.id);
  const launch = c.kind === 'subtask' ? cardActions.actionsOf(c).find((a) => a.key === 'launch') : undefined;
  const approve = c.kind === 'epic' ? cardActions.actionsOf(c).find((a) => a.key === 'approve') : undefined;
  const approved = approvedEpicOf(c, plan.byId);
  const mine = c.held_by === taskId || (c.status === 'done' && c.worked_by === taskId);
  const run = (key: string, verb: string, op: () => Promise<unknown>) => cardActions.run(c.id, key, verb, op);
  return (
    <section aria-label={`#${c.seq} details`} className={cn('flex flex-col gap-3 rounded-md bg-raised px-3 py-2.5 text-ui shadow-raised', className)}>
      <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-muted">
        <KindIcon kind={c.kind} />
        <span>{kindLabel(c.kind)}</span>
        <span aria-hidden="true">·</span>
        <span className="flex items-center gap-1">
          <StatusGlyph status={c.status} />
          {STATUS_WORD[c.status]}
        </span>
        {mine && <span className="text-accent">· This task works on it</span>}
        {!mine && c.held_by && <TaskChip taskId={c.held_by} />}
      </p>
      {!c.confirmed && <Note>{approved ? `A proposal: editing and linking keep it one; approving #${approved.seq} again confirms it.` : 'A proposal: editing and linking keep it one; launching it confirms it.'}</Note>}
      {locked && <Note>{locked}</Note>}
      {editing ? (
        <CardEditor
          card={c}
          onCancel={() => setEditing(false)}
          onSave={async (patch) => {
            setEditing(false);
            await run('edit', 'save the card', () => api.planner.edit(c.id, patch));
          }}
        />
      ) : (
        <div className="flex flex-col gap-0.5">
          <p className="text-title text-ink [overflow-wrap:anywhere]">{c.title}</p>
          <p className={cn('[overflow-wrap:anywhere]', c.win_condition ? 'text-body' : 'text-muted')}>
            {c.win_condition ? (
              <>
                <span className="text-muted">Done when: </span>
                {c.win_condition}
              </>
            ) : (
              'No "done when" yet.'
            )}
          </p>
        </div>
      )}
      {c.desc && <Markdown text={c.desc} />}
      {c.kind === 'subtask' && (
        <Checklist
          card={c}
          readOnly={c.status === 'cancelled'}
          locked={!!locked}
          busy={!!busy}
          save={async (checklist) => (await run('checklist', 'save the checklist', async () => {
            await api.planner.edit(c.id, { checklist });
            return true;
          })) === true}
        />
      )}
      <Links card={c} byId={plan.byId} onOpen={openCard} readOnly={false} locked={locked} />
      {requests.length > 0 && (
        <ul aria-label="Requests" className="flex flex-col gap-2">
          {requests.map((r) => (
            <li key={r.id}>
              <RequestItem request={r} byId={plan.byId} showCard={false} />
            </li>
          ))}
        </ul>
      )}
      {!editing && (
        <div className="flex flex-wrap gap-1.5">
          {launch && (
            <Button size="sm" variant="primary" loading={busy === 'launch'} disabled={!!launch.reason || (!!busy && busy !== 'launch')} aria-describedby={launch.reason ? `plan-launch-${c.id}-reason` : undefined} onClick={launch.onClick}>
              {launch.icon}
              Launch
            </Button>
          )}
          {approve && (
            <Button size="sm" variant="primary" disabled={!!busy} onClick={approve.onClick}>
              <BadgeCheck />
              Approve…
            </Button>
          )}
          {!locked && (
            <Button size="sm" variant="secondary" disabled={!!busy} onClick={() => setEditing(true)}>
              <Pencil />
              Edit
            </Button>
          )}
          {!c.confirmed && (
            <Button size="sm" disabled={!!busy} loading={busy === 'dismiss'} onClick={() => void run('dismiss', 'discard the card', () => api.planner.dismiss(c.id))}>
              <X />
              Discard
            </Button>
          )}
        </div>
      )}
      {!editing && launch?.reason && <Note id={`plan-launch-${c.id}-reason`}>{launch.reason}</Note>}
      {cardActions.dialogs}
    </section>
  );
}

/* ---------- A Task with no card ---------- */

const NEW = 'new';

/**
 * Adds a Task with no card to a story (ADR 0005 §5): a new subtask there, named after the Task
 * unless renamed, or one of the story's subtasks not started yet. The Task then works on it, as a
 * launched Task does, so work starts only on confirmed cards: under a proposal it asks first,
 * naming what it confirms.
 */
function AttachBox({ plan, taskId, taskName, onAttached }: Readonly<{ plan: TaskPlan; taskId: string; taskName: string; onAttached: (id: string) => void }>) {
  const api = useApi();
  // Not under an approved epic: its approval owns starting the work there (ADR 0006 §6.3).
  const stories = plan.cards.filter((c) => c.kind === 'story' && c.status !== 'done' && !approvedEpicOf(c, plan.byId));
  const [story, setStory] = useState('');
  const [target, setTarget] = useState(NEW);
  const [title, setTitle] = useState(taskName);
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const chosen = plan.byId.get(story);
  const open = chosen ? (plan.index.get(chosen.id) ?? []).filter((c) => c.kind === 'subtask' && !c.held_by && (c.status === 'planned' || c.status === 'todo')) : [];
  const existing = target === NEW ? undefined : plan.byId.get(target);
  const confirms = chosen ? cardPath(existing ?? chosen, plan.byId).filter((c) => !c.confirmed).reverse() : [];
  const label = (c: Card) => `${cardPath(c, plan.byId).slice(0, -1).map((p) => p.title).concat(`#${c.seq} ${c.title}`).join(' › ')}${c.confirmed ? '' : ' (proposed)'}`;

  const attach = async (confirm: boolean) => {
    if (!chosen) return;
    setBusy(true);
    setError('');
    try {
      const held = await api.planner.attach(existing?.id ?? chosen.id, { task_id: taskId, title: existing ? undefined : title.trim(), confirm });
      setAsking(false);
      onAttached(held.id);
    } catch (e) {
      setAsking(false);
      setError(plannerErrorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section aria-label="Add this task to a story" className="flex flex-col gap-2 rounded-md bg-tint-well px-3 py-2.5 text-ui">
      <p className="text-body">This task is not part of a story yet. Add it to one, and the plan shows its progress.</p>
      {stories.length === 0 ? (
        <p className="text-caption text-muted">{plan.project.name} has no open stories. Add one in the Planner first.</p>
      ) : (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (!chosen || (!existing && !title.trim())) return;
            if (confirms.length) setAsking(true);
            else void attach(false);
          }}
        >
          <Select
            aria-label="Story"
            className={cn('h-8 w-full', !story && 'text-muted')}
            value={story}
            onValueChange={(v) => {
              setStory(v);
              setTarget(NEW);
            }}
            items={[{ value: '', label: 'Pick a story…', hidden: true }, ...stories.map((s) => ({ value: s.id, label: label(s) }))]}
          />
          {chosen && open.length > 0 && (
            <Select
              aria-label="Work on"
              className="h-8 w-full"
              value={target}
              onValueChange={setTarget}
              items={[{ value: NEW, label: 'A new subtask' }, ...open.map((c) => ({ value: c.id, label: `#${c.seq} ${c.title}${c.confirmed ? '' : ' (proposed)'}` }))]}
            />
          )}
          {chosen && !existing && <Input size="md" aria-label="New subtask title" placeholder="Subtask title" value={title} onChange={(e) => setTitle(e.target.value)} />}
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" size="sm" variant="primary" loading={busy && !asking} disabled={!chosen || (!existing && !title.trim()) || busy}>
              <Plus />
              Add to story
            </Button>
            {chosen && confirms.length > 0 && <span className="text-caption text-muted">Asks to confirm the proposals it works under.</span>}
          </div>
          {error && <p role="alert" className="text-caption text-error">{error}</p>}
        </form>
      )}
      <AlertDialog
        open={asking}
        onOpenChange={(o) => !o && setAsking(false)}
        danger={false}
        title="Confirm and add this task?"
        description="A task works only on confirmed cards. Adding it to this story confirms these proposals:"
        confirmLabel="Confirm and add"
        busy={busy}
        onConfirm={() => void attach(true)}
      >
        <ul aria-label="Adding confirms" className="mt-2 list-disc pl-4 text-ui text-body">
          {confirms.map((c) => (
            <li key={c.id}>
              #{c.seq} {c.title}
            </li>
          ))}
        </ul>
      </AlertDialog>
    </section>
  );
}
