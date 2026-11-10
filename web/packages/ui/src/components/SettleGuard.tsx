import { ArrowUpFromLine, FileDiff, FilePen } from 'lucide-react';
import type { GitPending } from '../api';
import { Blocks } from './ui/blocks';
import { Button } from './ui/button';
import { AlertDialog } from './ui/dialog';

/**
 * A Settle or Archive the service refused over unsaved git work (`unsaved_work`): the Thread's own changes, or all its own worktree's work.
 * An Archive not refused yet has no `pending`: the same popover asks plainly, and a refusal fills it in place.
 */
export interface UnsavedTarget {
  kind: 'settle' | 'archive';
  /** Where the confirmation hangs: the sidebar row or the control that asked, read as it was activated (`rowAnchor`). */
  anchor: Element | null;
  pending: GitPending | null;
  /** The first changed files, as the service named them. */
  paths: string[];
  /** Puts the Thread away anyway (`force`); without `pending`, the plain request, whose refusal fills this target in. */
  force: () => Promise<unknown>;
  /** Opens the Thread's Changes; absent for another machine's Thread. */
  openChanges?: () => void;
}

const VERB = { settle: 'Settle', archive: 'Archive' } as const;
const WHERE = { settle: 'Settling moves this Thread out of Active (Reopen brings it back)', archive: 'Archiving moves this Thread to Settings → Archived (Unarchive brings it back)' } as const;

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
  const more = (p?.uncommitted ?? 0) - paths.length;
  // One popover: an Archive refused while it asks plainly fills this one in place, never a second.
  return (
    <AlertDialog
      open={open}
      onOpenChange={onOpenChange}
      onClosed={onClosed}
      anchor={anchor}
      title={p ? `${VERB[kind]} with unsaved work?` : 'Archive this thread?'}
      description={p ? `${WHERE[kind]}; this work is not saved anywhere else.` : 'The thread moves to Settings → Archived with its full conversation, and Unarchive returns it to Finished.'}
      confirmLabel={p ? `${VERB[kind]} anyway` : 'Archive thread'}
      danger={!!p}
      busy={busy}
      onConfirm={onConfirm}
      aside={openChanges && p && p.uncommitted > 0 && (
        <Button size="sm" variant="ghost" className="mr-auto -ml-1.5 text-muted" onClick={openChanges}>
          <FileDiff />
          Open Changes
        </Button>
      )}
    >
      {/* Counts are blocks (DESIGN.md): neutral cells, the tone on the glyph and the word; nothing here is a filled tag. */}
      {p && <Blocks
        className="mt-1.5"
        items={[
          ...(p.uncommitted > 0 ? [{ key: 'uncommitted', tone: 'warning' as const, Icon: FilePen, label: `${p.uncommitted} not committed`, title: `${p.uncommitted} ${p.uncommitted === 1 ? 'file' : 'files'} not committed` }] : []),
          ...(p.unpushed > 0 ? [{ key: 'unpushed', tone: 'warning' as const, Icon: ArrowUpFromLine, label: `${p.unpushed} not pushed`, title: `${p.unpushed} ${p.unpushed === 1 ? 'commit' : 'commits'} not pushed` }] : []),
        ]}
      />}
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
