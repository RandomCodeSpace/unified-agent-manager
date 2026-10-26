import { ArrowUp, Check, CircleDot, TriangleAlert } from 'lucide-react';
import { lazy, Suspense, useCallback, useContext, useEffect, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { ApiError, describeError, type DiskInfo, type WorktreeEntry, type Worktrees } from '../api';
import { cn } from '../lib/cn';
import { percentFree, sizeText } from '../lib/worktrees';
import { Note, ProjectBadge, Skeleton, dateTime, timeAgo, useMinuteTick } from './common';
import { TaskList, useTaskActions } from './taskActions';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { AlertDialog, useConfirm } from './ui/dialog';
import { HelpTip } from './ui/tooltip';

const FolderPicker = lazy(() => import('./FolderPicker').then((m) => ({ default: m.FolderPicker })));

const RULE = 'Settling, archiving or deleting a Thread removes its worktree; the branch stays. A worktree with changes not committed is kept until you remove it here. Using the Thread again brings it back from the branch.';
const STAGE = { active: 'Active', settled: 'Settled', archived: 'Archived' } as const;
const card = 'flex flex-col gap-2 rounded-lg bg-raised p-5 shadow-raised';
/** While a size is still being measured, the list is read again this often. */
const MEASURING_POLL_MS = 3000;

/**
 * Settings → Projects & worktrees (mock C, "storage first"): the folders new projects and worktrees go in, the disk
 * with its 10% line, the Thread worktrees biggest first with what removing each would lose, and the ones settling
 * removed on its own. `projectFolder` is the default project location's field, shown first in Folders.
 */
export function WorktreesSettings({ projectFolder }: Readonly<{ projectFolder?: ReactNode }>) {
  const api = useApi();
  const [list, setList] = useState<Worktrees | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => api.worktrees().then((l) => { setList(l); setError(null); }, (e: unknown) => setError(describeError(e))), [api]);
  useEffect(() => { void load(); }, [load]);
  const measuring = !!list?.worktrees.some((w) => w.size_bytes === undefined);
  useEffect(() => {
    if (!measuring) return;
    const timer = window.setTimeout(() => void load(), MEASURING_POLL_MS);
    return () => window.clearTimeout(timer);
  }, [measuring, list, load]);
  if (!list) return error ? <Note tone="error" role="alert">Could not read the worktrees: {error}</Note> : <Skeleton label="Reading the worktrees…" rows={3} />;
  const total = list.worktrees.reduce((n, w) => n + (w.size_bytes ?? 0), 0);
  return (
    <>
      {(projectFolder || api.supports('worktree-folder-v1')) && <FolderCard list={list} onChanged={load} projectFolder={projectFolder} />}
      {list.disk && <DiskCard disk={list.disk} worktrees={total} />}
      <WorktreeList list={list} total={total} measuring={measuring} onChanged={load} />
      {list.removed.length > 0 && <RemovedCard list={list} />}
      {error && <Note tone="error" role="alert">{error}</Note>}
    </>
  );
}

