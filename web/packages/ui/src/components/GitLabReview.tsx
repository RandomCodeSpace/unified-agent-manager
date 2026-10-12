import { ArrowLeft, Check, FileCode2, MessageSquare, RefreshCw, Search, WandSparkles, Wrench } from 'lucide-react';
import { useCallback, useEffect, useEffectEvent, useRef, useState } from 'react';
import { useApi } from '../ApiContext';
import { ApiError, LIVE, describeError, newRequestId, readOnly, type GitLabDiscussion, type GitLabReview as Review, type GitLabReviewAction, type Item, type SessionDetail, type Submission } from '../api';
import { dateTime, Note, Skeleton } from './common';
import { Button } from './ui/button';
import { Chip } from './ui/chip';

const actionLabel = { investigate: 'Investigation', fix: 'Fix', draft_reply: 'Reply draft' };
const unresolved = (discussion: GitLabDiscussion) => discussion.notes.some((note) => note.resolvable && !note.resolved);
const canResolve = (discussion: GitLabDiscussion) => !discussion.individual_note && discussion.notes.some((note) => note.resolvable);

/** Only this request's completed main-agent turn may supply a reply draft. */
function actionTurn(session: SessionDetail | undefined, requestId: string) {
  if (!session) return;
  const items = [...new Map([...(session.items ?? []), ...(session.recent_items ?? [])].map((item) => [item.id, item])).values()].sort((a, b) => Date.parse(a.time) - Date.parse(b.time));
  const marker = `UAM GitLab review request: ${requestId}`;
  const user = [...items, ...(session.outline ?? [])].find((item) => item.kind === 'user' && item.text?.split('\n')[0] === marker);
  if (!user) return;
  const timing = session.turn_timings?.find((turn) => turn.user_item_id === user.id);
  if (!timing || timing.state === 'working' || !timing.ended_at) return;
  const ownerIndex = items.findIndex((item) => item.id === user.id);
  const nextIndex = items.findIndex((item, index) => item.kind === 'user' && !item.agent_id && !item.delivery && (ownerIndex >= 0 ? index > ownerIndex : Date.parse(item.time) > Date.parse(user.time)));
  const reply = items.slice(ownerIndex + 1, nextIndex < 0 ? undefined : nextIndex).filter((item) => item.kind === 'assistant' && !item.agent_id && Date.parse(item.time) >= Date.parse(user.time) && Date.parse(item.time) <= Date.parse(timing.ended_at!)).at(-1);
  return { timing, reply };
}

type PendingAction = { key: string; action: GitLabReviewAction; receipt: Submission; revision: number };
type Notice = { key: string; text: string; error?: boolean };
type SavedReview = { replies: Record<string, string>; pending?: PendingAction; candidate?: { key: string; text: string }; uncertain: boolean };

function savedReview(storageKey: string): SavedReview {
  try {
    const value = JSON.parse(sessionStorage.getItem(storageKey) ?? '{}') as Partial<SavedReview> | null;
    const pending = value?.pending;
    const candidate = value?.candidate;
    return {
      replies: Object.fromEntries(Object.entries(value?.replies ?? {}).filter(([, text]) => typeof text === 'string')),
      pending: pending && typeof pending.key === 'string' && pending.action in actionLabel && typeof pending.receipt?.request_id === 'string' ? pending : undefined,
      candidate: candidate && typeof candidate.key === 'string' && typeof candidate.text === 'string' ? candidate : undefined,
      uncertain: value?.uncertain === true,
    };
  } catch { return { replies: {}, uncertain: false }; }
}

/**
 * A merge request's discussions. With `sha` (the Thread's own commit) they can be answered,
 * resolved and handed to the agent; without it they are read-only.
 */
