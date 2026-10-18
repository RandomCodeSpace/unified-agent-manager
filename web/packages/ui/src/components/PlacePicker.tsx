import { Popover as BasePopover } from '@base-ui/react/popover';
import { Check, ChevronDown, Folder, FolderGit2, GitBranch, GitBranchPlus, Lock, Search } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, LIVE, readOnly, type BranchList, type Project, type SessionSummary, type ThreadPlace } from '../api';
import { cn } from '../lib/cn';
import { timeAgo } from './common';
import { itemClass, labelClass, popupClass, separatorClass } from './ui/menu';
import { Segmented } from './ui/segmented';
import { Tip } from './ui/tooltip';

/** What a service that runs new Threads in a new branch or worktree advertises to connected instances. */
export const PLACE_CAPABILITY = 'worktrees-v1';
/** What a service that switches the Project folder to an existing branch advertises: the `switch` place and POST …/git/branch. */
export const SWITCH_CAPABILITY = 'branch-switch-v1';

const CHIP = 'flex h-7 min-w-0 items-center gap-1.5 rounded-sm px-2 text-caption text-body transition-colors hover:bg-tint-hover aria-expanded:bg-tint-hover pointer-coarse:h-9 [&_svg]:size-3.5 [&_svg]:shrink-0';
const TEXT = 'flex h-7 min-w-0 items-center gap-1.5 px-2 text-caption text-body [&_svg]:size-3.5 [&_svg]:shrink-0';

type Branch = BranchList['branches'][number];
/** A row of the branch menu: a new branch from the folder's branch, or an existing one. */
type Pick = { kind: 'new' } | { kind: 'existing'; branch: Branch };

/**
 * The branch menu above its button, as light as the model picker: a search field, then (to switch the folder) a New
 * branch row, then the Local and Remote branches with their last commit's age and a check on `value`. To switch, the
 * folder's guard is mirrored from the service: a Thread that has not finished there turns every row off, with one line
 * naming it; changed tracked files turn off only the existing branches, since a new branch takes the changes along.
 */
