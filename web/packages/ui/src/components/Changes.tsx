import { useApi } from '../ApiContext';
import { ChevronDown, Copy, Ellipsis, Eye, FileDiff, MessageSquarePlus, RefreshCw, ShieldAlert, Undo2, X } from 'lucide-react';
import { memo, useCallback, useEffect, useEffectEvent, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode, type RefObject } from 'react';
import { diffWordsWithSpace, parsePatch, structuredPatch, type StructuredPatch } from 'diff';
import { LIVE, describeError, newRequestId, readOnly, type ChangeFile, type Changes as ChangesData, type FileDiff as FileDiffData, type SessionSummary } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { hunkText } from '../lib/lens';
import { LARGE_CHANGE, byRisk, commentsMessage, emptyReview, fileDigest, parseReview, reviewKey, riskOf, serializeReview, turnFile, viewState, type Review, type ReviewComment } from '../lib/review';
import { Highlighted, InstanceName, Note, Skeleton } from './common';
import { FileIcon } from './FileIcon';
import { PanelHeader, SidePanel } from './Subagents';
import { Button } from './ui/button';
import { AlertDialog, useConfirm } from './ui/dialog';
import { Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

/** What Changes opens on: a turn's edited files (its first one scrolled to), or one file and line to comment on (Lens). */
export interface ChangesTurn {
  files: string[];
  latest: boolean;
  at: number;
  /** A new-side line of the first file whose comment box opens. */
  line?: number;
}

/** Where Changes was left in a Thread: the next open from the header scrolls back to it. */
export interface ChangesPlace { scrollTop: number }

/** Scrolls `el` back to `top` while its content grows in over the next frames (diff slices, a late image), until it holds or the reader scrolls. */
export function restoreScroll(el: HTMLElement, top: number): () => void {
  let frame = 0, tries = 0, last = -1;
  const apply = () => {
    if (last >= 0 && el.scrollTop !== last) return;
    el.scrollTop = top;
    last = el.scrollTop;
    if (last < top && tries++ < 30) frame = requestAnimationFrame(apply);
  };
  apply();
  return () => cancelAnimationFrame(frame);
}

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

const splitPath = (p: string) => ({ dir: p.slice(0, p.lastIndexOf('/') + 1), name: p.slice(p.lastIndexOf('/') + 1) });

/**
 * This Thread's changes as one scroll of file cards: each file's diff under its own head, folded once viewed, with
 * Revert and Comment on every change. The Task's event-driven list seeds it; while open it reads the list again every
 * 5 s, and a card reads its diff again only when its file's digest moves.
 */
export function ChangesSheet({
  turn = null,
  evidence,
  session,
  changes,
  changesError,
  isDefaultPending,
  inline,
  open,
  active,
  onChanges,
  onClose,
  onClosed,
  onOpenFile,
  memory,
}: Readonly<{
  /** Scroll to this turn's first edited file; with `line`, open a comment there. */
  turn?: ChangesTurn | null;
  evidence?: ReactNode;
  session: SessionSummary;
  changes: ChangesData | null;
  /** Why the list failed to load; null while it loads or once it has. */
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
  /** Opens the whole file in the file viewer (Lens). */
  onOpenFile: (path: string, opener: HTMLElement) => void;
  /** This Thread's last place in Changes, written as the sheet leaves; a turn's ask scrolls to the turn instead. */
  memory?: RefObject<ChangesPlace | null>;
}>) {
  const api = useApi();
  const [view, setView] = useState<{ sessionId: string; list: ChangesData | null; error: string | null } | null>(null);
  const [tick, setTick] = useState(0);
  const request = useRef<AbortController | null>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  useEffect(() => { closeRef.current?.focus({ preventScroll: true }); }, []);

  const current = view?.sessionId === session.id ? view : null;
  const data = current?.list ?? changes;
  const error = current ? current.error : changesError;
  // Risky files first, so the top of the stack is the one most worth a look.
  const files = useMemo(() => byRisk(data?.supported ? data.files : []), [data]);
  const countsKnown = files.every(f => !f.counts_unknown);
  const adds = files.reduce((n, f) => n + f.additions, 0);
  const dels = files.reduce((n, f) => n + f.deletions, 0);

  // Viewed marks and line comments, per Task in localStorage; every change writes through.
  const [stored, setStored] = useState(() => ({ id: session.id, review: readReview(api.cacheKey(session.id)) }));
  const review = stored.id === session.id ? stored.review : readReview(api.cacheKey(session.id));
  // The latest review, for a change that lands later (a comment send resolving): it must not undo marks made meanwhile.
  const latest = useRef(stored);
  const updateReview = useCallback((change: (r: Review) => Review) => {
    const id = session.id;
    const next = change(latest.current.id === id ? latest.current.review : readReview(api.cacheKey(id)));
    writeReview(api.cacheKey(id), next);
    latest.current = { id, review: next };
    setStored(latest.current);
  }, [api, session.id]);
  const viewedCount = files.filter((f) => viewState(review, f) === 'viewed').length;
  const toggleViewed = (f: ChangeFile, viewed: boolean) => updateReview((r) => {
    const marks = { ...r.viewed };
    if (viewed) marks[f.path] = fileDigest(f);
    else delete marks[f.path];
    return { ...r, viewed: marks };
  });
  const editable = !readOnly(session);
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
        setSendNote({ tone: 'error', text: sub.error || 'The thread did not take the comments.' });
        return;
      }
      pendingSend.current = null;
      const sent = new Set(batch.map((c) => c.id));
      updateReview((r) => ({ ...r, comments: r.comments.filter((c) => !sent.has(c.id)) }));
      setSendNote({ tone: 'info', text: sub.status === 'queued' ? 'Comments queued: they go out after this turn.' : 'Comments sent to the thread.' });
    }).catch((e: unknown) => {
      setSendNote({ tone: 'error', text: `Could not send the comments: ${describeError(e)}` });
    }).finally(() => setSending(false));
  };
  const addComment = useCallback((c: Omit<ReviewComment, 'id'>) => {
    setSendNote(null);
    updateReview((r) => ({ ...r, comments: [...r.comments, { ...c, id: newRequestId() }] }));
  }, [updateReview]);
  const removeComment = useCallback((id: string) => updateReview((r) => ({ ...r, comments: r.comments.filter((c) => c.id !== id) })), [updateReview]);

  const stop = useEffectEvent(() => {
    request.current?.abort();
    request.current = null;
  });
  const refresh = useEffectEvent(() => {
    if (!open || !active || document.visibilityState !== 'visible' || request.current || isDefaultPending()) return;
    const controller = new AbortController();
    request.current = controller;
    api.changes(session.id, 'task', controller.signal).then((list) => {
      if (controller.signal.aborted) return;
      setView({ sessionId: session.id, list, error: null });
      onChanges(list);
    }).catch((e: unknown) => {
      if (!controller.signal.aborted) setView({ sessionId: session.id, list: data, error: describeError(e) });
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
    if (document.visibilityState === 'visible') schedule();
    document.addEventListener('visibilitychange', visibility);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', visibility);
      stop();
    };
  }, [session.id, open, active]);
  useEffect(() => { if (tick) { stop(); refresh(); } }, [tick]);
  const reread = useCallback(() => setTick(t => t + 1), []);

  // Folded cards: a viewed file folds unless the reader opened it again; a file it did not decide on stays open.
  const [folds, setFolds] = useState<Record<string, boolean>>({});
  const folded = (f: ChangeFile) => folds[f.path] ?? viewState(review, f) === 'viewed';
  const body = useRef<HTMLDivElement>(null);
  const scrolled = useRef(0);
  const [left] = useState(() => (turn ? null : memory?.current ?? null));
  useEffect(() => () => { if (memory) memory.current = { scrollTop: scrolled.current }; }, [memory]);
  // A turn's ask: its first listed file opens and comes into view, once its card is there.
  const target = turn ? turnFile(files.map(f => f.path), turn.files) : null;
  const [shown, setShown] = useState<{ at: number; path: string } | null>(null);
  if (turn && target && shown?.at !== turn.at) {
    setShown({ at: turn.at, path: target });
    setFolds(f => ({ ...f, [target]: false }));
  }
  useLayoutEffect(() => {
    if (shown) body.current?.querySelector(`[data-change-file="${CSS.escape(shown.path)}"]`)?.scrollIntoView({ block: 'start' });
  }, [shown]);
  useLayoutEffect(() => {
    if (left && body.current && files.length) return restoreScroll(body.current, left.scrollTop);
  }, [left, files.length]);
  const jump = (path: string) => {
    setFolds(f => ({ ...f, [path]: false }));
    requestAnimationFrame(() => body.current?.querySelector(`[data-change-file="${CSS.escape(path)}"]`)?.scrollIntoView({ block: 'start', behavior: 'smooth' }));
  };

  const confirm = useConfirm<ChangeFile>();
  const [reverting, setReverting] = useState(false);
  const [revertError, setRevertError] = useState<string | null>(null);
  const revert = async (path: string, hunk?: string) => {
    setRevertError(null);
    try {
      await api.revertChange(session.id, path, hunk);
      reread();
    } catch (e) {
      setRevertError(`Could not revert ${path}: ${describeError(e)}`);
      throw e;
    }
  };
  const revertFile = () => {
    const f = confirm.target;
    if (!f) return;
    setReverting(true);
    revert(f.path).then(confirm.close, () => confirm.close()).finally(() => setReverting(false));
  };

  return (
    <SidePanel id="changes" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Changes" defaultWidth={560}>
      <PanelHeader>
        <FileDiff aria-hidden="true" className="size-4 text-muted" />
        <span className="text-title text-ink">Changes</span>
        <InstanceName />
        {data?.supported && (
          <span className="line-clamp-2 min-w-0 text-caption leading-tight tabular-nums text-muted">
            <span className="whitespace-nowrap">{files.length} {files.length === 1 ? 'file' : 'files'} by this thread{countsKnown && adds > 0 && <> · <span className="text-success">+{adds}</span></>}{countsKnown && dels > 0 && <> <span className="text-error">−{dels}</span></>}</span>
            {files.length > 0 && <> · <span className="whitespace-nowrap">{viewedCount} of {files.length} viewed</span></>}
          </span>
        )}
        <span className="flex-1" />
        <Tip label="Refresh">
          <Button size="icon-md" aria-label="Refresh" className="text-muted" onClick={reread}>
            <RefreshCw />
          </Button>
        </Tip>
        <Button ref={closeRef} size="icon-md" aria-label="Close changes" className="text-muted" onClick={onClose}>
          <X />
        </Button>
      </PanelHeader>
      {evidence}
      {data?.supported && data.reason && <Note tone="warn" className="shrink-0 px-3 pb-1">Note: {data.reason}.</Note>}
      {countsKnown && adds + dels > LARGE_CHANGE && (
        <Note tone="warn" className="shrink-0 px-3 pb-1">Large change: {adds + dels} lines. Reviews catch the most under about {LARGE_CHANGE} lines, so go file by file and mark each one viewed.</Note>
      )}
      {revertError && <Note tone="error" role="alert" className="shrink-0 px-3 pb-1">{revertError}</Note>}
      {files.length > 1 && (
        <nav aria-label="Changed files" className="flex shrink-0 gap-1.5 overflow-x-auto px-3 pb-2">
          {files.map(f => (
            <button key={f.path} type="button" title={f.path} className="flex shrink-0 items-center gap-1.5 rounded-full bg-tint-well px-2.5 py-0.5 text-caption text-ink hover:bg-tint-hover" onClick={() => jump(f.path)}>
              <FileIcon path={f.path} className="size-3.5" />
              {splitPath(f.path).name}
              {!f.counts_unknown && <span className="tabular-nums"><span className="text-success">+{f.additions}</span> <span className="text-error">−{f.deletions}</span></span>}
            </button>
          ))}
        </nav>
      )}
      <div ref={body} className="@container min-h-40 flex-1 overflow-y-auto overflow-x-hidden px-3 pb-3" aria-busy={!data && !error ? true : undefined} onScroll={(e) => { scrolled.current = e.currentTarget.scrollTop; }}>
        {error && (
          <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 flex-1">Could not load the changes: {error}</span>
            <Button size="sm" variant="secondary" onClick={reread}>Retry</Button>
          </Note>
        )}
        {!data && !error && <Skeleton label="Loading the changes…" rows={3} className="gap-2" rowClassName="h-6 w-full" />}
        {data && !data.supported && <Note>{data.reason || 'Not available for this thread.'}</Note>}
        {data?.supported && files.length === 0 && <Note>No changes from this thread: it has not edited a file that still differs from HEAD.</Note>}
        <div className="flex flex-col gap-3">
          {files.map((f) => (
            <FileCard
              key={f.path}
              sessionId={session.id}
              file={f}
              viewed={viewState(review, f)}
              folded={folded(f)}
              onFold={(fold) => setFolds(m => ({ ...m, [f.path]: fold }))}
              onViewed={(v) => { toggleViewed(f, v); setFolds(m => ({ ...m, [f.path]: v })); }}
              // Files are repository paths; the viewer opens them in the Thread's folder, and nothing outside it.
              onOpenFile={f.path.startsWith(data?.prefix ?? '') ? (opener) => onOpenFile(f.path.slice(data?.prefix?.length ?? 0), opener) : undefined}
              comments={comments}
              onComment={editable ? addComment : undefined}
              onRemoveComment={removeComment}
              onRevert={editable ? (hunk) => revert(f.path, hunk) : undefined}
              onRevertFile={editable ? () => confirm.ask(f) : undefined}
              draftLine={turn?.line !== undefined && shown?.at === turn.at && shown.path === f.path ? turn.line : undefined}
            />
          ))}
        </div>
      </div>
      {editable && (comments.length > 0 || sendNote) && (
        <CommentBatch comments={comments} working={working} sending={sending} note={sendNote} onOpen={jump} onRemove={removeComment} onSend={sendComments} />
      )}
      <AlertDialog
        {...confirm.props}
        className="max-w-88"
        title="Revert this file?"
        description={confirm.target ? `${confirm.target.path} goes back to how it is at HEAD${confirm.target.status === 'untracked' || confirm.target.status === 'added' ? ': the new file is deleted' : ''}. Changes anything else made to it are lost too.` : undefined}
        confirmLabel="Revert file"
        busy={reverting}
        onConfirm={revertFile}
      />
    </SidePanel>
  );
}

