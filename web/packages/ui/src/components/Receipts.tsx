import { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import { cn } from '../lib/cn';
import { useApi } from '../ApiContext';
import type { Item } from '../api';
import { bySubagents, MAX_STAMPS, settle, type Stamp } from '../lib/receipts';
import { mainCall } from '../lib/subagents';
import { useBodyRead } from './Details';
import { useActions, useLocateItem, useScope } from './Subagents';
import { Button } from './ui/button';

// Receipts (DESIGN.md Receipts): under a turn's last reply, the claims its record proves wrong,
// each stamped with what the record shows. Silent otherwise: a claim that holds, or one the turn
// did not touch, says nothing. A stamp opens the call that is the evidence. A stamp waiting on a
// call's whole output reads it here and settles, showing only if it settles contradicted; the
// first MAX_STAMPS show, the rest on request. A claim the main agent's calls leave unseen is
// judged again once the turn's subagents' transcripts are read; its stamp opens that subagent.

/** `stamps`, those that ask for it judged again against the calls of the subagents the turn started, once read; as given until then, or outside a Thread. */
function useSubagentEvidence(stamps: readonly Stamp[]): readonly Stamp[] {
  const api = useApi();
  const calls = stamps.find((s) => s.subagentCalls)?.subagentCalls;
  const sessionId = useScope((d) => d.task.id);
  // The subagents those calls started (and the ones they spawned) as a string, so a session update that leaves them alone reads nothing again.
  const spawned = useScope((d) => (calls ? JSON.stringify(d.subagents.filter((s) => calls.includes(mainCall(s, d.subagents) ?? '')).map((s) => [s.id, s.name, s.status])) : ''));
  const [judged, setJudged] = useState<{ of: readonly Stamp[]; stamps: readonly Stamp[] } | null>(null);
  useEffect(() => {
    const list = spawned ? (JSON.parse(spawned) as [string, string, string][]) : [];
    if (!sessionId || !list.length) return;
    const controller = new AbortController();
    // A transcript that cannot be read is no evidence: its claims stay unseen.
    void Promise.all(list.map(([id]) => api.subagent(sessionId, id, controller.signal).then((d) => d.items, (): Item[] => []))).then((lists) => {
      if (!controller.signal.aborted) setJudged({ of: stamps, stamps: bySubagents(stamps, lists.flat(), new Map(list.map(([id, name]) => [id, name]))) });
    });
    return () => controller.abort();
  }, [api, sessionId, spawned, stamps]);
  return judged?.of === stamps ? judged.stamps : stamps;
}

export function Receipts({ stamps: given }: Readonly<{ stamps: readonly Stamp[] }>) {
  const stamps = useSubagentEvidence(given);
  const locate = useLocateItem();
  const show = useActions()?.show;
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
            <span className="shrink-0 text-muted">{s.by ? `${s.note} · by ${s.by.name}` : s.note}</span>
          </>
        );
        const className = 'flex h-6 max-w-full items-center gap-1.5 rounded-sm bg-error-wash px-1.5 text-meta tabular-nums text-error transition-colors duration-100';
        return (
          <li key={`${s.kind}:${s.claim}`} className="min-w-0 max-w-full">
            {s.by && show ? (
              <button type="button" className={cn(className, 'hover:bg-tint-hover pointer-coarse:min-h-11')} title="Contradicted by the record. Show the subagent." onClick={() => show(s.by!.agentId)}>
                {body}
              </button>
            ) : s.itemId && locate ? (
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
