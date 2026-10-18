import { ArrowUpFromLine, FileDiff, FilePen } from 'lucide-react';
import type { GitPending } from '../api';
import { Button } from './ui/button';
import { AlertDialog } from './ui/dialog';

/** A Settle or Archive the service refused over unsaved git work (`unsaved_work`): the Thread's own changes, or all its own worktree's work. */
export interface UnsavedTarget {
  kind: 'settle' | 'archive';
  /** Where the confirmation hangs: the sidebar row or the control that asked, read as it was activated (`rowAnchor`). */
  anchor: Element | null;
  pending: GitPending;
  /** The first changed files, as the service named them. */
  paths: string[];
  /** Puts the Thread away anyway (`force`). */
  force: () => Promise<unknown>;
  /** Opens the Thread's Changes on All changes; absent for another machine's Thread. */
  openChanges?: () => void;
}

const VERB = { settle: 'Settle', archive: 'Archive' } as const;
const WHERE = { settle: 'Settling moves this Thread out of Active', archive: 'Archiving moves this Thread to Settings → Archived' } as const;

/**
 * The compact anchored confirmation (settle mock C): the counts as pills with their glyphs and
 * words, the first files and how many more, Open Changes, Cancel and "Settle anyway". It hangs
 * under the sidebar row that asked, or the control that asked, or is centred on a phone.
 */
export function SettleGuard({ target, open, onOpenChange, onClosed, busy, onConfirm }: Readonly<{
  target: UnsavedTarget | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onClosed: () => void;
  busy: boolean;
  onConfirm: () => void;
}>) {
  if (!target) return null;
  const { kind, pending: p, paths, openChanges, anchor } = target;
  const more = p.uncommitted - paths.length;
  return (
    <AlertDialog
      open={open}
      onOpenChange={onOpenChange}
      onClosed={onClosed}
      anchor={anchor}
      title={`${VERB[kind]} with unsaved work?`}
      description={`${WHERE[kind]}; this work is not saved anywhere else.`}
      confirmLabel={`${VERB[kind]} anyway`}
      busy={busy}
      onConfirm={onConfirm}
      aside={openChanges && p.uncommitted > 0 && (
        <Button size="sm" variant="ghost" className="mr-auto -ml-1.5 text-muted" onClick={openChanges}>
          <FileDiff />
          Open Changes
        </Button>
      )}
    >
      <span className="mt-1.5 flex flex-wrap items-center gap-1.5">
        {p.uncommitted > 0 && (
          <span className="inline-flex h-5 items-center gap-1 rounded-xs bg-warning-wash px-1.5 text-caption text-warning tabular-nums">
            <FilePen aria-hidden="true" className="size-3" />
            {p.uncommitted} not committed
          </span>
        )}
        {p.unpushed > 0 && (
          <span className="inline-flex h-5 items-center gap-1 rounded-xs bg-warning-wash px-1.5 text-caption text-warning tabular-nums">
            <ArrowUpFromLine aria-hidden="true" className="size-3" />
            {p.unpushed} not pushed
          </span>
        )}
      </span>
      {paths.length > 0 && (
        <ul aria-label="Not committed" className="mt-1 flex flex-col">
          {paths.map((path) => {
            const slash = path.lastIndexOf('/');
            return (
              <li key={path} className="flex h-6 min-w-0 items-center text-caption" title={path}>
                <span className="min-w-0 truncate">
                  <span className="text-muted">{path.slice(0, slash + 1)}</span>
                  <span className="text-body">{path.slice(slash + 1)}</span>
                </span>
              </li>
            );
          })}
          {more > 0 && <li className="h-6 text-caption text-muted">and {more} more</li>}
        </ul>
      )}
    </AlertDialog>
  );
}
