import { Ellipsis, Inbox, KanbanSquare, PictureInPicture2, Plus, Trash2, X } from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api, plannerErrorText, type BoardJob, type Project } from '../../api';
import { boardOf, childIndex, epicOf } from '../../lib/board';
import { cn } from '../../lib/cn';
import type { Action, BoardState } from '../../state';
import { Note, Skeleton, useApp, useMedia } from '../common';
import { Button } from '../ui/button';
import { usePresence } from '../ui/collapse';
import { AlertDialog } from '../ui/dialog';
import { Menu } from '../ui/menu';
import { Segmented } from '../ui/segmented';
import { Select } from '../ui/select';
import { Switch } from '../ui/switch';
import { Tip } from '../ui/tooltip';
import { PlannerProjectPicker } from '../ProjectPicker';
import { PanelHeader, SidePanel } from '../Subagents';
import { BoardView } from './BoardView';
import { CardPanel } from './CardPanel';
import { INITIAL_UI, PlannerContext, usePlanner, type PlannerContextValue, type PlannerNotice, type PlannerUi, type PlannerViewKind, type PopKind } from './context';
import { MapView } from './MapView';
import { NoticeBar } from './parts';
import { PHONE, PopOutHost, openPipWindow, popMode } from './PopOut';
import { InboxList } from './Requests';
import { TreeView } from './TreeView';

export { PlannerContext };

/** The Boards to follow: every git Project's, and the Unassigned list, while the planner is on. */
export function plannerKeys(enabled: boolean, projects: readonly Project[]): string[] {
  return enabled ? [...projects.filter((p) => !p.no_git).map((p) => p.id), 'unassigned'] : [];
}

const HIDDEN_KEY = 'uam.plannerHidden';

/** The owner's last Hide (true) or Show (false) of a Task's panel, per Project; anything else stored counts as none. */
function readHidden(): Record<string, boolean> {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(HIDDEN_KEY) ?? '{}');
    if (v && typeof v === 'object' && !Array.isArray(v) && Object.values(v).every((h) => typeof h === 'boolean')) return v as Record<string, boolean>;
  } catch {
    // Unreadable: as if nothing were stored.
  }
  return {};
}

/**
 * The planner's app-level state (ADR 0005 §10, §15): the shared view state, the pop-out, and the
 * Boards it follows. Each followed Board is fetched once, then kept by `board` frames; one that
 * went stale (a revision gap, a new stream) is fetched again. The Needs-you count reads them all.
 */
