import { Check, ChevronDown, CircleDashed, Clock, KanbanSquare, Layers, FolderPlus, Search, Settings, X } from 'lucide-react';
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react';
import type { Project, SessionSummary } from '../api';
import { useFederation } from '../FederationContext';
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
export function ProjectFilterPicker({ projects, filter, onFilter, onEdit, onRoutines, onPlan, groups, side = 'bottom' }: Readonly<{ projects: Project[]; filter: string | null; onFilter: (id: string | null) => void; onEdit: (p: Project) => void; onRoutines?: (p: Project) => void; onPlan?: (p: Project) => void; /** With connected instances: the Projects by machine, each with its own actions; `projects` and `filter` then name the chosen one alone. */ groups?: ProjectGroup[]; /** Where the list opens: below the sidebar header's button, or right of the collapsed rail's. */ side?: 'bottom' | 'right' }>) {
  const chosen = filteredProject(projects, filter);
  const [open, setOpen] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  // Plan, Routines or Edit, run once the list has closed so focus lands back on the badge.
  const after = useRef<(() => void) | null>(null);
  const later = (run: () => void) => {
    after.current = run;
    setOpen(false);
  };
  return (
    <Popover.Root
      open={open}
      onOpenChange={setOpen}
      onOpenChangeComplete={(o) => {
        if (o) return;
        const run = after.current;
        after.current = null;
        run?.();
      }}
    >
      <Tip side={side === 'right' ? 'right' : undefined} label={chosen ? <>Project filter<span className="block text-on-primary/70">{chosen.name}</span></> : 'All projects'}>
        <Popover.Trigger render={<Button size="icon" aria-label={chosen ? `Project filter: ${chosen.name}` : 'Project filter: all projects'} className="text-muted" />}>
          {chosen ? <ProjectBadge badge={chosen.badge} /> : <Layers />}
        </Popover.Trigger>
      </Tip>
      {/* Wider than the Board pickers' list: each row also carries its Plan, Routines and Edit buttons. */}
      <Popover.Content side={side} align="start" sideOffset={side === 'right' ? 8 : 4} initialFocus={input} className="w-88 max-w-(--available-width) gap-0 p-1">
        <FilterList
          projects={projects}
          filter={chosen?.id ?? null}
          input={input}
          groups={groups?.map((g) => ({
            ...g,
            onPick: (p) => { g.onPick(p); setOpen(false); },
            onEdit: g.onEdit && ((p) => later(() => g.onEdit!(p))),
            onPlan: g.onPlan && ((p) => later(() => g.onPlan!(p))),
            onRoutines: g.onRoutines && ((p) => later(() => g.onRoutines!(p))),
          }))}
          onPick={(id) => {
            onFilter(id);
            setOpen(false);
          }}
          onEdit={(p) => later(() => onEdit(p))}
          onPlan={onPlan && ((p) => later(() => onPlan(p)))}
          onRoutines={onRoutines && ((p) => later(() => onRoutines(p)))}
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
  // With connected instances, the Boards of every machine whose planner is on, under a heading each; another machine's opens there.
  const federation = useFederation();
  const machines = federation?.machines;
  const groups: ProjectGroup[] | undefined = machines && [
    ...machines.filter((m) => m.active).map((m) => ({ key: m.id, label: m.label, projects, current: value, reason: noPlan, onPick: (p: Project) => { onPick(p.id); setOpen(false); } })),
    ...machines.filter((m) => !m.active && m.state.settings.planner === true && m.client.supports('planner-v1')).map((m) => ({ key: m.id, label: m.label, projects: m.state.projects, reason: noPlan, onPick: (p: Project) => { setOpen(false); federation.go?.(m.id, `#planner=${encodeURIComponent(p.id)}`); } })),
  ];
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
          groups={groups}
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

/** The Routines header's Project filter: the Planner's picker with All projects first (`value` null). */
export function RoutinesProjectPicker({ projects, value, onPick }: Readonly<{ projects: Project[]; value: string | null; onPick: (id: string | null) => void }>) {
  const [open, setOpen] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const project = projects.find((p) => p.id === value);
  const name = project?.name ?? 'All projects';
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger render={<Button size="md" aria-label={`Project: ${name}`} className="min-w-0 max-w-64 shrink px-2 sm:ml-1" />}>
        {project ? <ProjectBadge badge={project.badge} /> : <Layers className="text-muted" />}
        <span className="min-w-0 truncate max-[480px]:hidden">{name}</span>
        <ChevronDown className="text-muted" />
      </Popover.Trigger>
      <Popover.Content side="bottom" align="start" sideOffset={4} initialFocus={input} className="w-72 max-w-(--available-width) gap-0 p-1">
        <FilterList
          projects={projects}
          filter={project?.id ?? null}
          input={input}
          onPick={(id) => {
            onPick(id);
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
 * With connected instances, one machine's Projects in a list: a heading, then its rows, each
 * acting on that machine. `current` is the chosen Project's id there, if the choice is on it.
 */
export interface ProjectGroup {
  key: string;
  label: string;
  projects: Project[];
  current?: string | null;
  onPick: (p: Project) => void;
  onEdit?: (p: Project) => void;
  onPlan?: (p: Project) => void;
  onRoutines?: (p: Project) => void;
  reason?: (p: Project) => string | undefined;
}

/** A row on screen, with what picking it and its buttons do. */
interface ShownRow extends FilterRow {
  key: string;
  current: boolean;
  pick: () => void;
  onEdit?: () => void;
  onPlan?: () => void;
  onRoutines?: () => void;
}

/**
 * The searchable Project list. The sidebar filter heads it with All projects and gives each
 * Project its Plan and gear buttons; the Planner's picker lists no All projects, disables the
 * Projects `reason` names, and ends with its `tail` entry while that matches the search.
 * `groups` (connected instances) list the Projects under a heading per machine.
 */
function FilterList({ projects, filter, input, onPick, onEdit, onPlan, onRoutines, all = true, reason, tail, groups }: Readonly<{
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
  groups?: ProjectGroup[];
}>) {
  const id = useId();
  const [query, setQuery] = useState('');
  const q = query.trim().toLocaleLowerCase();
  const sections = useMemo(() => {
    const projectRow = (p: Project, current: boolean, pick: (p: Project) => void, actions: Pick<ProjectGroup, 'onEdit' | 'onPlan' | 'onRoutines' | 'reason'>, key = p.id): ShownRow => ({
      id: p.id,
      key,
      label: p.name,
      project: p,
      reason: actions.reason?.(p),
      current,
      pick: () => pick(p),
      onEdit: actions.onEdit && (() => actions.onEdit!(p)),
      onPlan: actions.onPlan && !p.no_git ? () => actions.onPlan!(p) : undefined,
      onRoutines: actions.onRoutines && (() => actions.onRoutines!(p)),
    });
    // All projects heads the list unless a search is on.
    const head: ShownRow[] = all && !q ? [{ id: null, key: 'all', label: 'All projects', icon: <Layers />, current: groups ? !groups.some((g) => g.current) : filter === null, pick: () => onPick(null) }] : [];
    if (groups) {
      return [
        { rows: head },
        ...groups.map((g) => ({ label: g.label, key: g.key, rows: searchProjects(g.projects, query).map((p) => projectRow(p, p.id === g.current, g.onPick, g, `${g.key}\n${p.id}`)) })).filter((section) => section.rows.length > 0),
        { rows: tail && tail.label.toLocaleLowerCase().includes(q) ? [{ ...tail, key: tail.id ?? 'tail', current: tail.id === filter, pick: () => onPick(tail.id) }] : [] },
      ];
    }
    return [{
      rows: [
        ...head,
        ...searchProjects(projects, query).map((p) => projectRow(p, p.id === filter, (project) => onPick(project.id), { onEdit, onPlan, onRoutines, reason })),
        ...(tail && tail.label.toLocaleLowerCase().includes(q) ? [{ ...tail, key: tail.id ?? 'tail', current: tail.id === filter, pick: () => onPick(tail.id) }] : []),
      ],
    }];
  }, [all, q, query, groups, projects, filter, onPick, onEdit, onPlan, onRoutines, reason, tail]);
  const rows = sections.flatMap((section) => section.rows);
  const { active, setActive, list, onKeyDown } = useHighlight(rows.length, 0, (i) => !rows[i].reason && rows[i].pick());
  const renderRow = (row: ShownRow, i: number) => {
    const p = row.project;
    const pick = () => !row.reason && row.pick();
    return (
      // The row holds the option (the Project, named by it alone) and, beside it, its buttons. The option is reached through the search box (`aria-activedescendant`), never focused itself.
      <div
        key={row.key}
        role="none"
        data-highlighted={i === active ? '' : undefined}
        className={cn(itemClass, 'pr-1', row.current && 'text-ink', row.reason && 'items-start py-1.5')}
        onPointerMove={() => i !== active && setActive(i)}
      >
        <div
          role="option"
          tabIndex={-1}
          id={`${id}-${i}`}
          aria-selected={i === active}
          aria-current={row.current || undefined}
          aria-disabled={row.reason ? true : undefined}
          aria-label={row.count === undefined ? undefined : `${row.label}, ${row.count} ${row.count === 1 ? 'card' : 'cards'}`}
          className={cn('flex min-w-0 flex-1 items-center gap-2 self-stretch outline-hidden', row.reason && 'items-start')}
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
          {row.current && <Check aria-hidden="true" strokeWidth={2.5} className="!size-3.5 !text-accent" />}
        </div>
        {p && row.onPlan && (
          <Button
            size="icon-sm"
            aria-label={`Plan ${p.name}`}
            title="Plan"
            className="text-muted"
            onClick={(e) => {
              e.stopPropagation();
              row.onPlan!();
            }}
          >
            <KanbanSquare />
          </Button>
        )}
        {p && row.onRoutines && (
          <Button
            size="icon-sm"
            aria-label={`Routines of ${p.name}`}
            title="Routines"
            className="text-muted"
            onClick={(e) => {
              e.stopPropagation();
              row.onRoutines!();
            }}
          >
            <Clock />
          </Button>
        )}
        {p && row.onEdit && (
          <Button
            size="icon-sm"
            aria-label={`Edit ${p.name}`}
            className="text-muted"
            onClick={(e) => {
              e.stopPropagation();
              row.onEdit!();
            }}
          >
            <Settings />
          </Button>
        )}
      </div>
    );
  };
  let index = 0;
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
      <div ref={list} role="listbox" id={`${id}-list`} aria-label="Projects" className="max-h-[min(60dvh,360px)] overflow-y-auto overflow-x-hidden overscroll-contain pt-1">
        {sections.map((section) => {
          const shown = section.rows.map((row) => renderRow(row, index++));
          if (!('label' in section)) return shown;
          return (
            <div key={section.key} role="group" aria-labelledby={`${id}-g-${section.key || 'home'}`}>
              <div id={`${id}-g-${section.key || 'home'}`} role="presentation" className={cn(labelClass, 'truncate')}>{section.label}</div>
              {shown}
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
 * (each Project's place in the full list, kept while searching) pick one; the draft opens once
 * the palette has closed, and its composer keeps the focus. A search that matches nothing offers Add project.
 */
export function NewTaskPalette({ open, onOpenChange, projects, sessions, selectedId, filter, onPick, onAddProject, groups, start }: Readonly<{ open: boolean; onOpenChange: (open: boolean) => void; projects: Project[]; sessions: SessionSummary[]; selectedId: string | null; filter: string | null; onPick: (projectId: string, group?: string) => void; onAddProject: () => void; /** With connected instances: every machine's Projects, under a heading each. */ groups?: PaletteGroup[]; /** The row highlighted first with `groups`. */ start?: { group: string; project?: string } }>) {
  const input = useRef<HTMLInputElement>(null);
  const pending = useRef<PaletteEntry | null>(null);
  const adding = useRef(false);
  // Read by Base UI when the popup unmounts; a pick leaves focus to the draft's composer.
  const leaveFocus = useRef(false);
  const entries: PaletteEntry[] = groups ? groups.flatMap((g) => g.projects.map((project) => ({ key: `${g.key}\n${project.id}`, project, group: g }))) : projects.map((project) => ({ key: project.id, project }));
  const first = groups ? start && entries.find((e) => e.group?.key === start.group && e.project.id === start.project) : undefined;
  return (
    <CommandDialog
      open={open}
      onOpenChange={(o) => {
        if (o) leaveFocus.current = false;
        onOpenChange(o);
      }}
      onClosed={() => {
        const entry = pending.current;
        pending.current = null;
        if (entry) onPick(entry.project.id, entry.group?.key);
        if (adding.current) onAddProject();
        adding.current = false;
      }}
      initialFocus={input}
      finalFocus={() => !leaveFocus.current}
      label="New task"
    >
      <PaletteBody
        entries={entries}
        start={groups ? first?.key : newTaskProject(projects, sessions, filter, selectedId)?.id}
        input={input}
        onClose={() => onOpenChange(false)}
        onPick={(entry) => {
          pending.current = entry;
          leaveFocus.current = true;
          onOpenChange(false);
        }}
        onAddProject={() => {
          adding.current = true;
          leaveFocus.current = true;
          onOpenChange(false);
        }}
      />
    </CommandDialog>
  );
}

/** A machine's Projects in the New task palette. */
export interface PaletteGroup {
  key: string;
  label: string;
  projects: Project[];
}

interface PaletteEntry {
  key: string;
  project: Project;
  group?: PaletteGroup;
}

function PaletteBody({ entries, start, input, onClose, onPick, onAddProject }: Readonly<{ entries: PaletteEntry[]; start: string | undefined; input: RefObject<HTMLInputElement | null>; onClose: () => void; onPick: (entry: PaletteEntry) => void; onAddProject: () => void }>) {
  const id = useId();
  const { loaded } = useApp();
  const [query, setQuery] = useState('');
  // Search within each machine's Projects, so the headings keep their order.
  const matches = useMemo(() => {
    const groups = [...new Set(entries.map((e) => e.group))];
    return groups.flatMap((g) => {
      const own = entries.filter((e) => e.group === g);
      return searchProjects(own.map((e) => e.project), query).map((project) => own.find((e) => e.project === project)!);
    });
  }, [entries, query]);
  const { active, setActive, list, onKeyDown } = useHighlight(matches.length, Math.max(0, entries.findIndex((e) => e.key === start)), (i) => onPick(matches[i]));
  const option = (e: PaletteEntry, i: number) => (
    <button
      key={e.key}
      type="button"
      role="option"
      tabIndex={-1}
      id={`${id}-${i}`}
      aria-selected={i === active}
      data-highlighted={i === active ? '' : undefined}
      className={cn(itemClass, 'gap-2.5 px-2.5 py-1.5 text-left')}
      onPointerMove={() => i !== active && setActive(i)}
      onClick={() => onPick(e)}
    >
      <ProjectBadge badge={e.project.badge} />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-ink">{e.project.name}</span>
        <span className="truncate text-meta text-muted" title={e.project.dir}>{e.project.dir}</span>
      </span>
      {entries.indexOf(e) < 9 && <Key>Alt+{entries.indexOf(e) + 1}</Key>}
    </button>
  );
  const grouped = entries.some((e) => e.group);
  return (
    <>
      <div className="flex items-center gap-1 px-2 py-1.5">
        <Button size="icon" aria-label="Close" className="text-muted" onClick={onClose}>
          <X />
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
          placeholder="Choose a project for the new task…"
          className={inputClass}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={(e) => {
            const digit = e.altKey && !e.ctrlKey && !e.metaKey ? DIGIT.exec(e.code)?.[1] : undefined;
            // A Project's number is its place in the full list; it picks the Project while the search shows it.
            const hit = digit && matches.find((m) => m === entries[Number(digit) - 1]);
            if (hit) {
              e.preventDefault();
              onPick(hit);
              return;
            }
            onKeyDown(e);
          }}
        />
      </div>
      <div className="max-h-[min(60dvh,420px)] overflow-y-auto overflow-x-hidden overscroll-contain p-1">
        <p id={`${id}-label`} className={labelClass}>
          Projects
        </p>
        <div ref={list} role="listbox" id={`${id}-list`} aria-labelledby={`${id}-label`}>
          {grouped
            ? [...new Set(matches.map((m) => m.group!))].map((g) => (
                <div key={g.key} role="group" aria-labelledby={`${id}-g-${g.key || 'home'}`}>
                  <div id={`${id}-g-${g.key || 'home'}`} role="presentation" className={cn(labelClass, 'truncate')}>{g.label}</div>
                  {matches.map((m, i) => (m.group === g ? option(m, i) : null))}
                </div>
              ))
            : matches.map(option)}
        </div>
        {matches.length === 0 && !loaded && <Skeleton label="Loading projects…" rows={3} className="gap-1 px-1 pb-1" rowClassName="h-10 w-full" />}
        {matches.length === 0 && loaded && (
          <div className="flex flex-col items-start gap-2 px-2 py-3">
            <p role="status" className="text-caption text-muted">No project matches</p>
            <Button variant="secondary" size="sm" onClick={onAddProject}>
              <FolderPlus />
              Add project
            </Button>
          </div>
        )}
      </div>
      {/* Keys mean nothing on a touch screen. */}
      <div className="fade-rule mx-3 pointer-coarse:hidden" aria-hidden="true" />
      <footer className="pointer-coarse:hidden flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-caption text-muted">
        <span className="flex items-center gap-1"><Key>↑</Key><Key>↓</Key> Navigate</span>
        <span className="flex items-center gap-1"><Key>Enter</Key> Select</span>
        <span className="flex items-center gap-1"><Key>Esc</Key> Close</span>
      </footer>
    </>
  );
}
