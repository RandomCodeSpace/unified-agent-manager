import { CheckCircle2, CircleDashed, CircleX, FileDiff, History, X } from 'lucide-react';
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { describeError, type Changes, type Item, type SessionDetail } from '../api';
import { CHECK_TEXT, checkFrom, editedPaths, finalMessage, judgeClaims, lastTurn, type Check } from '../lib/evidence';
import { cn } from '../lib/cn';
import type { CommandOutput } from './CommandOutput';
import { useBodyRead } from './Details';
import { Button } from './ui/button';
import { Chip } from './ui/chip';

/**
 * "Since you left": one line under the Task header when the Task changed while the owner was
 * away (lib/evidence `sinceYouLeft`), with a way to the first thing they have not seen.
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

/** A check's result line: exit status, counts, time and why it is unclear. */
function checkDetail(c: Check): string {
  return [c.exit !== null ? `exit ${c.exit}` : '', c.counts?.label ?? '', c.took ?? '', c.note].filter(Boolean).join(' · ');
}

const MARK = {
  pass: <CheckCircle2 aria-label="Passed" className="mt-0.5 size-4 shrink-0 text-success" />,
  fail: <CircleX aria-label="Failed" className="mt-0.5 size-4 shrink-0 text-error" />,
  unclear: <CircleDashed aria-label="Unclear" className="mt-0.5 size-4 shrink-0 text-warning" />,
  running: <CircleDashed aria-label="Running" className="mt-0.5 size-4 shrink-0 text-muted" />,
};

/**
 * The finish card under a finished turn: evidence before prose. First the checks the turn
 * ran (tests, build, lint, vet, type checks) with their command, exit status, counts and
 * time, each opening its whole output; then each claim of the final message, backed or "Not
 * verified" (lib/evidence `judgeClaims`); then the files the turn edited, which open Changes,
 * and the actions. `commit` is where the commit panel goes.
 */
export function FinishCard({ session, items, changes, onShowOutput, onReview, commit }: Readonly<{
  session: SessionDetail;
  /** The Task's newest items; the card reads the last turn from them. */
  items: readonly Item[];
  changes: Changes | null;
  onShowOutput: (output: CommandOutput) => void;
  /** Opens the Changes view; absent where the Task has none (no git). */
  onReview?: () => void;
  /** The commit panel, mounted between the files and the actions. */
  commit?: ReactNode;
}>) {
  const read = useBodyRead();
  const turn = useMemo(() => lastTurn(items).filter((item) => !item.agent_id), [items]);
  const candidates = useMemo(() => turn.filter((item) => checkFrom(item)), [turn]);
  // A compact item holds no output: the exit status and counts come from its whole body, read once.
  const [bodies, setBodies] = useState<ReadonlyMap<string, Item>>(new Map());
  const [error, setError] = useState('');
  const wanted = candidates.filter((item) => (item.compact || item.clipped) && item.tool?.status !== 'running' && item.tool?.status !== 'pending' && !bodies.has(item.id));
  const wantedKey = wanted.map((item) => item.id).join(',');
  useEffect(() => {
    if (!read || !wantedKey) return;
    let alive = true;
    for (const item of wanted) {
      read(item).then((body) => { if (alive) setBodies((m) => new Map(m).set(item.id, body)); }).catch((e: unknown) => { if (alive && !(e instanceof DOMException && e.name === 'AbortError')) setError(describeError(e)); });
    }
    return () => { alive = false; };
    // The ids name the reads; the items themselves change identity with every frame.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [read, wantedKey]);
  const checks = candidates.map((item) => checkFrom(item, bodies.get(item.id))).filter((c): c is Check => !!c);
  const claims = judgeClaims(finalMessage(turn), checks, turn, session.workdir);
  const unverified = claims.filter((c) => !c.verified).length;
  const files = editedPaths(turn, session.workdir);
  const stat = new Map(changes?.files.map((f) => [f.path, f]) ?? []);

  function show(c: Check) {
    const open = (body: Item) => onShowOutput({ id: c.item.id, name: c.command, title: c.command, text: body.tool?.output ?? '', markdown: false });
    const held = bodies.get(c.item.id) ?? (!c.item.compact && !c.item.clipped ? c.item : undefined);
    if (held || !read) open(held ?? c.item);
    else read(c.item).then(open).catch((e: unknown) => setError(describeError(e)));
  }

  return (
    <section aria-labelledby="finish-title" className="flex flex-col gap-3 rounded-lg bg-raised p-4 shadow-raised animate-rise sm:p-5">
      <div className="flex flex-wrap items-center gap-2">
        <h2 id="finish-title" className="text-title text-ink">Finished — check the evidence</h2>
        {unverified > 0 && <Chip className="bg-warning-wash text-warning">{unverified} {unverified === 1 ? 'claim' : 'claims'} not verified</Chip>}
      </div>
      <ul className="flex flex-col divide-y divide-hairline">
        {checks.map((c) => (
          <li key={c.item.id} className="flex items-start gap-3 py-2.5">
            {MARK[c.outcome]}
            <div className="min-w-0 flex-1">
              <p className="text-ui text-ink">{CHECK_TEXT[c.kind]}</p>
              <p className="text-caption text-muted">
                <code className="font-mono text-code-sm break-all text-body">{c.command}</code>
                {checkDetail(c) && <span> · {checkDetail(c)}</span>}
              </p>
            </div>
            {(c.item.tool?.has_output || c.item.tool?.output) && <Button size="sm" variant="subtle" className="text-accent" onClick={() => show(c)}>Show output</Button>}
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
      </ul>
      {error && <p role="alert" className="text-caption text-error">{error}</p>}
      {files.length > 0 && (
        <div className="flex flex-col gap-1">
          <h3 className="text-eyebrow text-muted uppercase">Changed in this turn</h3>
          <ul className="flex max-h-60 flex-col overflow-y-auto overscroll-contain">
            {files.map((path) => {
              const f = stat.get(path);
              const row = (
                <>
                  <FileDiff aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
                  <span className="min-w-0 flex-1 truncate font-mono text-code-sm text-ink">{path}</span>
                  {f && <span className="shrink-0 font-mono text-meta"><span className="text-success">+{f.additions}</span> <span className="text-error">−{f.deletions}</span></span>}
                </>
              );
              return (
                <li key={path}>
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
      <div className="flex flex-wrap items-center gap-2 max-sm:[&>button]:flex-1">
        {onReview && (files.length > 0 || !!changes?.files.length) && <Button variant="secondary" onClick={onReview}>Review changes</Button>}
      </div>
    </section>
  );
}
