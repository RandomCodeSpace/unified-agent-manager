// Rules for the turn's rows (ADR 0004, "Tool calls, thinking and approvals in the turn"):
// what a tool row shows as its main argument, how a long run folds, which decided request
// sits on which tool row, and what a question asked and got. No DOM, so the unit tests
// run in node.

import type { Interaction, InteractionState, Item, ToolCall } from '../api';

/** Argument keys per tool name, most telling first; `GENERIC` serves every other tool. */
const KEYS: Record<string, string[]> = {
  bash: ['command', 'cmd'],
  shell: ['command', 'cmd'],
  powershell: ['command', 'cmd'],
  sql: ['query', 'sql'],
  web_fetch: ['url'],
  webfetch: ['url'],
  fetch: ['url'],
  view: ['path', 'file_path', 'filePath'],
  read: ['path', 'file_path', 'filePath'],
  create: ['path', 'file_path', 'filePath'],
  edit: ['path', 'file_path', 'filePath'],
  write: ['path', 'file_path', 'filePath'],
  list: ['path'],
  grep: ['pattern', 'query'],
  glob: ['pattern'],
  task: ['description', 'name', 'agent_type'],
  skill: ['skill', 'name'],
  store_memory: ['fact'],
  ask_user: ['question'],
};
const GENERIC = ['command', 'cmd', 'url', 'path', 'file_path', 'filePath', 'pattern', 'query', 'skill', 'name', 'description', 'question', 'prompt', 'fact', 'intent'];

/** Most characters a row's argument keeps; CSS ellipsises what still does not fit. */
const MAX_ARG = 300;

const oneLine = (s: string) => s.replace(/\s+/g, ' ').trim().slice(0, MAX_ARG);

