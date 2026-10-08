import { useApi } from '../ApiContext';
import { CheckCircle2, CircleDashed, CircleX, FileDiff, GitBranch, History, X } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { describeError, type Changes, type CheckKind, type EvidenceCheck, type Item, type SessionDetail, type TurnEvidence } from '../api';
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

/** The finished turn's evidence also drives the header's review dot. */
export function useTurnEvidence(session: SessionDetail, changes: Changes | null, finished: boolean) {
  const api = useApi();
  const turnEnd = session.turn_timings?.at(-1)?.ended_at;
  const key = `${session.id}:${session.updated_at}:${turnEnd}`;
  const [result, setResult] = useState<{ key: string; evidence: TurnEvidence | null; error: string } | null>(null);
  // The evidence names the files Changes lists, so a Changes refresh listing other files (an edit, a commit) reads it
  // again. The first list was read alongside the evidence: it changes nothing, nor does a refresh that lists the same.
  const listed = useMemo(() => changes && JSON.stringify(changes.files), [changes]);
  const [tree, setTree] = useState({ listed, version: 0 });
  if (listed !== null && listed !== tree.listed) setTree({ listed, version: tree.listed === null ? tree.version : tree.version + 1 });
  useEffect(() => {
    if (!finished) return;
    const controller = new AbortController();
    api.evidence(session.id, undefined, controller.signal).then((evidence) => {
      if (!controller.signal.aborted) setResult({ key, evidence, error: '' });
    }).catch((e: unknown) => {
      if (!controller.signal.aborted) setResult({ key, evidence: null, error: describeError(e) });
    });
    return () => controller.abort();
  }, [session.id, session.history, key, tree.version, finished, api]);
  const evidence = finished && result?.key === key ? result.evidence : null;
  const error = finished && result?.key === key ? result.error : '';
  const available = !!evidence && (evidence.checks.length > 0 || evidence.claims.length > 0 || evidence.files.length > 0);
  return { evidence, error, available };
}

/** One branch-named button always opens Changes, with its evidence in the same panel. */
export function ChangesButton({ branch, label, compact, sheetOpen, changes, evidenceAvailable, onOpen }: Readonly<{
  branch?: string;
  label: boolean;
  /** A narrower header: the branch truncates sooner. */
  compact?: boolean;
  sheetOpen: boolean;
  changes: Changes | null;
  evidenceAvailable: boolean;
  onOpen: () => void;
}>) {
  const fileCount = changes?.supported ? changes.files.length : null;
  const needsReview = evidenceAvailable || (fileCount ?? 0) > 0;
  return (
    <Button id="changes-link" size="md" className="px-2 text-muted" aria-label={`Open changes${branch ? ` on branch ${branch}` : ''}${fileCount === null ? '' : `, ${fileCount} files`}${evidenceAvailable ? ', evidence available' : ''}`} aria-pressed={sheetOpen} title={`Changes${branch ? ` · ${branch}` : ''}`} onClick={onOpen}>
      <GitBranch aria-hidden="true" />
      {label && <span className={cn('truncate', compact ? 'max-w-24' : 'max-w-40')}>{branch ?? 'Changes'}</span>}
      {fileCount !== null && <span className="tabular-nums text-ink">{fileCount}</span>}
      <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full', needsReview ? 'bg-warning' : 'bg-faint')} />
    </Button>
  );
}

/** Checks, claims and edited files inside Changes. Output opens the existing output panel. */
export function FinishEvidence({ evidence, error: loadError, items, onShowOutput }: Readonly<{
  evidence: TurnEvidence | null;
  error: string;
  items: readonly Item[];
  onShowOutput: (output: CommandOutput) => void;
}>) {
  const read = useBodyRead();
  const [outputError, setOutputError] = useState('');
  const checks = evidence?.checks ?? [];
  const claims = evidence?.claims ?? [];
  const files = evidence?.files ?? [];
  const unverified = claims.filter((c) => !c.verified).length;
  const error = loadError || outputError;
  if (!error && checks.length + claims.length + files.length === 0) return null;

  function show(c: EvidenceCheck) {
    const open = (body: Item) => onShowOutput({ id: c.item_id, name: c.command, title: c.command, text: body.tool?.output ?? '', markdown: false });
    const held = items.find((item) => item.id === c.item_id && !item.agent_id && !item.compact && !item.clipped);
    if (held || !read) return held && open(held);
    read({ id: c.item_id, kind: 'tool', time: '', compact: { has_reasoning: false, has_text: false } }).then(open).catch((e: unknown) => setOutputError(describeError(e)));
  }
  return (
    <section aria-label="Finished — check the evidence" className="max-h-[35%] shrink-0 overflow-y-auto overflow-x-hidden overscroll-contain border-b border-hairline px-3 py-2">
      <details open>
        <summary className="cursor-pointer text-ui font-medium text-ink">Finished — check the evidence</summary>
        <div className="flex flex-col gap-3 pt-2">
          {unverified > 0 && <Chip className="self-start bg-warning-wash text-warning">{unverified} {unverified === 1 ? 'claim' : 'claims'} not verified</Chip>}
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
              <ul className="flex max-h-60 flex-col overflow-y-auto overflow-x-hidden overscroll-contain">
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
                      <div className="flex min-h-8 items-center gap-3 px-2">{row}</div>
                    </li>
                  );
                })}
              </ul>
            </div>
          )}
        </div>
      </details>
    </section>
  );
}
