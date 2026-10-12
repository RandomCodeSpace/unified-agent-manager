import { scopedMachine, useFederation, type Machine } from '../FederationContext';
import { Archive, ArrowUpFromLine, ChevronRight, CircleAlert, CircleCheck, CircleMinus, Clock, FilePen, CloudOff, Eye, FolderPlus, GitBranch, MessageCircleQuestion, Minimize2, Pause, RefreshCw, Settings as SettingsIcon, Search, Square, SquarePen, TriangleAlert } from 'lucide-react';
import { memo, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react';
import { readOnly, taskName, type Project, type SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { encodeEntity } from '../lib/instanceIdentity';
import { filteredProject, groupTasks, needsYouNow, pendingWork, sidebarTasks, taskStatus, type Unread } from '../lib/tasks';
import { pendingWords } from './BranchChip';
import type { Connection } from '../state';
import { Dot, InlineName, ProjectBadge, Skeleton, TaskTitle, WorkingMark, dateTime, relTime, useApp, useMinuteTick } from './common';
import { ProjectFilterPicker, type ProjectGroup } from './ProjectPicker';
import { TaskActionsContext, canRename, taskMenuItems, useTaskActions, type TaskActions } from './taskActions';
import { Button } from './ui/button';
import { Collapse } from './ui/collapse';
import { ContextMenu } from './ui/menu';
import { Tip } from './ui/tooltip';
import { UsageButton } from './UsageButton';
import { restartNotice } from './UamService';
import { BUILD_VERSION, shortVersion } from '../lib/update';
import { InstanceSwitcher } from './InstanceSwitcher';
import { InstanceBadge } from './InstanceBadge';

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
  /** Opens Settings, or with a target on that page: Token costs' prices, or Connected instances. */
  onSettings: (target?: 'token-prices' | 'connections') => void;
}

/**
 * The sidebar toggle (Ctrl/Cmd+B). In the sidebar header it hides the sidebar; at the start
 * of the main pane's header, once hidden, it brings it back. On a narrow screen it opens
 * and closes the drawer instead.
 */
export function SidebarToggle({ id, open, onToggle, size = 'icon', wordmark = false, count = 0, side, className }: Readonly<{ id?: string; open: boolean; onToggle: () => void; size?: 'icon' | 'icon-md'; wordmark?: boolean; /** Tasks that need you, shown on the button (beside the wordmark in the sidebar header); hidden at zero. */ count?: number; side?: TipSide; className?: string }>) {
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
          <span aria-hidden="true" className={cn('flex h-4 min-w-4 items-center justify-center rounded-full bg-attention px-1 text-meta font-semibold text-on-primary tabular-nums', !wordmark && 'absolute -top-0.5 -right-0.5 pointer-coarse:top-0.5 pointer-coarse:right-0.5')}>
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
  const federation = useFederation();
  const machines = federation?.machines;
  if (machines) {
    // Each machine's Projects under its name (the scoped instance's alone); a Project's buttons act on its own machine, opening it where the view lives.
    const filter = federation.filter;
    const scope = scopedMachine(federation);
    const groups: ProjectGroup[] = machines.filter((m) => !scope || m === scope).map((m) => {
      return {
        key: m.id,
        label: m.label,
        projects: m.active ? projects : m.state.projects,
        current: filter?.machine === m.id ? filter.project : null,
        onPick: (p) => federation.onFilter?.({ machine: m.id, project: p.id }),
        onEdit: m.active ? actions.onEditProject : (p) => federation.request?.({ machine: m.id, kind: 'edit-project', projectId: p.id }),
        onRoutines: m.active ? actions.onRoutines : m.client.supports('routines-v1') ? (p) => federation.go?.(m.id, `#routines=${encodeURIComponent(p.id)}`) : undefined,
      };
    });
    const chosen = groups.find((g) => g.current && g.projects.some((p) => p.id === g.current));
    // All projects keeps the instance scope; the instance switcher's All instances lifts it.
    return <ProjectFilterPicker projects={chosen?.projects ?? []} filter={chosen?.current ?? null} groups={groups} onFilter={() => federation.onFilter?.(scope ? { machine: scope.id, project: '' } : null)} onEdit={actions.onEditProject} side={side === 'right' ? 'right' : undefined} />;
  }
  return <ProjectFilterPicker projects={projects} filter={actions.filter} onFilter={actions.onFilter} onEdit={actions.onEditProject} onRoutines={actions.onRoutines} side={side === 'right' ? 'right' : undefined} />;
}

/** Whether any machine on screen has a Project: the rail and the header offer their Project buttons then. */
function useAnyProject(projects: Project[]): boolean {
  const machines = useFederation()?.machines;
  return projects.length > 0 || !!machines?.some((m) => !m.active && m.state.projects.length > 0);
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

/**
 * An icon with its name in a tip: the rail's and the sidebar footer's. A `notice` (something waiting there) is an
 * amber dot on the icon's corner, its sentence in the tip and the button's description; the name stays the same.
 */
function FooterButton({ label, icon, pressed, onClick, side, notice }: Readonly<{ label: string; icon: ReactNode; pressed: boolean; onClick: () => void; side?: TipSide; notice?: string }>) {
  const noticeId = useId();
  return (
    <>
      <Tip label={notice ? <>{label}<span className="block text-on-primary/70">{notice}</span></> : label} side={side}>
        <Button size="icon" aria-label={label} aria-pressed={pressed} aria-describedby={notice ? noticeId : undefined} className="text-muted" onClick={onClick}>
          {icon}
          {notice && <Dot tone="warning" className="absolute top-0.5 right-0.5" />}
        </Button>
      </Tip>
      {notice && <span id={noticeId} className="sr-only">{notice}</span>}
    </>
  );
}

function SettingsButton({ actions, side }: Readonly<{ actions: WorkspaceActions; side?: TipSide }>) {
  const meta = useApp().meta;
  const cliUpdate = meta?.providers.some((p) => p.cli_update);
  const notice = [restartNotice(meta?.service), cliUpdate && 'Copilot CLI update available'].filter(Boolean).join('; ');
  return <FooterButton label="Settings" icon={<SettingsIcon />} pressed={actions.settingsOpen} onClick={() => actions.onSettings()} side={side} notice={notice || undefined} />;
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
  const build = BUILD_VERSION.exec(version);
  const short = shortVersion(version);
  return (
    <Tip
      label={
        <>
          UAM {version}
          {build && <span className="block text-on-primary/70">{`${build[2]} ${build[2] === '1' ? 'commit' : 'commits'} after ${build[1]}, build ${build[3]}${build[4] ? ', with local changes' : ''}`}</span>}
        </>
      }
    >
      <span data-version className="min-w-0 truncate text-meta text-muted tabular-nums">
        {short}
      </span>
    </Tip>
  );
}

/**
 * The collapsed sidebar (wide layout): a narrow rail (`--spacing-rail-collapsed`) on the sidebar's floor. At the top the UAM
 * mark (shows the sidebar, with the Needs you count), then the Project filter, Routines, Add project and New task,
 * in the sidebar header's order; at the foot Settings and the connection. Each is the expanded
 * sidebar's own control, so it opens the same thing; tips open to the right.
 */
export function SidebarRail({ projects, actions, connection, count }: Readonly<{ projects: Project[]; actions: WorkspaceActions; connection: Connection; count: number }>) {
  const anyProject = useAnyProject(projects);
  return (
    <nav aria-label="Sidebar" className="flex h-full w-rail-collapsed flex-col items-center bg-rail pb-2 text-body">
      <div className="flex h-header shrink-0 items-center">
        <SidebarToggle id="sidebar-show" open={false} count={count} onToggle={actions.onToggleSidebar} side="right" />
      </div>
      <div className="flex flex-col items-center gap-1.5 pointer-coarse:gap-4">
        {anyProject && <FilterButton projects={projects} actions={actions} side="right" />}
        {projects.length > 0 && <RoutinesButton actions={actions} side="right" />}
        <AddProjectButton actions={actions} side="right" />
        {anyProject && <NewTaskButton actions={actions} side="right" />}
      </div>
      <span className="flex-1" />
      <div className="flex flex-col items-center gap-1.5 pointer-coarse:gap-4">
        <SettingsButton actions={actions} side="right" />
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

/** A shelf row's tip: the full title, its Project with the directory, and when the Task was created and settled. */
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
        {s.stage === 'settled' && s.settled_by && <span className="block">{SETTLED_BY[s.settled_by]}</span>}
      </span>
    </>
  );
}

/** Why the service settled a Task, in a Settled row's tip. */
const SETTLED_BY = { auto: 'Settled automatically after 7 quiet days', reviewed: 'Settled after you reviewed it' } as const;

/** This Task's own changes (contract C1), when the service reports them. */
type DiffStat = { files: number; additions: number; deletions: number };

/** A Task row's tip, styled like a shelf row's: the full title, its Project, the state in words, the Project branch, the Task's changes and its folder's unsaved git work. */
function rowTip(s: SessionSummary, project: Project, status: string, diff?: DiffStat) {
  const pending = readOnly(s) ? null : pendingWork(s);
  // The 7-day settle skips a Thread with unsaved work: the tip says why it is still here.
  const quietDays = Math.floor((Date.now() - Date.parse(s.updated_at)) / 86_400_000);
  return (
    <>
      <span className="block font-medium">{taskName(s) || 'New task'}</span>
      <span className="mt-1 flex items-center gap-1.5"><ProjectBadge badge={project.badge} />{project.name}</span>
      <span className="mt-1 block text-on-primary/70">
        <span className="block">{status}</span>
        {project.branch && <span className="flex items-center gap-1"><GitBranch aria-hidden="true" className="size-3 shrink-0" />{project.branch}</span>}
        {diff && diff.additions + diff.deletions > 0 && <span className="block">+{diff.additions} −{diff.deletions} lines</span>}
        {pending && <span className="block">{pendingWords(pending)}</span>}
        {pending && quietDays >= 7 && <span className="block">{`Quiet ${quietDays} days; stays active until this work is saved`}</span>}
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
  'Tests fail': { icon: CircleAlert, tone: 'text-warning' },
  'Command failed': { icon: CircleAlert, tone: 'text-warning' },
  Error: { icon: TriangleAlert, tone: 'text-error' },
  Interrupted: { icon: Pause, tone: 'text-warning' },
  Stopped: { icon: Square, tone: 'text-muted' },
  Closed: { icon: CircleMinus, tone: 'text-muted' },
  Idle: { icon: Clock, tone: 'text-muted' },
  Settled: { icon: CircleCheck, tone: 'text-muted' },
  Archived: { icon: Archive, tone: 'text-muted' },
};

/** Shared task presentation; owner-specific actions stay with each caller. */
export function TaskRowContent({ session, project, selected, unread, machine, settleable = false }: Readonly<{ session: SessionSummary; project?: Project; selected: boolean; unread: boolean; machine?: Pick<Machine, 'id' | 'label'>; settleable?: boolean }>) {
  const status = taskStatus(session, unread);
  const { icon: StatusIcon, tone: statusTone } = STATUS_MARKS[status.label];
  const pending = readOnly(session) ? null : pendingWork(session);
  return (
    <>
      <span className="flex w-full min-w-0 items-center gap-1.5">
        {project && <ProjectBadge badge={project.badge} />}
        <span className="min-w-0 truncate font-normal text-muted">{project?.name ?? 'Project'}</span>
        {/* The folder's unsaved git work, after its name: shared by every Thread there. Glyph and count, the words for screen readers. */}
        {pending && (
          <span className="flex shrink-0 items-center gap-1.5 text-meta font-normal text-warning tabular-nums">
            {pending.uncommitted > 0 && <span className="flex items-center gap-0.5"><FilePen aria-hidden="true" className="size-3" />{pending.uncommitted}</span>}
            {pending.unpushed > 0 && <span className="flex items-center gap-0.5"><ArrowUpFromLine aria-hidden="true" className="size-3" />{pending.unpushed}</span>}
            <span className="sr-only">{pendingWords(pending)}</span>
          </span>
        )}
        <span className="flex-1" />
        <span className={cn('flex shrink-0 items-center justify-end gap-1 font-normal', statusTone, settleable && 'min-w-14 group-hover/row:opacity-0 group-focus-within/row:opacity-0')}>
          <StatusIcon aria-hidden="true" className="size-3.5 shrink-0" />
          {status.label}
        </span>
      </span>
      <span className="flex w-full min-w-0 items-baseline gap-1.5">
        <TaskTitle session={session} className={cn('min-w-0 flex-1 truncate text-ui', selected && 'font-semibold')} />
        {project?.branch && <span className="min-w-0 max-w-20 truncate text-meta font-normal text-muted"><GitBranch aria-hidden="true" className="mr-1 inline-block size-3 align-text-bottom" /><span>{project.branch}</span></span>}
        <time dateTime={session.updated_at} className="shrink-0 text-meta font-normal text-muted tabular-nums">{relTime(session.updated_at)}</time>
        {machine && <InstanceBadge id={machine.id} label={machine.label} />}
      </span>
    </>
  );
}

/**
 * `compact`: a Settled shelf row, the Project badge and title on one line, faded until hovered, focused or selected; the tip holds the rest.
 * Otherwise a Task row, never more than two lines: Project badge/name and state, then Task title, muted branch and time;
 * its tip holds the full detail (what a Needs you row waits on, a finished turn's outcome).
 * `tabStop`: the row holds the list's one tab stop.
 */
const TaskRow = memo(function TaskRow({ session: s, project, selected, unread, attention, compact = false, tabStop, rowKey = s.id, machine, named = true, oneInstance = false }: Readonly<{ session: SessionSummary; project: Project; selected: boolean; unread: boolean; attention: boolean; /** The list's shared clock refreshes relative times even when the Task has not changed. */ minute: number; compact?: boolean; tabStop: boolean; /** The row's key in the list: the Task's ID, qualified by its machine under federation. */ rowKey?: string; /** The machine the Task runs on, with connected instances. */ machine?: Machine; /** Whether the row shows its machine badge: not while the list shows one instance. */ named?: boolean; /** Every row of the list is from one instance: the tip leaves out its name. */ oneInstance?: boolean }>) {
  const a = useTaskActions();
  // A touch release after opening the context menu must not select the Task and close the drawer.
  const contextOpen = useRef(false);
  const revealSettleOnTouch = useRef(false);
  const strong = selected || attention || unread;
  const items = taskMenuItems(s, a, 'row');
  // The menu's own Settle item, shown on hover only while its rules allow it.
  const settle = compact ? undefined : items.find((item) => item.key === 'settle' && !item.disabled);
  const renaming = a.renaming?.id === s.id && a.renaming.place === 'row';
  const status = taskStatus(s, unread);
  const diff = (s as SessionSummary & { diff?: DiffStat }).diff;
  // A machine linked to another Copilot account keeps its rows, faded; opening one shows why it is blocked.
  const unavailable = machine?.status.status === 'account-mismatch';
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
      unavailable && !selected && 'opacity-60',
      weight,
    );
  }

  const row = (
    <div
      data-task-row={rowKey}
      className={compact ? undefined : 'lift group/row rounded-md'}
    >
      {renaming ? (
        // Not a button while the input is inside: interactive content cannot nest in one.
        <div className={rowClass}>
          {!compact && <span className="flex w-full min-w-0 items-center gap-1.5 text-meta text-muted"><ProjectBadge badge={project.badge} /><span className="truncate text-caption">{project.name}</span></span>}
          <InlineName initial={s.name} onSave={(v) => void a.rename(s.id, v)} onCancel={a.cancelRename} className="h-7 w-full" label="Task name" />
        </div>
      ) : (
        <Tip label={<>{machine && !unavailable && !oneInstance && <span className="block text-on-primary/70">{machine.label}</span>}{unavailable && <span className="block text-warning">{machine.short} is unavailable: linked to a different Copilot account.</span>}{compact ? shelfTip(s, project) : rowTip(s, project, status.text, diff)}</>} side="right">
          <button
            type="button"
            data-nav={rowKey}
            aria-describedby={machine ? machineLabelId(machine) : undefined}
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
                {machine && named && <InstanceBadge id={machine.id} label={machine.label} />}
              </>
            ) : (
              <TaskRowContent session={s} project={project} selected={selected} unread={unread} settleable={!!settle} machine={named ? machine : undefined} />
            )}
          </button>
        </Tip>
      )}
      {settle && !renaming && (
        // A sibling of the row button, so Settle replaces the status without selecting the Task.
        // A first touch on the status reveals Settle; a second tap activates it.
        // Not a tab stop of its own: Right from the row reaches it (onListKeyDown), and the row's menu has it too.
        <Tip label={settle.label}>
          <Button
            size="sm"
            tabIndex={-1}
            data-settle=""
            aria-label={`Settle ${taskName(s) || 'New task'}`}
            className={cn(
              'pointer-events-none absolute top-1.5 right-1.5 h-6 min-w-16 justify-end gap-1 px-1 text-caption font-normal text-muted opacity-0 after:inset-0 group-hover/row:pointer-events-auto group-hover/row:opacity-100 group-focus-within/row:pointer-events-auto group-focus-within/row:opacity-100 pointer-coarse:pointer-events-auto pointer-coarse:after:inset-0 [&_svg]:size-3.5',
              selected ? 'bg-tint-selected' : 'bg-raised',
            )}
            onPointerDown={(event) => { revealSettleOnTouch.current = event.pointerType === 'touch' && document.activeElement !== event.currentTarget; }}
            onClick={(event) => {
              if (event.detail && revealSettleOnTouch.current) { revealSettleOnTouch.current = false; return; }
              settle.onSelect();
            }}
          >
            <CircleCheck aria-hidden="true" />
            {settle.label}
          </Button>
        </Tip>
      )}
    </div>
  );

  return (
    <li>
      <ContextMenu.Root onOpenChange={(open) => { contextOpen.current = open; }}>
        <ContextMenu.Trigger render={<div />}>{row}</ContextMenu.Trigger>
        <ContextMenu.Content>
          <ContextMenu.Actions items={items} />
        </ContextMenu.Content>
      </ContextMenu.Root>
    </li>
  );
});

