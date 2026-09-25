import { ChevronRight, FolderPlus, GitBranch, LogOut, Settings as SettingsIcon, Search, SquarePen } from 'lucide-react';
import { ViewTransition, memo, useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { LIVE, needsYou, readOnly, taskName, type Project, type SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { filteredProject, groupTasks, sidebarTasks } from '../lib/tasks';
import type { Connection } from '../state';
import { Dot, InlineName, ProjectBadge, STATE_LABELS, STATE_TONE, StateMark, TONE_TEXT, TaskTitle, relTime, useApp, useMinuteTick } from './common';
import { ProjectFilterPicker } from './ProjectPicker';
import { canRename, taskMenuItems, useTaskActions } from './taskActions';
import { Button } from './ui/button';
import { Collapse } from './ui/collapse';
import { ContextMenu } from './ui/menu';
import { Tip } from './ui/tooltip';
import copilotIcon from '../assets/copilot.svg';

/** Project-level navigation and actions. */
export interface WorkspaceActions {
  /** Opens the New task palette (or the draft at once when there is one Project). */
  onNewTask: () => void;
  onAddProject: () => void;
  /** Edit project: the one place for a Project's name, defaults, previous sessions and removal. */
  onEditProject: (p: Project) => void;
  /** The Project the sidebar is filtered to; null shows every Project. Remembered per browser. */
  filter: string | null;
  onFilter: (id: string | null) => void;
  /** Whether the sidebar (or, on a narrow screen, the drawer) is showing. */
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
  settingsOpen: boolean;
  onSettings: () => void;
}

/**
 * The sidebar toggle (Ctrl/Cmd+B). In the sidebar header it hides the sidebar; at the start
 * of the main pane's header, once hidden, it brings it back. On a narrow screen it opens
 * and closes the drawer instead.
 */
export function SidebarToggle({ id, open, onToggle, size = 'icon', wordmark = false, className }: { id?: string; open: boolean; onToggle: () => void; size?: 'icon' | 'icon-md'; wordmark?: boolean; className?: string }) {
  const label = open ? 'Hide sidebar' : 'Show sidebar';
  return (
    <Tip
      label={
        <>
          {label}
          <span className="block text-on-primary/70">Ctrl+B</span>
        </>
      }
    >
      <Button id={id} size={wordmark ? 'md' : size} aria-label={label} aria-expanded={open} aria-keyshortcuts="Control+B Meta+B" className={cn('[&_svg]:size-4', wordmark && 'px-1', className)} onClick={onToggle}>
        <Brand markOnly={!wordmark} />
      </Button>
    </Tip>
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
export function Brand({ className, markOnly = false }: { className?: string; markOnly?: boolean }) {
  return (
    <span className={cn('inline-flex items-center gap-2', className)}>
      <svg aria-hidden="true" viewBox="0 0 20 20" className="size-4 shrink-0">
        <rect x="1" y="1" width="18" height="18" rx="5" className="fill-ink" />
        <rect x="4.5" y="5" width="4.5" height="10" rx="1.2" className="fill-raised" />
        <rect x="11" y="5" width="4.5" height="6" rx="1.2" className="fill-raised" />
        <rect x="11" y="12.5" width="4.5" height="2.5" rx="1" className="fill-raised opacity-60" />
      </svg>
      {!markOnly && <span className="text-title font-semibold text-ink">uam</span>}
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
  const i = rows.indexOf(target.closest<HTMLElement>('[data-nav]') as HTMLElement);
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

/** Right-slot text: the header's status word for act-now, in-motion, broken and unread rows; the relative time otherwise. */
function rowMeta(s: SessionSummary, unread: boolean): { text: string; tone: string } {
  if (readOnly(s)) return { text: relTime(s.updated_at), tone: 'text-muted' };
  const tone = STATE_TONE[s.state];
  if (needsYou(s) || LIVE.includes(s.state) || s.state === 'failed' || s.state === 'interrupted' || unread) {
    return { text: STATE_LABELS[s.state], tone: TONE_TEXT[tone] };
  }
  return { text: relTime(s.updated_at), tone: 'text-muted' };
}

/** Rows enter, leave and move with a view transition when the Task list changes (type "sessions"); every other render, the selection included, leaves them to their CSS transitions. */
const ROW_TRANSITION = { sessions: 'vt-row', default: 'none' } as const;
const ROW_ENTER = { sessions: 'vt-row-enter', default: 'none' } as const;
const ROW_EXIT = { sessions: 'vt-row-exit', default: 'none' } as const;

function TaskRow({ session: s, project, selected }: { session: SessionSummary; project: Project; selected: boolean }) {
  const { hasNews } = useApp();
  const a = useTaskActions();
  // A touch release after opening the context menu must not select the Task and close the drawer.
  const contextOpen = useRef(false);
  const unread = hasNews(s);
  const attention = needsYou(s) && !readOnly(s);
  const strong = selected || attention || unread;
  const items = taskMenuItems(s, a, 'row');
  const renaming = a.renaming?.id === s.id && a.renaming.place === 'row';
  const meta = rowMeta(s, unread);
  // One class string for the button and for the plain container that replaces it while renaming, so the swap never shifts layout.
  const rowClass = cn(
    'flex min-h-14 w-full flex-col justify-center gap-1 rounded-md bg-raised px-2.5 py-2 text-left text-caption transition-colors duration-100 focus-visible:-outline-offset-2',
    selected ? 'bg-tint-selected text-ink' : 'hover:bg-tint-hover',
    readOnly(s) && !selected && 'text-muted',
    strong ? 'font-medium text-ink' : 'text-body',
  );

  const row = (
    <div data-task-row={s.id}>
      {renaming ? (
        // Not a button while the input is inside: interactive content cannot nest in one.
        <div className={rowClass}>
          <span className="flex w-full min-w-0 items-center gap-1.5 text-meta text-muted"><ProjectBadge badge={project.badge} /><span className="truncate text-caption" title={project.name}>{project.name}</span></span>
          <InlineName initial={s.name} onSave={(v) => void a.rename(s.id, v)} onCancel={a.cancelRename} className="h-7 w-full" label="Task name" />
        </div>
      ) : (
        <button
          type="button"
          data-nav=""
          aria-current={selected ? 'true' : undefined}
          title={taskName(s) || 'New task'}
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
          <span className="flex w-full min-w-0 items-center gap-1.5 text-meta font-normal text-muted">
            <ProjectBadge badge={project.badge} />
            <span className={cn('min-w-0 flex-1 truncate text-caption', selected && 'font-semibold')} title={project.dir}>{project.name}</span>
            {project.branch && <span className="flex min-w-0 max-w-[50%] items-center gap-1" title={`Project branch: ${project.branch}`}><GitBranch aria-hidden="true" className="size-3 shrink-0" /><span className="truncate">{project.branch}</span></span>}
            {readOnly(s) && <span className="shrink-0">{s.stage === 'archived' ? 'Archived' : 'Settled'}</span>}
          </span>
          <span className="flex w-full min-w-0 items-center gap-1.5">
            <span className={cn('flex shrink-0 items-center gap-1 text-caption font-normal tabular-nums whitespace-nowrap transition-colors duration-160', meta.tone)}>
              {(needsYou(s) || LIVE.includes(s.state)) && <StateMark state={s.state} />}
              {meta.text}
            </span>
            <TaskTitle session={s} className={cn('min-w-0 flex-1 truncate text-ui', selected && 'font-semibold')} />
            {s.provider && <span className="shrink-0 text-meta font-normal text-muted" title={s.provider === 'copilot' ? 'GitHub Copilot' : s.provider}>
              {s.provider === 'copilot' ? <img src={copilotIcon} alt="GitHub Copilot" className="size-3.5 opacity-70" /> : s.provider}
            </span>}
          </span>
        </button>
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

function Shelf({ projects, label, tasks, selectedId, open, onToggle }: { projects: ReadonlyMap<string, Project>; label: string; tasks: SessionSummary[]; selectedId: string | null; open: boolean; onToggle: () => void }) {
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
        <span className="h-px flex-1 bg-hairline" aria-hidden="true" />
        <ChevronRight aria-hidden="true" className={cn('size-3.5 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
      </button>
      <Collapse open={open}>
        <ul className="flex flex-col gap-px pt-px">
          {/* The pinned copy below owns the selected row while the shelf is closed: one view-transition name each. */}
          {tasks.filter((t) => t !== pinned).map((t) => (
            <TaskRow project={projects.get(t.project_id)!} key={t.id} session={t} selected={t.id === selectedId} />
          ))}
        </ul>
      </Collapse>
      {pinned && (
        <ul className="flex flex-col gap-px">
          <TaskRow project={projects.get(pinned.project_id)!} session={pinned} selected />
        </ul>
      )}
    </div>
  );
}

/* ---------- Sidebar ---------- */

export const Sidebar = memo(function Sidebar({
  projects,
  sessions,
  selectedId,
  actions,
  authRequired,
  onLogout,
  connection,
  version,
}: {
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
  const [query, setQuery] = useState('');
  const [shelves, setShelves] = useState<Record<string, boolean>>(readShelves);
  const list = useRef<HTMLDivElement>(null);
  const chosen = filteredProject(projects, actions.filter);
  const projectMap = useMemo(() => new Map(projects.map((project) => [project.id, project])), [projects]);
  const tasks = useMemo(() => sidebarTasks(projects, sessions, actions.filter, query), [projects, sessions, actions.filter, query]);
  const { active, settled, archived } = groupTasks(tasks);
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

  return (
    <nav aria-label="Tasks" className="flex h-full min-h-0 flex-col bg-rail text-body">
      <header className="flex h-header shrink-0 items-center gap-0.5 px-2">
        <label className="flex min-w-0 flex-1 items-center gap-1.5 rounded-sm px-1 text-muted focus-within:bg-raised focus-within:outline-2 focus-within:outline-focus">
          <Search aria-hidden="true" className="size-3.5 shrink-0" />
          <input type="search" aria-label="Search tasks" placeholder="Search" value={query} onChange={(e) => setQuery(e.target.value)} className="h-8 min-w-0 w-full bg-transparent text-ui outline-none placeholder:text-muted pointer-coarse:h-11" />
        </label>
        <SidebarToggle id="sidebar-hide" open={actions.sidebarOpen} onToggle={actions.onToggleSidebar} />
        {projects.length > 0 && <ProjectFilterPicker projects={projects} filter={actions.filter} onFilter={actions.onFilter} onEdit={actions.onEditProject} />}
        <Tip label="Add project">
          <Button size="icon" aria-label="Add project" className="text-muted" onClick={actions.onAddProject}>
            <FolderPlus />
          </Button>
        </Tip>
        {projects.length > 0 && (
          <Tip
            label={
              <>
                New task
                <span className="block text-on-primary/70">Alt+N</span>
              </>
            }
          >
            <Button id="new-task" size="icon" aria-label="New task" aria-keyshortcuts="Alt+N" className="text-muted" onClick={actions.onNewTask}>
              <SquarePen />
            </Button>
          </Tip>
        )}
      </header>

      {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions -- the rows are buttons; this only relays arrow keys between them. */}
      <div ref={list} className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pt-1 pb-3" onKeyDown={(e) => onListKeyDown(e)}>
        {projects.length === 0 ? (
          <div className="flex flex-col items-start gap-3 px-2 pt-6">
            <p className="text-ui text-muted">No projects yet. A project is a directory on this host.</p>
            <Button variant="secondary" size="sm" onClick={actions.onAddProject}>
              <FolderPlus />
              Add project
            </Button>
          </div>
        ) : query.trim() ? (
          <>
            <p className="px-2 py-2 text-caption text-muted" role="status">{tasks.length} matching tasks</p>
            <ul className="flex flex-col gap-1 animate-fade-in">{tasks.map((t) => <TaskRow project={projectMap.get(t.project_id)!} key={t.id} session={t} selected={t.id === selectedId} />)}</ul>
          </>
        ) : (
          <>
            <ul className="flex flex-col gap-1 animate-fade-in">{active.map((t) => <TaskRow project={projectMap.get(t.project_id)!} key={t.id} session={t} selected={t.id === selectedId} />)}</ul>
            {active.length === 0 && <p className="px-2 py-3 text-caption text-muted">No active tasks.</p>}
            <Shelf projects={projectMap} label="Settled" tasks={settled} selectedId={selectedId} open={!!shelves[`${shelfScope}:settled`]} onToggle={() => toggleShelf(`${shelfScope}:settled`)} />
            <Shelf projects={projectMap} label="Archived" tasks={archived} selectedId={selectedId} open={!!shelves[`${shelfScope}:archived`]} onToggle={() => toggleShelf(`${shelfScope}:archived`)} />
          </>
        )}
      </div>

      {connection !== 'connected' && (
        <p role="status" className={cn('mx-2 mb-2 flex items-start gap-2 rounded-sm px-2 py-1.5 text-caption', connection === 'offline' ? 'bg-error-wash text-error' : 'bg-warning-wash text-warning')}>
          <Dot tone={conn} pulse className="mt-1.5" />
          {CONNECTION_TEXT[connection]}
        </p>
      )}
      <footer className="flex min-h-9 shrink-0 items-center gap-2 px-3 text-caption text-muted">
        <Tip label="Settings">
          <Button size="icon" aria-label="Settings" aria-pressed={actions.settingsOpen} className="-ml-1.5 text-muted" onClick={actions.onSettings}>
            <SettingsIcon />
          </Button>
        </Tip>
        <Tip label={connection === 'connected' ? 'Connected' : CONNECTION_TEXT[connection]}>
          <span role="status" className="flex size-7 items-center justify-center">
            <Dot tone={conn} pulse={connection !== 'connected'} />
            <span className="sr-only">{CONNECTION_TEXT[connection]}</span>
          </span>
        </Tip>
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
