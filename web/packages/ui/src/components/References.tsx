import { useEffect, useMemo, useState, type ReactNode, type RefObject } from 'react';
import { useApi } from '../ApiContext';
import type { GitLabItemSummary, Item, Reference } from '../api';
import { cn } from '../lib/cn';
import { refKey, type GitLabRef } from '../lib/gitlabRefs';
import { citesReferences, gitLabKind, mismatched, referenceName, referenceSummary, referenceWords } from '../lib/references';
import { useDisclosure } from './Details';
import { held, put, useHeld } from './ReferenceCite';
import { stateBlocks, useSummaries, useTurnGitLabRefs } from './GitLabStrip';
import { useLocateItem } from './Subagents';
import { Blocks, type Block, type BlockTone } from './ui/blocks';
import { Collapse, EXIT_MS } from './ui/collapse';

// References ledger (DESIGN.md turn outcome): the numbered items the reply that ends a turn cites
// ("issues 4, 8 and 17"), each resolved by the service to where the Thread itself defined it.
// One disclosure on the sign-off counts them by recorded state; open, a row per item in the reply's
// order jumps to its definition. Each cited number in the reply's prose opens the list on its row.

/* ---------- the ledger on the sign-off ---------- */

export interface Ledger {
  /** "6 issues · 4 open · 2 closed · 1 unknown"; undefined when the reply cites nothing resolved. */
  summary?: string;
  open: boolean;
  /** Whether the list was ever opened (it mounts on the first open). */
  opened: boolean;
  toggle: () => void;
  listId: string;
  /** The GitLab items the list shows, by `refKey`: the turn's GitLab chips leave them out. */
  hide?: ReadonlySet<string>;
  /** The list, for under the sign-off. */
  list: ReactNode;
}

const NO_CALLS: Item[] = [];

/**
 * The references of `reply`, the main agent's reply that ends its turn (undefined otherwise), read
 * when its text cites a numbered item and again when that text or the turn's end changes. A failed
 * read shows nothing. `calls` are the turn's GitLab candidates: a cited issue or merge request the
 * turn touched shows the state its GitLab chip read, and that chip goes.
 */
export function useReferenceLedger({ id, sessionId, reply, end, calls, anchor }: Readonly<{ /** The sign-off's DOM id: the list's is made from it. */ id: string; sessionId?: string; reply?: Item; end?: string; calls?: Item[]; anchor: RefObject<HTMLElement | null> }>): Ledger {
  const api = useApi();
  const replyId = reply && !reply.agent_id ? reply.id : undefined;
  const text = reply?.text ?? '';
  const ended = end ?? '';
  const want = !!replyId && citesReferences(text);
  const [key, entry] = useHeld(sessionId, replyId);
  useEffect(() => {
    if (!want || !key || !sessionId || !replyId) return;
    const before = held.get(key);
    if (before && before.text === text && before.end === ended) return;
    const controller = new AbortController();
    api.references(sessionId, replyId, controller.signal).then(
      (answer) => put(key, { text, end: ended, refs: answer.references ?? [], pick: held.get(key)?.pick }),
      () => { if (!controller.signal.aborted) put(key, { text, end: ended, refs: [], pick: held.get(key)?.pick }); },
    );
    return () => controller.abort();
  }, [api, key, want, sessionId, replyId, text, ended]);
  const answered = entry?.refs;
  const refs = useMemo(() => (want ? answered ?? [] : []), [want, answered]);

  // A cited issue or merge request the turn's GitLab calls touched: its row shows the chip's state, read for the chip anyway.
  const touched = useTurnGitLabRefs(calls ?? NO_CALLS);
  const matched = useMemo(() => {
    const out = new Map<number, GitLabRef>();
    refs.forEach((r, i) => {
      const kind = gitLabKind(r);
      const found = kind && touched.find((g) => g.kind === kind && String(g.id) === r.number);
      if (found) out.set(i, found);
    });
    return out;
  }, [refs, touched]);
  const summaries = useSummaries(sessionId ?? '', [...matched.values()], anchor);
  const hide = useMemo(() => (matched.size ? new Set([...matched.values()].map(refKey)) : undefined), [matched]);

  const [open, setOpen] = useDisclosure(`references:${key}`);
  const [opened, setOpened] = useState(open);
  const listId = `${id}-references`;
  // A number in the prose opens the list on its row; a pick from before this mounted is not a new one.
  const pick = entry?.pick;
  const [handled, setHandled] = useState(pick?.seq ?? 0);
  const [flash, setFlash] = useState<{ index: number; seq: number } | null>(null);
  if (pick && pick.seq !== handled) {
    setHandled(pick.seq);
    setOpened(true);
    setOpen(true);
    setFlash(pick);
  }
  useEffect(() => {
    if (!flash) return;
    // The list may be opening: the row lands where it ends up.
    const timer = window.setTimeout(() => {
      const row = document.getElementById(`${listId}-${flash.index}`);
      if (!row) return;
      row.scrollIntoView({ block: 'nearest' });
      row.classList.add('animate-flash');
      window.setTimeout(() => row.classList.remove('animate-flash'), 1400);
    }, EXIT_MS);
    return () => window.clearTimeout(timer);
  }, [flash, listId]);

  const toggle = () => {
    setOpened(true);
    setOpen((o) => !o);
  };
  const list = opened && refs.length > 0 && (
    <Collapse open={open} appear className="-mt-3" inner="pt-3">
      <div aria-hidden="true" className="fade-rule" />
      <ul id={listId} aria-label="References this reply cites" className="flex flex-col py-1 text-caption">
        {refs.map((r, i) => <ReferenceRow key={`${r.label}:${i}`} id={`${listId}-${i}`} r={r} gitlab={matched.has(i) ? summaries.get(refKey(matched.get(i)!)) : undefined} />)}
      </ul>
    </Collapse>
  );
  return { summary: refs.length ? referenceSummary(refs) : undefined, open, opened, toggle, listId, hide, list };
}

