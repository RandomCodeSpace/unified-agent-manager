import { ChevronRight, CircleCheck, Clock, FolderPlus, KanbanSquare, LogOut, Settings as SettingsIcon, Search, SquarePen } from 'lucide-react';
import { ViewTransition, memo, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { readOnly, taskName, type Project, type SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { GROUP_TITLES, commandGroups, filteredProject, groupTasks, needsYouNow, sidebarTasks, taskStatus, type GroupKey } from '../lib/tasks';
import type { Connection } from '../state';
import { Dot, InlineName, ProjectBadge, Skeleton, TONE_TEXT, TaskTitle, relTime, useApp, useMinuteTick } from './common';
import { Key } from './InlinePicker';
import { ProjectFilterPicker } from './ProjectPicker';
import { canRename, taskMenuItems, useTaskActions } from './taskActions';
import { Button } from './ui/button';
import { Collapse } from './ui/collapse';
import { ContextMenu } from './ui/menu';
import { Tip } from './ui/tooltip';
import { UsageButton } from './Usage';

/** Project-level navigation and actions. */
export interface WorkspaceActions {
  /** Opens the New task palette (or the draft at once when there is one Project). */
  onNewTask: () => void;
  onAddProject: () => void;
  /** Edit project: the one place for a Project's name, defaults, previous sessions and removal. */
  onEditProject: (p: Project) => void;
  /** A Project's routines, in the main pane. */
  onRoutines: (p: Project) => void;
  /** Whether Routines is showing; the sidebar's Routines button opens every Project's, or closes the view. */
  routinesOpen: boolean;
  onAllRoutines: () => void;
  /** The Project the sidebar is filtered to; null shows every Project. Remembered per browser. */
  filter: string | null;
  onFilter: (id: string | null) => void;
  /** Whether the sidebar (or, on a narrow screen, the drawer) is showing. */
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
  settingsOpen: boolean;
  onSettings: (target?: 'token-prices') => void;
  /** The planner, while Settings → Planner is on: whether its view is showing, and Plan (a Project's Board, or the last one shown). */
  planner?: { open: boolean; onOpen: (projectId?: string) => void };
}

/**
 * The sidebar toggle (Ctrl/Cmd+B). In the sidebar header it hides the sidebar; at the start
 * of the main pane's header, once hidden, it brings it back. On a narrow screen it opens
 * and closes the drawer instead.
 */
export function SidebarToggle({ id, open, onToggle, size = 'icon', wordmark = false, count = 0, side, className }: Readonly<{ id?: string; open: boolean; onToggle: () => void; size?: 'icon' | 'icon-md'; wordmark?: boolean; /** Tasks that need you, shown on the button while the list is out of sight. */ count?: number; side?: TipSide; className?: string }>) {
  const label = (open ? 'Hide sidebar' : 'Show sidebar') + (count > 0 ? `, ${count} need you` : '');
  return (
    <Tip
      side={side}
      label={
        <>
          {label}
          <span className="block text-on-primary/70">Ctrl+B</span>
        </>
      }
    >
      <Button id={id} size={wordmark ? 'md' : size} aria-label={label} aria-expanded={open} aria-keyshortcuts="Control+B Meta+B" className={cn('[&_svg]:size-4', wordmark && 'px-2', className)} onClick={onToggle}>
        <Brand markOnly={!wordmark} />
        {count > 0 && (
          <span aria-hidden="true" className="absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-attention px-1 text-meta font-semibold text-on-primary tabular-nums pointer-coarse:top-0.5 pointer-coarse:right-0.5">
            {count}
          </span>
        )}
      </Button>
    </Tip>
  );
}

type TipSide = 'top' | 'right';

/* ---------- Header and footer controls (the sidebar's, and the collapsed rail's) ---------- */

function NewTaskButton({ actions, id, side }: Readonly<{ actions: WorkspaceActions; id?: string; side?: TipSide }>) {
  return (
    <Tip
      side={side}
      label={
        <>
          New task<span className="block text-on-primary/70">Alt+N</span>
        </>
      }
    >
      <Button id={id} size="icon" aria-label="New task" aria-keyshortcuts="Alt+N" className="text-muted" onClick={actions.onNewTask}>
        <SquarePen />
      </Button>
    </Tip>
  );
}

function AddProjectButton({ actions, side }: Readonly<{ actions: WorkspaceActions; side?: TipSide }>) {
  return (
    <Tip label="Add project" side={side}>
      <Button size="icon" aria-label="Add project" className="text-muted" onClick={actions.onAddProject}>
        <FolderPlus />
      </Button>
    </Tip>
  );
}

function FilterButton({ projects, actions, side }: Readonly<{ projects: Project[]; actions: WorkspaceActions; side?: TipSide }>) {
  return <ProjectFilterPicker projects={projects} filter={actions.filter} onFilter={actions.onFilter} onEdit={actions.onEditProject} onRoutines={actions.onRoutines} onPlan={actions.planner && ((p) => actions.planner!.onOpen(p.id))} side={side === 'right' ? 'right' : undefined} />;
}

function RoutinesButton({ actions, side }: Readonly<{ actions: WorkspaceActions; side?: TipSide }>) {
  return (
    <Tip label="Routines" side={side}>
      <Button size="icon" aria-label="Routines" aria-pressed={actions.routinesOpen} className="text-muted" onClick={actions.onAllRoutines}>
        <Clock />
      </Button>
    </Tip>
  );
}

function SettingsButton({ actions, side, className }: Readonly<{ actions: WorkspaceActions; side?: TipSide; className?: string }>) {
  return (
    <Tip label="Settings" side={side}>
      <Button size="icon" aria-label="Settings" aria-pressed={actions.settingsOpen} className={cn('text-muted', className)} onClick={() => actions.onSettings()}>
        <SettingsIcon />
      </Button>
    </Tip>
  );
}

function PlannerButton({ actions, side, className }: Readonly<{ actions: WorkspaceActions; side?: TipSide; className?: string }>) {
  if (!actions.planner) return null;
  return (
    <Tip label="Planner" side={side}>
      <Button size="icon" aria-label="Planner" aria-pressed={actions.planner.open} className={cn('text-muted', className)} onClick={() => actions.planner!.onOpen()}>
        <KanbanSquare />
      </Button>
    </Tip>
  );
}

function ConnectionDot({ connection, side }: Readonly<{ connection: Connection; side?: TipSide }>) {
  return (
    <Tip label={connection === 'connected' ? 'Connected' : CONNECTION_TEXT[connection]} side={side}>
      <output className="flex size-7 items-center justify-center">
        <Dot tone={CONNECTION_TONE[connection]} pulse={connection !== 'connected'} />
        <span className="sr-only">{CONNECTION_TEXT[connection]}</span>
      </output>
    </Tip>
  );
}

/**
 * The collapsed sidebar (wide layout): a narrow rail (`--spacing-rail-collapsed`) on the sidebar's floor. At the top the UAM
 * mark (shows the sidebar, with the Needs you count), New task, the Project filter, Routines and Add
 * project; at the foot Settings, the planner and the connection. Each is the expanded
 * sidebar's own control, so it opens the same thing; tips open to the right.
 */
export function SidebarRail({ projects, actions, connection, count }: Readonly<{ projects: Project[]; actions: WorkspaceActions; connection: Connection; count: number }>) {
  return (
    <nav aria-label="Sidebar" className="flex h-full w-rail-collapsed flex-col items-center bg-rail pb-2 text-body">
      <div className="flex h-header shrink-0 items-center">
        <SidebarToggle id="sidebar-show" open={false} count={count} onToggle={actions.onToggleSidebar} side="right" />
      </div>
      <div className="flex flex-col items-center gap-1.5 pointer-coarse:gap-4">
        {projects.length > 0 && <NewTaskButton actions={actions} side="right" />}
        {projects.length > 0 && <FilterButton projects={projects} actions={actions} side="right" />}
        {projects.length > 0 && <RoutinesButton actions={actions} side="right" />}
        <AddProjectButton actions={actions} side="right" />
      </div>
      <span className="flex-1" />
      <div className="flex flex-col items-center gap-1.5 pointer-coarse:gap-4">
        <SettingsButton actions={actions} side="right" />
        <PlannerButton actions={actions} side="right" />
        <UsageButton side="right" onAddPrices={() => actions.onSettings('token-prices')} />
        <ConnectionDot connection={connection} side="right" />
      </div>
    </nav>
  );
}

export const CONNECTION_TEXT: Record<Connection, string> = {
  connecting: 'Connecting…',
  connected: 'Connected',
  reconnecting: 'Connection lost, reconnecting. Work continues on the server.',
  offline: 'Offline, retrying. Work continues on the server.',
};

const CONNECTION_TONE = { connecting: 'warning', connected: 'success', reconnecting: 'warning', offline: 'error' } as const;

const SHELVES_KEY = 'uam.shelves';

function readShelves(): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(SHELVES_KEY) ?? '{}') as Record<string, boolean>;
  } catch {
    return {};
  }
}

