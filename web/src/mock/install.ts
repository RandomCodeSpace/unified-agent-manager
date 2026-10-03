// Development-only in-browser fake of the `uam web` service, loaded by main.tsx
// when the page is opened with `?mock` under `vite dev`. It replaces window.fetch
// for `/api/*` and window.EventSource, and plays scripted continuations so the
// workspace feels alive. Not part of the production bundle.

import { BADGE_COLORS, LIVE, type Ask, type Attachment, type CustomModel, type Badge, type Interaction, type Item, type Project, type QueuedPrompt, type SessionDetail, type SessionSummary, type Subagent, type SubagentStatus, type Submission, type TaskDefaults } from '../api';
import { itemCursor } from '../lib/historyWindow';
import { boardMock } from './board';
import { gitMock } from './git';
import { chartMock } from './charts';
import { routinesMock } from './routines';
import { accountMock } from './account';
import { configurationMock } from './configuration';
import { mcpMock } from './mcp';
import { assistMock } from './assist';
import { seed, type MockState, type MockTask } from './data';
import { seedUtility, utilityLog } from './utility';

type Json = Record<string, unknown>;

/** The service's history page size, and how many of its newest items a compact Task's main transcript keeps in memory; subagents keep none (read on open). */
const PAGE = 50;
const HELD = 200;
/** Archive cursors are `a.` and the item cursor, as on the service. */
const ARCHIVE_CURSOR = 'a.';

