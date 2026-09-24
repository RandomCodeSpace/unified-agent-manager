import { ChevronRight, Ellipsis, FolderMinus, FolderPlus, GitBranch, LogOut, Plus, Settings2, SquarePen } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { LIVE, needsYou, readOnly, type Project, type SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { groupTasks, mostRecentProject, tasksOf } from '../lib/tasks';
import type { Connection } from '../state';
import { Dot, InlineName, STATE_LABELS, STATE_TONE, Sep, StateMark, TONE_TEXT, TaskTitle, relTime, useApp, useMinuteTick } from './common';
import { taskMenuItems, useTaskActions } from './taskActions';
import { Button } from './ui/button';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

/** Project-level navigation and actions. */
export interface WorkspaceActions {
  onHome: () => void;
  onNewTask: (projectId: string) => void;
  onAddProject: () => void;
  onEditProject: (p: Project) => void;
  onRemoveProject: (p: Project) => void;
  /** UI-local collapsed project groups. */
  collapsed: ReadonlySet<string>;
  onToggleProject: (id: string) => void;
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
      <svg aria-hidden="true" viewBox="0 0 20 20" className="size-5 shrink-0">
        <rect x="1" y="1" width="18" height="18" rx="5" className="fill-ink" />
        <rect x="4.5" y="5" width="4.5" height="10" rx="1.2" className="fill-raised" />
        <rect x="11" y="5" width="4.5" height="6" rx="1.2" className="fill-raised" />
        <rect x="11" y="12.5" width="4.5" height="2.5" rx="1" className="fill-raised opacity-60" />
      </svg>
      {!markOnly && <span className="text-title font-semibold tracking-[-0.01em] text-ink">uam</span>}
    </span>
  );
}

/* ---------- Keyboard navigation ---------- */

const NAV = '[data-nav]:not([disabled])';

/** Arrow keys move between rows; Home/End jump; Left/Right collapse or expand a project. */
function onListKeyDown(e: KeyboardEvent<HTMLElement>, actions: WorkspaceActions) {
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
    case 'ArrowRight':
    case 'ArrowLeft': {
      const project = target.dataset.project;
      if (!project) return;
      const open = target.getAttribute('aria-expanded') === 'true';
      if ((e.key === 'ArrowRight') !== open) {
        actions.onToggleProject(project);
        e.preventDefault();
      } else if (e.key === 'ArrowRight') focus(i + 1);
    }
  }
}

/* ---------- Collapsible body with a height transition (grid rows, no measuring) ---------- */