/** Two-pane mark: the host's sessions side by side, in ink. */
export function Brand({ className, markOnly = false }: Readonly<{ className?: string; markOnly?: boolean }>) {
  return (
    <span className={cn('inline-flex items-center gap-2', className)}>
      <svg aria-hidden="true" viewBox="0 0 20 20" className="size-4 shrink-0">
        <rect x="1" y="1" width="18" height="18" rx="5" className="fill-ink" />
        <rect x="4.5" y="5" width="4.5" height="10" rx="1.2" className="fill-raised" />
        <rect x="11" y="5" width="4.5" height="6" rx="1.2" className="fill-raised" />
        <rect x="11" y="12.5" width="4.5" height="2.5" rx="1" className="fill-raised opacity-60" />
      </svg>
      {!markOnly && <span className="text-title font-semibold text-ink">UAM</span>}
    </span>
  );
}

/* ---------- Keyboard navigation ---------- */

const NAV = '[data-nav]:not([disabled])';

/** Arrow keys move between visible Task and shelf rows; Home/End jump. */
function onListKeyDown(e: KeyboardEvent<HTMLElement>) {
  const target = e.target as HTMLElement;
  if (target.tagName === 'INPUT') return;
  const rows = Array.from(e.currentTarget.querySelectorAll<HTMLElement>(NAV)).filter((el) => el.offsetParent !== null);
  // From a row's hover action (Settle), move relative to that row.
  const current = target.closest('[data-task-row]')?.querySelector<HTMLElement>('[data-nav]') ?? target.closest<HTMLElement>('[data-nav]');
  const i = rows.indexOf(current as HTMLElement);
  const focus = (n: number) => {
    rows[Math.max(0, Math.min(rows.length - 1, n))]?.focus();
    e.preventDefault();
  };
  switch (e.key) {
    case 'ArrowDown':
      return focus(i + 1);
    case 'ArrowUp':
      return focus(i - 1);
    case 'Home':
      return focus(0);
    case 'End':
      return focus(rows.length - 1);

  }
}