export function usePlannerController({ enabled, boards, jobs, projects, dispatch, onShowPlanner, onOpenTask, initialProject, taskId, taskProject }: Readonly<{
  enabled: boolean;
  boards: Record<string, BoardState>;
  jobs: Record<string, BoardJob>;
  projects: Project[];
  dispatch: (a: Action) => void;
  onShowPlanner: () => void;
  onOpenTask: (id: string) => void;
  initialProject: string | null;
  /** The Task on screen and its Project, when it is a git Project's (not over Settings, the Planner or a new Task). */
  taskId: string | null;
  taskProject: string | null;
}>) {
  const [ui, setUiState] = useState<PlannerUi>(() => ({ ...INITIAL_UI, project: initialProject }));
  const [notice, notify] = useState<PlannerNotice | null>(null);
  /*
   * The pop-out, in two ideas (ADR 0005 §10, as the owner reworked it):
   * - `pop`, the explicit pop-out: the owner popped a view out (the Planner's buttons). It renders
   *   the Planner's own view state (`ui`), so selection and filters carry between them, and stays
   *   up across navigation until closed. `folded` is its Hide, for as long as it is up; `win` is
   *   its separate window when the owner moved it into one.
   * - otherwise the Task's panel, while a git Project's Task is open: that Project's Board, in a
   *   view state of its own (`taskUi`), so following Tasks never moves the Planner's Board,
   *   selection or filters (invariant 21). Never over the Planner view. `visit` is its fold,
   *   decided once per Task opened: the owner's last Hide or Show for the Project (`hidden`,
   *   persisted; only those two buttons write it), else expanded only over a Board with a live
   *   card; on a phone always the tab. So it never covers the conversation unasked, not even
   *   when the Board fills later.
   */
  const [kind, setKind] = useState<PopKind>('tree');
  const [pop, setPop] = useState<{ win: Window | null; folded: boolean } | null>(null);
  const [taskUi, setTaskUiState] = useState<PlannerUi>(INITIAL_UI);
  const [visit, setVisit] = useState<{ task: string; folded: boolean } | null>(null);
  const [hidden, setHidden] = useState(readHidden);
  const phone = useMedia(PHONE);
  const inflight = useRef(new Set<string>());
  const setUi = useCallback((patch: Partial<PlannerUi> | ((u: PlannerUi) => Partial<PlannerUi>)) => setUiState((u) => ({ ...u, ...(typeof patch === 'function' ? patch(u) : patch) })), []);
  const setTaskUi = useCallback((patch: Partial<PlannerUi> | ((u: PlannerUi) => Partial<PlannerUi>)) => setTaskUiState((u) => ({ ...u, ...(typeof patch === 'function' ? patch(u) : patch) })), []);

  const load = useCallback((key: string) => {
    if (inflight.current.has(key)) return;
    inflight.current.add(key);
    dispatch({ type: 'board_loading', key });
    api.planner
      .board(key)
      .then((data) => dispatch({ type: 'board_loaded', key, data }))
      .catch((e: unknown) => dispatch({ type: 'board_failed', key, error: plannerErrorText(e) }))
      .finally(() => inflight.current.delete(key));
  }, [dispatch]);

  const keys = plannerKeys(enabled, projects).join('\n');
  useEffect(() => {
    const wanted = keys ? keys.split('\n') : [];
    for (const key of wanted) {
      const b = boards[key];
      if (!b || (b.stale && !b.loading)) load(key);
    }
    for (const key of Object.keys(boards)) if (!wanted.includes(key)) dispatch({ type: 'board_dropped', key });
  }, [keys, boards, load, dispatch]);

  // The Task's panel: its Board once loaded (or failed), folded as this visit decided.
  const popped = pop !== null;
  const auto = enabled && !popped && taskId !== null && taskProject !== null;
  const taskBoard = taskProject ? boards[taskProject] : undefined;
  if (!auto && visit) setVisit(null);
  if (auto && visit?.task !== taskId && (taskBoard?.data || taskBoard?.error)) {
    const live = !!taskBoard.data?.cards.some((c) => c.status !== 'cancelled');
    setVisit({ task: taskId, folded: phone || (hidden[taskProject] ?? !live) });
  }
  if (auto && taskUi.project !== taskProject) setTaskUiState((u) => ({ ...u, project: taskProject, selected: null, epic: null, panel: null, creating: null }));
  // As of the last render, for the stable callbacks below.
  const now = useRef({ taskId, taskProject, popped });
  useLayoutEffect(() => {
    now.current = { taskId, taskProject, popped };
  });

  const foldPop = useCallback((folded: boolean) => setPop((p) => p && { ...p, folded }), []);
  /** The owner's Hide (true) or Show (false) of the Task's panel: for this visit, and remembered for the Project. */
  const foldTask = useCallback((folded: boolean) => {
    const project = now.current.taskProject;
    setVisit((v) => v && { ...v, folded });
    if (!project) return;
    setHidden((h) => {
      const next = { ...h, [project]: folded };
      try {
        localStorage.setItem(HIDDEN_KEY, JSON.stringify(next));
      } catch {
        // Storage full or off: the choice lasts this visit.
      }
      return next;
    });
  }, []);

  // The open Picture-in-Picture window, so closing the pop-out can close it too.
  const pipWin = useRef<Window | null>(null);
  const closePopout = useCallback(() => {
    const win = pipWin.current;
    pipWin.current = null;
    if (win && !win.closed) win.close();
    setPop(null);
    // Over a Task, its panel takes the pop-out's place as the tab for this visit, so Close opens no other panel.
    const { taskId: task, taskProject: project } = now.current;
    setVisit(task && project ? { task, folded: true } : null);
  }, []);
  // The planner turned off: nothing to pop out.
  const [wasEnabled, setWasEnabled] = useState(enabled);
  if (enabled !== wasEnabled) {
    setWasEnabled(enabled);
    if (!enabled) setPop(null);
  }
  useEffect(() => {
    if (enabled || !pipWin.current) return;
    pipWin.current.close();
    pipWin.current = null;
  }, [enabled]);

  /** The explicit pop-out: the floating panel, expanded (or the separate window, while one is open). */
  const popOut = useCallback((k: PopKind) => {
    setKind(k);
    const open = pipWin.current;
    setPop({ win: open && !open.closed ? open : null, folded: false });
  }, []);
  /**
   * Moves the pop-out into a separate window; called from the click that asked, for the window's
   * user gesture. The window is the explicit pop-out, on the Planner's view state: from the Task's
   * panel, the Planner switches to that Board first, as a pick of it in the Planner would.
   */
  const toWindow = useCallback(() => {
    const board = now.current.popped ? null : now.current.taskProject;
    openPipWindow()
      .then((win) => {
        pipWin.current = win;
        if (board) setUi((u) => (u.project === board ? {} : { project: board, selected: null, epic: null, panel: null, creating: null }));
        setPop({ win, folded: false });
      })
      .catch(() => {});
  }, [setUi]);

  // The Boards as of the last render, for openCard: it stays stable, so the memoised rows that take it keep their props.
  const boardsNow = useRef(boards);
  useLayoutEffect(() => {
    boardsNow.current = boards;
  });
  /**
   * Shows a card, switching to its Board when another is shown: callers outside the planner (a
   * transcript's card chip, the Task's panel) name only the card. The Planner's own filters never
   * hide the card it selects: an epic filter it is not under clears, and a cancelled card shows cancelled ones.
   */
  const openCard = useCallback((id: string) => {
    const home = boardOf(boardsNow.current, id);
    const cards = home ? (boardsNow.current[home].data?.cards ?? []) : [];
    const card = cards.find((c) => c.id === id);
    setUi((u) => {
      const moved = !!home && home !== u.project;
      const outsideEpic = !!u.epic && !!card && epicOf(card, new Map(cards.map((c) => [c.id, c])))?.id !== u.epic;
      return {
        ...(moved ? { project: home, creating: null } : {}),
        epic: moved || outsideEpic ? null : u.epic,
        ...(card?.status === 'cancelled' ? { showCancelled: true } : {}),
        selected: id,
        panel: 'card',
      };
    });
    onShowPlanner();
  }, [setUi, onShowPlanner]);

  const value: PlannerContextValue = useMemo(() => ({
    ui,
    setUi,
    boards,
    jobs,
    projects,
    openCard,
    openTask: onOpenTask,
    reload: load,
    popout: popped ? kind : null,
    popOut,
    closePopout,
    notice,
    notify,
    enabled,
  }), [ui, setUi, boards, jobs, projects, openCard, onOpenTask, load, popped, kind, popOut, closePopout, notice, enabled]);

  const taskValue: PlannerContextValue = useMemo(() => ({ ...value, ui: taskUi, setUi: setTaskUi }), [value, taskUi, setTaskUi]);

  // Every prop is stable across renders that change nothing here (a streamed reply's deltas), so the memoised host skips them.
  const onWindow = popMode() === 'pip' ? toWindow : undefined;
  let host: ReactNode = null;
  if (enabled && pop) host = <PopOutHost win={pop.win} kind={kind} onKind={setKind} folded={pop.folded} onFold={foldPop} onClose={closePopout} onWindow={onWindow} />;
  else if (auto && visit?.task === taskId) {
    host = (
      <PlannerContext.Provider value={taskValue}>
        <PopOutHost win={null} kind={kind} onKind={setKind} folded={visit.folded} onFold={foldTask} onWindow={onWindow} />
      </PlannerContext.Provider>
    );
  }
  return { value, host };
}

