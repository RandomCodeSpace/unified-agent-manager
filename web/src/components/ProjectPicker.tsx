import { ArrowLeft, Check, ChevronDown, CircleDashed, Clock, KanbanSquare, Layers, Search, Settings } from 'lucide-react';
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react';
import type { Project, SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { filteredProject, newTaskProject, searchProjects } from '../lib/tasks';
import { ProjectBadge, Skeleton, useApp } from './common';
import { Key } from './InlinePicker';
import { Button } from './ui/button';
import { CommandDialog } from './ui/dialog';
import { itemClass, labelClass } from './ui/menu';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

/**
 * The Project lists a search box drives (T3 Code): the sidebar's filter dropdown (and the
 * Planner's Board picker, the same list) and the New task palette. The box keeps focus; the
 * highlight is virtual (`aria-activedescendant`), arrows move it, Enter picks it, Esc closes
 * the surface and a pointer over a row takes it.
 */

/** The highlighted index, clamped to the list; `initial` is used once, on mount. */
function useHighlight(count: number, initial: number, onPick: (index: number) => void) {
  const [active, setActive] = useState(initial);
  const at = Math.min(active, count - 1);
  const list = useRef<HTMLDivElement>(null);
  useEffect(() => {
    list.current?.querySelector('[data-highlighted]')?.scrollIntoView({ block: 'nearest' });
  }, [at]);
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.altKey || e.ctrlKey || e.metaKey) return;
    const move = (n: number) => {
      setActive(Math.max(0, Math.min(count - 1, n)));
      e.preventDefault();
    };
    if (e.key === 'ArrowDown') move(at + 1);
    else if (e.key === 'ArrowUp') move(at - 1);
    else if (e.key === 'Enter' && at >= 0) {
      e.preventDefault();
      onPick(at);
    }
  };
  return { active: at, setActive, list, onKeyDown };
}

const inputClass = 'h-8 min-w-0 flex-1 bg-transparent text-ui text-ink outline-none placeholder:text-muted';

/* ---------- Sidebar filter ---------- */

/**
 * The header's Project badge is the filter: the filtered Project's badge, or a layers glyph
 * for all of them. It opens a searchable list with All projects first; each Project row
 * carries a gear that opens Edit project once the list has closed, so focus returns to the badge.
 */
export function ProjectFilterPicker({ projects, filter, onFilter, onEdit, onRoutines, onPlan }: Readonly<{ projects: Project[]; filter: string | null; onFilter: (id: string | null) => void; onEdit: (p: Project) => void; onRoutines?: (p: Project) => void; onPlan?: (p: Project) => void }>) {
  const chosen = filteredProject(projects, filter);
  const [open, setOpen] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const pending = useRef<Project | null>(null);
  const planning = useRef<Project | null>(null);
  const scheduling = useRef<Project | null>(null);
  return (
    <Popover.Root
      open={open}
      onOpenChange={setOpen}
      onOpenChangeComplete={(o) => {
        if (o) return;
        const plan = planning.current;
        planning.current = null;
        if (plan) onPlan?.(plan);
        const routines = scheduling.current;
        scheduling.current = null;
        if (routines) onRoutines?.(routines);
        if (!pending.current) return;
        const p = pending.current;
        pending.current = null;
        onEdit(p);
      }}
    >
      <Tip label={chosen ? <>Project filter<span className="block text-on-primary/70">{chosen.name}</span></> : 'All projects'}>
        <Popover.Trigger render={<Button size="icon" aria-label={chosen ? `Project filter: ${chosen.name}` : 'Project filter: all projects'} className="text-muted" />}>
          {chosen ? <ProjectBadge badge={chosen.badge} /> : <Layers />}
        </Popover.Trigger>
      </Tip>
      <Popover.Content side="bottom" align="start" sideOffset={4} initialFocus={input} className="w-72 max-w-(--available-width) gap-0 p-1">
        <FilterList
          projects={projects}
          filter={chosen?.id ?? null}
          input={input}
          onPick={(id) => {
            onFilter(id);
            setOpen(false);
          }}
          onEdit={(p) => {
            pending.current = p;
            setOpen(false);
          }}
          onPlan={onPlan && ((p) => {
            planning.current = p;
            setOpen(false);
          })}
          onRoutines={onRoutines && ((p) => {
            scheduling.current = p;
            setOpen(false);
          })}
        />
      </Popover.Content>
    </Popover.Root>
  );
}

