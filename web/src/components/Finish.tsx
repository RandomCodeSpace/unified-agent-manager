import { CheckCircle2, CircleDashed, CircleX, FileDiff, History, X } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { api, describeError, type Changes, type CheckKind, type EvidenceCheck, type Item, type SessionDetail, type TurnEvidence } from '../api';
import { cn } from '../lib/cn';
import { formatMs } from '../lib/transcript';
import type { CommandOutput } from './CommandOutput';
import { useBodyRead } from './Details';
import { Button } from './ui/button';
import { Chip } from './ui/chip';

/**
 * "Since you left": one line under the Task header when the Task changed while the owner was
 * away (the service's turn evidence), with a way to the first thing they have not seen.
 */
export function SinceYouLeft({ away, text, onJump, onDismiss }: Readonly<{ away: string; text: string; onJump: () => void; onDismiss: () => void }>) {
  return (
    <div role="status" className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-0.5 rounded-md bg-info-wash px-3 py-1.5 text-ui text-ink">
      <History aria-hidden="true" className="size-4 shrink-0 text-info" />
      <p className="min-w-60 flex-1">
        <span className="font-medium">Since you left</span> ({away}): {text}.
      </p>
      <span className="ml-auto flex shrink-0 items-center gap-1">
        <Button size="sm" variant="subtle" className="text-info pointer-coarse:min-h-11" onClick={onJump}>Jump to where you stopped</Button>
        <Button size="icon" variant="subtle" aria-label="Dismiss" className="text-muted" onClick={onDismiss}><X /></Button>
      </span>
    </div>
  );
}

/** Plain words for a check row: what the agent ran. */
const CHECK_WORDS: Record<CheckKind, string> = { test: 'the tests', build: 'the build', lint: 'the linter', vet: 'go vet', typecheck: 'the type check' };

/** "Ran the tests", "Ran the build and the tests". */
function checkText(kinds: readonly CheckKind[]): string {
  const words = kinds.map((k) => CHECK_WORDS[k]);
  return `Ran ${words.length < 3 ? words.join(' and ') : `${words.slice(0, -1).join(', ')}, and ${words.at(-1)}`}`;
}

/** A check's result line: exit status, counts, time and why it is unclear. */
function checkDetail(c: EvidenceCheck): string {
  return [c.exit !== undefined ? `exit ${c.exit}` : '', c.counts ?? '', c.took_ms !== undefined ? formatMs(c.took_ms) : '', c.note ?? ''].filter(Boolean).join(' · ');
}

const MARK = {
  pass: <CheckCircle2 aria-label="Passed" className="mt-0.5 size-4 shrink-0 text-success" />,
  fail: <CircleX aria-label="Failed" className="mt-0.5 size-4 shrink-0 text-error" />,
  unclear: <CircleDashed aria-label="Unclear" className="mt-0.5 size-4 shrink-0 text-warning" />,
  running: <CircleDashed aria-label="Running" className="mt-0.5 size-4 shrink-0 text-muted" />,
};

/** "<1 min", "42 min", "3 h", "2 days" since an ISO time. */
export function away(mark: string, now: number): string {
  const min = Math.floor((now - Date.parse(mark)) / 60000);
  if (!Number.isFinite(min) || min < 1) return '<1 min';
  if (min < 60) return `${min} min`;
  const h = Math.floor(min / 60);
  if (h < 48) return `${h} h`;
  return `${Math.floor(h / 24)} days`;
}

/**
 * The finish card under a finished turn: evidence before prose. The service reads the whole
 * turn (GET /api/sessions/{id}/evidence; the outcome line reads the same): first the checks the
 * turn ran (tests, build, lint, vet, type checks) with their command, exit status, counts and
 * time, each opening its whole output; then each claim of the final message, backed or "Not
 * verified"; then the files the turn edited, which open Changes, and the actions. `commit` is
 * where the commit panel goes. Only sections with content show, and no card at all until the
 * evidence is read, nor while the turn ran no check, made no claim, edited no file and the Task
 * has no change to review or commit (a chat-only answer); once shown for a turn it stays, so a
 * commit's outcome stays in view after the files are committed.
 */