/** One changed file: its head (type, path, counts, actions) and, unfolded, its diff with Revert and Comment on each change. */
function FileCard({ sessionId, file: f, viewed, folded, onFold, onViewed, onOpenFile, comments, onComment, onRemoveComment, onRevert, onRevertFile, draftLine }: Readonly<{
  sessionId: string;
  file: ChangeFile;
  viewed: 'viewed' | 'changed' | 'unviewed';
  folded: boolean;
  onFold: (folded: boolean) => void;
  onViewed: (viewed: boolean) => void;
  /** Opens the file in the viewer; unset for a file outside the Thread's folder. */
  onOpenFile?: (opener: HTMLElement) => void;
  comments: ReviewComment[];
  onComment?: (comment: Omit<ReviewComment, 'id'>) => void;
  onRemoveComment: (id: string) => void;
  onRevert?: (hunk: string) => Promise<void>;
  onRevertFile?: () => void;
  draftLine?: number;
}>) {
  const api = useApi();
  const [diff, setDiff] = useState<{ key: string; file: FileDiffData | null; error: string | null } | null>(null);
  const key = `${f.path}\n${fileDigest(f)}`;
  // Read when unfolded, and again only when the file's digest moves; the last diff stays on screen meanwhile.
  useEffect(() => {
    if (folded || diff?.key === key) return;
    const controller = new AbortController();
    api.changeFile(sessionId, 'task', f.path, controller.signal).then(
      (file) => setDiff({ key, file, error: null }),
      (e: unknown) => { if (!controller.signal.aborted) setDiff({ key, file: diff?.file ?? null, error: describeError(e) }); },
    );
    return () => controller.abort();
  }, [api, sessionId, f.path, key, folded, diff?.key, diff?.file]);
  const [, copy] = useCopied();
  const items: ActionItem[] = [
    { key: 'copy', label: 'Copy path', icon: <Copy />, onSelect: () => copy(f.path) },
    ...(onRevertFile ? [{ key: 'revert', label: 'Revert file…', icon: <Undo2 />, onSelect: onRevertFile }] : []),
  ];
  const risk = riskOf(f.path);
  const { dir, name } = splitPath(f.path);
  const fileComments = useMemo(() => comments.filter((c) => c.path === f.path), [comments, f.path]);
  return (
    <section data-change-file={f.path} aria-label={f.path} className="rounded-md bg-raised shadow-raised">
      <div className={cn('sticky top-0 z-10 flex min-h-10 items-center gap-2 rounded-t-md bg-raised py-1 pr-1.5 pl-1', folded && 'rounded-b-md')}>
        <Button size="icon-sm" aria-expanded={!folded} aria-label={`${folded ? 'Show' : 'Hide'} the diff of ${f.path}`} className="text-faint" onClick={() => onFold(!folded)}>
          <ChevronDown className={cn('transition-transform duration-100', folded && '-rotate-90')} />
        </Button>
        <FileIcon path={f.path} />
        <span className="min-w-0 flex-1 truncate text-ui" title={f.path}>
          <span className="text-muted">{dir}</span><span className="font-medium text-ink">{name}</span>
          {f.old_path && <span className="ml-1.5 text-caption text-muted">from {f.old_path}</span>}
        </span>
        {risk && <span className="inline-flex shrink-0 items-center gap-1 text-meta text-error" title={risk.detail}><ShieldAlert aria-hidden="true" className="size-3" />{risk.label}</span>}
        {viewed === 'changed' && <span className="shrink-0 text-meta text-warning">Changed since viewed</span>}
        {f.binary || f.counts_unknown ? <span className="shrink-0 text-caption text-muted">{f.binary ? 'binary' : '—'}</span> : (
          <span className="shrink-0 text-caption tabular-nums"><span className="text-success">+{f.additions}</span> <span className="text-error">−{f.deletions}</span></span>
        )}
        {onOpenFile && (
          <Tip label="Open file">
            <Button size="icon-sm" aria-label={`Open ${f.path}`} className="text-muted" disabled={f.status === 'deleted'} onClick={(e) => onOpenFile(e.currentTarget)}><Eye /></Button>
          </Tip>
        )}
        <Menu.Root modal={false}>
          <Menu.Trigger render={<Button size="icon-sm" aria-label={`Actions for ${f.path}`} className="text-muted" />}><Ellipsis /></Menu.Trigger>
          <Menu.Content align="end"><Menu.Actions items={items} /></Menu.Content>
        </Menu.Root>
        <label className="flex shrink-0 cursor-pointer items-center gap-1.5 rounded-sm px-1.5 py-1 text-caption text-muted hover:bg-tint-hover pointer-coarse:min-h-11">
          <input type="checkbox" className="size-3.5 cursor-pointer accent-success" aria-label={`Viewed ${f.path}`} checked={viewed === 'viewed'} onChange={(e) => onViewed(e.target.checked)} />
          <span aria-hidden="true">Viewed</span>
        </label>
      </div>
      {!folded && (
        <div className="overflow-hidden rounded-b-md">
          <FileView path={f.path} file={diff?.file ?? null} error={diff?.error ?? null} bare comments={fileComments} onComment={onComment} onRemoveComment={onRemoveComment} onRevert={onRevert} draftLine={draftLine} />
        </div>
      )}
    </section>
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
        <ul className="max-h-32 overflow-y-auto overflow-x-hidden px-1 pt-1">
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
          <span className="min-w-0 flex-1 text-caption text-muted">{working ? 'Goes out after this turn, as one message.' : 'Goes to the thread as one message.'}</span>
        )}
        {note && <Note tone={note.tone} role={note.tone === 'error' ? 'alert' : 'status'} className="min-w-0 flex-1">{note.text}</Note>}
        {n > 0 && (
          <Button variant="primary" size="md" loading={sending} onClick={onSend}>
            Send {n} {n === 1 ? 'comment' : 'comments'} to the thread
          </Button>
        )}
      </div>
    </section>
  );
}

