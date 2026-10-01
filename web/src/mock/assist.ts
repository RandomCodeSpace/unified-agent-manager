// Development-only: the assist routes (suggested replies, Run again, Export as Markdown, saved
// prompts) for the in-browser mock service (install.ts). Not part of the production bundle.

import type { SavedPrompt, SessionSummary, Settings } from '../api';
import type { MockTask } from './data';

type Json = Record<string, unknown>;

/** What the assist routes need from the rest of the mock service. */
export interface AssistHost {
  broadcast: (name: string, payload: Json) => void;
  settings: () => Settings;
  setSettings: (s: Settings) => void;
  task: (id: string) => MockTask | undefined;
  /** The create route, as POST /api/sessions answers; the Task it made is the newest. */
  create: (body: Json) => Response;
  newest: () => MockTask | undefined;
  summary: (t: MockTask) => SessionSummary;
}

function json(status: number, body?: unknown): Response {
  if (body === undefined) return new Response(null, { status });
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}
const fail = (status: number, error: string) => json(status, { error });

/** Replies the mock suggests for a Task's last answer. */
const REPLIES = ['Run the whole test suite', 'Commit this with a short message', 'Show me the diff'];

export function assistMock(host: AssistHost) {
  let counter = 0;
  const savePrompts = (list: SavedPrompt[]) => {
    host.setSettings({ ...host.settings(), saved_prompts: list });
    host.broadcast('settings', { settings: host.settings() });
  };
  return function route(method: string, url: URL, body: Json): Response | null {
    const path = url.pathname;
    let r: RegExpMatchArray | null;
    if ((r = path.match(/^\/api\/sessions\/([^/]+)\/suggestions$/)) && method === 'POST') {
      const t = host.task(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      const last = t.items.filter((i) => !i.agent_id).at(-1);
      const on = host.settings().suggest_replies !== false && t.state === 'completed' && last?.kind === 'assistant';
      return json(200, on ? { item_id: last.id, replies: REPLIES } : { item_id: '', replies: [] });
    }
    if ((r = path.match(/^\/api\/sessions\/([^/]+)\/rerun$/)) && method === 'POST') {
      const t = host.task(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      const text = t.items.filter((i) => !i.agent_id && i.kind === 'user').at(-1)?.text;
      if (!text) return fail(409, 'this task has no message to run again');
      const other = typeof body.model === 'string' && body.model && body.model !== t.model;
      const res = host.create({ project_id: t.project_id, provider: t.provider, model: other ? body.model : t.model, effort: other ? '' : t.effort, context_size: other ? 'default' : t.context_size, mode: t.mode, prompt: text, request_id: body.request_id });
      const made = host.newest();
      if (!res.ok || !made) return res;
      made.rerun_of = t.id;
      host.broadcast('session', { session: host.summary(made) });
      return json(201, host.summary(made));
    }
    if ((r = path.match(/^\/api\/sessions\/([^/]+)\/export$/)) && method === 'GET') {
      const t = host.task(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      const lines = [`# ${t.name || t.title || 'New task'}`, ''];
      for (const it of t.items) {
        if (it.kind === 'user') lines.push(`## You`, '', it.text ?? '', '');
        if (it.kind === 'assistant') lines.push(`## Assistant`, '', it.text ?? '', '');
        if (it.kind === 'tool' && it.tool) lines.push(`<details><summary>${it.tool.title ?? it.tool.name}</summary>`, '', '```text', it.tool.output ?? '', '```', '', '</details>', '');
      }
      return new Response(lines.join('\n'), { status: 200, headers: { 'Content-Type': 'text/markdown; charset=utf-8', 'Content-Disposition': `attachment; filename="${(t.name || 'task').toLowerCase().replace(/[^a-z0-9]+/g, '-')}.md"` } });
    }
    if (path === '/api/prompts' && method === 'POST') {
      const name = String(body.name ?? '').trim();
      const text = String(body.text ?? '');
      if (!name || !text.trim()) return fail(400, 'prompt name and text are required');
      const p: SavedPrompt = { id: `prompt${++counter}`, name, text, created_at: new Date().toISOString(), ...(typeof body.project_id === 'string' && body.project_id ? { project_id: body.project_id } : {}) };
      savePrompts([...(host.settings().saved_prompts ?? []), p]);
      return json(201, p);
    }
    if ((r = path.match(/^\/api\/prompts\/([^/]+)$/))) {
      const id = decodeURIComponent(r[1]);
      const list = host.settings().saved_prompts ?? [];
      const at = list.findIndex((p) => p.id === id);
      if (at < 0) return fail(404, 'prompt not found');
      if (method === 'DELETE') {
        savePrompts(list.filter((p) => p.id !== id));
        return json(204);
      }
      if (method === 'PATCH') {
        const name = String(body.name ?? '').trim();
        if (!name) return fail(400, 'prompt name is required');
        const next = { ...list[at], name };
        savePrompts(list.map((p) => (p.id === id ? next : p)));
        return json(200, next);
      }
    }
    return null;
  };
}

