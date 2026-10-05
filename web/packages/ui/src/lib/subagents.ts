import type { Item, OutlineItem, Subagent, SubagentStatus } from '../api';
import { duration, toolKind } from './transcript.ts';

// Subagents in the conversation (DESIGN.md Subagents): which reply spawned which, their identity
// tones, the live card's set and rows, the status groups of a long list and the header index.

/** Identity tones in spawn order: badge tones that never stand for a state (no red, green or blue). */
export const IDENTITY_TONES = ['violet', 'pink', 'cyan', 'amber', 'teal'] as const;
export type IdentityTone = (typeof IDENTITY_TONES)[number];

/** A reply with more subagents than this draws them neutral: past five, colours stop telling them apart. */
export const IDENTITY_LIMIT = 5;
/** In Detailed, a reply with more subagents than this is one list instead of a row per call… */
export const GROUP_OVER = 8;
/** …and offers a filter with more than this. */
export const FILTER_OVER = 12;
/** Families a list (or the live set) renders at a time; "Show 50 more" adds the next. */
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
  const parts: CountPart[] = [];
  if (c.done > 0) parts.push({ text: `${c.done} done`, tone: 'success' });
  if (c.failed > 0) parts.push({ text: `${c.failed} failed`, tone: 'error' });
  if (c.running > 0) parts.push({ text: `${c.running} running`, tone: 'accent' });
  if (c.stopped > 0) parts.push({ text: `${c.stopped} stopped`, tone: 'muted' });
  return parts;
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

/**
 * The outline the replies are read from: the server's (`outline`, the whole transcript it holds)
 * with what is loaded here and not in it, by time: older pages read from the record before it,
 * newer items after it. Without one, the loaded items alone.
 */
export function mergeOutline(server: readonly OutlineItem[] | undefined, held: readonly OutlineItem[]): readonly OutlineItem[] {
  if (!server?.length) return held;
  const known = new Set(server.map((item) => item.id));
  const extra = held.filter((item) => !known.has(item.id));
  if (!extra.length) return server;
  const first = Date.parse(server[0].time);
  const before = extra.filter((item) => Date.parse(item.time) < first);
  return [...before, ...server, ...extra.filter((item) => !before.includes(item))];
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
export function replyIndex(index: readonly OutlineItem[], subagents: readonly Subagent[]): Replies {
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

export interface Family {
  /** A subagent no other one in the list spawned. */
  head: Subagent;
  /** It, then each one it spawned (and theirs), in spawn order, with how deep each is. */
  rows: { subagent: Subagent; depth: number }[];
}

const RANK: Partial<Record<SubagentStatus, number>> = { failed: 0, running: 1 };

/**
 * A list's rows, each subagent followed by the ones it spawned: the families with a failed
 * member first, then those with a running one, then the rest, each in spawn order. Without
 * `ranked` (the header index) the families stay in spawn order.
 */
export function families(subagents: readonly Subagent[], ranked = true): Family[] {
  const ids = new Set(subagents.map((s) => s.id));
  const children = new Map<string, Subagent[]>();
  for (const s of subagents) {
    if (s.parent_agent_id && ids.has(s.parent_agent_id)) children.set(s.parent_agent_id, [...(children.get(s.parent_agent_id) ?? []), s]);
  }
  const list = subagents.filter((s) => !s.parent_agent_id || !ids.has(s.parent_agent_id)).map((head, at) => {
    const rows: Family['rows'] = [];
    const walk = (subagent: Subagent, depth: number) => {
      if (rows.some((r) => r.subagent === subagent)) return;
      rows.push({ subagent, depth });
      for (const child of children.get(subagent.id) ?? []) walk(child, depth + 1);
    };
    walk(head, 0);
    return { head, rows, at, rank: Math.min(...rows.map((r) => RANK[r.subagent.status] ?? 2)) };
  });
  if (ranked) list.sort((a, b) => a.rank - b.rank || a.at - b.at);
  return list.map(({ head, rows }) => ({ head, rows }));
}

/** The tokens the subagents used, as far as they are known. */
export const totalTokens = (subagents: readonly Subagent[]) => subagents.reduce((n, s) => n + (s.tokens ?? 0), 0);

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
export function indexGroups(index: readonly OutlineItem[], items: readonly Item[], subagents: readonly Subagent[]): IndexGroup[] {
  const replyOf = new Map<string, OutlineItem | null>();
  const position = new Map<string, number>();
  let user: OutlineItem | null = null;
  index.forEach((item, i) => {
    if (item.kind === 'user' && !item.delivery) user = item;
    replyOf.set(item.id, user);
    position.set(item.id, i);
  });
  // A loaded message's own text, else the outline's first line.
  const texts = new Map([...index, ...items].filter((i) => i.kind === 'user').map((i) => [i.id, i.text ?? '']));
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