/** The disk: free of total, a bar of everything else, the worktrees and free space, and the 10% line. */
function DiskCard({ disk, worktrees }: Readonly<{ disk: DiskInfo; worktrees: number }>) {
  const other = Math.max(0, disk.total_bytes - disk.free_bytes - worktrees);
  const width = (n: number) => `${(n / disk.total_bytes) * 100}%`;
  const legend = (swatch: string, label: string, value: number) => (
    <span className="flex items-center gap-1.5">
      <span aria-hidden="true" className={cn('size-2.5 rounded-xs', swatch)} />
      {label}
      <span className="text-ink tabular-nums">{sizeText(value)}</span>
    </span>
  );
  return (
    <section aria-labelledby="worktrees-disk-title" className={cn(card, 'gap-4')}>
      <div className="flex flex-wrap items-baseline gap-x-3">
        <h2 id="worktrees-disk-title" className="text-title text-ink">Disk</h2>
        <span className={cn('flex items-center gap-1.5 text-ui tabular-nums', disk.low ? 'text-warning' : 'text-body')}>
          {disk.low && <TriangleAlert aria-hidden="true" className="size-4 self-center" />}
          <span className="font-medium">{sizeText(disk.free_bytes)} free</span> of {sizeText(disk.total_bytes)} · {percentFree(disk)}%
          {disk.low && <span className="font-medium">· below 10%</span>}
        </span>
      </div>
      <div className="relative pb-4">
        <div role="img" aria-label={`Worktrees ${sizeText(worktrees)}, everything else ${sizeText(other)}, free ${sizeText(disk.free_bytes)}`} className="flex h-3 overflow-hidden rounded-xs bg-sunken">
          <span className="h-full bg-hairline-strong" style={{ width: width(other) }} />
          <span className="h-full border-l-2 border-raised bg-accent" style={{ width: width(worktrees) }} />
          <span className={cn('h-full flex-1 border-l-2 border-raised', disk.low ? 'bg-warning-wash' : 'bg-sunken')} />
        </div>
        {/* The 10% line: the warning starts once free space falls right of it. */}
        <span aria-hidden="true" className="absolute -top-1 right-[10%] h-5 w-px bg-ink" />
        <span aria-hidden="true" className="absolute top-4 right-[10%] translate-x-1/2 text-meta text-muted">10%</span>
      </div>
      <div className="flex flex-wrap gap-x-5 gap-y-1 text-caption text-muted">
        {legend('bg-accent', 'uam worktrees', worktrees)}
        {legend('bg-hairline-strong', 'Everything else', other)}
        {legend(disk.low ? 'bg-warning-wash ring-1 ring-warning' : 'bg-sunken ring-1 ring-hairline-strong', 'Free', disk.free_bytes)}
      </div>
    </section>
  );
}

