// Where a turn stands, read the same way by the status line, the transcript and the composer.

import type { Item, SessionState, TurnTiming } from '../api';

/**
 * A turn's timing as the page reads it: `idle` none yet, `running` no end, `ended` it has an end,
 * `cut` the service cut it off (unknown without an end, #434). `connected`: the event stream is up;
 * without it an unknown timing is the running one this page lost track of, so it still runs.
 */
export type TurnState = 'idle' | 'running' | 'ended' | 'cut';

export function turnState(timing: TurnTiming | undefined, connected: boolean): TurnState {
  if (!timing) return 'idle';
  if (timing.ended_at) return 'ended';
  return connected && timing.state === 'unknown' ? 'cut' : 'running';
}

/** An ordinary message, which starts a turn; a steer or an autopilot message joins the running one. */
export const isRealUserTurn = (item: Pick<Item, 'kind' | 'delivery'>) => item.kind === 'user' && !item.delivery;

/** A tool call still waiting or running. */
export const openCall = (status?: string) => status === 'pending' || status === 'running';

/** The Thread runs a turn, or is starting to. */
export const turnBusy = (state: SessionState) => state === 'working' || state === 'starting';