export function FinishCard({ session, items, changes, onShowOutput, onReview, commit }: Readonly<{
  session: SessionDetail;
  /** The items on hand: a check's whole item among them shows its output without a read. */
  items: readonly Item[];
  /** The Changes listing: its refresh also refreshes the card. */
  changes: Changes | null;
  onShowOutput: (output: CommandOutput) => void;
  /** Opens the Changes view; absent where the Task has none (no git). */
  onReview?: () => void;
  /** The commit panel, mounted between the files and the actions. */
  commit?: ReactNode;
}>) {
  const read = useBodyRead();
  const [evidence, setEvidence] = useState<TurnEvidence | null>(null);
  const [error, setError] = useState('');
  // A new turn end, a history read or a Changes refresh (an edit landed) reads it again.
  const turnEnd = session.turn_timings?.at(-1)?.ended_at;
  useEffect(() => {
    const controller = new AbortController();
    api.evidence(session.id, undefined, controller.signal).then((ev) => { setEvidence(ev); setError(''); }).catch((e: unknown) => {
      if (!(e instanceof DOMException && e.name === 'AbortError')) setError(describeError(e));
    });
    return () => controller.abort();
  }, [session.id, session.updated_at, session.history, turnEnd, changes]);
  const checks = evidence?.checks ?? [];
  const claims = evidence?.claims ?? [];
  const files = evidence?.files ?? [];
  const unverified = claims.filter((c) => !c.verified).length;
  const reviewable = !!onReview && (files.length > 0 || !!changes?.files.length);
  // Nothing until the evidence is read: no big empty card while it loads.
  const content = !!error || (!!evidence && (checks.length > 0 || claims.length > 0 || files.length > 0 || !!changes?.files.length));
  // Kept per turn end: a commit that empties Changes must not take its own outcome away.
  const turnKey = turnEnd ?? '';
  const [kept, setKept] = useState<string | null>(null);
  if (content && kept !== turnKey) setKept(turnKey);

  function show(c: EvidenceCheck) {
    const open = (body: Item) => onShowOutput({ id: c.item_id, name: c.command, title: c.command, text: body.tool?.output ?? '', markdown: false });
    const held = items.find((item) => item.id === c.item_id && !item.agent_id && !item.compact && !item.clipped);
    if (held || !read) return held && open(held);
    // Not on hand whole: the item's body, read once, has the output.
    read({ id: c.item_id, kind: 'tool', time: '', compact: { has_reasoning: false, has_text: false } }).then(open).catch((e: unknown) => setError(describeError(e)));
  }

  if (!content && kept !== turnKey) return null;
  return (
    <section aria-labelledby="finish-title" className="flex flex-col gap-3 rounded-lg bg-raised p-4 shadow-raised animate-rise sm:p-5">
      <div className="flex flex-wrap items-center gap-2">
        <h2 id="finish-title" className="text-title text-ink">Finished — check the evidence</h2>
        {unverified > 0 && <Chip className="bg-warning-wash text-warning">{unverified} {unverified === 1 ? 'claim' : 'claims'} not verified</Chip>}
      </div>
      {(checks.length > 0 || claims.length > 0) && <ul className="flex flex-col divide-y divide-hairline">
        {checks.map((c) => (
          <li key={c.item_id} className="flex items-start gap-3 py-2.5">
            {MARK[c.outcome]}
            <div className="min-w-0 flex-1">
              <p className="text-ui text-ink">{checkText(c.kinds)}</p>
              <p className="text-caption text-muted">
                <code className="font-mono text-code-sm break-all text-body">{c.command}</code>
                {checkDetail(c) && <span> · {checkDetail(c)}</span>}
              </p>
            </div>
            {c.has_output && <Button size="sm" variant="subtle" className="text-accent" onClick={() => show(c)}>Show output</Button>}
          </li>
        ))}
        {checks.length === 0 && (
          <li className="flex items-start gap-3 py-2.5">
            <CircleDashed aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-faint" />
            <p className="text-ui text-muted">No tests, builds or linters ran in this turn.</p>
          </li>
        )}
        {claims.map((c, i) => (
          <li key={`claim-${i}`} className="flex items-start gap-3 py-2.5">
            {c.verified ? <CheckCircle2 aria-label="Verified" className="mt-0.5 size-4 shrink-0 text-success" /> : <CircleDashed aria-label="Not verified" className="mt-0.5 size-4 shrink-0 text-warning" />}
            <div className="min-w-0 flex-1">
              <p className="text-ui text-ink">“{c.text}”</p>
              <p className={cn('text-caption break-words', c.verified ? 'text-muted' : 'text-warning')}>{c.verified ? `Backed by ${c.detail}` : c.detail}</p>
            </div>
          </li>
        ))}
      </ul>}
      {error && <p role="alert" className="text-caption text-error">{error}</p>}
      {files.length > 0 && (
        <div className="flex flex-col gap-1">
          <h3 className="text-eyebrow text-muted uppercase">Changed in this turn</h3>
          <ul className="flex max-h-60 flex-col overflow-y-auto overscroll-contain">
            {files.map((f) => {
              const row = (
                <>
                  <FileDiff aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
                  <span className="min-w-0 flex-1 truncate font-mono text-code-sm text-ink">{f.path}</span>
                  {f.additions !== undefined && <span className="shrink-0 font-mono text-meta"><span className="text-success">+{f.additions}</span> <span className="text-error">−{f.deletions ?? 0}</span></span>}
                </>
              );
              return (
                <li key={f.path}>
                  {onReview ? (
                    <button type="button" className="flex min-h-8 w-full items-center gap-3 rounded-sm px-2 text-left hover:bg-tint-hover pointer-coarse:min-h-11" onClick={onReview}>{row}</button>
                  ) : (
                    <div className="flex min-h-8 items-center gap-3 px-2">{row}</div>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      )}
      {commit}
      {reviewable && (
        <div className="flex flex-wrap items-center gap-2 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onReview}>Review changes</Button>
        </div>
      )}
    </section>
  );
}