const VIEW_ITEMS = [
  { value: 'tree', label: 'Tree' },
  { value: 'board', label: 'Board' },
  { value: 'map', label: 'Map' },
];

/**
 * The Planner view (ADR 0005 §10): one Project's Board in the main pane, as Tree, Board or Map,
 * switched in place with the selection and filters kept; the Inbox and the card detail open as
 * side panels (inline from 1280px, an overlay below, a sheet on a phone).
 */
export function PlannerView({ leading, inline, onClose, defaultProject }: Readonly<{ leading?: ReactNode; inline: boolean; onClose: () => void; defaultProject: string | null }>) {
  const p = usePlanner();
  const { narrow } = useApp();
  const { ui, setUi, projects, boards } = p;
  const unassigned = boards.unassigned?.data?.cards.length ?? 0;
  const project = projects.find((x) => x.id === ui.project);
  const key = ui.project ?? '';
  const board = key ? boards[key] : undefined;
  const [purging, setPurging] = useState(false);
  const [purgeBusy, setPurgeBusy] = useState(false);

  // A Board to show: the one asked for, else the default (the filtered or most recent git Project).
  useEffect(() => {
    if (ui.project && (ui.project === 'unassigned' || projects.some((x) => x.id === ui.project))) return;
    if (defaultProject) setUi({ project: defaultProject, selected: null, epic: null, panel: null, creating: null });
  }, [ui.project, projects, defaultProject, setUi]);

  const cards = board?.data?.cards;
  // Cancelled epics (expired proposals among them) leave the filter, except the one it is set to.
  const epics = useMemo(() => (cards ? (childIndex(cards).get('') ?? []).filter((c) => c.kind === 'epic' && (c.status !== 'cancelled' || c.id === ui.epic)) : []), [cards, ui.epic]);
  const stale = cards?.filter((c) => c.stale).length ?? 0;
  const cancelled = cards?.filter((c) => c.status === 'cancelled').length ?? 0;
  const pending = board?.data?.requests.length ?? 0;
  const panelPresence = usePresence(!!ui.panel);
  const [lastPanel, setLastPanel] = useState(ui.panel);
  if (ui.panel && ui.panel !== lastPanel) setLastPanel(ui.panel);
  const panel = ui.panel ?? lastPanel;
  const closePanel = () => setUi({ panel: null });
  // Owner authoring (§3) happens in the Tree: a new card's form opens there, under its parent.
  const author = !!key && key !== 'unassigned' && !project?.no_git && !!board?.data;
  const create = (kind: 'epic' | 'subtask') => setUi({ view: 'tree', creating: { parent: '', kind } });

  let body: ReactNode;
  if (project?.no_git) {
    // Planner writes and launches need Git (§14 no_git): the reason, and no authoring.
    body = <Empty>{project.no_git === 'not_installed' ? `Git is not installed where uam can find it, so ${project.name} has no plan.` : `${project.name} is not a git repository, so it has no plan.`}</Empty>;
  } else if (!key) {
    body = <Empty>Add a git project to plan its work.</Empty>;
  } else if (!board?.data && board?.error) {
    body = (
      <Note tone="error" role="alert" className="mx-4 my-4 flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1">Could not load the plan: {board.error}</span>
        <Button size="sm" variant="secondary" onClick={() => p.reload(key)}>Retry</Button>
      </Note>
    );
  } else if (!board?.data) {
    body = <Skeleton label="Loading the plan…" rows={8} className="px-4 py-4" rowClassName="h-6" />;
  } else if (author && !board.data.cards.length && !ui.creating) {
    body = (
      <div className="flex flex-col items-center gap-3 px-4 py-10 text-center">
        <p className="text-ui text-muted">Nothing is planned for {project?.name ?? 'this project'} yet.</p>
        <div className="flex flex-wrap justify-center gap-2">
          <Button size="md" variant="primary" onClick={() => create('epic')}>
            <Plus />
            New epic
          </Button>
          <Button size="md" variant="secondary" onClick={() => create('subtask')}>
            <Plus />
            Add subtask
          </Button>
        </div>
      </div>
    );
  } else if (ui.view === 'tree') {
    body = <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain"><TreeView readOnly={key === 'unassigned'} /></div>;
  } else if (ui.view === 'board') {
    body = <BoardView />;
  } else {
    body = <MapView />;
  }

  const menuItems = [
    { key: 'purge', label: `Purge cancelled${cancelled ? ` (${cancelled})` : ''}`, icon: <Trash2 />, danger: true, disabled: !cancelled || key === 'unassigned', reason: !cancelled ? 'No cancelled cards on this board.' : undefined, onSelect: () => setPurging(true) },
  ];

  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col animate-rise">
        <header className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3">
          {leading}
          <KanbanSquare aria-hidden="true" className="size-4 shrink-0 text-muted max-sm:hidden" />
          <h1 className="shrink-0 text-display-sm text-ink max-sm:sr-only">Planner</h1>
          <PlannerProjectPicker projects={projects} value={key} unassigned={unassigned} onPick={(v) => setUi({ project: v, selected: null, epic: null, panel: null, creating: null })} />
          <span className="flex-1" />
          {author && (
            <Tip label="New epic">
              <Button size="md" aria-label="New epic" className="px-2 text-muted" onClick={() => create('epic')}>
                <Plus />
                <span className="max-sm:hidden">New epic</span>
              </Button>
            </Tip>
          )}
          {!narrow && <Segmented size="sm" aria-label="View" value={ui.view} onValueChange={(v) => setUi({ view: v as PlannerViewKind })} items={VIEW_ITEMS} />}
          <Tip label="Inbox">
            <Button id="planner-inbox" size="md" aria-pressed={ui.panel === 'inbox'} aria-label={`Inbox, ${pending} pending`} className="px-2 text-muted" onClick={() => setUi({ panel: ui.panel === 'inbox' ? null : 'inbox' })}>
              <Inbox />
              <span className="max-sm:hidden">Inbox</span>
              {pending > 0 && <span className="rounded-xs bg-attention-wash px-1 text-caption tabular-nums text-attention">{pending}</span>}
            </Button>
          </Tip>
          <Tip label={`Pop out the ${ui.view}`}>
            <Button size="icon-md" aria-label={`Pop out the ${ui.view}`} className="text-muted" disabled={!board?.data} onClick={() => p.popOut(ui.view)}>
              <PictureInPicture2 />
            </Button>
          </Tip>
          <Menu.Root modal={false}>
            <Menu.Trigger render={<Button size="icon-md" aria-label="Planner actions" className="text-muted" />}>
              <Ellipsis />
            </Menu.Trigger>
            <Menu.Content align="end">
              <Menu.Actions items={menuItems} />
            </Menu.Content>
          </Menu.Root>
          <Tip label="Close planner">
            <Button size="icon-md" aria-label="Close planner" className="text-muted" onClick={onClose}>
              <X />
            </Button>
          </Tip>
        </header>
        <div className="flex min-h-9 shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 px-3 pb-1.5">
          {narrow && <Segmented size="sm" aria-label="View" className="w-full" value={ui.view} onValueChange={(v) => setUi({ view: v as PlannerViewKind })} items={VIEW_ITEMS} />}
          {epics.length > 0 && (
            <Select
              aria-label="Epic"
              // A quiet filter like Show cancelled beside it: caption text, no well, the tint on hover.
              className="h-7 w-auto max-w-64 min-w-0 gap-1 bg-transparent px-1.5 text-caption text-body shadow-none"
              value={ui.epic ?? ''}
              onValueChange={(v) => setUi({ epic: v || null })}
              items={[{ value: '', label: 'All epics' }, ...epics.map((e) => ({ value: e.id, label: `#${e.seq} ${e.title}` }))]}
            />
          )}
          <span className="flex items-center gap-2 text-caption text-muted">
            <Switch aria-label="Show cancelled" checked={ui.showCancelled} onCheckedChange={(v) => setUi({ showCancelled: v })} />
            <span aria-hidden="true">Show cancelled</span>
          </span>
          {stale > 0 && <span className="text-caption text-warning">{stale} stale</span>}
          {board?.loading && board.data && <span className="text-caption text-muted">Refreshing…</span>}
        </div>
        <NoticeBar className="mx-3 mb-1" />
        <div className={cn('flex min-h-0 flex-1 flex-col', ui.view === 'map' && 'relative')} aria-busy={!board?.data || undefined}>
          {body}
        </div>
      </div>
      {panelPresence.mounted && panel === 'card' && <CardPanel inline={inline} open={ui.panel === 'card'} onClose={closePanel} onClosed={panelPresence.onClosed} />}
      {panelPresence.mounted && panel === 'inbox' && <InboxPanel inline={inline} open={ui.panel === 'inbox'} onClose={closePanel} onClosed={panelPresence.onClosed} />}
      <AlertDialog
        open={purging}
        onOpenChange={(o) => !o && setPurging(false)}
        title={`Purge ${cancelled} cancelled ${cancelled === 1 ? 'card' : 'cards'}?`}
        description="They are deleted for good, with their comments and requests. Restore cannot bring them back."
        confirmLabel="Purge"
        busy={purgeBusy}
        onConfirm={async () => {
          setPurgeBusy(true);
          try {
            // Only subtrees that are cancelled all the way down go; a cancelled card with a live one under it stays.
            const { purged } = await api.planner.purge(key);
            p.notify({ tone: 'muted', text: `Purged ${purged} ${purged === 1 ? 'card' : 'cards'}.${purged < cancelled ? ' Cancelled cards with open work under them stay.' : ''}` });
          } catch (e) {
            p.notify({ tone: 'error', text: `Could not purge: ${plannerErrorText(e)}` });
          } finally {
            setPurgeBusy(false);
            setPurging(false);
          }
        }}
      />
    </div>
  );
}

