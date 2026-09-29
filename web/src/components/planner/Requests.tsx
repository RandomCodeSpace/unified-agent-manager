import { Check, ChevronRight, FileDiff, GitCommitHorizontal, MessageSquareText, SquareCheck, SquareTerminal, X } from 'lucide-react';
import { useMemo, useState, type ReactNode, type SubmitEvent } from 'react';
import { api, describeError, type BoardRequest, type Card, type Evidence, type RequestFlag, type RequestKind, type SessionSummary } from '../../api';
import { cn } from '../../lib/cn';
import { relTime } from '../common';
import { Button } from '../ui/button';
import { Chip } from '../ui/chip';
import { Collapse } from '../ui/collapse';
import { Input } from '../ui/input';
import { useShownBoard } from './context';
import { TaskChip } from './parts';

export const REQUEST_LABEL: Record<RequestKind, string> = { done: 'Done', cancel: 'Cancel', blocked: 'Blocked', split: 'Split', change: 'Change' };

const FLAG_TEXT: Record<RequestFlag, string> = {
  acceptance_could_not_run: 'Acceptance could not run',
  no_change_in_tree: 'No change in tree',
  tests_or_build_changed: 'Tests or build files changed',
  overlap: 'Overlaps another hold',
};

/** One disclosure row of the evidence: a 24px caption line that opens its body. */
function Row({ icon, label, children }: Readonly<{ icon: ReactNode; label: ReactNode; children?: ReactNode }>) {
  const [open, setOpen] = useState(false);
  if (!children) {
    return (
      <div className="flex min-h-6 items-center gap-1.5 px-1 text-caption text-muted [&_svg]:size-3.5 [&_svg]:shrink-0">
        <span aria-hidden="true" className="size-3.5 shrink-0" />
        {icon}
        {label}
      </div>
    );
  }
  return (
    <div>
      <button type="button" aria-expanded={open} className="flex min-h-6 w-full items-center gap-1.5 rounded-xs px-1 text-left text-caption text-muted transition-colors hover:bg-tint-hover hover:text-body focus-visible:-outline-offset-2 pointer-coarse:min-h-11 [&_svg]:size-3.5 [&_svg]:shrink-0" onClick={() => setOpen((o) => !o)}>
        <ChevronRight aria-hidden="true" className={cn('text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
        {icon}
        {label}
      </button>
      <Collapse open={open}>
        <div className="pt-1 pb-2 pl-6">{children}</div>
      </Collapse>
    </div>
  );
}

/** A done request's evidence rows (§6): diff, commits, acceptance, transcript and checklist, each one line until opened. */
export function EvidenceRows({ evidence: ev, sessions, onOpenTask }: Readonly<{ evidence: Evidence; sessions: SessionSummary[]; onOpenTask: (id: string) => void }>) {
  const rows: ReactNode[] = [];
  if (ev.diff) {
    const files = ev.diff.files;
    rows.push(
      <Row key="diff" icon={<FileDiff />} label={<span className="tabular-nums"><span className="text-success">+{ev.diff.added}</span> <span className="text-error">−{ev.diff.deleted}</span> in {files.length} {files.length === 1 ? 'file' : 'files'}</span>}>
        {files.length > 0 && (
          <ul className="flex flex-col gap-0.5">
            {files.map((f) => (
              <li key={f.path} className="flex min-w-0 items-center gap-2 text-caption">
                <span className="min-w-0 truncate text-ink" title={f.path}>{f.path}</span>
                <span className="shrink-0 tabular-nums"><span className="text-success">+{f.added}</span> <span className="text-error">−{f.deleted}</span></span>
                {!f.by_task && <span className="shrink-0 text-muted">not by this task</span>}
                {f.overlap && <Chip tone="warning">overlaps with #{f.overlap.card}{taskLabel(sessions, f.overlap.task_id)}</Chip>}
              </li>
            ))}
          </ul>
        )}
      </Row>,
    );
  }
  if (ev.commits) {
    rows.push(
      <Row key="commits" icon={<GitCommitHorizontal />} label={`${ev.commits.length} ${ev.commits.length === 1 ? 'commit' : 'commits'}`}>
        {ev.commits.length > 0 && (
          <ul className="flex flex-col gap-0.5">
            {ev.commits.map((c) => (
              <li key={c.sha} className="flex min-w-0 gap-2 text-caption">
                <span className="shrink-0 tabular-nums text-muted">{c.sha.slice(0, 7)}</span>
                <span className="min-w-0 text-ink">{c.subject}</span>
              </li>
            ))}
          </ul>
        )}
      </Row>,
    );
  }
  if (ev.accept) {
    const a = ev.accept;
    rows.push(
      <Row key="accept" icon={<SquareTerminal />} label={<span>Acceptance <span className="font-mono text-code-sm">{a.cmd}</span> · <span className={a.exit === 0 ? 'text-success' : 'text-error'}>exit {a.exit}</span>{a.stale && <span className="text-warning"> · command changed since</span>}</span>}>
        <p className="mb-1 text-caption text-muted">
          At {a.head}{a.dirty ? ', with uncommitted changes' : ', clean'}, {relTime(a.ran_at)} ago · {a.cmd_hash}
        </p>
        {a.tail && <pre className="max-h-48 overflow-auto rounded-sm bg-code-bg px-2 py-1.5 font-mono text-code-sm text-ink shadow-well">{a.tail}</pre>}
      </Row>,
    );
  }
  if (ev.transcript) {
    const t = ev.transcript;
    rows.push(
      <div key="transcript" className="flex min-h-6 items-center gap-1.5 px-1 text-caption text-muted">
        <span aria-hidden="true" className="size-3.5 shrink-0" />
        <MessageSquareText aria-hidden="true" className="size-3.5 shrink-0" />
        Transcript
        <TaskChip taskId={t.task_id} sessions={sessions} onOpen={onOpenTask} />
      </div>,
    );
  }
  if (ev.checklist) rows.push(<Row key="checklist" icon={<SquareCheck />} label={<span className="tabular-nums">Checklist {ev.checklist.done}/{ev.checklist.total}</span>} />);
  if (!rows.length) return null;
  return <div className="flex flex-col">{rows}</div>;
}

function taskLabel(sessions: SessionSummary[], id: string): string {
  const s = sessions.find((x) => x.id === id);
  const name = s?.name || s?.title;
  return name ? ` (${name})` : '';
}

/** What a split or change request proposes. */
function Proposal({ request: r, card, byId }: Readonly<{ request: BoardRequest; card: Card | undefined; byId: ReadonlyMap<string, Card> }>) {
  if (r.kind === 'split') {
    const children = Array.isArray(r.payload.children) ? (r.payload.children as { title: string; win_condition?: string; done?: boolean }[]) : [];
    return (
      <div className="flex flex-col gap-0.5">
        <p className="text-caption text-muted">Turns #{card?.seq} into a story with {children.length} subtasks:</p>
        <ul className="flex flex-col gap-0.5 pl-1">
          {children.map((c, i) => (
            <li key={i} className="flex min-w-0 items-start gap-1.5 text-caption">
              {c.done ? <Check aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-success" /> : <span aria-hidden="true" className="mt-1 size-2.5 shrink-0 rounded-full border-[1.5px] border-faint" />}
              <span className="min-w-0">
                <span className="text-ink">{c.title}</span>
                {c.done && <span className="text-muted"> (ticked: accepted with the split)</span>}
                {c.win_condition && <span className="block text-muted">{c.win_condition}</span>}
              </span>
            </li>
          ))}
        </ul>
      </div>
    );
  }
  if (r.kind === 'change') {
    const fields = Object.entries(r.payload);
    return (
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-caption">
        {fields.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted">{k.replace('_', ' ')}</dt>
            <dd className="min-w-0">
              {card && k in card && <span className="text-muted line-through">{String((card as unknown as Record<string, unknown>)[k] ?? '')}</span>}
              <span className="block text-ink">{String(v)}</span>
            </dd>
          </div>
        ))}
      </dl>
    );
  }
  if (r.kind === 'blocked' && typeof r.payload.blocker === 'string') {
    const b = byId.get(r.payload.blocker);
    return <p className="text-caption text-muted">Blocked by {b ? <span className="text-ink">#{b.seq} {b.title}</span> : 'another card'}</p>;
  }
  return null;
}

/**
 * One request: its kind, card, Task and comment, its flags and evidence, and (while pending)
 * Accept, and Reject with a required reason, both inline so they work in a popped-out window.
 */
export function RequestItem({ request: r, byId, showCard = true }: Readonly<{ request: BoardRequest; byId: ReadonlyMap<string, Card>; showCard?: boolean }>) {
  const { sessions, openCard, openTask, notify } = useShownBoard();
  const card = byId.get(r.card_id);
  const [rejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const pending = r.status === 'pending';

  async function decide(op: () => Promise<unknown>, verb: string) {
    setBusy(true);
    notify(null);
    try {
      await op();
      setRejecting(false);
    } catch (e) {
      notify({ tone: 'error', text: `Could not ${verb}: ${describeError(e)}` });
    } finally {
      setBusy(false);
    }
  }
  const reject = (e: SubmitEvent) => {
    e.preventDefault();
    if (reason.trim()) void decide(() => api.planner.reject(r.id, reason.trim()), 'reject the request');
  };
  const overlap = r.evidence.diff?.files.find((f) => f.overlap)?.overlap;

  return (
    <article aria-label={`${REQUEST_LABEL[r.kind]} request on #${card?.seq ?? '?'}`} className="flex flex-col gap-1.5 rounded-md bg-raised px-3 py-2.5 shadow-raised">
      <header className="flex min-w-0 flex-wrap items-center gap-1.5">
        <Chip tone={pending ? 'attention' : 'muted'}>
          {pending && <span aria-hidden="true" className="size-1.5 rounded-full bg-attention" />}
          {REQUEST_LABEL[r.kind]}
        </Chip>
        {showCard && card && (
          <button type="button" className="min-w-0 truncate text-left text-ui font-medium text-ink hover:underline focus-visible:outline-offset-0" onClick={() => openCard(card.id)}>
            #{card.seq} {card.title}
          </button>
        )}
        {!pending && <span className="text-caption text-muted capitalize">{r.status}</span>}
        <span className="flex-1" />
        <TaskChip taskId={r.task_id} sessions={sessions} onOpen={openTask} />
        <time className="shrink-0 text-caption tabular-nums text-muted" dateTime={r.created_at} title={new Date(r.created_at).toLocaleString()}>{relTime(r.created_at)}</time>
      </header>
      {r.comment && <p className="text-ui text-body [overflow-wrap:anywhere]">{r.comment}</p>}
      {r.flags.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {r.flags.map((f) => (
            <Chip key={f} tone="warning" fill="well">
              {f === 'overlap' && overlap ? `Overlaps with #${overlap.card}${taskLabel(sessions, overlap.task_id)}` : FLAG_TEXT[f]}
            </Chip>
          ))}
        </div>
      )}
      <Proposal request={r} card={card} byId={byId} />
      <EvidenceRows evidence={r.evidence} sessions={sessions} onOpenTask={openTask} />
      {r.decision_comment && <p className="text-caption text-muted">Decision: {r.decision_comment}</p>}
      {pending && !rejecting && (
        <div className="flex flex-wrap justify-end gap-2 pt-1">
          <Button size="sm" variant="danger" disabled={busy} onClick={() => setRejecting(true)}>
            <X />
            Reject
          </Button>
          <Button size="sm" variant="primary" loading={busy} onClick={() => void decide(() => api.planner.accept(r.id, ''), 'accept the request')}>
            <Check />
            Accept
          </Button>
        </div>
      )}
      {pending && rejecting && (
        <form aria-label="Reject the request" className="flex flex-col gap-1.5 pt-1" onSubmit={reject}>
          {/* eslint-disable-next-line jsx-a11y/no-autofocus -- Reject opens this field to type the reason into. */}
          <Input size="md" autoFocus aria-label="Reason" required placeholder="Why, for the task (required)" value={reason} onChange={(e) => setReason(e.target.value)} onKeyDown={(e) => e.key === 'Escape' && setRejecting(false)} />
          <div className="flex flex-wrap justify-end gap-2">
            <Button size="sm" onClick={() => setRejecting(false)}>
              Keep
            </Button>
            <Button type="submit" size="sm" variant="danger" className="bg-sunken" loading={busy} disabled={!reason.trim()}>
              Reject request
            </Button>
          </div>
        </form>
      )}
    </article>
  );
}

/** The Inbox (§10): the shown Board's pending requests, oldest first. */
export function InboxList() {
  const { board, cards } = useShownBoard();
  const byId = useMemo(() => new Map(cards.map((c) => [c.id, c])), [cards]);
  const requests = useMemo(() => [...(board?.data?.requests ?? [])].sort((a, b) => a.created_at.localeCompare(b.created_at)), [board?.data?.requests]);
  if (!requests.length) return <p className="px-1 py-4 text-ui text-muted">Nothing to decide.</p>;
  return (
    <ul className="flex flex-col gap-2" aria-label="Pending requests">
      {requests.map((r) => (
        <li key={r.id}>
          <RequestItem request={r} byId={byId} />
        </li>
      ))}
    </ul>
  );
}