/** Where a comment goes: one line on one side of the diff, with its text when the comment was started. */
type Anchor = Pick<ReviewComment, 'line' | 'side' | 'code'>;

/** One file's diff; the file panel's Diff view shows it without review marks or comments. */
export function FileView({ path, file, error, viewed, onViewed, comments = [], onComment, onRemoveComment, onRevert, bare = false, draftLine }: Readonly<{
  path: string;
  file: FileDiffData | null;
  error: string | null;
  viewed?: boolean;
  onViewed?: (viewed: boolean) => void;
  comments?: ReviewComment[];
  /** Absent where comments cannot be sent (a settled or archived Task). */
  onComment?: (comment: Omit<ReviewComment, 'id'>) => void;
  onRemoveComment?: (id: string) => void;
  /** Reverts one hunk (its patch text); absent where the Thread cannot change files. */
  onRevert?: (hunk: string) => Promise<void>;
  /** Without its own head: the file card above says which file it is. */
  bare?: boolean;
  /** A new-side line whose comment box opens once the diff is in. */
  draftLine?: number;
}>) {
  const patch = useMemo<StructuredPatch | null | Error>(() => {
    if (!file || file.binary || file.truncated || file.counts_unknown) return null;
    try {
      if (file.patch) return parsePatch(file.patch)[0] ?? null;
      return structuredPatch(file.path, file.path, file.before ?? '', file.after ?? '');
    } catch (e) {
      return e instanceof Error ? e : new Error(String(e));
    }
  }, [file]);
  const [draft, setDraft] = useState<Anchor | null>(null);
  const [asked, setAsked] = useState<number | undefined>(undefined);
  if (draftLine !== asked && patch && !(patch instanceof Error)) {
    setAsked(draftLine);
    const at = draftLine === undefined ? undefined : patch.hunks.flatMap((h) => numberedLines(h)).find((l) => l.side === 'new' && l.line === draftLine);
    if (at) setDraft({ side: at.side, line: at.line, code: at.code });
  }
  const [reverting, setReverting] = useState<number | null>(null);
  // The comment glyph is drawn once (below) and referenced from every line: thousands of inline icons are what makes a long diff slow to mount.
  const glyph = useId();
  // One object per change, so the mounted slices of a long diff are left alone until a comment moves.
  const lines = useMemo<LineNotes>(() => ({
    glyph,
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
    reverting,
    revert: onRevert ? (hi, text) => {
      setReverting(hi);
      onRevert(text).catch(() => {}).finally(() => setReverting(null));
    } : undefined,
  }), [comments, draft, glyph, onComment, onRemoveComment, path, onRevert, reverting]);

  const warning = error ? <Note tone="error" role="alert" className="p-3">{error}</Note> : null;
  if (!file) return warning ?? <Skeleton label="Loading the diff…" rows={6} className="gap-2 p-3" />;
  if (file.truncated) return <>{warning}<Note className="p-3">The diff exceeded its size limit and is unavailable.</Note></>;
  if (file.binary) return <>{warning}<Note className="p-3">Binary file: no textual diff is available.</Note></>;
  if (file.counts_unknown) return <>{warning}<Note className="p-3">Line counts and a textual diff are unavailable for this change.</Note></>;
  if (patch instanceof Error) return <>{warning}<Note tone="error" className="p-3">Could not parse diff: {patch.message}</Note></>;
  if (!patch || patch.hunks.length === 0) {
    return <>{warning}<Note className="p-3">{file.old_path ? `Renamed from ${file.old_path}: no textual changes.` : `No textual changes in ${path}.`}</Note></>;
  }

  // The gutter is as wide as the longest line number, so its columns can stay put while a long line scrolls (index.css .diff).
  const digits = Math.max(2, ...patch.hunks.map((h) => String(Math.max(h.oldStart + h.oldLines, h.newStart + h.newLines)).length));
  return (
    <>
      {warning}
      <div className="diff animate-fade-in" translate="no" style={{ '--num': `${digits}ch` } as CSSProperties}>
        {!bare && <div className="head">
          <span className="flex items-center gap-2">
            <span className="min-w-0 flex-1 truncate">{file.old_path ? `${file.old_path} → ${file.path}` : file.path}</span>
            {onViewed && (
              <label className="flex shrink-0 cursor-pointer items-center gap-1.5 font-sans text-caption pointer-coarse:min-h-11">
                <input type="checkbox" className="size-3.5 cursor-pointer accent-success" checked={!!viewed} onChange={(e) => onViewed(e.target.checked)} />
                Viewed
              </label>
            )}
          </span>
          {onComment && <span className="block font-sans text-meta">Select a line to comment on it.</span>}
        </div>}
        {onComment && (
          <svg width="0" height="0" aria-hidden="true" className="absolute">
            <symbol id={glyph} viewBox="0 0 24 24"><MessageSquarePlus /></symbol>
          </svg>
        )}
        <DiffRows hunks={patch.hunks} notes={lines} />
      </div>
    </>
  );
}

