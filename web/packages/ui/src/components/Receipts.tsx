import { useEffect, useState } from 'react';
import { Check, CircleDashed, Hourglass, X } from 'lucide-react';
import { cn } from '../lib/cn';
import { allUnseen, allVerified, MAX_STAMPS, settle, type Stamp } from '../lib/receipts';
import { useBodyRead } from './Details';
import { useLocateItem } from './Subagents';

// Receipts (DESIGN.md Receipts): under a turn's last reply, each path, command and test claim
// it makes, stamped with what the record shows. Quiet when every claim holds: one line that says
// so. A stamp with evidence opens the call that is the evidence. A stamp waiting on a call's
// whole output reads it here and settles; the first MAX_STAMPS show, the rest on request.

const MARK = {
  verified: <Check aria-hidden="true" className="size-3 shrink-0 text-success" strokeWidth={2.5} />,
  unseen: <CircleDashed aria-hidden="true" className="size-3 shrink-0 text-warning" strokeWidth={2.25} />,
  contradicted: <X aria-hidden="true" className="size-3 shrink-0 text-error" strokeWidth={2.5} />,
  pending: <Hourglass aria-hidden="true" className="size-3 shrink-0 text-muted" strokeWidth={2.25} />,
} as const;

const WORD = { verified: 'matches the record', unseen: 'not seen in the record', contradicted: 'contradicted by the record', pending: 'still being checked against the record' } as const;

export function Receipts({ stamps }: Readonly<{ stamps: readonly Stamp[] }>) {
  const locate = useLocateItem();
  const read = useBodyRead();
  // Pending stamps by what they settled to, keyed by the pending stamp itself (stamps keep their identity while the turn's calls are unchanged).
  const [settled, setSettled] = useState<ReadonlyMap<Stamp, Stamp>>(new Map());
  const [all, setAll] = useState(false);
  useEffect(() => {
    let alive = true;
    for (const stamp of stamps) {
      if (!stamp.awaits) continue;
      const body = stamp.awaits;
      (read ? read(body) : Promise.resolve(body)).then(
        (whole) => settle(stamp, whole),
        // A read the Task's change aborted is not an answer; the stamp waits for the next render's.
        (reason: unknown) => (reason instanceof DOMException && reason.name === 'AbortError' ? undefined : { ...stamp, verdict: 'unseen' as const, note: 'test output could not be read', awaits: undefined }),
      ).then((done) => { if (alive && done) setSettled((held) => new Map(held).set(stamp, done)); }, () => {});
    }
    return () => { alive = false; };
  }, [stamps, read]);
  const shown = stamps.map((s) => settled.get(s) ?? s);
  if (!shown.length) return null;
  if (allVerified(shown)) {
    return (
      <p className="mt-2 flex items-center gap-1.5 text-meta text-muted" title={shown.map((s) => `${s.claim}: ${s.note}`).join('\n')}>
        {MARK.verified}
        {shown.length === 1 ? 'Its one claim matches the record' : `Its ${shown.length} claims match the record`}
      </p>
    );
  }
  if (allUnseen(shown)) {
    const aside = shown[0].note.split(' · ')[1];
    return (
      <p className="mt-2 flex items-center gap-1.5 text-meta text-muted" title={shown.map((s) => `${s.claim}: ${s.note}`).join('\n')}>
        {MARK.unseen}
        {`${shown.length} ${shown.length === 1 ? 'claim' : 'claims'} not seen in the main agent’s work${aside ? ` · ${aside}` : ''}`}
      </p>
    );
  }
  const more = all ? 0 : Math.max(0, shown.length - MAX_STAMPS);
  return (
    <ul aria-label="Receipts" className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1">
      {(more ? shown.slice(0, MAX_STAMPS) : shown).map((s) => {
        const body = (
          <>
            {MARK[s.verdict]}
            <span className={cn('min-w-0 truncate', s.kind === 'tests' ? '' : 'font-mono text-code-sm')}>{s.claim}</span>
            <span className="shrink-0 text-muted">{s.note}</span>
          </>
        );
        const className = cn('flex h-6 max-w-full items-center gap-1.5 rounded-sm px-1.5 text-meta tabular-nums transition-colors duration-100', s.verdict === 'contradicted' ? 'bg-error-wash text-error' : 'bg-tint-well text-body');
        return (
          <li key={`${s.kind}:${s.claim}`} className="min-w-0 max-w-full">
            {s.itemId && locate ? (
              <button type="button" className={cn(className, 'hover:bg-tint-hover pointer-coarse:min-h-11')} title={`${WORD[s.verdict]}. Show the call.`} onClick={() => void locate(s.itemId!)}>
                {body}
              </button>
            ) : (
              <span className={className} title={WORD[s.verdict]}>{body}</span>
            )}
          </li>
        );
      })}
      {more > 0 && (
        <li>
          <button type="button" className="h-6 rounded-sm px-1.5 text-meta text-accent hover:underline pointer-coarse:min-h-11" onClick={() => setAll(true)}>
            {`Show ${more} more`}
          </button>
        </li>
      )}
    </ul>
  );
}
