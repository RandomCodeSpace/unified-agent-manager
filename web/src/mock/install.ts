// Development-only in-browser fake of the `uam web` service, loaded by main.tsx
// when the page is opened with `?mock` under `vite dev`. It replaces window.fetch
// for `/api/*` and window.EventSource, and plays scripted continuations so the
// workspace feels alive. Not part of the production bundle.

import { LIVE, type Interaction, type Item, type Project, type SessionDetail, type SessionSummary, type Subagent, type SubagentStatus } from '../api';
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
  /** Streams `text` as deltas onto a new item, a word at a time, until the task is no longer busy. */
  const stream = async (t: MockTask, text: string, kind: 'assistant' | 'reasoning', msPerWord: number, agentId?: string) => {
    const id = nextId(kind === 'reasoning' ? 'r' : 'm');
    for (const word of text.split(' ')) {
      if (!busy(t)) return false;
      delta(t, id, `${word} `, kind, agentId);
      await wait(msPerWord);
    }
    return true;
  };
  const setSubagent = (t: MockTask, id: string, status: SubagentStatus, error?: string) => {
    const s = t.subagents.find((x) => x.id === id);
    if (!s || s.status !== 'running') return;
    const next: Subagent = { ...s, status, ended_at: now(), ...(error ? { error } : {}) };
    t.subagents = t.subagents.map((x) => (x.id === id ? next : x));
    broadcast('subagent', { session_id: t.id, subagent: next }, t.id);
    const parent = t.items.find((i) => i.id === s.parent_tool_call_id);
    if (parent?.tool) pushItem(t, { ...parent, tool: { ...parent.tool, status: status === 'completed' ? 'completed' : 'failed', output: status === 'completed' ? 'Finished. One file changed.' : error } });
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
    setSubagent(t, 'a1', 'completed');
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
    const body: Json = init?.body ? (JSON.parse(String(init.body)) as Json) : {};
    await wait(60);
    return route(method, url, body);
  };

  function route(method: string, url: URL, body: Json): Response {
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
      const p: Project = { id: nextId('p'), name: String(body.name ?? '').trim() || dir.split('/').filter(Boolean).pop() || dir, dir, created_at: now() };
      st.projects.push(p);
      st.changes[p.id] = [];
      broadcast('project', { project: p });
      return json(201, p);
    }
    if ((r = m(/^\/api\/projects\/([^/]+)$/))) {
      const p = st.projects.find((x) => x.id === decodeURIComponent(r![1]));
      if (!p) return fail(404, 'project not found');
      if (method === 'PATCH') {
        p.name = String(body.name ?? '').trim() || p.dir.split('/').filter(Boolean).pop() || p.dir;
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
      const model = String(body.model ?? '');
      if (model && !st.meta.providers[0].models.some((x) => x.id === model)) return fail(400, 'model is not in the catalog');
      const prompt = String(body.prompt ?? '').trim();
      const t: MockTask = {
        id: nextId('t'),
        project_id: p.id,
        provider: 'copilot',
        name: String(body.name ?? '').trim(),
        title: '',
        workdir: p.dir,
        conversation_id: nextId('conv'),
        model,
        last_model: '',
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
        if (typeof body.model === 'string') {
          if (!body.model) return fail(400, 'model cannot be reset to the default');
          if (!st.meta.providers[0].models.some((x) => x.id === body.model)) return fail(400, 'model is not in the catalog');
          if (body.model !== t.model && busy(t)) return fail(409, 'a turn is running');
          t.model = body.model;
        } else if (typeof body.name === 'string') {
          t.name = body.name.trim();
        } else return fail(400, 'name or model required');
        touch(t);
        return json(200, summary(t));
      }
      if (method === 'DELETE') {
        if (busy(t)) return fail(409, 'the task is busy');
        st.tasks = st.tasks.filter((x) => x.id !== t.id);
        broadcast('session_removed', { session_id: t.id });
        return json(204);
      }
    }

    if ((r = m(/^\/api\/sessions\/([^/]+)\/(prompt|cancel|close)$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      switch (r[2]) {
        case 'prompt': {
          if (busy(t)) return fail(409, 'a turn is running');
          const text = String(body.text ?? '');
          const sub = { request_id: String(body.request_id ?? ''), status: 'accepted' as const, time: now() };
          pushItem(t, { id: nextId('u'), kind: 'user', text, time: now() });
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
          touch(t, { state: 'cancelled', pending: 0 });
          return json(202, summary(t));
        }
        case 'close':
          for (const s of t.subagents) setSubagent(t, s.id, 'cancelled');
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
    return fail(404, `mock: no route for ${method} ${path}`);
  }

  function resolve(t: MockTask, i: Interaction, state: Interaction['state'], resolution: string): Interaction {
    const next: Interaction = { ...i, state, resolution };
    t.interactions = t.interactions.map((x) => (x.id === i.id ? next : x));
    broadcast('interaction', { session_id: t.id, interaction: next }, t.id);
    return next;
  }
}