/* ---------- Task row ---------- */

/** Rows enter, leave and move with a view transition when the Task list changes (type "sessions"); every other render, the selection included, leaves them to their CSS transitions. */
const ROW_TRANSITION = { sessions: 'vt-row', default: 'none' } as const;
const ROW_ENTER = { sessions: 'vt-row-enter', default: 'none' } as const;
const ROW_EXIT = { sessions: 'vt-row-exit', default: 'none' } as const;

/** A shelf row's tip: the full title, its Project with the directory, and when the Task was created, settled and archived. */
function shelfTip(s: SessionSummary, project: Project) {
  const at = (label: string, iso?: string) => iso && <span className="block">{label} <time dateTime={iso}>{new Date(iso).toLocaleString()}</time></span>;
  return (
    <>
      <span className="block font-medium">{taskName(s) || 'New task'}</span>
      <span className="mt-1 flex items-center gap-1.5"><ProjectBadge badge={project.badge} />{project.name}</span>
      <span className="block text-on-primary/70 [overflow-wrap:anywhere]">{project.dir}</span>
      <span className="mt-1 block text-on-primary/70">
        {at('Created', s.created_at)}
        {at('Settled', s.settled_at)}
        {at('Archived', s.archived_at)}
      </span>
    </>
  );
}

/** This Task's own changes (contract C1), when the service reports them. */
type DiffStat = { files: number; additions: number; deletions: number };

