// Development-only in-browser fake of the `uam web` service, loaded by main.tsx
// when the page is opened with `?mock` under `vite dev`. It replaces window.fetch
// for `/api/*` and window.EventSource, and plays scripted continuations so the
// workspace feels alive. Not part of the production bundle.

import { LIVE, type Attachment, type Interaction, type Item, type Project, type QueuedPrompt, type SessionDetail, type SessionSummary, type Subagent, type SubagentStatus, type Submission, type TaskDefaults } from '../api';
import { seed, type MockState, type MockTask } from './data';

type Json = Record<string, unknown>;

const THINKING =
  'The user wants a small, safe change. I should check the existing tests first, then edit the one function involved and run the package tests rather than the whole suite.';

const LONG_THINKING = `The history file is at ~/.zsh_history and uses the extended format, so each line starts with a timestamp.

I need the alias names from .zshrc, then count how often each appears as the first word of a command. Aliases that never appear are the unused ones.

A plain grep per alias would be quadratic on a large history; better to build one awk pass that tallies first words, then join against the alias list.`;

const ALIAS_ANSWER = `Three aliases never appear in the last 10,000 history entries:

| Alias | Expands to |
|---|---|
| \`gl\` | \`git log --oneline\` |
| \`dcu\` | \`docker compose up\` |
| \`serve\` | \`python -m http.server\` |

I counted first words with one pass over \`~/.zsh_history\`:

\`\`\`sh
awk -F';' '{ split($2, w, " "); n[w[1]]++ } END { for (k in n) print n[k], k }' ~/.zsh_history | sort -rn
\`\`\`

Want me to remove them from \`.zshrc\`?`;

const now = () => new Date().toISOString();
let counter = 1000;
const nextId = (prefix: string) => `${prefix}${++counter}`;

