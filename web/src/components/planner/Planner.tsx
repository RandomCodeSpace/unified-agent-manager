import { Ellipsis, Inbox, KanbanSquare, Plus, Trash2, X } from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api, plannerErrorText, type BoardJob, type Project } from '../../api';
import { boardOf, childIndex, epicOf } from '../../lib/board';
import { cn } from '../../lib/cn';
import type { Action, BoardState } from '../../state';
import { popupOpen } from '../../App';
import { Note, Skeleton, useApp } from '../common';
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
import { INITIAL_UI, PlannerContext, usePlanner, type PlannerContextValue, type PlannerNotice, type PlannerUi, type PlannerViewKind } from './context';
import { MapView } from './MapView';
import { NoticeBar } from './parts';
import { InboxList } from './Requests';
import { TreeView } from './TreeView';

export { PlannerContext };

/** The Boards to follow: every git Project's, and the Unassigned list, while the planner is on. */
export function plannerKeys(enabled: boolean, projects: readonly Project[]): string[] {
  return enabled ? [...projects.filter((p) => !p.no_git).map((p) => p.id), 'unassigned'] : [];
}

/**
 * The planner's app-level state (ADR 0005 §10, §15): the Planner view's state and the Boards it
 * follows. Each followed Board is fetched once, then kept by `board` frames; one that went stale
 * (a revision gap, a new stream) is fetched again. The Needs-you count reads them all, and a
 * Task's story strip and Plan panel read its Project's.
 */
