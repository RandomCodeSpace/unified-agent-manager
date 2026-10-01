import { Copy, Ellipsis, FileDiff, MessageSquarePlus, RefreshCw, ShieldAlert, X } from 'lucide-react';
import { useEffect, useEffectEvent, useMemo, useRef, useState } from 'react';
import { parsePatch, structuredPatch, type StructuredPatch } from 'diff';
import { LIVE, api, describeError, newRequestId, readOnly, type ChangeFile, type Changes as ChangesData, type FileDiff as FileDiffData, type Scope, type SessionSummary } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { CommitPanel } from './CommitPanel';
import { LARGE_CHANGE, byRisk, commentsMessage, emptyReview, fileDigest, parseReview, reviewKey, riskOf, serializeReview, statusLetter, viewState, type Review, type ReviewComment } from '../lib/review';
import { Note, Skeleton } from './common';
import { PanelHeader, SidePanel } from './Subagents';
import { Button } from './ui/button';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
import { Segmented } from './ui/segmented';
import { Tip } from './ui/tooltip';

/** Scope the header counts: the provider's own diff when it has one, else the files this Task's agent edited. */
export function defaultScope(s: SessionSummary): Scope {
  return s.capabilities.session_diff ? 'session' : 'task';
}

const STATUS_TONE: Record<string, string> = { A: 'text-success', U: 'text-success', D: 'text-error', '!': 'text-error' };

const EMPTY: Record<Scope, string> = {
  task: 'No changes from this task: it has not edited a file that still differs from HEAD.',
  turn: 'The latest turn edited no file that still differs from HEAD.',
  workspace: 'No uncommitted changes.',
  session: 'No changes.',
};

function readReview(id: string): Review {
  try {
    return parseReview(localStorage.getItem(reviewKey(id)));
  } catch {
    return emptyReview();
  }
}