/* ---------- The Settled shelf (archived Tasks are in Settings → Archived) ---------- */

/** Settled shows its latest this many rows until "Show all". */
const SHELF_CAP = 10;

/** A section's rows on screen: the first `SHELF_CAP` unless `all`, plus the selected one when it is older. */
function capRows(rows: ListRow[], all: boolean, selectedKey: string | null): ListRow[] {
  if (all || rows.length <= SHELF_CAP) return rows;
  const head = rows.slice(0, SHELF_CAP);
  const picked = rows.find((r) => r.key === selectedKey);
  return picked && !head.includes(picked) ? [...head, picked] : head;
}

/** The keys of a section's rows on screen, in order: its header (`shelf:<label>`), then its Tasks while open (and `more:<label>` past the cap), else the selected one pinned below it. */
function shelfKeys(label: string, rows: ListRow[], open: boolean, selectedKey: string | null, all: boolean): string[] {
  if (rows.length === 0) return [];
  return [`shelf:${label}`, ...capRows(rows, all, selectedKey).filter((r) => open || r.key === selectedKey).map((r) => r.key), ...(open && rows.length > SHELF_CAP ? [`more:${label}`] : [])];
}

/** Settled, under the Task list's scroller: its open rows scroll on their own within 40% of the screen, so the Task list keeps the rest. */
function Shelf({ label, rows, selectedKey, open, onToggle, tabStop, render, all, onAll }: Readonly<{ label: string; rows: ListRow[]; selectedKey: string | null; open: boolean; onToggle: () => void; /** The key of the row holding the list's tab stop. */ tabStop?: string; /** Every row, not only the latest SHELF_CAP. */ all: boolean; onAll: () => void; render: (row: ListRow, compact: boolean) => ReactNode }>) {
  if (rows.length === 0) return null;
  const pinned = !open ? rows.find((r) => r.key === selectedKey) : undefined;
  const shown = capRows(rows, all, selectedKey);
  return (
    <>
      <button
        type="button"
        data-nav={`shelf:${label}`}
        tabIndex={tabStop === `shelf:${label}` ? 0 : -1}
        aria-expanded={open}
        className="mt-1 flex h-7 w-full shrink-0 items-center gap-2 rounded-sm px-2 text-caption text-muted transition-colors hover:bg-tint-hover hover:text-body focus-visible:-outline-offset-2 pointer-coarse:h-11"
        onClick={onToggle}
      >
        <span className="whitespace-nowrap">
          {label} <span className="tabular-nums text-muted">{rows.length}</span>
        </span>
        <span className="fade-rule flex-1" aria-hidden="true" />
        <ChevronRight aria-hidden="true" className={cn('size-3.5 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
      </button>
      <Collapse open={open}>
        <div className="max-h-[40dvh] overflow-y-auto overscroll-contain">
          <ul className="flex flex-col gap-px pt-px">
            {/* The pinned copy below owns the selected row while the shelf is closed. */}
            {shown.filter((r) => r !== pinned).map((r) => render(r, true))}
          </ul>
          {rows.length > SHELF_CAP && (
            <button type="button" data-nav={`more:${label}`} tabIndex={tabStop === `more:${label}` ? 0 : -1} className="mt-px flex h-7 w-full items-center rounded-sm px-2 text-caption text-muted transition-colors hover:bg-tint-hover hover:text-body focus-visible:-outline-offset-2 pointer-coarse:h-11" onClick={onAll}>
              {all ? 'Show fewer' : `Show all ${rows.length}`}
            </button>
          )}
        </div>
      </Collapse>
      {pinned && (
        <ul className="flex flex-col gap-px">
          {render(pinned, true)}
        </ul>
      )}
    </>
  );
}

/** A Task in the list: keyed by its ID, or under federation by its machine and ID (IDs collide across machines). */
interface ListRow {
  key: string;
  task: SessionSummary;
  project: Project;
  machine?: Machine;
}

type TaskGroup = keyof ReturnType<typeof groupTasks>;
type Placement = readonly [group: TaskGroup, order: string];
const samePlacement = (a: Placement | undefined, b: Placement) => a?.[0] === b[0] && a[1] === b[1];

/** Defer only automatic shelf placement. Rows always render the latest Task and action eligibility. */
function useSidebarGroups(rows: ListRow[], list: RefObject<HTMLDivElement | null>) {
  const rowOf = new Map(rows.map((row) => [row.task, row]));
  const desired = new Map<string, Placement>();
  for (const [group, tasks] of Object.entries(groupTasks(rows.map((row) => row.task)))) {
    for (const task of tasks) desired.set(rowOf.get(task)!.key, [group as TaskGroup, task.settled_at ?? '']);
  }
  const [placements, setPlacements] = useState(desired);
  const shown = new Map<string, Placement>();
  for (const row of rows) {
    const target = desired.get(row.key)!;
    const automatic = target[0] === 'settled' && !!row.task.settled_by;
    shown.set(row.key, automatic ? placements.get(row.key) ?? target : target);
  }
  // New Tasks, resumed work, explicit stage changes and removed sources take effect in this render.
  if (shown.size !== placements.size || [...shown].some(([key, value]) => !samePlacement(placements.get(key), value))) setPlacements(shown);
  // Task text/usage updates do not restart the quiet period; only a new placement target does.
  const pending = JSON.stringify([...desired].filter(([key, value]) => !samePlacement(shown.get(key), value)));
  useEffect(() => {
    const changes = JSON.parse(pending) as [string, Placement][];
    const node = list.current;
    if (!changes.length || !node) return;
    let timer: ReturnType<typeof setTimeout>;
    const schedule = () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        // A stationary pointer or keyboard focus still owns this list. Leaving it starts a fresh quiet period.
        if (node.matches(':hover') || node.contains(node.ownerDocument.activeElement)) return;
        setPlacements((current) => {
          const next = new Map(current);
          for (const [key, value] of changes) if (next.has(key)) next.set(key, value);
          return next;
        });
      }, 5000);
    };
    const events = ['pointerenter', 'pointerleave', 'pointermove', 'pointerdown', 'pointerup', 'focusin', 'focusout', 'keydown', 'scroll'] as const;
    for (const event of events) node.addEventListener(event, schedule, { passive: true, capture: true });
    schedule();
    return () => {
      clearTimeout(timer);
      for (const event of events) node.removeEventListener(event, schedule, true);
    };
  }, [pending, list, placements]);
  const grouped: Record<TaskGroup, ListRow[]> = { active: [], settled: [], archived: [] };
  for (const row of rows) grouped[shown.get(row.key)![0]].push(row);
  grouped.settled.sort((a, b) => shown.get(b.key)![1].localeCompare(shown.get(a.key)![1]));
  return grouped;
}

