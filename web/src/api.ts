// Typed mirror of docs/adr/0004-web-interface.md plus the additions decided in
// issue #142 (projects, model catalog, titles, subagents). Field names are the
// wire names (snake_case). Paths keep the `sessions` name; the UI calls them
// Tasks.

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
}

/** One selectable model of the signed-in account. */
export interface Model {
  id: string;
  name: string;
  efforts?: string[];
  context_sizes?: { id: string; tokens: number }[];
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

export interface Project {
  id: string;
  name: string;
  dir: string;
  created_at: string;
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
  context?: { used: number; limit: number };
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

export interface Item {
  id: string;
  kind: ItemKind;
  delivery?: 'steer';
  text?: string;
  tool?: ToolCall;
  time: string;
  /** Subagent instance that produced the item; absent for the main agent. */
  agent_id?: string;
}

/** `idle` is not terminal: the subagent finished and accepts a follow-up (see promptSubagent). */
export type SubagentStatus = 'running' | 'idle' | 'completed' | 'failed' | 'cancelled';

export interface Subagent {
  id: string;
  /** Item id of the `task` tool call that started this subagent (a tool item's id is the provider tool call id). */
  parent_tool_call_id?: string;
  name: string;
  description?: string;
  /** Terminal statuses are final; close, runtime exit and service stop mark running ones cancelled and idle ones completed. */
  status: SubagentStatus;
  error?: string;
  started_at?: string;
  ended_at?: string;
  /** Model id and effort level the subagent runs with, when the provider reports them. */
  model?: string;
  effort?: string;
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
}

export interface Answer {
  decision?: string;
  answers?: string[][];
  reject?: boolean;
}

export type PromptMode = 'send' | 'steer' | 'queue';
export type SubmissionStatus = 'accepted' | 'rejected' | 'uncertain' | 'queued' | 'cancelled';
export interface QueuedPrompt { request_id: string; text: string; queued_at: string }

export interface Submission {
  request_id: string;
  status: SubmissionStatus;
  error?: string;
  time: string;
}

export interface SessionDetail extends SessionSummary {
  queue?: QueuedPrompt[];
  queue_paused?: boolean;
  /** Main agent items only; subagent items come from the subagent route. */
  items: Item[];
  interactions: Interaction[];
  subagents: Subagent[];
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
  sessions: SessionSummary[];
  session: SessionDetail | null;
}

export type UpdateData =
  | { name: 'queue'; seq: number; session_id: string; queue: QueuedPrompt[]; paused: boolean }
  | { name: 'session'; seq: number; session: SessionSummary }
  | { name: 'session_removed'; seq: number; session_id: string }
  | { name: 'project'; seq: number; project: Project }
  | { name: 'project_removed'; seq: number; project_id: string }
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
  | { name: 'subagent'; seq: number; session_id: string; subagent: Subagent };

export const UPDATE_EVENTS = [
  'session',
  'session_removed',
  'project',
  'project_removed',
  'item',
  'delta',
  'interaction',
  'submission',
  'queue',
  'subagent',
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

  projects: async () => (await call<{ projects: Project[] }>('GET', '/api/projects')).projects,
  createProject: (body: { dir: string; name?: string }) => call<Project>('POST', '/api/projects', body),
  renameProject: (id: string, name: string) => call<Project>('PATCH', `/api/projects/${enc(id)}`, { name }),
  deleteProject: (id: string) => call<void>('DELETE', `/api/projects/${enc(id)}`),

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
  prompt: (id: string, text: string, request_id: string, mode: PromptMode = 'send') =>
    call<Submission>('POST', `/api/sessions/${enc(id)}/prompt`, { text, request_id, mode }),
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

export function modelName(meta: Meta | null, providerName: string, id: string): string {
  if (!id) return 'Default model';
  return modelCatalog(meta, providerName).find((m) => m.id === id)?.name ?? id;
}

export const readOnly = (s: SessionSummary): boolean => s.stage === 'settled' || s.stage === 'archived';
export const stageLabel = (s: SessionSummary): string => s.stage === 'settled' ? 'Settled' : s.stage === 'archived' ? 'Archived' : 'Active';
