// Typed mirror of docs/adr/0004-web-interface.md plus the additions decided in
// issue #142 (projects, model catalog, titles, subagents). Field names are the
// wire names (snake_case). Paths keep the `sessions` name; the UI calls them
// Tasks.

import { visibleModels } from './lib/models';

export type SessionState =
  | 'idle'
  | 'starting'
  | 'working'
  | 'awaiting_permission'
  | 'awaiting_answer'
  | 'completed'
  | 'cancelled'
  | 'failed'
  | 'interrupted'
  | 'closed';

/** States in which the provider still holds the turn. */
export const LIVE: readonly SessionState[] = ['starting', 'working', 'awaiting_permission', 'awaiting_answer'];
/** States in which the provider is waiting for the user. */
export const ATTENTION: readonly SessionState[] = ['awaiting_permission', 'awaiting_answer'];

export interface Capabilities {
  cancel: boolean;
  permissions: boolean;
  questions: boolean;
  session_diff: boolean;
  history: boolean;
  context_size?: boolean;
  /** The provider reports account quota and per-Task AI units (#188); the second parity exception. */
  usage?: boolean;
  /** A chosen model can title the provider's new Tasks (#183). */
  titles?: boolean;
  import?: boolean;
}

/** What a model accepts as uploads; absent on the model means it reports nothing and is not gated. */
export interface Media {
  images: boolean;
  pdf: boolean;
  /** Most images one prompt may carry; absent or 0 means no reported limit. */
  max_images?: number;
  types?: string[];
}

export type CostTier = 'low' | 'medium' | 'high' | 'very_high';

/** One context tier's token prices in AI Credits per `Prices.batch_size` tokens; an absent price was not reported. */
export interface TierPrices {
  input?: number;
  output?: number;
  cache_read?: number;
  cache_write?: number;
  /** The tier's prompt budget; the long-context prices apply past it. */
  max_prompt_tokens?: number;
}

export interface Prices extends TierPrices {
  /** Tokens per priced batch (1,000,000 for Copilot); absent means 1,000,000. */
  batch_size?: number;
  long_context?: TierPrices;
}

/** One selectable model of the signed-in account. */
export interface Model {
  id: string;
  name: string;
  efforts?: string[];
  context_sizes?: { id: string; tokens: number }[];
  media?: Media;
  /** The provider's relative cost of the model; absent when it reports none. */
  cost_tier?: CostTier;
  /** Whole-number discount on usage billed through this model (Copilot's `auto`). */
  discount_percent?: number;
  /** Token prices; absent when the provider reports none (`auto`). */
  prices?: Prices;
}

/** The latest live context report: `prompt` and `cached` come from the latest main-agent call and may lag `used`. */
export interface ContextUsage {
  used: number;
  limit: number;
  prompt?: number;
  cached?: number;
}

/** One account quota of a provider with the `usage` capability. */
export interface Quota {
  provider: string;
  type: string;
  used: number;
  entitlement: number;
  unlimited: boolean;
  remaining_percent: number;
  overage: number;
  /** Present only while it lies in the future. */
  reset_at?: string;
}

/** `GET /api/usage`, the `usage` frame and the snapshot's `usage`: the last quota read that succeeded. */
export interface AccountUsage {
  quotas: Quota[];
  /** The latest read failed; the quotas are the previous ones. */
  stale: boolean;
  updated_at?: string;
}

/** A command the Task can run from the composer (`/name`). */
export interface Command {
  name: string;
  description: string;
  kind: 'skill' | 'command';
  input_hint: string;
}

export interface FileEntry {
  path: string;
  type: 'file' | 'directory';
}

export interface FileList {
  files: FileEntry[];
  /** Why the list is empty or short (not a git tree, too many files); empty otherwise. */
  reason: string;
}

/** One folder in a `GET /api/fs/dirs` listing. `name` is sanitised for display; `path` is exact and is what navigation sends back. */
export interface DirEntry {
  name: string;
  path: string;
  /** Holds `.git` (a directory or, in a linked worktree, a file). */
  git: boolean;
  /** The name starts with `.`; the server lists these and the picker filters them. */
  hidden: boolean;
  /** A symbolic link to a directory. */
  link: boolean;
}

export interface DirList {
  path: string;
  /** Absent at `/`. */
  parent?: string;
  entries: DirEntry[];
  /** More than 1,000 folders; only the first 1,000 are listed. */
  truncated: boolean;
}

