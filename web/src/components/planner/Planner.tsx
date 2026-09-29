import { Ellipsis, Inbox, KanbanSquare, PictureInPicture2, Plus, Trash2, X } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api, describeError, errorCode, type BoardJob, type Project } from '../../api';
import { childIndex } from '../../lib/board';
import { cn } from '../../lib/cn';
import type { Action, BoardState } from '../../state';
import { Note, ProjectBadge, Skeleton, useApp } from '../common';
import { Button } from '../ui/button';
import { usePresence } from '../ui/collapse';
import { AlertDialog } from '../ui/dialog';
import { Menu } from '../ui/menu';
import { Segmented } from '../ui/segmented';
import { Select } from '../ui/select';
import { Switch } from '../ui/switch';
import { Tip } from '../ui/tooltip';
import { PanelHeader, SidePanel } from '../Subagents';
import { BoardView } from './BoardView';
import { CardPanel } from './CardPanel';
import { INITIAL_UI, PlannerContext, usePlanner, type PlannerContextValue, type PlannerNotice, type PlannerUi, type PlannerViewKind, type PopKind } from './context';
import { MapView } from './MapView';
import { PopOutHost, openPipWindow, popMode } from './PopOut';
import { InboxList } from './Requests';
import { TreeView } from './TreeView';

export { PlannerContext };

/** The Boards to follow: every git Project's, and the Unassigned list, while the planner is on. */
export function plannerKeys(enabled: boolean, projects: readonly Project[]): string[] {
  return enabled ? [...projects.filter((p) => !p.no_git).map((p) => p.id), 'unassigned'] : [];
}

/**
 * The planner's app-level state (ADR 0005 §10, §15): the shared view state, the pop-out, and the
 * Boards it follows. Each followed Board is fetched once, then kept by `board` frames; one that
 * went stale (a revision gap, a new stream) is fetched again. The Needs-you count reads them all.
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
  const [pop, setPop] = useState<{ kind: PopKind; win: Window | null } | null>(null);
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
      .catch((e: unknown) => dispatch({ type: 'board_failed', key, error: errorCode(e) === 'planner_off' ? 'The planner is off.' : describeError(e) }))
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

  // The open Picture-in-Picture window, so closing the pop-out can close it too.
  const pipWin = useRef<Window | null>(null);
  const closePopout = useCallback(() => {
    const win = pipWin.current;
    pipWin.current = null;
    if (win && !win.closed) win.close();
    setPop(null);
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

  const popOut = useCallback((kind: PopKind) => {
    const open = pipWin.current;
    if (open && !open.closed) {
      setPop({ kind, win: open });
      return;
    }
    if (popMode() === 'pip') {
      // Called from the click that asked: the window needs its user gesture.
      openPipWindow()
        .then((win) => {
          pipWin.current = win;
          setPop({ kind, win });
        })
        .catch(() => setPop({ kind, win: null }));
      return;
    }
    setPop({ kind, win: null });
  }, []);

  // Stable, so the memoised rows that take it keep their props.
  const openCard = useCallback((id: string) => {
    setUi({ selected: id, panel: 'card' });
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
    popout: pop?.kind ?? null,
    popOut,
    closePopout,
    notice,
    notify,
    enabled,
  }), [ui, setUi, boards, jobs, projects, openCard, onOpenTask, load, pop?.kind, popOut, closePopout, notice, enabled]);

  const host = pop && enabled ? <PopOutHost win={pop.win} kind={pop.kind} onKind={(kind) => setPop((p) => p && { ...p, kind })} onClose={closePopout} /> : null;
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
  const git = projects.filter((x) => !x.no_git);
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
  const epics = useMemo(() => (cards ? (childIndex(cards).get('') ?? []).filter((c) => c.kind === 'epic') : []), [cards]);
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
    body = <Empty>{project.name} is not a git repository, so it has no plan.</Empty>;
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
          <Select
            aria-label="Project"
            className="h-8 w-auto max-w-56 min-w-0 bg-transparent shadow-none hover:not-data-disabled:bg-tint-hover sm:ml-1"
            value={key}
            // Base UI reports null when the chosen option leaves the list (the Unassigned entry, once its last card moves): not a pick.
            onValueChange={(v) => v && setUi({ project: v, selected: null, epic: null, panel: null, creating: null })}
            items={[...git.map((x) => ({ value: x.id, label: x.name })), ...(unassigned || key === 'unassigned' ? [{ value: 'unassigned', label: `Unassigned (${unassigned})` }] : [])]}
          />
          {project && <ProjectBadge badge={project.badge} className="-ml-0.5 max-sm:hidden" />}
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
              className="h-7 w-auto max-w-64 min-w-0 text-caption"
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
        {p.notice && (
          <p role={p.notice.tone === 'error' ? 'alert' : 'status'} className={cn('mx-3 mb-1 flex items-center gap-2 rounded-sm px-3 py-1.5 text-caption animate-fade-in', p.notice.tone === 'error' ? 'bg-error-wash text-error' : 'bg-surface text-body')}>
            <span className="min-w-0 flex-1">{p.notice.text}</span>
            {p.notice.task && (
              <Button size="sm" variant="secondary" onClick={() => p.openTask(p.notice!.task!)}>
                Open task
              </Button>
            )}
            <Button size="icon-sm" aria-label="Dismiss" className="text-current" onClick={() => p.notify(null)}>
              <X />
            </Button>
          </p>
        )}
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
            await api.planner.purge(key);
          } catch (e) {
            p.notify({ tone: 'error', text: `Could not purge: ${describeError(e)}` });
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
