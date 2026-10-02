import type { Item, Subagent, SubagentStatus } from '../api';

// Subagents in the conversation (DESIGN.md Subagents): which reply spawned which, their identity
// tones, the live card's set and rows, the status groups of a long list and the header index.

/** Identity tones in spawn order: badge tones that never stand for a state (no red, green or blue). */
export const IDENTITY_TONES = ['violet', 'pink', 'cyan', 'amber', 'teal'] as const;
export type IdentityTone = (typeof IDENTITY_TONES)[number];

/** A reply with more subagents than this draws them neutral: past five, colours stop telling them apart. */
export const IDENTITY_LIMIT = 5;
/** The live card's rows before "Show all". */
export const LIVE_ROWS = 5;
/** A list with more subagents than this groups them by status… */
export const GROUP_OVER = 8;
/** …and offers a filter with more than this. */
export const FILTER_OVER = 12;
/** Rows a group (or the live card's whole set) renders at a time; "Show 50 more" adds the next. */
export const PAGE_ROWS = 50;

export const isDone = (s: Subagent) => s.status === 'completed' || s.status === 'idle';

export interface SubagentCounts {
  total: number;
  running: number;
  /** Completed or idle. */
  done: number;
  failed: number;
  /** Cancelled. */
  stopped: number;
}

export function countSubagents(list: readonly Subagent[]): SubagentCounts {
  const c: SubagentCounts = { total: list.length, running: 0, done: 0, failed: 0, stopped: 0 };
  for (const s of list) {
    if (s.status === 'running') c.running++;
    else if (s.status === 'failed') c.failed++;
    else if (s.status === 'cancelled') c.stopped++;
    else c.done++;
  }
  return c;
}

export interface CountPart {
  text: string;
  tone: 'muted' | 'success' | 'error' | 'accent';
}

/** "2 done · 1 failed · 3 running · 1 stopped", only the parts that are not zero. */
export function countParts(c: SubagentCounts): CountPart[] {
  const parts: (CountPart | false)[] = [
    c.done > 0 && { text: `${c.done} done`, tone: 'success' },
    c.failed > 0 && { text: `${c.failed} failed`, tone: 'error' },
    c.running > 0 && { text: `${c.running} running`, tone: 'accent' },
    c.stopped > 0 && { text: `${c.stopped} stopped`, tone: 'muted' },
  ];
  return parts.filter(Boolean) as CountPart[];
}

export const subagentNoun = (n: number) => `${n} ${n === 1 ? 'subagent' : 'subagents'}`;

/** The subagents one of `items` spawned (a reused one keeps its first `task` call), in spawn order. */
export function spawnedBy(items: readonly Item[], subagents: readonly Subagent[]): Subagent[] {
  if (!subagents.length) return [];
  const byParent = new Map<string, Subagent>();
  for (const s of subagents) if (s.parent_tool_call_id && !byParent.has(s.parent_tool_call_id)) byParent.set(s.parent_tool_call_id, s);
  const out: Subagent[] = [];
  for (const item of items) {
    const s = item.kind === 'tool' ? byParent.get(item.id) : undefined;
    if (s) out.push(s);
  }
  return out;
}

/** The replies in `items`: each runs from a user message (steers stay in their reply) to the next. */
export function replies(items: readonly Item[]): Item[][] {
  const out: Item[][] = [];
  let reply: Item[] = [];
  for (const item of items) {
    if (item.kind === 'user' && !item.delivery) {
      if (reply.length) out.push(reply);
      reply = [];
    }
    reply.push(item);
  }
  if (reply.length) out.push(reply);
  return out;
}

/** Each subagent's identity tone: its place among its reply's subagents, while that reply has at most five. */
export function identities(items: readonly Item[], subagents: readonly Subagent[]): Map<string, IdentityTone> {
  const tones = new Map<string, IdentityTone>();
  if (!subagents.length) return tones;
  for (const reply of replies(items)) {
    const spawned = spawnedBy(reply, subagents);
    if (spawned.length > IDENTITY_LIMIT) continue;
    spawned.forEach((s, i) => tones.set(s.id, IDENTITY_TONES[i % IDENTITY_TONES.length]));
  }
  return tones;
}

export interface LiveSubagent {
  subagent: Subagent;
  /** Spawned by an earlier reply and running again now. */
  resumed: boolean;
}

/**
 * The live card's set: the subagents the latest reply (`latest`) spawned and every running one an
 * earlier reply spawned (resumed). Empty once none of them runs, and the card goes.
 */
export function liveSet(latest: readonly Item[], subagents: readonly Subagent[]): LiveSubagent[] {
  const mine = spawnedBy(latest, subagents);
  const own = new Set(mine.map((s) => s.id));
  const set: LiveSubagent[] = [
    ...mine.map((subagent) => ({ subagent, resumed: false })),
    ...subagents.filter((s) => s.status === 'running' && !own.has(s.id)).map((subagent) => ({ subagent, resumed: true })),
  ];
  return set.some((x) => x.subagent.status === 'running') ? set : [];
}

const ORDER: Record<SubagentStatus, number> = { failed: 0, running: 1, idle: 2, completed: 3, cancelled: 4 };