/** An upload of this Task. Without `id` there is no stored copy (a provider record only). */
export interface Attachment {
  id?: string;
  name: string;
  mime: string;
  size?: number;
}

export interface ProviderInfo {
  name: string;
  display_name: string;
  available: boolean;
  reason?: string;
  capabilities: Capabilities;
  /** Selectable models; empty means "provider default only". */
  models: Model[];
}

export interface Meta {
  version: string;
  providers: ProviderInfo[];
  recent_workdirs: string[];
}

/** Settings a new Task starts with. `context_size` is `default` when unset; `effort` may be empty. */
export interface TaskDefaults {
  provider: string;
  model: string;
  effort: string;
  context_size: string;
  mode: 'safe' | 'yolo';
}

export const BADGE_COLORS = ['red', 'orange', 'amber', 'lime', 'green', 'teal', 'cyan', 'blue', 'violet', 'pink'] as const;
export type BadgeColor = (typeof BADGE_COLORS)[number];

/** A Project's badge: two uppercase characters on one of the ten palette tones (DESIGN.md Badges). */
export interface Badge {
  text: string;
  color: BadgeColor;
}

export type SendDefault = 'steer' | 'queue';

/** The web interface's settings, kept by the service so they apply in every browser. */
export interface Settings {
  /** What Enter does while a turn runs. */
  send_default: SendDefault;
  /** Model IDs not offered anywhere a model is chosen, by provider; omitted when none is hidden (#191). */
  hidden_models?: Record<string, string[]>;
  /** The model that titles a provider's new Tasks; omitted when every provider keeps its own title (#183). */
  title_model?: Record<string, string>;
}

export interface Project {
  id: string;
  name: string;
  dir: string;
  created_at: string;
  badge: Badge;
  /** Defaults for new Tasks; absent when the Project has none. */
  defaults?: TaskDefaults;
  /** Current git branch of the directory; absent unless it is a checkout on a named branch. May change between `project` frames. */
  branch?: string;
}

export interface SessionSummary {
  id: string;
  project_id: string;
  provider: string;
  /** User-given name; empty means "display the provider title". */
  name: string;
  /** Provider-generated conversation title; empty until it arrives. */
  title: string;
  workdir: string;
  conversation_id: string;
  /** Selected model; empty means the provider default (and cannot be set back to empty). */
  model: string;
  /** Model reported by the latest turn; live only, empty when unknown. */
  last_model: string;
  subagents_running: number;
  effort?: string;
  context_size?: string;
  context?: ContextUsage;
  /** AI units the conversation used so far, once the provider reports them; never zero. */
  usage?: { ai_units: number };
  mode?: 'safe' | 'yolo';
  stage?: 'active' | 'settled' | 'archived';
  queued?: number;
  state: SessionState;
  state_detail?: string;
  open: boolean;
  pending: number | boolean;
  created_at: string;
  updated_at: string;
  capabilities: Capabilities;
}

export type ItemKind = 'user' | 'assistant' | 'reasoning' | 'tool' | 'notice';
export type ToolStatus = 'pending' | 'running' | 'completed' | 'failed';

export interface ToolCall {
  name: string;
  title?: string;
  status: ToolStatus;
  input?: string;
  output?: string;
}

/** An image a tool's result returned, stored with the Task; served by the attachment route. */
export interface ToolImage {
  id: string;
  mime: string;
  size: number;
  name?: string;
}

export interface Item {
  id: string;
  kind: ItemKind;
  delivery?: 'steer';
  text?: string;
  tool?: ToolCall;
  time: string;
  /** Subagent instance that produced the item; absent for the main agent. */
  agent_id?: string;
  /** Uploads a user item carried; never their bytes. */
  attachments?: Attachment[];
  /** Images a tool item's result returned, in the provider's order. */
  images?: ToolImage[];
  /** Why some of a tool item's images were left out. */
  images_note?: string;
}

/** `idle` is not terminal: the subagent finished and accepts a follow-up (see promptSubagent). */
export type SubagentStatus = 'running' | 'idle' | 'completed' | 'failed' | 'cancelled';

export interface Subagent {
  id: string;
  /** Item id of the `task` tool call that started this subagent (a tool item's id is the provider tool call id). */
  parent_tool_call_id?: string;
  name: string;
  description?: string;
  /** Failed and cancelled are final; completed turns idle only on the provider's report. Close, runtime exit and service stop mark running ones cancelled and idle ones completed. */
  status: SubagentStatus;
  error?: string;
  started_at?: string;
  ended_at?: string;
  /** Model id and effort level the subagent runs with, when the provider reports them. */
  model?: string;
  effort?: string;
}

