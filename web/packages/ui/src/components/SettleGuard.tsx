import { ArrowUpFromLine, FileDiff, FilePen } from 'lucide-react';
import { useCallback, useRef, useState } from 'react';
import { unsavedRefusal, type Changes as ChangesData, type GitPending } from '../api';
import { Blocks } from './ui/blocks';
import { Button } from './ui/button';
import { AlertDialog, rowAnchor } from './ui/dialog';

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

/**
 * The state behind SettleGuard. `askUnsaved` and `askArchive` open the confirmation when a Settle or an Archive
 * meets unsaved work; `guard` is what the popover takes.
 */
export function useUnsavedGuard() {
  // Settle and Archive of a Thread whose folder has unsaved git work ask first (SettleGuard): the service refuses
  // without `force`, naming the counts and the first files. `changesRequest` asks the Thread's view to open its Changes.
  const [unsaved, setUnsaved] = useState<UnsavedTarget | null>(null);
  const [unsavedOpen, setUnsavedOpen] = useState(false);
  const [forcing, setForcing] = useState(false);
  const [changesRequest, setChangesRequest] = useState<{ id: string; at: number } | null>(null);
  // Counts openings: a confirm whose request reopened the popover with a refusal leaves it open.
  const unsavedAsked = useRef(0);
  /** Opens the confirmation; `list` reads the folder's first changed files when no refusal named them. */
  const openUnsaved = useCallback((t: UnsavedTarget, list?: () => Promise<ChangesData>) => {
    unsavedAsked.current += 1;
    setUnsaved(t);
    setUnsavedOpen(true);
    list?.().then((c) => {
      const paths = c.files.filter((f) => f.status !== 'untracked').slice(0, 3).map((f) => f.path);
      setUnsaved((u) => (u === t ? { ...t, paths } : u));
    }).catch(() => {});
  }, []);
  /**
   * Settle or Archive a Thread. Unsaved work the summary knows will stop it (`known`, see `putAwayWork`) opens the
   * confirmation at once, with no request; otherwise `request` runs, and a refusal over work the summary did not know
   * (a shared folder's Thread's own changes) opens it instead of failing.
   * The anchor is read now, as the control is activated: the row stays on screen while its hover Settle leaves it.
   */
  const askUnsaved = useCallback(async <T,>(known: GitPending | null, request: () => Promise<T>, target: (refused: Pick<UnsavedTarget, 'pending' | 'paths' | 'anchor'>) => UnsavedTarget, list: () => Promise<ChangesData>): Promise<T | void> => {
    // Read now; a refusal while the plain Archive asks keeps that popover and its anchor.
    const anchor = rowAnchor();
    if (known) return openUnsaved(target({ pending: known, paths: [], anchor }), list);
    try {
      return await request();
    } catch (e) {
      const refused = unsavedRefusal(e);
      if (!refused) throw e;
      openUnsaved(target({ ...refused, anchor }));
    }
  }, [openUnsaved]);
  /**
   * Archive asks once: its dry run (`check`) reads the Thread's unsaved work before the popover opens, so it opens as
   * the plain question or with the files. Any other failure (an older instance's 405) opens the plain one, whose
   * request still meets the service's refusal in place.
   */
  const askArchive = useCallback((check: () => Promise<unknown>, target: (refused: Pick<UnsavedTarget, 'pending' | 'paths' | 'anchor'>) => UnsavedTarget, plain: () => Promise<unknown>) => {
    const anchor = rowAnchor();
    void check().then(() => null, unsavedRefusal).then((refused) => openUnsaved(refused ? target({ ...refused, anchor }) : { kind: 'archive', anchor, pending: null, paths: [], force: plain }));
  }, [openUnsaved]);
  const guard = {
    target: unsaved,
    open: unsavedOpen,
    onOpenChange: (o: boolean) => !o && setUnsavedOpen(false),
    onClosed: () => setUnsaved(null),
    busy: forcing,
    onConfirm: () => {
      if (!unsaved) return;
      const asked = unsavedAsked.current;
      setForcing(true);
      void unsaved.force().catch(() => {}).finally(() => {
        setForcing(false);
        if (unsavedAsked.current === asked) setUnsavedOpen(false);
      });
    },
  };
  return { guard, askUnsaved, askArchive, changesRequest, setChangesRequest, setUnsavedOpen };
}
