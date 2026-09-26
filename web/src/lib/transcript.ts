// Rules for the turn's rows (ADR 0004, "Tool calls, thinking and approvals in the turn"):
// what a tool row shows as its main argument, how a long run folds, which decided request
// sits on which tool row, and what a question asked and got. No DOM, so the unit tests
// run in node.

import type { Interaction, InteractionState, Item, Subagent, ToolCall, TurnTiming } from '../api';

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

/** A tool's input object, parsed once per tool call: streaming re-renders the turn many times a second. */
const parsed = new WeakMap<ToolCall, Record<string, unknown> | null>();
function inputOf(tool: ToolCall): Record<string, unknown> | null {
  let v = parsed.get(tool);
  if (v === undefined) {
    v = parseObject(tool.input);
    parsed.set(tool, v);
  }
  return v;
}

/**
 * The tool row's name and main argument. The argument is the shell command, URL, path,
 * pattern, query, skill or subagent name from the input JSON, per tool; failing a known
 * key, the input's first string value; a non-JSON input as is; else the provider's title.
 * Without a name the title's first word stands in, as the ledger showed it. A title that
 * repeats the name as its verb ("Edit cmd/doctor.go" for `edit`) contributes the rest.
 */
export function toolLabel(tool: ToolCall | undefined): { name: string; arg: string } {
  const name = tool?.name?.trim() ?? '';
  const title = tool?.title?.trim() ?? '';
  const arg = tool?.display_arg ?? mainArgument(name, tool?.input);
  if (name) {
    const rest = title.toLowerCase().startsWith(`${name.toLowerCase()} `) ? title.slice(name.length + 1) : title;
    return { name, arg: arg || (rest && rest !== name ? oneLine(rest) : '') };
  }
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

/** Reveal roughly 100 more items, keeping the first turn whole (including steers). */
export function transcriptWindowStart(items: Item[], before = items.length): number {
  let start = Math.max(0, before - 100);
  while (start > 0 && (items[start].kind !== 'user' || items[start].delivery)) start--;
  return start;
}

/** Hidden rows keep their decisions; they must not reappear as unrelated loose requests. */
export function windowInteractions(items: Item[], interactions: Interaction[], start: number, hasEarlier = false, hasLater = false): Interaction[] {
  if (start === 0 && !hasEarlier && !hasLater) return interactions;
  if (!items[start]) return [];
  const indices = new Map(items.map((it, index) => [it.id, index]));
  return interactions.filter((ix) => {
    if (hasLater && ix.time > items.at(-1)!.time) return false;
    const index = ix.tool_call_id ? indices.get(ix.tool_call_id) : undefined;
    return index === undefined ? (!hasEarlier || !ix.tool_call_id) && ix.time >= items[start].time : index >= start;
  });
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
  // The same requests over the same tool calls give the same result object, so rows memoised
  // on their requests hold while text streams into other items.
  let sig = '';
  for (const it of items) if (it.kind === 'tool' && (it.agent_id ?? '') === agentId) sig += `${it.id}\u0000${it.tool?.name === ASK_TOOL ? askedText(it.tool) : ''}\u0001`;
  const byAgent = linkCache.get(interactions) ?? new Map<string, { sig: string; links: Links }>();
  linkCache.set(interactions, byAgent);
  const hit = byAgent.get(agentId);
  if (hit?.sig === sig) return hit.links;
  const links = placeInteractions(items, interactions, agentId);
  byAgent.set(agentId, { sig, links });
  return links;
}

const linkCache = new WeakMap<Interaction[], Map<string, { sig: string; links: Links }>>();

function placeInteractions(items: Item[], interactions: Interaction[], agentId: string): Links {
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
  const obj = (tool && inputOf(tool)) ?? {};
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
/** Legacy answered resolution, "Answered: a, b; c". */
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
  if (tool?.question_outcome && !tool.input && !interaction) return { ...q, outcome: tool.question_outcome === 'pending' && !live ? 'none' : tool.question_outcome };
  if (interaction?.questions?.length) {
    q.questions = interaction.questions.map((x) => ({ text: x.text, header: x.header || undefined, choices: x.choices ?? [] }));
  } else if (isAsk) {
    const obj = inputOf(tool) ?? {};
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

/** What a tool row asked: Copilot's `ask_user` call, or any call a question request names; null for every other call. */
export function askedOn(item: Item, approvals: Map<string, Interaction[]> | undefined, live: boolean): AskedQuestion | null {
  return questionOf(item.tool, approvals?.get(item.id)?.filter((ix) => ix.kind === 'question').at(-1), live);
}

function answered(q: AskedQuestion, answer: string): AskedQuestion {
  q.outcome = 'answered';
  q.answer = answer;
  const parts = answer.split(/;\s*|,\s*/).map((s) => s.trim());
  const all = q.questions.flatMap((x) => x.choices);
  q.chosen = all.filter((c) => c === answer || parts.includes(c));
  return q;
}

const noun = (n: number, word: string, plural = `${word}s`) => `${n} ${n === 1 ? word : plural}`;
/** "a, b and c", capitalised. */
const sentence = (parts: string[]) => {
  const s = parts.length > 1 ? `${parts.slice(0, -1).join(', ')} and ${parts.at(-1)}` : parts[0] ?? '';
  return s && s[0].toUpperCase() + s.slice(1);
};

/** The tools that write to the tree, by the names providers report; the Changes count follows their completions. */
export const CHANGE_TOOLS: readonly string[] = ['edit', 'write', 'create'];

/** Completed calls of a tool that changes files, so far in the Task; a rise means the Changes count may be stale. */
export function completedChanges(items: readonly Item[]): number {
  let n = 0;
  for (const it of items) if (it.kind === 'tool' && it.tool?.status === 'completed' && CHANGE_TOOLS.includes(it.tool.name.toLowerCase())) n++;
  return n;
}

/** What a run of tool calls did: the successful, recognizable operations as phrases, and the counts that stay explicit. */
function toolCounts(items: Item[], live: boolean): { done: string[]; active: number; failed: number; noResult: number } {
  let commands = 0, other = 0, failed = 0, active = 0, noResult = 0;
  const changed = new Set<string>();
  const read = new Set<string>();
  for (const item of items) {
    const t = item.tool;
    if (t?.status === 'failed') { failed++; continue; }
    if (t?.status !== 'completed') { if (live) active++; else noResult++; continue; }
    const name = t.name.toLowerCase();
    if (['bash', 'shell', 'powershell'].includes(name)) { commands++; continue; }
    const input = inputOf(t);
    const path = t.path || (input && [input.path, input.file_path, input.filePath].find((v): v is string => typeof v === 'string' && !!v.trim()));
    if (path && CHANGE_TOOLS.includes(name)) changed.add(path);
    else if (path && ['read', 'view'].includes(name)) read.add(path);
    else other++;
  }
  const done = [changed.size && `changed ${noun(changed.size, 'file')}`, commands && `ran ${noun(commands, 'command')}`, read.size && `read ${noun(read.size, 'file')}`, other && `used ${noun(other, 'tool')}`].filter(Boolean) as string[];
  return { done, active, failed, noResult };
}

/** Count only successful, recognizable operations; failed or unresolved calls stay explicit. */
export function summarizeTools(items: Item[], live: boolean): string {
  const { done, active, failed, noResult } = toolCounts(items, live);
  return [sentence(done), active && `${active} running`, failed && `${failed} failed`, noResult && `${noResult} without a result`].filter(Boolean).join(' · ');
}

/** "12s", "1m 4s" or "<1s" between two ISO timestamps; null when they are not in order. */
export function duration(from: string, to: string): string | null {
  return formatMs(new Date(to).getTime() - new Date(from).getTime());
}

/** How long a tool call or thought took, from its recorded start and end; null while it runs or when unrecorded. */
export function itemTook(item: Item): string | null {
  return item.ended_at ? duration(item.time, item.ended_at) : null;
}

/** A span in milliseconds as "12s", "1m 4s" or "<1s"; null when it is not a span. */
export function formatMs(ms: number): string | null {
  if (!Number.isFinite(ms) || ms < 0) return null;
  if (ms < 1000) return '<1s';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}

/** The request waits for the user: pending, and not one yolo mode is already answering. */
export const awaitsUser = (ix: Interaction) => ix.state === 'pending' && !ix.auto;

const isActiveTool = (item: Item) => item.tool?.status === 'pending' || item.tool?.status === 'running';

/** Work between two messages: thinking, tool calls, decided requests and questions that no longer wait. Prose, the user's bubbles and notices bound it. */
export function isWork(entry: Entry): boolean {
  if (entry.interaction) return entry.interaction.state !== 'pending';
  return entry.item.kind === 'reasoning' || entry.item.kind === 'tool';
}

export interface Segment {
  /** The first entry's id: it never changes while entries append to the run, so the row keeps its key and its state. */
  key: string;
  entries: Entry[];
  /** A run of work, folded into one activity row; otherwise one entry that stands on its own. */
  work: boolean;
}

/**
 * A turn's entries as segments: every contiguous run of work (as `work` says) is one
 * segment, and every other entry is a segment of its own, in order.
 */
export function segmentActivity(entries: Entry[], work: (entry: Entry) => boolean = isWork): Segment[] {
  const out: Segment[] = [];
  for (const entry of entries) {
    const key = entry.item ? entry.item.id : entry.interaction.id;
    const last = out.at(-1);
    if (work(entry) && last?.work) last.entries.push(entry);
    else out.push({ key, entries: [entry], work: work(entry) });
  }
  return out;
}

export interface ActivitySummary {
  /** "Thought 4×, ran 4 commands and answered 2 questions · 1 failed · 12s"; empty when the run has nothing to show. */
  label: string;
  /** `error` when a call failed, `attention` while a call waits for the user's permission. */
  tone: 'muted' | 'error' | 'attention';
  /** A call is still running or thinking still streams: the label names it and the row carries the working mark. */
  active: boolean;
}

/**
 * The activity row's label for one run of work. Finished thoughts are counted ("Thought",
 * "Thought 4×") with what the tools did and the questions answered or declined, then what
 * stays explicit: failures (a failed question too), calls without a result, questions
 * without an answer, images returned, the call waiting for permission or an answer or
 * still running (the last one, by name and argument), thinking still streaming; and, once
 * the run ended and the next item's time is known, how long it took from the first item.
 */
export function summarizeActivity(entries: Entry[], { live, streamingId, approvals, endedAt }: { live: boolean; streamingId?: string; approvals?: Map<string, Interaction[]>; endedAt?: string }): ActivitySummary {
  const items = entries.flatMap((e) => (e.item ? [e.item] : []));
  const tools = items.filter((it) => it.kind === 'tool');
  const thinking = items.some((it) => it.kind === 'reasoning' && it.id === streamingId);
  const thoughts = items.filter((it) => it.kind === 'reasoning' && it.id !== streamingId && (it.text?.trim() || it.compact?.has_reasoning)).length;
  // A question that no longer waits counts as a question, never as a tool call; one still waiting stays a call.
  const outcomes: AskedQuestion['outcome'][] = [];
  const calls = tools.filter((it) => {
    const q = askedOn(it, approvals, live);
    if (!q || q.outcome === 'pending') return true;
    outcomes.push(q.outcome);
    return false;
  });
  let decided = 0;
  for (const { interaction } of entries) {
    if (interaction?.kind === 'question') outcomes.push(questionOf(undefined, interaction, live)!.outcome);
    else if (interaction) decided++;
  }
  const asked = (outcome: AskedQuestion['outcome']) => outcomes.filter((o) => o === outcome).length;
  const [answeredQs, declinedQs, unanswered] = [asked('answered'), asked('declined'), asked('none')];
  const counts = toolCounts(calls, live);
  const { done, noResult } = counts;
  const failed = counts.failed + asked('failed');
  const running = live ? tools.filter(isActiveTool).at(-1) : undefined;
  const pending = running && approvals?.get(running.id)?.find(awaitsUser);
  const waiting = !!pending;
  const images = tools.reduce((n, it) => n + (it.images?.length ?? 0), 0);
  const active = !!running || thinking;
  const took = !active && endedAt && items[0] ? duration(items[0].time, endedAt) : null;
  const call = running && toolLabel(running.tool);
  const now = running ? `${waiting ? `Waiting for your ${pending.kind === 'question' ? 'answer' : 'approval'}` : 'Running'}: ${call!.arg ? `${call!.name} ${call!.arg}` : call!.name}` : thinking ? 'Thinking…' : '';
  const label = [
    sentence([thoughts && (thoughts === 1 ? 'thought' : `thought ${thoughts}×`), ...done, answeredQs && `answered ${noun(answeredQs, 'question')}`, declinedQs && `declined ${noun(declinedQs, 'question')}`, decided && `decided ${noun(decided, 'request')}`].filter(Boolean) as string[]),
    failed && `${failed} failed`,
    noResult && `${noResult} without a result`,
    unanswered && `${noun(unanswered, 'question')} unanswered`,
    images && noun(images, 'image'),
    now,
    took,
  ].filter(Boolean).join(' · ');
  return { label, tone: failed ? 'error' : waiting ? 'attention' : 'muted', active };
}

/** The current turn includes every segment across steer messages. */
export function foregroundItems(items: Item[]): Item[] {
  for (let i = items.length - 1; i >= 0; i--) {
    if (items[i].kind === 'user' && !items[i].delivery) return items.slice(i);
  }
  return items;
}

/** The live clock uses recorded foreground evidence, never message timestamps. */
export function foregroundStart(timings: TurnTiming[]): string | undefined {
  const current = timings.at(-1);
  return current?.state === 'working' ? current.started_at : undefined;
}

/** Link the recorded interval to the ordinary user message that began its turn. */
export function timingForTurn(timings: TurnTiming[], userItemId: string | undefined): TurnTiming | undefined {
  if (!userItemId) return undefined;
  for (let i = timings.length - 1; i >= 0; i--) if (timings[i].user_item_id === userItemId) return timings[i];
  return undefined;
}

/** A stopped clock requires both observed boundaries and a terminal outcome. */
export function completedDuration(timing: TurnTiming | undefined): string | null {
  if (!timing?.ended_at || !['completed', 'cancelled', 'failed'].includes(timing.state)) return null;
  return elapsedSince(timing.started_at, Date.parse(timing.ended_at));
}

/** A recorded empty reply still has a duration when the next ordinary prompt arrives. */
export function showTurnEnd(timing: TurnTiming | undefined, { hasContent, boundary, last, live }: { hasContent: boolean; boundary: boolean; last: boolean; live: boolean }): boolean {
  return boundary && (!last || !live) && (hasContent || completedDuration(timing) !== null);
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

/**
 * The line under a subagent's name in its row. Running: its current step from its own latest
 * item, which the browser holds only once its transcript was opened ("Running: bash npm test",
 * "Thinking…"), else nothing. Completed: its saved utility summary when available. Failed:
 * its error. The fallback is the first plain-text line of the provider report.
 */
export function subagentSummary(subagent: Subagent, items: readonly Item[] | undefined, parent: ToolCall | undefined): string {
  if (subagent.status === 'running') {
    if (subagent.preview) return subagent.preview;
    const last = items?.at(-1);
    if (last?.kind === 'reasoning') return 'Thinking…';
    if (last?.kind === 'tool') {
      const { name, arg } = toolLabel(last.tool);
      return `Running: ${name}${arg ? ` ${arg}` : ''}`;
    }
    return '';
  }
  if ((subagent.status === 'completed' || subagent.status === 'idle') && subagent.summary) return firstLine(subagent.summary);
  if (subagent.status === 'failed') return firstLine(subagent.error) || firstLine(subagent.result_summary) || firstLine(parent?.output);
  return firstLine(subagent.result_summary) || firstLine(parent?.output);
}

/**
 * The first line of a markdown report that says something, as plain text: fences skipped,
 * heading marks, bullets, quotes, emphasis, code ticks and link syntax dropped, so it reads
 * in a caption. A heading ("## Summary") only stands in when nothing follows it. Underscores
 * inside words stay, so identifiers survive.
 */
export function firstLine(text: string | undefined): string {
  if (!text) return '';
  let fenced = false;
  let heading = '';
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (/^(```|~~~)/.test(line)) {
      fenced = !fenced;
      continue;
    }
    if (fenced || /^[-*_=]{3,}$/.test(line)) continue;
    const plain = line
      .replace(/^(?:#{1,6}\s+|>\s*|[-*+]\s+|\d+[.)]\s+)+/, '')
      .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/(\*\*|__)(.+?)\1/g, '$2')
      .replace(/(^|[^\w])[*_](.+?)[*_](?=[^\w]|$)/g, '$1$2')
      .replace(/~~(.+?)~~/g, '$1')
      .replace(/`+([^`]*)`+/g, '$1');
    if (!plain.trim()) continue;
    if (/^#{1,6}\s/.test(line)) {
      heading ||= oneLine(plain);
      continue;
    }
    return oneLine(plain);
  }
  return heading;
}

// ---------------------------------------------------------------------------------------
// The compact activity model (DESIGN.md Transcript, "Turn line"): one line per turn instead
// of one activity row per run of work. How a turn's thoughts and tool calls are counted for
// its head row, which entries still stand in the answer (promotion) and the current step for
// the live foot line.

export type ActivityKind = 'command' | 'file' | 'search' | 'question' | 'subagent' | 'tool';

const COMMAND_TOOLS: readonly string[] = ['bash', 'shell', 'powershell'];
const READ_TOOLS: readonly string[] = ['view', 'read', 'list'];
const SEARCH_TOOLS: readonly string[] = ['grep', 'glob', 'web_fetch', 'webfetch', 'fetch', 'web_search', 'websearch'];

/** What kind of work a tool call is, by the names providers report; anything else is a tool. */
export function toolKind(name: string): ActivityKind {
  const n = name.toLowerCase();
  if (COMMAND_TOOLS.includes(n)) return 'command';
  if (READ_TOOLS.includes(n) || CHANGE_TOOLS.includes(n)) return 'file';
  if (SEARCH_TOOLS.includes(n)) return 'search';
  if (n === 'task') return 'subagent';
  if (n === 'ask_user') return 'question';
  return 'tool';
}

export interface ActivityContext {
  /** The provider still holds the turn: an open call may still report. */
  live: boolean;
  /** The item still receiving deltas, if any. */
  streamingId?: string;
  /** Requests by the tool item they sit on. */
  approvals?: Map<string, Interaction[]>;
}

export interface SummaryPart {
  /** "5 thoughts (42s)", "3 commands", "2 files read", "1 failed"… */
  text: string;
  /** `error` for the failures; the rest stay `muted`. */
  tone: 'muted' | 'error';
}

export interface TurnSummary {
  /** The counts in order; empty when the turn folded nothing. */
  parts: SummaryPart[];
  /** The parts joined by " · ". */
  label: string;
  /** `error` when a call failed; `attention` while a call waits for the user's permission or answer. */
  tone: 'muted' | 'error' | 'attention';
  /** How many entries the line stands for. */
  count: number;
}

/**
 * The turn line's counts (DESIGN.md turn line): finished thoughts with their total time, then
 * what the tools did by kind (commands, files changed and read by distinct path, searches,
 * subagents, other tools), the questions answered or declined and the requests decided, then
 * what stays explicit: failures, calls without a result, questions without an answer and
 * images returned. A call still running and thinking still streaming are not counted: the
 * live foot line names them.
 */
export function summarizeTurn(entries: Entry[], ctx: ActivityContext): TurnSummary {
  let count = 0, thoughts = 0, thinkMs = 0, commands = 0, searches = 0, subagents = 0, other = 0, failed = 0, noResult = 0, decided = 0, images = 0;
  const changed = new Set<string>();
  const read = new Set<string>();
  const outcomes: AskedQuestion['outcome'][] = [];
  let waiting = false;
  for (const entry of entries) {
    if (!isWork(entry)) continue;
    count++;
    if (entry.interaction) {
      if (entry.interaction.kind === 'question') outcomes.push(questionOf(undefined, entry.interaction, ctx.live)!.outcome);
      else decided++;
      continue;
    }
    const item = entry.item;
    if (item.kind === 'reasoning') {
      if (item.id === ctx.streamingId || (!item.text?.trim() && !item.compact?.has_reasoning)) continue;
      thoughts++;
      if (item.ended_at) thinkMs += Math.max(0, Date.parse(item.ended_at) - Date.parse(item.time));
      continue;
    }
    const t = item.tool;
    images += item.images?.length ?? 0;
    const asked = askedOn(item, ctx.approvals, ctx.live);
    if (asked && asked.outcome !== 'pending') {
      outcomes.push(asked.outcome);
      continue;
    }
    if (t?.status === 'failed') {
      failed++;
      continue;
    }
    if (t?.status !== 'completed') {
      if (ctx.live) waiting ||= !!ctx.approvals?.get(item.id)?.some(awaitsUser);
      else noResult++;
      continue;
    }
    const name = t.name.toLowerCase();
    switch (toolKind(name)) {
      case 'command':
        commands++;
        break;
      case 'file': {
        const path = t.path ?? mainArgument(name, t.input);
        if (CHANGE_TOOLS.includes(name)) changed.add(path || item.id);
        else read.add(path || item.id);
        break;
      }
      case 'search':
        searches++;
        break;
      case 'subagent':
        subagents++;
        break;
      default:
        other++;
    }
  }
  const asked = (outcome: AskedQuestion['outcome']) => outcomes.filter((o) => o === outcome).length;
  failed += asked('failed');
  const thinkTime = thinkMs > 0 ? formatMs(thinkMs) : null;
  const quiet = (text: string | 0): SummaryPart | null => (text ? { text, tone: 'muted' } : null);
  const parts = [
    quiet(thoughts && `${noun(thoughts, 'thought')}${thinkTime ? ` (${thinkTime})` : ''}`),
    quiet(commands && noun(commands, 'command')),
    quiet(changed.size && `${noun(changed.size, 'file')} changed`),
    quiet(read.size && `${noun(read.size, 'file')} read`),
    quiet(searches && noun(searches, 'search', 'searches')),
    quiet(subagents && noun(subagents, 'subagent')),
    quiet(other && noun(other, 'tool')),
    quiet(asked('answered') && `${noun(asked('answered'), 'question')} answered`),
    quiet(asked('declined') && `${noun(asked('declined'), 'question')} declined`),
    quiet(decided && `${noun(decided, 'request')} decided`),
    failed && { text: `${failed} failed`, tone: 'error' as const },
    quiet(noResult && `${noResult} without a result`),
    quiet(asked('none') && `${noun(asked('none'), 'question')} unanswered`),
    quiet(images && noun(images, 'image')),
  ].filter(Boolean) as SummaryPart[];
  return { parts, label: parts.map((p) => p.text).join(' · '), tone: failed ? 'error' : waiting ? 'attention' : 'muted', count };
}

export interface Step {
  /** "Thinking…", "Running: bash npm test", "Waiting for your approval: bash rm -rf build". */
  label: string;
  tone: 'muted' | 'attention';
  /** The label is a state word that shimmers, not a command to read. */
  shimmer: boolean;
}

/**
 * What the live foot line names while a turn runs: thinking that still streams, or the last
 * call still open (by name and argument), waiting for the user when its request does. Null
 * when the agent is between steps or prose streams, so the line falls back to its verb. A
 * call `own` claims (a subagent's) is its own row and never a step.
 */
export function currentStep(items: readonly Item[], ctx: ActivityContext, own?: (item: Item) => boolean): Step | null {
  const last = items[items.length - 1];
  if (!last) return null;
  if (last.kind === 'reasoning') return last.id === ctx.streamingId ? { label: 'Thinking…', tone: 'muted', shimmer: true } : null;
  if (last.kind !== 'tool' || own?.(last)) return null;
  const status = last.tool?.status;
  if (!ctx.live || (status !== 'pending' && status !== 'running')) return null;
  const { name, arg } = toolLabel(last.tool);
  const call = arg ? `${name} ${arg}` : name;
  const pending = ctx.approvals?.get(last.id)?.find(awaitsUser);
  if (pending) return { label: `Waiting for your ${pending.kind === 'question' ? 'answer' : 'approval'}: ${call}`, tone: 'attention', shimmer: false };
  return { label: `Running: ${call}`, tone: 'muted', shimmer: false };
}

/**
 * Whether an entry stands in the answer at its place (DESIGN.md promotion) rather than folding
 * into the turn line: prose, notices and steer bubbles always; a failed call; a question that
 * no longer waits; a call whose result returned images; a call `own` takes over (a subagent
 * row); a question request. Thoughts, routine calls and decided permissions never do.
 */
export function promoted(entry: Entry, ctx: ActivityContext, own?: (item: Item) => boolean): boolean {
  if (entry.interaction) return entry.interaction.kind === 'question';
  const item = entry.item;
  if (item.kind === 'reasoning') return false;
  if (item.kind !== 'tool') return true;
  if (own?.(item)) return true;
  if (item.tool?.status === 'failed') return true;
  if ((item.images?.length ?? 0) > 0 || !!item.images_note) return true;
  const asked = askedOn(item, ctx.approvals, ctx.live);
  return !!asked && asked.outcome !== 'pending';
}

/** Only a completed local declaration replaces its routine tool row with a file card. */
export function isFileDeclaration(item: Item): boolean {
  const tool = item.tool;
  const declaration = tool?.declaration;
  return item.kind === 'tool' && tool?.name === 'uam_show_file' && tool.status === 'completed'
    && typeof declaration?.artifact_id === 'string' && !!declaration.artifact_id
    && typeof declaration.path === 'string' && declaration.path.startsWith('/');
}

/** The lightweight history index keeps only this grouping fact, never declaration metadata. */
export function isDeclarationBoundary(item: Item): boolean {
  return isFileDeclaration(item) || item.tool?.declaration_boundary === true;
}

export const FILE_DECLARATION_LIMIT = 128;

/** Call IDs are agent-local; the latest copy of each call decides whether it has a card. */
export function declarationIdentity(item: Pick<Item, 'id' | 'agent_id'>): string {
  return JSON.stringify([item.agent_id ?? '', item.id]);
}

/** A window keeps only its newest declaration cards; older calls remain ordinary tool rows. */
export function newestFileDeclarations(items: readonly Item[]): Set<string> {
  const seen = new Set<string>();
  const cards = new Set<string>();
  for (let index = items.length - 1; index >= 0 && cards.size < FILE_DECLARATION_LIMIT; index--) {
    const item = items[index];
    const key = declarationIdentity(item);
    if (seen.has(key)) continue;
    seen.add(key);
    if (isFileDeclaration(item)) cards.add(key);
  }
  return cards;
}

/** The distinct paths the turn's completed edit, write and create calls named, in order. */
export function changedFiles(entries: Entry[]): string[] {
  const out: string[] = [];
  for (const { item } of entries) {
    const t = item?.tool;
    if (item?.kind !== 'tool' || t?.status !== 'completed' || !CHANGE_TOOLS.includes(t.name.toLowerCase())) continue;
    const path = t.path ?? mainArgument(t.name, t.input);
    if (path && !out.includes(path)) out.push(path);
  }
  return out;
}
