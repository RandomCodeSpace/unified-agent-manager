import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { Check, Copy, FolderGit2, GitBranch, HardDrive, TriangleAlert, X } from 'lucide-react';
import { Fragment, useId, useState, type ReactNode } from 'react';
import type { Project, SessionSummary } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { Key } from './InlinePicker';
import { BottomSheet, LiftedRow } from './Subagents';
import { PHONE } from './Todos';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead, PanelSection } from './ui/panel';
import { Tip } from './ui/tooltip';

/** Free and total bytes of the disk holding the worktrees, passed only while free space is low. */
export interface LowDisk { free: number; total: number }

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];
/** 1.2 GB, 64 GB: one decimal below 10, binary steps. */
export function bytesLabel(n: number): string {
  let v = n, i = 0;
  while (v >= 1024 && i < UNITS.length - 1) { v /= 1024; i++; }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${UNITS[i]}`;
}

/** "Low disk: 31 GB free (8%)", with "of 387 GB" when `whole`. */
export function lowDiskWords({ free, total }: LowDisk, whole = false): string {
  return `Low disk: ${bytesLabel(free)} free${whole ? ` of ${bytesLabel(total)}` : ''} (${total > 0 ? Math.round((free / total) * 100) : 0}%)`;
}

/** "2 not committed · 3 not pushed": words and counts, never a colour alone; '' when nothing is pending. */
export function pendingWords(pending: SessionSummary['git_pending']): string {
  if (!pending) return '';
  return [pending.uncommitted > 0 && `${pending.uncommitted} not committed`, pending.unpushed > 0 && `${pending.unpushed} not pushed`].filter(Boolean).join(' · ');
}

/** A path that wraps only after its slashes, so no folder name is broken; a name wider than the line scrolls. */
export function FolderPath({ path, className }: Readonly<{ path: string; className?: string }>) {
  const parts = path.split('/');
  return (
    <code className={cn('block overflow-x-auto font-mono text-code-sm', className)}>
      {parts.map((part, i) => <Fragment key={i}><span className="whitespace-nowrap">{part}{i < parts.length - 1 && '/'}</span>{i < parts.length - 1 && <wbr />}</Fragment>)}
    </code>
  );
}

const CHIP = 'flex h-7 min-w-0 items-center gap-1.5 rounded-sm px-2 text-caption transition-colors hover:bg-tint-hover aria-expanded:bg-tint-hover pointer-coarse:h-9 [&_svg]:size-3.5 [&_svg]:shrink-0';

export function Pill({ children }: Readonly<{ children: ReactNode }>) {
  return <span className="inline-flex h-6 items-center gap-1 rounded-full bg-tint-well px-2 text-meta whitespace-nowrap tabular-nums text-body [&_svg]:size-3">{children}</span>;
}

/**
 * An anchored reader for the composer foot: above its chip over a dim with the chip lifted, a bottom
 * sheet on phones. `head` renders the reader's head; the close button comes with it on a sheet.
 */
export function FootReader({ open, anchor, label, width, onClose, onClosed, head, foot, children }: Readonly<{
  open: boolean;
  anchor: HTMLElement | null;
  label: string;
  /** A width class for the desktop reader. */
  width: string;
  onClose: () => void;
  onClosed: () => void;
  head: (close: ReactNode) => ReactNode;
  foot: ReactNode;
  children: ReactNode;
}>) {
  const id = useId();
  const sheet = !anchor || window.matchMedia(PHONE).matches;
  if (sheet) {
    return (
      <BottomSheet id={id} open={open} onClose={onClose} onClosed={onClosed} label={label} finalFocus={() => anchor} className="duration-160" backdropClassName="duration-160">
        {head(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
        {children}
      </BottomSheet>
    );
  }
  return (
    <BasePopover.Root open={open} modal onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
      <BasePopover.Portal>
        <BasePopover.Backdrop className={`${backdropClass} duration-160`} />
        {open && anchor.isConnected && <LiftedRow row={anchor} />}
        <BasePopover.Positioner anchor={anchor} side="top" align="start" sideOffset={8} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup id={id} data-popup="" aria-label={label} aria-modal="true" finalFocus={() => anchor} className={cn('flex max-h-[min(80vh,var(--available-height))] max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0', width)}>
            {head(null)}
            {children}
            {foot}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}

/** The reader's head: a glyph, the title and its state word, a line under it and fact pills. */
export function FootHead({ icon, title, state, description, facts, close }: Readonly<{ icon?: ReactNode; title: ReactNode; state?: ReactNode; description?: ReactNode; facts?: ReactNode; close: ReactNode }>) {
  return (
    <PanelHead className="gap-0.5 pt-3 pr-3 pb-3 pl-4">
      <div className="flex min-h-7 min-w-0 items-center gap-2 [&>svg]:size-4 [&>svg]:shrink-0 [&>svg]:text-muted">
        {icon}
        <h2 className="min-w-0 truncate text-title text-ink">{title}</h2>
        {state}
        <span className="flex-1" />
        {close}
      </div>
      {description && <p className={cn('text-caption text-muted', icon && 'pl-6')}>{description}</p>}
      {facts && <div className={cn('mt-1.5 flex flex-wrap items-center gap-1.5', icon && 'pl-6')}>{facts}</div>}
    </PanelHead>
  );
}

/**
 * The Thread's branch under the composer: the branch of its working folder, "worktree" when it runs
 * in its own worktree, and what is not committed or not pushed there, in words. It opens an anchored
 * reader with the branch, the folder and, for a worktree, its size. A low-disk warning sits at the
 * strip's end while `lowDisk` is passed. Nothing shows without a branch.
 */
export function BranchChip({ session, project, size, lowDisk }: Readonly<{
  session: SessionSummary;
  project: Project | undefined;
  /** The worktree's size in bytes, once measured. */
  size?: number;
  lowDisk?: LowDisk;
}>) {
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [reader, setReader] = useState<'open' | 'closing' | null>(null);
  const [copied, copy] = useCopied();
  const worktree = session.worktree;
  const branch = worktree?.branch ?? session.branch ?? project?.branch;
  if (!branch || project?.no_git) return null;
  const folder = worktree?.path ?? session.workdir;
  const pending = pendingWords(session.git_pending);
  const open = () => setReader('open');
  const close = () => setReader((r) => r && 'closing');
  return (
    <div className="flex min-w-0 flex-1 items-center gap-1 text-caption text-muted">
      <button ref={setAnchor} type="button" aria-haspopup="dialog" aria-expanded={reader === 'open'} aria-label={`Branch ${branch}${worktree ? ', worktree' : ''}${pending ? `, ${pending}` : ''}`} className={CHIP} onClick={open}>
        {worktree ? <FolderGit2 aria-hidden="true" /> : <GitBranch aria-hidden="true" />}
        <span className="truncate font-medium text-ink">{branch}</span>
        {worktree && <span className="shrink-0">· worktree</span>}
      </button>
      {pending && <span aria-hidden="true" className="min-w-0 truncate tabular-nums text-body">· {pending}</span>}
      <span className="flex-1" />
      {lowDisk && (
        <button type="button" className={cn(CHIP, 'shrink-0 text-warning')} onClick={open}>
          <TriangleAlert aria-hidden="true" />
          {lowDiskWords(lowDisk)}
        </button>
      )}
      {reader && (
        <FootReader
          open={reader === 'open'}
          anchor={anchor}
          label="Branch and folder"
          width="w-[400px]"
          onClose={close}
          onClosed={() => setReader(null)}
          head={(closeButton) => (
            <FootHead
              icon={worktree ? <FolderGit2 aria-hidden="true" /> : <GitBranch aria-hidden="true" />}
              title={branch}
              state={<span className="shrink-0 text-caption font-medium text-muted">{worktree ? 'Worktree' : 'Project folder'}</span>}
              facts={(session.git_pending?.uncommitted || session.git_pending?.unpushed || (worktree && size !== undefined)) ? <>
                {!!session.git_pending?.uncommitted && <Pill>{session.git_pending.uncommitted} not committed</Pill>}
                {!!session.git_pending?.unpushed && <Pill>{session.git_pending.unpushed} not pushed</Pill>}
                {worktree && size !== undefined && <Pill><HardDrive aria-hidden="true" />{bytesLabel(size)}</Pill>}
              </> : undefined}
              close={closeButton}
            />
          )}
          foot={<PanelFoot><span className="flex items-center gap-1.5"><Key>Esc</Key>close</span></PanelFoot>}
        >
          <div className="flex flex-col gap-3 overflow-y-auto px-4 pt-1 pb-3">
            <PanelSection label="Folder" action={<Tip label={copied ? 'Copied' : 'Copy path'}><Button size="icon-sm" aria-label="Copy path" className="text-muted" onClick={() => copy(folder)}>{copied ? <Check /> : <Copy />}</Button></Tip>}>
              <FolderPath path={folder} className="text-body" />
            </PanelSection>
            {lowDisk && (
              <p className="flex items-start gap-1.5 text-caption text-warning">
                <TriangleAlert aria-hidden="true" className="mt-px size-3.5 shrink-0" />
                <span>{lowDiskWords(lowDisk, true)}</span>
              </p>
            )}
          </div>
        </FootReader>
      )}
    </div>
  );
}
