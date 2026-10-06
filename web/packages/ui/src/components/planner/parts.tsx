import { BadgeCheck, Bot, Check, Circle, CircleDashed, CircleDot, GitCommitHorizontal, GitMerge, Layers, Link2, ListTree, Lock, Minus, Pause, SquareCheck, TriangleAlert, X } from 'lucide-react';
import { memo, type ReactNode } from 'react';
import { taskName, type Card, type CardKind, type CardStatus } from '../../api';
import { KIND_LABEL, STATUS_LABEL, shownProgress } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Button } from '../ui/button';
import { Chip } from '../ui/chip';
import { usePlanner, usePlannerTasks } from './context';

/**
 * Card state marks (DESIGN.md State marks, extended): colour only where it means something —
 * doing is in motion (`accent`), done is `success`, a pending request is `attention`, staleness
 * `warning`; planned and to do stay quiet. Every mark has a glyph and a word.
 */
export const STATUS_TEXT: Record<CardStatus, string> = { planned: 'text-muted', todo: 'text-body', doing: 'text-accent', done: 'text-success', cancelled: 'text-muted' };

// Memoised, like ProgressText: the Board's column and lane heads draw them again on every frame.
export const StatusGlyph = memo(function StatusGlyph({ status, className }: Readonly<{ status: CardStatus; className?: string }>) {
  const c = cn('size-3.5 shrink-0', className);
  switch (status) {
    case 'planned':
      return <CircleDashed aria-hidden="true" className={cn(c, 'text-faint')} strokeWidth={2} />;
    case 'todo':
      return <Circle aria-hidden="true" className={cn(c, 'text-muted')} strokeWidth={2} />;
    case 'doing':
      return <CircleDot aria-hidden="true" className={cn(c, 'text-accent')} strokeWidth={2} />;
    case 'done':
      return <Check aria-hidden="true" className={cn(c, 'text-success')} strokeWidth={2.5} />;
    case 'cancelled':
      return <Minus aria-hidden="true" className={cn(c, 'text-muted')} strokeWidth={2.5} />;
  }
});

/** The status as a glyph and a word (a chip), or the glyph alone with the word for screen readers. */
export function StatusMark({ status, label = false, className }: Readonly<{ status: CardStatus; label?: boolean; className?: string }>) {
  if (!label) {
    return (
      <span className={cn('inline-flex size-4 shrink-0 items-center justify-center', className)} title={STATUS_LABEL[status]}>
        <StatusGlyph status={status} />
        <span className="sr-only">{STATUS_LABEL[status]}</span>
      </span>
    );
  }
  return (
    <Chip className={cn(STATUS_TEXT[status], className)}>
      <StatusGlyph status={status} />
      {STATUS_LABEL[status]}
    </Chip>
  );
}

export function KindIcon({ kind, className }: Readonly<{ kind: CardKind; className?: string }>) {
  const c = cn('size-3.5 shrink-0 text-muted', className);
  if (kind === 'epic') return <Layers aria-hidden="true" className={c} />;
  if (kind === 'story') return <ListTree aria-hidden="true" className={c} />;
  return <SquareCheck aria-hidden="true" className={c} />;
}

export const kindLabel = (k: CardKind) => KIND_LABEL[k];

/** A container's progress (`shownProgress`): "1/4 · 1 proposed", with "done" when `long`. Memoised on the card, which stays the same object while unchanged. */
export const ProgressText = memo(function ProgressText({ card, long = false, className }: Readonly<{ card: Card; long?: boolean; className?: string }>) {
  if (!card.progress) return null;
  const p = shownProgress(card.progress);
  return (
    <span className={cn('shrink-0 text-caption tabular-nums text-muted', className)} title={`${p.done} of ${p.total} subtasks done${p.proposed ? `, ${p.proposed} of them proposed` : ''}`}>
      {long ? p.text : p.short}
    </span>
  );
});

/** A 16px ring filled by a container's progress (the Map draws its own in SVG). */
export function ProgressRing({ card, className }: Readonly<{ card: Card; className?: string }>) {
  const fraction = card.progress ? shownProgress(card.progress).fraction : 0;
  const r = 6;
  const length = 2 * Math.PI * r;
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16" className={cn('size-4 shrink-0 -rotate-90', className)}>
      <circle cx="8" cy="8" r={r} fill="none" strokeWidth="2.5" className="stroke-hairline-strong" />
      {fraction > 0 && <circle cx="8" cy="8" r={r} fill="none" strokeWidth="2.5" strokeLinecap="round" strokeDasharray={`${length * fraction} ${length}`} className="stroke-success" />}
    </svg>
  );
}

/** The Task holding a subtask; a click opens it. */
export function TaskChip({ taskId, className }: Readonly<{ taskId: string; className?: string }>) {
  const { sessions, openTask } = usePlannerTasks();
  const s = sessions.find((x) => x.id === taskId);
  const name = s ? taskName(s) || 'New task' : 'A task';
  return (
    <button
      type="button"
      className={cn('inline-flex h-5 max-w-44 min-w-0 shrink items-center gap-1 rounded-xs bg-tint-well px-1.5 text-caption text-accent transition-colors hover:bg-tint-hover focus-visible:outline-offset-0 pointer-coarse:h-8', className)}
      title={`Held by ${name}. Open the task.`}
      aria-label={`Open task ${name}`}
      onClick={(e) => {
        e.stopPropagation();
        openTask(taskId);
      }}
    >
      <Bot aria-hidden="true" className="size-3 shrink-0" />
      <span className="truncate">{name}</span>
    </button>
  );
}