const machineLabelId = (machine: Machine) => `uam-task-source-${machine.id || 'home'}`;

/* ---------- Sidebar ---------- */

export const Sidebar = memo(function Sidebar({
  loaded,
  projects,
  sessions,
  selectedId,
  hasNews,
  actions,
  connection,
  version,
  machineActions,
  count = 0,
}: {
  /** False until the first snapshot: the list is a skeleton, never "No projects yet". */
  loaded: boolean;
  projects: Project[];
  sessions: SessionSummary[];
  selectedId: string | null;
  hasNews: Unread;
  actions: WorkspaceActions;
  connection: Connection;
  version?: string;
  /** Under federation, the row actions of each machine not on screen, which run against that machine. */
  machineActions?: ReadonlyMap<string, TaskActions>;
  /** Tasks that need you (the collapsed rail's count), shown beside the header's wordmark. */
  count?: number;
}) {
  const federation = useFederation();
  // With connected instances, every machine's Tasks in one list; the machine on screen is this App's own state.
  const machines = federation?.machines;
  const carry = machines ? federation.carry : undefined;
  const minute = useMinuteTick();
  const [query, setQueryState] = useState(() => carry?.read().query ?? '');
  const setQuery = (value: string) => {
    setQueryState(value);
    carry?.write({ query: value });
  };
  const [shelves, setShelves] = useState<Record<string, boolean>>(readShelves);
  const list = useRef<HTMLDivElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const search = useRef<HTMLInputElement>(null);
  // Under federation the filter names an instance, or a Project on one; one that no longer exists counts as none.
  const machineFilter = machines ? federation.filter ?? null : null;
  const filterMachine = scopedMachine(federation);
  const chosen = machines ? filteredProject(filterMachine ? (filterMachine.active ? projects : filterMachine.state.projects) : [], machineFilter?.project ?? null) : filteredProject(projects, actions.filter);
  const rows = useMemo<ListRow[]>(() => {
    if (!machines) {
      const projectMap = new Map(projects.map((project) => [project.id, project]));
      return sidebarTasks(projects, sessions, actions.filter, query).map((task) => ({ key: task.id, task, project: projectMap.get(task.project_id)! }));
    }
    return machines.flatMap((m) => {
      if (filterMachine && filterMachine !== m) return [];
      const own = m.active ? { projects, sessions } : m.state;
      const projectMap = new Map(own.projects.map((project) => [project.id, project]));
      return sidebarTasks(own.projects, own.sessions, chosen ? chosen.id : null, query).map((task) => ({ key: encodeEntity(m.id || null, task.id), task, project: projectMap.get(task.project_id)!, machine: m }));
    }).sort((a, b) => b.task.created_at.localeCompare(a.task.created_at));
  }, [machines, projects, sessions, actions.filter, query, chosen, filterMachine]);
  const { active, settled } = useSidebarGroups(rows, list);
  // Newest first, whatever their state: a row never moves because its Task changed.
  const unsettled = active;
  const here = machines?.find((m) => m.active);
  const selectedKey = selectedId && here ? encodeEntity(here.id || null, selectedId) : selectedId;
  const shelfScope = chosen ? (filterMachine ? encodeEntity(filterMachine.id || null, chosen.id) : chosen.id) : 'all';
  const settledOpen = !!shelves[`${shelfScope}:settled`];
  const [settledAll, setSettledAll] = useState(false);
  // The row holding the list's one tab stop: the last focused while it is on screen, else the open Task's, else the first.
  const [focused, setFocused] = useState<string | null>(null);
  const searching = !!query.trim();
  const keys = searching ? rows.map((r) => r.key) : [...unsettled.map((r) => r.key), ...shelfKeys('Settled', settled, settledOpen, selectedKey, settledAll)];
  const tabStop = [focused, selectedKey].find((key) => key && keys.includes(key)) ?? keys[0];
  const anyProject = useAnyProject(projects);
  const oneInstance = new Set(rows.map((r) => r.machine?.id)).size < 2;
  const renderRow = (r: ListRow, compact = false) => {
    // Inactive machines keep their own read marks; the active one follows App's open Task.
    const reader = r.machine && !r.machine.active ? r.machine.hasNews : hasNews;
    const row = <TaskRow project={r.project} key={r.key} rowKey={r.key} machine={r.machine} named={!filterMachine} oneInstance={oneInstance} session={r.task} selected={r.key === selectedKey} unread={reader(r.task)} attention={needsYouNow(r.task, reader)} minute={minute} compact={compact} tabStop={r.key === tabStop} />;
    // Another machine's row runs its menu, Settle and rename against that machine.
    const remote = r.machine && !r.machine.active ? machineActions?.get(r.machine.id) : undefined;
    return remote ? <TaskActionsContext.Provider key={r.key} value={remote}>{row}</TaskActionsContext.Provider> : row;
  };
  const toggleSettled = () =>
    setShelves((s) => {
      const next = { ...s, [`${shelfScope}:settled`]: !settledOpen };
      localStorage.setItem(SHELVES_KEY, JSON.stringify(next));
      return next;
    });

  // Keep the selected row in view when the selection changes from outside the list.
  useEffect(() => {
    if (!selectedKey) return;
    list.current?.querySelector<HTMLElement>(`[data-task-row="${CSS.escape(selectedKey)}"]`)?.scrollIntoView({ block: 'nearest' });
  }, [selectedKey]);
  // Switching the machine on screen keeps the list where it was.
  useLayoutEffect(() => {
    if (carry && scroller.current) scroller.current.scrollTop = carry.read().scroll;
  }, [carry]);

  const conn = CONNECTION_TONE[connection];

  // The list: a skeleton before the first snapshot, the invitation without a Project, the search's matches, or the Tasks with Settled under them.
  let body: ReactNode;
  let shelvesBody: ReactNode = null;
  if (!loaded) {
    body = <Skeleton label="Loading tasks…" rows={5} className="gap-1" rowClassName="h-14 w-full rounded-md" />;
  } else if (!anyProject) {
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
        <p className="px-2 py-2 text-caption text-muted" role="status">{rows.length === 0 ? 'No matching tasks' : `${rows.length} matching ${rows.length === 1 ? 'task' : 'tasks'}`}</p>
        {rows.length === 0 && (
          <div className="flex flex-col items-start gap-2 px-2">
            <p className="text-caption text-muted">{`Searches names, projects, folders, branches${chosen ? ` in ${chosen.name}` : ''}.`}</p>
            <Button variant="secondary" size="sm" onClick={() => { setQuery(''); search.current?.focus(); }}>
              Clear search
            </Button>
          </div>
        )}
        <ul className="flex flex-col gap-1 animate-fade-in">{rows.map((r) => renderRow(r))}</ul>
      </>
    );
  } else {
    body = (
      <>
        <ul aria-label="Unsettled tasks" className="flex flex-col gap-1 animate-fade-in">
          {unsettled.map((r) => renderRow(r))}
        </ul>
        {active.length === 0 && <p className="px-2 py-3 text-caption text-muted">No active tasks.</p>}
      </>
    );
    // Settled sits at the foot, outside the Task list's scroller, so opening it never pushes the Tasks off screen.
    shelvesBody = (
      <div className="flex shrink-0 flex-col px-2">
        <Shelf label="Settled" rows={settled} selectedKey={selectedKey} open={settledOpen} onToggle={toggleSettled} tabStop={tabStop} render={renderRow} all={settledAll} onAll={() => setSettledAll((v) => !v)} />
      </div>
    );
  }

  return (
    <nav aria-label="Tasks" className="flex h-full min-h-0 flex-col bg-rail text-body">
      <header className="flex h-header shrink-0 items-center gap-0.5 px-2 max-sm:h-auto max-sm:flex-wrap max-sm:py-1">
        <SidebarToggle id="sidebar-hide" wordmark open={actions.sidebarOpen} count={count} onToggle={actions.onToggleSidebar} className={machines ? "max-sm:[&>span>span]:hidden" : undefined} />
        {/* Search uses the spare header width, or its own full row on a phone. */}
        <label className="flex min-w-0 flex-1 items-center gap-1.5 rounded-sm px-1 text-muted max-sm:order-last max-sm:basis-full">
          <Search aria-hidden="true" className="size-3.5 shrink-0" />
          <input ref={search} type="search" aria-label="Search tasks" placeholder="Search" value={query} onChange={(e) => setQuery(e.target.value)} className="h-8 min-w-0 w-full bg-transparent text-ui outline-none placeholder:text-muted pointer-coarse:h-11" />
        </label>
        {machines && <InstanceSwitcher place="sidebar" live={{ sessions, hasNews }} settingsOpen={actions.settingsOpen} onConnect={() => actions.onSettings('connections')} />}
        {anyProject && <FilterButton projects={projects} actions={actions} />}
        {projects.length > 0 && <RoutinesButton actions={actions} />}
        <AddProjectButton actions={actions} />
        {anyProject && <NewTaskButton actions={actions} id="new-task" />}
      </header>
      {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions -- the rows are buttons; this only relays arrow keys between them. */}
      <div ref={list} className="flex min-h-0 flex-1 flex-col" aria-busy={!loaded || undefined} onKeyDown={(e) => onListKeyDown(e)} onFocus={(e) => { const key = (e.target as HTMLElement).dataset.nav; if (key) setFocused(key); }}>
        <div ref={scroller} className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain px-2 pt-1 pb-3" onScroll={carry && ((e) => carry.write({ scroll: e.currentTarget.scrollTop }))}>
          {/* Each row names its machine to assistive technology by reference. */}
          {machines?.map((m) => <span key={m.id} id={machineLabelId(m)} className="sr-only">{m.label}{m.status.status === 'account-mismatch' ? ', unavailable: linked to a different Copilot account' : ''}</span>)}
          {body}
        </div>
        {shelvesBody}
      </div>

      {connection !== 'connected' && (
        <output className={cn('mx-2 mb-2 flex items-start gap-2 rounded-sm px-2 py-1.5 text-caption', connection === 'offline' ? 'bg-error-wash text-error' : 'bg-warning-wash text-warning')}>
          <Dot tone={conn} className="mt-1.5" />
          {CONNECTION_TEXT[connection]}
        </output>
      )}
      {/* The foot, one row: Settings as an icon with a tip, the account allowance as a small chip, then the version. A lost connection is the banner above; while connected the status is for screen readers only. */}
      <footer className="flex shrink-0 flex-col gap-1 px-2 pb-2">
        <div className="fade-rule mx-1 mb-1" aria-hidden="true" />
        <div className="flex min-w-0 items-center gap-1">
          <SettingsButton actions={actions} />
          <UsageButton variant="chip" side="top" className="ml-1" onAddPrices={() => actions.onSettings('token-prices')} />
          <span className="flex-1" />
          {version && <VersionMeta version={version} />}
          {connection === 'connected' && <output className="sr-only">{CONNECTION_TEXT.connected}</output>}
        </div>
      </footer>
    </nav>
  );
});
