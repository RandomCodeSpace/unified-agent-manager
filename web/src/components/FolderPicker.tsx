import { Check, ChevronRight, Eye, EyeOff, Folder, FolderPlus, FolderUp, GitBranch, Link, X } from 'lucide-react';
import { useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { ApiError, api, describeError, isStatus, type DirList } from '../api';
import { cn } from '../lib/cn';
import { breadcrumbs, cleanPath, listingError, matchFrom, parentOf, visibleFolders } from '../lib/folders';
import { Note } from './common';
import { Button } from './ui/button';

/** One listing request. A fresh object each time, so the same folder can be listed again after a folder is created in it. */
interface Want {
  /** Absent: the service user's home. */
  path?: string;
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
 * folder** takes the selected row, else the folder being shown. Focus stays on the listbox
 * (`aria-activedescendant`): arrows move, Enter opens, Backspace goes up, typing jumps to a
 * name, Ctrl/Cmd+Enter uses. Escape cancels the New folder row first, then closes the picker;
 * the dialog stays open either way.
 */
export function FolderPicker({ id, start, onUse, onClose }: { id: string; start: string; onUse: (path: string) => void; onClose: () => void }) {
  const root = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const nameInput = useRef<HTMLInputElement>(null);
  const typed = useRef({ text: '', at: 0 });
  const [want, setWant] = useState<Want>(() => ({ path: cleanPath(start) || undefined, fallback: true }));
  const [listing, setListing] = useState<Listing | null>(null);
  const [showHidden, setShowHidden] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [createBusy, setCreateBusy] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  const loading = listing?.for !== want;
  const entries = listing ? visibleFolders(listing.entries, showHidden) : [];
  const selIndex = entries.findIndex((e) => e.path === selected);
  const target = selIndex >= 0 ? entries[selIndex].path : listing?.path;
  const hiddenCount = listing ? listing.entries.length - visibleFolders(listing.entries, false).length : 0;
  const crumbs = breadcrumbs(listing?.path ?? '');

  useEffect(() => {
    let live = true;
    api
      .listDirs(want.path)
      .then((l) => live && setListing({ ...l, for: want }))
      .catch((e: unknown) => {
        if (!live) return;
        const status = e instanceof ApiError ? e.status : 0;
        // Not a folder (400, 404): start at home instead. A folder that exists but cannot be read says so.
        if (want.fallback && status !== 403) {
          setWant({});
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
    setWant({ path });
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
    if (!name || !listing || createBusy) return;
    setCreateBusy(true);
    setCreateError(null);
    try {
      const { path } = await api.makeDir(listing.path, name);
      setCreating(false);
      if (name.startsWith('.')) setShowHidden(true);
      setSelected(path);
      setWant({ path: listing.path });
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

  const markClass = 'flex shrink-0 items-center gap-1 text-caption text-muted';

  return (
    // eslint-disable-next-line jsx-a11y/no-static-element-interactions -- every control inside is focusable; this only relays Ctrl/Cmd+Enter and Escape from them.
    <div ref={root} id={id} className="flex flex-col gap-2 animate-fade-in" onKeyDown={onRootKey}>
      <div className="flex items-start gap-2">
        <nav aria-label="Folder path" className="min-w-0 flex-1 pt-0.5">
          <ol className="flex flex-wrap items-center gap-y-0.5 font-mono text-code-sm">
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
                    className={cn('h-6 max-w-full truncate rounded-xs px-1 transition-colors duration-100 hover:bg-canvas hover:text-ink', last ? 'text-ink' : 'text-muted')}
                    onClick={() => open(c.path)}
                  >
                    {c.name}
                  </button>
                </li>
              );
            })}
          </ol>
        </nav>
        <Button size="sm" aria-pressed={showHidden} className="-mr-2 shrink-0 text-muted" onClick={() => setShowHidden(!showHidden)}>
          {showHidden ? <Eye /> : <EyeOff />}
          Show hidden
        </Button>
      </div>

      <div className="flex h-[min(320px,40dvh)] flex-col overflow-hidden rounded-sm bg-canvas max-sm:h-[55dvh]">
        {creating && (
          <div className="border-b border-hairline p-1">
            <div className="flex items-center gap-2 pl-1">
              <FolderPlus aria-hidden="true" className="size-4 shrink-0 text-muted" />
              <input
                ref={nameInput}
                aria-label="New folder name"
                aria-invalid={createError ? true : undefined}
                aria-describedby={createError ? `${id}-create-error` : undefined}
                className="h-7 min-w-0 flex-1 rounded-xs border border-hairline-strong bg-raised px-2 font-mono text-code text-ink outline-hidden transition-colors placeholder:text-muted focus:border-accent disabled:opacity-45 pointer-coarse:h-9"
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
              <Button size="icon" variant="subtle" aria-label="Create folder" className="hover:bg-surface" disabled={createBusy || !newName.trim()} onClick={() => void create()}>
                <Check />
              </Button>
              <Button size="icon" variant="subtle" aria-label="Cancel new folder" className="hover:bg-surface" disabled={createBusy} onClick={cancelCreate}>
                <X />
              </Button>
            </div>
            {createError && (
              <Note id={`${id}-create-error`} tone="error" role="alert" className="px-1 pt-1">
                {createError}
              </Note>
            )}
          </div>
        )}
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
          {loading && !listing && (
            <div aria-hidden="true" className="flex flex-col gap-1 p-1">
              <span className="h-6 w-2/3 rounded-sm bg-surface animate-pulse-dot" />
              <span className="h-6 w-1/2 rounded-sm bg-surface animate-pulse-dot" />
              <span className="h-6 w-3/5 rounded-sm bg-surface animate-pulse-dot" />
            </div>
          )}
          {listing?.error && (
            <p role="status" className="px-2 py-3 text-caption text-muted">
              {listing.error}
            </p>
          )}
          {listing && !listing.error && !loading && entries.length === 0 && (
            <p className="px-2 py-3 text-caption text-muted">{hiddenCount > 0 ? `No folders here except ${hiddenCount} hidden` : 'No folders here'}</p>
          )}
          {entries.map((e, i) => {
            const sel = i === selIndex;
            return (
              // The option is a button (native keyboard semantics); the chevron that opens the folder sits beside it, since a button cannot hold one.
              <div
                key={e.path}
                className={cn(
                  'flex items-center rounded-sm pr-1 transition-[background-color,color,box-shadow] duration-100',
                  sel ? 'bg-raised text-ink shadow-raised' : 'text-body hover:bg-surface',
                  e.hidden && !sel && 'text-muted',
                  loading && 'opacity-60',
                )}
              >
                <button
                  type="button"
                  role="option"
                  id={`${id}-opt-${i}`}
                  aria-selected={sel}
                  tabIndex={-1}
                  className="flex h-8 min-w-0 flex-1 cursor-default items-center gap-2 pl-2 text-left outline-hidden select-none pointer-coarse:h-11"
                  onMouseDown={(ev) => ev.preventDefault()}
                  onClick={() => {
                    setSelected(e.path);
                    list.current?.focus();
                  }}
                  onDoubleClick={() => open(e.path)}
                >
                  <Folder aria-hidden="true" className="size-4 shrink-0 text-muted" />
                  <span className="min-w-0 flex-1 truncate font-mono text-code">{e.name}</span>
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
                </button>
                <button
                  type="button"
                  tabIndex={-1}
                  aria-label={`Open ${e.name}`}
                  className="flex size-7 shrink-0 items-center justify-center rounded-xs text-faint transition-colors duration-100 hover:bg-canvas hover:text-ink pointer-coarse:size-10"
                  onMouseDown={(ev) => ev.preventDefault()}
                  onClick={() => open(e.path)}
                >
                  <ChevronRight aria-hidden="true" className="size-4" />
                </button>
              </div>
            );
          })}
          {listing?.truncated && <p className="px-2 py-1.5 text-caption text-muted">Showing the first 1,000</p>}
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2 max-sm:[&>button]:flex-1">
        <Button variant="subtle" disabled={!listing?.parent} onClick={up}>
          <FolderUp />
          Up
        </Button>
        <Button variant="subtle" disabled={!listing || !!listing.error || creating} onClick={startCreate}>
          <FolderPlus />
          New folder
        </Button>
        {/* The folder "Use this folder" takes. RTL direction puts the ellipsis at the start, so the folder's own name stays visible; <bdi> keeps the path itself left-to-right. */}
        <span dir="rtl" className="min-w-0 flex-1 truncate font-mono text-code-sm text-muted max-sm:order-first max-sm:basis-full max-sm:text-left" title={target}>
          <bdi>{target}</bdi>
        </span>
        <Button variant="secondary" disabled={!target} onClick={choose}>
          Use this folder
        </Button>
      </div>
    </div>
  );
}
