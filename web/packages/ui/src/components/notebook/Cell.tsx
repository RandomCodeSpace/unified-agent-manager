// One turn of the main conversation in the notebook layout (lib/layout). Transcript hands it the
// parts the current layout stands in rows (the turn's message, what stands in its answer, its head),
// and the cell only places them: a gutter with the turn's number, its state in a word, its duration
// and its age; then two tracks, "said" (the message and the answer) and "did" (the head, which opens
// the turn's timeline). The tracks stand side by side while the cell is wide enough and one under the
// other when it is not: a container query on the cell, in index.css with the gutter's own rule.

import type { ReactNode } from 'react';
import type { TurnTiming } from '../../api';
import { cn } from '../../lib/cn';
import { completedDuration } from '../../lib/transcript';
import { dateTime, timeAgo, useMinuteTick } from '../common';
import { useNotebookSlotRef } from './slots';

export type CellState = 'running' | 'asked' | 'done' | 'failed' | 'stopped';

/**
 * A turn's state, from what its head reads already: it runs, or the Thread holds it while it waits
 * on the owner, else how its timing ended. None for a turn whose end was never recorded.
 */
export function cellState(runs: boolean, waits: boolean, timing: TurnTiming | undefined): CellState | undefined {
  if (runs) return 'running';
  if (waits) return 'asked';
  if (timing?.state === 'completed') return 'done';
  if (timing?.state === 'failed') return 'failed';
  return timing?.state === 'cancelled' ? 'stopped' : undefined;
}

/** Each state's word and its tone; the turn at work and the one that waits also mark the gutter's edge. */
const STATES: Record<CellState, { word: string; tone: string; mark?: string }> = {
  running: { word: 'Running', tone: 'text-accent', mark: 'before:bg-accent' },
  asked: { word: 'Asked you', tone: 'text-attention', mark: 'before:bg-attention' },
  done: { word: 'Done', tone: 'text-muted' },
  failed: { word: 'Failed', tone: 'text-error' },
  stopped: { word: 'Stopped', tone: 'text-muted' },
};

/** What Transcript knows of a turn beyond its head and its answer. */
export interface CellParts {
  /** The turn's place among the Thread's turn timings, from 1; none without a timing. */
  number?: number;
  state?: CellState;
  timing?: TurnTiming;
  /** The turn's message, when the loaded window holds it. */
  prompt?: ReactNode;
  /** The last turn at the Thread's live tail: its cell gives the Thread the two slots (notebook/slots). */
  live: boolean;
  /** The last turn's foot: the status of a turn with nothing in it yet, under the head. */
  status?: ReactNode;
  /** The last turn's foot: the step in progress, under the answer. */
  tail?: ReactNode;
}

/** How long ago the turn ended, kept current by the minute. */
function Age({ at }: Readonly<{ at: string }>) {
  useMinuteTick();
  return <time dateTime={at} title={dateTime(at)} className="block">{timeAgo(at)}</time>;
}

export function Cell({ replyKey, number, state, timing, prompt, live, status, tail, did, children }: Readonly<CellParts & { /** The reply's key, by which the Thread finds the turn (`data-reply`). */ replyKey: string; /** The turn's head. */ did: ReactNode; /** What stands in the answer. */ children?: ReactNode }>) {
  const liveSlot = useNotebookSlotRef('live');
  const asksSlot = useNotebookSlotRef('asks');
  const shown = state && STATES[state];
  const took = completedDuration(timing);
  return (
    <div
      data-reply={replyKey}
      data-cell={state ?? ''}
      className={cn('nb-cell relative flex border-t border-hairline pt-4', shown?.mark && ["before:absolute before:inset-y-0 before:-left-3 before:w-0.5 before:rounded-full before:content-['']", shown.mark])}
    >
      <div className="nb-gutter pr-3 text-stamp leading-normal tabular-nums text-muted">
        {number !== undefined && <div className="text-display-sm text-ink"><span className="sr-only">Turn </span>{number}</div>}
        {shown && <div data-cell-state="" className={cn('text-caption font-medium', shown.tone)}>{shown.word}</div>}
        {took && <div className="mt-1" title="Recorded foreground turn duration">{took}</div>}
        {timing?.ended_at && <Age at={timing.ended_at} />}
      </div>
      <div className="nb-tracks">
        <div data-track="said" className="nb-said flex min-w-0 flex-col gap-4">
          {prompt}
          <div className="nb-answer flex flex-col gap-3 empty:hidden">{children}</div>
          {tail}
          {/* Where the Thread draws a question or a permission that waits; it takes no room until then. */}
          {live && <div ref={asksSlot} data-notebook-slot="asks" className="empty:hidden" />}
        </div>
        {/* Nothing in it (a turn without a head, a slot not filled): the track leaves, so a folded cell keeps no gap for it. */}
        <div data-track="did" className="nb-did hidden min-w-0 flex-col gap-3 has-[>:not(:empty)]:flex">
          {/* Where the Thread draws the live card, above the head. */}
          {live && <div ref={liveSlot} data-notebook-slot="live" className="empty:hidden" />}
          {did}
          {status}
        </div>
      </div>
    </div>
  );
}