function Empty({ children }: Readonly<{ children: ReactNode }>) {
  return <p className="px-4 py-8 text-center text-ui text-muted">{children}</p>;
}

function InboxPanel({ inline, open, onClose, onClosed }: Readonly<{ inline: boolean; open: boolean; onClose: () => void; onClosed: () => void }>) {
  const p = usePlanner();
  const { narrow } = useApp();
  const pending = p.ui.project ? (p.boards[p.ui.project]?.data?.requests.length ?? 0) : 0;
  return (
    <SidePanel id="planner-inbox" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Inbox">
      <PanelHeader>
        <Inbox aria-hidden="true" className="size-4 text-muted" />
        <span className="text-title text-ink">Inbox</span>
        <span className="text-caption tabular-nums text-muted">{pending} pending</span>
        <span className="flex-1" />
        <Button size="icon-md" aria-label="Pop out the inbox" className="text-muted" onClick={() => p.popOut('inbox')}>
          <PictureInPicture2 />
        </Button>
        <Button size="icon-md" aria-label="Close inbox" className="text-muted" onClick={onClose}>
          <X />
        </Button>
      </PanelHeader>
      <div className={cn('min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 pb-4', narrow && 'pt-1')}>
        <InboxList />
      </div>
    </SidePanel>
  );
}
