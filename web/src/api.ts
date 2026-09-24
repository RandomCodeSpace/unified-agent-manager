// Typed mirror of docs/adr/0004-web-interface.md and internal/agentapi.
// Field names are the wire names (snake_case).

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

export interface Capabilities {
  cancel: boolean;
  permissions: boolean;
  questions: boolean;
  session_diff: boolean;
  history: boolean;
}

export interface ProviderInfo {
  name: string;
  display_name: string;
  available: boolean;
  reason?: string;
  capabilities: Capabilities;
}

export interface Meta {
  version: string;
  providers: ProviderInfo[];
  recent_workdirs: string[];
}

export interface SessionSummary {
  id: string;
  provider: string;
  name: string;
  workdir: string;
  conversation_id: string;
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
  text?: string;
  tool?: ToolCall;
  time: string;
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
}

export interface Answer {
  decision?: string;
  answers?: string[][];
  reject?: boolean;
}

export type SubmissionStatus = 'accepted' | 'rejected' | 'uncertain';

export interface Submission {
  request_id: string;
  status: SubmissionStatus;
  error?: string;
  time: string;
}

export interface SessionDetail extends SessionSummary {
  items: Item[];
  interactions: Interaction[];
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
  sessions: SessionSummary[];
  session: SessionDetail | null;
}

export type UpdateData =
  | { name: 'session'; seq: number; session: SessionSummary }
  | { name: 'item'; seq: number; session_id: string; item: Item }
  | { name: 'delta'; seq: number; session_id: string; item_id: string; kind: ItemKind; text: string }
  | { name: 'interaction'; seq: number; session_id: string; interaction: Interaction }
  | { name: 'submission'; seq: number; session_id: string; submission: Submission };

export const UPDATE_EVENTS = ['session', 'item', 'delta', 'interaction', 'submission'] as const;

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
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

async function call<T>(method: 'GET' | 'POST' | 'PATCH', path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: method === 'GET' ? undefined : { 'Content-Type': 'application/json' },
      body: method === 'GET' ? undefined : JSON.stringify(body ?? {}),
    });
  } catch {
    throw new ApiError(0, 'Could not reach the server');
  }
  if (res.status === 401 && path !== '/api/login') unauthorized();
  if (!res.ok) throw new ApiError(res.status, await errorMessage(res));
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

async function errorMessage(res: Response): Promise<string> {
  try {
    const j = (await res.json()) as { error?: unknown };
    if (typeof j.error === 'string' && j.error) return j.error;
  } catch {
    // not JSON
  }
  return `${res.status} ${res.statusText}`.trim();
}

const enc = encodeURIComponent;

export const api = {
  auth: () => call<{ authenticated: boolean; required?: boolean }>('GET', '/api/auth'),
  login: (token: string) => call<void>('POST', '/api/login', { token }),
  logout: () => call<void>('POST', '/api/logout'),
  meta: () => call<Meta>('GET', '/api/meta'),
  createSession: (body: { provider: string; workdir: string; name: string; prompt?: string; request_id: string }) =>
    call<SessionSummary>('POST', '/api/sessions', body),
  rename: (id: string, name: string) => call<SessionSummary>('PATCH', `/api/sessions/${enc(id)}`, { name }),
  prompt: (id: string, text: string, request_id: string) =>
    call<Submission>('POST', `/api/sessions/${enc(id)}/prompt`, { text, request_id }),
  cancel: (id: string) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/cancel`),
  close: (id: string) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/close`),
  respond: (id: string, iid: string, answer: Answer) =>
    call<Interaction>('POST', `/api/sessions/${enc(id)}/interactions/${enc(iid)}`, answer),
  changes: (id: string, scope: Scope) => call<Changes>('GET', `/api/sessions/${enc(id)}/changes?scope=${scope}`),
  changeFile: (id: string, scope: Scope, path: string) =>
    call<FileDiff>('GET', `/api/sessions/${enc(id)}/changes/file?scope=${scope}&path=${enc(path)}`),
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

export function sessionName(s: SessionSummary): string {
  return s.name || basename(s.workdir);
}

export function pendingCount(s: SessionSummary): number {
  return typeof s.pending === 'number' ? s.pending : s.pending ? 1 : 0;
}

export function providerLabel(meta: Meta | null, name: string): string {
  return meta?.providers.find((p) => p.name === name)?.display_name ?? name;
}