/** A recorded state's tone: open is `success`, a closed or finished one `accent`, the rest neutral. */
function stateTone(state: string): BlockTone {
  const s = state.toLowerCase();
  if (s === 'open' || s === 'opened') return 'success';
  if (s === 'closed' || s === 'merged' || s === 'done' || s === 'fixed' || s === 'resolved') return 'accent';
  return 'none';
}
const DOT: Record<BlockTone, string> = { success: 'bg-success', accent: 'bg-accent', warning: 'bg-warning', error: 'bg-error', attention: 'bg-attention', none: 'bg-faint' };
const ROW = 'flex w-full min-w-0 min-h-7 items-center gap-2 rounded-sm px-1 py-0.5 text-left pointer-coarse:min-h-11';

/** One cited item: state dot · "#4" · title · state; a mismatch with the reply's own state word in `error`. */
function ReferenceRow({ id, r, gitlab }: Readonly<{ id: string; r: Reference; gitlab?: GitLabItemSummary }>) {
  const locate = useLocateItem();
  const bad = mismatched(r);
  const tone: BlockTone = bad ? 'error' : stateTone(r.state ?? '');
  const state = r.state ?? '';
  const chip: Block[] = gitlab ? stateBlocks(gitlab) : state ? [{ key: 'state', tone, label: state.charAt(0).toUpperCase() + state.slice(1), title: bad ? `${state}, the reply says ${r.claimed}` : state }] : [];
  const resolved = r.status === 'resolved';
  const words = referenceWords(r);
  const body = (
    <>
      <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', resolved ? DOT[tone] : 'border border-dashed border-faint')} />
      <span className="shrink-0 font-semibold tabular-nums text-ink">{referenceName(r)}</span>
      <span className={cn('min-w-0 flex-1 break-words', !resolved ? 'text-muted' : bad ? 'text-error' : 'text-body')}>
        {words}
        {bad && <span className="whitespace-nowrap"> · reply says {r.claimed}</span>}
      </span>
      <Blocks items={chip} />
    </>
  );
  const target = r.source?.item_id;
  return (
    <li id={id} className={cn('min-w-0 rounded-sm', bad && 'text-error')}>
      {target && locate && r.status !== 'ambiguous' ? (
        <button type="button" title="Show where this Thread defined it" className={cn(ROW, 'transition-colors duration-100 hover:bg-tint-well')} onClick={() => locate(target)}>{body}</button>
      ) : (
        <div className={ROW}>{body}</div>
      )}
      {r.status === 'ambiguous' && !!r.candidates?.length && (
        <ul aria-label={`Matches for ${referenceName(r)}`} className="flex flex-col pl-7">
          {r.candidates.map((c, k) => {
            const words = <span className="min-w-0 break-words">{c.noun ? <span className="text-muted">{c.noun} · </span> : null}{c.title}</span>;
            return (
              <li key={`${c.item_id ?? ''}:${k}`} className="min-w-0">
                {c.item_id && locate ? (
                  <button type="button" title="Show where this Thread defined it" className={cn(ROW, 'text-body transition-colors duration-100 hover:bg-tint-well')} onClick={() => locate(c.item_id!)}>{words}</button>
                ) : (
                  <div className={cn(ROW, 'text-body')}>{words}</div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </li>
  );
}