/* ---------- Planner Board picker ---------- */

/** Why a Project has no plan, in the words the Planner uses for it (§14 no_git). */
const noPlan = (p: Project) => {
  if (p.no_git === 'not_installed') return 'Git is not installed where uam can find it.';
  return p.no_git ? 'Not a git repository, so it has no plan.' : undefined;
};

/**
 * The Planner header's Board picker: the filter's list, opening below and start-aligned, with
 * every Project (one without git listed, disabled, with the reason) and Unassigned last with
 * its card count while it has cards or is shown. The trigger is the Board's badge, name and a chevron.
 */
export function PlannerProjectPicker({ projects, value, unassigned, onPick }: Readonly<{ projects: Project[]; value: string; unassigned: number; onPick: (id: string) => void }>) {
  const [open, setOpen] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const project = projects.find((p) => p.id === value);
  const name = project?.name ?? (value === 'unassigned' ? 'Unassigned' : 'Choose a project');
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger render={<Button size="md" aria-label={`Project: ${name}`} className="min-w-0 max-w-64 shrink px-2 sm:ml-1" />}>
        {project ? <ProjectBadge badge={project.badge} /> : <CircleDashed className="text-muted" />}
        {/* A phone's header has room for the badge and the chevron only; the label names the Board. */}
        <span className="min-w-0 truncate max-[480px]:hidden">{name}</span>
        <ChevronDown className="text-muted" />
      </Popover.Trigger>
      <Popover.Content side="bottom" align="start" sideOffset={4} initialFocus={input} className="w-72 max-w-(--available-width) gap-0 p-1">
        <FilterList
          projects={projects}
          filter={value}
          input={input}
          all={false}
          reason={noPlan}
          tail={unassigned || value === 'unassigned' ? { id: 'unassigned', label: 'Unassigned', icon: <CircleDashed />, count: unassigned } : undefined}
          onPick={(id) => {
            if (id) onPick(id);
            setOpen(false);
          }}
        />
      </Popover.Content>
    </Popover.Root>
  );
}

/** A row of the list: a Project, All projects (`id` null), or an extra entry after the Projects. */
interface FilterRow {
  id: string | null;
  label: string;
  project?: Project;
  icon?: ReactNode;
  /** Why the row cannot be picked; shown under its name, and the row is disabled. */
  reason?: string;
  count?: number;
}

/**
 * The searchable Project list. The sidebar filter heads it with All projects and gives each
 * Project its Plan and gear buttons; the Planner's picker lists no All projects, disables the
 * Projects `reason` names, and ends with its `tail` entry while that matches the search.
 */