export function usePlannerController({ enabled, boards, jobs, projects, dispatch, onShowPlanner, onOpenTask, initialProject }: Readonly<{
  enabled: boolean;
  boards: Record<string, BoardState>;
  jobs: Record<string, BoardJob>;
  projects: Project[];
  dispatch: (a: Action) => void;
  onShowPlanner: () => void;
  onOpenTask: (id: string) => void;
  initialProject: string | null;
}>) {
  const [ui, setUiState] = useState<PlannerUi>(() => ({ ...INITIAL_UI, project: initialProject }));
  const [notice, notify] = useState<PlannerNotice | null>(null);
  const inflight = useRef(new Set<string>());
  const setUi = useCallback((patch: Partial<PlannerUi> | ((u: PlannerUi) => Partial<PlannerUi>)) => setUiState((u) => ({ ...u, ...(typeof patch === 'function' ? patch(u) : patch) })), []);

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

  // An earlier version stored which Tasks hid the pop-out; nothing reads it now.
  useEffect(() => {
    try {
      localStorage.removeItem('uam.plannerHidden');
    } catch {
      // Storage unavailable: nothing to remove.
    }
  }, []);

  const keys = plannerKeys(enabled, projects).join('\n');
  useEffect(() => {
    const wanted = keys ? keys.split('\n') : [];
    for (const key of wanted) {
      const b = boards[key];
      if (!b || (b.stale && !b.loading)) load(key);
    }
    for (const key of Object.keys(boards)) if (!wanted.includes(key)) dispatch({ type: 'board_dropped', key });
  }, [keys, boards, load, dispatch]);

  // The Boards as of the last render, for openCard: it stays stable, so the memoised rows that take it keep their props.
  const boardsNow = useRef(boards);
  useLayoutEffect(() => {
    boardsNow.current = boards;
  });
  /**
   * Shows a card in the Planner view, switching to its Board when another is shown. The Planner's
   * own filters never hide the card it selects: an epic filter it is not under clears, and a
   * cancelled card shows cancelled ones.
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
    notice,
    notify,
    enabled,
  }), [ui, setUi, boards, jobs, projects, openCard, onOpenTask, load, notice, enabled]);

  return { value };
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
  // An empty plan has one way in, its own New epic, and nothing for the views to show.
  const empty = author && !board?.data?.cards.length;
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
  } else if (empty && !ui.creating) {
    body = (
      <div className="flex flex-col items-center gap-3 px-4 py-10 text-center">
        <p className="text-ui text-muted">Nothing is planned for {project?.name ?? 'this project'} yet.</p>
        <Button size="md" variant="primary" data-add-root="epic" onClick={() => create('epic')}>
          <Plus />
          New epic
        </Button>
      </div>
    );
  } else if (ui.view === 'tree') {
    body = <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain"><TreeView readOnly={key === 'unassigned'} /></div>;
  } else if (ui.view === 'board') {
    body = <BoardView />;
  } else {
    body = <MapView />;
  }

  const menuItems = [
    { key: 'purge', label: `Purge cancelled${cancelled ? ` (${cancelled})` : ''}`, icon: <Trash2 />, danger: true, disabled: !cancelled || key === 'unassigned', reason: !cancelled ? 'No cancelled cards on this board.' : undefined, onSelect: () => setPurging(true) },
  ];

  // The header spans the side panels, so Close planner keeps its place while one is open.
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col animate-rise">
      <header className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3">
        {leading}
        <KanbanSquare aria-hidden="true" className="size-4 shrink-0 text-muted max-sm:hidden" />
        <h1 className="shrink-0 text-display-sm text-ink">Planner</h1>
        <PlannerProjectPicker projects={projects} value={key} unassigned={unassigned} onPick={(v) => setUi({ project: v, selected: null, epic: null, panel: null, creating: null })} />
        <span className="flex-1" />
        {author && !empty && (
          <Tip label="New epic">
            <Button size="md" aria-label="New epic" data-add-root="epic" className="px-2 text-muted" onClick={() => create('epic')}>
              <Plus />
              <span className="max-sm:hidden">New epic</span>
            </Button>
          </Tip>
        )}
        {!narrow && !empty && <Segmented size="sm" aria-label="View" value={ui.view} onValueChange={(v) => setUi({ view: v as PlannerViewKind })} items={VIEW_ITEMS} />}
        <Tip label="Inbox">
          <Button id="planner-inbox" size="md" aria-pressed={ui.panel === 'inbox'} aria-label={`Inbox, ${pending} pending`} className="px-2 text-muted" onClick={() => setUi({ panel: ui.panel === 'inbox' ? null : 'inbox' })}>
            <Inbox />
            <span className="max-sm:hidden">Inbox</span>
            {pending > 0 && <span className="rounded-xs bg-attention-wash px-1 text-caption tabular-nums text-attention">{pending}</span>}
          </Button>
        </Tip>
        {/* Only while it holds something to do: a menu of one disabled item says nothing. */}
        {menuItems.some((item) => !item.disabled) && (
          <Menu.Root modal={false}>
            <Menu.Trigger render={<Button size="icon-md" aria-label="Planner actions" className="text-muted" />}>
              <Ellipsis />
            </Menu.Trigger>
            <Menu.Content align="end">
              <Menu.Actions items={menuItems} />
            </Menu.Content>
          </Menu.Root>
        )}
        <Tip label="Close planner">
          <Button size="icon-md" aria-label="Close planner" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        </Tip>
      </header>
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex min-h-9 shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 px-3 pb-1.5">
            {narrow && !empty && <Segmented size="sm" aria-label="View" className="w-full" value={ui.view} onValueChange={(v) => setUi({ view: v as PlannerViewKind })} items={VIEW_ITEMS} />}
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
      </div>
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
  // Inline, the panel is no dialog: Esc closes it and returns to the Inbox button, unless a popup or a field owns the key.
  useEffect(() => {
    if (!inline || !open) return;
    const escape = (event: KeyboardEvent) => {
      const editing = (event.target as Element | null)?.closest?.('input, textarea, select, [contenteditable="true"]');
      if (event.key !== 'Escape' || event.defaultPrevented || editing || popupOpen()) return;
      onClose();
      document.getElementById('planner-inbox')?.focus();
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, open, onClose]);
  return (
    // Nothing pending needs no more than the narrowest panel.
    <SidePanel id="planner-inbox" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Inbox" className={inline && !pending ? 'w-80' : undefined}>
      <PanelHeader>
        <Inbox aria-hidden="true" className="size-4 text-muted" />
        <span className="text-title text-ink">Inbox</span>
        <span className="text-caption tabular-nums text-muted">{pending} pending</span>
        <span className="flex-1" />
        <Button size="icon-md" aria-label="Close inbox" className="text-muted" onClick={onClose}>
          <X />
        </Button>
      </PanelHeader>
      <div className={cn('min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain px-3 pb-4', narrow && 'pt-1')}>
        <InboxList />
      </div>
    </SidePanel>
  );
}