/**
 * `compact`: a Settled or Archived shelf row, the Project badge and title on one line, faded until hovered, focused or selected; the tip holds the rest.
 * Otherwise a Task row, never more than two lines: the Project badge, the name, `+N −M` and the time, then one plain status line,
 * truncated; the row's title holds the whole of it (what a Needs you row waits on, a finished turn's outcome).
 */
function TaskRow({ session: s, project, selected, compact = false }: Readonly<{ session: SessionSummary; project: Project; selected: boolean; compact?: boolean }>) {
  const { hasNews } = useApp();
  const a = useTaskActions();
  // A touch release after opening the context menu must not select the Task and close the drawer.
  const contextOpen = useRef(false);
  const unread = hasNews(s);
  const attention = needsYouNow(s, hasNews);
  const strong = selected || attention || unread;
  const items = taskMenuItems(s, a, 'row');
  // The menu's own Settle item, shown on hover only while its rules allow it.
  const settle = compact ? undefined : items.find((item) => item.key === 'settle' && !item.disabled);
  const renaming = a.renaming?.id === s.id && a.renaming.place === 'row';
  const status = taskStatus(s, unread);
  const diff = (s as SessionSummary & { diff?: DiffStat }).diff;
  // One class string for the button and for the plain container that replaces it while renaming, so the swap never shifts layout.
  // A Task card on the rail: `raised` with the soft ring, the open one `tint-selected`; the wrapper lifts it on hover (`lift`: transform and a pre-drawn shadow's opacity).
  const weight = strong ? 'font-medium text-ink' : 'text-body';
  let rowClass: string;
  if (compact) {
    rowClass = cn(
      'flex h-8 w-full items-center gap-2 rounded-sm px-2 text-left text-ui transition-[background-color,color,opacity] duration-100 focus-visible:-outline-offset-2 pointer-coarse:h-11',
      selected ? 'bg-tint-selected' : 'opacity-60 hover:bg-tint-hover hover:opacity-100 focus-within:opacity-100',
      weight,
    );
  } else {
    rowClass = cn(
      'flex min-h-14 w-full flex-col justify-center gap-0.5 rounded-md px-2.5 py-2 text-left text-caption shadow-raised transition-[background-color] duration-100 focus-visible:-outline-offset-2',
      selected ? 'bg-tint-selected' : 'bg-raised',
      readOnly(s) && !selected && 'text-muted',
      weight,
    );
  }

  const row = (
    <div
      data-task-row={s.id}
      className={compact ? undefined : 'lift group/row rounded-md'}
    >
      {renaming ? (
        // Not a button while the input is inside: interactive content cannot nest in one.
        <div className={rowClass}>
          {!compact && <span className="flex w-full min-w-0 items-center gap-1.5 text-meta text-muted"><ProjectBadge badge={project.badge} /><span className="truncate text-caption" title={project.name}>{project.name}</span></span>}
          <InlineName initial={s.name} onSave={(v) => void a.rename(s.id, v)} onCancel={a.cancelRename} className="h-7 w-full" label="Task name" />
        </div>
      ) : (
        <Tip label={compact && shelfTip(s, project)} side="right">
          <button
            type="button"
            data-nav=""
            aria-current={selected ? 'true' : undefined}
            title={compact ? undefined : `${taskName(s) || 'New task'} · ${project.name}\n${status.text}`}
            className={rowClass}
            onClick={(event) => {
              if (contextOpen.current) { event.preventDefault(); return; }
              a.select(s.id);
            }}
            onDoubleClick={() => canRename(s, a) && a.startRename(s.id, 'row')}
            onKeyDown={(e) => {
              if (e.key === 'F2' && canRename(s, a)) {
                e.preventDefault();
                a.startRename(s.id, 'row');
              }
            }}
          >
            {compact ? (
              <>
                <ProjectBadge badge={project.badge} />
                <span className="sr-only">{project.name}, </span>
                <TaskTitle session={s} className={cn('min-w-0 flex-1 truncate', selected && 'font-semibold')} />
              </>
            ) : (
              <>
                <span className="flex w-full min-w-0 items-center gap-2">
                  <ProjectBadge badge={project.badge} />
                  <span className="sr-only">{project.name}, </span>
                  <TaskTitle session={s} className={cn('min-w-0 flex-1 truncate text-ui', selected && 'font-semibold')} />
                  {diff && diff.additions + diff.deletions > 0 && (
                    <span className="shrink-0 font-mono text-meta font-normal tabular-nums">
                      <span className="text-success">+{diff.additions}</span> <span className="text-error">−{diff.deletions}</span>
                      <span className="sr-only"> lines,</span>
                    </span>
                  )}
                  <time dateTime={s.updated_at} className="shrink-0 text-meta font-normal text-muted tabular-nums">{relTime(s.updated_at)}</time>
                </span>
                <span className={cn('block w-full truncate pl-6 font-normal', TONE_TEXT[status.tone])}>
                  <span className="sr-only">, </span>
                  {status.text}
                </span>
              </>
            )}
          </button>
        </Tip>
      )}
      {settle && !renaming && (
        // A sibling of the row button, not inside it, so clicking Settle never selects the row. It sits at the end of the status line.
        <Tip label="Settle">
          <Button
            size="icon-sm"
            aria-label={`Settle ${taskName(s) || 'New task'}`}
            className={cn(
              'absolute right-1.5 bottom-1.5 text-muted opacity-0 transition-[opacity,background-color,color] group-hover/row:opacity-100 group-has-focus-visible/row:opacity-100 pointer-coarse:opacity-100',
              selected ? 'bg-tint-selected' : 'bg-raised',
            )}
            onClick={settle.onSelect}
          >
            <CircleCheck />
          </Button>
        </Tip>
      )}
    </div>
  );

  return (
    <ViewTransition name={`task-${s.id}`} update={ROW_TRANSITION} enter={ROW_ENTER} exit={ROW_EXIT} share="none" default="none">
      <li>
        <ContextMenu.Root onOpenChange={(open) => { contextOpen.current = open; }}>
          <ContextMenu.Trigger render={<div />}>{row}</ContextMenu.Trigger>
          <ContextMenu.Content>
            <ContextMenu.Actions items={items} />
          </ContextMenu.Content>
        </ContextMenu.Root>
      </li>
    </ViewTransition>
  );
}