function FilterList({ projects, filter, input, onPick, onEdit, onPlan, onRoutines, all = true, reason, tail }: Readonly<{
  projects: Project[];
  filter: string | null;
  input: RefObject<HTMLInputElement | null>;
  onPick: (id: string | null) => void;
  onEdit?: (p: Project) => void;
  onPlan?: (p: Project) => void;
  onRoutines?: (p: Project) => void;
  all?: boolean;
  reason?: (p: Project) => string | undefined;
  tail?: FilterRow;
}>) {
  const id = useId();
  const [query, setQuery] = useState('');
  const matches = useMemo(() => searchProjects(projects, query), [projects, query]);
  const q = query.trim().toLocaleLowerCase();
  // All projects heads the list unless a search is on.
  const rows: FilterRow[] = [
    ...(all && !q ? [{ id: null, label: 'All projects', icon: <Layers /> }] : []),
    ...matches.map((p) => ({ id: p.id, label: p.name, project: p, reason: reason?.(p) })),
    ...(tail && tail.label.toLocaleLowerCase().includes(q) ? [tail] : []),
  ];
  const { active, setActive, list, onKeyDown } = useHighlight(rows.length, 0, (i) => !rows[i].reason && onPick(rows[i].id));
  return (
    <>
      <label className="flex items-center gap-1.5 px-2 pb-1 text-muted">
        <Search aria-hidden="true" className="size-3.5 shrink-0" />
        <input
          ref={input}
          type="search"
          role="combobox"
          aria-label="Search projects"
          aria-autocomplete="list"
          aria-expanded="true"
          aria-controls={`${id}-list`}
          aria-activedescendant={active >= 0 ? `${id}-${active}` : undefined}
          placeholder="Search projects…"
          className={inputClass}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={onKeyDown}
        />
      </label>
      <div ref={list} role="listbox" id={`${id}-list`} aria-label="Projects" className="max-h-[min(60dvh,360px)] overflow-y-auto overscroll-contain pt-1">
        {rows.map((row, i) => {
          const p = row.project;
          const current = row.id === filter;
          const pick = () => !row.reason && onPick(row.id);
          return (
            // Not a button: the gear inside is one. The row is reached through the search box (`aria-activedescendant`), never focused itself.
            <div
              key={row.id ?? 'all'}
              role="option"
              tabIndex={-1}
              id={`${id}-${i}`}
              aria-selected={i === active}
              aria-current={current || undefined}
              aria-disabled={row.reason ? true : undefined}
              aria-label={row.count === undefined ? undefined : `${row.label}, ${row.count} ${row.count === 1 ? 'card' : 'cards'}`}
              data-highlighted={i === active ? '' : undefined}
              className={cn(itemClass, 'pr-1', current && 'text-ink', row.reason && 'items-start py-1.5')}
              onPointerMove={() => i !== active && setActive(i)}
              onClick={pick}
              onKeyDown={(e) => e.key === 'Enter' && e.target === e.currentTarget && pick()}
            >
              {/* A disabled row dims its badge and name; its reason stays readable in caption beneath (DESIGN.md menus). */}
              {p ? <ProjectBadge badge={p.badge} className={cn(row.reason && 'opacity-45')} /> : row.icon}
              <span className="flex min-w-0 flex-1 flex-col">
                <span className={cn('truncate', row.reason && 'opacity-45')}>{row.label}</span>
                {row.reason && <span className="text-caption text-muted">{row.reason}</span>}
              </span>
              {row.count !== undefined && <span className="text-caption tabular-nums text-muted">{row.count}</span>}
              {current && <Check aria-hidden="true" strokeWidth={2.5} className="!size-3.5 !text-accent" />}
              {p && onPlan && !p.no_git && (
                <Button
                  size="icon-sm"
                  aria-label={`Plan ${p.name}`}
                  title="Plan"
                  className="text-muted"
                  onClick={(e) => {
                    e.stopPropagation();
                    onPlan(p);
                  }}
                >
                  <KanbanSquare />
                </Button>
              )}
              {p && onRoutines && (
                <Button
                  size="icon-sm"
                  aria-label={`Routines of ${p.name}`}
                  title="Routines"
                  className="text-muted"
                  onClick={(e) => {
                    e.stopPropagation();
                    onRoutines(p);
                  }}
                >
                  <Clock />
                </Button>
              )}
              {p && onEdit && (
                <Button
                  size="icon-sm"
                  aria-label={`Edit ${p.name}`}
                  className="text-muted"
                  onClick={(e) => {
                    e.stopPropagation();
                    onEdit(p);
                  }}
                >
                  <Settings />
                </Button>
              )}
            </div>
          );
        })}
        {rows.length === 0 && <p role="status" className="px-2 py-2 text-caption text-muted">No project matches</p>}
      </div>
    </>
  );
}

/* ---------- New task palette ---------- */

const DIGIT = /^Digit([1-9])$/;

/**
 * New task: a command palette listing every Project with its directory; the filtered
 * Project (else the most recently active one) starts highlighted. Enter, a click or Alt+1…9
 * pick one; the draft opens once the palette has closed, and its composer keeps the focus.
 */