/** Lines per slice: a table of its own, so the browser lays out and paints only the slices in view. */
const SLICE = 200;
/** A slice's height before it is laid out (its line height), so the scrollbar is right from the start. */
const LINE_PX = 18;

/**
 * The rows of a diff as slices, each a table in a box the browser skips while off screen, mounted one
 * slice per frame: the top shows at once and the rest follows without a long task, and growing the diff
 * never lays out the whole of it again. A slice already on screen is not rebuilt when the next arrives.
 */
function DiffRows({ hunks, notes }: Readonly<{ hunks: StructuredPatch['hunks']; notes: LineNotes }>) {
  const total = hunks.reduce((n, h) => n + h.lines.length, 0);
  const [shown, setShown] = useState(SLICE);
  useEffect(() => {
    if (shown >= total) return;
    const frame = requestAnimationFrame(() => setShown((n) => Math.min(total, n + SLICE)));
    return () => cancelAnimationFrame(frame);
  }, [shown, total]);
  const slices: ReactNode[] = [];
  for (let from = 0; from < Math.min(shown, total); from += SLICE) {
    slices.push(<DiffSlice key={from} hunks={hunks} from={from} to={Math.min(from + SLICE, total)} notes={notes} />);
  }
  return <>{slices}</>;
}

/** The rows of lines [from, to) of the diff, counting across its hunks; a hunk's header comes with its first line. */
const DiffSlice = memo(function DiffSlice({ hunks, from, to, notes }: Readonly<{ hunks: StructuredPatch['hunks']; from: number; to: number; notes: LineNotes }>) {
  const rows: ReactNode[] = [];
  let start = 0;
  for (const [hi, h] of hunks.entries()) {
    const end = start + h.lines.length;
    if (end > from && start < to) rows.push(...renderHunk(h, hi, notes, Math.max(from, start) - start, Math.min(to, end) - start, false, hunks[hi - 1]));
    start = end;
    if (start >= to) break;
  }
  return (
    <div className="slice" style={{ containIntrinsicSize: `auto ${(to - from) * LINE_PX}px` }}>
      <table><tbody>{rows}</tbody></table>
    </div>
  );
});