function parseObject(input: string | undefined): Record<string, unknown> | null {
  if (!input || input[0] !== '{') return null;
  try {
    const v: unknown = JSON.parse(input);
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

/**
 * The tool row's name and main argument. The argument is the shell command, URL, path,
 * pattern, query, skill or subagent name from the input JSON, per tool; failing a known
 * key, the input's first string value; a non-JSON input as is; else the provider's title.
 * Without a name the title's first word stands in, as the ledger showed it.
 */
export function toolLabel(tool: ToolCall | undefined): { name: string; arg: string } {
  const name = tool?.name?.trim() ?? '';
  const title = tool?.title?.trim() ?? '';
  const arg = mainArgument(name, tool?.input);
  if (name) return { name, arg: arg || (title && title !== name ? oneLine(title) : '') };
  if (title) {
    const space = title.indexOf(' ');
    return space > 0 ? { name: title.slice(0, space), arg: arg || oneLine(title.slice(space + 1)) } : { name: title, arg };
  }
  return { name: 'Tool', arg };
}

/** The main argument from a tool's input, or '' when the input says nothing. */
export function mainArgument(name: string, input: string | undefined): string {
  if (!input?.trim()) return '';
  const obj = parseObject(input);
  if (!obj) return oneLine(input);
  for (const key of [...(KEYS[name.toLowerCase()] ?? []), ...GENERIC]) {
    const v = obj[key];
    if (typeof v === 'string' && v.trim()) return oneLine(v);
  }
  for (const v of Object.values(obj)) if (typeof v === 'string' && v.trim()) return oneLine(v);
  return oneLine(input);
}

/** Runs longer than this fold their middle. */
export const FOLD_AFTER = 8;
const FOLD_HEAD = 2;
const FOLD_TAIL = 3;

/** How a run of `count` rows shows: all of them, or the first `head`, "Show `hidden` more", the last `tail`. */
export function foldWindow(count: number): { head: number; hidden: number; tail: number } {
  if (count <= FOLD_AFTER) return { head: count, hidden: 0, tail: 0 };
  return { head: FOLD_HEAD, hidden: count - FOLD_HEAD - FOLD_TAIL, tail: FOLD_TAIL };
}

export const STATE_TEXT: Record<InteractionState, string> = {
  pending: 'Pending',
  answered: 'Answered',
  rejected: 'Declined',
  expired: 'Expired',
};

export const YOLO_RESOLUTION = 'allowed (yolo)';

/** The word on a tool row's approval mark and the full resolution behind it. */
export function approvalMark(ix: Interaction): { word: string; full: string; tone: 'ok' | 'denied' | 'gone' } {
  const full = `${ix.title} · ${STATE_TEXT[ix.state]}${ix.resolution ? ` · ${ix.resolution}` : ''}`;
  if (ix.kind === 'question') return { word: STATE_TEXT[ix.state].toLowerCase(), full, tone: ix.state === 'answered' ? 'ok' : ix.state === 'rejected' ? 'denied' : 'gone' };
  if (ix.resolution === YOLO_RESOLUTION) return { word: 'auto', full, tone: 'ok' };
  switch (ix.state) {
    case 'answered':
      return { word: 'allowed', full, tone: 'ok' };
    case 'rejected':
      return { word: 'denied', full, tone: 'denied' };
    case 'expired':
      return { word: 'expired', full, tone: 'gone' };
    default:
      return { word: 'pending', full, tone: 'gone' };
  }
}

/** Copilot's question tool; its input and output stand in for the interaction when history has no interactions. */
const ASK_TOOL = 'ask_user';

export interface Links {
  /** Requests by the tool item they sit on, oldest first; every request is kept. */
  linked: Map<string, Interaction[]>;
  /** Decided permissions that name no tool row; they join the rows by time. */
  loose: Interaction[];
  /** Questions that name no tool row, pending or decided; each is its own block by time. */
  questions: Interaction[];
}

/**
 * One agent's requests (`agentId` empty for the main agent), placed. A request with a
 * `tool_call_id` that names one of the agent's tool items sits on that row; a question
 * without one sits on the oldest unclaimed `ask_user` call with its text, in time order.
 * Pending permissions are placed nowhere: they stay action cards. When two requests name
 * one call, both are kept, oldest first.
 */
export function linkInteractions(items: Item[], interactions: Interaction[], agentId = ''): Links {
  const linked = new Map<string, Interaction[]>();
  const loose: Interaction[] = [];
  const questions: Interaction[] = [];
  const tools = new Set<string>();
  const asked = new Map<string, string[]>();
  for (const it of items) {
    if (it.kind !== 'tool' || (it.agent_id ?? '') !== agentId) continue;
    tools.add(it.id);
    const text = it.tool?.name === ASK_TOOL ? askedText(it.tool) : '';
    if (text) asked.set(text, [...(asked.get(text) ?? []), it.id]);
  }
  const mine = interactions.filter((ix) => (ix.agent_id ?? '') === agentId).sort((a, b) => a.time.localeCompare(b.time));
  const claimed = new Set(mine.map((ix) => ix.tool_call_id).filter((id): id is string => !!id && tools.has(id)));
  const place = (id: string, ix: Interaction) => linked.set(id, [...(linked.get(id) ?? []), ix]);
  for (const ix of mine) {
    if (ix.tool_call_id && tools.has(ix.tool_call_id)) place(ix.tool_call_id, ix);
  }
  // Questions by text, oldest first, onto the oldest ask_user call not already named by an ID.
  const byTime = mine.filter((ix) => ix.kind === 'question' && !(ix.tool_call_id && tools.has(ix.tool_call_id)));
  for (const ix of byTime) {
    const queue = asked.get(ix.questions?.[0]?.text ?? '') ?? [];
    const id = queue.find((candidate) => !claimed.has(candidate));
    if (id) {
      claimed.add(id);
      place(id, ix);
    } else questions.push(ix);
  }
  for (const ix of mine) {
    if (ix.kind === 'question' || ix.state === 'pending' || (ix.tool_call_id && tools.has(ix.tool_call_id))) continue;
    loose.push(ix);
  }
  return { linked, loose, questions };
}

/** The question text an `ask_user` call asked, from its input. */
function askedText(tool: ToolCall | undefined): string {
  const obj = parseObject(tool?.input) ?? {};
  return typeof obj.question === 'string' ? obj.question.trim() : '';
}

export type Entry = { item: Item; interaction?: undefined } | { interaction: Interaction; item?: undefined };

/** Items in their order, with each request placed before the first item that started after it. */
export function mergeByTime(items: Item[], interactions: Interaction[]): Entry[] {
  const queue = [...interactions].sort((a, b) => a.time.localeCompare(b.time));
  const out: Entry[] = [];
  let k = 0;
  for (const item of items) {
    while (k < queue.length && queue[k].time < item.time) out.push({ interaction: queue[k++] });
    out.push({ item });
  }
  while (k < queue.length) out.push({ interaction: queue[k++] });
  return out;
}

export interface AskedQuestion {
  questions: { text: string; header?: string; choices: string[] }[];
  /** The chosen choice or the typed text, once answered; several answers joined. */
  answer?: string;
  /** Choices the answer named, so they can be marked. */
  chosen: string[];
  /** `none`: the call ended without an answer (a restart, a stopped turn). */
  outcome: 'pending' | 'answered' | 'declined' | 'failed' | 'none';
  /** What the tool reported when it failed. */
  error?: string;
}

/** The answer in Copilot's `ask_user` output: "User selected: <choice>", "User responded: <text>". */
const ANSWER = /^User\s+(?:selected|responded|answered)\s*:\s*/i;
/** What Copilot's CLI records for a question the user declined (CLI 1.0.88): a completed call with this output. */
export const DECLINED_OUTPUT = 'The user was unable to respond due to an error';
/** What UAM's adapter tells the CLI when the user declines; a failed call may carry it. */
const DECLINED_ERROR = 'the user declined to answer';
/** OpenCode's answered resolution, "Answered: a, b; c". */
const RESOLVED = /^Answered:\s*/;

/**
 * What a question asked and what it got. The interaction (any provider) gives the
 * questions and choices, its state the outcome, and its resolution the answer; the tool
 * item (Copilot's `ask_user`) gives the same from its input and output, so a question
 * reads the same after a reload or a restart, when history carries no interactions. A
 * call still open when the turn is not live got no answer. Null when neither is a question.
 */
export function questionOf(tool: ToolCall | undefined, interaction: Interaction | undefined, live: boolean): AskedQuestion | null {
  const isAsk = tool?.name === ASK_TOOL;
  if (!isAsk && interaction?.kind !== 'question') return null;
  const q: AskedQuestion = { questions: [], chosen: [], outcome: 'pending' };
  if (interaction?.questions?.length) {
    q.questions = interaction.questions.map((x) => ({ text: x.text, header: x.header || undefined, choices: x.choices ?? [] }));
  } else if (isAsk) {
    const obj = parseObject(tool.input) ?? {};
    const choices = Array.isArray(obj.choices) ? obj.choices.filter((c): c is string => typeof c === 'string' && !!c.trim()) : [];
    q.questions = [{ text: askedText(tool), choices }];
  }
  const out = tool?.output?.trim() ?? '';
  const state = interaction?.state;
  // The tool's own result first: it is the provider's record of what the model got.
  if (isAsk && tool.status === 'failed') {
    q.outcome = out === DECLINED_ERROR || state === 'rejected' ? 'declined' : 'failed';
    if (q.outcome === 'failed' && out) q.error = out;
    return q;
  }
  if (isAsk && tool.status === 'completed') {
    const answer = out.replace(ANSWER, '').trim();
    if (state === 'rejected' || answer === DECLINED_OUTPUT) {
      q.outcome = 'declined';
      return q;
    }
    return answered(q, answer);
  }
  switch (state) {
    case 'answered':
      return answered(q, (interaction!.resolution ?? '').replace(RESOLVED, '').trim());
    case 'rejected':
      q.outcome = 'declined';
      return q;
    case 'expired':
      q.outcome = 'none';
      return q;
    case 'pending':
      return q;
  }
  // No interaction and the call is still open: waiting only while the turn runs.
  q.outcome = live ? 'pending' : 'none';
  return q;
}

function answered(q: AskedQuestion, answer: string): AskedQuestion {
  q.outcome = 'answered';
  q.answer = answer;
  const parts = answer.split(/;\s*|,\s*/).map((s) => s.trim());
  const all = q.questions.flatMap((x) => x.choices);
  q.chosen = all.filter((c) => c === answer || parts.includes(c));
  return q;
}

/** Count only successful, recognizable operations; failed or unresolved calls stay explicit. */
export function summarizeTools(items: Item[], live: boolean): string {
  let commands = 0, other = 0, failed = 0, active = 0, noResult = 0;
  const changed = new Set<string>();
  const read = new Set<string>();
  for (const item of items) {
    const t = item.tool;
    if (t?.status === 'failed') { failed++; continue; }
    if (t?.status !== 'completed') { if (live) active++; else noResult++; continue; }
    const name = t.name.toLowerCase();
    if (['bash', 'shell', 'powershell'].includes(name)) { commands++; continue; }
    const input = parseObject(t.input);
    const path = input && [input.path, input.file_path, input.filePath].find((v): v is string => typeof v === 'string' && !!v.trim());
    if (path && ['edit', 'write', 'create'].includes(name)) changed.add(path);
    else if (path && ['read', 'view'].includes(name)) read.add(path);
    else other++;
  }
  const noun = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;
  const done = [changed.size && `changed ${noun(changed.size, 'file')}`, commands && `ran ${noun(commands, 'command')}`, read.size && `read ${noun(read.size, 'file')}`, other && `used ${noun(other, 'tool')}`].filter(Boolean) as string[];
  const summary = done.length > 1 ? `${done.slice(0, -1).join(', ')} and ${done.at(-1)}` : done[0] ?? '';
  return [summary && summary[0].toUpperCase() + summary.slice(1), active && `${active} running`, failed && `${failed} failed`, noResult && `${noResult} without a result`].filter(Boolean).join(' · ');
}

/** The current turn includes every segment across steer messages. */
export function foregroundItems(items: Item[]): Item[] {
  for (let i = items.length - 1; i >= 0; i--) {
    if (items[i].kind === 'user' && items[i].delivery !== 'steer') return items.slice(i);
  }
  return items;
}

/** A steer belongs to its existing turn and must not restart elapsed time. */
export function foregroundStart(items: Item[]): string | undefined {
  return foregroundItems(items).find((item) => item.kind === 'user' && item.delivery !== 'steer')?.time;
}

/** Never use session.updated_at as turn time: it also changes for unrelated updates. */
export function elapsedSince(start: string | undefined, now: number): string | null {
  if (!start) return null;
  const ms = now - Date.parse(start);
  if (!Number.isFinite(ms) || ms < 0) return null;
  if (ms < 1000) return '<1s';
  const seconds = Math.floor(ms / 1000);
  return seconds < 60 ? `${seconds}s` : seconds < 3600 ? `${Math.floor(seconds / 60)}m` : `${Math.floor(seconds / 3600)}h ${Math.floor(seconds % 3600 / 60)}m`;
}
