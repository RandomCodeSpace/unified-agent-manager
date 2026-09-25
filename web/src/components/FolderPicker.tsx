import { Check, ChevronRight, Eye, EyeOff, Folder, FolderPlus, FolderUp, GitBranch, Link, X } from 'lucide-react';
import { useEffect, useRef, useState, type KeyboardEvent, type MouseEvent } from 'react';
import { ApiError, api, describeError, isStatus, type DirList } from '../api';
import { cn } from '../lib/cn';
import { breadcrumbs, cleanPath, listingError, matchFrom, parentOf } from '../lib/folders';
import { Loading, Note } from './common';
import { Button } from './ui/button';
import { Input } from './ui/input';

/** One listing request. A fresh object each time, so the same folder can be listed again after a folder is created in it. */
interface Want {
  /** Absent: the service user's home. */
  path?: string;
  /** Dot-folders too; the server filters and caps after the filter. */
  hidden: boolean;
  /** Fall back to home when this path cannot be listed (the path field held something that is not a folder). */
  fallback?: boolean;
}

interface Listing extends DirList {
  for: Want;
  /** Why `entries` is empty; the breadcrumb and Up still work on the folder that failed. */
  error?: string;
}

const TYPEAHEAD_MS = 700;

/**
 * The Add project dialog's inline folder browser (DESIGN.md: Folder picker). A breadcrumb, a
 * listbox of folders in a `canvas` well, and a footer. The selection is the target: **Use this
 * folder** takes the selected path, else the folder being shown. Focus stays on the listbox
 * (`aria-activedescendant`): arrows move, Enter opens, Backspace goes up, typing jumps to a
 * name, Ctrl/Cmd+Enter uses. Escape cancels the New folder row first, then closes the picker;
 * the dialog stays open either way.
 */
