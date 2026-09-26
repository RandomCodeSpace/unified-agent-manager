import { Copy, Ellipsis, FileDiff, RefreshCw, X } from 'lucide-react';
import { useEffect, useEffectEvent, useMemo, useRef, useState } from 'react';
import { parsePatch, structuredPatch, type StructuredPatch } from 'diff';
import { api, describeError, type ChangeFile, type Changes as ChangesData, type FileDiff as FileDiffData, type Scope, type SessionSummary } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { Note, Skeleton } from './common';
import { PanelHeader, SidePanel } from './Subagents';
import { Button } from './ui/button';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
import { Segmented } from './ui/segmented';
import { Tip } from './ui/tooltip';

/** Scope the meta line counts: the provider's own diff when it has one, else the working tree. */
export function defaultScope(s: SessionSummary): Scope {
  return s.capabilities.session_diff ? 'session' : 'workspace';
}

const STATUS_TONE: Record<string, string> = { A: 'text-success', D: 'text-error', '?': 'text-success' };

/**
 * The sheet owns one visible list/detail refresh cycle. The Task's event-driven default
 * list seeds it and receives refreshed counts. Polling reads only the selected scope.
 */
export function ChangesSheet({
  session,
  projectName,
  changes,
  changesError,
  isDefaultPending,
  inline,
  open,
  active,
  onChanges,
  onClose,
  onClosed,
}: {
  session: SessionSummary;
  projectName: string;
  changes: ChangesData | null;
  /** Why the default scope's list failed to load; null while it loads or once it has. */
  changesError: string | null;
  /** Read at refresh time so pending Task reads do not need to rerender the panel. */
  isDefaultPending: () => boolean;
  inline: boolean;
  /** False while the sheet leaves; `onClosed` follows, and the owner unmounts it. */
  open: boolean;
  active: boolean;
  onChanges: (changes: ChangesData) => void;
  onClose: () => void;
  onClosed: () => void;
}) {
  const canSession = session.capabilities.session_diff;
  const [scope, setScope] = useState<Scope>(defaultScope(session));
  const [view, setView] = useState<{
    sessionId: string; scope: Scope; list: ChangesData | null; path: string | null;
    file: FileDiffData | null; error: string | null; fileError: string | null;
  } | null>(null);
  const [path, setPath] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const request = useRef<AbortController | null>(null);
  const observedChanges = useRef(changes);
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    closeRef.current?.focus({ preventScroll: true });
  }, []);

  const isDefault = scope === defaultScope(session);
  const current = view?.sessionId === session.id && view.scope === scope ? view : null;
  const data = current?.list ?? (isDefault ? changes : null);
  const error = current ? current.error : isDefault ? changesError : null;
  const files = data?.supported ? data.files : [];
  const shownPath = path && files.some((f) => f.path === path) ? path : (files[0]?.path ?? null);
  const adds = files.reduce((n, f) => n + f.additions, 0);
  const dels = files.reduce((n, f) => n + f.deletions, 0);
  const label = data ? data.label : `${projectName} vs HEAD`;

  const stop = useEffectEvent(() => {
    request.current?.abort();
    request.current = null;
  });
  const refresh = useEffectEvent((mode: 'read' | 'initial' | 'known' | 'selection' = 'read') => {
    if (!open || !active || document.visibilityState !== 'visible' || request.current) return;
    let listing = mode === 'selection' ? data : mode !== 'read' && isDefault ? changes : null;
    // The Task may already be loading the default list. Reuse its response on arrival.
    if (mode === 'initial' && isDefault && !listing) return;
    if (!listing && isDefault && isDefaultPending()) return;
    const controller = new AbortController();
    request.current = controller;
    let selected = shownPath;
    let readingFile = false;
    void Promise.resolve(listing ?? api.changes(session.id, scope, controller.signal)).then(accepted => {
      if (controller.signal.aborted) return null;
      listing = accepted;
      const entries = listing.supported ? listing.files : [];
      selected = path && entries.some(file => file.path === path) ? path : (entries[0]?.path ?? null);
      readingFile = !!selected;
      const next = { sessionId: session.id, scope, list: listing, path: selected,
        file: current?.path === selected ? current.file : null, error: null,
        fileError: current?.path === selected ? current.fileError : null };
      setView(next);
      if (isDefault) {
        observedChanges.current = listing;
        onChanges(listing);
      }
      return selected
        ? api.changeFile(session.id, scope, selected, controller.signal).then(file => ({ ...next, file, fileError: null }))
        : next;
    }).then(next => {
      if (controller.signal.aborted || !next) return;
      setView(next);
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setView({ sessionId: session.id, scope, list: listing ?? data, path: selected,
        file: current?.path === selected ? current.file : null,
        error: readingFile ? null : describeError(error), fileError: readingFile ? describeError(error) : null });
    }).finally(() => {
      if (request.current === controller) request.current = null;
    });
  });

  useEffect(() => {
    if (!open || !active) return;
    let timer: number | undefined;
    const schedule = () => { timer = window.setInterval(() => { void refresh(); }, 5000); };
    const visibility = () => {
      window.clearInterval(timer);
      timer = undefined;
      if (document.visibilityState === 'visible') {
        void refresh();
        schedule();
      } else stop();
    };
    if (document.visibilityState === 'visible') {
      void refresh('initial');
      schedule();
    }
    document.addEventListener('visibilitychange', visibility);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', visibility);
      stop();
    };
  }, [session.id, scope, open, active]);
  useEffect(() => {
    if (changes === observedChanges.current) return;
    observedChanges.current = changes;
    stop();
    void refresh('known');
  }, [changes]);
  useEffect(() => { if (tick) void refresh(); }, [tick]);
  useEffect(() => {
    if (!path) return;
    stop();
    void refresh('selection');
  }, [path]);

  return (
    <SidePanel id="changes" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Changes" defaultWidth={440}>
      <PanelHeader>
        <FileDiff aria-hidden="true" className="size-4 text-muted" />
        <span className="text-title text-ink">Changes</span>
        {data?.supported && (
          <span className="text-caption tabular-nums text-muted">
            {files.length} {files.length === 1 ? 'file' : 'files'}{adds > 0 && <> · <span className="text-success">+{adds}</span></>}{dels > 0 && <> <span className="text-error">−{dels}</span></>}
          </span>
        )}
        <span className="flex-1" />
        {canSession && (
          <Segmented
            size="sm"
            aria-label="Scope"
            value={scope}
            onValueChange={(s) => setScope(s as Scope)}
            items={[
              { value: 'session', label: 'This task' },
              { value: 'workspace', label: 'Workspace' },
            ]}
          />
        )}
        <Tip label="Refresh">
          <Button size="icon-md" aria-label="Refresh" className="text-muted" onClick={() => setTick(t => t + 1)}>
            <RefreshCw />
          </Button>
        </Tip>
        <Button ref={closeRef} size="icon-md" aria-label="Close changes" className="text-muted" onClick={onClose}>
          <X />
        </Button>
      </PanelHeader>
      <p className="shrink-0 px-3 py-1.5 text-caption leading-relaxed text-muted" title={label}>
        {scope === 'workspace' ? 'All uncommitted project changes vs HEAD.' : label}
      </p>
      <ul className="max-h-[40%] shrink-0 overflow-y-auto p-1" aria-busy={!data && !error ? true : undefined}>
        {error && (
          <li className="px-2 py-1">
            <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
              <span className="min-w-0 flex-1">Could not load the changes: {error}</span>
              <Button size="sm" variant="secondary" onClick={() => setTick(t => t + 1)}>
                Retry
              </Button>
            </Note>
          </li>
        )}
        {!data && !error && (
          <li className="px-2 py-1">
            <Skeleton label="Loading the changes…" rows={3} className="gap-2" rowClassName="h-6 w-full" />
          </li>
        )}
        {data && !data.supported && (
          <li className="px-2 py-1">
            <Note>{data.reason || 'Not available for this task.'}</Note>
          </li>
        )}
        {data?.supported && files.length === 0 && (
          <li className="px-2 py-1">
            <Note>No changes.</Note>
          </li>
        )}
        {files.map((f) => (
          <FileRow key={f.path} file={f} selected={f.path === shownPath} onOpen={() => setPath(f.path)} />
        ))}
      </ul>
      <div className="fade-rule mx-3 shrink-0" aria-hidden="true" />
      <div className="min-h-0 flex-1 overflow-auto">{shownPath && <FileView path={shownPath} file={current?.path === shownPath ? current.file : null} error={current?.path === shownPath ? current.fileError : null} />}</div>
    </SidePanel>
  );
}