function BranchMenu({ anchor, open, onClose, projectId, from, switching, existing, value, self, busy, error, onPick }: Readonly<{
  anchor: HTMLElement | null;
  open: boolean;
  onClose: () => void;
  projectId: string;
  /** The folder's branch, which a new branch starts from. */
  from: string;
  /** Switch the folder's branch (New branch row, guard); otherwise choose a new worktree's source branch. */
  switching: boolean;
  /** Existing branches can be chosen (a source branch always can; a switch needs `branch-switch-v1`). */
  existing: boolean;
  /** The checked row: 'new', or a branch (any branch of that name when `remote` is absent). */
  value: { name: string; remote?: boolean } | 'new';
  /** The started Thread that switches its own folder: it must have finished too. */
  self?: SessionSummary;
  /** A switch is being made. */
  busy?: boolean;
  error?: string;
  onPick: (pick: Pick) => void;
}>) {
  const api = useApi();
  const input = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const [list, setList] = useState<{ data?: BranchList; error?: string } | null>(null);
  // Each opening reads the branches and the guard again; the last list stays while it does.
  useEffect(() => {
    if (!open) return;
    const ctl = new AbortController();
    api.projectBranches(projectId, ctl.signal).then(
      (data) => setList({ data }),
      (e) => { if (!ctl.signal.aborted) setList((l) => ({ data: l?.data, error: describeError(e) })); },
    );
    return () => ctl.abort();
  }, [open, api, projectId]);

  const data = list?.data;
  const selfWorking = !!self && LIVE.includes(self.state);
  const reason: ReactNode = !switching || !data ? null
    : selfWorking || (data.busy && data.busy.id === self?.id) ? 'This Thread is working'
      : data.busy ? <><span className="font-medium text-ink">{data.busy.name}</span> is working</>
        : data.uncommitted ? `${data.uncommitted} ${data.uncommitted === 1 ? 'file' : 'files'} not committed`
          : null;
  const allOff = switching && (!data || busy || selfWorking || !!data.busy);
  const existingOff = allOff || (switching && !!data?.uncommitted);
  const q = query.trim().toLowerCase();
  const shown = useMemo(() => {
    const all = existing ? (data?.branches ?? []).filter((b) => b.name.toLowerCase().includes(q)) : [];
    return [...all.filter((b) => !b.remote), ...all.filter((b) => b.remote)];
  }, [data, q, existing]);
  const newRow = switching && !q;
  const rows: { key: string; off: boolean; pick: Pick }[] = [
    ...(newRow ? [{ key: '+new', off: allOff, pick: { kind: 'new' } as Pick }] : []),
    ...shown.map((b) => ({ key: `${b.remote ? 'r' : 'l'}:${b.name}`, off: existingOff, pick: { kind: 'existing', branch: b } as Pick })),
  ];
  const enabled = rows.flatMap((r, i) => (r.off ? [] : [i]));
  const at = enabled.includes(active) ? active : enabled[0] ?? -1;
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && enabled.length) {
      e.preventDefault();
      const k = enabled.indexOf(at);
      setActive(enabled[(k + (e.key === 'ArrowDown' ? 1 : enabled.length - 1)) % enabled.length]);
    } else if (e.key === 'Enter' && at >= 0) {
      e.preventDefault();
      onPick(rows[at].pick);
    }
  };
  const row = (r: (typeof rows)[number], i: number, content: ReactNode, on: boolean) => (
    <button key={r.key} type="button" role="option" aria-selected={on} aria-disabled={r.off || undefined} data-highlighted={i === at ? '' : undefined} data-disabled={r.off ? '' : undefined}
      className={cn(itemClass, 'text-left')} onMouseMove={() => !r.off && setActive(i)} onClick={() => !r.off && onPick(r.pick)}>
      {content}
      <Check aria-hidden="true" className={cn('!size-3.5 !text-accent', !on && 'invisible')} strokeWidth={2.5} />
    </button>
  );
  const branchRow = (b: Branch) => {
    const i = rows.findIndex((r) => r.key === `${b.remote ? 'r' : 'l'}:${b.name}`);
    const on = value !== 'new' && value.name === b.name && (value.remote === undefined || value.remote === b.remote);
    return row(rows[i], i, <>
      <GitBranch aria-hidden="true" className="!size-3.5" />
      <span className={cn('min-w-0 flex-1 truncate', on && 'font-medium text-ink')}>{b.name}</span>
      {b.updated_at && <span className="shrink-0 text-caption text-muted">{timeAgo(b.updated_at)}</span>}
    </>, on);
  };
  const local = shown.filter((b) => !b.remote);
  const remote = shown.filter((b) => b.remote);
  const label = switching ? 'Switch the branch' : 'Worktree from';
  return (
    <BasePopover.Root open={open} onOpenChange={(o, d) => {
      // A press on the button toggles it itself.
      if (!o && !(d.event?.target instanceof Node && anchor?.contains(d.event.target))) onClose();
    }} onOpenChangeComplete={(o) => { if (!o) { setQuery(''); setActive(0); } }}>
      <BasePopover.Portal>
        <BasePopover.Positioner anchor={anchor} side="top" align="start" sideOffset={6} collisionPadding={8} className="z-50 outline-hidden">
          <BasePopover.Popup data-popup="" initialFocus={input} aria-label={label} className={cn(popupClass, 'flex w-80 max-w-(--available-width) flex-col')}>
            <div className="flex h-8 items-center gap-2 px-2">
              <Search aria-hidden="true" className="size-3.5 shrink-0 text-muted" />
              <input ref={input} value={query} onChange={(e) => { setQuery(e.target.value); setActive(0); }} onKeyDown={onKey} placeholder="Find a branch" aria-label="Find a branch"
                className="min-w-0 flex-1 bg-transparent text-ui text-ink outline-hidden placeholder:text-muted" />
            </div>
            <div className={separatorClass} />
            {reason && <p className="flex items-center gap-1.5 px-2 pt-1 pb-1.5 text-caption text-muted"><Lock aria-hidden="true" className="size-3 shrink-0" /><span className="min-w-0 truncate">{reason}</span></p>}
            <div role="listbox" aria-label="Branches" className="flex max-h-72 flex-col overflow-y-auto overscroll-contain">
              {newRow && row(rows[0], 0, <>
                <GitBranchPlus aria-hidden="true" className="!size-3.5" />
                <span className="min-w-0 flex-1 truncate">New branch <span className="text-muted">from {from}</span></span>
              </>, value === 'new')}
              {local.length > 0 && <><p className={labelClass}>Local</p>{local.map(branchRow)}</>}
              {remote.length > 0 && <><p className={labelClass}>Remote</p>{remote.map(branchRow)}</>}
              {existing && data && !shown.length && q && <p className="px-2 py-2 text-caption text-muted">No branch matches “{query.trim()}”.</p>}
              {!data && !list?.error && <p className="px-2 py-2 text-caption text-muted">Reading branches…</p>}
              {data?.reason && <p className="px-2 py-2 text-caption text-muted">{data.reason}</p>}
            </div>
            {(error || list?.error) && <p role="alert" className="px-2 py-2 text-caption text-warning">{error || `Could not read the branches: ${list?.error}`}</p>}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}