function writeReview(id: string, r: Review) {
  try {
    const raw = serializeReview(r);
    if (raw) localStorage.setItem(reviewKey(id), raw);
    else localStorage.removeItem(reviewKey(id));
  } catch {
    // Storage full or unavailable: the review lasts until reload.
  }
}

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
}: Readonly<{
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
}>) {
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
  let error: string | null = null;
  if (current) error = current.error;
  else if (isDefault) error = changesError;
  // Risky files first, so the default selection is the one most worth a look.
  const files = useMemo(() => byRisk(data?.supported ? data.files : []), [data]);
  const shownPath = path && files.some((f) => f.path === path) ? path : (files[0]?.path ?? null);
  const adds = files.reduce((n, f) => n + f.additions, 0);
  const dels = files.reduce((n, f) => n + f.deletions, 0);
  const label = data ? data.label : `${projectName} vs HEAD`;
  // Every git listing carries all three counts; the Task's own listing stands in while another loads.
  const counts = data?.counts ?? changes?.counts;
  const count = (key: 'task' | 'turn' | 'workspace') => (counts ? ` · ${counts[key]}` : '');

  // Viewed marks and line comments, per Task in localStorage; every change writes through.
  const [stored, setStored] = useState(() => ({ id: session.id, review: readReview(session.id) }));
  const review = stored.id === session.id ? stored.review : readReview(session.id);
  const updateReview = (change: (r: Review) => Review) => {
    const next = change(review);
    writeReview(session.id, next);
    setStored({ id: session.id, review: next });
  };
  const viewedCount = files.filter((f) => viewState(review, f) === 'viewed').length;
  const toggleViewed = (f: ChangeFile, viewed: boolean) => updateReview((r) => {
    const marks = { ...r.viewed };
    if (viewed) marks[f.path] = fileDigest(f);
    else delete marks[f.path];
    return { ...r, viewed: marks };
  });
  const commentable = !readOnly(session);
  const comments = review.comments;
  const working = LIVE.includes(session.state);
  const [sending, setSending] = useState(false);
  const [sendNote, setSendNote] = useState<{ tone: 'error' | 'info'; text: string } | null>(null);
  // A retry of the same batch reuses its request ID, so the service never takes it twice.
  const pendingSend = useRef<{ text: string; id: string } | null>(null);
  const sendComments = () => {
    const batch = comments;
    const text = commentsMessage(batch);
    if (pendingSend.current?.text !== text) pendingSend.current = { text, id: newRequestId() };
    setSending(true);
    setSendNote(null);
    // Queue mode sends at once to an idle Task and waits for the end of a running turn.
    api.prompt(session.id, text, pendingSend.current.id, 'queue').then((sub) => {
      if (sub.status !== 'accepted' && sub.status !== 'queued') {
        setSendNote({ tone: 'error', text: sub.error || 'The task did not take the comments.' });
        return;
      }
      pendingSend.current = null;
      const sent = new Set(batch.map((c) => c.id));
      updateReview((r) => ({ ...r, comments: r.comments.filter((c) => !sent.has(c.id)) }));
      setSendNote({ tone: 'info', text: sub.status === 'queued' ? 'Comments queued: they go out after this turn.' : 'Comments sent to the task.' });
    }).catch((e: unknown) => {
      setSendNote({ tone: 'error', text: `Could not send the comments: ${describeError(e)}` });
    }).finally(() => setSending(false));
  };
  const addComment = (c: Omit<ReviewComment, 'id'>) => {
    setSendNote(null);
    updateReview((r) => ({ ...r, comments: [...r.comments, { ...c, id: newRequestId() }] }));
  };
  const removeComment = (id: string) => updateReview((r) => ({ ...r, comments: r.comments.filter((c) => c.id !== id) }));
  const shownFile = files.find((f) => f.path === shownPath);

  const stop = useEffectEvent(() => {
    request.current?.abort();
    request.current = null;
  });
  const refresh = useEffectEvent((mode: 'read' | 'initial' | 'known' | 'selection' = 'read') => {
    if (!open || !active || document.visibilityState !== 'visible' || request.current) return;
    let listing: ChangesData | null = null;
    if (mode === 'selection') listing = data;
    else if (mode !== 'read' && isDefault) listing = changes;
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
      selected = path && entries.some(file => file.path === path) ? path : (byRisk(entries)[0]?.path ?? null);
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
    const schedule = () => { timer = window.setInterval(() => { refresh(); }, 5000); };
    const visibility = () => {
      window.clearInterval(timer);
      timer = undefined;
      if (document.visibilityState === 'visible') {
        refresh();
        schedule();
      } else stop();
    };
    if (document.visibilityState === 'visible') {
      refresh('initial');
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
    refresh('known');
  }, [changes]);
  useEffect(() => { if (tick) refresh(); }, [tick]);
  useEffect(() => {
    if (!path) return;
    stop();
    refresh('selection');
  }, [path]);

  return (
    <SidePanel id="changes" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Changes" defaultWidth={440}>
      <PanelHeader>
        <FileDiff aria-hidden="true" className="size-4 text-muted" />
        <span className="text-title text-ink">Changes</span>
        {data?.supported && (
          <span className="min-w-0 truncate text-caption tabular-nums text-muted">
            {files.length} {files.length === 1 ? 'file' : 'files'}{adds > 0 && <> · <span className="text-success">+{adds}</span></>}{dels > 0 && <> <span className="text-error">−{dels}</span></>}
            {files.length > 0 && <> · {viewedCount} of {files.length} viewed</>}
          </span>
        )}
        <span className="flex-1" />
        <Tip label="Refresh">
          <Button size="icon-md" aria-label="Refresh" className="text-muted" onClick={() => setTick(t => t + 1)}>
            <RefreshCw />
          </Button>
        </Tip>
        <Button ref={closeRef} size="icon-md" aria-label="Close changes" className="text-muted" onClick={onClose}>
          <X />
        </Button>
      </PanelHeader>
      <div className="shrink-0 px-3 pb-1.5">
        <Segmented
          size="sm"
          aria-label="Scope"
          className="w-full"
          value={scope}
          onValueChange={(s) => setScope(s as Scope)}
          items={canSession ? [
            { value: 'session', label: 'This task' },
            { value: 'workspace', label: 'All changes' },
          ] : [
            { value: 'task', label: <span className="tabular-nums">This task{count('task')}</span> },
            { value: 'turn', label: <span className="tabular-nums">Last turn{count('turn')}</span> },
            { value: 'workspace', label: <span className="tabular-nums">All changes{count('workspace')}</span> },
          ]}
        />
      </div>
      <p className="shrink-0 px-3 py-1 text-caption leading-relaxed text-muted" title={label}>
        {scope === 'workspace' ? 'All uncommitted project changes vs HEAD, from any task or source.' : label}
      </p>
      {data?.supported && data.reason && <Note tone="warn" className="shrink-0 px-3 pb-1">Note: {data.reason}.</Note>}
      {adds + dels > LARGE_CHANGE && (
        <Note tone="warn" className="shrink-0 px-3 pb-1">Large change: {adds + dels} lines. Reviews catch the most under about {LARGE_CHANGE} lines, so go file by file and mark each one viewed.</Note>
      )}
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
            <Note>{EMPTY[scope]}</Note>
          </li>
        )}
        {files.map((f) => (
          <FileRow key={f.path} file={f} selected={f.path === shownPath} viewed={viewState(review, f)} onOpen={() => setPath(f.path)} onViewed={(v) => toggleViewed(f, v)} />
        ))}
      </ul>
      <div className="fade-rule mx-3 shrink-0" aria-hidden="true" />
      <div className="@container min-h-0 flex-1 overflow-auto">
        {shownPath && (
          <FileView
            key={shownPath}
            path={shownPath}
            file={current?.path === shownPath ? current.file : null}
            error={current?.path === shownPath ? current.fileError : null}
            viewed={shownFile ? viewState(review, shownFile) === 'viewed' : undefined}
            onViewed={shownFile ? (v: boolean) => toggleViewed(shownFile, v) : undefined}
            comments={comments.filter((c) => c.path === shownPath)}
            onComment={commentable ? addComment : undefined}
            onRemoveComment={removeComment}
          />
        )}
      </div>
      {commentable && (comments.length > 0 || sendNote) && (
        <CommentBatch comments={comments} working={working} sending={sending} note={sendNote} onOpen={setPath} onRemove={removeComment} onSend={sendComments} />
      )}
      <div className="fade-rule mx-3 shrink-0" aria-hidden="true" />
      <CommitPanel session={session} onChanged={() => setTick(t => t + 1)} />
    </SidePanel>
  );
}

