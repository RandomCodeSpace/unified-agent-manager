import { Check, ChevronDown, Folder, FolderGit2, GitBranch, GitBranchPlus, Lock, Search, TriangleAlert } from 'lucide-react';
import { useEffect, useId, useMemo, useState, type KeyboardEvent } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type BranchList, type Project, type SessionSummary, type ThreadPlace } from '../api';
import { cn } from '../lib/cn';
import { FolderPath, FootHead, FootReader, lowDiskWords, Pill, type LowDisk } from './BranchChip';
import { timeAgo } from './common';
import { Key } from './InlinePicker';
import { Input } from './ui/input';
import { PanelFoot, PanelSection } from './ui/panel';
import { Segmented } from './ui/segmented';

/** What a service that runs new Threads in a new branch or worktree advertises to connected instances. */
export const PLACE_CAPABILITY = 'worktrees-v1';

const CHIP = 'flex h-7 min-w-0 items-center gap-1.5 rounded-sm px-2 text-caption transition-colors hover:bg-tint-hover aria-expanded:bg-tint-hover pointer-coarse:h-9 [&_svg]:size-3.5 [&_svg]:shrink-0';

/** The folder name the service gives a Project under ~/.uam/worktrees: ASCII letters and digits, single hyphens, at most 48 bytes. */
export function projectSlug(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+/, '').slice(0, 48).replace(/-+$/, '') || 'project';
}

/**
 * A draft Thread's place under the composer: the Project folder's current branch, a new branch
 * there, or a new worktree from a base branch. The chip opens one anchored reader with the
 * three modes, the searchable local and remote base branches, and what the first message
 * creates. New branch is locked while `blocking` (a Thread working in the Project folder) runs,
 * since both would share one working tree.
 */
