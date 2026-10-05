import { Archive, ChevronRight, CircleCheck, CircleMinus, Clock, CloudOff, Eye, FolderPlus, GitBranch, KanbanSquare, MessageCircleQuestion, Minimize2, Pause, RefreshCw, Settings as SettingsIcon, Search, Square, SquarePen, TriangleAlert } from 'lucide-react';
import { ViewTransition, memo, useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { readOnly, taskName, type Project, type SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { filteredProject, groupTasks, needsYouNow, sidebarTasks, taskStatus } from '../lib/tasks';
import type { Connection } from '../state';
import { Dot, InlineName, ProjectBadge, Skeleton, TaskTitle, WorkingMark, dateTime, relTime, useApp, useMinuteTick } from './common';
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

/** The rail's icon with a tip, or (`labelled`, in the sidebar footer) the icon and its word. */
function FooterButton({ label, icon, pressed, onClick, labelled, side }: Readonly<{ label: string; icon: ReactNode; pressed: boolean; onClick: () => void; labelled?: boolean; side?: TipSide }>) {
  if (labelled) {
    return (
      <Button size="sm" aria-pressed={pressed} className="text-muted" onClick={onClick}>
        {icon}
        {label}
      </Button>
    );
  }
  return (
    <Tip label={label} side={side}>
      <Button size="icon" aria-label={label} aria-pressed={pressed} className="text-muted" onClick={onClick}>
        {icon}
      </Button>
    </Tip>
  );
}

function SettingsButton({ actions, side, labelled }: Readonly<{ actions: WorkspaceActions; side?: TipSide; labelled?: boolean }>) {
  return <FooterButton label="Settings" icon={<SettingsIcon />} pressed={actions.settingsOpen} onClick={() => actions.onSettings()} labelled={labelled} side={side} />;
}

function PlannerButton({ actions, side, labelled }: Readonly<{ actions: WorkspaceActions; side?: TipSide; labelled?: boolean }>) {
  if (!actions.planner) return null;
  return <FooterButton label="Planner" icon={<KanbanSquare />} pressed={actions.planner.open} onClick={() => actions.planner!.onOpen()} labelled={labelled} side={side} />;
}

/**
 * The rail's connection: silent while connected (the status is for screen readers), a warning or
 * error glyph with its sentence in a tip once the stream is lost. Colour is never the only sign.
 */
function ConnectionMark({ connection, side }: Readonly<{ connection: Connection; side?: TipSide }>) {
  if (connection === 'connected' || connection === 'connecting') return <output className="sr-only">{CONNECTION_TEXT[connection]}</output>;
  const Icon = connection === 'offline' ? CloudOff : RefreshCw;
  return (
    <Tip label={CONNECTION_TEXT[connection]} side={side}>
      <output className={cn('flex size-7 items-center justify-center', connection === 'offline' ? 'text-error' : 'text-warning')}>
        <Icon aria-hidden="true" className="size-4" />
        <span className="sr-only">{CONNECTION_TEXT[connection]}</span>
      </output>
    </Tip>
  );
}

/** "v0.7.1-161-g228dbee0" reads as "v0.7.1+161"; the tip keeps the whole build. Release versions read as they are. */
function VersionMeta({ version }: Readonly<{ version: string }>) {
  const build = /^(.+)-(\d+)-g([0-9a-f]{7,})(-dirty)?$/.exec(version);
  const short = build ? `${build[1]}+${build[2]}` : version;
  return (
    <Tip
      label={
        <>
          UAM {version}
          {build && <span className="block text-on-primary/70">{`${build[2]} ${build[2] === '1' ? 'commit' : 'commits'} after ${build[1]}, build ${build[3]}${build[4] ? ', with local changes' : ''}`}</span>}
        </>
      }
    >
      <span data-version className="min-w-0 truncate text-meta text-faint tabular-nums">
        {short}
      </span>
    </Tip>
  );
}

/**
 * The collapsed sidebar (wide layout): a narrow rail (`--spacing-rail-collapsed`) on the sidebar's floor. At the top the UAM
 * mark (shows the sidebar, with the Needs you count), then the Project filter, Routines, Add project and New task,
 * in the sidebar header's order; at the foot Settings, the planner and the connection. Each is the expanded
 * sidebar's own control, so it opens the same thing; tips open to the right.
 */
export function SidebarRail({ projects, actions, connection, count }: Readonly<{ projects: Project[]; actions: WorkspaceActions; connection: Connection; count: number }>) {
  return (
    <nav aria-label="Sidebar" className="flex h-full w-rail-collapsed flex-col items-center bg-rail pb-2 text-body">
      <div className="flex h-header shrink-0 items-center">
        <SidebarToggle id="sidebar-show" open={false} count={count} onToggle={actions.onToggleSidebar} side="right" />
      </div>
      <div className="flex flex-col items-center gap-1.5 pointer-coarse:gap-4">
        {projects.length > 0 && <FilterButton projects={projects} actions={actions} side="right" />}
        {projects.length > 0 && <RoutinesButton actions={actions} side="right" />}
        <AddProjectButton actions={actions} side="right" />
        {projects.length > 0 && <NewTaskButton actions={actions} side="right" />}
      </div>
      <span className="flex-1" />
      <div className="flex flex-col items-center gap-1.5 pointer-coarse:gap-4">
        <SettingsButton actions={actions} side="right" />
        <PlannerButton actions={actions} side="right" />
        <UsageButton side="right" onAddPrices={() => actions.onSettings('token-prices')} />
        <ConnectionMark connection={connection} side="right" />
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

/**
 * The list is one tab stop (roving `tabIndex`). Arrow keys move between the Task and shelf rows on screen (a
 * closed shelf's rows are inert), Home/End jump; Right reaches a row's Settle and Left comes back.
 */
function onListKeyDown(e: KeyboardEvent<HTMLElement>) {
  const target = e.target as HTMLElement;
  if (target.tagName === 'INPUT') return;
  const rows = Array.from(e.currentTarget.querySelectorAll<HTMLElement>(NAV)).filter((el) => el.offsetParent !== null && !el.closest('[inert]'));
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
    case 'ArrowRight': {
      const settle = target.closest('[data-task-row]')?.querySelector<HTMLElement>('[data-settle]');
      if (!settle || settle === target) return;
      settle.focus();
      return e.preventDefault();
    }
    case 'ArrowLeft':
      if (target.matches('[data-settle]')) focus(i);
  }
}

/* ---------- Task row ---------- */

/** Rows enter, leave and move with a view transition when the Task list changes (type "sessions"); every other render, the selection included, leaves them to their CSS transitions. */
const ROW_TRANSITION = { sessions: 'vt-row', default: 'none' } as const;
const ROW_ENTER = { sessions: 'vt-row-enter', default: 'none' } as const;
const ROW_EXIT = { sessions: 'vt-row-exit', default: 'none' } as const;

/** A shelf row's tip: the full title, its Project with the directory, and when the Task was created, settled and archived. */
function shelfTip(s: SessionSummary, project: Project) {
  const at = (label: string, iso?: string) => iso && <span className="block">{label} <time dateTime={iso}>{dateTime(iso)}</time></span>;
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

/** A Task row's tip, styled like a shelf row's: the full title, its Project, the state in words, the Project branch and the Task's changes. */
function rowTip(s: SessionSummary, project: Project, status: string, diff?: DiffStat) {
  return (
    <>
      <span className="block font-medium">{taskName(s) || 'New task'}</span>
      <span className="mt-1 flex items-center gap-1.5"><ProjectBadge badge={project.badge} />{project.name}</span>
      <span className="mt-1 block text-on-primary/70">
        <span className="block">{status}</span>
        {project.branch && <span className="block">Project branch: {project.branch}</span>}
        {diff && diff.additions + diff.deletions > 0 && <span className="block">+{diff.additions} −{diff.deletions} lines</span>}
      </span>
    </>
  );
}

const STATUS_MARKS = {
  Input: { icon: MessageCircleQuestion, tone: 'text-warning' },
  Starting: { icon: WorkingMark, tone: 'text-accent' },
  Working: { icon: WorkingMark, tone: 'text-accent' },
  Compacting: { icon: Minimize2, tone: 'text-badge-violet' },
  Review: { icon: Eye, tone: 'text-badge-teal' },
  Finished: { icon: CircleCheck, tone: 'text-success' },
  Error: { icon: TriangleAlert, tone: 'text-error' },
  Interrupted: { icon: Pause, tone: 'text-warning' },
  Stopped: { icon: Square, tone: 'text-muted' },
  Closed: { icon: CircleMinus, tone: 'text-muted' },
  Idle: { icon: Clock, tone: 'text-muted' },
  Settled: { icon: CircleCheck, tone: 'text-muted' },
  Archived: { icon: Archive, tone: 'text-muted' },
};

/**
 * `compact`: a Settled or Archived shelf row, the Project badge and title on one line, faded until hovered, focused or selected; the tip holds the rest.
 * Otherwise a Task row, never more than two lines: Project badge/name and state, then Task title, muted branch and time;
 * its tip holds the full detail (what a Needs you row waits on, a finished turn's outcome).
 * `tabStop`: the row holds the list's one tab stop.
 */
function TaskRow({ session: s, project, selected, compact = false, tabStop }: Readonly<{ session: SessionSummary; project: Project; selected: boolean; compact?: boolean; tabStop: boolean }>) {
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
  const { icon: StatusIcon, tone: statusTone } = STATUS_MARKS[status.label];
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
          {!compact && <span className="flex w-full min-w-0 items-center gap-1.5 text-meta text-muted"><ProjectBadge badge={project.badge} /><span className="truncate text-caption">{project.name}</span></span>}
          <InlineName initial={s.name} onSave={(v) => void a.rename(s.id, v)} onCancel={a.cancelRename} className="h-7 w-full" label="Task name" />
        </div>
      ) : (
        <Tip label={compact ? shelfTip(s, project) : rowTip(s, project, status.text, diff)} side="right">
          <button
            type="button"
            data-nav={s.id}
            tabIndex={tabStop ? 0 : -1}
            aria-current={selected ? 'true' : undefined}
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
                <span className="flex w-full min-w-0 items-center gap-1.5">
                  <ProjectBadge badge={project.badge} />
                  <span className="min-w-0 flex-1 truncate font-normal text-muted">{project.name}</span>
                  <span className={cn('flex shrink-0 items-center gap-1 font-normal', statusTone)}>
                    <StatusIcon aria-hidden="true" className="size-3.5 shrink-0" />
                    {status.label}
                  </span>
                </span>
                <span className={cn('flex w-full min-w-0 items-baseline gap-1.5', settle && 'pr-6')}>
                  <TaskTitle session={s} className={cn('min-w-0 flex-1 truncate text-ui', selected && 'font-semibold')} />
                  {project.branch && <span className="min-w-0 max-w-20 truncate text-meta font-normal text-muted"><GitBranch aria-hidden="true" className="mr-1 inline-block size-3 align-text-bottom" /><span>{project.branch}</span></span>}
                  <time dateTime={s.updated_at} className="shrink-0 text-meta font-normal text-muted tabular-nums">{relTime(s.updated_at)}</time>
                </span>
              </>
            )}
          </button>
        </Tip>
      )}
      {settle && !renaming && (
        // A sibling of the row button, not inside it, so clicking Settle never selects the row. Its reserved space at the end of the second line keeps the time visible.
        // Not a tab stop of its own: Right from the row reaches it (onListKeyDown), and the row's menu has it too.
        <Tip label="Settle">
          <Button
            size="icon-sm"
            tabIndex={-1}
            data-settle=""
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

/* ---------- Shelf (Settled / Archived) ---------- */

/** The keys of a shelf's rows on screen, in order: its header (`shelf:<label>`), then its Tasks while open, else the selected one pinned below it. */
function shelfKeys(label: string, tasks: SessionSummary[], open: boolean, selectedId: string | null): string[] {
  if (tasks.length === 0) return [];
  return [`shelf:${label}`, ...tasks.filter((t) => open || t.id === selectedId).map((t) => t.id)];
}

function Shelf({ projects, label, tasks, selectedId, open, onToggle, tabStop }: Readonly<{ projects: ReadonlyMap<string, Project>; label: string; tasks: SessionSummary[]; selectedId: string | null; open: boolean; onToggle: () => void; /** The key of the row holding the list's tab stop. */ tabStop?: string }>) {
  if (tasks.length === 0) return null;
  const pinned = !open ? tasks.find((t) => t.id === selectedId) : undefined;
  return (
    <div className="mt-1">
      <button
        type="button"
        data-nav={`shelf:${label}`}
        tabIndex={tabStop === `shelf:${label}` ? 0 : -1}
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
            <TaskRow project={projects.get(t.project_id)!} key={t.id} session={t} selected={t.id === selectedId} compact tabStop={t.id === tabStop} />
          ))}
        </ul>
      </Collapse>
      {pinned && (
        <ul className="flex flex-col gap-px">
          <TaskRow project={projects.get(pinned.project_id)!} session={pinned} selected compact tabStop={pinned.id === tabStop} />
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
  connection,
  version,
}: {
  /** False until the first snapshot: the list is a skeleton, never "No projects yet". */
  loaded: boolean;
  projects: Project[];
  sessions: SessionSummary[];
  selectedId: string | null;
  actions: WorkspaceActions;
  connection: Connection;
  version?: string;
}) {
  useMinuteTick();
  const [query, setQuery] = useState('');
  const [shelves, setShelves] = useState<Record<string, boolean>>(readShelves);
  const list = useRef<HTMLDivElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const chosen = filteredProject(projects, actions.filter);
  const projectMap = useMemo(() => new Map(projects.map((project) => [project.id, project])), [projects]);
  const tasks = useMemo(() => sidebarTasks(projects, sessions, actions.filter, query), [projects, sessions, actions.filter, query]);
  const { active, settled, archived } = groupTasks(tasks);
  // Newest first, whatever their state: a row never moves because its Task changed.
  const unsettled = active;
  const shelfScope = chosen?.id ?? 'all';
  const settledOpen = !!shelves[`${shelfScope}:settled`];
  const archivedOpen = !!shelves[`${shelfScope}:archived`];
  // The row holding the list's one tab stop: the last focused while it is on screen, else the open Task's, else the first.
  const [focused, setFocused] = useState<string | null>(null);
  const searching = !!query.trim();
  const keys = searching ? tasks.map((t) => t.id) : [...unsettled.map((t) => t.id), ...shelfKeys('Settled', settled, settledOpen, selectedId), ...shelfKeys('Archived', archived, archivedOpen, selectedId)];
  const tabStop = [focused, selectedId].find((key) => key && keys.includes(key)) ?? keys[0];
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
  } else if (searching) {
    body = (
      <>
        <p className="px-2 py-2 text-caption text-muted" role="status">{tasks.length === 0 ? 'No matching tasks' : `${tasks.length} matching ${tasks.length === 1 ? 'task' : 'tasks'}`}</p>
        {tasks.length === 0 && (
          <div className="flex flex-col items-start gap-2 px-2">
            <p className="text-caption text-muted">{`Search looks at task names, project names, folders and branches${chosen ? ` in ${chosen.name}` : ''}.`}</p>
            <Button variant="secondary" size="sm" onClick={() => { setQuery(''); search.current?.focus(); }}>
              Clear search
            </Button>
          </div>
        )}
        <ul className="flex flex-col gap-1 animate-fade-in">{tasks.map((t) => <TaskRow project={projectMap.get(t.project_id)!} key={t.id} session={t} selected={t.id === selectedId} tabStop={t.id === tabStop} />)}</ul>
      </>
    );
  } else {
    body = (
      // The shelves sit at the foot of the list while the active Tasks are few, and follow them once they scroll.
      <div className="flex min-h-full flex-col">
        <ul aria-label="Unsettled tasks" className="flex flex-col gap-1 animate-fade-in">
          {unsettled.map((t) => <TaskRow project={projectMap.get(t.project_id)!} key={t.id} session={t} selected={t.id === selectedId} tabStop={t.id === tabStop} />)}
        </ul>
        {active.length === 0 && <p className="px-2 py-3 text-caption text-muted">No active tasks.</p>}
        <div className="mt-auto">
          <Shelf projects={projectMap} label="Settled" tasks={settled} selectedId={selectedId} open={settledOpen} onToggle={() => toggleShelf(`${shelfScope}:settled`)} tabStop={tabStop} />
          <Shelf projects={projectMap} label="Archived" tasks={archived} selectedId={selectedId} open={archivedOpen} onToggle={() => toggleShelf(`${shelfScope}:archived`)} tabStop={tabStop} />
        </div>
      </div>
    );
  }

  return (
    <nav aria-label="Tasks" className="flex h-full min-h-0 flex-col bg-rail text-body">
      <header className="flex h-header shrink-0 items-center gap-0.5 px-2">
        <SidebarToggle id="sidebar-hide" wordmark open={actions.sidebarOpen} onToggle={actions.onToggleSidebar} />
        {/* The field lifts when focused and carries the keyboard focus ring; the input's own outline is off. */}
        <label className="flex min-w-0 flex-1 items-center gap-1.5 rounded-sm px-1 text-muted transition-[background-color,box-shadow] duration-100 focus-within:bg-raised focus-within:shadow-focus focus-within:outline-2 focus-within:outline-offset-1 focus-within:outline-focus">
          <Search aria-hidden="true" className="size-3.5 shrink-0" />
          <input ref={search} type="search" aria-label="Search tasks" placeholder="Search" value={query} onChange={(e) => setQuery(e.target.value)} className="h-8 min-w-0 w-full bg-transparent text-ui outline-none placeholder:text-muted pointer-coarse:h-11" />
        </label>
        {projects.length > 0 && <FilterButton projects={projects} actions={actions} />}
        {projects.length > 0 && <RoutinesButton actions={actions} />}
        <AddProjectButton actions={actions} />
        {projects.length > 0 && <NewTaskButton actions={actions} id="new-task" />}
      </header>

      {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions -- the rows are buttons; this only relays arrow keys between them. */}
      <div ref={list} className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain px-2 pt-1 pb-3" aria-busy={!loaded || undefined} onKeyDown={(e) => onListKeyDown(e)} onFocus={(e) => { const key = (e.target as HTMLElement).dataset.nav; if (key) setFocused(key); }}>
        {body}
      </div>

      {connection !== 'connected' && (
        <output className={cn('mx-2 mb-2 flex items-start gap-2 rounded-sm px-2 py-1.5 text-caption', connection === 'offline' ? 'bg-error-wash text-error' : 'bg-warning-wash text-warning')}>
          <Dot tone={conn} pulse className="mt-1.5" />
          {CONNECTION_TEXT[connection]}
        </output>
      )}
      {/* The foot: the account allowance as a labelled meter, then Settings, the planner and the version. A lost connection is the banner above; while connected the status is for screen readers only. */}
      <footer className="flex shrink-0 flex-col gap-1 px-2 pb-2">
        <div className="fade-rule mx-1 mb-1" aria-hidden="true" />
        <UsageButton variant="row" onAddPrices={() => actions.onSettings('token-prices')} />
        <div className="flex min-w-0 items-center gap-0.5">
          <SettingsButton actions={actions} labelled />
          <PlannerButton actions={actions} labelled />
          <span className="flex-1" />
          {version && <VersionMeta version={version} />}
          {connection === 'connected' && <output className="sr-only">{CONNECTION_TEXT.connected}</output>}
        </div>
      </footer>
    </nav>
  );
});