function Collapsible({ open, children, className }: { open: boolean; children: ReactNode; className?: string }) {
  return (
    <div className={cn('grid transition-[grid-template-rows] duration-240 ease-app', open ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]', className)}>
      <div className="min-h-0 overflow-hidden" inert={!open} aria-hidden={!open}>
        {children}
      </div>
    </div>
  );
}

/* ---------- Task row ---------- */

/** Short status words for the row's right slot (T3 Code's vocabulary); the header chip carries the full label. */
const ROW_WORD: Partial<Record<SessionSummary['state'], string>> = {
  awaiting_permission: 'Approval',
  awaiting_answer: 'Input',
  working: 'Working',
  starting: 'Starting',
  failed: 'Failed',
  interrupted: 'Paused',
  completed: 'Done',
  cancelled: 'Stopped',
  closed: 'Closed',
};

/** Right-slot text: a status word for act-now, in-motion, broken and unread rows; the relative time otherwise. */
function rowMeta(s: SessionSummary, unread: boolean): { text: string; tone: string } {
  if (readOnly(s)) return { text: relTime(s.updated_at), tone: 'text-faint' };
  const tone = STATE_TONE[s.state];
  if (needsYou(s) || LIVE.includes(s.state) || s.state === 'failed' || s.state === 'interrupted' || unread) {
    return { text: ROW_WORD[s.state] ?? STATE_LABELS[s.state], tone: TONE_TEXT[tone] };
  }
  return { text: relTime(s.updated_at), tone: 'text-muted' };
}

function TaskRow({ session: s, selected }: { session: SessionSummary; selected: boolean }) {
  const { hasNews } = useApp();
  const a = useTaskActions();
  const [menuOpen, setMenuOpen] = useState(false);
  const unread = hasNews(s);
  const attention = needsYou(s) && !readOnly(s);
  const strong = selected || attention || unread;
  const items = taskMenuItems(s, a, 'row');
  const renaming = a.renaming?.id === s.id && a.renaming.place === 'row';
  const meta = rowMeta(s, unread);

  const row = (
    <div className={cn('group relative', menuOpen && 'is-open')} data-task-row={s.id}>
      <button
        type="button"
        data-nav=""
        aria-current={selected ? 'true' : undefined}
        className={cn(
          'grid h-8 w-full grid-cols-[16px_minmax(0,1fr)_auto] items-center gap-2 rounded-sm pr-2 pl-2 text-left text-ui transition-[background-color,color,box-shadow] duration-100 focus-visible:-outline-offset-2 pointer-coarse:h-11 pointer-coarse:pr-10',
          selected ? 'bg-raised text-ink shadow-[0_1px_2px_rgba(28,27,24,0.06)]' : 'hover:bg-canvas',
          menuOpen && !selected && 'bg-canvas',
          readOnly(s) && !selected && 'text-muted',
          strong ? 'font-medium text-ink' : 'text-body',
        )}
        onClick={() => a.select(s.id)}
        onDoubleClick={() => s.stage !== 'archived' && a.startRename(s.id, 'row')}
        onKeyDown={(e) => {
          if (e.key === 'F2' && s.stage !== 'archived') {
            e.preventDefault();
            a.startRename(s.id, 'row');
          }
        }}
      >
        <StateMark state={s.state} />
        <Sep />
        {renaming ? (
          <InlineName initial={s.name} onSave={(v) => void a.rename(s.id, v)} onCancel={a.cancelRename} className="col-span-2 h-6" label="Task name" />
        ) : (
          <>
            <TaskTitle session={s} className="truncate" />
            <Sep />
            <span
              className={cn(
                'text-caption tabular-nums whitespace-nowrap transition-opacity duration-100 group-hover:opacity-0 group-focus-within:opacity-0 group-[.is-open]:opacity-0 pointer-coarse:group-hover:opacity-100 pointer-coarse:group-focus-within:opacity-100',
                meta.tone,
              )}
            >
              {meta.text}
            </span>
          </>
        )}
      </button>
      {!renaming && (
        <Menu.Root open={menuOpen} onOpenChange={setMenuOpen} modal={false}>
          <Menu.Trigger
            render={
              <Button
                size="icon"
                variant={selected ? 'subtle' : 'ghost'}
                aria-label={`Actions for ${s.name || s.title || 'new task'}`}
                className={cn(
                  'absolute top-1/2 right-1 -translate-y-1/2 text-muted opacity-0 transition-opacity duration-100 group-hover:opacity-100 group-focus-within:opacity-100 data-open:opacity-100 focus-visible:opacity-100 pointer-coarse:opacity-100',
                )}
              />
            }
          >
            <Ellipsis />
          </Menu.Trigger>
          <Menu.Content align="start" side="right" sideOffset={6}>
            <Menu.Actions items={items} />
          </Menu.Content>
        </Menu.Root>
      )}
    </div>
  );

  return (
    <li>
      <ContextMenu.Root>
        <ContextMenu.Trigger render={<div />}>{row}</ContextMenu.Trigger>
        <ContextMenu.Content>
          <ContextMenu.Actions items={items} />
        </ContextMenu.Content>
      </ContextMenu.Root>
    </li>
  );
}

/* ---------- Shelf (Settled / Archived) ---------- */

function Shelf({ label, tasks, selectedId, open, onToggle }: { label: string; tasks: SessionSummary[]; selectedId: string | null; open: boolean; onToggle: () => void }) {
  if (tasks.length === 0) return null;
  const pinned = !open ? tasks.find((t) => t.id === selectedId) : undefined;
  return (
    <div className="mt-1">
      <button
        type="button"
        data-nav=""
        aria-expanded={open}
        className="flex h-7 w-full items-center gap-2 rounded-sm px-2 text-caption text-muted transition-colors hover:bg-canvas hover:text-body focus-visible:-outline-offset-2 pointer-coarse:h-11"
        onClick={onToggle}
      >
        <span className="whitespace-nowrap">
          {label} <span className="tabular-nums text-faint">{tasks.length}</span>
        </span>
        <span className="h-px flex-1 bg-hairline" aria-hidden="true" />
        <ChevronRight aria-hidden="true" className={cn('size-3.5 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
      </button>
      <Collapsible open={open}>
        <ul className="flex flex-col gap-px pt-px">
          {tasks.map((t) => (
            <TaskRow key={t.id} session={t} selected={t.id === selectedId} />
          ))}
        </ul>
      </Collapsible>
      {pinned && (
        <ul className="flex flex-col gap-px">
          <TaskRow session={pinned} selected />
        </ul>
      )}
    </div>
  );
}

/* ---------- Project group ---------- */

function ProjectGroup({
  project: p,
  tasks,
  selectedId,
  actions,
  shelves,
  onToggleShelf,
}: {
  project: Project;
  tasks: SessionSummary[];
  selectedId: string | null;
  actions: WorkspaceActions;
  shelves: Record<string, boolean>;
  onToggleShelf: (key: string) => void;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const open = !actions.collapsed.has(p.id);
  const { active, settled, archived } = groupTasks(tasks);
  const attention = active.filter(needsYou).length;
  const pinned = !open ? tasks.find((t) => t.id === selectedId) : undefined;
  const items: ActionItem[] = [
    { key: 'new', label: 'New task', icon: <SquarePen />, onSelect: () => actions.onNewTask(p.id) },
    { key: 'edit', label: 'Edit project', icon: <Settings2 />, onSelect: () => actions.onEditProject(p), separator: true },
    { key: 'remove', label: 'Remove project', icon: <FolderMinus />, danger: true, onSelect: () => actions.onRemoveProject(p) },
  ];

  return (
    <section aria-label={p.name} className="mb-2">
      <ContextMenu.Root>
        <ContextMenu.Trigger render={<div />}>
          <div className={cn('group relative', menuOpen && 'is-open')}>
            <button
              type="button"
              data-nav=""
              data-project={p.id}
              aria-expanded={open}
              className={cn(
                'flex w-full items-center gap-1.5 rounded-sm pr-16 pl-1 text-left transition-colors duration-100 hover:bg-canvas focus-visible:-outline-offset-2 pointer-coarse:pr-20',
                p.branch ? 'min-h-10 py-1 pointer-coarse:min-h-12' : 'h-8 pointer-coarse:h-11',
                menuOpen && 'bg-canvas',
              )}
              onClick={() => actions.onToggleProject(p.id)}
            >
              <span className="relative flex size-4 shrink-0 items-center justify-center">
                <ChevronRight
                  aria-hidden="true"
                  className={cn('size-3.5 text-faint transition-[transform,opacity] duration-160 ease-app', open && 'rotate-90', !open && attention > 0 && 'opacity-0 group-hover:opacity-100')}
                />
                {!open && attention > 0 && <Dot tone="attention" className="absolute transition-opacity duration-160 group-hover:opacity-0" />}
              </span>
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="truncate text-ui font-medium text-ink">{p.name}</span>
                {p.branch && (
                  <span className="flex min-w-0 items-center gap-1 text-caption text-muted" title={p.branch}>
                    <GitBranch aria-hidden="true" className="size-3 shrink-0 text-faint" />
                    <span className="truncate font-mono text-[11px]">{p.branch}</span>
                  </span>
                )}
              </span>
              {!open && (
                <span className="ml-auto text-caption tabular-nums text-faint">
                  {active.length}
                  <span className="sr-only"> active tasks</span>
                  {attention > 0 && <span className="sr-only">, {attention} need you</span>}
                </span>
              )}
            </button>
            <span className="absolute top-1/2 right-1 flex -translate-y-1/2 items-center gap-0.5 opacity-0 transition-opacity duration-100 group-hover:opacity-100 group-focus-within:opacity-100 group-[.is-open]:opacity-100 pointer-coarse:opacity-100">
              <Tip label="New task">
                <Button size="icon" aria-label={`New task in ${p.name}`} className="text-muted" onClick={() => actions.onNewTask(p.id)}>
                  <SquarePen />
                </Button>
              </Tip>
              <Menu.Root open={menuOpen} onOpenChange={setMenuOpen} modal={false}>
                <Menu.Trigger render={<Button size="icon" aria-label={`Project actions for ${p.name}`} className="text-muted data-open:opacity-100" />}>
                  <Ellipsis />
                </Menu.Trigger>
                <Menu.Content align="start" side="right" sideOffset={6}>
                  <Menu.Actions items={items} />
                </Menu.Content>
              </Menu.Root>
            </span>
          </div>
        </ContextMenu.Trigger>
        <ContextMenu.Content>
          <ContextMenu.Actions items={items} />
        </ContextMenu.Content>
      </ContextMenu.Root>

      <Collapsible open={open}>
        <div className="pt-px pl-2">
          <ul className="flex flex-col gap-px">
            {active.map((t) => (
              <TaskRow key={t.id} session={t} selected={t.id === selectedId} />
            ))}
            {active.length === 0 && (
              <li>
                <button
                  type="button"
                  data-nav=""
                  className="flex h-8 w-full items-center gap-2 rounded-sm px-2 text-ui text-muted transition-colors hover:bg-canvas hover:text-ink focus-visible:-outline-offset-2 pointer-coarse:h-11"
                  onClick={() => actions.onNewTask(p.id)}
                >
                  <Plus aria-hidden="true" className="size-4 text-faint" />
                  New task
                </button>
              </li>
            )}
          </ul>
          <Shelf label="Settled" tasks={settled} selectedId={selectedId} open={!!shelves[`${p.id}:settled`]} onToggle={() => onToggleShelf(`${p.id}:settled`)} />
          <Shelf label="Archived" tasks={archived} selectedId={selectedId} open={!!shelves[`${p.id}:archived`]} onToggle={() => onToggleShelf(`${p.id}:archived`)} />
        </div>
      </Collapsible>
      {pinned && (
        <ul className="pl-2">
          <TaskRow session={pinned} selected />
        </ul>
      )}
    </section>
  );
}

/* ---------- Sidebar ---------- */

export function Sidebar({
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
  const [shelves, setShelves] = useState<Record<string, boolean>>(readShelves);
  const list = useRef<HTMLDivElement>(null);
  const recent = useMemo(() => mostRecentProject(projects, sessions, selectedId), [projects, sessions, selectedId]);
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
    <nav aria-label="Projects" className="flex h-full min-h-0 flex-col bg-rail text-body">
      <header className="flex h-header shrink-0 items-center gap-0.5 pr-2 pl-3">
        <button type="button" className="mr-auto flex h-8 items-center rounded-sm pr-2 transition-colors hover:bg-canvas pointer-coarse:h-11" onClick={actions.onHome} aria-label="Home">
          <Brand />
        </button>
        <Tip label={connection === 'connected' ? 'Connected' : CONNECTION_TEXT[connection]}>
          <span role="status" className="flex size-7 items-center justify-center">
            <Dot tone={conn} pulse={connection !== 'connected'} />
            <span className="sr-only">{CONNECTION_TEXT[connection]}</span>
          </span>
        </Tip>
        {recent && (
          <Tip
            label={
              <>
                New task
                <span className="block text-on-primary/70">in {recent.name}</span>
              </>
            }
          >
            <Button size="icon" aria-label={`New task in ${recent.name}`} className="text-muted" onClick={() => actions.onNewTask(recent.id)}>
              <SquarePen />
            </Button>
          </Tip>
        )}
        <Tip label="Add project">
          <Button size="icon" aria-label="Add project" className="text-muted" onClick={actions.onAddProject}>
            <FolderPlus />
          </Button>
        </Tip>
      </header>

      {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions -- the rows are buttons; this only relays arrow keys between them. */}
      <div ref={list} className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pt-1 pb-3" onKeyDown={(e) => onListKeyDown(e, actions)}>
        {projects.length === 0 ? (
          <div className="flex flex-col items-start gap-3 px-2 pt-6">
            <p className="text-ui text-muted">No projects yet. A project is a directory on this host.</p>
            <Button variant="secondary" size="sm" onClick={actions.onAddProject}>
              <FolderPlus />
              Add project
            </Button>
          </div>
        ) : (
          projects.map((p) => (
            <ProjectGroup key={p.id} project={p} tasks={tasksOf(sessions, p.id)} selectedId={selectedId} actions={actions} shelves={shelves} onToggleShelf={toggleShelf} />
          ))
        )}
      </div>

      {connection !== 'connected' && (
        <p role="status" className={cn('mx-2 mb-2 flex items-start gap-2 rounded-sm px-2 py-1.5 text-caption', connection === 'offline' ? 'bg-error-wash text-error' : 'bg-warning-wash text-warning')}>
          <Dot tone={conn} pulse className="mt-1.5" />
          {CONNECTION_TEXT[connection]}
        </p>
      )}
      <footer className="flex h-9 shrink-0 items-center gap-2 px-3 text-caption text-faint">
        {version && <span className="truncate font-mono text-[11px]">{version}</span>}
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
}