/** The collected line comments and the one action that sends them to the Task as a single message. */
function CommentBatch({ comments, working, sending, note, onOpen, onRemove, onSend }: Readonly<{
  comments: ReviewComment[];
  working: boolean;
  sending: boolean;
  note: { tone: 'error' | 'info'; text: string } | null;
  onOpen: (path: string) => void;
  onRemove: (id: string) => void;
  onSend: () => void;
}>) {
  const n = comments.length;
  return (
    <section aria-label="Review comments" className="shrink-0">
      <div className="fade-rule mx-3" aria-hidden="true" />
      {n > 0 && (
        <ul className="max-h-32 overflow-y-auto px-1 pt-1">
          {comments.map((c) => (
            <li key={c.id} className="flex items-center gap-1 text-caption">
              <button type="button" className="min-w-0 flex-1 truncate rounded-xs px-2 py-1 text-left text-body hover:bg-tint-hover pointer-coarse:min-h-11" title={c.body} onClick={() => onOpen(c.path)}>
                <span className="font-mono text-code-sm text-ink">{c.path.split('/').at(-1)}:{c.line}</span> {c.body}
              </button>
              <Button size="icon-sm" aria-label={`Remove comment on ${c.path} line ${c.line}`} className="text-muted" onClick={() => onRemove(c.id)}>
                <X />
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 py-2">
        {n > 0 && (
          <span className="min-w-0 flex-1 text-caption text-muted">{working ? 'Goes out after this turn, as one message.' : 'Goes to the task as one message.'}</span>
        )}
        {note && <Note tone={note.tone} role={note.tone === 'error' ? 'alert' : 'status'} className="min-w-0 flex-1">{note.text}</Note>}
        {n > 0 && (
          <Button variant="primary" size="md" loading={sending} onClick={onSend}>
            Send {n} {n === 1 ? 'comment' : 'comments'} to the task
          </Button>
        )}
      </div>
    </section>
  );
}

function FileRow({ file: f, selected, viewed, onOpen, onViewed }: Readonly<{ file: ChangeFile; selected: boolean; viewed: 'viewed' | 'changed' | 'unviewed'; onOpen: () => void; onViewed: (viewed: boolean) => void }>) {
  const [, copy] = useCopied();
  const items: ActionItem[] = [
    { key: 'open', label: 'Open diff', icon: <FileDiff />, onSelect: onOpen },
    { key: 'copy', label: 'Copy path', icon: <Copy />, onSelect: () => copy(f.path) },
  ];
  const risk = riskOf(f.path);
  const letter = statusLetter(f.status);
  return (
    <li className="flex items-center">
      <label className="flex size-8 shrink-0 cursor-pointer items-center justify-center pointer-coarse:size-11" title={viewed === 'changed' ? 'Changed since you viewed it' : 'Viewed'}>
        <input type="checkbox" className="size-3.5 cursor-pointer accent-success" aria-label={`Viewed ${f.path}`} checked={viewed === 'viewed'} onChange={(e) => onViewed(e.target.checked)} />
      </label>
      <ContextMenu.Root>
        <ContextMenu.Trigger render={<div className="group/file relative min-w-0 flex-1" />}>
          <button
            type="button"
            aria-pressed={selected}
            title={f.path}
            className={cn('grid min-h-8 w-full grid-cols-[max-content_minmax(0,1fr)_auto] items-center gap-2 rounded-sm py-1 pr-9 pl-2 text-left text-caption transition-colors focus-visible:-outline-offset-2 pointer-coarse:min-h-11 pointer-coarse:pr-12', selected ? 'bg-raised text-ink shadow-raised' : 'text-body hover:bg-tint-hover')}
            onClick={onOpen}
          >
            <span className={cn('w-3 text-center font-mono font-semibold', STATUS_TONE[letter] ?? 'text-muted')} title={f.status}>{letter}</span>
            <span className="min-w-0">
              <span className="block truncate [direction:rtl] text-left [unicode-bidi:plaintext]">{f.path}</span>
              {(risk || viewed === 'changed') && (
                <span className="flex flex-wrap items-center gap-x-2 text-meta">
                  {risk && <span className="inline-flex items-center gap-1 text-error" title={risk.detail}><ShieldAlert aria-hidden="true" className="size-3" />{risk.label}</span>}
                  {viewed === 'changed' && <span className="text-warning">Changed since you viewed</span>}
                </span>
              )}
            </span>
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

/** Where a comment goes: one line on one side of the diff, with its text when the comment was started. */
type Anchor = Pick<ReviewComment, 'line' | 'side' | 'code'>;

function FileView({ path, file, error, viewed, onViewed, comments = [], onComment, onRemoveComment }: Readonly<{
  path: string;
  file: FileDiffData | null;
  error: string | null;
  viewed?: boolean;
  onViewed?: (viewed: boolean) => void;
  comments?: ReviewComment[];
  /** Absent where comments cannot be sent (a settled or archived Task). */
  onComment?: (comment: Omit<ReviewComment, 'id'>) => void;
  onRemoveComment?: (id: string) => void;
}>) {
  const patch = useMemo<StructuredPatch | null | Error>(() => {
    if (!file) return null;
    try {
      if (file.patch) return parsePatch(file.patch)[0] ?? null;
      return structuredPatch(file.path, file.path, file.before ?? '', file.after ?? '');
    } catch (e) {
      return e instanceof Error ? e : new Error(String(e));
    }
  }, [file]);
  const [draft, setDraft] = useState<Anchor | null>(null);

  const warning = error ? <Note tone="error" role="alert" className="p-3">{error}</Note> : null;
  if (!file) return warning ?? <Skeleton label="Loading the diff…" rows={6} className="gap-2 p-3" />;
  if (patch instanceof Error) return <>{warning}<Note tone="error" className="p-3">Could not parse diff: {patch.message}</Note></>;
  if (!patch || patch.hunks.length === 0) return <>{warning}<Note className="p-3">No textual changes in {path}.</Note></>;

  const lines: LineNotes = {
    comments,
    draft,
    open: onComment ? (anchor) => {
      // A click that ends a text selection is for copying, not commenting.
      if (globalThis.getSelection?.()?.toString()) return;
      setDraft(anchor);
    } : undefined,
    save: (body) => {
      if (draft && onComment) onComment({ path, ...draft, body });
      setDraft(null);
    },
    cancel: () => setDraft(null),
    remove: onRemoveComment,
  };
  return (
    <>
      {warning}
      <table className="diff animate-fade-in" translate="no">
        <caption>
          <span className="sticky left-3 flex w-[calc(100cqw-24px)] items-center gap-2">
            <span className="min-w-0 flex-1 truncate">{file.path}</span>
            {onViewed && (
              <label className="flex shrink-0 cursor-pointer items-center gap-1.5 font-sans text-caption pointer-coarse:min-h-11">
                <input type="checkbox" className="size-3.5 cursor-pointer accent-success" checked={!!viewed} onChange={(e) => onViewed(e.target.checked)} />
                Viewed
              </label>
            )}
          </span>
          {onComment && <span className="sticky left-3 block font-sans text-meta">Select a line to comment on it.</span>}
        </caption>
        <tbody>{patch.hunks.flatMap((h, hi) => renderHunk(h, hi, lines))}</tbody>
      </table>
    </>
  );
}

interface LineNotes {
  comments: ReviewComment[];
  draft: Anchor | null;
  open?: (anchor: Anchor) => void;
  save: (body: string) => void;
  cancel: () => void;
  remove?: (id: string) => void;
}

function renderHunk(h: StructuredPatch['hunks'][number], hi: number, notes?: LineNotes) {
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
    const anchor: Anchor | null = right !== '' ? { side: 'new', line: right, code: text } : left !== '' ? { side: 'old', line: left, code: text } : null;
    const open = anchor && notes?.open;
    const label = anchor && `Comment on ${anchor.side === 'old' ? 'removed line' : 'line'} ${anchor.line}`;
    rows.push(
      <tr key={`${hi}-${li}`} className={cn(cls, open && 'group/row cursor-pointer')} onClick={open ? () => open(anchor) : undefined}>
        <td className="num">{left}</td>
        <td className="num">
          {open ? (
            <button type="button" aria-label={label ?? undefined} className="group/line inline-flex w-full items-center justify-end gap-0.5 text-inherit focus-visible:-outline-offset-2" onClick={(e) => { e.stopPropagation(); open(anchor); }}>
              <MessageSquarePlus aria-hidden="true" className="size-3 text-accent opacity-0 group-hover/row:opacity-100 group-focus-visible/line:opacity-100" />
              {right}
            </button>
          ) : right}
        </td>
        <td className="code-cell">
          <span className="sign">{sign}</span>
          {text}
        </td>
      </tr>,
    );
    if (!anchor || !notes) return;
    for (const c of notes.comments) {
      if (c.side !== anchor.side || c.line !== anchor.line) continue;
      rows.push(
        <tr key={`c-${c.id}`} className="note">
          <td colSpan={3} className="px-2 py-1 font-sans whitespace-normal">
            <div className="sticky left-2 ml-6 w-[calc(100cqw-40px)] rounded-md bg-raised px-3 py-2 text-caption shadow-raised">
              <p className="whitespace-pre-wrap text-ink">{c.body}</p>
              <p className="mt-1 flex items-center gap-2 text-meta text-muted">
                <span className="flex-1">Your comment · goes out with the batch</span>
                {notes.remove && <Button size="sm" variant="subtle" onClick={() => notes.remove?.(c.id)}>Remove</Button>}
              </p>
            </div>
          </td>
        </tr>,
      );
    }
    if (notes.draft?.side === anchor.side && notes.draft.line === anchor.line) {
      rows.push(
        <tr key={`d-${hi}-${li}`} className="note">
          <td colSpan={3} className="px-2 py-1 font-sans whitespace-normal">
            <CommentBox label={label ?? 'Comment'} onSave={notes.save} onCancel={notes.cancel} />
          </td>
        </tr>,
      );
    }
  });
  return rows;
}

function CommentBox({ label, onSave, onCancel }: Readonly<{ label: string; onSave: (body: string) => void; onCancel: () => void }>) {
  const ref = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    ref.current?.focus({ preventScroll: true });
  }, []);
  const save = () => {
    const body = ref.current?.value.trim() ?? '';
    if (body) onSave(body);
  };
  return (
    <form
      className="sticky left-2 ml-6 flex w-[calc(100cqw-40px)] flex-col gap-1.5 rounded-md bg-raised p-2 shadow-raised"
      onSubmit={(e) => { e.preventDefault(); save(); }}
    >
      <textarea
        ref={ref}
        rows={3}
        aria-label={label}
        placeholder="What should the agent change here?"
        className="w-full resize-y rounded-sm bg-sunken px-2 py-1.5 text-ui text-ink shadow-well placeholder:text-muted focus-visible:bg-raised focus-visible:shadow-focus focus-visible:outline-none"
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); save(); }
          if (e.key === 'Escape') { e.stopPropagation(); onCancel(); }
        }}
      />
      <span className="flex items-center justify-end gap-1.5">
        <Button size="sm" variant="subtle" onClick={onCancel}>Cancel</Button>
        <Button size="sm" variant="primary" type="submit">Add comment</Button>
      </span>
    </form>
  );
}