export function NewTaskPalette({ open, onOpenChange, projects, sessions, selectedId, filter, onPick }: Readonly<{ open: boolean; onOpenChange: (open: boolean) => void; projects: Project[]; sessions: SessionSummary[]; selectedId: string | null; filter: string | null; onPick: (projectId: string) => void }>) {
  const input = useRef<HTMLInputElement>(null);
  const pending = useRef<string | null>(null);
  // Read by Base UI when the popup unmounts; a pick leaves focus to the draft's composer.
  const leaveFocus = useRef(false);
  return (
    <CommandDialog
      open={open}
      onOpenChange={(o) => {
        if (o) leaveFocus.current = false;
        onOpenChange(o);
      }}
      onClosed={() => {
        const id = pending.current;
        pending.current = null;
        if (id) onPick(id);
      }}
      initialFocus={input}
      finalFocus={() => !leaveFocus.current}
      label="New task"
    >
      <PaletteBody
        projects={projects}
        start={newTaskProject(projects, sessions, filter, selectedId)}
        input={input}
        onClose={() => onOpenChange(false)}
        onPick={(id) => {
          pending.current = id;
          leaveFocus.current = true;
          onOpenChange(false);
        }}
      />
    </CommandDialog>
  );
}

function PaletteBody({ projects, start, input, onClose, onPick }: Readonly<{ projects: Project[]; start: Project | undefined; input: RefObject<HTMLInputElement | null>; onClose: () => void; onPick: (id: string) => void }>) {
  const id = useId();
  const { loaded } = useApp();
  const [query, setQuery] = useState('');
  const matches = useMemo(() => searchProjects(projects, query), [projects, query]);
  const { active, setActive, list, onKeyDown } = useHighlight(matches.length, Math.max(0, projects.findIndex((p) => p.id === start?.id)), (i) => onPick(matches[i].id));
  return (
    <>
      <div className="flex items-center gap-1 px-2 py-1.5">
        <Button size="icon" aria-label="Close" className="text-muted" onClick={onClose}>
          <ArrowLeft />
        </Button>
        <input
          ref={input}
          type="search"
          role="combobox"
          aria-label="Search projects"
          aria-autocomplete="list"
          aria-expanded="true"
          aria-controls={`${id}-list`}
          aria-activedescendant={active >= 0 ? `${id}-${active}` : undefined}
          placeholder="Search…"
          className={inputClass}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={(e) => {
            const digit = e.altKey && !e.ctrlKey && !e.metaKey ? DIGIT.exec(e.code)?.[1] : undefined;
            const hit = digit && matches[Number(digit) - 1];
            if (hit) {
              e.preventDefault();
              onPick(hit.id);
              return;
            }
            onKeyDown(e);
          }}
        />
      </div>
      <div className="max-h-[min(60dvh,420px)] overflow-y-auto overscroll-contain p-1">
        <p id={`${id}-label`} className={labelClass}>
          Projects
        </p>
        <div ref={list} role="listbox" id={`${id}-list`} aria-labelledby={`${id}-label`}>
          {matches.map((p, i) => (
            <button
              key={p.id}
              type="button"
              role="option"
              tabIndex={-1}
              id={`${id}-${i}`}
              aria-selected={i === active}
              data-highlighted={i === active ? '' : undefined}
              className={cn(itemClass, 'gap-2.5 px-2.5 py-1.5 text-left')}
              onPointerMove={() => i !== active && setActive(i)}
              onClick={() => onPick(p.id)}
            >
              <ProjectBadge badge={p.badge} />
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="truncate text-ink">{p.name}</span>
                <span className="truncate text-meta text-muted" title={p.dir}>{p.dir}</span>
              </span>
              {i < 9 && <Key>Alt+{i + 1}</Key>}
            </button>
          ))}
        </div>
        {matches.length === 0 && !loaded && <Skeleton label="Loading projects…" rows={3} className="gap-1 px-1 pb-1" rowClassName="h-10 w-full" />}
        {matches.length === 0 && loaded && <p role="status" className="px-2 py-3 text-caption text-muted">No project matches</p>}
      </div>
      <div className="fade-rule mx-3" aria-hidden="true" />
      <footer className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-caption text-muted">
        <span className="flex items-center gap-1"><Key>↑</Key><Key>↓</Key> Navigate</span>
        <span className="flex items-center gap-1"><Key>Enter</Key> Select</span>
        <span className="flex items-center gap-1"><Key>Esc</Key> Close</span>
      </footer>
    </>
  );
}