/** Live provider-owned shells. Unknown snapshots retain the last observation only. */
export interface BackgroundTasks {
  known: boolean;
  tasks: { id: string; description?: string; command: string; status: string; started_at?: string; ended_at?: string }[];
}

export interface SubagentDetail {
  /** SSE sequence captured with the transcript and metadata. */
  seq: number;
  subagent: Subagent;
  items: Item[];
}

export type InteractionKind = 'permission' | 'question';
export type InteractionState = 'pending' | 'answered' | 'rejected' | 'expired';

export interface Option {
  id: string;
  label: string;
  reject?: boolean;
}

export interface Question {
  text: string;
  header?: string;
  choices?: string[];
  multiple?: boolean;
  custom: boolean;
}

export interface Interaction {
  id: string;
  kind: InteractionKind;
  title: string;
  detail?: string;
  options?: Option[];
  questions?: Question[];
  state: InteractionState;
  resolution?: string;
  time: string;
  agent_id?: string;
  /** The `tool` item (same `agent_id`) this request is for; absent or unmatched means no link. */
  tool_call_id?: string;
}

export interface Answer {
  decision?: string;
  answers?: string[][];
  reject?: boolean;
}

export type PromptMode = 'send' | 'steer' | 'queue';
export type SubmissionStatus = 'accepted' | 'rejected' | 'uncertain' | 'queued' | 'cancelled';
export interface QueuedPrompt {
  request_id: string;
  text: string;
  queued_at: string;
  files?: string[];
  attachments?: Attachment[];
}

/** Structured parts of a prompt beside its text. */
export interface PromptExtras {
  /** Project paths relative to the Task's directory (at most 20). */
  files?: string[];
  /** Upload IDs from `api.upload` (at most 5). */
  attachments?: string[];
}

export interface Submission {
  request_id: string;
  status: SubmissionStatus;
  error?: string;
  time: string;
}

export interface PreviousSession {
  provider: string;
  conversation_id: string;
  title: string;
  created_at: string;
  updated_at: string;
  in_use: boolean;
}

export interface SessionDetail extends SessionSummary {
  seq?: number;
  history?: 'loaded' | 'loading' | 'unavailable';
  history_reason?: string;
  terminal_session?: { id: string; name: string };
  queue?: QueuedPrompt[];
  queue_paused?: boolean;
  /** Main agent items only; subagent items come from the subagent route. */
  items: Item[];
  interactions: Interaction[];
  subagents: Subagent[];
  background_tasks?: BackgroundTasks;
  history_truncated: boolean;
  last_submission: Submission | null;
}

export type Scope = 'session' | 'workspace';

export interface ChangeFile {
  path: string;
  status: string;
  additions: number;
  deletions: number;
}

export interface Changes {
  scope: Scope;
  label: string;
  supported: boolean;
  reason?: string;
  files: ChangeFile[];
}

export interface FileDiff {
  path: string;
  status?: string;
  additions: number;
  deletions: number;
  before?: string;
  after?: string;
  patch?: string;
}

export interface SnapshotData {
  seq: number;
  projects: Project[];
  /** Absent from a service older than settings; the defaults apply then. */
  settings?: Settings;
  /** Absent from a service older than usage (#188). */
  usage?: AccountUsage;
  sessions: SessionSummary[];
  session: SessionDetail | null;
}

export type UpdateData =
  | { name: 'history'; seq: number; session_id: string; history: 'loaded' | 'loading' | 'unavailable'; history_reason?: string; history_truncated: boolean; items: Item[]; subagents: Subagent[] }
  | { name: 'queue'; seq: number; session_id: string; queue: QueuedPrompt[]; paused: boolean }
  | { name: 'session'; seq: number; session: SessionSummary }
  | { name: 'session_removed'; seq: number; session_id: string }
  | { name: 'project'; seq: number; project: Project }
  | { name: 'project_removed'; seq: number; project_id: string }
  | { name: 'settings'; seq: number; settings: Settings }
  | { name: 'usage'; seq: number; usage: AccountUsage }
  | { name: 'item'; seq: number; session_id: string; item: Item; agent_id?: string }
  | {
      name: 'delta';
      seq: number;
      session_id: string;
      item_id: string;
      kind: ItemKind;
      text: string;
      agent_id?: string;
    }
  | { name: 'interaction'; seq: number; session_id: string; interaction: Interaction }
  | { name: 'submission'; seq: number; session_id: string; submission: Submission }
  | { name: 'subagent'; seq: number; session_id: string; subagent: Subagent }
  | { name: 'background_tasks'; seq: number; session_id: string; background_tasks: BackgroundTasks };