/** The same diff renderer as Changes, mounted only for an open native edit. */
export function InlinePatch({ path, text }: Readonly<{ path: string; text: string }>) {
  const patch = useMemo(() => {
    try { return parsePatch(text)[0] ?? null; } catch { return null; }
  }, [text]);
  const [all, setAll] = useState(false);
  if (!patch || !patch.hunks.length) return <p className="text-caption text-muted">This recorded diff has no supported textual hunks.</p>;
  const total = patch.hunks.reduce((n, h) => n + h.lines.length + 1, 0);
  let remaining = all ? Infinity : 400;
  const rows: ReactNode[] = [];
  for (const [index, h] of patch.hunks.entries()) {
    if (remaining <= 0) break;
    const shown = Math.max(0, Math.min(h.lines.length, remaining - 1));
    remaining -= shown + 1;
    rows.push(...renderHunk(h, index, undefined, 0, shown, true));
  }
  const digits = Math.max(2, ...patch.hunks.map(h => String(Math.max(h.oldStart + h.oldLines, h.newStart + h.newLines)).length));
  return <div style={{ contentVisibility: 'auto' }}>
    <table className="diff" translate="no" style={{ '--num': `${digits}ch` } as CSSProperties}>
      <caption>{path}</caption><tbody>{rows}</tbody>
    </table>
    {!all && total > 400 && <Button size="sm" variant="subtle" onClick={() => setAll(true)}>Show all {total} lines</Button>}
  </div>;
}

