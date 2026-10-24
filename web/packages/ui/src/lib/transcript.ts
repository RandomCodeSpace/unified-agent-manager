// Rules for the turn's rows (ADR 0004, "Tool calls, thinking and approvals in the turn"):
// what a tool row shows as its main argument, how a long run folds, which decided request
// sits on which tool row, and what a question asked and got. No DOM, so the unit tests
// run in node.

import { isChartCall } from './chart.ts';
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
  if (!input?.startsWith('{')) return null;
  try {
    const v: unknown = JSON.parse(input);
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

/** The text an input that is one JSON string holds (apply_patch's patch), escapes decoded; null for any other input. */
function parseText(input: string): string | null {
  if (!input.startsWith('"')) return null;
  try {
    const v: unknown = JSON.parse(input);
    return typeof v === 'string' ? v : null;
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
 * key, the input's first string value; for a patch, the files it names; an input that is
 * one JSON string, decoded; a non-JSON input as is; else the provider's title.
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
  if (name.toLowerCase() === 'apply_patch') {
    const paths = patchPaths(input);
    if (paths.length) return oneLine(paths.join(', '));
  }
  const obj = parseObject(input);
  if (!obj) return oneLine(parseText(input) ?? input);
  for (const key of [...(KEYS[name.toLowerCase()] ?? []), ...GENERIC]) {
    const v = obj[key];
    if (typeof v === 'string' && v.trim()) return oneLine(v);
  }
  for (const v of Object.values(obj)) if (typeof v === 'string' && v.trim()) return oneLine(v);
  return oneLine(input);
}

/**
 * A tool's input as a person reads it, for its details and its copy action: a command tool's
 * command, an input that is one JSON string decoded, else the input as it came. `kind` says
 * which: "command" only for a command.
 */
export function readableInput(name: string, input: string): { kind: 'command' | 'input'; text: string } {
  const text = parseText(input);
  if (toolKind(name) !== 'command') return { kind: 'input', text: text ?? input };
  const obj = parseObject(input);
  if (!obj) return { kind: 'command', text: text ?? input };
  const command = KEYS.bash.map((key) => obj[key]).find((v): v is string => typeof v === 'string' && !!v.trim());
  return command ? { kind: 'command', text: command } : { kind: 'input', text: input };
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
export const ASSISTED_RESOLUTION = 'allowed (assisted review)';

/** A question's mark by its state: answered is fine, declined is a denial, the rest is gone. */
const QUESTION_TONE: Record<InteractionState, 'ok' | 'denied' | 'gone'> = { pending: 'gone', answered: 'ok', rejected: 'denied', expired: 'gone' };

/** The word on a tool row's approval mark and the full resolution behind it. */
export function approvalMark(ix: Interaction): { word: string; full: string; tone: 'ok' | 'denied' | 'gone' } {
  const resolution = ix.resolution ? ` · ${ix.resolution}` : '';
  const full = `${ix.title} · ${STATE_TEXT[ix.state]}${resolution}`;
  if (ix.kind === 'question') return { word: ix.elicitation && ix.state === 'rejected' && ix.resolution === CANCELLED ? CANCELLED : STATE_TEXT[ix.state].toLowerCase(), full, tone: QUESTION_TONE[ix.state] };
  if (ix.resolution === YOLO_RESOLUTION) return { word: 'auto', full, tone: 'ok' };
  if (ix.resolution === ASSISTED_RESOLUTION) return { word: 'reviewed', full, tone: 'ok' };
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
  const mine = interactions.filter((ix) => ix.kind !== 'plan_review' && (ix.agent_id ?? '') === agentId).sort((a, b) => a.time.localeCompare(b.time));
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
  /** `none`: the call ended without an answer (a restart, a stopped turn); `cancelled`: an elicitation dismissed without declining it. */
  outcome: 'pending' | 'answered' | 'declined' | 'cancelled' | 'failed' | 'none';
  /** An elicitation link the user opened and confirmed: there is no answer text. */
  link?: boolean;
  /** What the tool reported when it failed. */
  error?: string;
}

/** The answer in Copilot's `ask_user` output: "User selected: <choice>", "User responded: <text>". */
const ANSWER = /^User\s+(?:selected|responded|answered)\s*:\s*/i;
/** What Copilot's CLI records for a question the user declined (CLI 1.0.88): a completed call with this output. */
export const DECLINED_OUTPUT = 'The user was unable to respond due to an error';
/** What UAM's adapter tells the CLI when the user declines; a failed call may carry it. */
const DECLINED_ERROR = 'the user declined to answer';
/** The service's resolution of an elicitation dismissed without declining it. */
const CANCELLED = 'cancelled';
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
    case 'answered': {
      if (interaction!.elicitation?.mode === 'url') return { ...answered(q, ''), link: true };
      // A bare "answered" is the service's state word, not the answer: the text is not recorded here.
      const text = (interaction!.resolution ?? '').replace(RESOLVED, '').trim();
      return answered(q, /^answered$/i.test(text) ? '' : text);
    }
    case 'rejected':
      q.outcome = interaction!.elicitation && interaction!.resolution === CANCELLED ? 'cancelled' : 'declined';
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
  const parts = new Set(answer.split(/;\s*|,\s*/).map((s) => s.trim()));
  const all = q.questions.flatMap((x) => x.choices);
  q.chosen = all.filter((c) => c === answer || parts.has(c));
  return q;
}

const noun = (n: number, word: string, plural = `${word}s`) => `${n} ${n === 1 ? word : plural}`;

/** The tools that write to the tree, by the names providers report; the Changes count follows their completions. */
export const CHANGE_TOOLS: readonly string[] = ['edit', 'write', 'create', 'apply_patch'];

/** The files a patch's Add, Update, Delete and Move to headers name; the input may be the patch as a JSON string or inside a JSON object. */
export function patchPaths(input: string | undefined): string[] {
  let patch = input?.trim() ?? '';
  if (patch.startsWith('"') || patch.startsWith('{')) {
    try {
      const v: unknown = JSON.parse(patch);
      if (typeof v === 'string') patch = v;
      else if (v && typeof v === 'object') patch = Object.values(v).find((x): x is string => typeof x === 'string' && x.includes('*** ')) ?? '';
    } catch {
      // Clipped JSON: split its escaped line breaks and drop the cut last line.
      patch = patch.replaceAll('\\n', '\n');
      patch = patch.slice(0, patch.lastIndexOf('\n') + 1);
    }
  }
  const out: string[] = [];
  for (const m of patch.matchAll(/^\s*\*\*\* (?:Add File|Update File|Delete File|Move to): (.+?)\s*$/gm)) if (!out.includes(m[1])) out.push(m[1]);
  return out;
}

/** The files one change call names: a patch's (the service's `file_paths`, else its headers), else its path argument. */
export function changePaths(t: ToolCall): string[] {
  if (t.name.toLowerCase() === 'apply_patch') return t.file_paths?.length ? [...t.file_paths] : patchPaths(t.input);
  const path = t.path ?? mainArgument(t.name, t.input);
  return path ? [path] : [];
}

/** Completed calls of a tool that changes files, so far in the Task; a rise means the Changes count may be stale. */
export function completedChanges(items: readonly Item[]): number {
  let n = 0;
  for (const it of items) if (it.kind === 'tool' && it.tool?.status === 'completed' && CHANGE_TOOLS.includes(it.tool.name.toLowerCase())) n++;
  return n;
}

/** "12s", "1m 4s" or "<1s" between two ISO timestamps; null when they are not in order. */
export function duration(from: string, to: string): string | null {
  return formatMs(new Date(to).getTime() - new Date(from).getTime());
}

/** How long a tool call or thought took, from its recorded start and end; null while it runs or when unrecorded. */
export function itemTook(item: Item): string | null {
  return item.ended_at ? duration(item.time, item.ended_at) : null;
}

/** A span in milliseconds as "12s", "1m 4s", "1h 15m" or "<1s"; null when it is not a span. */
export function formatMs(ms: number): string | null {
  if (!Number.isFinite(ms) || ms < 0) return null;
  if (ms < 1000) return '<1s';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor(s % 3600 / 60)}m`;
}

/** The request waits for the user: pending, and not one yolo mode is already answering. */
export const awaitsUser = (ix: Interaction) => ix.state === 'pending' && !ix.auto;

/** A run's tone: a failure first, then a call waiting for the user, else quiet. */
function activityTone(failed: number, waiting: boolean): 'muted' | 'error' | 'attention' {
  if (failed) return 'error';
  return waiting ? 'attention' : 'muted';
}

/** Work between two messages: thinking, tool calls, decided requests and questions that no longer wait. Prose, the user's bubbles and notices bound it. */
export function isWork(entry: Entry): boolean {
  if (entry.interaction) return entry.interaction.state !== 'pending';
  return entry.item.kind === 'reasoning' || entry.item.kind === 'tool';
}

/** The current turn includes every segment across steer messages. */
export function foregroundItems(items: Item[]): Item[] {
  for (let i = items.length - 1; i >= 0; i--) {
    if (items[i].kind === 'user' && !items[i].delivery) return items.slice(i);
  }
  return items;
}

/** Link the recorded interval to the ordinary user message that began its turn. */
export function timingForTurn(timings: TurnTiming[], userItemId: string | undefined): TurnTiming | undefined {
  if (!userItemId) return undefined;
  for (let i = timings.length - 1; i >= 0; i--) if (timings[i].user_item_id === userItemId) return timings[i];
  return undefined;
}

/**
 * A stopped clock requires both observed boundaries and a terminal outcome. It rounds like the
 * turn's steps (formatMs), so a thought never reads longer than the turn that holds it.
 */
export function completedDuration(timing: TurnTiming | undefined): string | null {
  if (!timing?.ended_at || !['completed', 'cancelled', 'failed'].includes(timing.state)) return null;
  const end = Date.parse(timing.paused_at || timing.ended_at);
  return formatMs(end - (timing.paused_ms ?? 0) - Date.parse(timing.started_at));
}

/** Recorded manual waits never count as foreground work, including after reload. */
export function turnElapsed(timing: TurnTiming | undefined, now: number): string | null {
  if (!timing || timing.state === 'unknown') return null;
  const end = timing.paused_at ? Date.parse(timing.paused_at) : now;
  return elapsedSince(timing.started_at, end - (timing.paused_ms ?? 0));
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
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor(seconds % 3600 / 60)}m`;
}

/**
 * The line under a subagent's name in its row. Running: its current step from its own latest
 * item, which the browser holds only once its transcript was opened ("Running: bash npm test",
 * "Thinking…"), else nothing. Failed: its error. Otherwise the first plain-text line of its
 * own result: the provider report, never a generated summary.
 */
export function subagentSummary(subagent: Subagent, items: readonly Item[] | undefined, parent: ToolCall | undefined): string {
  if (subagent.status === 'running') {
    if (subagent.preview) return subagent.preview;
    const last = items?.at(-1);
    if (last?.kind === 'reasoning') return 'Thinking…';
    if (last?.kind === 'tool') {
      const { name, arg } = toolLabel(last.tool);
      const call = arg ? `${name} ${arg}` : name;
      return `Running: ${call}`;
    }
    return '';
  }
  // A background launch's call output only acknowledges the launch.
  const report = subagent.background ? undefined : parent?.output;
  if (subagent.status === 'failed') return firstLine(subagent.error) || firstLine(subagent.result_summary) || firstLine(report);
  return firstLine(subagent.result_summary) || firstLine(report);
}

/** One leading heading, quote, bullet or ordered-list mark. */
const LEADING_MARK = /^(?:#{1,6}\s+|>\s*|[-*+]\s+|\d+[.)]\s+)/;

/** The line without its leading marks, however many ("> - # x" is "x"). */
function stripMarks(line: string): string {
  let rest = line;
  for (let m = LEADING_MARK.exec(rest); m; m = LEADING_MARK.exec(rest)) rest = rest.slice(m[0].length);
  return rest;
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
    // A `[` right after another `[` cannot start a link the earlier one did not, so the scan stays linear.
    const plain = stripMarks(line)
      .replace(/(?<!\[)!?\[([^\]]*)\]\([^)]*\)/g, '$1')
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

const COMMAND_TOOLS: ReadonlySet<string> = new Set(['bash', 'shell', 'powershell']);
const READ_TOOLS: ReadonlySet<string> = new Set(['view', 'read', 'list']);
const SEARCH_TOOLS: ReadonlySet<string> = new Set(['grep', 'glob', 'web_fetch', 'webfetch', 'fetch', 'web_search', 'websearch']);

/** What kind of work a tool call is, by the names providers report; anything else is a tool. */
export function toolKind(name: string): ActivityKind {
  const n = name.toLowerCase();
  if (COMMAND_TOOLS.has(n)) return 'command';
  if (READ_TOOLS.has(n) || CHANGE_TOOLS.includes(n)) return 'file';
  if (SEARCH_TOOLS.has(n)) return 'search';
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
  /** The Task's scratch plan file (`scratchPlan`): editing it changes no file of the Task. */
  scratch?: (path: string) => boolean;
}

/** Whether a changed path is exactly the provider's scratch plan file: a relative path is in `workdir`, as the service compares it. */
export function scratchPlan(planPath: string | undefined, workdir: string): ((path: string) => boolean) | undefined {
  if (!planPath) return undefined;
  const clean = (path: string) => path.split('/').filter((part, i) => part !== '.' && (part !== '' || i === 0)).join('/');
  return (path) => clean(path.startsWith('/') ? path : `${workdir}/${path}`) === clean(planPath);
}

export interface SummaryPart {
  /** "5 thoughts (42s)", "3 commands", "2 files read", "1 failed"… */
  text: string;
  /** `error` for the failures; the rest stay `muted`. */
  tone: 'muted' | 'error';
  /** The files-changed count, which the turn card's change chip states instead when it shows. */
  kind?: 'changed';
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
 * The turn line's counts (DESIGN.md turn line), counts only (the durations are on the steps): finished thoughts, then
 * what the tools did by kind (commands, files changed and read by distinct path, searches,
 * other tools), the questions answered or declined and the requests decided, then what stays
 * explicit: failures, calls without a result, questions without an answer and images returned.
 * A call still running and thinking still streaming are not counted: the live foot line names
 * them. A subagent's `task` call is never counted, failed or not: the reply's subagent chip is.
 */
export function summarizeTurn(entries: Entry[], ctx: ActivityContext): TurnSummary {
  let count = 0, thoughts = 0, commands = 0, searches = 0, other = 0, failed = 0, noResult = 0, decided = 0, images = 0;
  const changed = new Set<string>();
  const read = new Set<string>();
  const outcomes: AskedQuestion['outcome'][] = [];
  let waiting = false;
  for (const entry of entries) {
    // A subagent's call is not counted here: the reply's subagent chip counts its subagents.
    if (!isWork(entry) || isSubagentCall(entry.item)) continue;
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
        if (CHANGE_TOOLS.includes(name)) {
          const paths = changePaths(t);
          for (const p of paths.length ? paths : [item.id]) if (!ctx.scratch?.(p)) changed.add(p);
        } else read.add(t.path ?? (mainArgument(name, t.input) || item.id));
        break;
      }
      case 'search':
        searches++;
        break;
      default:
        other++;
    }
  }
  const asked = (outcome: AskedQuestion['outcome']) => outcomes.filter((o) => o === outcome).length;
  failed += asked('failed');
  const quiet = (text: string | 0, kind?: SummaryPart['kind']): SummaryPart | null => (text ? { text, tone: 'muted', ...(kind && { kind }) } : null);
  const parts = [
    quiet(thoughts && noun(thoughts, 'thought')),
    quiet(commands && noun(commands, 'command')),
    quiet(changed.size && `${noun(changed.size, 'file')} changed`, 'changed'),
    quiet(read.size && `${noun(read.size, 'file')} read`),
    quiet(searches && noun(searches, 'search', 'searches')),
    quiet(other && noun(other, 'tool')),
    quiet(asked('answered') && `${noun(asked('answered'), 'question')} answered`),
    quiet(asked('declined') && `${noun(asked('declined'), 'question')} declined`),
    quiet(asked('cancelled') && `${noun(asked('cancelled'), 'question')} cancelled`),
    quiet(decided && `${noun(decided, 'request')} decided`),
    failed && { text: `${failed} failed`, tone: 'error' as const },
    quiet(noResult && `${noResult} without a result`),
    quiet(asked('none') && `${noun(asked('none'), 'question')} not answered`),
    quiet(images && noun(images, 'image')),
  ].filter(Boolean) as SummaryPart[];
  return { parts, label: parts.map((p) => p.text).join(' · '), tone: activityTone(failed, waiting), count };
}

/** The time `spans` cover together: thoughts recorded in parallel (several summaries of one model call) count once. */
/**
 * The turn head's "local" figure: how long the turn's own tool calls ran, overlapping calls
 * counted once, less `pausedMs`, the time the turn waited on the user (a call that asks for
 * approval runs while it waits). A subagent's `task` call is not local work, its time is the
 * subagent's own, and a question call is waiting on the user. Calls without a recorded end add nothing.
 */
export function localToolMs(entries: readonly Entry[], pausedMs = 0): number {
  const spans: [number, number][] = [];
  for (const { item } of entries) {
    if (item?.kind !== 'tool' || !item.ended_at || isSubagentCall(item) || toolKind(item.tool?.name ?? '') === 'question') continue;
    spans.push([Date.parse(item.time), Date.parse(item.ended_at)]);
  }
  return Math.max(0, spanMs(spans) - pausedMs);
}

/** What a finished step produced, as its row's result: test counts, line counts of its edits, its exit code, or that it failed. */
export type StepResult =
  | { kind: 'tests'; passed: number; failed: number }
  | { kind: 'diff'; additions: number; deletions: number }
  | { kind: 'exit'; code: number }
  | { kind: 'failed' };

/** A step's result from what the call recorded, never inferred from its output text; null when it recorded nothing to show. */
export function stepResult(tool: ToolCall | undefined): StepResult | null {
  if (!tool || (tool.status !== 'completed' && tool.status !== 'failed')) return null;
  if (tool.tests && tool.tests.passed + tool.tests.failed > 0) return { kind: 'tests', passed: tool.tests.passed, failed: tool.tests.failed };
  const known = tool.status === 'completed' ? (tool.file_edits ?? []).filter((edit) => edit.diff_status === 'available') : [];
  if (known.length) return { kind: 'diff', additions: known.reduce((n, e) => n + (e.additions ?? 0), 0), deletions: known.reduce((n, e) => n + (e.deletions ?? 0), 0) };
  if (tool.status === 'failed') return tool.exit_code ? { kind: 'exit', code: tool.exit_code } : { kind: 'failed' };
  return tool.exit_code === 0 ? { kind: 'exit', code: 0 } : null;
}

function spanMs(spans: [number, number][]): number {
  let total = 0, end = -Infinity;
  for (const [from, to] of spans.filter(([a, b]) => b > a).sort((a, b) => a[0] - b[0])) {
    if (to <= end) continue;
    total += to - Math.max(from, end);
    end = to;
  }
  return total;
}

export interface Step {
  /** "Thinking…", "Running: bash npm test", "Waiting for your approval: bash rm -rf build". */
  label: string;
  tone: 'muted' | 'attention';
  /** The label is a state word that shimmers, not a command to read. */
  shimmer: boolean;
  /** The thought streaming or the call running, which the foot shows unfolded (DESIGN.md live step); absent for a call that waits for the user or asks a question. */
  item?: Item;
}

/**
 * What the live foot line names while a turn runs: thinking that still streams, or the last
 * call still open (by name and argument), waiting for the user when its request does. Null
 * when the agent is between steps or prose streams, so the line falls back to its verb. A
 * call `own` claims (a chart) and a subagent's call (the live subagent card
 * shows it) are never a step. A streaming thought and a running call carry their item, which
 * the foot shows unfolded until it folds into the counts.
 */
export function currentStep(items: readonly Item[], ctx: ActivityContext, own?: (item: Item) => boolean): Step | null {
  const last = items.at(-1);
  if (!last) return null;
  if (last.kind === 'reasoning') return last.id === ctx.streamingId ? { label: 'Thinking…', tone: 'muted', shimmer: true, item: last } : null;
  if (last.kind !== 'tool' || own?.(last) || isSubagentCall(last)) return null;
  const status = last.tool?.status;
  if (!ctx.live || (status !== 'pending' && status !== 'running')) return null;
  const { name, arg } = toolLabel(last.tool);
  const call = arg ? `${name} ${arg}` : name;
  const pending = ctx.approvals?.get(last.id)?.find(awaitsUser);
  if (pending) return { label: `Waiting for your ${pending.kind === 'question' ? 'answer' : 'approval'}: ${call}`, tone: 'attention', shimmer: false };
  return { label: `Running: ${call}`, tone: 'muted', shimmer: false, item: toolKind(name) === 'question' ? undefined : last };
}

/** A `task` call, which spawned a subagent: the live subagent card and the reply's chip show it, never the turn line or the foot. */
export function isSubagentCall(item: Item | undefined): boolean {
  return item?.kind === 'tool' && toolKind(item.tool?.name ?? '') === 'subagent';
}

/**
 * Whether an entry stands in the answer at its place (DESIGN.md promotion) rather than folding
 * into the turn line: prose, notices and steer bubbles always; a question that no longer waits;
 * a call whose result returned images (the images do, while the call folds, see `callProduct`);
 * a call `own` takes over (a chart); a question request.
 * Thoughts, calls (failed ones too: the turn line counts them) and decided permissions never do.
 */
export function promoted(entry: Entry, ctx: ActivityContext, own?: (item: Item) => boolean): boolean {
  if (entry.interaction) return entry.interaction.kind === 'question';
  const item = entry.item;
  if (item.kind === 'reasoning') return false;
  if (item.kind !== 'tool') return true;
  if (own?.(item)) return true;
  if ((item.images?.length ?? 0) > 0 || !!item.images_note) return true;
  const asked = askedOn(item, ctx.approvals, ctx.live);
  return !!asked && asked.outcome !== 'pending';
}

/** Only a completed local declaration can hint an inline file reference. */
export function isFileDeclaration(item: Item): boolean {
  const tool = item.tool;
  const declaration = tool?.declaration;
  return item.kind === 'tool' && tool?.name === 'uam_show_file' && tool.status === 'completed'
    && typeof declaration?.artifact_id === 'string' && !!declaration.artifact_id
    && typeof declaration.path === 'string' && declaration.path.startsWith('/');
}

/** Charts and returned images stand in the answer; file declarations stay in activity. */
export function callProduct(item: Item): 'chart' | 'images' | null {
  if (item.kind !== 'tool') return null;
  if (isChartCall(item)) return 'chart';
  return (item.images?.length ?? 0) > 0 || !!item.images_note ? 'images' : null;
}

/** The distinct paths the turn's completed edit, write, create and apply_patch calls named, in order, but the scratch plan. */
/**
 * The line counts of a turn's edits from its edit calls' own recorded patches, for its Changed
 * chip when no capture of the turn's changes was kept; null unless every completed edit call
 * (the scratch plan's aside) recorded counts for each file it touched.
 */
export function editCounts(entries: Entry[], scratch?: (path: string) => boolean): { additions: number; deletions: number } | null {
  let additions = 0, deletions = 0, any = false;
  for (const { item } of entries) {
    const t = item?.tool;
    if (item?.kind !== 'tool' || t?.status !== 'completed' || !CHANGE_TOOLS.includes(t.name.toLowerCase())) continue;
    const paths = changePaths(t);
    if (paths.length && paths.every((path) => scratch?.(path))) continue;
    const edits = t.file_edits ?? [];
    if (!edits.length || t.file_edits_truncated || edits.some((e) => e.diff_status !== 'available')) return null;
    for (const e of edits) { additions += e.additions ?? 0; deletions += e.deletions ?? 0; }
    any = true;
  }
  return any ? { additions, deletions } : null;
}

export function changedFiles(entries: Entry[], scratch?: (path: string) => boolean): string[] {
  const out: string[] = [];
  for (const { item } of entries) {
    const t = item?.tool;
    if (item?.kind !== 'tool' || t?.status !== 'completed' || !CHANGE_TOOLS.includes(t.name.toLowerCase())) continue;
    for (const path of changePaths(t)) if (!out.includes(path) && !scratch?.(path)) out.push(path);
  }
  return out;
}

// ---------------------------------------------------------------------------------------
// The step timeline (DESIGN.md steps): one clock-stamped row per thought, call or decided
// request, same-kind runs folded, the time between steps over 30s on the rail, a cap that defers.

/** Which Lucide glyph a step wears; the component maps it. */
export type StepIcon = 'thought' | 'skill' | 'read' | 'image' | 'search' | 'find' | 'fetch' | 'tool' | 'edit' | 'create' | 'files' | 'intent' | 'command' | 'question' | 'subagent';
export type WordTone = 'muted' | 'error' | 'attention';
export interface StepWords {
  icon: StepIcon;
  /** The step in plain words: "Read redraw.go", "Ran go test ./...", "Searched “1004”". */
  label: string;
  /** What it got, from what the call recorded: "ok · 12 passed", "exit 1", the first error line, "auto-approved". */
  result: { text: string; tone: WordTone }[];
  tone: WordTone;
  /** A provider's stated intent (report_intent): a muted italic step without a clock. */
  intent?: boolean;
  /** Runs of the same key fold into one step. */
  fold?: 'read' | 'search' | 'edit' | 'command';
}

const IMAGE_FILE = /\.(?:png|jpe?g|gif|webp|svg)$/i;
const baseName = (path: string) => path.slice(Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\')) + 1) || path;
const openCall = (status?: string) => status === 'pending' || status === 'running';

/** The line of a failed call's output that says what broke: the first naming an error, a failure or a `file:line:`, else the first non-empty one. */
export function firstErrorLine(output: string | undefined): string {
  const lines = (output ?? '').split('\n').map((l) => l.trim()).filter((l) => l && !/^exit status \d+$/i.test(l));
  return oneLine(lines.find((l) => /error|fail|panic|denied|not found|:\d+(?::\d+)?:/i.test(l)) ?? lines[0] ?? '');
}

/** A tool call as a step: its glyph, its label in plain words and its result words. `live`: the provider still holds the turn. */
export function callWords(item: Item, live: boolean, approvals?: Interaction[]): StepWords {
  const t = item.tool;
  const { name, arg } = toolLabel(t);
  const n = name.toLowerCase();
  const running = live && openCall(t?.status);
  const words = (icon: StepIcon, label: string, fold?: StepWords['fold']) => ({ icon, label, fold });
  let w: ReturnType<typeof words>;
  if (COMMAND_TOOLS.has(n)) w = words('command', `${running ? 'Running' : 'Ran'} ${arg || name}`, 'command');
  else if (READ_TOOLS.has(n)) w = IMAGE_FILE.test(arg) ? words('image', `Viewed ${baseName(arg)}`) : words('read', `${n === 'list' ? 'Listed' : 'Read'} ${baseName(arg) || name}`, 'read');
  else if (n === 'create') w = words('create', `Created ${baseName(arg) || name}`);
  else if (CHANGE_TOOLS.includes(n)) w = words('edit', `Edited ${(n === 'apply_patch' ? arg : baseName(arg)) || name}`, 'edit');
  else if (n === 'grep') {
    // A title standing in for the input ("Search "x" in src") already says what was searched.
    const what = arg.replace(/^search(?:ing|ed)?\s+/i, '');
    w = words('search', `Searched ${/["“]/.test(what) ? what : `“${what}”`}`, 'search');
  }
  else if (n === 'glob') w = words('find', `Found ${arg}`, 'search');
  else if (n === 'web_search' || n === 'websearch') w = words('search', `Searched the web for “${arg}”`);
  else if (SEARCH_TOOLS.has(n)) w = words('fetch', `Fetched ${arg.replace(/^https?:\/\//, '')}`);
  else if (n === 'skill') w = words('skill', `Loaded skill ${arg}`);
  else if (n === 'report_intent') return { icon: 'intent', label: arg || 'Intent', result: [], tone: 'muted', intent: true };
  else if (n === 'task') w = words('subagent', arg || 'Subagent');
  else if (n === ASK_TOOL) w = words('question', arg || 'Asked a question');
  else w = words('tool', arg ? `${name} · ${arg}` : name);
  const result: StepWords['result'] = [];
  const r = stepResult(t);
  if (r?.kind === 'tests') result.push(r.failed ? { text: `${r.passed} passed · ${r.failed} failed`, tone: 'error' } : { text: `ok · ${r.passed} passed`, tone: 'muted' });
  else if (r?.kind === 'diff') result.push({ text: `+${r.additions} −${r.deletions}`, tone: 'muted' });
  else if (r?.kind === 'exit') result.push({ text: r.code === 0 ? 'ok' : `exit ${r.code}`, tone: r.code === 0 ? 'muted' : 'error' });
  else if (r?.kind === 'failed') result.push({ text: 'failed', tone: 'error' });
  // A completed call that exited non-zero failed all the same (stepResult leaves its code out).
  if (t?.status === 'completed' && t.exit_code && r?.kind !== 'tests') result.push({ text: `exit ${t.exit_code}`, tone: 'error' });
  const failed = t?.status === 'failed' || !!t?.exit_code || result.some((x) => x.tone === 'error');
  const why = failed ? firstErrorLine(t?.output) : '';
  if (why) result.push({ text: why, tone: 'error' });
  const asked = n === ASK_TOOL ? questionOf(t, undefined, live)?.outcome : undefined;
  if (asked && asked !== 'pending') result.push({ text: asked === 'none' ? 'not answered' : asked, tone: 'attention' });
  else if (!live && openCall(t?.status)) result.push({ text: 'no result', tone: 'muted' });
  const decided = approvals?.filter((ix) => ix.kind !== 'question' && ix.state !== 'pending').at(-1);
  const mark = decided && approvalMark(decided);
  const machine = mark?.word === 'auto' || mark?.word === 'reviewed';
  if (mark) result.push({ text: machine ? `${mark.word === 'auto' ? 'auto' : 'review'}-approved` : mark.word, tone: machine ? 'muted' : 'attention' });
  // A call a person decided on, one that failed or one that returned images stands alone: its words are its own.
  const fold = w.fold && t?.status === 'completed' && !failed && !mark && !item.images?.length ? w.fold : undefined;
  return { ...w, fold, result, tone: failed ? 'error' : (mark && !machine) || asked ? 'attention' : 'muted' };
}

/** A step of the timeline (its `end` not finite when unrecorded): one entry, a folded run of same-kind calls, the time between two steps, or the cap's "N earlier steps". */
export type TimelineRow =
  | { type: 'entry'; key: string; entry: Entry; start: number; end: number }
  | { type: 'fold'; key: string; items: Item[]; fold: NonNullable<StepWords['fold']>; start: number; end: number }
  | { type: 'gap'; key: string; ms: number; you: boolean }
  | { type: 'more'; key: string; hidden: number; calls: number };

/** Gaps between two steps longer than this show on the rail. */
export const GAP_MS = 30_000;
/** Timelines with more steps than this show the first, "N earlier steps", and the last `CAP_TAIL`. */
export const STEP_CAP = 20;
const CAP_TAIL = 12;

const FOLD_LABEL: Record<NonNullable<StepWords['fold']>, (n: number) => string> = {
  read: (n) => `Read ${noun(n, 'file')}`,
  search: (n) => `Searched ${n} times`,
  edit: (n) => `Edited ${noun(n, 'file')}`,
  command: (n) => `Ran ${noun(n, 'command')}`,
};
export const foldLabel = (fold: NonNullable<StepWords['fold']>, n: number) => FOLD_LABEL[fold](n);

/**
 * A turn's work as timeline rows (DESIGN.md steps): each thought with something in it, each call
 * (`skip` leaves some out: a subagent's call is its row in the reply's list) and each decided
 * request; consecutive completed calls of one kind fold; a gap over 30s between one step's end and
 * the next one's start is its own row, "waiting on you" when the turn asked the person within it (or right after a decided request). Over
 * `STEP_CAP` steps, unless `all`, the first, a "N earlier steps" row and the last 12.
 */
export function timelineRows(entries: readonly Entry[], { live, approvals, skip, all = false }: { live: boolean; approvals?: Map<string, Interaction[]>; skip?: (item: Item) => boolean; all?: boolean }): TimelineRow[] {
  type Step = Extract<TimelineRow, { type: 'entry' | 'fold' }>;
  const steps: Step[] = [];
  const foldOf = (step: Step | undefined) => (step?.type === 'fold' ? step.fold : step?.entry.item?.kind === 'tool' ? callWords(step.entry.item, live, approvals?.get(step.entry.item.id)).fold : undefined);
  for (const entry of entries) {
    if (!isWork(entry)) continue;
    if (entry.interaction) {
      const at = Date.parse(entry.interaction.time);
      steps.push({ type: 'entry', key: entry.interaction.id, entry, start: at, end: at });
      continue;
    }
    const item = entry.item;
    if ((item.kind === 'reasoning' && !item.text?.trim() && !item.compact?.has_reasoning) || skip?.(item)) continue;
    const start = Date.parse(item.time);
    // No recorded end, no gap after it: nothing is inferred from what followed.
    const end = item.ended_at ? Date.parse(item.ended_at) : Number.NaN;
    const words = item.kind === 'tool' ? callWords(item, live, approvals?.get(item.id)) : undefined;
    const prev = steps.at(-1);
    if (words?.fold && prev && foldOf(prev) === words.fold) {
      const items = prev.type === 'fold' ? [...prev.items, item] : [prev.entry.item!, item];
      steps[steps.length - 1] = { type: 'fold', key: items[0].id, items, fold: words.fold, start: prev.start, end: Math.max(...[prev.end, end].filter(Number.isFinite)) };
      continue;
    }
    steps.push({ type: 'entry', key: item.id, entry, start, end });
  }
  // When the turn asked the person: a gap holding one of those moments was theirs, any other is the model's.
  const asks = entries.flatMap((e) => (e.item ? (approvals?.get(e.item.id) ?? []) : [])).filter((ix) => ix.state !== 'pending' && !['auto', 'reviewed'].includes(approvalMark(ix).word)).map((ix) => Date.parse(ix.time));
  const rows: TimelineRow[] = [];
  steps.forEach((step, i) => {
    rows.push(step);
    const next = steps[i + 1];
    const ms = next ? next.start - step.end : 0;
    if (Number.isFinite(ms) && ms > GAP_MS) rows.push({ type: 'gap', key: `gap-${step.key}`, ms, you: (step.type === 'entry' && !!step.entry.interaction) || asks.some((t) => t > step.end && t < next.start) });
  });
  if (all || steps.length <= STEP_CAP) return rows;
  const calls = steps.reduce((n, s) => n + (s.type === 'fold' ? s.items.length : 1), 0);
  const tail = rows.slice(rows.indexOf(steps[steps.length - CAP_TAIL]));
  return [rows[0], { type: 'more', key: 'more', hidden: steps.length - 1 - CAP_TAIL, calls }, ...tail];
}