export function FolderPicker({ id, start, onUse, onClose }: { id: string; start: string; onUse: (path: string) => void; onClose: () => void }) {
  const root = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const nameInput = useRef<HTMLInputElement>(null);
  const typed = useRef({ text: '', at: 0 });
  const [want, setWant] = useState<Want>(() => ({ path: cleanPath(start) || undefined, hidden: false, fallback: true }));
  /** The request in flight or answered last; a create that finishes after the user moved on must not undo the move. */
  const latest = useRef(want);
  const [listing, setListing] = useState<Listing | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [createBusy, setCreateBusy] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  const loading = listing?.for !== want;
  const entries = listing?.entries ?? [];
  const selIndex = entries.findIndex((e) => e.path === selected);
  // A path, not an index: a folder just created is the target before the re-list shows it.
  const target = selected ?? listing?.path;
  const crumbs = breadcrumbs(listing?.path ?? '');

  useEffect(() => {
    latest.current = want;
    let live = true;
    api
      .listDirs(want.path, want.hidden)
      .then((l) => live && setListing({ ...l, for: want }))
      .catch((e: unknown) => {
        if (!live) return;
        const status = e instanceof ApiError ? e.status : 0;
        // Not a folder (400, 404): start at home instead. A folder that exists but cannot be read says so.
        if (want.fallback && status !== 403) {
          setWant({ hidden: want.hidden });
          return;
        }
        const path = want.path ?? '';
        setListing({ for: want, path, parent: parentOf(path), entries: [], truncated: false, error: listingError(status, describeError(e)) });
      });
    return () => {
      live = false;
    };
  }, [want]);

  // The listbox takes focus so the arrow keys work at once; on a phone the picker scrolls into view.
  useEffect(() => {
    list.current?.focus({ preventScroll: true });
    root.current?.scrollIntoView({ block: 'nearest' });
  }, []);

  useEffect(() => {
    list.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [selIndex, listing]);

  useEffect(() => {
    if (creating) nameInput.current?.focus();
  }, [creating]);

  function open(path: string) {
    setWant({ path, hidden: want.hidden });
    setSelected(null);
    setCreating(false);
    setCreateError(null);
    list.current?.focus();
  }

  function up() {
    if (listing?.parent) open(listing.parent);
  }

  function choose() {
    if (target) onUse(target);
  }

  function toggleHidden() {
    const hidden = !want.hidden;
    if (!hidden && entries[selIndex]?.hidden) setSelected(null);
    setWant({ ...want, hidden, fallback: false });
  }

  function startCreate() {
    setNewName('');
    setCreateError(null);
    setCreating(true);
  }

  function cancelCreate() {
    setCreating(false);
    setCreateError(null);
    list.current?.focus();
  }

  async function create() {
    const name = newName.trim();
    if (!name || !listing || loading || createBusy) return;
    const at = want;
    setCreateBusy(true);
    setCreateError(null);
    try {
      const { path } = await api.makeDir(listing.path, name);
      setCreating(false);
      // Select the new folder only while the user is still in the folder it was made in; a move made meanwhile stands.
      if (latest.current.path === at.path) {
        setSelected(path);
        setWant({ path: at.path, hidden: latest.current.hidden || name.startsWith('.') });
      }
      list.current?.focus();
    } catch (e) {
      setCreateError(isStatus(e, 409) ? 'A folder with this name already exists' : describeError(e));
    } finally {
      setCreateBusy(false);
    }
  }

  function onRootKey(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      choose();
    } else if (e.key === 'Escape') {
      // Only the picker closes; the event must not reach the dialog's document listener.
      e.preventDefault();
      e.stopPropagation();
      onClose();
    }
  }

  function onListKey(e: KeyboardEvent<HTMLDivElement>) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    switch (e.key) {
      case 'ArrowDown':
      case 'ArrowUp':
      case 'Home':
      case 'End': {
        e.preventDefault();
        if (entries.length === 0) return;
        const last = entries.length - 1;
        const next = e.key === 'Home' ? 0 : e.key === 'End' ? last : e.key === 'ArrowDown' ? Math.min(selIndex + 1, last) : Math.max(selIndex - 1, 0);
        setSelected(entries[next].path);
        return;
      }
      case 'Enter':
        e.preventDefault();
        if (selIndex >= 0) open(entries[selIndex].path);
        return;
      case 'Backspace':
        e.preventDefault();
        up();
        return;
    }
    if (e.key.length !== 1) return;
    e.preventDefault();
    // Keys within TYPEAHEAD_MS of each other extend the prefix; the event's own clock is enough.
    const extending = typed.current.text !== '' && e.timeStamp - typed.current.at < TYPEAHEAD_MS;
    typed.current = { text: extending ? typed.current.text + e.key : e.key, at: e.timeStamp };
    const hit = matchFrom(
      entries.map((x) => x.name),
      extending ? selIndex - 1 : selIndex,
      typed.current.text,
    );
    if (hit >= 0) setSelected(entries[hit].path);
  }

  /** The option is one button. Its chevron is a hit region, not a control: a press there opens the folder, anywhere else selects it. */
  function onRowClick(e: MouseEvent<HTMLButtonElement>, path: string) {
    if ((e.target as HTMLElement).closest('[data-opens]')) {
      open(path);
      return;
    }
    setSelected(path);
    list.current?.focus();
  }

  const markClass = 'flex shrink-0 items-center gap-1 text-caption text-muted';
  const statusClass = 'shrink-0 px-3 py-3 text-caption text-muted';

  return (
    // eslint-disable-next-line jsx-a11y/no-static-element-interactions -- every control inside is focusable; this only relays Ctrl/Cmd+Enter and Escape from them.
    <div ref={root} id={id} className="flex flex-col gap-2 animate-fade-in" onKeyDown={onRootKey}>
      <div className="flex items-start gap-2">
        <nav aria-label="Folder path" className="min-w-0 flex-1 pt-0.5">
          <ol className="flex flex-wrap items-center gap-y-0.5 text-caption">
            {crumbs.map((c, i) => {
              const last = i === crumbs.length - 1;
              return (
                <li key={c.path} className="flex min-w-0 items-center">
                  {i > 1 && (
                    <span aria-hidden="true" className="px-0.5 text-faint">
                      /
                    </span>
                  )}
                  <button
                    type="button"
                    aria-current={last ? 'location' : undefined}
                    aria-label={i === 0 ? 'Root' : undefined}
                    className={cn('h-6 max-w-full truncate rounded-xs px-1 transition-colors duration-100 hover:bg-tint-hover hover:text-ink pointer-coarse:min-h-11', last ? 'text-ink' : 'text-muted')}
                    title={c.name}
                    onClick={() => open(c.path)}
                  >
                    {c.name}
                  </button>
                </li>
              );
            })}
          </ol>
        </nav>
        <Button size="sm" aria-pressed={want.hidden} className="shrink-0 text-muted" onClick={toggleHidden}>
          {want.hidden ? <Eye /> : <EyeOff />}
          Show hidden
        </Button>
      </div>

      <div className="flex h-picker flex-col overflow-hidden rounded-sm bg-canvas max-sm:h-picker-phone">
        {creating && (
          <div className="shrink-0 p-1.5 pl-3">
            <div className="flex items-center gap-2">
              <FolderPlus aria-hidden="true" className="size-4 shrink-0 text-muted" />
              <Input
                ref={nameInput}
                aria-label="New folder name"
                aria-invalid={createError ? true : undefined}
                aria-describedby={createError ? `${id}-create-error` : undefined}
                className="min-w-0 flex-1 text-ui"
                placeholder="Folder name"
                spellCheck={false}
                autoComplete="off"
                value={newName}
                disabled={createBusy}
                onChange={(e) => setNewName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !(e.ctrlKey || e.metaKey)) {
                    e.preventDefault(); // not the form's submit
                    void create();
                  } else if (e.key === 'Escape') {
                    e.preventDefault();
                    e.stopPropagation(); // cancels the row, not the picker or the dialog
                    cancelCreate();
                  }
                }}
              />
              <Button size="icon" variant="subtle" aria-label="Create folder" disabled={loading || createBusy || !newName.trim()} onClick={() => void create()}>
                <Check />
              </Button>
              <Button size="icon" variant="subtle" aria-label="Cancel new folder" disabled={createBusy} onClick={cancelCreate}>
                <X />
              </Button>
            </div>
            {createError && (
              <Note id={`${id}-create-error`} tone="error" role="alert" className="pt-1.5 pl-6">
                {createError}
              </Note>
            )}
          </div>
        )}
        {loading && !listing && <Loading className="shrink-0 px-2" />}
        {listing?.error && (
          <p role="status" className={statusClass}>
            {listing.error}
          </p>
        )}
        {listing && !listing.error && !loading && entries.length === 0 && <p className={statusClass}>No folders here</p>}
        <div
          ref={list}
          id={`${id}-list`}
          role="listbox"
          aria-label="Folders"
          aria-busy={loading || undefined}
          aria-activedescendant={selIndex >= 0 ? `${id}-opt-${selIndex}` : undefined}
          tabIndex={0}
          className="min-h-0 flex-1 overflow-y-auto p-1 focus-visible:-outline-offset-2"
          onKeyDown={onListKey}
        >
          {entries.map((e, i) => {
            const sel = i === selIndex;
            return (
              <button
                key={e.path}
                type="button"
                role="option"
                id={`${id}-opt-${i}`}
                aria-selected={sel}
                tabIndex={-1}
                className={cn(
                  'flex h-8 w-full cursor-default items-center gap-2 rounded-sm pr-0 pl-2 text-left outline-hidden transition-[background-color,color,box-shadow] duration-100 select-none pointer-coarse:h-11',
                  sel ? 'bg-raised text-ink shadow-raised' : 'text-body hover:bg-surface',
                  e.hidden && !sel && 'text-muted',
                  loading && 'opacity-60',
                )}
                onMouseDown={(ev) => ev.preventDefault()}
                onClick={(ev) => onRowClick(ev, e.path)}
                onDoubleClick={() => open(e.path)}
              >
                <Folder aria-hidden="true" className="size-4 shrink-0 text-muted" />
                <span className="min-w-0 flex-1 truncate text-ui" title={e.name}>{e.name}</span>
                {e.git && (
                  <span className={markClass}>
                    <GitBranch aria-hidden="true" className="size-3" />
                    git
                  </span>
                )}
                {e.link && (
                  <span className={markClass}>
                    <Link aria-hidden="true" className="size-3" />
                    link
                  </span>
                )}
                <span
                  data-opens=""
                  aria-hidden="true"
                  className="flex size-8 shrink-0 items-center justify-center rounded-xs text-muted transition-colors duration-100 hover:bg-tint-hover hover:text-ink pointer-coarse:size-11"
                >
                  <ChevronRight className="size-4" />
                </span>
              </button>
            );
          })}
        </div>
        {listing?.truncated && <p className={cn(statusClass, 'py-1.5')}>Showing the first 1,000</p>}
      </div>

      <div className="flex flex-wrap items-center gap-2 max-sm:[&>button]:flex-1">
        <Button variant="subtle" disabled={!listing?.parent} onClick={up}>
          <FolderUp />
          Up
        </Button>
        <Button variant="subtle" disabled={loading || !listing || !!listing.error || creating} onClick={startCreate}>
          <FolderPlus />
          New folder
        </Button>
        {/* The folder "Use this folder" takes. RTL direction puts the ellipsis at the start, so the folder's own name stays visible; <bdi> keeps the path itself left-to-right. */}
        <span dir="rtl" className="min-w-0 flex-1 truncate text-caption text-muted max-sm:order-first max-sm:basis-full max-sm:text-left" title={target}>
          <bdi>{target}</bdi>
        </span>
        <Button variant="secondary" disabled={!target} onClick={choose}>
          Use this folder
        </Button>
      </div>
    </div>
  );
}