interface LineNotes {
  /** The id of the comment glyph drawn once above the rows. */
  glyph: string;
  comments: ReviewComment[];
  draft: Anchor | null;
  open?: (anchor: Anchor) => void;
  save: (body: string) => void;
  cancel: () => void;
  remove?: (id: string) => void;
  /** Reverts hunk `hi` from its patch text. */
  revert?: (hi: number, text: string) => void;
  /** The hunk whose revert is under way. */
  reverting?: number | null;
}

type DiffHunk = StructuredPatch['hunks'][number];

/** Each line's comment anchor: its new-side number, or its old-side one for a removed line; `changed` for added and removed lines. */
function numberedLines(h: DiffHunk): (Anchor & { changed: boolean })[] {
  let oldNo = h.oldStart, newNo = h.newStart;
  return h.lines.flatMap((line): (Anchor & { changed: boolean })[] => {
    const sign = line[0] ?? ' ';
    if (sign === '\\') return [];
    if (sign === '-') return [{ side: 'old', line: oldNo++, code: line.slice(1), changed: true }];
    if (sign === '+') return [{ side: 'new', line: newNo++, code: line.slice(1), changed: true }];
    oldNo++;
    return [{ side: 'new', line: newNo++, code: line.slice(1), changed: false }];
  });
}

