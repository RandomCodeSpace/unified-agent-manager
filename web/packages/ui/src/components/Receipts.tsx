import { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import { cn } from '../lib/cn';
import { MAX_STAMPS, settle, type Stamp } from '../lib/receipts';
import { useBodyRead } from './Details';
import { useLocateItem } from './Subagents';
import { Button } from './ui/button';

// Receipts (DESIGN.md Receipts): under a turn's last reply, the claims its record proves wrong,
// each stamped with what the record shows. Silent otherwise: a claim that holds, or one the turn
// did not touch, says nothing. A stamp opens the call that is the evidence. A stamp waiting on a
// call's whole output reads it here and settles, showing only if it settles contradicted; the
// first MAX_STAMPS show, the rest on request.

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
  const shown = stamps.map((s) => settled.get(s) ?? s).filter((s) => s.verdict === 'contradicted');
  if (!shown.length) return null;
  const more = all ? 0 : Math.max(0, shown.length - MAX_STAMPS);
  return (
    <ul aria-label="Receipts" className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1">
      {(more ? shown.slice(0, MAX_STAMPS) : shown).map((s) => {
        const body = (
          <>
            <X aria-hidden="true" className="size-3 shrink-0 text-error" strokeWidth={2.5} />
            <span className={cn('min-w-0 truncate', s.kind === 'tests' ? '' : 'font-mono text-code-sm')}>{s.claim}</span>
            <span className="shrink-0 text-muted">{s.note}</span>
          </>
        );
        const className = 'flex h-6 max-w-full items-center gap-1.5 rounded-sm bg-error-wash px-1.5 text-meta tabular-nums text-error transition-colors duration-100';
        return (
          <li key={`${s.kind}:${s.claim}`} className="min-w-0 max-w-full">
            {s.itemId && locate ? (
              <button type="button" className={cn(className, 'hover:bg-tint-hover pointer-coarse:min-h-11')} title="Contradicted by the record. Show the call." onClick={() => void locate(s.itemId!)}>
                {body}
              </button>
            ) : (
              <span className={className} title="Contradicted by the record">{body}</span>
            )}
          </li>
        );
      })}
      {more > 0 && (
        <li>
          <Button variant="link" className="px-1.5 text-meta" onClick={() => setAll(true)}>
            {`Show ${more} more`}
          </Button>
        </li>
      )}
    </ul>
  );
}
