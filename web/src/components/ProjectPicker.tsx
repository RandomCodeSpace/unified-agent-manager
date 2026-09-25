import { ArrowLeft, Check, Layers, Search, Settings } from 'lucide-react';
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type RefObject } from 'react';
import type { Project, SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { filteredProject, newTaskProject, searchProjects } from '../lib/tasks';
import { ProjectBadge } from './common';
import { Key } from './InlinePicker';
import { Button } from './ui/button';
import { CommandDialog } from './ui/dialog';
import { itemClass, labelClass } from './ui/menu';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

/**
 * The two Project lists a search box drives (T3 Code): the sidebar's filter dropdown and
 * the New task palette. The box keeps focus; the highlight is virtual (`aria-activedescendant`),
 * arrows move it, Enter picks it, Esc closes the surface and a pointer over a row takes it.
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
export function ProjectFilterPicker({ projects, filter, onFilter, onEdit }: { projects: Project[]; filter: string | null; onFilter: (id: string | null) => void; onEdit: (p: Project) => void }) {
  const chosen = filteredProject(projects, filter);
  const [open, setOpen] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const pending = useRef<Project | null>(null);
  return (
    <Popover.Root
      open={open}
      onOpenChange={setOpen}
      onOpenChangeComplete={(o) => {
        if (o || !pending.current) return;
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
        />
      </Popover.Content>
    </Popover.Root>
  );
}

function FilterList({ projects, filter, input, onPick, onEdit }: { projects: Project[]; filter: string | null; input: RefObject<HTMLInputElement | null>; onPick: (id: string | null) => void; onEdit: (p: Project) => void }) {
  const id = useId();
  const [query, setQuery] = useState('');
  const matches = useMemo(() => searchProjects(projects, query), [projects, query]);
  // All projects heads the list unless a search is on.
  const rows: (Project | null)[] = query.trim() ? matches : [null, ...matches];
  const { active, setActive, list, onKeyDown } = useHighlight(rows.length, 0, (i) => onPick(rows[i]?.id ?? null));
  return (
    <>
      <label className="flex items-center gap-1.5 border-b border-hairline px-2 pb-1 text-muted">
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
        {rows.map((p, i) => {
          const current = (p?.id ?? null) === filter;
          const pick = () => onPick(p?.id ?? null);
          return (
            // Not a button: the gear inside is one. The row is reached through the search box (`aria-activedescendant`), never focused itself.
            <div
              key={p?.id ?? 'all'}
              role="option"
              tabIndex={-1}
              id={`${id}-${i}`}
              aria-selected={i === active}
              aria-current={current || undefined}
              data-highlighted={i === active ? '' : undefined}
              className={cn(itemClass, 'pr-1', current && 'text-ink')}
              onPointerMove={() => i !== active && setActive(i)}
              onClick={pick}
              onKeyDown={(e) => e.key === 'Enter' && e.target === e.currentTarget && pick()}
            >
              {p ? <ProjectBadge badge={p.badge} /> : <Layers />}
              <span className="min-w-0 flex-1 truncate">{p ? p.name : 'All projects'}</span>
              {current && <Check aria-hidden="true" strokeWidth={2.5} className="!size-3.5 !text-accent" />}
              {p && (
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
export function NewTaskPalette({ open, onOpenChange, projects, sessions, selectedId, filter, onPick }: { open: boolean; onOpenChange: (open: boolean) => void; projects: Project[]; sessions: SessionSummary[]; selectedId: string | null; filter: string | null; onPick: (projectId: string) => void }) {
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

function PaletteBody({ projects, start, input, onClose, onPick }: { projects: Project[]; start: Project | undefined; input: RefObject<HTMLInputElement | null>; onClose: () => void; onPick: (id: string) => void }) {
  const id = useId();
  const [query, setQuery] = useState('');
  const matches = useMemo(() => searchProjects(projects, query), [projects, query]);
  const { active, setActive, list, onKeyDown } = useHighlight(matches.length, Math.max(0, projects.findIndex((p) => p.id === start?.id)), (i) => onPick(matches[i].id));
  return (
    <>
      <div className="flex items-center gap-1 border-b border-hairline px-2 py-1.5">
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
        {matches.length === 0 && <p role="status" className="px-2 py-3 text-caption text-muted">No project matches</p>}
      </div>
      <footer className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-hairline px-3 py-2 text-caption text-muted">
        <span className="flex items-center gap-1"><Key>↑</Key><Key>↓</Key> Navigate</span>
        <span className="flex items-center gap-1"><Key>Enter</Key> Select</span>
        <span className="flex items-center gap-1"><Key>Esc</Key> Close</span>
      </footer>
    </>
  );
}