export const UPDATE_EVENTS = [
  'session',
  'history',
  'session_removed',
  'project',
  'project_removed',
  'settings',
  'usage',
  'item',
  'delta',
  'interaction',
  'submission',
  'queue',
  'subagent',
  'background_tasks',
] as const;

export class ApiError extends Error {
  status: number;
  /** Parsed JSON error body, when the server sent one (e.g. `project_id` on 409). */
  body: Record<string, unknown>;
  constructor(status: number, message: string, body: Record<string, unknown> = {}) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

let unauthorized: () => void = () => {};

/** Registers the handler run when any request (other than login) gets a 401. */
export function onUnauthorized(fn: () => void): void {
  unauthorized = fn;
}

export function describeError(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export function isStatus(e: unknown, status: number): e is ApiError {
  return e instanceof ApiError && e.status === status;
}

type Method = 'GET' | 'POST' | 'PATCH' | 'DELETE';

async function call<T>(method: Method, path: string, body?: unknown, omitBody = false): Promise<T> {
  // GET and DELETE carry no body; the others are JSON (the server rejects anything else).
  const bodyless = method === 'GET' || method === 'DELETE';
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: bodyless ? undefined : { 'Content-Type': 'application/json' },
      body: bodyless || omitBody ? undefined : JSON.stringify(body ?? {}),
    });
  } catch {
    throw new ApiError(0, 'Could not reach the server');
  }
  if (res.status === 401 && path !== '/api/login') unauthorized();
  if (!res.ok) {
    const parsed = await errorBody(res);
    throw new ApiError(res.status, parsed.message, parsed.body);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

async function errorBody(res: Response): Promise<{ message: string; body: Record<string, unknown> }> {
  try {
    const j = (await res.json()) as Record<string, unknown>;
    if (j && typeof j === 'object') {
      const message = typeof j.error === 'string' && j.error ? j.error : `${res.status} ${res.statusText}`.trim();
      return { message, body: j };
    }
  } catch {
    // not JSON
  }
  return { message: `${res.status} ${res.statusText}`.trim(), body: {} };
}

const enc = encodeURIComponent;

export const api = {
  auth: () => call<{ authenticated: boolean; required?: boolean }>('GET', '/api/auth'),
  login: (token: string) => call<void>('POST', '/api/login', { token }),
  logout: () => call<void>('POST', '/api/logout'),
  meta: () => call<Meta>('GET', '/api/meta'),

  session: (id: string) => call<SessionDetail>('GET', `/api/sessions/${enc(id)}`),
  previousCounts: () => call<Record<string, number>>('GET', '/api/previous/counts'),
  previous: (id: string) => call<PreviousSession[]>('GET', `/api/projects/${enc(id)}/previous`),
  importPrevious: (id: string, conversationId: string) => call<SessionSummary>('POST', `/api/projects/${enc(id)}/previous/${enc(conversationId)}/import`),

  projects: async () => (await call<{ projects: Project[] }>('GET', '/api/projects')).projects,
  createProject: (body: { dir: string; name?: string; defaults?: TaskDefaults }) => call<Project>('POST', '/api/projects', body),
  updateProject: (id: string, body: { name?: string; defaults?: TaskDefaults }) => call<Project>('PATCH', `/api/projects/${enc(id)}`, body),
  deleteProject: (id: string) => call<void>('DELETE', `/api/projects/${enc(id)}`),
  /** Subdirectories of an absolute directory; the service user's home without `path`. Dot-folders only with `hidden`; the 1,000 cap counts what is listed. */
  listDirs: (path?: string, hidden = false) => call<DirList>('GET', `/api/fs/dirs${path ? `?path=${enc(path)}${hidden ? '&hidden=1' : ''}` : hidden ? '?hidden=1' : ''}`),
  makeDir: (parent: string, name: string) => call<{ path: string }>('POST', '/api/fs/dirs', { parent, name }),

  webSettings: () => call<Settings>('GET', '/api/settings'),
  /** The service refuses an unknown key or value with 400 and changes nothing. */
  updateWebSettings: (body: Partial<Settings>) => call<Settings>('PATCH', '/api/settings', body),
  /** The cached account quotas; never calls the provider. */
  usage: () => call<AccountUsage>('GET', '/api/usage'),

  createSession: (body: {
    project_id: string;
    provider: string;
    model?: string;
    effort?: string;
    context_size?: string;
    mode?: 'safe' | 'yolo';
    name?: string;
    prompt?: string;
    request_id: string;
  }) => call<SessionSummary>('POST', '/api/sessions', body),
  rename: (id: string, name: string) => call<SessionSummary>('PATCH', `/api/sessions/${enc(id)}`, { name }),
  setModel: (id: string, model: string) => call<SessionSummary>('PATCH', `/api/sessions/${enc(id)}`, { model }),
  settings: (id: string, body: { model?: string; effort?: string; context_size?: string; mode?: 'safe' | 'yolo' }) =>
    call<SessionSummary>('PATCH', `/api/sessions/${enc(id)}`, body),
  stage: (id: string, action: 'settle' | 'reopen' | 'archive') => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/${action}`),
  queueAction: (id: string, action: 'resume' | 'clear') => call<void>('POST', `/api/sessions/${enc(id)}/queue/${action}`),
  cancelQueued: (id: string, requestId: string) => call<void>('DELETE', `/api/sessions/${enc(id)}/queue/${enc(requestId)}`),
  cancelSubagent: (id: string, agentId: string) => call<Subagent>('POST', `/api/sessions/${enc(id)}/subagents/${enc(agentId)}/cancel`, undefined, true),
  /** Follow-up to an idle subagent; the main agent never sees it. A repeated request_id returns the recorded outcome without resending. */
  promptSubagent: (id: string, agentId: string, text: string, request_id: string) =>
    call<Submission>('POST', `/api/sessions/${enc(id)}/subagents/${enc(agentId)}/prompt`, { text, request_id }),
  deleteSession: (id: string) => call<void>('DELETE', `/api/sessions/${enc(id)}`),
  prompt: (id: string, text: string, request_id: string, mode: PromptMode = 'send', extras: PromptExtras = {}) =>
    call<Submission>('POST', `/api/sessions/${enc(id)}/prompt`, { text, request_id, mode, ...extras }),
  /** Runs a listed command; the rules of a send (409 while a turn runs, no queue or steer). */
  command: (id: string, name: string, args: string, request_id: string, extras: PromptExtras = {}) =>
    call<Submission>('POST', `/api/sessions/${enc(id)}/command`, { request_id, name, arguments: args, ...extras }),
  commands: async (id: string) => (await call<{ commands: Command[] }>('GET', `/api/sessions/${enc(id)}/commands`)).commands,
  files: (id: string, q: string, limit = 50) => call<FileList>('GET', `/api/sessions/${enc(id)}/files?q=${enc(q)}&limit=${limit}`),
  upload: uploadFile,
  attachmentUrl: (id: string, attachmentId: string) => `/api/sessions/${enc(id)}/attachments/${enc(attachmentId)}`,
  cancel: (id: string) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/cancel`),
  close: (id: string) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/close`),
  respond: (id: string, iid: string, answer: Answer) =>
    call<Interaction>('POST', `/api/sessions/${enc(id)}/interactions/${enc(iid)}`, answer),
  changes: (id: string, scope: Scope) => call<Changes>('GET', `/api/sessions/${enc(id)}/changes?scope=${scope}`),
  changeFile: (id: string, scope: Scope, path: string) =>
    call<FileDiff>('GET', `/api/sessions/${enc(id)}/changes/file?scope=${scope}&path=${enc(path)}`),
  subagent: (id: string, agentId: string) =>
    call<SubagentDetail>('GET', `/api/sessions/${enc(id)}/subagents/${enc(agentId)}`),
  eventsUrl: (id: string | null) => (id ? `/api/events?session=${enc(id)}` : '/api/events'),
};

export interface Upload {
  done: Promise<Attachment & { id: string }>;
  abort: () => void;
}

/**
 * Uploads one file as the raw body (`application/octet-stream`, the name in the query).
 * XMLHttpRequest, not fetch: it is the only same-origin transport that reports upload
 * progress over HTTP/1.1. `onProgress` gets 0…1.
 */
function uploadFile(id: string, file: File, onProgress: (fraction: number) => void): Upload {
  const xhr = new XMLHttpRequest();
  const done = new Promise<Attachment & { id: string }>((resolve, reject) => {
    xhr.open('POST', `/api/sessions/${enc(id)}/attachments?name=${enc(file.name)}`);
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) onProgress(Math.min(1, e.loaded / e.total));
    };
    xhr.onload = () => {
      let body: Record<string, unknown> = {};
      try {
        body = JSON.parse(xhr.responseText) as Record<string, unknown>;
      } catch {
        // not JSON
      }
      if (xhr.status === 401) unauthorized();
      if (xhr.status < 200 || xhr.status >= 300) {
        const message = typeof body.error === 'string' && body.error ? body.error : `${xhr.status} ${xhr.statusText}`.trim();
        reject(new ApiError(xhr.status, message, body));
        return;
      }
      resolve(body as unknown as Attachment & { id: string });
    };
    xhr.onerror = () => reject(new ApiError(0, 'Could not reach the server'));
    xhr.onabort = () => reject(new ApiError(0, 'Upload cancelled'));
    xhr.send(file);
  });
  return { done, abort: () => xhr.abort() };
}

/** UUID v4; crypto.randomUUID needs a secure context, which a plain-HTTP tunnel host may not be. */
export function newRequestId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

export function basename(path: string): string {
  const parts = path.replace(/\/+$/, '').split('/');
  return parts[parts.length - 1] || path;
}

/** Display name of a Task: the user's name, else the provider title, else a placeholder. */
export function taskName(s: Pick<SessionSummary, 'name' | 'title'>): string {
  return s.name || s.title || '';
}

export function pendingCount(s: SessionSummary): number {
  return typeof s.pending === 'number' ? s.pending : s.pending ? 1 : 0;
}

export function needsYou(s: SessionSummary): boolean {
  return ATTENTION.includes(s.state) || pendingCount(s) > 0;
}

export function provider(meta: Meta | null, name: string): ProviderInfo | undefined {
  return meta?.providers.find((p) => p.name === name);
}

export function providerLabel(meta: Meta | null, name: string): string {
  return provider(meta, name)?.display_name ?? name;
}

export function modelCatalog(meta: Meta | null, providerName: string): Model[] {
  // `?? []` tolerates a server older than the catalog.
  return provider(meta, providerName)?.models ?? [];
}

/**
 * What a new Task starts with, from a Project's defaults checked against the live catalog:
 * the default provider if listed and available, else the first available one; the default
 * model if offered and not hidden in Settings, keeping its effort and context size only
 * where still offered; a model no longer offered, or hidden, falls back to `auto` (else the
 * first visible model) with effort cleared and context size `default`. Without defaults:
 * `auto`, no effort, `default`, safe. Null until the provider list has loaded.
 */
export function resolveTaskDefaults(meta: Meta | null, defaults?: TaskDefaults, hidden?: Settings['hidden_models']): TaskDefaults | null {
  const providers = meta?.providers ?? [];
  const chosen =
    (defaults && providers.find((p) => p.name === defaults.provider && p.available)) ?? providers.find((p) => p.available) ?? providers[0];
  if (!chosen) return null;
  const mode = defaults?.mode ?? 'safe';
  const offered = visibleModels(chosen.models, hidden?.[chosen.name]);
  const model = defaults && offered.find((m) => m.id === defaults.model);
  if (!defaults || !model) {
    const first = offered.some((m) => m.id === 'auto') ? 'auto' : (offered[0]?.id ?? '');
    return { provider: chosen.name, model: first, effort: '', context_size: 'default', mode };
  }
  const contextOffered = !!chosen.capabilities.context_size && !!model.context_sizes?.some((s) => s.id === defaults.context_size);
  return {
    provider: chosen.name,
    model: model.id,
    effort: model.efforts?.includes(defaults.effort) ? defaults.effort : '',
    context_size: contextOffered ? defaults.context_size : 'default',
    mode,
  };
}

export function modelName(meta: Meta | null, providerName: string, id: string): string {
  if (!id) return 'Default model';
  return modelCatalog(meta, providerName).find((m) => m.id === id)?.name ?? id;
}

export const readOnly = (s: SessionSummary): boolean => s.stage === 'settled' || s.stage === 'archived';
export const stageLabel = (s: SessionSummary): string => s.stage === 'settled' ? 'Settled' : s.stage === 'archived' ? 'Archived' : 'Active';