/** Failed first, then running, idle, completed and stopped; spawn order within each. */
export function byStatus<T>(list: readonly T[], status: (x: T) => SubagentStatus): T[] {
  return list.map((x, i) => [x, i] as const).sort((a, b) => ORDER[status(a[0])] - ORDER[status(b[0])] || a[1] - b[1]).map(([x]) => x);
}

export interface LiveRows {
  rows: LiveSubagent[];
  /** What the folded rows hold, for "3 more running · 31 done". */
  rest: string;
}

/**
 * The live card's rows: its failed and running subagents, failed first, at most `LIVE_ROWS`;
 * with `all`, every one of the set (failed, running, idle, completed, stopped). `rest` names the
 * ones left out, empty when none is.
 */
export function liveRows(set: readonly LiveSubagent[], all: boolean): LiveRows {
  const sorted = byStatus(set, (x) => x.subagent.status);
  const rows = all ? sorted : sorted.filter((x) => x.subagent.status === 'failed' || x.subagent.status === 'running').slice(0, LIVE_ROWS);
  if (all) return { rows, rest: '' };
  const shown = new Set(rows);
  const c = countSubagents(sorted.filter((x) => !shown.has(x)).map((x) => x.subagent));
  const rest = [c.running && `${c.running} more running`, c.failed && `${c.failed} more failed`, c.done && `${c.done} done`, c.stopped && `${c.stopped} stopped`].filter(Boolean).join(' · ');
  return { rows, rest };
}

export interface StatusGroup {
  key: 'failed' | 'running' | 'done' | 'stopped';
  label: string;
  /** Open until the reader folds it. */
  open: boolean;
  subagents: Subagent[];
}

const GROUPS: { key: StatusGroup['key']; label: string; open: boolean; has: (s: Subagent) => boolean }[] = [
  { key: 'failed', label: 'Failed', open: true, has: (s) => s.status === 'failed' },
  { key: 'running', label: 'Running', open: true, has: (s) => s.status === 'running' },
  { key: 'done', label: 'Done', open: false, has: isDone },
  { key: 'stopped', label: 'Stopped', open: false, has: (s) => s.status === 'cancelled' },
];

/** A long list by status: Failed and Running open, Done (completed and idle) and Stopped folded; empty groups left out. */
export function statusGroups(list: readonly Subagent[]): StatusGroup[] {
  return GROUPS.map(({ has, ...g }) => ({ ...g, subagents: list.filter(has) })).filter((g) => g.subagents.length > 0);
}

/** Whether a subagent's name or one-line summary holds `query`, case-insensitively; an empty query holds everything. */
export function matches(s: Subagent, summary: string, query: string): boolean {
  const q = query.trim().toLowerCase();
  return !q || s.name.toLowerCase().includes(q) || summary.toLowerCase().includes(q);
}

export type IndexFilter = 'all' | 'running' | 'failed' | 'done';

export function inFilter(s: Subagent, filter: IndexFilter): boolean {
  if (filter === 'all') return true;
  if (filter === 'done') return isDone(s);
  return s.status === filter;
}

export interface IndexGroup {
  /** The user message's item id; empty for subagents whose spawning reply is not known here. */
  key: string;
  /** The message's first line, when its text is loaded. */
  text: string;
  /** When the message was sent, when known. */
  time?: string;
  subagents: Subagent[];
}

/**
 * The header index: subagents grouped by the user message that started the reply that spawned
 * them, newest message first, in spawn order within each. `index` places the calls (the
 * identity index, which outlives history pages); the message text comes from `items` when that
 * message is loaded. Subagents whose call is not placed (older ones paged in from the record)
 * form the last group, keyed "".
 */
export function indexGroups(index: readonly Item[], items: readonly Item[], subagents: readonly Subagent[]): IndexGroup[] {
  const replyOf = new Map<string, Item | null>();
  const position = new Map<string, number>();
  let user: Item | null = null;
  index.forEach((item, i) => {
    if (item.kind === 'user' && !item.delivery) user = item;
    replyOf.set(item.id, user);
    position.set(item.id, i);
  });
  const texts = new Map(items.filter((i) => i.kind === 'user').map((i) => [i.id, i.text ?? '']));
  const groups = new Map<string, IndexGroup & { at: number }>();
  const placed = [...subagents].sort((a, b) => (position.get(a.parent_tool_call_id ?? '') ?? Infinity) - (position.get(b.parent_tool_call_id ?? '') ?? Infinity));
  for (const s of placed) {
    const parent = s.parent_tool_call_id ?? '';
    const known = replyOf.has(parent);
    const message = known ? replyOf.get(parent) : null;
    const key = known ? message?.id ?? 'start' : '';
    let group = groups.get(key);
    if (!group) {
      const text = message ? firstLineOf(texts.get(message.id) ?? '') : '';
      group = { key, text, time: message?.time, subagents: [], at: message ? position.get(message.id)! : known ? -1 : -Infinity };
      groups.set(key, group);
    }
    group.subagents.push(s);
  }
  return [...groups.values()].sort((a, b) => b.at - a.at).map(({ at: _at, ...g }) => g);
}

const firstLineOf = (text: string) => text.split('\n').map((l) => l.trim()).find(Boolean) ?? '';