/** `⑂ main ▾`: opens the branch menu. */
function BranchButton({ refFn, open, onClick, children, name }: Readonly<{ refFn: (el: HTMLButtonElement | null) => void; open: boolean; onClick: () => void; children: ReactNode; name: string }>) {
  return (
    <button ref={refFn} type="button" aria-haspopup="dialog" aria-expanded={open} aria-label={`Branch ${name}`} className={CHIP} onClick={onClick}>
      {children}
      <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
    </button>
  );
}

/**
 * A draft Thread's place under the composer: the segmented Project folder | New worktree, then the branch dropdown. In
 * the Project folder it switches the folder's branch (New branch from the folder's branch, or an existing branch,
 * `branch-switch-v1`); in New worktree it picks the worktree's source branch. Nothing is made before the first
 * message, which carries the choice; the worktree's own name shows only once it exists.
 */
export function DraftPlaceFoot({ project, place, onPlace }: Readonly<{ project: Project; place: ThreadPlace; onPlace: (place: ThreadPlace) => void }>) {
  const api = useApi();
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [open, setOpen] = useState(false);
  const worktree = place.mode === 'worktree';
  const from = project.branch ?? 'HEAD';
  const value: { name: string; remote?: boolean } | 'new' = place.mode === 'branch' ? 'new'
    : place.mode === 'switch' ? { name: place.branch ?? '', remote: !!place.remote }
      : worktree && place.base ? { name: place.base } : { name: from, remote: false };
  const pick = (p: Pick) => {
    setOpen(false);
    if (worktree) {
      if (p.kind === 'existing') onPlace({ mode: 'worktree', base: p.branch.name });
    } else if (p.kind === 'new') onPlace({ mode: 'branch' });
    else if (!p.branch.remote && p.branch.name === project.branch) onPlace({ mode: 'current' });
    else onPlace({ mode: 'switch', branch: p.branch.name, ...(p.branch.remote ? { remote: true } : {}) });
  };
  const name = value === 'new' ? 'New branch' : value.name;
  return (
    <div data-place="" className="flex min-w-0 items-center gap-1 text-caption">
      <Segmented aria-label="Where this Thread runs" value={worktree ? 'worktree' : 'current'} className="ml-1"
        onValueChange={(v) => { if ((v === 'worktree') !== worktree) onPlace({ mode: v === 'worktree' ? 'worktree' : 'current' }); }}
        items={[
          { value: 'current', label: <span className="flex items-center gap-1.5"><Folder aria-hidden="true" className="size-3.5 shrink-0" />Project folder</span> },
          { value: 'worktree', label: <span className="flex items-center gap-1.5"><FolderGit2 aria-hidden="true" className="size-3.5 shrink-0" />New worktree</span> },
        ]} />
      <BranchButton refFn={setAnchor} open={open} onClick={() => setOpen((o) => !o)} name={name}>
        {value === 'new' ? <GitBranchPlus aria-hidden="true" className="text-muted" /> : <GitBranch aria-hidden="true" className="text-muted" />}
        <span className="truncate font-medium text-ink">{name}</span>
      </BranchButton>
      <BranchMenu anchor={anchor} open={open} onClose={() => setOpen(false)} projectId={project.id} from={from} switching={!worktree}
        existing={worktree || api.supports(SWITCH_CAPABILITY)} value={value} onPick={pick} />
    </div>
  );
}