/**
 * The quiet markers a card row carries: an approved epic and the proposals waiting for its next
 * approval (`toApprove`), its pause (`pause`, from `pauseLabel`), a lane subtask landing or landed
 * (ADR 0006 §3.2), pending requests, staleness, blocked (`blockers`: its open blockers, from
 * `openBlockerSeqs`).
 */
export function CardMarkers({ card, blockers, pause = '', toApprove = 0, compact = false }: Readonly<{ card: Card; blockers: string; pause?: string; toApprove?: number; compact?: boolean }>) {
  const marks: ReactNode[] = [];
  if (card.run) {
    const r = card.run;
    marks.push(
      <Chip key="approved" title={`Approved to run on ${r.model}, ${r.mode === 'yolo' ? 'Yolo' : 'Safe'}, ${r.parallel} at a time`}>
        <BadgeCheck aria-hidden="true" className="size-3" />
        <span className={cn(compact && 'max-sm:sr-only')}>Approved</span>
      </Chip>,
    );
  }
  if (toApprove > 0) {
    marks.push(
      <Chip key="to-approve" tone="attention" title={`${toApprove} ${toApprove === 1 ? 'proposal waits' : 'proposals wait'} for this epic's next approval`}>
        <span aria-hidden="true" className="size-1.5 rounded-full bg-attention" />
        {toApprove} to approve
      </Chip>,
    );
  }
  if (pause) {
    // A pause on a card above is named on that card; a compact row says so with a glyph. uam's
    // pause (an attempt ended without landing) is a warning; the owner's is their own choice.
    const own = !pause.includes(' via ');
    marks.push(
      <Chip key="paused" tone={pause.startsWith('Paused by uam') ? 'warning' : undefined} title={pause}>
        <Pause aria-hidden="true" className="size-3" />
        {compact && !own ? <span className="sr-only">{pause}</span> : pause}
      </Chip>,
    );
  }
  const lane = card.lane;
  if (lane?.landed_sha && card.held_by) {
    // The landing intent is stored and the branch move is under way: a static glyph, as doing is (one ring per place).
    marks.push(
      <Chip key="landing" tone="accent" title={`Landing as ${lane.landed_sha.slice(0, 7)} on its integration branch`}>
        <GitMerge aria-hidden="true" className="size-3" />
        Landing
      </Chip>,
    );
  } else if (lane?.landed_sha && card.status === 'done' && !lane.reverted_sha) {
    marks.push(
      <Chip key="landed" tone="success" title={`Landed on its integration branch as ${lane.landed_sha}`}>
        <GitCommitHorizontal aria-hidden="true" className="size-3" />
        {'Landed '}
        <span className={cn('font-mono', compact && 'sr-only')}>{lane.landed_sha.slice(0, 7)}</span>
      </Chip>,
    );
  }
  if (card.pending_requests > 0) {
    marks.push(
      <Chip key="pending" tone="attention" title={`${card.pending_requests} pending ${card.pending_requests === 1 ? 'request' : 'requests'}`}>
        <span aria-hidden="true" className="size-1.5 rounded-full bg-attention" />
        {compact ? card.pending_requests : `${card.pending_requests} to decide`}
      </Chip>,
    );
  }
  if (card.stale) {
    const s = card.stale;
    const what = s.diverged ? 'diverged' : `${s.behind} behind`;
    marks.push(
      <Chip key="stale" tone="warning" title={`Stale: ${s.diverged ? 'the pin is no longer an ancestor of HEAD' : `HEAD is ${s.behind} commits past the pin`}${s.files.length ? `; changed: ${s.files.join(', ')}` : ''}`}>
        <TriangleAlert aria-hidden="true" className="size-3" />
        {!compact && what}
        <span className="sr-only">{compact ? `stale, ${what}` : ', stale'}</span>
      </Chip>,
    );
  }
  if (card.blocked || blockers) {
    const title = card.blocked ? 'Marked blocked' : `Waiting on ${blockers}`;
    marks.push(
      <Chip key="blocked" title={title}>
        {card.blocked ? <Lock aria-hidden="true" className="size-3" /> : <Link2 aria-hidden="true" className="size-3" />}
        {compact ? <span className="sr-only">{title}</span> : title}
      </Chip>,
    );
  }
  return <>{marks}</>;
}

/** "in 9 days" / "today" for a suggestion's expiry. */
export function expiresIn(iso: string | undefined, now = Date.now()): string {
  if (!iso) return '';
  const days = Math.ceil((Date.parse(iso) - now) / 86400000);
  if (days <= 0) return 'expires today';
  return `expires in ${days} ${days === 1 ? 'day' : 'days'}`;
}

/** The planner's last notice (a failed action, a launch with its Task), over the view it came from: the Planner's or a Task's Plan panel. */
export function NoticeBar({ className }: Readonly<{ className?: string }>) {
  const { notice, notify, openTask } = usePlanner();
  if (!notice) return null;
  return (
    <p role={notice.tone === 'error' ? 'alert' : 'status'} className={cn('flex items-center gap-2 rounded-sm px-3 py-1.5 text-caption animate-fade-in', notice.tone === 'error' ? 'bg-error-wash text-error' : 'bg-surface text-body', className)}>
      <span className="min-w-0 flex-1">{notice.text}</span>
      {notice.task && (
        <Button size="sm" variant="secondary" onClick={() => openTask(notice.task!)}>
          Open task
        </Button>
      )}
      <Button size="icon-sm" aria-label="Dismiss" className="text-current" onClick={() => notify(null)}>
        <X />
      </Button>
    </p>
  );
}