export function GitLabReview({ sessionId, session, iid, sha, onLoad }: Readonly<{ sessionId: string; session?: SessionDetail; iid: number; sha?: string; onLoad?: (review: Review) => void }>) {
  const api = useApi();
  const writable = !!sha;
  const storageKey = api.storageKey(`uam:gitlab-review:${sessionId}:${iid}:${sha ?? 'read'}`);
  const [saved] = useState(() => savedReview(storageKey));
  const [review, setReview] = useState<Review>();
  const [readError, setReadError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<string | null>();
  const [filter, setFilter] = useState<'all' | 'unresolved' | 'resolved'>('all');
  const [replies, setReplies] = useState<Record<string, string>>(saved.replies);
  const [notice, setNotice] = useState<Notice>();
  const [busy, setBusy] = useState<string>();
  const [uncertain, setUncertain] = useState(saved.uncertain);
  const [pending, setPending] = useState<PendingAction | undefined>(saved.pending);
  const [candidate, setCandidate] = useState<{ key: string; text: string } | undefined>(saved.candidate);
  const request = useRef<AbortController | null>(null);
  const live = useRef(true);
  const writing = useRef(false);
  const revision = useRef<Record<string, number>>({});

  useEffect(() => {
    try { sessionStorage.setItem(storageKey, JSON.stringify({ replies, pending, candidate, uncertain })); }
    catch { /* The open panel still works when browser storage is unavailable. */ }
  }, [storageKey, replies, pending, candidate, uncertain]);

  const onLoaded = useEffectEvent((next: Review) => onLoad?.(next));
  useEffect(() => { if (review) onLoaded(review); }, [review]);
  const load = useCallback((reconcile = false) => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    return api.gitlabReview(sessionId, iid, sha, controller.signal).then((next) => {
      if (controller.signal.aborted) return;
      setReview(next);
      setReadError(undefined);
      if (reconcile) setUncertain(false);
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setReadError(describeError(error));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
  }, [api, sessionId, iid, sha]);
  useEffect(() => {
    live.current = true;
    void load();
    return () => { live.current = false; request.current?.abort(); };
  }, [load]);

  const finishDraft = useEffectEvent((action: PendingAction, text: string) => {
    if (!live.current) return;
    if ((revision.current[action.key] ?? 0) === action.revision && !replies[action.key]?.trim()) {
      setReplies((before) => ({ ...before, [action.key]: text }));
      setNotice({ key: action.key, text: 'Draft ready. Review it before posting.' });
    } else {
      setCandidate({ key: action.key, text });
      setNotice({ key: action.key, text: 'Draft ready. Your edits were kept.' });
    }
    setPending(undefined);
  });
  useEffect(() => {
    if (!pending) return;
    const turn = actionTurn(session, pending.receipt.request_id);
    const receipt = session?.last_submission?.request_id === pending.receipt.request_id ? session.last_submission : undefined;
    if (!turn && receipt?.status !== 'cancelled' && receipt?.status !== 'rejected') return;
    const controller = new AbortController();
    // The promise also avoids updating render state synchronously from an effect.
    void (async () => {
      await Promise.resolve();
      if (controller.signal.aborted) return;
      if (!turn) {
        setNotice({ key: pending.key, text: receipt?.error || `${actionLabel[pending.action]} ${receipt?.status}.`, error: true });
        setPending(undefined);
        return;
      }
      if (turn.timing.state !== 'completed') {
        setNotice({ key: pending.key, text: `${actionLabel[pending.action]} ${turn.timing.state}. See the task for details.`, error: true });
        setPending(undefined);
        return;
      }
      if (pending.action !== 'draft_reply') {
        setNotice({ key: pending.key, text: `${actionLabel[pending.action]} complete. Results are in the task.` });
        setPending(undefined);
        return;
      }
      if (!turn.reply) return; // The detail stream may arrive after turn_timing.
      try {
        const item: Item = turn.reply.compact || turn.reply.clipped ? (await api.itemBody(sessionId, turn.reply.id, '', controller.signal)).item : turn.reply;
        if (controller.signal.aborted) return;
        if (item.text?.trim()) finishDraft(pending, item.text.trim());
        else {
          setNotice({ key: pending.key, text: 'The agent returned no reply draft. See the task for details.', error: true });
          setPending(undefined);
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setNotice({ key: pending.key, text: `Could not load the draft: ${describeError(error)}. Copy it from the task.`, error: true });
          setPending(undefined);
        }
      }
    })();
    return () => controller.abort();
  }, [api, sessionId, session, pending]);

  const discussions = review?.discussions.filter((discussion) => discussion.notes.some((note) => !note.system)) ?? [];
  const currentId = selected === undefined ? discussions[0]?.id : selected;
  const discussion = discussions.find((entry) => entry.id === currentId);
  const key = discussion?.id ?? 'comment';
  const reply = replies[key] ?? '';
  const stale = !!readError || loading;
  const locked = !!session && readOnly(session);
  const actionBlocked = !session || locked || !!session.rewind || session.state === 'starting';
  const updateReply = (text: string) => {
    revision.current[key] = (revision.current[key] ?? 0) + 1;
    setReplies((before) => ({ ...before, [key]: text }));
  };
  const startAction = async (action: GitLabReviewAction) => {
    if (!sha || !discussion || writing.current || pending || actionBlocked || stale) return;
    writing.current = true;
    setBusy(action);
    setNotice(undefined);
    const requestId = newRequestId();
    const capturedRevision = revision.current[key] ?? 0;
    setPending({ key, action, receipt: { request_id: requestId, status: 'uncertain', time: new Date().toISOString() }, revision: capturedRevision });
    try {
      const receipt = await api.gitlabReviewAction(sessionId, iid, { commit_sha: sha, discussion_id: discussion.id, action, request_id: requestId, mode: LIVE.includes(session!.state) || !!session!.queued ? 'queue' : 'send' });
      if (!live.current) return;
      if (receipt.status === 'rejected' || receipt.status === 'cancelled') {
        setPending(undefined);
        setNotice({ key, text: receipt.error || `${actionLabel[action]} ${receipt.status}.`, error: true });
      } else {
        setPending({ key, action, receipt, revision: capturedRevision });
        setNotice({ key, text: receipt.status === 'uncertain' ? 'The request may have started. Check the task before trying again.' : `${actionLabel[action]} ${receipt.status === 'queued' ? 'queued' : 'started'}. Follow progress in the task.`, error: receipt.status === 'uncertain' });
      }
    } catch (error) {
      if (!live.current) return;
      const unknown = !(error instanceof ApiError) || error.status === 0 || error.status >= 500;
      if (unknown) setPending({ key, action, receipt: { request_id: requestId, status: 'uncertain', time: new Date().toISOString() }, revision: capturedRevision });
      else setPending(undefined);
      setNotice({ key, text: unknown ? 'The request may have started. Check the task before trying again.' : describeError(error), error: true });
    } finally {
      writing.current = false;
      if (live.current) setBusy(undefined);
    }
  };
  const write = async (kind: 'post' | 'resolve') => {
    if (!sha || writing.current || stale || uncertain || locked || (kind === 'post' && !reply.trim())) return;
    writing.current = true;
    setBusy(kind);
    setUncertain(true);
    setNotice(undefined);
    const capturedRevision = revision.current[key] ?? 0;
    try {
      if (kind === 'post') await api.postGitlabComment(sessionId, iid, { commit_sha: sha, ...(discussion && !discussion.individual_note ? { discussion_id: discussion.id } : {}), body: reply.trim() });
      else if (discussion) await api.resolveGitlabThread(sessionId, iid, { commit_sha: sha, discussion_id: discussion.id, resolved: unresolved(discussion) });
      if (!live.current) return;
      setUncertain(false);
      if (kind === 'post' && (revision.current[key] ?? 0) === capturedRevision) setReplies((before) => ({ ...before, [key]: '' }));
      setNotice({ key, text: kind === 'post' ? 'Posted to GitLab.' : 'Discussion updated in GitLab.' });
      setLoading(true);
      await load();
    } catch (error) {
      if (!live.current) return;
      const unknown = !(error instanceof ApiError) || error.status === 0 || error.status >= 500;
      setUncertain(unknown);
      setNotice({ key, text: unknown ? 'GitLab may have saved this change. Refresh and check the discussion before trying again.' : describeError(error), error: true });
    } finally {
      writing.current = false;
      if (live.current) setBusy(undefined);
    }
  };

  return <div className="flex min-w-0 flex-col gap-3">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <span className="text-caption text-muted">{discussions.filter(unresolved).length} unresolved {review && `· ${review.merge_request.source_branch} → ${review.merge_request.target_branch}`}</span>
      <Button size="icon-sm" aria-label="Refresh discussions" loading={loading} disabled={!!busy} onClick={() => { setLoading(true); void load(true); }}><RefreshCw /></Button>
    </div>
    {readError && <Note tone="error" role="alert">{readError} Refresh discussions to check again.</Note>}
    {!review && loading && <Skeleton label="Loading discussions…" rows={3} />}
    {review && <>
      {review.truncated && <Note>Showing part of the discussion history. Open the merge request in GitLab for all comments.</Note>}
      <div className="flex flex-wrap gap-1" role="group" aria-label="Filter discussions">
        {(['all', 'unresolved', 'resolved'] as const).map((value) => <Button key={value} size="sm" aria-pressed={filter === value} onClick={() => setFilter(value)}>{value === 'all' ? 'All' : value === 'unresolved' ? 'Unresolved' : 'Resolved'}</Button>)}
      </div>
      <div className="flex max-h-48 flex-col gap-1 overflow-y-auto" aria-label="Discussions">
        {discussions.filter((entry) => filter === 'all' || (filter === 'unresolved' ? unresolved(entry) : canResolve(entry) && !unresolved(entry))).map((entry) => {
          const first = entry.notes.find((note) => !note.system)!;
          const replies = entry.notes.filter((note) => !note.system).length - 1;
          return <button key={entry.id} type="button" aria-pressed={entry.id === currentId} className="flex min-w-0 flex-col gap-1 rounded-sm px-2 py-2 text-left text-ui hover:bg-tint-hover aria-pressed:bg-sunken" onClick={() => setSelected(entry.id)}>
            <span className="flex flex-wrap items-center gap-2"><span className="font-medium text-ink">{first.author.name || first.author.username}</span><span className="text-caption text-muted">{entry.individual_note ? 'Comment' : `${replies} ${replies === 1 ? 'reply' : 'replies'}`}</span>{canResolve(entry) && <Chip tone={unresolved(entry) ? 'warning' : 'success'}>{unresolved(entry) ? 'Unresolved' : 'Resolved'}</Chip>}</span>
            <span className="line-clamp-2 break-words text-body">{first.body}</span>
          </button>;
        })}
        {!discussions.length && <Note>No comments yet.</Note>}
        {!!discussions.length && !discussions.some((entry) => filter === 'all' || (filter === 'unresolved' ? unresolved(entry) : canResolve(entry) && !unresolved(entry))) && <Note>No {filter} discussions.</Note>}
      </div>
      {writable && <Button size="sm" variant="subtle" className="self-start" onClick={() => setSelected(null)}><MessageSquare />Add comment</Button>}
      {discussion ? <section aria-label="Selected discussion" className="flex min-w-0 flex-col gap-3 border-t border-hairline pt-3">
        {discussion.notes.filter((note) => !note.system).map((note) => <article key={note.id} className="flex min-w-0 flex-col gap-2">
          <div className="flex flex-wrap items-baseline gap-x-2"><span className="text-ui font-medium text-ink">{note.author.name || note.author.username}</span><time dateTime={note.created_at} className="text-meta text-muted">{dateTime(note.created_at)}</time></div>
          {note.position && <div className="flex items-start gap-1.5 text-caption text-muted"><FileCode2 aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" /><code className="min-w-0 break-all font-mono">{note.position.new_path || note.position.old_path}:{note.position.new_line ?? note.position.old_line}</code>{note.position.head_sha !== (review.merge_request.head_sha || sha) && <Chip fill="outline">Earlier commit</Chip>}</div>}
          <p className="whitespace-pre-wrap break-words text-ui text-body">{note.body}</p>
        </article>)}
        {writable && <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="primary" loading={busy === 'investigate'} disabled={actionBlocked || stale || !!busy || !!pending} onClick={() => void startAction('investigate')}><Search />Investigate</Button>
          <Button size="sm" variant="secondary" loading={busy === 'fix'} disabled={actionBlocked || stale || !!busy || !!pending} onClick={() => void startAction('fix')}><Wrench />Fix</Button>
        </div>}
        {writable && session && <p className="text-caption text-muted">{locked ? 'Reopen this task to work on the review.' : `Uses ${session.model || 'the task’s selected model'}${LIVE.includes(session.state) || session.queued ? ' · Queues after the current work' : ''}`}</p>}
      </section> : writable && <h4 className="text-ui font-medium text-ink">New comment on MR !{iid}</h4>}
      {notice && notice.key === key && <Note role={notice.error ? 'alert' : 'status'} tone={notice.error ? 'error' : undefined}>{notice.text}</Note>}
      {uncertain && !busy && (!notice || notice.key !== key) && <Note role="alert" tone="error">GitLab may have saved the last change. Refresh and check the discussion before trying again.</Note>}
      {pending && !busy && (!notice || notice.key !== key) && <Note>{actionLabel[pending.action]} {pending.receipt.status === 'uncertain' ? 'may have started. Check the task before trying again.' : 'is pending. Follow progress in the task.'}</Note>}
      {pending?.receipt.status === 'uncertain' && !busy && <Button size="sm" className="self-start" onClick={() => { setPending(undefined); setNotice(undefined); }}>I checked the task</Button>}
      {writable && <>
      <div className="flex items-center justify-between gap-2">
        <label htmlFor={`gitlab-reply-${iid}`} className="text-caption text-muted">{discussion && !discussion.individual_note ? `Reply on MR !${iid}` : `Comment on MR !${iid}`}</label>
        {discussion && <Button size="sm" variant="subtle" loading={busy === 'draft_reply'} disabled={actionBlocked || stale || !!busy || !!pending} onClick={() => void startAction('draft_reply')}><WandSparkles />Draft reply</Button>}
      </div>
      <textarea id={`gitlab-reply-${iid}`} rows={4} value={reply} maxLength={16000} onChange={(event) => updateReply(event.target.value)} placeholder={discussion && !discussion.individual_note ? 'Write a reply…' : 'Write a comment…'} className="w-full min-w-0 resize-y rounded-sm bg-sunken px-2.5 py-2 text-ui text-ink shadow-well placeholder:text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" />
      {candidate?.key === key && <details className="text-ui text-body"><summary className="cursor-pointer">Agent draft</summary><p className="my-2 whitespace-pre-wrap break-words">{candidate.text}</p><Button size="sm" onClick={() => { updateReply(candidate.text); setCandidate(undefined); }}>Use draft</Button></details>}
      <div className="flex flex-wrap items-center justify-between gap-2">
        {discussion && canResolve(discussion) ? <Button size="sm" variant="subtle" loading={busy === 'resolve'} disabled={stale || !!busy || uncertain || locked} onClick={() => void write('resolve')}><Check />{unresolved(discussion) ? 'Resolve discussion' : 'Reopen discussion'}</Button> : <span />}
        <Button size="sm" variant="secondary" loading={busy === 'post'} disabled={!reply.trim() || stale || !!busy || uncertain || locked} onClick={() => void write('post')}>{discussion && !discussion.individual_note ? 'Post reply' : 'Post comment'}</Button>
      </div>
      {selected === null && discussions.length > 0 && <Button size="sm" variant="subtle" className="self-start" onClick={() => setSelected(undefined)}><ArrowLeft />Back to discussion</Button>}
      </>}
    </>}
  </div>;
}