function decodeCursor(cursor: string): string {
  const raw = cursor.startsWith(ARCHIVE_CURSOR) ? cursor.slice(ARCHIVE_CURSOR.length) : cursor;
  return new TextDecoder().decode(Uint8Array.from(atob(raw.replaceAll('-', '+').replaceAll('_', '/')), (c) => c.charCodeAt(0)));
}

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
  /** The query of a detail stream (`/api/events/detail`), null on the main stream. */
  readonly detail: URLSearchParams | null;
  private detach: () => void = () => {};

  constructor(url: string, hooks: { attach: (src: FakeEventSource) => () => void }) {
    super();
    const parsed = new URL(url, window.location.origin);
    this.session = parsed.searchParams.get('session');
    this.detail = parsed.pathname === '/api/events/detail' ? parsed.searchParams : null;
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

/** A request the prompt or the interaction route took, in order, so a test can check what went and when. */
export interface Received {
  route: 'prompt' | 'answer';
  session: string;
  body: Json;
}

export function install(): { received: Received[] } {
  const st: MockState = seed();
  const received: Received[] = [];
  // `?mock&slow=1500` holds every reply and the first snapshot that long, to look at the loading states.
  const slow = Math.max(0, Number(new URLSearchParams(window.location.search).get('slow')) || 0);
  // `?mock&planner=off` starts with the planner off; `?mock&planner=unset` is a service that predates it (no setting, no routes).
  const plannerMode = new URLSearchParams(window.location.search).get('planner');
  const plannerKnown = plannerMode !== 'unset';
  if (!plannerKnown) delete st.settings.planner;
  else if (plannerMode === 'off') st.settings.planner = false;
  // `?mock&manychanges` adds 40 uncommitted files from no Task, to check Changes and Commit with a long list.
  if (new URLSearchParams(window.location.search).has('manychanges')) {
    for (let i = 1; i <= 40; i++) st.changes.p1.push({ path: `web/src/generated/module-${i}.ts`, status: 'M', additions: i, deletions: 1, patch: '' });
  }
  // Background AI: today's limit (40 here) is reached, so Settings shows the paused notice.
  st.settings.utility_daily_limit = 40;
  const utility = seedUtility(40);
  const createdBy = new Map<string, string>();
  const sources = new Set<FakeEventSource>();
  let seq = 1;
  // A reload is a service restart: a new instance, while archive cursors stay valid.
  const epoch = `mock-${Date.now().toString(36)}`;

  const summary = (t: MockTask): SessionSummary => {
    const { items: _i, interactions: _n, subagents: _s, history_truncated: _h, last_submission: _l, agentItems: _a, representation: _r, detail_stream: _d, ...rest } = t;
    return { ...rest, ask: askOf(t.interactions) };
  };
  // A compact Task's detail carries its newest page and a cursor for the rest.
  const detail = (t: MockTask): SessionDetail => {
    const { agentItems: _a, ...rest } = t;
    if (t.representation !== 'compact-v1') return rest;
    const start = Math.max(0, t.items.length - PAGE);
    const older = t.recordSubagents?.length && t.subagents.length ? { subagents_before: `a.${itemCursor(t.subagents[0].id)}` } : {};
    return { ...rest, ...older, epoch, items: t.items.slice(start), history_before: start > 0 ? itemCursor(t.items[start].id) : '' };
  };
  /**
   * One history page the way the service pages: the `held` newest items come from memory; older
   * ones from Copilot's record, as pages marked `archive` whose cursors survive a restart. Neither
   * kind of page crosses into the other, and every page carries the current seq and epoch.
   */
  const historyPage = (items: Item[], held: number, url: URL): Response => {
    const before = url.searchParams.get('before');
    const cursor = before ?? url.searchParams.get('after') ?? '';
    let at: number;
    try {
      const id = decodeCursor(cursor);
      at = items.findIndex((i) => i.id === id);
    } catch {
      return fail(400, 'invalid history cursor');
    }
    if (at < 0) return fail(409, 'history changed; reload the task');
    const boundary = Math.max(0, items.length - held);
    const start = before !== null ? Math.max(at - PAGE, at > boundary ? boundary : 0) : at + 1;
    const end = before !== null ? at : Math.min(start + PAGE, start < boundary ? boundary : items.length);
    const mark = (i: number) => (i <= boundary ? ARCHIVE_CURSOR : '') + itemCursor(items[i].id);
    return json(200, {
      seq,
      epoch,
      representation: 'compact-v1',
      items: items.slice(start, end),
      before: start > 0 ? mark(start) : '',
      after: start < end && end < items.length ? itemCursor(items[end - 1].id) : '',
      ...(end <= boundary ? { archive: true } : {}),
    });
  };
  const find = (id: string) => st.tasks.find((t) => t.id === id);
  const busy = (t: MockTask) => LIVE.includes(t.state);
  const routines = routinesMock((id) => st.projects.some((p) => p.id === id));
  const account = accountMock(st.meta);
  const configuration = configurationMock(() => !!st.settings.terminal);
  const mcp = mcpMock(() => !!st.settings.terminal);
  // The planner (ADR 0005); `?mock&bigplan` adds about 200 cards to notes-site.
  const charts = chartMock(st.projects, (project) => broadcast('project', { project }));
  const assist = assistMock({
    broadcast: (name, payload) => broadcast(name, payload),
    settings: () => st.settings,
    task: find,
    create: (body) => route('POST', new URL('/api/sessions', window.location.origin), body),
    newest: () => st.tasks.at(-1),
    summary: (t) => summary(t),
  });
  const board = boardMock({
    broadcast: (name, payload) => broadcast(name, payload),
    projects: () => st.projects,
    settings: () => st.settings,
    task: (id) => { const t = find(id); return t && summary(t); },
    createTask: (projectId, name, prompt) => {
      const p = st.projects.find((x) => x.id === projectId)!;
      const d = st.settings.task_defaults;
      const t: MockTask = {
        id: nextId('t'), project_id: p.id, provider: 'copilot', name, title: name, workdir: p.dir, conversation_id: nextId('conv'),
        model: d?.model ?? 'auto', last_model: '', effort: d?.effort, context_size: d?.context_size, mode: d?.mode, subagents_running: 0,
        state: 'working', open: true, pending: 0, created_at: now(), updated_at: now(), capabilities: st.meta.providers[0].capabilities,
        items: [{ id: nextId('u'), kind: 'user', text: prompt, time: now() }], interactions: [], subagents: [], history_truncated: false, last_submission: null, agentItems: {},
      };
      st.tasks.push(t);
      broadcast('session', { session: summary(t) });
      void reply(t, 'Reading the card and the files it names, then I will claim the next subtask.', t.model === 'auto' ? 'mai-code-1.1-flash' : t.model);
      return summary(t);
    },
  }, { big: new URLSearchParams(window.location.search).has('bigplan') });
  // The commit panel's routes; a Task's files are the ones its edit tools name.
  const git = gitMock({
    projects: () => st.projects,
    tasks: () => st.tasks.map(summary),
    task: (id) => { const t = find(id); return t && summary(t); },
    changes: () => st.changes,
    touched: (id) => {
      const t = find(id);
      const items = t ? [...t.items, ...Object.values(t.agentItems).flat()] : [];
      return items.flatMap((i) => (i.tool && ['edit', 'create'].includes(i.tool.name) ? [(i.tool.title ?? '').replace(/^(Edit|Create) /, '')] : []));
    },
    utilityLimit: () => st.settings.utility_daily_limit,
    projectChanged: (p) => {
      st.projects = st.projects.map((x) => (x.id === p.id ? p : x));
      broadcast('project', { project: p });
    },
  });
  /** Stored uploads by id: the bytes and the record the routes hand out. */
  const uploads = new Map<string, Attachment & { id: string; task: string; bytes: Uint8Array }>();
  /** The service's badge rule, roughly: first letter or digit plus one from the rest, unique text, an unused tone. */
  const newBadge = (name: string): Badge => {
    const chars = name.toUpperCase().replace(/[^A-Z0-9]/g, '');
    const taken = new Set(st.projects.map((p) => p.badge.text));
    let text = '';
    for (const second of [...chars.slice(1), ...'ABCDEFGHIJKLMNOPQRSTUVWXYZ']) {
      text = `${chars[0] ?? 'P'}${second}`;
      if (!taken.has(text)) break;
    }
    const used = new Set(st.projects.map((p) => p.badge.color));
    return { text, color: BADGE_COLORS.find((c) => !used.has(c)) ?? BADGE_COLORS[st.projects.length % BADGE_COLORS.length] };
  };
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
  /** The service's selection checks, for the Task defaults setting and a new Task alike: a string is the 400 message. */
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
    const started = now();
    pushItem(t, { id: tid, kind: 'tool', time: started, tool: { name: 'bash', title: 'go test ./...', status: 'running', input: 'go test ./... -count=1' } });
    // The output streams while the call runs, as Copilot reports it: the whole output so far each time.
    let output = '';
    for (const pkg of ['internal/agentapi', 'internal/adapter/copilot', 'internal/web', 'internal/vterm']) {
      await wait(700);
      if (!busy(t)) return;
      output += `ok  \t${pkg}\t0.${pkg.length}s\n`;
      pushItem(t, { id: tid, kind: 'tool', time: started, tool: { name: 'bash', title: 'go test ./...', status: 'running', input: 'go test ./... -count=1', output } });
    }
    await wait(700);
    if (!busy(t)) return;
    pushItem(t, { id: tid, kind: 'tool', time: started, ended_at: now(), tool: { name: 'bash', title: 'go test ./...', status: 'completed', input: 'go test ./... -count=1', output } });
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
      // A detail stream: an open subagent's first window. The mock's items carry their bodies, so no
      // body interest needs an answer. A subagent is read from Copilot's record on open, hence the wait.
      if (src.detail) {
        const agent = src.detail.get('agent');
        const items = t && agent ? t.agentItems[agent] : undefined;
        if (t && agent && items) {
          window.setTimeout(() => {
            const start = Math.max(0, items.length - PAGE);
            src.emit('detail_snapshot', { seq, epoch, session_id: t.id, agent_id: agent, items: items.slice(start), before: start > 0 ? ARCHIVE_CURSOR + itemCursor(items[start].id) : '', archive: true });
            src.emit('detail_ready', { seq, epoch, session_id: t.id });
          }, 600 + slow);
        }
        return () => sources.delete(src);
      }
      const emitSnapshot = () => src.emit('snapshot', { seq, projects: st.projects, settings: st.settings, ...(st.settings.planner ? { boards: { ...Object.fromEntries(st.projects.map((p) => [p.id, 0])), ...board.revisions() } } : {}), sessions: st.tasks.map(summary), session: t ? detail(t) : null });
      if (slow) window.setTimeout(emitSnapshot, slow);
      else emitSnapshot();
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

  // A Project's terminal socket gets a fake shell; every other socket is real.
  const RealWebSocket = window.WebSocket;
  window.WebSocket = Object.assign(function (url: string | URL, protocols?: string | string[]) {
    return /\/api\/projects\/.+\/terminal/.test(String(url)) ? fakeShell() : new RealWebSocket(url, protocols);
  }, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 }) as unknown as typeof WebSocket;

  /** Prompts `mock$ `, echoes what is typed, answers each line with `you typed: <line>`; `exit` exits with code 0. */
  function fakeShell(): WebSocket {
    let line = '';
    const socket = {
      readyState: 0,
      binaryType: 'blob',
      onopen: null as ((e: Event) => void) | null,
      onmessage: null as ((e: MessageEvent) => void) | null,
      onclose: null as ((e: CloseEvent) => void) | null,
      send(data: string | Uint8Array) {
        if (typeof data === 'string') return; // resize
        for (const ch of new TextDecoder().decode(data)) {
          if (ch === '\x7f') {
            if (line) out('\b \b');
            line = line.slice(0, -1);
          } else if (ch !== '\r') {
            if (ch < ' ') continue; // Esc, Ctrl keys
            line += ch;
            out(ch);
          } else if (line.trim() === 'exit') {
            out('\r\n');
            socket.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ type: 'exit', code: 0 }) }));
            socket.close();
          } else {
            out(`\r\nyou typed: ${line}\r\nmock$ `);
            line = '';
          }
        }
      },
      close() {
        if (socket.readyState === 3) return;
        socket.readyState = 3;
        window.setTimeout(() => socket.onclose?.(new CloseEvent('close')), 0);
      },
    };
    const out = (text: string) => socket.readyState === 1 && socket.onmessage?.(new MessageEvent('message', { data: new TextEncoder().encode(text).buffer }));
    window.setTimeout(() => {
      if (socket.readyState !== 0) return;
      socket.readyState = 1;
      socket.onopen?.(new Event('open'));
      out('mock$ ');
    }, 30);
    return socket as unknown as WebSocket;
  }

  const realFetch = window.fetch.bind(window);
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, window.location.origin);
    if (!url.pathname.startsWith('/api/')) return realFetch(input, init);
    const method = (init?.method ?? 'GET').toUpperCase();
    const body: Json = typeof init?.body === 'string' ? (JSON.parse(init.body) as Json) : {};
    // The auth check gates the whole page and is not what the loading states are for.
    await wait(url.pathname === '/api/auth' ? 60 : 60 + slow);
    // An import reads history on the host: slow enough here to watch "Import all" progress.
    if (/\/previous\/[^/]+\/import$/.test(url.pathname)) await wait(500);
    // A clipped item's whole text is read from the record.
    if (/\/items\/[^/]+$/.test(url.pathname)) await wait(600);
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

  const screenshots = new Map<string, Uint8Array>();
  /** A png the size of a small window, labelled with its path, drawn once per path. */
  function screenshotPng(label: string): Uint8Array {
    let bytes = screenshots.get(label);
    if (bytes) return bytes;
    const canvas = document.createElement('canvas');
    canvas.width = 960;
    canvas.height = 540;
    const c = canvas.getContext('2d')!;
    c.fillStyle = '#1f2430';
    c.fillRect(0, 0, 960, 540);
    c.fillStyle = '#7aa2f7';
    for (let i = 0; i < 12; i++) c.fillRect(40 + i * 76, 420 - (i % 5) * 60, 48, 120 + (i % 5) * 60);
    c.fillStyle = '#e6e6e6';
    c.font = '28px sans-serif';
    c.fillText(label, 40, 60);
    const b64 = canvas.toDataURL('image/png').split(',')[1] ?? '';
    bytes = Uint8Array.from(atob(b64), (ch) => ch.charCodeAt(0));
    screenshots.set(label, bytes);
    return bytes;
  }
  // The seed's stored images (a user's uploads, a tool's results) get bytes, so their thumbnails render.
  for (const t of st.tasks) {
    for (const item of t.items) {
      for (const a of [...(item.attachments ?? []), ...(item.images ?? [])]) {
        if ('id' in a && a.id && a.mime === 'image/png') uploads.set(a.id, { id: a.id, name: a.name ?? a.id, mime: a.mime, size: a.size ?? 0, task: t.id, bytes: screenshotPng(a.name ?? a.id) });
      }
    }
  }

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
    if (svgRoot(text)) return { status: 415, error: 'SVG images cannot be attached' };
    if (b.length > 256 << 10) return { status: 413, error: 'text files can be at most 256 KiB' };
    return { mime: 'text/plain' };
  }

  // Folders for Add project → Browse: a small tree under the mock home. String checks only, no regular expressions.
  const HOME = '/home/user';
  const folders = new Set([
    '/',
    '/etc',
    '/home',
    '/root',
    '/tmp',
    HOME,
    ...st.meta.recent_workdirs,
    `${HOME}/projects`,
    `${HOME}/projects/archive`,
    `${HOME}/.config`,
    `${HOME}/.cache`,
    `${HOME}/projects/unified-agent-manager/.git`,
  ]);
  const gitFolders = new Set(st.meta.recent_workdirs);
  const parentDir = (p: string) => (p === '/' ? undefined : p.slice(0, p.lastIndexOf('/')) || '/');
  const cleanDir = (p: string) => p.split('/').every((s) => s !== '.' && s !== '..') && !p.endsWith('/') && !p.includes('//');

  function listDirs(url: URL): Response {
    const p = url.searchParams.get('path') || HOME;
    if (!p.startsWith('/') || (p !== '/' && !cleanDir(p))) return fail(400, 'path must be an absolute path in clean form');
    if (p === '/root' || p.startsWith('/root/')) return fail(403, 'permission denied');
    if (!folders.has(p)) return fail(404, 'path does not exist');
    const hidden = url.searchParams.get('hidden') === '1';
    const entries = [...folders]
      .filter((d) => d !== '/' && parentDir(d) === p)
      .map((d) => ({ name: d.slice(d.lastIndexOf('/') + 1), path: d }))
      .filter((e) => hidden || !e.name.startsWith('.'))
      .sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase()) || a.name.localeCompare(b.name))
      .map((e) => ({ ...e, git: gitFolders.has(e.path), hidden: e.name.startsWith('.'), link: e.name === 'archive' }));
    return json(200, { path: p, ...(parentDir(p) ? { parent: parentDir(p) } : {}), entries, truncated: false });
  }

  function makeDir(body: Json): Response {
    const parent = String(body.parent ?? '');
    const name = String(body.name ?? '');
    if (!parent) return fail(400, 'parent is required');
    if (!name) return fail(400, 'name is required');
    if (name === '.' || name === '..' || name.includes('/') || name.trim() !== name) return fail(400, 'name must be one plain path element');
    if (!folders.has(parent)) return fail(404, 'parent does not exist');
    if (parent === '/root' || parent === '/etc') return fail(403, 'permission denied');
    const made = parent === '/' ? `/${name}` : `${parent}/${name}`;
    if (folders.has(made)) return fail(409, 'a file or folder with this name already exists');
    folders.add(made);
    return json(201, { path: made });
  }

  function route(method: string, url: URL, body: Json, raw?: Uint8Array): Response {
    const path = url.pathname;
    const m = (re: RegExp) => path.match(re);
    let r: RegExpMatchArray | null;

    if (path === '/api/auth') return json(200, { authenticated: true, required: false });
    if (path === '/api/logout') return json(204);
    if (path === '/api/meta') return json(200, st.meta);
    const accounted = account.route(method, path, body);
    if (accounted) return accounted;
    const planned = plannerKnown ? board.route(method, url, body) : null;
    if (planned) return planned;
    const gitted = git.route(method, url, body);
    if (gitted) return gitted;
    const charted = charts(method, url, body);
    if (charted) return charted;
    const routined = routines.route(method, path, body);
    if (routined) return routined;
    const configured = configuration.route(method, url, body);
    if (configured) return configured;
    const mcped = mcp.route(method, path, body);
    if (mcped) return mcped;
    const assisted = assist(method, url, body);
    if (assisted) return assisted;

    if (path === '/api/settings' && method === 'GET') return json(200, st.settings);
    if (path === '/api/utility' && method === 'GET') return json(200, utilityLog(utility, st.settings.utility_daily_limit ?? 200, Number(url.searchParams.get('before')) || 0, Number(url.searchParams.get('limit')) || 200));
    if (path === '/api/settings/custom-models/discover' && method === 'POST') {
      // The mock serves a fixed list, as an OpenAI-compatible /models would; keys are never set.
      if (!/^UAM_BYOM_[A-Za-z0-9_]+$/.test(String(body.api_key_env ?? ''))) return fail(400, 'API key variable must be named UAM_BYOM_<NAME>');
      return json(200, { models: ['deepseek-v3.1:671b', 'gemma3:27b', 'gpt-oss:120b', 'gpt-oss:20b', 'kimi-k2:1t', 'qwen3-coder:480b', 'qwen3.5:397b'], key_present: true });
    }
    if (path === '/api/settings' && method === 'PATCH') {
      for (const key of Object.keys(body)) if (key !== 'send_default' && key !== 'custom_models' && key !== 'task_defaults' && key !== 'terminal' && key !== 'utility_daily_limit' && key !== 'suggest_replies' && key !== 'compact_threshold' && (key !== 'planner' || !plannerKnown)) return fail(400, `unknown setting "${key}"`);
      if (typeof body.suggest_replies === 'boolean') {
        st.settings = { ...st.settings, suggest_replies: body.suggest_replies };
        broadcast('settings', { settings: st.settings });
      }
      if (body.utility_daily_limit !== undefined) {
        const limit = body.utility_daily_limit;
        if (typeof limit !== 'number' || !Number.isInteger(limit) || limit < 0 || limit > 1000) return fail(400, 'utility_daily_limit must be 0 to 1000');
        st.settings = { ...st.settings, utility_daily_limit: limit };
        broadcast('settings', { settings: st.settings });
      }
      if (body.compact_threshold !== undefined) {
        const t = body.compact_threshold;
        if (t !== null && (typeof t !== 'number' || !Number.isInteger(t) || t < 50 || t > 90)) return fail(400, 'compact_threshold must be 50 to 90');
        const { compact_threshold: _, ...rest } = st.settings;
        st.settings = t === null || t === 80 ? rest : { ...rest, compact_threshold: t };
        broadcast('settings', { settings: st.settings });
      }
      if (body.planner !== undefined) {
        if (typeof body.planner !== 'boolean') return fail(400, 'planner must be true or false');
        st.settings = { ...st.settings, planner: body.planner };
        broadcast('settings', { settings: st.settings });
      }
      if (body.terminal !== undefined) {
        if (typeof body.terminal !== 'boolean') return fail(400, 'terminal must be true or false');
        st.settings = { ...st.settings, terminal: body.terminal };
        broadcast('settings', { settings: st.settings });
      }
      if (body.task_defaults !== undefined) {
        const defaults = checkSelection(body.task_defaults);
        if (typeof defaults === 'string') return fail(400, defaults);
        st.settings = { ...st.settings, task_defaults: defaults };
        broadcast('settings', { settings: st.settings });
      }
      if (Array.isArray(body.custom_models)) {
        // The mock sets no key variables; the service lists the models after Copilot's own.
        const custom = (body.custom_models as CustomModel[]).map((c) => ({ ...c, key_present: false }));
        const copilot = st.meta.providers[0];
        copilot.models = [...copilot.models.filter((mo) => !mo.id.includes('/')), ...custom.map((c) => ({ id: `${c.name}/${c.model_id}`, name: c.display_name || `${c.name}/${c.model_id}`, efforts: [], context_sizes: [], media: { images: false, pdf: false } }))];
        st.settings = { ...st.settings, custom_models: custom.length ? custom : undefined };
        broadcast('settings', { settings: st.settings });
      }
      if (body.send_default !== undefined) {
        if (body.send_default !== 'steer' && body.send_default !== 'queue') return fail(400, 'send_default must be steer or queue');
        st.settings = { ...st.settings, send_default: body.send_default };
        broadcast('settings', { settings: st.settings });
      }
      return json(200, st.settings);
    }
    if (path === '/api/fs/dirs') return method === 'POST' ? makeDir(body) : listDirs(url);
    // Which Task this tab shows: the real service skips pushes for it; the mock sends none.
    if (path === '/api/viewing' && method === 'POST') return json(204);

    if (path === '/api/projects' && method === 'GET') return json(200, { projects: st.projects });
    if (path === '/api/projects' && method === 'POST') {
      const dir = String(body.dir ?? '').trim();
      if (!dir.startsWith('/')) return fail(400, 'dir must be an absolute path to a directory');
      const existing = st.projects.find((p) => p.dir === dir.replace(/\/+$/, ''));
      if (existing) return fail(409, 'that directory already has a project', { project_id: existing.id });
      const name = String(body.name ?? '').trim() || dir.split('/').filter(Boolean).pop() || dir;
      const p: Project = { id: nextId('p'), name, dir, created_at: now(), badge: newBadge(name) };
      st.projects.push(p);
      st.changes[p.id] = [];
      broadcast('project', { project: p });
      return json(201, p);
    }
    if ((r = m(/^\/api\/projects\/([^/]+)$/))) {
      const p = st.projects.find((x) => x.id === decodeURIComponent(r![1]));
      if (!p) return fail(404, 'project not found');
      if (method === 'PATCH') {
        if (body.name === undefined) return fail(400, 'name is required');
        p.name = String(body.name).trim() || p.dir.split('/').filter(Boolean).pop() || p.dir;
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

    // Recorded CLI conversations that are not Tasks yet; importing one makes it a completed Task with its history.
    const notLinked = (p: Project) => (st.previous[p.id] ?? []).filter((s) => !st.tasks.some((t) => t.conversation_id === s.conversation_id));
    if (path === '/api/previous/counts') return json(200, Object.fromEntries(st.projects.map((p) => [p.id, notLinked(p).length])));
    if ((r = m(/^\/api\/projects\/([^/]+)\/previous$/)) && method === 'GET') {
      const p = st.projects.find((x) => x.id === decodeURIComponent(r![1]));
      return p ? json(200, notLinked(p)) : fail(404, 'project not found');
    }
    if ((r = m(/^\/api\/projects\/([^/]+)\/previous\/([^/]+)\/import$/)) && method === 'POST') {
      const p = st.projects.find((x) => x.id === decodeURIComponent(r![1]));
      const s = p && notLinked(p).find((x) => x.conversation_id === decodeURIComponent(r![2]));
      if (!p || !s) return fail(404, 'previous session not found');
      if (s.in_use) return fail(409, 'the conversation is open in another client');
      if (s.conversation_id.endsWith('-broken')) return fail(500, 'the recorded history could not be read');
      const t: MockTask = {
        id: nextId('t'),
        project_id: p.id,
        provider: s.provider,
        name: '',
        title: s.title,
        workdir: p.dir,
        conversation_id: s.conversation_id,
        model: 'auto',
        last_model: '',
        subagents_running: 0,
        state: 'completed',
        open: false,
        pending: 0,
        created_at: s.created_at,
        updated_at: s.updated_at,
        capabilities: st.meta.providers[0].capabilities,
        items: [
          { id: nextId('u'), kind: 'user', text: s.title, time: s.created_at },
          { id: nextId('m'), kind: 'assistant', text: 'Imported from the CLI history: the conversation ended here.', time: s.updated_at },
        ],
        interactions: [],
        subagents: [],
        history_truncated: false,
        last_submission: null,
        agentItems: {},
      };
      st.tasks.push(t);
      broadcast('session', { session: summary(t) });
      return json(201, summary(t));
    }

    if (path === '/api/sessions' && method === 'GET') return json(200, st.tasks.map(summary));
    if (path === '/api/sessions' && method === 'POST') {
      const signedOut = body.provider === 'copilot' && account.refusal();
      if (signedOut) return signedOut;
      // A repeated request_id answers with the Task it created, as the service does.
      const again = typeof body.request_id === 'string' && st.tasks.find((x) => createdBy.get(x.id) === body.request_id);
      if (again) return json(201, summary(again));
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
      if (typeof body.request_id === 'string') createdBy.set(t.id, body.request_id);
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
          if (st.settings.planner) {
            const holds = board.settle(t.id, body);
            if (holds) return holds;
          }
          touch(t, { stage: 'settled', settled_at: now(), state: 'closed', open: false });
          return json(200, summary(t));
        case 'reopen':
          if (stage !== 'settled') return fail(409, `a ${stage} task cannot be reopened`);
          touch(t, { stage: 'active', settled_at: undefined });
          return json(200, summary(t));
        case 'archive':
          if (stage === 'archived') return fail(409, 'the task is already archived');
          if (stage === 'active' && blocked) return fail(409, 'stop the turn, resolve pending requests and clear queued prompts first');
          touch(t, { stage: 'archived', archived_at: now(), state: 'closed', open: false });
          board.ended(t.id);
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
    if ((r = m(/^\/api\/sessions\/([^/]+)\/files\/raw$/)) && method === 'GET') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      const raw = url.searchParams.get('path') ?? '';
      if (!raw) return fail(400, 'path is required');
      // The service resolves the path inside the Task's directory; here a clean prefix check stands in.
      const abs = (raw.startsWith('/') ? raw : `${t.workdir}/${raw}`).replace(/\/\.\//g, '/');
      if (abs.split('/').includes('..') || !abs.startsWith(`${t.workdir}/`) || /missing/.test(abs)) return fail(404, 'file not found');
      if (!/\.png$/i.test(abs)) return fail(415, 'only png, jpeg, gif and webp images are served');
      return new Response(screenshotPng(abs.slice(t.workdir.length + 1)).slice(), { status: 200, headers: { 'Content-Type': 'image/png', 'X-Content-Type-Options': 'nosniff', 'Content-Disposition': 'inline', 'Cache-Control': 'private, no-cache' } });
    }
    // One folder of a Task's directory for the Files panel: folders first, then files.
    if ((r = m(/^\/api\/sessions\/([^/]+)\/files\/tree$/)) && method === 'GET') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      const tree = st.files[t.project_id];
      if (!tree) return json(200, { files: [], reason: 'this directory is not in a Git working tree' });
      const dir = url.searchParams.get('dir') ?? '';
      if (dir && !isDir(t.project_id, dir)) return fail(404, 'folder not found');
      const prefix = dir ? `${dir}/` : '';
      const names = new Set(tree.filter((f) => f.startsWith(prefix)).map((f) => f.slice(prefix.length).split('/')[0]));
      const files = [...names]
        .map((name) => ({ path: prefix + name, type: isDir(t.project_id, prefix + name) ? 'directory' : 'file' }))
        .sort((a, b) => (a.type === b.type ? (a.path < b.path ? -1 : 1) : a.type === 'directory' ? -1 : 1));
      return json(200, { files, reason: '' });
    }
    // The view route for a listed file: text made up from its path, enough for previews.
    if ((r = m(/^\/api\/sessions\/([^/]+)\/files\/view\/(.+)$/)) && (method === 'GET' || method === 'HEAD')) {
      const t = find(decodeURIComponent(r[1]));
      const path = r[2].split('/').map(decodeURIComponent).join('/');
      if (!t || !(st.files[t.project_id] ?? []).includes(path)) return fail(404, 'file not found');
      if (/\.png$/i.test(path)) return new Response(method === 'HEAD' ? null : screenshotPng(path).slice(), { status: 200, headers: { 'Content-Type': 'image/png' } });
      const text = `// ${path}\n${Array.from({ length: 40 }, (_, i) => `const line${i + 1} = ${JSON.stringify(`${path} `.repeat(i % 7 === 3 ? 12 : 1).trim())};`).join('\n')}\n`;
      const body = new TextEncoder().encode(text);
      return new Response(method === 'HEAD' ? null : body, { status: 200, headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Content-Length': String(body.length) } });
    }
    // The `@` listing of a Task's directory, or of a Project's for a new Task that does not exist yet.
    if ((r = m(/^\/api\/(sessions|projects)\/([^/]+)\/files$/)) && method === 'GET') {
      const id = decodeURIComponent(r[2]);
      const projectId = r[1] === 'projects' ? st.projects.find((p) => p.id === id)?.id : find(id)?.project_id;
      if (!projectId) return fail(404, r[1] === 'projects' ? 'project not found' : 'session not found');
      const tree = st.files[projectId];
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
        .map(({ path }) => ({ path, type: isDir(projectId, path) ? 'directory' : 'file' }));
      return json(200, { files, reason: '' });
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/command$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      if (t.stage && t.stage !== 'active') return fail(409, `a ${t.stage} task takes no messages`);
      const name = String(body.name ?? '');
      const offered = st.commands.find((c) => c.name === name);
      if (!offered) return fail(404, `/${name} is not one of this task's commands`);
      if (offered.disabled_reason) return fail(409, offered.disabled_reason);
      // A read-only command answers with native text laid out for a terminal, and starts no turn.
      if (name === 'context') {
        const text = 'Context Usage\n  ○ ○ ○ ◌ ◌ · · · · ·   gpt-6-luna · 17k/128k tokens (13%)\n  · · · · · · · · · ·   ○ System Prompt     9.5k   (7%)\n  · · · · · · · · · ·   · Free Space      104.8k  (82%)';
        const sub: Submission = { request_id: String(body.request_id ?? ''), status: 'accepted', time: now(), command_result: { kind: 'text', text } };
        t.last_submission = sub;
        broadcast('submission', { session_id: t.id, submission: sub }, t.id);
        return json(202, sub);
      }
      // Mode switches apply at once, mid-turn too, and add no transcript line.
      if (name === 'autopilot' || name === 'allow-all') {
        const arg = String(body.arguments ?? '').trim().toLowerCase();
        if (name === 'autopilot') touch(t, { execution: { known: true, mode: arg === 'off' ? 'interactive' : 'autopilot' } });
        else touch(t, { mode: arg === 'on' || (!arg && t.mode !== 'yolo') ? 'yolo' : 'safe' });
        const sub: Submission = { request_id: String(body.request_id ?? ''), status: 'accepted', time: now() };
        t.last_submission = sub;
        broadcast('submission', { session_id: t.id, submission: sub }, t.id);
        return json(202, sub);
      }
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

    // Stopping a background shell: accepted at once, reported as cancelled.
    if ((r = m(/^\/api\/sessions\/([^/]+)\/background-tasks\/([^/]+)\/cancel$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      const id = decodeURIComponent(r[2]);
      if (!t?.background_tasks?.tasks.some((task) => task.id === id)) return fail(404, 'background task not found');
      t.background_tasks = { ...t.background_tasks, tasks: t.background_tasks.tasks.map((task) => (task.id === id ? { ...task, status: 'cancelled', ended_at: new Date().toISOString() } : task)) };
      touch(t, { background_tasks_running: t.background_tasks.tasks.filter((task) => task.status === 'running').length });
      return json(200, { accepted: true, background_tasks: t.background_tasks });
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/(prompt|cancel|close)$/)) && method === 'POST') {
      const signedOut = r[2] === 'prompt' && account.refusal();
      if (signedOut) return signedOut;
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      switch (r[2]) {
        case 'prompt': {
          received.push({ route: 'prompt', session: t.id, body });
          if (t.stage && t.stage !== 'active') return fail(409, `a ${t.stage} task takes no messages`);
          // As the service: blank text beside files or uploads goes as none.
          const text = String(body.text ?? '').trim() ? String(body.text) : '';
          const extras = checkExtras(t, body);
          if (extras instanceof Response) return extras;
          if (!text && !extras.files.length && !extras.attachments.length) return fail(400, 'prompt text, a file or an attachment is required');
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
              // As with Copilot: a receipt until the provider records the message under the same ID.
              const steer: Item = { id: nextId('u'), kind: 'user', delivery: 'steer', text, time: now(), ...withAttachments };
              pushItem(t, { ...steer, steer_status: 'accepted' });
              void wait(2000).then(() => pushItem(t, busy(t) ? steer : { ...steer, steer_status: 'not_delivered' }));
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
      received.push({ route: 'answer', session: t.id, body });
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
      const all = st.changes[t.project_id] ?? [];
      const mine = all.filter((f) => f.by?.includes(t.id));
      const turn = mine.filter((f) => f.turn?.includes(t.id));
      const p = st.projects.find((x) => x.id === t.project_id);
      const scope = url.searchParams.get('scope') ?? 'workspace';
      const files = scope === 'task' ? mine : scope === 'turn' ? turn : all;
      return json(200, {
        scope,
        label: scope === 'workspace' ? `Uncommitted changes in ${p?.name ?? t.workdir}, from any source, versus HEAD` : scope === 'turn' ? 'Files the agent edited in its latest turn, compared with HEAD.' : "Files this task's agent edited, compared with HEAD. Changes made to them by anything else show too.",
        supported: true,
        counts: { task: mine.length, turn: turn.length, workspace: all.length },
        files: files.map(({ path: fp, status, additions, deletions, patch }) => ({ path: fp, status, additions, deletions, digest: `${patch.length}` })),
      });
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/changes\/file$/))) {
      const t = find(decodeURIComponent(r[1]));
      const f = t && (st.changes[t.project_id] ?? []).find((x) => x.path === url.searchParams.get('path'));
      if (!f) return fail(404, 'file not found');
      return json(200, f);
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/history$/)) && method === 'GET') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      return historyPage(t.items, HELD, url);
    }
    // An item's body: whole, a clipped item's from `wholeTexts`.
    if ((r = m(/^\/api\/sessions\/([^/]+)\/items\/([^/]+)$/)) && method === 'GET') {
      const t = find(decodeURIComponent(r[1]));
      const agent = url.searchParams.get('agent_id') ?? '';
      const id = decodeURIComponent(r[2]);
      const found = t && (agent ? t.agentItems[agent] : t.items)?.find((i) => i.id === id);
      if (!t || !found) return fail(404, 'item is no longer retained');
      const { clipped: _c, ...item } = found;
      return json(200, { seq, epoch, session_id: t.id, agent_id: agent, item: id in st.wholeTexts ? { ...item, text: st.wholeTexts[id] } : item });
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/subagents\/([^/]+)\/history$/)) && method === 'GET') {
      const t = find(decodeURIComponent(r[1]));
      const items = t?.agentItems[decodeURIComponent(r[2])];
      if (!t || !items) return fail(404, 'subagent not found');
      return historyPage(items, 0, url);
    }
    // Older subagents from "Copilot's record", one page, only when asked for.
    if ((r = m(/^\/api\/sessions\/([^/]+)\/subagents$/)) && method === 'GET') {
      const t = find(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      if (!url.searchParams.get('before')) return fail(400, 'provide a subagents cursor');
      return json(200, { seq, epoch, representation: 'compact-v1', subagents: t.recordSubagents ?? [], before: '' });
    }
    if ((r = m(/^\/api\/sessions\/([^/]+)\/subagents\/([^/]+)$/))) {
      const t = find(decodeURIComponent(r[1]));
      const s = [...(t?.subagents ?? []), ...(t?.recordSubagents ?? [])].find((x) => x.id === decodeURIComponent(r![2]));
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
    // Stopping one subagent: accepted at once, reported as cancelled.
    if ((r = m(/^\/api\/sessions\/([^/]+)\/subagents\/([^/]+)\/cancel$/)) && method === 'POST') {
      const t = find(decodeURIComponent(r[1]));
      const s = t?.subagents.find((x) => x.id === decodeURIComponent(r![2]));
      if (!t || !s) return fail(404, 'subagent not found');
      if (s.status !== 'running') return fail(409, `the subagent is ${s.status}`);
      setSubagent(t, s.id, 'cancelled');
      return json(200, t.subagents.find((x) => x.id === s.id));
    }
    return fail(404, `mock: no route for ${method} ${path}`);
  }

  function resolve(t: MockTask, i: Interaction, state: Interaction['state'], resolution: string): Interaction {
    const next: Interaction = { ...i, state, resolution };
    t.interactions = t.interactions.map((x) => (x.id === i.id ? next : x));
    broadcast('interaction', { session_id: t.id, interaction: next }, t.id);
    return next;
  }

  return { received };
}

/** Mirrors the server's pendingAsk: the first pending permission, else the first pending question, yolo's own left out. */
function askOf(interactions: readonly Interaction[]): Ask | undefined {
  const waiting = interactions.filter((i) => i.state === 'pending' && !i.auto);
  const i = waiting.find((x) => x.kind === 'permission') ?? waiting[0];
  if (!i) return undefined;
  const line = (text = '') => text.trim().split('\n')[0] ?? '';
  if (i.kind === 'permission') return { kind: i.kind, title: i.title };
  const q = i.questions?.[0];
  return { kind: i.kind, title: line(q?.text) || q?.header || i.title };
}

/** Mirrors the server's svgRoot: after a BOM, whitespace, `<?…?>`, `<!--…-->` and `<!…>`, does the text start with `<svg`? */
function svgRoot(text: string): boolean {
  let rest = text.slice(0, 64 << 10).replace(/^\ufeff/, '');
  for (;;) {
    rest = rest.replace(/^[ \t\r\n]+/, '');
    const end = rest.startsWith('<?') ? '?>' : rest.startsWith('<!--') ? '-->' : rest.startsWith('<!') ? '>' : '';
    if (!end) return rest.slice(0, 4).toLowerCase() === '<svg';
    const i = rest.indexOf(end);
    if (i < 0) return false;
    rest = rest.slice(i + end.length);
  }
}