/**
 * A Thread's place under the composer once it exists. In its own worktree: one read-only run, `Worktree branch`, the
 * path in a tooltip (its folder is named for the branch, so the row leaves it out). In the Project folder: the branch
 * dropdown alone, which switches the folder at once under the same rule as a draft (the Thread itself must have
 * finished too); read-only text when the Thread is settled or archived, or the service cannot switch. A draft on a
 * service without places shows its branch. It shrinks only once the phase's detail beside it has given way (Task).
 */
export function PlaceFoot({ session, project }: Readonly<{ session: SessionSummary; project: Project | undefined }>) {
  const api = useApi();
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const worktree = session.worktree;
  const branch = worktree?.branch ?? session.branch ?? project?.branch;
  if (!branch || !project || project.no_git) return null;
  if (worktree) {
    return (
      <div data-place="" className="flex min-w-0 items-center">
        {/* Read-only: it only shows the path, on hover, focus or a press. */}
        <Tip openOnClick label={<span className="break-all">{worktree.path}</span>}>
          <button type="button" aria-label={`Worktree, branch ${branch}`} className={cn(TEXT, 'cursor-default rounded-sm')}>
            <FolderGit2 aria-hidden="true" className="text-muted" />
            <span className="shrink-0 text-muted">Worktree</span>
            <span className="min-w-0 truncate font-medium text-ink">{branch}</span>
          </button>
        </Tip>
      </div>
    );
  }
  const branchText = <span className={TEXT}><GitBranch aria-hidden="true" className="text-muted" /><span className="truncate font-medium text-ink">{branch}</span></span>;
  if (!session.id) return <div data-place="" className="flex min-w-0 items-center">{branchText}</div>;
  const switchable = api.supports(SWITCH_CAPABILITY) && !readOnly(session);
  const pick = async (p: Pick) => {
    if (p.kind === 'existing' && !p.branch.remote && p.branch.name === branch) { setOpen(false); return; }
    setBusy(true);
    setError('');
    try {
      await api.switchBranch(session.id, p.kind === 'new' ? { mode: 'branch' } : { mode: 'switch', branch: p.branch.name, ...(p.branch.remote ? { remote: true } : {}) });
      setOpen(false);
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div data-place="" className="flex min-w-0 items-center">
      {switchable ? <>
        <BranchButton refFn={setAnchor} open={open} onClick={() => { setError(''); setOpen((o) => !o); }} name={branch}>
          <GitBranch aria-hidden="true" className="text-muted" />
          <span className="truncate font-medium text-ink">{branch}</span>
        </BranchButton>
        <BranchMenu anchor={anchor} open={open} onClose={() => setOpen(false)} projectId={project.id} from={branch} switching existing value={{ name: branch, remote: false }}
          self={session} busy={busy} error={error} onPick={(p) => void pick(p)} />
      </> : branchText}
    </div>
  );
}
