import type { Item, Subagent, SubagentStatus } from '../api';
import { duration, toolKind } from './transcript.ts';

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

const parents = new WeakMap<readonly Subagent[], ReadonlyMap<string, Subagent>>();

/** Each subagent by the `task` call that spawned it, built once per list; with two for one call, the later record wins. */
export function parentMap(subagents: readonly Subagent[]): ReadonlyMap<string, Subagent> {
  let map = parents.get(subagents);
  if (!map) {
    const built = new Map<string, Subagent>();
    for (const s of subagents) if (s.parent_tool_call_id) built.set(s.parent_tool_call_id, s);
    parents.set(subagents, built);
    map = built;
  }
  return map;
}

const agents = new WeakMap<readonly Subagent[], ReadonlyMap<string, Subagent>>();

/** Each subagent by its id, built once per list. */
function agentMap(subagents: readonly Subagent[]): ReadonlyMap<string, Subagent> {
  let map = agents.get(subagents);
  if (!map) {
    map = new Map(subagents.map((s) => [s.id, s]));
    agents.set(subagents, map);
  }
  return map;
}

/**
 * The `task` call in the main transcript a subagent comes from: its own, or, for one another
 * subagent spawned (that call is in the spawner's transcript), its top-level ancestor's.
 * Undefined when an ancestor is not loaded here.
 */
export function mainCall(s: Subagent, subagents: readonly Subagent[]): string | undefined {
  const byId = agentMap(subagents);
  let at: Subagent | undefined = s;
  for (let hops = 0; at?.parent_agent_id && hops < 32; hops++) at = byId.get(at.parent_agent_id);
  return at && !at.parent_agent_id ? at.parent_tool_call_id : undefined;
}

/** The subagents one of `items` spawned (a reused one keeps its first `task` call), in spawn order. */
export function spawnedBy(items: readonly Item[], subagents: readonly Subagent[]): Subagent[] {
  const byParent = parentMap(subagents);
  return items.flatMap((item) => {
    const s = item.kind === 'tool' ? byParent.get(item.id) : undefined;
    return s ? [s] : [];
  });
}

/** One reply: from a user message (steers stay in it) to the next. */
export interface Reply {
  /** The user message's id; "start" before the first one. */
  key: string;
  /** When that message was sent. */
  time?: string;
  /** Its `task` calls, in order, whether or not their subagents are loaded. */
  calls: string[];
  /** The subagents those calls spawned that are loaded here, in spawn order. */
  subagents: Subagent[];
}

export interface Replies {
  /** Every reply of the identity index, in order. */
  list: Reply[];
  byKey: ReadonlyMap<string, Reply>;
  /** The reply that holds each `task` call. */
  ofCall: ReadonlyMap<string, Reply>;
  /** Each subagent's identity tone: its call's place in its reply, while that reply has at most five calls. */
  tones: ReadonlyMap<string, IdentityTone>;
}

/**
 * The replies of the identity index (`history_index`, which outlives history pages; the items when
 * there is none), with the `task` calls and loaded subagents of each. Membership comes from here,
 * never from the window on screen or the live tail, so a long reply is counted whole.
 */
export function replyIndex(index: readonly Item[], subagents: readonly Subagent[]): Replies {
  const byParent = parentMap(subagents);
  const list: Reply[] = [];
  const byKey = new Map<string, Reply>();
  const ofCall = new Map<string, Reply>();
  const tones = new Map<string, IdentityTone>();
  let reply: Reply | undefined;
  const open = (key: string, time?: string) => {
    reply = { key, time, calls: [], subagents: [] };
    list.push(reply);
    byKey.set(key, reply);
  };
  for (const item of index) {
    if (item.kind === 'user' && !item.delivery) {
      open(item.id, item.time);
      continue;
    }
    const s = item.kind === 'tool' ? byParent.get(item.id) : undefined;
    if (!s && !(item.kind === 'tool' && toolKind(item.tool?.name ?? '') === 'subagent')) continue;
    if (!reply) open('start');
    reply!.calls.push(item.id);
    ofCall.set(item.id, reply!);
    if (s) reply!.subagents.push(s);
  }
  // A subagent another one spawned joins its top-level ancestor's reply, its call with it.
  for (const s of subagents) {
    const reply = s.parent_agent_id && s.parent_tool_call_id ? ofCall.get(mainCall(s, subagents) ?? '') : undefined;
    if (!reply) continue;
    reply.calls.push(s.parent_tool_call_id!);
    ofCall.set(s.parent_tool_call_id!, reply);
    reply.subagents.push(s);
  }
  for (const r of list) {
    if (r.calls.length > IDENTITY_LIMIT) continue;
    for (const s of r.subagents) tones.set(s.id, IDENTITY_TONES[r.calls.indexOf(s.parent_tool_call_id!) % IDENTITY_TONES.length]);
  }
  return { list, byKey, ofCall, tones };
}

