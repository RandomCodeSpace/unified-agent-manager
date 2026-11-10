import type { OutlineItem, SessionDetail, TurnTiming } from '../api';
import { mergeOutline, replyIndex } from './subagents.ts';

// The open Thread's turns as a list (the notebook layout's Contents column). A turn is what the
// conversation draws as one: a user message that is not a steer, and the reply up to the next one.
// Its key is the conversation's own (`data-reply`: the message's id, `start` before the first), and
// its number is the reply index's (the turn's place in the Thread's turn timings), so the list and
// the conversation cannot name a turn differently.

/** How a turn stands, in a word. */
export type TurnWord = 'Done' | 'Running' | 'Asked you' | 'Failed' | 'Stopped' | 'Queued';

export interface Turn {
  /** The turn's reply key; a queued message has none yet (`queue:` and its request). */
  key: string;
  /** From 1; absent for a turn without a timing in a Thread that has timings. */
  number?: number;
  /** The first line of its prompt; empty for a turn without a message, or whose message is not held here. */
  text: string;
  word?: TurnWord;
  /** A message that waits for the running turn: not in the conversation yet, so nothing to jump to. */
  queued?: boolean;
}

type Detail = Pick<SessionDetail, 'outline' | 'history_index' | 'history_before' | 'items' | 'turn_timings' | 'queue' | 'state'>;

const startsTurn = (item: OutlineItem) => item.kind === 'user' && !item.delivery;
const firstLine = (text: string) => text.trim().split('\n', 1)[0].slice(0, 160);

/** The last turn of a Thread, from the Thread's own state: for a turn the Thread holds no timing for. */
function threadWord(state: Detail['state']): TurnWord | undefined {
  switch (state) {
    case 'awaiting_permission': case 'awaiting_answer': return 'Asked you';
    case 'working': case 'starting': return 'Running';
    case 'completed': return 'Done';
    case 'failed': return 'Failed';
    case 'cancelled': case 'interrupted': return 'Stopped';
    default: return undefined;
  }
}

function word(timing: TurnTiming | undefined, last: boolean, state: Detail['state']): TurnWord | undefined {
  const now = last ? threadWord(state) : undefined;
  if (!timing) return now;
  switch (timing.state) {
    case 'completed': return 'Done';
    case 'failed': return 'Failed';
    // A timing left open by a Thread that no longer runs was cut off.
    case 'working': return now === 'Running' || now === 'Asked you' ? now : 'Stopped';
    // Cancelled, or cut off by the service without an end.
    default: return 'Stopped';
  }
}

/** The Thread's turns in order, then its queued messages. */
export function turnList(detail: Detail): Turn[] {
  const timings = detail.turn_timings ?? [];
  const index = detail.history_index ?? detail.items;
  // The server's outline holds every message, also of pages not loaded here; the index adds what arrived since.
  const messages = mergeOutline(detail.outline, index.filter(startsTurn)).filter(startsTurn);
  const numbers = replyIndex(messages, [], timings).turns;
  const byMessage = new Map(timings.flatMap((t) => (t.user_item_id ? [[t.user_item_id, t] as const] : [])));
  const turns: Turn[] = [];
  // A Thread from before turn timings numbers its turns by their place.
  const number = (key: string) => (timings.length ? numbers.get(key) : turns.length + 1);
  // What the Thread did before its first message is the conversation's `start` turn.
  const first = index[0];
  if (first && !startsTurn(first) && !detail.history_before && !messages.some((m) => m.time < first.time)) {
    const timing = timings[0]?.user_item_id ? undefined : timings[0];
    turns.push({ key: 'start', number: timing ? 1 : number('start'), text: '', word: word(timing, !messages.length, detail.state) });
  }
  const rows = messages.map((m) => ({ key: m.id, time: Date.parse(m.time), text: firstLine(m.text ?? ''), timing: byMessage.get(m.id) }));
  // A turn whose message is not held here (no outline from an older server, and its page not loaded) keeps its row.
  const held = new Set(messages.map((m) => m.id));
  for (const t of timings) if (t.user_item_id && !held.has(t.user_item_id)) rows.push({ key: t.user_item_id, time: Date.parse(t.started_at), text: '', timing: t });
  rows.sort((a, b) => a.time - b.time);
  rows.forEach((row, i) => {
    const last = row.timing ? row.timing === timings.at(-1) : i === rows.length - 1;
    turns.push({ key: row.key, number: number(row.key), text: row.text, word: word(row.timing, last, detail.state) });
  });
  const ran = timings.length || turns.length;
  (detail.queue ?? []).forEach((q, i) => {
    turns.push({ key: `queue:${q.request_id}`, number: ran + i + 1, text: firstLine(q.text), word: 'Queued', queued: true });
  });
  return turns;
}