function json(status: number, body?: unknown): Response {
  if (body === undefined) return new Response(null, { status });
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const fail = (status: number, error: string, extra: Json = {}) => json(status, { error, ...extra });

class FakeEventSource extends EventTarget {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  readonly CONNECTING = 0;
  readonly OPEN = 1;
  readonly CLOSED = 2;
  readyState = 0;
  onopen: ((e: Event) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  readonly session: string | null;
  private detach: () => void = () => {};

  constructor(url: string, hooks: { attach: (src: FakeEventSource) => () => void }) {
    super();
    this.session = new URL(url, window.location.origin).searchParams.get('session');
    window.setTimeout(() => {
      if (this.readyState === 2) return;
      this.readyState = 1;
      this.onopen?.(new Event('open'));
      this.detach = hooks.attach(this);
    }, 30);
  }

  emit(name: string, data: unknown) {
    if (this.readyState !== 1) return;
    this.dispatchEvent(new MessageEvent(name, { data: JSON.stringify(data) }));
  }

  close() {
    this.readyState = 2;
    this.detach();
  }
}

export function install(): void {
  const st: MockState = seed();
  const sources = new Set<FakeEventSource>();
  let seq = 1;

  const summary = (t: MockTask): SessionSummary => {
    const { items: _i, interactions: _n, subagents: _s, history_truncated: _h, last_submission: _l, agentItems: _a, ...rest } = t;
    return rest;
  };
  const detail = (t: MockTask): SessionDetail => {
    const { agentItems: _a, ...rest } = t;
    return rest;
  };
  const find = (id: string) => st.tasks.find((t) => t.id === id);
  const busy = (t: MockTask) => LIVE.includes(t.state);
  /** Stored uploads by id: the bytes and the record the routes hand out. */
  const uploads = new Map<string, Attachment & { id: string; task: string; bytes: Uint8Array }>();
  const modelMedia = (t: MockTask) => st.meta.providers.find((p) => p.name === t.provider)?.models.find((m) => m.id === t.model)?.media;
  const modelLabel = (t: MockTask) => st.meta.providers.find((p) => p.name === t.provider)?.models.find((m) => m.id === t.model)?.name ?? t.model;
  const isDir = (projectId: string, path: string) => (st.files[projectId] ?? []).some((f) => f.startsWith(`${path}/`));
  /** The service's checks on a prompt's files and attachments; a Response is the refusal. */
  const checkExtras = (t: MockTask, body: Json): { files: string[]; attachments: (Attachment & { id: string })[] } | Response => {
    const files = Array.isArray(body.files) ? body.files.map(String) : [];
    if (files.length > 20) return fail(400, 'a prompt can reference at most 20 files');
    const tree = st.files[t.project_id] ?? [];
    for (const f of files) {
      if (f.startsWith('/') || f.split('/').includes('..')) return fail(400, `file ${JSON.stringify(f)} must be a relative path inside the project`);
      if (!tree.includes(f) && !isDir(t.project_id, f)) return fail(400, `file ${JSON.stringify(f)} does not exist`);
    }
    const ids = Array.isArray(body.attachments) ? body.attachments.map(String) : [];
    if (ids.length > 5) return fail(400, 'a prompt can carry at most 5 attachments');
    const attachments: (Attachment & { id: string })[] = [];
    for (const id of ids) {
      const u = uploads.get(id);
      if (!u || u.task !== t.id) return fail(400, `attachment ${JSON.stringify(id)} is not an upload of this task`);
      attachments.push({ id: u.id, name: u.name, mime: u.mime, size: u.size });
    }
    const media = modelMedia(t);
    const images = attachments.filter((a) => a.mime.startsWith('image/')).length;
    if (media?.max_images && images > media.max_images) return fail(400, `${modelLabel(t)} accepts at most ${media.max_images} images per prompt`);
    return { files, attachments };
  };
  /** The service's selection checks, for a Project's defaults and a new Task alike: a string is the 400 message. */
  const checkSelection = (raw: unknown): TaskDefaults | string => {
    const d = (raw && typeof raw === 'object' ? raw : {}) as Json;
    const prov = st.meta.providers.find((p) => p.name === d.provider);
    if (!prov) return `unknown provider ${JSON.stringify(d.provider ?? '')}`;
    const model = String(d.model ?? '');
    const mo = prov.models.find((x) => x.id === model);
    if (model && !mo) return `model ${model} is not in the catalog`;
    const effort = String(d.effort ?? '');
    if (effort && !mo?.efforts?.includes(effort)) return `effort ${effort} is not offered by ${model || 'the default model'}`;
    const size = String(d.context_size || 'default');
    if (size !== 'default' && !(prov.capabilities.context_size && mo?.context_sizes?.some((s) => s.id === size))) return `context size ${size} is not offered by ${model || 'the default model'}`;
    const mode = d.mode ?? 'safe';
    if (mode !== 'safe' && mode !== 'yolo') return 'mode must be safe or yolo';
    return { provider: prov.name, model, effort, context_size: size, mode };
  };

  function broadcast(name: string, payload: Json, sessionId?: string) {
    seq++;
    for (const src of sources) {
      if (sessionId && src.session && src.session !== sessionId) continue;
      if (sessionId && !src.session && name !== 'session' && name !== 'session_removed') continue;
      src.emit(name, { seq, ...payload });
    }
  }
  const touch = (t: MockTask, patch: Partial<MockTask> = {}) => {
    Object.assign(t, patch, { updated_at: now() });
    broadcast('session', { session: summary(t) });
  };
  const pushItem = (t: MockTask, item: Item) => {
    const list = item.agent_id ? (t.agentItems[item.agent_id] ??= []) : t.items;
    const i = list.findIndex((x) => x.id === item.id);
    if (i < 0) list.push(item);
    else list[i] = item;
    broadcast('item', { session_id: t.id, item, ...(item.agent_id ? { agent_id: item.agent_id } : {}) }, t.id);
  };
  const delta = (t: MockTask, itemId: string, text: string, kind: 'assistant' | 'reasoning' = 'assistant', agentId?: string) => {
    const list = agentId ? (t.agentItems[agentId] ??= []) : t.items;
    const it = list.find((x) => x.id === itemId);
    if (it) it.text = (it.text ?? '') + text;
    else list.push({ id: itemId, kind, text, time: now(), ...(agentId ? { agent_id: agentId } : {}) });
    broadcast('delta', { session_id: t.id, item_id: itemId, kind, text, ...(agentId ? { agent_id: agentId } : {}) }, t.id);
  };
  /** Streams `text` as deltas onto a new item, a word at a time, while `alive` holds (by default: the task is busy). */
  const stream = async (t: MockTask, text: string, kind: 'assistant' | 'reasoning', msPerWord: number, agentId?: string, alive = () => busy(t)) => {
    const id = nextId(kind === 'reasoning' ? 'r' : 'm');
    for (const word of text.split(' ')) {
      if (!alive()) return false;
      delta(t, id, `${word} `, kind, agentId);
      await wait(msPerWord);
    }
    return true;
  };
  /** Running may end any way; idle may go running (a follow-up) or completed (conversation closed). The rest are final. */
  const setSubagent = (t: MockTask, id: string, status: SubagentStatus, error?: string) => {
    const s = t.subagents.find((x) => x.id === id);
    if (!s || !(s.status === 'running' || (s.status === 'idle' && (status === 'running' || status === 'completed')))) return;
    const { ended_at: _e, ...rest } = s;
    const next: Subagent = status === 'running' ? { ...rest, status } : { ...s, status, ended_at: now(), ...(error ? { error } : {}) };
    t.subagents = t.subagents.map((x) => (x.id === id ? next : x));
    broadcast('subagent', { session_id: t.id, subagent: next }, t.id);
    const parent = t.items.find((i) => i.id === s.parent_tool_call_id);
    const done = status === 'completed' || status === 'idle';
    if (parent?.tool && s.status === 'running') pushItem(t, { ...parent, tool: { ...parent.tool, status: done ? 'completed' : 'failed', output: done ? 'Finished. One file changed.' : error } });
    touch(t, { subagents_running: t.subagents.filter((x) => x.status === 'running').length });
  };
  const wait = (ms: number) => new Promise<void>((r) => window.setTimeout(r, ms));

  /** Streams reasoning, then an assistant reply, then a tool call, then a closing line, and completes the turn. */
  async function reply(t: MockTask, text: string, model = 'mai-code-1.1-flash') {
    await wait(600);
    if (!t.name && !t.title) touch(t, { title: (t.items.find((i) => i.kind === 'user')?.text ?? 'New task').slice(0, 60) });
    if (!(await stream(t, THINKING, 'reasoning', 70))) return;
    await wait(200);
    if (!(await stream(t, text, 'assistant', 45))) return;
    await wait(300);
    if (!busy(t)) return;
    const tid = nextId('c');
    pushItem(t, { id: tid, kind: 'tool', time: now(), tool: { name: 'bash', title: 'go test ./...', status: 'running', input: 'go test ./... -count=1' } });
    await wait(1400);
    if (!busy(t)) return;
    pushItem(t, { id: tid, kind: 'tool', time: now(), tool: { name: 'bash', title: 'go test ./...', status: 'completed', input: 'go test ./... -count=1', output: 'ok  \tinternal/vterm\t0.62s' } });
    await wait(400);
    if (!busy(t)) return;
    pushItem(t, { id: nextId('m'), kind: 'assistant', time: now(), text: 'Tests pass. Anything else?' });
    touch(t, { state: 'completed', last_model: model });
    drain(t);
  }

  /** Sends the oldest queued prompt when no turn is running and the queue is not paused. */
  function drain(t: MockTask) {
    if (busy(t) || t.queue_paused || !(t.queue?.length)) return;
    const [next, ...rest] = t.queue;
    t.queue = rest;
    broadcast('queue', { session_id: t.id, queue: rest, paused: false }, t.id);
    pushItem(t, { id: nextId('u'), kind: 'user', text: next.text, time: now(), ...(next.attachments?.length ? { attachments: next.attachments } : {}) });
    touch(t, { state: 'working', state_detail: undefined, open: true, queued: rest.length });
    void reply(t, `Picking up the queued message about "${next.text.slice(0, 40)}". Done; nothing else changed.`);
  }

  /** Keeps t8's two subagents alive: each thinks and adds a step every few seconds, then finishes. */
  async function runSubagents(t: MockTask) {
    void stream(t, 'The list template is fine; the cover image on post.html is the only one without alt text, so I will patch that and run the validator.', 'reasoning', 180, 'a1');
    for (let step = 1; step <= 3; step++) {
      await wait(2500);
      if (!busy(t)) return;
      for (const a of t.subagents.filter((x) => x.status === 'running')) {
        pushItem(t, { id: nextId(`${a.id}-x`), kind: 'tool', time: now(), agent_id: a.id, tool: { name: 'view', title: `Read file ${step} of 3`, status: 'completed', output: `${20 + step} lines` } });
      }
    }
    await wait(2000);
    if (!busy(t)) return;
    await stream(t, 'Cover images now carry `alt` text. Two templates changed:\n\n- `templates/post.html`\n- `templates/list.html`', 'assistant', 60, 'a1');
    setSubagent(t, 'a1', 'idle');
    await wait(2500);
    if (!busy(t)) return;
    pushItem(t, { id: nextId('a2-x'), kind: 'assistant', time: now(), agent_id: 'a2', text: '`--muted: #6b6560` measures 5.0:1 on white.' });
    setSubagent(t, 'a2', 'completed');
    await wait(1200);
    if (!busy(t)) return;
    pushItem(t, { id: nextId('m'), kind: 'assistant', time: now(), text: 'Both surveys are in. Cover images now carry alt text, and the muted token is 5.0:1 on white. Two files changed.' });
    touch(t, { state: 'completed' });
  }
  /** t7 is mid-turn from the start: a long think, then a markdown answer, streamed slowly enough to watch. */
  async function runThinking(t: MockTask) {
    await wait(400);
    if (!(await stream(t, LONG_THINKING, 'reasoning', 220))) return;
    await wait(400);
    touch(t, { title: 'Which of these aliases are never used?' });
    if (!(await stream(t, ALIAS_ANSWER, 'assistant', 60))) return;
    touch(t, { state: 'completed', last_model: 'mai-code-1.1-flash' });
  }
  /** A follow-up turn on one idle subagent: the reply streams into its transcript, then it is idle again. The task's own state never moves. */
  async function followUp(t: MockTask, agentId: string) {
    const alive = () => t.subagents.find((x) => x.id === agentId)?.status === 'running';
    await wait(500);
    const reply = 'Looked again with that in mind. The `alt` text now comes from the post\'s `cover_alt` front-matter field and falls back to the title, so no template needs an empty `alt`. One file changed: `templates/post.html`.';
    if (!(await stream(t, reply, 'assistant', 60, agentId, alive))) return;
    setSubagent(t, agentId, 'idle');
  }
  /** Outcome per request_id for subagent follow-ups; a repeat returns it without sending again. */
  const followUps = new Map<string, Submission>();
  const started = new Set<string>();

  const hooks = {
    attach(src: FakeEventSource) {
      sources.add(src);
      const t = src.session ? find(src.session) : undefined;
      if (src.session && !t) {
        src.onerror?.(new Event('error'));
        return () => sources.delete(src);
      }
      src.emit('snapshot', { seq, projects: st.projects, sessions: st.tasks.map(summary), session: t ? detail(t) : null });
      if (t && !started.has(t.id)) {
        if (t.id === 't8') {
          started.add(t.id);
          void runSubagents(t);
        } else if (t.id === 't7') {
          started.add(t.id);
          void runThinking(t);
        }
      }
      return () => sources.delete(src);
    },
  };

  window.EventSource = class extends FakeEventSource {
    constructor(url: string) {
      super(url, hooks);
    }
  } as unknown as typeof EventSource;

  const realFetch = window.fetch.bind(window);
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, window.location.origin);
    if (!url.pathname.startsWith('/api/')) return realFetch(input, init);
    const method = (init?.method ?? 'GET').toUpperCase();
    const body: Json = typeof init?.body === 'string' ? (JSON.parse(init.body) as Json) : {};
    await wait(60);
    return route(method, url, body);
  };

  /**
   * The upload goes through XMLHttpRequest for progress events, so the mock fakes the
   * subset the client uses: `open`, `setRequestHeader`, `send`, `abort`, `upload.onprogress`,
   * `onload`, `onerror`, `onabort`, `status`, `responseText`. Progress arrives in four steps.
   */
  class FakeXHR {
    upload: { onprogress: ((e: ProgressEvent) => void) | null } = { onprogress: null };
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    onabort: (() => void) | null = null;
    status = 0;
    statusText = '';
    responseText = '';
    private method = 'GET';
    private url = '';
    private aborted = false;
    open(method: string, url: string) {
      this.method = method.toUpperCase();
      this.url = url;
    }
    setRequestHeader() {}
    async send(body?: Blob | null) {
      const url = new URL(this.url, window.location.origin);
      const bytes = body instanceof Blob ? new Uint8Array(await body.arrayBuffer()) : new Uint8Array();
      const total = Math.max(1, bytes.length);
      for (let step = 1; step <= 4; step++) {
        await wait(110);
        if (this.aborted) return;
        this.upload.onprogress?.(new ProgressEvent('progress', { lengthComputable: true, loaded: Math.round((total * step) / 4), total }));
      }
      const res = route(this.method, url, {}, bytes);
      this.status = res.status;
      this.responseText = await res.text();
      if (!this.aborted) this.onload?.();
    }
    abort() {
      this.aborted = true;
      this.onabort?.();
    }
  }
  window.XMLHttpRequest = FakeXHR as unknown as typeof XMLHttpRequest;

  /** The service's byte sniffing and size limits; a `status` means the refusal. */
  function sniff(b: Uint8Array): { mime: string } | { status: number; error: string } {
    if (!b.length) return { status: 400, error: 'the file is empty' };
    const has = (sig: number[], off = 0) => sig.every((v, i) => b[off + i] === v);
    let mime = '';
    if (has([0x89, 0x50, 0x4e, 0x47])) mime = 'image/png';
    else if (has([0xff, 0xd8, 0xff])) mime = 'image/jpeg';
    else if (has([0x47, 0x49, 0x46, 0x38])) mime = 'image/gif';
    else if (has([0x52, 0x49, 0x46, 0x46]) && has([0x57, 0x45, 0x42, 0x50], 8)) mime = 'image/webp';
    else if (has([0x25, 0x50, 0x44, 0x46, 0x2d])) mime = 'application/pdf';
    if (mime.startsWith('image/')) return b.length > 3 << 20 ? { status: 413, error: 'images can be at most 3 MiB' } : { mime };
    if (mime) return b.length > 10 << 20 ? { status: 413, error: 'PDF files can be at most 10 MiB' } : { mime };
    const refused = { status: 415, error: 'only png, jpeg, gif and webp images, PDF files and UTF-8 text files can be attached' };
    if (b.includes(0)) return refused;
    let text: string;
    try {
      text = new TextDecoder('utf-8', { fatal: true }).decode(b);
    } catch {
      return refused;
    }
    if (/^\ufeff?\s*(?:<\?[\s\S]*?\?>\s*|<!--[\s\S]*?-->\s*|<![^>]*>\s*)*<svg/i.test(text)) return { status: 415, error: 'SVG images cannot be attached' };
    if (b.length > 256 << 10) return { status: 413, error: 'text files can be at most 256 KiB' };
    return { mime: 'text/plain' };
  }

  function route(method: string, url: URL, body: Json, raw?: Uint8Array): Response {
    const path = url.pathname;
    const m = (re: RegExp) => path.match(re);
    let r: RegExpMatchArray | null;

    if (path === '/api/auth') return json(200, { authenticated: true, required: false });
    if (path === '/api/logout') return json(204);
    if (path === '/api/meta') return json(200, st.meta);

    if (path === '/api/projects' && method === 'GET') return json(200, { projects: st.projects });
    if (path === '/api/projects' && method === 'POST') {
      const dir = String(body.dir ?? '').trim();
      if (!dir.startsWith('/')) return fail(400, 'dir must be an absolute path to a directory');
      const existing = st.projects.find((p) => p.dir === dir.replace(/\/+$/, ''));
      if (existing) return fail(409, 'that directory already has a project', { project_id: existing.id });
      const defaults = body.defaults === undefined ? undefined : checkSelection(body.defaults);
      if (typeof defaults === 'string') return fail(400, defaults);
      const p: Project = { id: nextId('p'), name: String(body.name ?? '').trim() || dir.split('/').filter(Boolean).pop() || dir, dir, created_at: now(), ...(defaults ? { defaults } : {}) };
      st.projects.push(p);
      st.changes[p.id] = [];
      broadcast('project', { project: p });
      return json(201, p);
    }
    if ((r = m(/^\/api\/projects\/([^/]+)$/))) {
      const p = st.projects.find((x) => x.id === decodeURIComponent(r![1]));
      if (!p) return fail(404, 'project not found');
      if (method === 'PATCH') {
        if (body.name === undefined && body.defaults === undefined) return fail(400, 'name or defaults required');
        const defaults = body.defaults === undefined ? undefined : checkSelection(body.defaults);
        if (typeof defaults === 'string') return fail(400, defaults);
        if (body.name !== undefined) p.name = String(body.name).trim() || p.dir.split('/').filter(Boolean).pop() || p.dir;
        if (defaults) p.defaults = defaults;
        broadcast('project', { project: p });
        return json(200, p);
      }
      if (method === 'DELETE') {
        const tasks = st.tasks.filter((t) => t.project_id === p.id);
        if (tasks.some(busy)) return fail(409, 'a task in this project is busy');
        st.tasks = st.tasks.filter((t) => t.project_id !== p.id);
        st.projects = st.projects.filter((x) => x.id !== p.id);
        for (const t of tasks) broadcast('session_removed', { session_id: t.id });
        broadcast('project_removed', { project_id: p.id });
        return json(204);
      }
    }

    if (path === '/api/sessions' && method === 'GET') return json(200, st.tasks.map(summary));
    if (path === '/api/sessions' && method === 'POST') {
      const p = st.projects.find((x) => x.id === body.project_id);
      if (!p) return fail(400, 'unknown project_id');
      const sel = checkSelection(body);
      if (typeof sel === 'string') return fail(400, sel);
      const { provider, model, effort, context_size, mode } = sel;
      const prompt = String(body.prompt ?? '').trim();
      const t: MockTask = {
        id: nextId('t'),
        project_id: p.id,
        provider,
        name: String(body.name ?? '').trim(),
        title: '',
        workdir: p.dir,
        conversation_id: nextId('conv'),
        model,
        last_model: '',
        effort,
        context_size,
        mode,
        subagents_running: 0,
        state: prompt ? 'working' : 'idle',
        open: true,
        pending: 0,
        created_at: now(),
        updated_at: now(),
        capabilities: st.meta.providers[0].capabilities,
        items: prompt ? [{ id: nextId('u'), kind: 'user', text: prompt, time: now() }] : [],
        interactions: [],
        subagents: [],
        history_truncated: false,
        last_submission: null,
        agentItems: {},
      };
      st.tasks.push(t);
      broadcast('session', { session: summary(t) });
      if (prompt) void reply(t, 'Starting on it. I will read the relevant files first, then make the change and run the tests.', model === 'auto' || !model ? 'mai-code-1.1-flash' : model);
      return json(201, summary(t));
    }

    if ((r = m(/^\/api\/sessions\/([^/]+)$/))) {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      if (method === 'GET') return json(200, detail(t));
      if (method === 'PATCH') {
        const keys = ['name', 'model', 'effort', 'context_size', 'mode'].filter((k) => body[k] !== undefined);
        if (keys.length === 0) return fail(400, 'name, model, effort, context_size or mode required');
        if (t.stage === 'archived' && keys.some((k) => k !== 'name')) return fail(409, 'an archived task is read-only');
        if (t.stage === 'settled' && keys.some((k) => k !== 'name')) return fail(409, 'a settled task takes no changes; reopen it first');
        if (typeof body.name === 'string') t.name = body.name.trim();
        if (body.mode !== undefined) {
          if (body.mode !== 'safe' && body.mode !== 'yolo') return fail(400, 'mode must be safe or yolo');
          t.mode = body.mode;
        }
        const selection = keys.some((k) => k === 'model' || k === 'effort' || k === 'context_size');
        if (selection) {
          if (busy(t)) return fail(409, 'a turn is running; model, effort and context size change between turns');
          const model = body.model === undefined ? t.model : String(body.model);
          if (!model) return fail(400, 'model cannot be reset to the default');
          const checked = checkSelection({ provider: t.provider, model, effort: body.effort ?? (body.model !== undefined ? '' : t.effort), context_size: body.context_size ?? (body.model !== undefined ? 'default' : t.context_size), mode: t.mode });
          if (typeof checked === 'string') return fail(400, checked);
          t.model = checked.model;
          t.effort = checked.effort;
          t.context_size = checked.context_size;
        }
        touch(t);
        return json(200, summary(t));
      }
      if (method === 'DELETE') {
        if (t.stage !== 'archived') return fail(409, 'only an archived task can be deleted');
        st.tasks = st.tasks.filter((x) => x.id !== t.id);
        broadcast('session_removed', { session_id: t.id });
        return json(204);
      }
    }

    // Lifecycle: settle -> archive (from any stage, final) -> delete; a busy Task refuses a stage change.
    if ((r = m(/^\/api\/sessions\/([^/]+)\/(settle|reopen|archive)$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      const stage = t.stage ?? 'active';
      const blocked = busy(t) || t.interactions.some((i) => i.state === 'pending') || (t.queue?.length ?? 0) > 0;
      switch (r[2]) {
        case 'settle':
          if (stage !== 'active') return fail(409, `a ${stage} task cannot be settled`);
          if (blocked) return fail(409, 'stop the turn, resolve pending requests and clear queued prompts first');
          touch(t, { stage: 'settled', state: 'closed', open: false });
          return json(200, summary(t));
        case 'reopen':
          if (stage !== 'settled') return fail(409, `a ${stage} task cannot be reopened`);
          touch(t, { stage: 'active' });
          return json(200, summary(t));
        case 'archive':
          if (stage === 'archived') return fail(409, 'the task is already archived');
          if (stage === 'active' && blocked) return fail(409, 'stop the turn, resolve pending requests and clear queued prompts first');
          touch(t, { stage: 'archived', state: 'closed', open: false });
          return json(200, summary(t));
      }
    }

    // Queue control: resume, clear, cancel one.
    if ((r = m(/^\/api\/sessions\/([^/]+)\/queue\/(resume|clear)$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      if (r[2] === 'clear') t.queue = [];
      t.queue_paused = false;
      broadcast('queue', { session_id: t.id, queue: t.queue ?? [], paused: false }, t.id);
      if (r[2] === 'resume') drain(t);
      return json(204);
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/queue\/([^/]+)$/)) && method === 'DELETE') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      t.queue = (t.queue ?? []).filter((q) => q.request_id !== decodeURIComponent(r![2]));
      broadcast('queue', { session_id: t.id, queue: t.queue, paused: !!t.queue_paused }, t.id);
      return json(204);
    }

    if ((r = m(/^\/api\/sessions\/([^/]+)\/commands$/)) && method === 'GET') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      if (!t.open) return fail(409, 'the provider conversation is not open');
      return json(200, { commands: st.commands });
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/files$/)) && method === 'GET') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      const tree = st.files[t.project_id];
      if (!tree) return json(200, { files: [], reason: 'this directory is not in a Git working tree' });
      const limit = Math.min(200, Math.max(1, Number(url.searchParams.get('limit') ?? 50) || 50));
      const q = (url.searchParams.get('q') ?? '').toLowerCase();
      const candidates = new Set<string>();
      for (const f of tree) {
        candidates.add(f);
        for (let d = f.lastIndexOf('/'); d > 0; d = f.lastIndexOf('/', d - 1)) candidates.add(f.slice(0, d));
      }
      const score = (path: string) => {
        if (!q) return 0;
        const lower = path.toLowerCase();
        const base = lower.slice(lower.lastIndexOf('/') + 1);
        return base.startsWith(q) ? 0 : base.includes(q) ? 1 : lower.includes(q) ? 2 : -1;
      };
      const files = [...candidates]
        .map((path) => ({ path, s: score(path) }))
        .filter((x) => x.s >= 0)
        .sort((a, b) => a.s - b.s || a.path.length - b.path.length || (a.path < b.path ? -1 : 1))
        .slice(0, limit)
        .map(({ path }) => ({ path, type: isDir(t.project_id, path) ? 'directory' : 'file' }));
      return json(200, { files, reason: '' });
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/command$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      if (t.stage && t.stage !== 'active') return fail(409, `a ${t.stage} task takes no messages`);
      const name = String(body.name ?? '');
      if (!st.commands.some((c) => c.name === name)) return fail(404, `/${name} is not one of this task's commands`);
      if (busy(t)) return fail(409, 'the provider is still running a turn');
      const extras = checkExtras(t, body);
      if (extras instanceof Response) return extras;
      const args = String(body.arguments ?? '').trim();
      const sub: Submission = { request_id: String(body.request_id ?? ''), status: 'accepted', time: now() };
      pushItem(t, { id: nextId('u'), kind: 'user', text: `/${name}${args ? ` ${args}` : ''}`, time: now(), ...(extras.attachments.length ? { attachments: extras.attachments } : {}) });
      t.last_submission = sub;
      touch(t, { state: 'working', state_detail: undefined, open: true });
      broadcast('submission', { session_id: t.id, submission: sub }, t.id);
      void reply(t, name === 'review' ? 'Reviewing the uncommitted changes. Two files differ from HEAD; the replay change is fine, the test could assert the order too.' : `Running /${name}${args ? ` with "${args}"` : ''}. On it.`);
      return json(202, sub);
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/attachments$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      if (t.stage && t.stage !== 'active') return fail(409, `a ${t.stage} task takes no attachments`);
      const bytes = raw ?? new Uint8Array();
      const sniffed = sniff(bytes);
      if ('status' in sniffed) return fail(sniffed.status, sniffed.error);
      const media = modelMedia(t);
      if (media && sniffed.mime.startsWith('image/') && !media.images) return fail(400, `${modelLabel(t)} does not accept images`);
      if (media && sniffed.mime === 'application/pdf' && !media.pdf) return fail(400, `${modelLabel(t)} does not accept PDF files`);
      const kind = sniffed.mime.startsWith('image/') ? 'png' : sniffed.mime === 'application/pdf' ? 'pdf' : 'txt';
      const name = (url.searchParams.get('name') ?? '').split('/').pop()?.trim().slice(0, 120) || 'file';
      const att = { id: nextId(`att-${kind}-`), name, mime: sniffed.mime, size: bytes.length };
      uploads.set(att.id, { ...att, task: t.id, bytes });
      return json(201, att);
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/attachments\/([^/]+)$/)) && method === 'GET') {
      const u = uploads.get(decodeURIComponent(r[2]));
      if (!u || u.task !== decodeURIComponent(r[1])) return fail(404, 'attachment not found');
      return new Response(u.bytes.slice(), { status: 200, headers: { 'Content-Type': u.mime, 'X-Content-Type-Options': 'nosniff' } });
    }

    if ((r = m(/^\/api\/sessions\/([^/]+)\/(prompt|cancel|close)$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      switch (r[2]) {
        case 'prompt': {
          if (t.stage && t.stage !== 'active') return fail(409, `a ${t.stage} task takes no messages`);
          const text = String(body.text ?? '');
          if (!text.trim()) return fail(400, 'prompt text is required');
          const extras = checkExtras(t, body);
          if (extras instanceof Response) return extras;
          const withAttachments = extras.attachments.length ? { attachments: extras.attachments } : {};
          const sub = { request_id: String(body.request_id ?? ''), status: 'accepted' as const, time: now() };
          if (busy(t)) {
            if (body.mode === 'queue') {
              const queued: QueuedPrompt = { request_id: sub.request_id, text, queued_at: now(), ...(extras.files.length ? { files: extras.files } : {}), ...withAttachments };
              t.queue = [...(t.queue ?? []), queued];
              touch(t, { queued: t.queue.length });
              broadcast('queue', { session_id: t.id, queue: t.queue, paused: !!t.queue_paused }, t.id);
              return json(202, { ...sub, status: 'queued' });
            }
            if (body.mode === 'steer') {
              if (extras.files.length || extras.attachments.length) return fail(400, 'a steer takes text only; send files and attachments with a prompt');
              pushItem(t, { id: nextId('u'), kind: 'user', delivery: 'steer', text, time: now() });
              t.last_submission = sub;
              broadcast('submission', { session_id: t.id, submission: sub }, t.id);
              return json(202, sub);
            }
            return fail(409, 'a turn is already running in this session');
          }
          pushItem(t, { id: nextId('u'), kind: 'user', text, time: now(), ...withAttachments });
          t.last_submission = sub;
          touch(t, { state: 'working', state_detail: undefined, open: true });
          broadcast('submission', { session_id: t.id, submission: sub }, t.id);
          void reply(
            t,
            `Looked into that. Here is what I found about "${text.slice(0, 40)}":\n\n- the change is small and covered by the existing tests\n- nothing else references \`replayModes\`\n\n\`\`\`go\nfunc (v *VTerm) replayFocusEvents(w io.Writer) error {\n\tif !v.focusEvents {\n\t\treturn nil\n\t}\n\t_, err := w.Write([]byte("\\x1b[?1004h"))\n\treturn err\n}\n\`\`\``,
          );
          return json(202, sub);
        }
        case 'cancel': {
          if (!busy(t)) return json(202, summary(t));
          for (const i of t.items) if (i.tool && (i.tool.status === 'running' || i.tool.status === 'pending')) pushItem(t, { ...i, tool: { ...i.tool, status: 'failed', output: 'cancelled' } });
          for (const s of t.subagents) setSubagent(t, s.id, 'cancelled');
          for (const i of t.interactions) if (i.state === 'pending') resolve(t, i, 'expired', 'Turn stopped');
          pushItem(t, { id: nextId('n'), kind: 'notice', text: 'Turn stopped.', time: now() });
          if (t.queue?.length) {
            t.queue_paused = true;
            broadcast('queue', { session_id: t.id, queue: t.queue, paused: true }, t.id);
          }
          touch(t, { state: 'cancelled', pending: 0 });
          return json(202, summary(t));
        }
        case 'close':
          for (const s of t.subagents) setSubagent(t, s.id, s.status === 'idle' ? 'completed' : 'cancelled');
          touch(t, { state: 'closed', open: false, pending: 0 });
          return json(200, summary(t));
      }
    }

    if ((r = m(/^\/api\/sessions\/([^/]+)\/interactions\/([^/]+)$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      const i = t?.interactions.find((x) => x.id === decodeURIComponent(r![2]));
      if (!t || !i) return fail(404, 'interaction not found');
      if (i.state !== 'pending') return fail(409, 'already resolved');
      const reject = body.reject === true || (typeof body.decision === 'string' && !!i.options?.find((o) => o.id === body.decision)?.reject);
      const resolution =
        typeof body.decision === 'string'
          ? (i.options?.find((o) => o.id === body.decision)?.label ?? body.decision)
          : Array.isArray(body.answers)
            ? (body.answers as string[][]).flat().join(', ')
            : 'Declined';
      const next = resolve(t, i, reject ? 'rejected' : 'answered', resolution);
      touch(t, { state: 'working', pending: 0 });
      void reply(t, reject ? 'Understood, I will not do that. Wrapping up with what I have.' : 'Thanks. Applied that and finished the change.');
      return json(200, next);
    }

    if ((r = m(/^\/api\/sessions\/([^/]+)\/changes$/))) {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      const files = st.changes[t.project_id] ?? [];
      const p = st.projects.find((x) => x.id === t.project_id);
      return json(200, {
        scope: 'workspace',
        label: `Uncommitted changes in ${p?.name ?? t.workdir}, from any source, versus HEAD`,
        supported: true,
        files: files.map(({ path: fp, status, additions, deletions }) => ({ path: fp, status, additions, deletions })),
      });
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/changes\/file$/))) {
      const t = find(decodeURIComponent(r[1]));
      const f = t && (st.changes[t.project_id] ?? []).find((x) => x.path === url.searchParams.get('path'));
      if (!f) return fail(404, 'file not found');
      return json(200, f);
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/subagents\/([^/]+)$/))) {
      const t = find(decodeURIComponent(r[1]));
      const s = t?.subagents.find((x) => x.id === decodeURIComponent(r![2]));
      if (!t || !s) return fail(404, 'subagent not found');
      return json(200, { seq, subagent: s, items: t.agentItems[s.id] ?? [] });
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/subagents\/([^/]+)\/prompt$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      const s = t?.subagents.find((x) => x.id === decodeURIComponent(r![2]));
      if (!t || !s) return fail(404, 'subagent not found');
      const requestId = String(body.request_id ?? '');
      const recorded = followUps.get(requestId);
      if (recorded) return json(202, recorded);
      if (busy(t)) return fail(409, 'the task is running a turn; wait for it to finish');
      if (s.status !== 'idle') return fail(409, `the subagent is ${s.status}; only an idle subagent takes a follow-up`);
      const sub: Submission = { request_id: requestId, status: 'accepted', time: now() };
      followUps.set(requestId, sub);
      setSubagent(t, s.id, 'running');
      pushItem(t, { id: nextId('u'), kind: 'user', text: String(body.text ?? ''), time: now(), agent_id: s.id });
      void followUp(t, s.id);
      return json(202, sub);
    }
    return fail(404, `mock: no route for ${method} ${path}`);
  }

  function resolve(t: MockTask, i: Interaction, state: Interaction['state'], resolution: string): Interaction {
    const next: Interaction = { ...i, state, resolution };
    t.interactions = t.interactions.map((x) => (x.id === i.id ? next : x));
    broadcast('interaction', { session_id: t.id, interaction: next }, t.id);
    return next;
  }
}