/* ---------- State groups ---------- */

/** One state group: its title and count, then its rows. Needs you carries the Alt+J hint (not on touch, where there are no keys). */
function Group({ group, projects, tasks, selectedId }: Readonly<{ group: GroupKey; projects: ReadonlyMap<string, Project>; tasks: SessionSummary[]; selectedId: string | null }>) {
  const id = useId();
  if (tasks.length === 0) return null;
  return (
    <section aria-labelledby={id} className="mb-1">
      <h2 id={id} className={cn('flex h-6 items-center gap-2 px-2 text-eyebrow uppercase', group === 'you' ? 'text-attention' : 'text-muted')}>
        {GROUP_TITLES[group]}
        <span className="rounded-full bg-sunken px-1.5 text-meta font-normal tracking-normal tabular-nums text-muted normal-case">{tasks.length}</span>
        {group === 'you' && (
          <span className="ml-auto flex items-center gap-1 text-meta font-normal tracking-normal text-muted normal-case pointer-coarse:hidden" aria-hidden="true">
            <Key>Alt</Key>+<Key>J</Key> next
          </span>
        )}
      </h2>
      <ul className="flex flex-col gap-1">{tasks.map((t) => <TaskRow project={projects.get(t.project_id)!} key={t.id} session={t} selected={t.id === selectedId} />)}</ul>
    </section>
  );
}

const GROUPS: readonly GroupKey[] = ['you', 'review', 'working', 'idle'];

