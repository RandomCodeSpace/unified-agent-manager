import { Autocomplete } from '@base-ui/react/autocomplete';
import { Combobox } from '@base-ui/react/combobox';
import { Check, ChevronDown, Clock, Layers, FolderPlus, Search, Settings, X } from 'lucide-react';
import { useId, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react';
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
 * The Project lists a search box drives (T3 Code): the sidebar's filter dropdown and the
 * New task palette, on Base UI's Autocomplete and Combobox (inline). The box keeps focus; the
 * highlight is virtual (`aria-activedescendant`), arrows move it, Enter picks it, Esc closes
 * the surface and a pointer over a row takes it.
 */

const inputClass = 'h-8 min-w-0 flex-1 bg-transparent text-ui text-ink outline-none placeholder:text-muted';

/* ---------- Sidebar filter ---------- */

/**
 * The header's Project badge is the filter: the filtered Project's badge, or a layers glyph
 * for all of them. It opens a searchable list with All projects first; each Project row
 * carries a gear that opens Edit project once the list has closed, so focus returns to the badge.
 */
export function ProjectFilterPicker({ projects, filter, onFilter, onEdit, onRoutines, groups, side = 'bottom' }: Readonly<{ projects: Project[]; filter: string | null; onFilter: (id: string | null) => void; onEdit: (p: Project) => void; onRoutines?: (p: Project) => void; /** With connected instances: the Projects by machine, each with its own actions; `projects` and `filter` then name the chosen one alone. */ groups?: ProjectGroup[]; /** Where the list opens: below the sidebar header's button, or right of the collapsed rail's. */ side?: 'bottom' | 'right' }>) {
  const chosen = filteredProject(projects, filter);
  const [open, setOpen] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  // Routines or Edit, run once the list has closed so focus lands back on the badge.
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
      {/* Wider than the Routines picker's list: each row also carries its Routines and Edit buttons. */}
      <Popover.Content aria-label="Project filter" side={side} align="start" sideOffset={side === 'right' ? 8 : 4} initialFocus={input} className="w-88 max-w-(--available-width) gap-0 p-1">
        <FilterList
          projects={projects}
          filter={chosen?.id ?? null}
          input={input}
          groups={groups?.map((g) => ({
            ...g,
            onPick: (p) => { g.onPick(p); setOpen(false); },
            onEdit: g.onEdit && ((p) => later(() => g.onEdit!(p))),
            onRoutines: g.onRoutines && ((p) => later(() => g.onRoutines!(p))),
          }))}
          onPick={(id) => {
            onFilter(id);
            setOpen(false);
          }}
          onEdit={(p) => later(() => onEdit(p))}
          onRoutines={onRoutines && ((p) => later(() => onRoutines(p)))}
        />
      </Popover.Content>
    </Popover.Root>
  );
}

/** The Routines header's Project filter: the filter's list, opening below, with All projects first (`value` null). */
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
      <Popover.Content aria-label="Project filter" side="bottom" align="start" sideOffset={4} initialFocus={input} className="w-72 max-w-(--available-width) gap-0 p-1">
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

/** A row of the list: a Project, or All projects (`id` null). */
interface FilterRow {
  id: string | null;
  label: string;
  project?: Project;
  icon?: ReactNode;
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
  onRoutines?: (p: Project) => void;
}

/** A row on screen, with what picking it and its buttons do. */
interface ShownRow extends FilterRow {
  key: string;
  current: boolean;
  pick: () => void;
  onEdit?: () => void;
  onRoutines?: () => void;
}

/**
 * The searchable Project list, headed by All projects. The sidebar filter gives each
 * Project its Routines and gear buttons.
 * `groups` (connected instances) list the Projects under a heading per machine.
 */