function FileRow({ file: f, selected, onOpen }: { file: ChangeFile; selected: boolean; onOpen: () => void }) {
  const [, copy] = useCopied();
  const items: ActionItem[] = [
    { key: 'open', label: 'Open diff', icon: <FileDiff />, onSelect: onOpen },
    { key: 'copy', label: 'Copy path', icon: <Copy />, onSelect: () => copy(f.path) },
  ];
  return (
    <li>
      <ContextMenu.Root>
        <ContextMenu.Trigger render={<div className="group/file relative" />}>
          <button
            type="button"
            aria-pressed={selected}
            title={f.path}
            className={cn('grid h-8 w-full grid-cols-[max-content_minmax(0,1fr)_auto] items-center gap-2 rounded-sm pr-9 pl-2 text-left text-caption transition-colors focus-visible:-outline-offset-2 pointer-coarse:min-h-11 pointer-coarse:pr-12', selected ? 'bg-raised text-ink shadow-raised' : 'text-body hover:bg-tint-hover')}
            onClick={onOpen}
          >
            <span className={cn('text-center', STATUS_TONE[f.status] ?? 'text-muted')}>{f.status}</span>
            <span className="truncate [direction:rtl] text-left [unicode-bidi:plaintext]">{f.path}</span>
            <span className="tabular-nums">
              <span className={f.additions ? 'text-success' : 'text-muted'}>+{f.additions}</span> <span className={f.deletions ? 'text-error' : 'text-muted'}>−{f.deletions}</span>
            </span>
          </button>
          <Menu.Root modal={false}>
            <Menu.Trigger render={<Button size="icon-sm" aria-label={`Actions for ${f.path}`} className="absolute top-1/2 right-1 -translate-y-1/2 text-muted opacity-0 transition-opacity group-hover/file:opacity-100 focus-visible:opacity-100 data-open:opacity-100 pointer-coarse:opacity-100" />}>
              <Ellipsis />
            </Menu.Trigger>
            <Menu.Content align="end">
              <Menu.Actions items={items} />
            </Menu.Content>
          </Menu.Root>
        </ContextMenu.Trigger>
        <ContextMenu.Content>
          <ContextMenu.Actions items={items} />
        </ContextMenu.Content>
      </ContextMenu.Root>
    </li>
  );
}