/* ---------- Shelf (Settled / Archived) ---------- */

function Shelf({ projects, label, tasks, selectedId, open, onToggle }: Readonly<{ projects: ReadonlyMap<string, Project>; label: string; tasks: SessionSummary[]; selectedId: string | null; open: boolean; onToggle: () => void }>) {
  if (tasks.length === 0) return null;
  const pinned = !open ? tasks.find((t) => t.id === selectedId) : undefined;
  return (
    <div className="mt-1">
      <button
        type="button"
        data-nav=""
        aria-expanded={open}
        className="flex h-7 w-full items-center gap-2 rounded-sm px-2 text-caption text-muted transition-colors hover:bg-tint-hover hover:text-body focus-visible:-outline-offset-2 pointer-coarse:h-11"
        onClick={onToggle}
      >
        <span className="whitespace-nowrap">
          {label} <span className="tabular-nums text-muted">{tasks.length}</span>
        </span>
        <span className="fade-rule flex-1" aria-hidden="true" />
        <ChevronRight aria-hidden="true" className={cn('size-3.5 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
      </button>
      <Collapse open={open}>
        <ul className="flex flex-col gap-px pt-px">
          {/* The pinned copy below owns the selected row while the shelf is closed: one view-transition name each. */}
          {tasks.filter((t) => t !== pinned).map((t) => (
            <TaskRow project={projects.get(t.project_id)!} key={t.id} session={t} selected={t.id === selectedId} compact />
          ))}
        </ul>
      </Collapse>
      {pinned && (
        <ul className="flex flex-col gap-px">
          <TaskRow project={projects.get(pinned.project_id)!} session={pinned} selected compact />
        </ul>
      )}
    </div>
  );
}

/* ---------- Sidebar ---------- */

export const Sidebar = memo(function Sidebar({
  loaded,
  projects,
  sessions,
  selectedId,
  actions,
  authRequired,
  onLogout,
  connection,
  version,
}: {
  /** False until the first snapshot: the list is a skeleton, never "No projects yet". */
  loaded: boolean;
  projects: Project[];
  sessions: SessionSummary[];
  selectedId: string | null;
  actions: WorkspaceActions;
  authRequired: boolean;
  onLogout: () => void;
  connection: Connection;
  version?: string;
}) {
  useMinuteTick();
  const { hasNews } = useApp();
  const [query, setQuery] = useState('');
  const [shelves, setShelves] = useState<Record<string, boolean>>(readShelves);
  const list = useRef<HTMLDivElement>(null);
  const chosen = filteredProject(projects, actions.filter);
  const projectMap = useMemo(() => new Map(projects.map((project) => [project.id, project])), [projects]);
  const tasks = useMemo(() => sidebarTasks(projects, sessions, actions.filter, query), [projects, sessions, actions.filter, query]);
  const { active, settled, archived } = groupTasks(tasks);
  const groups = commandGroups(active, hasNews);
  const shelfScope = chosen?.id ?? 'all';
  const toggleShelf = (key: string) =>
    setShelves((s) => {
      const next = { ...s, [key]: !s[key] };
      localStorage.setItem(SHELVES_KEY, JSON.stringify(next));
      return next;
    });

  // Keep the selected row in view when the selection changes from outside the list.
  useEffect(() => {
    if (!selectedId) return;
    list.current?.querySelector<HTMLElement>(`[data-task-row="${CSS.escape(selectedId)}"]`)?.scrollIntoView({ block: 'nearest' });
  }, [selectedId]);

  const conn = CONNECTION_TONE[connection];

  // The list: a skeleton before the first snapshot, the invitation without a Project, the search's matches, or the Tasks and their shelves.
  let body: ReactNode;
  if (!loaded) {
    body = <Skeleton label="Loading tasks…" rows={5} className="gap-1" rowClassName="h-14 w-full rounded-md" />;
  } else if (projects.length === 0) {
    body = (
      <div className="flex flex-col items-start gap-3 px-2 pt-6">
        <p className="text-ui text-muted">No projects yet. A project is a directory on this host.</p>
        <Button variant="secondary" size="sm" onClick={actions.onAddProject}>
          <FolderPlus />
          Add project
        </Button>
      </div>
    );
  } else if (query.trim()) {
    body = (
      <>
        <p className="px-2 py-2 text-caption text-muted" role="status">{tasks.length} matching tasks</p>
        <ul className="flex flex-col gap-1 animate-fade-in">{tasks.map((t) => <TaskRow project={projectMap.get(t.project_id)!} key={t.id} session={t} selected={t.id === selectedId} />)}</ul>
      </>
    );
  } else {
    body = (
      // The shelves sit at the foot of the list while the active Tasks are few, and follow them once they scroll.
      <div className="flex min-h-full flex-col">
        <div className="animate-fade-in">
          {GROUPS.map((g) => <Group key={g} group={g} projects={projectMap} tasks={groups[g]} selectedId={selectedId} />)}
        </div>
        {active.length === 0 && <p className="px-2 py-3 text-caption text-muted">No active tasks.</p>}
        <div className="mt-auto">
          <Shelf projects={projectMap} label="Settled" tasks={settled} selectedId={selectedId} open={!!shelves[`${shelfScope}:settled`]} onToggle={() => toggleShelf(`${shelfScope}:settled`)} />
          <Shelf projects={projectMap} label="Archived" tasks={archived} selectedId={selectedId} open={!!shelves[`${shelfScope}:archived`]} onToggle={() => toggleShelf(`${shelfScope}:archived`)} />
        </div>
      </div>
    );
  }

  return (
    <nav aria-label="Tasks" className="flex h-full min-h-0 flex-col bg-rail text-body">
      <header className="flex h-header shrink-0 items-center gap-0.5 px-2">
        <SidebarToggle id="sidebar-hide" wordmark open={actions.sidebarOpen} onToggle={actions.onToggleSidebar} />
        <label className="flex min-w-0 flex-1 items-center gap-1.5 rounded-sm px-1 text-muted transition-[background-color,box-shadow] duration-100 focus-within:bg-raised focus-within:shadow-focus">
          <Search aria-hidden="true" className="size-3.5 shrink-0" />
          <input type="search" aria-label="Search tasks" placeholder="Search" value={query} onChange={(e) => setQuery(e.target.value)} className="h-8 min-w-0 w-full bg-transparent text-ui outline-none placeholder:text-muted pointer-coarse:h-11" />
        </label>
        {projects.length > 0 && <FilterButton projects={projects} actions={actions} />}
        {projects.length > 0 && <RoutinesButton actions={actions} />}
        <AddProjectButton actions={actions} />
        {projects.length > 0 && <NewTaskButton actions={actions} id="new-task" />}
      </header>

      {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions -- the rows are buttons; this only relays arrow keys between them. */}
      <div ref={list} className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pt-1 pb-3" aria-busy={!loaded || undefined} onKeyDown={(e) => onListKeyDown(e)}>
        {body}
      </div>

      {connection !== 'connected' && (
        <output className={cn('mx-2 mb-2 flex items-start gap-2 rounded-sm px-2 py-1.5 text-caption', connection === 'offline' ? 'bg-error-wash text-error' : 'bg-warning-wash text-warning')}>
          <Dot tone={conn} pulse className="mt-1.5" />
          {CONNECTION_TEXT[connection]}
        </output>
      )}
      <footer className="flex min-h-9 shrink-0 items-center gap-2 px-3 text-caption text-muted">
        <SettingsButton actions={actions} className="-ml-1.5" />
        <PlannerButton actions={actions} className="-ml-1" />
        <UsageButton className="-ml-1" onAddPrices={() => actions.onSettings('token-prices')} />
        <ConnectionDot connection={connection} />
        {version && <span className="truncate text-meta" title={version}>{version}</span>}
        <span className="flex-1" />
        {authRequired && (
          <Button size="sm" className="-mr-2 text-muted" onClick={onLogout}>
            <LogOut />
            Log out
          </Button>
        )}
      </footer>
    </nav>
  );
});