export function PlaceChip({ project, place, onPlace, blocking, lowDisk }: Readonly<{
  project: Project;
  place: ThreadPlace;
  onPlace: (place: ThreadPlace) => void;
  blocking?: SessionSummary;
  /** Free space is low: a warning at the strip's end, as on a Thread's chip. */
  lowDisk?: LowDisk;
}>) {
  const api = useApi();
  const listId = useId();
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [reader, setReader] = useState<'open' | 'closing' | null>(null);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const [branches, setBranches] = useState<{ list?: BranchList['branches']; reason?: string; error?: string } | null>(null);
  const open = reader === 'open';
  // A new worktree starts from a chosen base; each opening in that mode reads the branches again, the last list staying while it does.
  const listing = open && place.mode === 'worktree';
  useEffect(() => {
    if (!listing) return;
    const ctl = new AbortController();
    api.projectBranches(project.id, ctl.signal).then(
      (r) => setBranches({ list: r.branches, reason: r.reason }),
      (e) => { if (!ctl.signal.aborted) setBranches((b) => ({ list: b?.list, error: describeError(e) })); },
    );
    return () => ctl.abort();
  }, [listing, api, project.id]);

  // A new branch starts from the folder's current branch; a new worktree from its base, by default that same branch.
  const current = project.branch ?? 'this folder';
  const base = place.mode === 'worktree' ? place.base ?? project.branch ?? '' : current;
  const q = query.trim().toLowerCase();
  // Local branches first, then remote ones, each in the service's order (most recently committed first).
  const shown = useMemo(() => {
    const all = (branches?.list ?? []).filter((b) => b.name.toLowerCase().includes(q));
    return [...all.filter((b) => !b.remote), ...all.filter((b) => b.remote)];
  }, [branches, q]);

  const choose = (mode: ThreadPlace['mode']) => onPlace(mode === 'worktree' ? { mode, base: place.base ?? project.branch } : { mode });
  const pick = (b: BranchList['branches'][number]) => {
    onPlace({ mode: 'worktree', base: b.name });
    setReader('closing');
  };
  const activeIndex = Math.min(active, Math.max(0, shown.length - 1));
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (shown.length) setActive((i) => (i + (e.key === 'ArrowDown' ? 1 : shown.length - 1)) % shown.length);
    } else if (e.key === 'Enter' && shown[activeIndex]) {
      e.preventDefault();
      pick(shown[activeIndex]);
    }
  };
  const branchLocked = !!blocking;
  const blocker = blocking ? blocking.name || blocking.title || 'A Thread' : '';

  const face = place.mode === 'current'
    ? <><GitBranch aria-hidden="true" className="text-muted" /><span className="truncate font-medium text-ink">{current}</span><span className="shrink-0 text-muted">· current branch</span></>
    : <>{place.mode === 'worktree' ? <FolderGit2 aria-hidden="true" className="text-muted" /> : <GitBranchPlus aria-hidden="true" className="text-muted" />}<span className="shrink-0 font-medium text-ink">{place.mode === 'worktree' ? 'New worktree' : 'New branch'}</span><span className="shrink-0 text-muted">from</span><span className="truncate font-medium text-ink">{base}</span></>;

  return (
    <div className="flex min-w-0 flex-1 items-center gap-1 text-caption text-muted">
      <button
        ref={setAnchor}
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={`Where this Thread runs: ${place.mode === 'current' ? `${current}, current branch` : `${place.mode === 'worktree' ? 'new worktree' : 'new branch'} from ${base}`}`}
        className={CHIP}
        onClick={() => { setQuery(''); setActive(0); setReader('open'); }}
      >
        {face}
        <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
      </button>
      <span className="flex-1" />
      {lowDisk && <span className="flex h-7 shrink-0 items-center gap-1.5 px-2 text-caption text-warning"><TriangleAlert aria-hidden="true" className="size-3.5" />{lowDiskWords(lowDisk)}</span>}
      {reader && (
        <FootReader
          open={open}
          anchor={anchor}
          label="Where this Thread runs"
          width="w-[420px]"
          onClose={() => setReader('closing')}
          onClosed={() => setReader(null)}
          head={(close) => <FootHead title="Where this Thread runs" description="Set by the first message" facts={<Pill><Folder aria-hidden="true" />{project.name}</Pill>} close={close} />}
          foot={<PanelFoot>
            {place.mode === 'worktree' && <><span className="flex items-center gap-1.5"><Key>↑</Key><Key>↓</Key>move</span><span className="flex items-center gap-1.5"><Key>Enter</Key>choose</span></>}
            <span className="flex items-center gap-1.5"><Key>Esc</Key>close</span>
          </PanelFoot>}
        >
          <div className="flex min-h-0 flex-col gap-3 overflow-y-auto px-4 pt-1 pb-3">
            <Segmented
              aria-label="Where this Thread runs"
              value={place.mode}
              onValueChange={(v) => choose(v as ThreadPlace['mode'])}
              items={[
                { value: 'current', label: 'Current branch' },
                { value: 'branch', label: <span className="flex items-center gap-1">{branchLocked && <Lock aria-hidden="true" className="size-3" />}New branch</span>, disabled: branchLocked },
                { value: 'worktree', label: 'New worktree' },
              ]}
              className="h-7"
            />
            {blocking && (
              <p className="flex items-start gap-1.5 text-caption text-muted">
                <Lock aria-hidden="true" className="mt-px size-3.5 shrink-0" />
                <span>New branch is off while <span className="font-medium text-ink">{blocker}</span> works in this folder.</span>
              </p>
            )}
            {place.mode !== 'worktree' ? (
              <PanelSection label={place.mode === 'current' ? 'Branch' : 'From'} meta={place.mode === 'current' ? 'shared with the Project folder' : 'the Project folder’s current branch'}>
                <p className="truncate text-ui font-medium text-ink">{project.branch ?? 'No branch checked out'}</p>
              </PanelSection>
            ) : (
              <PanelSection label="From">
                <div className="relative">
                  <Search aria-hidden="true" className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted" />
                  <Input
                    size="md"
                    className="pl-7"
                    placeholder="Find a branch"
                    role="combobox"
                    aria-label="Find a base branch"
                    aria-expanded="true"
                    aria-controls={listId}
                    aria-activedescendant={shown[activeIndex] ? `${listId}-${activeIndex}` : undefined}
                    value={query}
                    onChange={(e) => { setQuery(e.target.value); setActive(0); }}
                    onKeyDown={onKey}
                  />
                </div>
                <div id={listId} role="listbox" aria-label="Base branch" className="-mx-1 flex max-h-56 flex-col gap-px overflow-y-auto overscroll-contain px-1">
                  {shown.map((b, i) => {
                    const on = b.name === base;
                    const heading = i === 0 ? (b.remote ? 'Remote' : 'Local') : b.remote && !shown[i - 1].remote ? 'Remote' : '';
                    return (
                      <div key={`${b.remote ? 'r' : 'l'}:${b.name}`} className="contents">
                        {heading && <h3 className="px-2 pt-1 pb-1 text-eyebrow uppercase text-muted">{heading}</h3>}
                        <button
                          id={`${listId}-${i}`}
                          type="button"
                          role="option"
                          tabIndex={-1}
                          aria-selected={on}
                          className={cn('flex h-8 w-full min-w-0 shrink-0 items-center gap-2 rounded-sm px-2 text-left transition-colors pointer-coarse:h-11', i === activeIndex ? 'bg-tint-hover' : on && 'bg-tint-selected')}
                          onMouseMove={() => i !== activeIndex && setActive(i)}
                          onClick={() => pick(b)}
                        >
                          <GitBranch aria-hidden="true" className="size-3.5 shrink-0 text-muted" />
                          <span className={cn('min-w-0 flex-1 truncate text-ui', on ? 'font-medium text-ink' : 'text-body')}>{b.name}</span>
                          {b.updated_at && <span className="shrink-0 text-caption text-muted">{timeAgo(b.updated_at)}</span>}
                          <Check aria-hidden="true" className={cn('size-4 shrink-0 text-ink', !on && 'invisible')} />
                        </button>
                      </div>
                    );
                  })}
                  {!branches && <p className="px-2 py-2 text-caption text-muted">Reading branches…</p>}
                  {branches?.list && !shown.length && <p className="px-2 py-2 text-caption text-muted">{q ? `No branch matches “${query.trim()}”.` : 'No branches.'}</p>}
                  {branches?.reason && <p className="px-2 py-2 text-caption text-muted">{branches.reason}</p>}
                  {branches?.error && <p role="alert" className="px-2 py-2 text-caption text-warning">Could not read the branches: {branches.error}</p>}
                </div>
              </PanelSection>
            )}
            {place.mode !== 'current' && (
              <PanelSection label="Creates" meta="renamed after the title, until pushed">
                <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-caption">
                  <dt className="text-muted">Branch</dt>
                  <dd className="min-w-0 font-mono text-code-sm text-ink">uam/<span className="text-muted">&lt;8 hex&gt;</span></dd>
                  {place.mode === 'worktree' && (
                    <>
                      <dt className="text-muted">Folder</dt>
                      <dd className="min-w-0 text-ink"><FolderPath path={`~/.uam/worktrees/${projectSlug(project.name)}/uam-<8 hex>`} /></dd>
                    </>
                  )}
                </dl>
              </PanelSection>
            )}
          </div>
        </FootReader>
      )}
    </div>
  );
}