function FileView({ path, file, error }: { path: string; file: FileDiffData | null; error: string | null }) {
  const patch = useMemo<StructuredPatch | null | Error>(() => {
    if (!file) return null;
    try {
      if (file.patch) return parsePatch(file.patch)[0] ?? null;
      return structuredPatch(file.path, file.path, file.before ?? '', file.after ?? '');
    } catch (e) {
      return e instanceof Error ? e : new Error(String(e));
    }
  }, [file]);

  const warning = error ? <Note tone="error" role="alert" className="p-3">{error}</Note> : null;
  if (!file) return warning ?? <Skeleton label="Loading the diff…" rows={6} className="gap-2 p-3" />;
  if (patch instanceof Error) return <>{warning}<Note tone="error" className="p-3">Could not parse diff: {patch.message}</Note></>;
  if (!patch || patch.hunks.length === 0) return <>{warning}<Note className="p-3">No textual changes in {path}.</Note></>;

  return (
    <>
      {warning}
      <table className="diff animate-fade-in" translate="no">
        <caption>{file.path}</caption>
        <tbody>{patch.hunks.flatMap((h, hi) => renderHunk(h, hi))}</tbody>
      </table>
    </>
  );
}

function renderHunk(h: StructuredPatch['hunks'][number], hi: number) {
  let oldNo = h.oldStart;
  let newNo = h.newStart;
  const rows = [
    <tr key={`h${hi}`} className="hunk">
      <td colSpan={3}>{`@@ -${h.oldStart},${h.oldLines} +${h.newStart},${h.newLines} @@`}</td>
    </tr>,
  ];
  h.lines.forEach((line, li) => {
    const sign = line[0] ?? ' ';
    const text = line.slice(1);
    let cls = 'ctx';
    let left: number | '' = '';
    let right: number | '' = '';
    if (sign === '+') {
      cls = 'add';
      right = newNo++;
    } else if (sign === '-') {
      cls = 'del';
      left = oldNo++;
    } else if (sign === '\\') {
      cls = 'meta';
    } else {
      left = oldNo++;
      right = newNo++;
    }
    rows.push(
      <tr key={`${hi}-${li}`} className={cls}>
        <td className="num">{left}</td>
        <td className="num">{right}</td>
        <td className="code-cell">
          <span className="sign">{sign}</span>
          {text}
        </td>
      </tr>,
    );
  });
  return rows;
}