/** Lines longer than this, or runs longer than this many lines, are not compared word by word. */
const WORD_LINE = 400;
const WORD_RUN = 40;

/**
 * The changed words of a hunk: each run of removed lines is paired line by line with the added lines after it,
 * and each pair compared word by word. Index → the line's text in parts, the changed parts marked.
 */
function changedWords(h: DiffHunk): Map<number, { text: string; changed: boolean }[]> {
  const out = new Map<number, { text: string; changed: boolean }[]>();
  for (let i = 0; i < h.lines.length;) {
    if (h.lines[i][0] !== '-') { i++; continue; }
    const dels: number[] = [], adds: number[] = [];
    while (i < h.lines.length && h.lines[i][0] === '-') dels.push(i++);
    while (i < h.lines.length && h.lines[i][0] === '+') adds.push(i++);
    if (dels.length > WORD_RUN || adds.length > WORD_RUN) continue;
    for (let k = 0; k < Math.min(dels.length, adds.length); k++) {
      const before = h.lines[dels[k]].slice(1), after = h.lines[adds[k]].slice(1);
      if (before.length > WORD_LINE || after.length > WORD_LINE) continue;
      const parts = diffWordsWithSpace(before, after);
      out.set(dels[k], parts.filter((p) => !p.added).map((p) => ({ text: p.value, changed: !!p.removed })));
      out.set(adds[k], parts.filter((p) => !p.removed).map((p) => ({ text: p.value, changed: !!p.added })));
    }
  }
  return out;
}