function WorktreeList({ list, total, measuring, onChanged }: Readonly<{ list: Worktrees; total: number; measuring: boolean; onChanged: () => Promise<void> }>) {
  const api = useApi();
  const { projects } = useContext(TaskList);
  const actions = useTaskActions();
  const confirm = useConfirm<WorktreeEntry>();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const largest = Math.max(1, ...list.worktrees.map((w) => w.size_bytes ?? 0));
  const remove = async (w: WorktreeEntry, force: boolean) => {
    setBusy(w.path);
    setError(null);
    try {
      await api.removeWorktree(w.path, force);
      confirm.close();
    } catch (e) {
      // Changes appeared since the list was read: ask, as for a row that showed them.
      if (e instanceof ApiError && e.body.code === 'worktree_unsaved' && !force) confirm.ask({ ...w, uncommitted: Number((e.body.refs as string[] | undefined)?.[0]) || w.uncommitted || 1 });
      else setError(describeError(e));
    } finally {
      setBusy(null);
      await onChanged();
    }
  };
  const target = confirm.target;
  return (
    <section aria-labelledby="worktrees-title" className={card}>
      <div className="flex flex-wrap items-center gap-x-3">
        <div className="flex shrink-0 items-center gap-1">
          <h2 id="worktrees-title" className="text-title text-ink">Worktrees</h2>
          <HelpTip label="Worktrees" id="worktrees-help">{RULE}</HelpTip>
        </div>
        <span className="min-w-0 flex-1 truncate font-mono text-meta text-muted" title={list.root}>{list.root}</span>
        {list.worktrees.length > 0 && (
          <span className="text-ui text-body tabular-nums">
            {list.worktrees.length} {list.worktrees.length === 1 ? 'worktree' : 'worktrees'} · {measuring && '≥ '}{sizeText(total)}
          </span>
        )}
      </div>
      {list.worktrees.length === 0 ? (
        <Note>No worktrees. A Thread started in a new worktree is listed here.</Note>
      ) : (
        <ul aria-label="Worktrees" className="-mx-2 flex flex-col">
          {list.worktrees.map((w) => {
            const project = projects.find((p) => p.id === w.project_id);
            const loses = w.uncommitted > 0;
            return (
              <li key={w.path} className="flex min-h-14 flex-wrap items-center gap-x-3 gap-y-1 rounded-sm px-2 py-2 hover:bg-tint-hover">
                <div className="flex w-32 shrink-0 flex-col items-end gap-1 max-sm:w-20">
                  <span className="text-ui text-ink tabular-nums">{w.size_bytes === undefined ? <span className="text-muted">Measuring…</span> : `${w.size_partial ? '≥ ' : ''}${sizeText(w.size_bytes)}`}</span>
                  <span aria-hidden="true" className="h-1 w-full overflow-hidden rounded-full bg-sunken">
                    <span className="block h-full origin-right rounded-full bg-accent" style={{ transform: `scaleX(${(w.size_bytes ?? 0) / largest})` }} />
                  </span>
                </div>
                <div className="flex min-w-0 flex-1 basis-48 flex-col gap-0.5">
                  <div className="flex min-w-0 items-center gap-2">
                    {project && <ProjectBadge badge={project.badge} />}
                    {w.task_id ? (
                      <button type="button" className="min-w-0 truncate text-left text-ui text-ink hover:underline" onClick={() => actions.select(w.task_id!)}>{w.thread || 'New thread'}</button>
                    ) : (
                      <span className="min-w-0 truncate text-ui text-muted">No Thread</span>
                    )}
                    {w.stage && w.stage !== 'active' && <Chip fill="outline">{STAGE[w.stage]}</Chip>}
                  </div>
                  <span className="truncate font-mono text-code-sm text-muted" title={w.path}>{w.branch || w.path}</span>
                </div>
                <Pending w={w} />
                <Button size="sm" variant={loses ? 'danger' : 'subtle'} aria-label={`Remove worktree ${w.branch || w.path}`} disabled={busy !== null} onClick={() => (loses ? confirm.ask(w) : void remove(w, false))}>
                  Remove
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      {error && <Note tone="error" role="alert">{error}</Note>}
      <AlertDialog
        {...confirm.props}
        className="max-w-88"
        title="Remove this worktree?"
        description={target ? `${target.uncommitted} changed ${target.uncommitted === 1 ? 'file is' : 'files are'} not committed and will be lost.` : undefined}
        confirmLabel="Remove worktree"
        busy={busy !== null}
        onConfirm={() => { if (target) void remove(target, true); }}
      >
        {target?.branch && (
          <p className="mt-1 text-caption text-muted">
            Branch <Chip fill="well" className="h-auto px-1 font-mono text-ink">{target.branch}</Chip> stays{target.unpushed ? `, with ${target.unpushed} ${target.unpushed === 1 ? 'commit' : 'commits'} not pushed` : ''}.
          </p>
        )}
      </AlertDialog>
    </section>
  );
}

/** What removing loses, in words with a shape per kind, or a muted Saved. */
function Pending({ w }: Readonly<{ w: WorktreeEntry }>) {
  if (!w.uncommitted && !w.unpushed) {
    return <span className="flex w-36 shrink-0 items-center gap-1 text-caption text-muted"><Check aria-hidden="true" className="size-3.5" />Saved</span>;
  }
  return (
    <span className="flex w-36 shrink-0 flex-col gap-0.5 text-caption text-warning">
      {w.uncommitted > 0 && <span className="flex items-center gap-1 whitespace-nowrap"><CircleDot aria-hidden="true" className="size-3.5" />{w.uncommitted} not committed</span>}
      {w.unpushed > 0 && <span className="flex items-center gap-1 whitespace-nowrap"><ArrowUp aria-hidden="true" className="size-3.5" />{w.unpushed} not pushed</span>}
    </span>
  );
}

/** The worktrees settling removed on its own, newest first, faded: proof the cleanup runs. */
function RemovedCard({ list }: Readonly<{ list: Worktrees }>) {
  const { projects } = useContext(TaskList);
  useMinuteTick();
  return (
    <section aria-labelledby="worktrees-removed-title" className={card}>
      <div className="flex items-center gap-1">
        <h2 id="worktrees-removed-title" className="text-title text-ink">Removed on settling</h2>
        <HelpTip label="Removed on settling" id="worktrees-removed-help">{RULE}</HelpTip>
      </div>
      <ul aria-label="Removed on settling" className="-mx-2 flex flex-col">
        {list.removed.map((r) => {
          const project = projects.find((p) => p.id === r.project_id);
          return (
            <li key={`${r.path}:${r.at}`} className="flex min-h-9 flex-wrap items-center gap-x-3 px-2 text-ui text-muted">
              {project && <ProjectBadge badge={project.badge} className="opacity-60" />}
              <span className="min-w-0 flex-1 basis-40 truncate">{r.thread || 'New thread'}</span>
              <span className="min-w-0 truncate font-mono text-code-sm">{r.branch}</span>
              {r.size_bytes !== undefined && <span className="shrink-0 text-right text-caption tabular-nums">{sizeText(r.size_bytes)} freed</span>}
              <time dateTime={r.at} title={dateTime(r.at)} className="w-16 shrink-0 text-right text-caption tabular-nums">{timeAgo(r.at)}</time>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/** `path` with the home folder as ~, when the default folder (<home>/.uam/worktrees) gives it away. */
function homePath(path: string, defaultRoot?: string): string {
  const home = defaultRoot?.endsWith('/.uam/worktrees') ? defaultRoot.slice(0, -'/.uam/worktrees'.length) : '';
  return home && (path === home || path.startsWith(`${home}/`)) ? `~${path.slice(home.length)}` : path;
}

/** Folders: where Add project starts, then where new worktrees go with Change… (the folder picker) and Reset to default. */
function FolderCard({ list, onChanged, projectFolder }: Readonly<{ list: Worktrees; onChanged: () => Promise<void>; projectFolder?: ReactNode }>) {
  const api = useApi();
  const [browsing, setBrowsing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const save = async (worktrees_dir: string) => {
    setSaving(true);
    setError(null);
    try {
      await api.updateWebSettings({ worktrees_dir });
      setBrowsing(false);
      await onChanged();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setSaving(false);
    }
  };
  const isDefault = !list.default_root || list.root === list.default_root;
  return (
    <section aria-labelledby="worktrees-folder-title" className={cn(card, 'gap-4')}>
      <h2 id="worktrees-folder-title" className="text-title text-ink">Folders</h2>
      {projectFolder}
      {api.supports('worktree-folder-v1') && <>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex min-w-0 flex-1 basis-48 flex-col gap-0.5">
          <h3 className="text-caption text-muted">Folder for new worktrees</h3>
          <p className="text-caption text-muted">Existing worktrees stay where they are.</p>
        </div>
        <code className="min-w-0 truncate font-mono text-code-sm text-body" title={list.root}>{homePath(list.root, list.default_root)}</code>
        <div className="flex shrink-0 gap-2">
          <Button variant="secondary" disabled={saving || !api.supports('files-v1')} aria-expanded={browsing} aria-controls={browsing ? 'worktrees-folder-picker' : undefined} onClick={() => setBrowsing(!browsing)}>Change…</Button>
          <Button disabled={saving || isDefault} onClick={() => void save('')}>Reset to default</Button>
        </div>
      </div>
      {browsing && (
        <Suspense fallback={null}>
          <FolderPicker id="worktrees-folder-picker" start={list.root} onUse={(path) => void save(path)} onClose={() => setBrowsing(false)} />
        </Suspense>
      )}
      {error && <Note tone="error" role="alert">{error}</Note>}
      </>}
    </section>
  );
}