function FilterList({ projects, filter, input, onPick, onEdit, onRoutines, groups }: Readonly<{
  projects: Project[];
  filter: string | null;
  input: RefObject<HTMLInputElement | null>;
  onPick: (id: string | null) => void;
  onEdit?: (p: Project) => void;
  onRoutines?: (p: Project) => void;
  groups?: ProjectGroup[];
}>) {
  const [query, setQuery] = useState('');
  const q = query.trim().toLocaleLowerCase();
  const sections = useMemo(() => {
    const projectRow = (p: Project, current: boolean, pick: (p: Project) => void, actions: Pick<ProjectGroup, 'onEdit' | 'onRoutines'>, key = p.id): ShownRow => ({
      id: p.id,
      key,
      label: p.name,
      project: p,
      current,
      pick: () => pick(p),
      onEdit: actions.onEdit && (() => actions.onEdit!(p)),
      onRoutines: actions.onRoutines && (() => actions.onRoutines!(p)),
    });
    // All projects heads the list unless a search is on.
    const head: ShownRow[] = !q ? [{ id: null, key: 'all', label: 'All projects', icon: <Layers />, current: groups ? !groups.some((g) => g.current) : filter === null, pick: () => onPick(null) }] : [];
    if (groups) {
      return [
        { rows: head },
        ...groups.map((g) => ({ label: g.label, key: g.key, rows: searchProjects(g.projects, query).map((p) => projectRow(p, p.id === g.current, g.onPick, g, `${g.key}\n${p.id}`)) })).filter((section) => section.rows.length > 0),
      ];
    }
    return [{
      rows: [
        ...head,
        ...searchProjects(projects, query).map((p) => projectRow(p, p.id === filter, (project) => onPick(project.id), { onEdit, onRoutines })),
      ],
    }];
  }, [q, query, groups, projects, filter, onPick, onEdit, onRoutines]);
  const rows = sections.flatMap((section) => section.rows);
  const renderRow = (row: ShownRow) => {
    const p = row.project;
    return (
      // The row holds the option (the Project, named by it alone) and, beside it, its buttons. The option is reached through the search box (`aria-activedescendant`), never focused itself.
      <div key={row.key} role="none" className={cn(itemClass, 'pr-1 has-data-highlighted:bg-tint-hover has-data-highlighted:text-ink has-data-highlighted:[&_svg]:text-ink', row.current && 'text-ink')}>
        <Autocomplete.Item
          value={row.key}
          aria-current={row.current || undefined}
          className="flex min-w-0 flex-1 items-center gap-2 self-stretch outline-hidden"
          onClick={row.pick}
        >
          {p ? <ProjectBadge badge={p.badge} /> : row.icon}
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="truncate">{row.label}</span>
          </span>
          {row.current && <Check aria-hidden="true" strokeWidth={2.5} className="!size-3.5 !text-accent" />}
        </Autocomplete.Item>
        {p && row.onRoutines && (
          <Tip label="Routines">
            <Button
              size="icon-sm"
              aria-label={`Routines of ${p.name}`}
              className="text-muted"
              onClick={(e) => {
                e.stopPropagation();
                row.onRoutines!();
              }}
            >
              <Clock />
            </Button>
          </Tip>
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
  return (
    // Inline: the popover around it is the surface, so Esc bubbles to it. A row press would fill the box with its value; the list closes instead.
    <Autocomplete.Root inline open mode="none" autoHighlight="always" keepHighlight loopFocus={false} value={query} onValueChange={(v, d) => d.reason !== 'item-press' && setQuery(v)}>
      <div className="flex items-center gap-1.5 px-2 pb-1 text-muted">
        <Search aria-hidden="true" className="size-3.5 shrink-0" />
        <Autocomplete.Input ref={input} type="search" aria-label="Search projects" placeholder="Search projects…" className={inputClass} />
      </div>
      <Autocomplete.List aria-label="Projects" className="max-h-[min(60dvh,360px)] overflow-y-auto overflow-x-hidden overscroll-contain pt-1">
        {sections.map((section) => {
          const shown = section.rows.map(renderRow);
          if (!('label' in section)) return shown;
          return (
            <Autocomplete.Group key={section.key}>
              <Autocomplete.GroupLabel className={cn(labelClass, 'truncate')}>{section.label}</Autocomplete.GroupLabel>
              {shown}
            </Autocomplete.Group>
          );
        })}
        {rows.length === 0 && <p role="status" className="px-2 py-2 text-caption text-muted">No project matches</p>}
      </Autocomplete.List>
    </Autocomplete.Root>
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
      label="New thread"
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
  // Rows off screen skip style, layout and paint (content-visibility): with 300 Projects the palette opens about 10 frames sooner at 4x CPU throttle.
  const option = (e: PaletteEntry) => (
    <Combobox.Item key={e.key} value={e.key} className={cn(itemClass, 'gap-2.5 px-2.5 py-1.5 text-left [contain-intrinsic-size:auto_42px] [content-visibility:auto]')} onClick={() => onPick(e)}>
      <ProjectBadge badge={e.project.badge} />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-ink">{e.project.name}</span>
        <span className="truncate text-meta text-muted" title={e.project.dir}>{e.project.dir}</span>
      </span>
      {entries.indexOf(e) < 9 && <span className="contents pointer-coarse:hidden"><Key>Alt+{entries.indexOf(e) + 1}</Key></span>}
    </Combobox.Item>
  );
  const grouped = entries.some((e) => e.group);
  return (
    // Inline in the dialog, which owns Esc. The start row is the selection, so it holds the highlight on open and when the search is cleared.
    <Combobox.Root
      inline
      open
      autoHighlight
      loopFocus={false}
      value={start ?? entries[0]?.key ?? null}
      inputValue={query}
      onInputValueChange={(v, d) => d.reason !== 'item-press' && setQuery(v)}
    >
      <div className="flex items-center gap-1 px-2 py-1.5">
        <Button size="icon" aria-label="Close" className="text-muted" onClick={onClose}>
          <X />
        </Button>
        <Combobox.Input
          ref={input}
          aria-label="Search projects"
          placeholder="Search projects…"
          className={inputClass}
          onKeyDown={(e) => {
            const digit = e.altKey && !e.ctrlKey && !e.metaKey ? DIGIT.exec(e.code)?.[1] : undefined;
            // A Project's number is its place in the full list; it picks the Project while the search shows it.
            const hit = digit && matches.find((m) => m === entries[Number(digit) - 1]);
            if (hit) {
              e.preventDefault();
              onPick(hit);
            }
          }}
        />
      </div>
      <div className="max-h-[min(60dvh,420px)] overflow-y-auto overflow-x-hidden overscroll-contain p-1">
        <p id={`${id}-label`} className={labelClass}>
          Projects
        </p>
        <Combobox.List aria-labelledby={`${id}-label`}>
          {grouped
            ? [...new Set(matches.map((m) => m.group!))].map((g) => (
                <Combobox.Group key={g.key}>
                  <Combobox.GroupLabel className={cn(labelClass, 'truncate')}>{g.label}</Combobox.GroupLabel>
                  {matches.map((m) => (m.group === g ? option(m) : null))}
                </Combobox.Group>
              ))
            : matches.map(option)}
        </Combobox.List>
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
    </Combobox.Root>
  );
}