/** The rows of hunk h's lines [from, to), with the hunk's bar when from is 0: the unchanged lines before it, Comment and Revert. */
function renderHunk(h: DiffHunk, hi: number, notes?: LineNotes, from = 0, to = h.lines.length, highlight = false, prev?: DiffHunk) {
  let oldNo = h.oldStart;
  let newNo = h.newStart;
  // The numbers the first shown line has, counted over the lines before it.
  for (const line of h.lines.slice(0, from)) {
    const sign = line[0] ?? ' ';
    if (sign === '+') newNo++;
    else if (sign === '-') oldNo++;
    else if (sign !== '\\') { oldNo++; newNo++; }
  }
  const rows: ReactNode[] = [];
  if (from === 0) {
    const skipped = h.oldStart - (prev ? prev.oldStart + prev.oldLines : 1);
    const first = notes?.open && numberedLines(h).find((l) => l.changed);
    rows.push(
      <tr key={`h${hi}`} className="hunk">
        <td colSpan={3}>
          {notes?.revert || notes?.open ? (
            <span className="sticky left-3 flex items-center gap-2 pr-3 font-sans">
              <span className="min-w-0 flex-1 truncate">{skipped > 0 ? `${skipped} unchanged ${skipped === 1 ? 'line' : 'lines'}` : `Lines ${h.newStart}–${h.newStart + Math.max(0, h.newLines - 1)}`}</span>
              {first && <Button size="sm" variant="subtle" className="h-6 text-caption" onClick={() => notes.open?.({ side: first.side, line: first.line, code: first.code })}><MessageSquarePlus />Comment</Button>}
              {notes.revert && <Button size="sm" variant="subtle" className="h-6 text-caption" loading={notes.reverting === hi} disabled={notes.reverting != null} onClick={() => notes.revert?.(hi, hunkText(h))}><Undo2 />Revert</Button>}
            </span>
          ) : <span className="sticky left-3 inline-block">{`@@ -${h.oldStart},${h.oldLines} +${h.newStart},${h.newLines} @@`}</span>}
        </td>
      </tr>,
    );
  }
  const words = highlight || h.lines.length > 2000 ? null : changedWords(h);
  h.lines.slice(from, to).forEach((line, offset) => {
    const li = from + offset;
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
            <button type="button" aria-label={label ?? undefined} className="group/line inline-flex w-full items-center justify-end gap-1.5 text-inherit focus-visible:-outline-offset-2" onClick={(e) => { e.stopPropagation(); open(anchor); }}>
              <svg aria-hidden="true" viewBox="0 0 24 24" className="size-3 text-accent opacity-0 group-hover/row:opacity-100 group-focus-visible/line:opacity-100"><use href={`#${notes.glyph}`} /></svg>
              {right}
            </button>
          ) : right}
        </td>
        <td className="code-cell">
          <span className="sign">{highlight ? '' : sign}</span>
          {highlight ? <Highlighted language="diff" code={line} /> : words?.get(li)?.map((p, i) => (p.changed ? <mark key={i} className={cn('rounded-xs text-inherit', sign === '+' ? 'bg-success/25' : 'bg-error/25')}>{p.text}</mark> : p.text)) ?? text}
        </td>
      </tr>,
    );
    if (!anchor || !notes) return;
    for (const c of notes.comments) {
      if (c.side !== anchor.side || c.line !== anchor.line) continue;
      rows.push(
        <tr key={`c-${c.id}`} className="note">
          <td colSpan={3} className="px-2 py-1 font-sans whitespace-normal">
            <div className="ml-6 mr-2 rounded-md bg-raised px-3 py-2 text-caption shadow-raised">
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
      className="ml-6 mr-2 flex flex-col gap-1.5 rounded-md bg-raised p-2 shadow-raised"
      onSubmit={(e) => { e.preventDefault(); save(); }}
    >
      <textarea
        ref={ref}
        rows={3}
        aria-label={label}
        placeholder="What should the agent change here?"
        className="w-full resize-y rounded-sm bg-sunken px-2 py-1.5 text-ui text-ink shadow-well placeholder:text-muted focus-visible:outline-none"
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