export interface LiveSubagent {
  subagent: Subagent;
  /** Spawned by an earlier reply: still running from it, or running again. */
  earlier: boolean;
}

/**
 * The live card's set: the subagents the latest reply spawned and every running one an earlier
 * reply spawned. Empty once none of them runs and no row of the card is open (`keep`, which
 * stays in the set while it is open even after it has stopped).
 */
export function liveSet(latest: Reply | undefined, subagents: readonly Subagent[], keep?: string): LiveSubagent[] {
  const mine = latest?.subagents ?? [];
  const own = new Set(mine.map((s) => s.id));
  const set: LiveSubagent[] = [
    ...mine.map((subagent) => ({ subagent, earlier: false })),
    ...subagents.filter((s) => !own.has(s.id) && (s.status === 'running' || s.id === keep)).map((subagent) => ({ subagent, earlier: true })),
  ];
  return set.some((x) => x.subagent.status === 'running' || x.subagent.id === keep) ? set : [];
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
 * The live card's rows: its failed and running subagents, failed first, at most `LIVE_ROWS`, and
 * the open one (`keep`) whatever its state, so an open row never leaves under the reader; with
 * `all`, every one of the set (failed, running, idle, completed, stopped). `keep.at`, the place
 * the open row had when it was opened, holds it there whatever its state becomes, so it is never
 * moved (a move would reset its scroll and focus). `rest` names the ones left out, empty when none is.
 */
export function liveRows(set: readonly LiveSubagent[], all: boolean, keep?: { id: string; at?: number }): LiveRows {
  const sorted = byStatus(set, (x) => x.subagent.status);
  const kept = sorted.find((x) => x.subagent.id === keep?.id);
  const place = (rows: LiveSubagent[]) => {
    if (!kept || keep?.at === undefined) return rows;
    const others = rows.filter((x) => x !== kept);
    return [...others.slice(0, keep.at), kept, ...others.slice(keep.at)];
  };
  if (all) return { rows: place(sorted), rest: '' };
  const picked = new Set(sorted.filter((x) => x.subagent.status === 'failed' || x.subagent.status === 'running').slice(0, LIVE_ROWS));
  if (kept) picked.add(kept);
  const rows = place(sorted.filter((x) => picked.has(x)));
  const c = countSubagents(sorted.filter((x) => !picked.has(x)).map((x) => x.subagent));
  const rest = [c.running && `${c.running} more running`, c.failed && `${c.failed} more failed`, c.done && `${c.done} done`, c.stopped && `${c.stopped} stopped`].filter(Boolean).join(' · ');
  return { rows, rest };
}

/** An ISO time as milliseconds, whatever its zone or fraction; NaN when absent or unreadable. */
export const ms = (iso?: string) => (iso ? Date.parse(iso) : NaN);

/** The service keeps a subagent's first run and its newest 49: at this many, runs in between may be gone. */
export const RUNS_KEPT = 50;

/** "3 runs", "50+ runs" once the record may have dropped some; empty for one run or none. */
export function runCount(s: Subagent): string {
  const n = s.runs?.length ?? 0;
  if (n < 2) return '';
  return n >= RUNS_KEPT ? `${RUNS_KEPT}+ runs` : `${n} runs`;
}

export type Run = NonNullable<Subagent['runs']>[number];
const TRIGGER: Record<Run['trigger'], string> = { spawn: 'started by the agent', user: 'your follow-up', agent: 'resumed by the agent' };

/**
 * What the live card says of a subagent an earlier reply spawned. Only its runs can tell a
 * subagent started again from one still running from that reply: with more than one, the
 * latest run's trigger (`agent`, `user`) and when it first ran; else just that it is from earlier.
 */
export function earlierTag(s: Subagent, parentTime?: string): { text: 'resumed by the agent' | 'your follow-up' | 'from an earlier reply'; first?: string } {
  const runs = s.runs ?? [];
  const last = runs.at(-1);
  if (runs.length > 1 && last && last.trigger !== 'spawn') return { text: last.trigger === 'agent' ? 'resumed by the agent' : 'your follow-up', first: runs[0].started_at || parentTime || undefined };
  return { text: 'from an earlier reply' };
}

/**
 * A run of `s` that started after its reply (from the next user message on): the latest such run's
 * start and the reply it started in (the last user message before it). Null when it never ran
 * again after its reply, or when its reply is the latest.
 */
export function ranAgain(s: Subagent, replies: Replies): { at: string; reply: string } | null {
  const own = replies.ofCall.get(s.parent_tool_call_id ?? '');
  if (!own || !s.runs?.length) return null;
  const next = ms(replies.list[replies.list.indexOf(own) + 1]?.time);
  if (Number.isNaN(next)) return null;
  // Times compared as instants: a run's and a message's may differ in zone or fraction.
  const later = s.runs.filter((r) => ms(r.started_at) >= next).at(-1);
  if (!later?.started_at) return null;
  const at = ms(later.started_at);
  const reply = replies.list.filter((r) => ms(r.time) <= at).at(-1);
  return reply ? { at: later.started_at, reply: reply.key } : null;
}

export interface RunLine {
  /** Its place among the runs; null after the gap where the record dropped runs. */
  n: number | null;
  /** The record dropped runs between the first and this one. */
  gapBefore: boolean;
  started_at?: string;
  /** "started by the agent", "your follow-up", "resumed by the agent". */
  trigger: string;
  /** "✓ 2m 40s", "✗ failed · 1m 0s", "stopped", "running". */
  outcome: string;
  tone: 'muted' | 'success' | 'error' | 'accent';
  running: boolean;
}

/**
 * A subagent's runs as lines for its expanded row; empty with fewer than two. At `RUNS_KEPT` the
 * record holds the first run and the newest ones: the lines after the first carry no number and
 * the second says runs before it are gone.
 */
export function runLines(s: Subagent): RunLine[] {
  const runs = s.runs ?? [];
  if (runs.length < 2) return [];
  const gap = runs.length >= RUNS_KEPT;
  return runs.map((r, i) => {
    const took = r.started_at && r.ended_at ? duration(r.started_at, r.ended_at) : null;
    let outcome = 'running', tone: RunLine['tone'] = 'accent';
    if (r.status === 'failed') [outcome, tone] = [took ? `✗ failed · ${took}` : '✗ failed', 'error'];
    else if (r.status === 'cancelled') [outcome, tone] = ['stopped', 'muted'];
    else if (r.status !== 'running') [outcome, tone] = [took ? `✓ ${took}` : '✓', 'success'];
    return { n: gap && i > 0 ? null : i + 1, gapBefore: gap && i === 1, started_at: r.started_at || undefined, trigger: TRIGGER[r.trigger] ?? r.trigger, outcome, tone, running: r.status === 'running' };
  });
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

/**
 * A long list by status: Failed and Running open, Done (completed and idle) and Stopped folded;
 * empty groups left out. `pin` holds the open row in the group it was opened in, whatever its state
 * becomes, so it is never unmounted under the reader.
 */
export function statusGroups(list: readonly Subagent[], pin?: { id: string; key: StatusGroup['key'] }): StatusGroup[] {
  const groupOf = (s: Subagent) => (s.id === pin?.id ? pin.key : GROUPS.find((g) => g.has(s))?.key);
  return GROUPS.map(({ has: _has, ...g }) => ({ ...g, subagents: list.filter((s) => groupOf(s) === g.key) })).filter((g) => g.subagents.length > 0);
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
 * message is loaded. A subagent another one spawned goes with its top-level ancestor. Subagents
 * whose call is not placed (older ones paged in from the record) form the last group, keyed "".
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
  // One another subagent spawned is placed by its top-level ancestor's call.
  const call = new Map(subagents.map((s) => [s.id, mainCall(s, subagents) ?? '']));
  const placed = [...subagents].sort((a, b) => (position.get(call.get(a.id)!) ?? Infinity) - (position.get(call.get(b.id)!) ?? Infinity));
  for (const s of placed) {
    const parent = call.get(s.id)!;
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
