// Development-only: the assist routes (suggested replies, Run again, Export as Markdown) for the
// in-browser mock service (install.ts). Not part of the production bundle.

import type { SessionSummary, Settings, TurnEvidence } from '../api';
import type { MockTask } from './data';

type Json = Record<string, unknown>;

/** What the assist routes need from the rest of the mock service. */
export interface AssistHost {
  broadcast: (name: string, payload: Json) => void;
  settings: () => Settings;
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

/**
 * The service's reading of a turn (GET /api/sessions/{id}/evidence): t20's ran checks, t3's last
 * turn only claims; other Tasks show none, so a chat-only answer (t-chart) has no finish card.
 */
const EVIDENCE: Record<string, Omit<TurnEvidence, 'since'>> = {
  t3: {
    checks: [],
    claims: [{ text: 'Covered by TestDoctorDumbTerminal, and the doctor tests pass.', verified: false, detail: 'Not verified · no test run in this turn' }],
    files: [],
  },
  t20: {
    checks: [
      { item_id: 'f5', kinds: ['test'], command: 'go test ./internal/vterm/... -run Redraw', outcome: 'pass', exit: 0, counts: '1 package ok', took_ms: 1400, has_output: true },
      { item_id: 'f6', kinds: ['vet'], command: 'go vet ./internal/vterm/... 2>&1 | tail -5', outcome: 'unclear', exit: 0, note: 'piped, so the exit status is the last command’s', has_output: true },
    ],
    claims: [
      { text: 'TestRedrawReplaysFocusEvents covers it, and the vterm tests pass.', verified: true, detail: 'go test ./internal/vterm/... -run Redraw · exit 0' },
      { text: 'go vet is clean.', verified: false, detail: 'Not verified · the last run’s result is unclear' },
      { text: 'docs/terminal.md describes the new replay order.', verified: false, detail: 'Not verified · no edit to docs/terminal.md in this turn' },
    ],
    files: [{ path: 'internal/vterm/redraw.go', additions: 3, deletions: 0 }, { path: 'internal/vterm/redraw_test.go', additions: 12, deletions: 0 }],
  },
};

/** Replies the mock suggests for a Task's last answer. */
const REPLIES = ['Run the whole test suite', 'Commit this with a short message', 'Show me the diff'];
/** A Task whose suggestion is longer than the empty composer holds. */
const LONG_REPLIES: Record<string, string[]> = {
  t21: ['Upload the .sig files next to each archive in the release job, then add a section to the install docs that explains how to fetch the public key and verify a downloaded archive with cosign before anyone runs it, and link it from the README.'],
};

export function assistMock(host: AssistHost) {
  return function route(method: string, url: URL, body: Json): Response | null {
    const path = url.pathname;
    let r: RegExpMatchArray | null;
    if ((r = path.match(/^\/api\/sessions\/([^/]+)\/suggestions$/)) && method === 'POST') {
      const t = host.task(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      const last = t.items.filter((i) => !i.agent_id).at(-1);
      const on = host.settings().suggest_replies !== false && t.state === 'completed' && last?.kind === 'assistant';
      return json(200, on ? { item_id: last.id, replies: LONG_REPLIES[t.id] ?? REPLIES } : { item_id: '', replies: [] });
    }
    if ((r = path.match(/^\/api\/sessions\/([^/]+)\/evidence$/)) && method === 'GET') {
      const t = host.task(decodeURIComponent(r[1]));
      if (!t) return fail(404, 'session not found');
      const out: TurnEvidence = { ...(EVIDENCE[t.id] ?? { checks: [], claims: [], files: [] }) };
      const since = url.searchParams.get('since'), until = url.searchParams.get('until') ?? new Date().toISOString();
      const fresh = since ? t.items.filter((i) => !i.agent_id && i.time > since && i.time <= until) : [];
      if (fresh.length && t.id === 't20') out.since = { text: 'the agent finished, ran the tests plus 1 other command, and changed 2 files', ids: fresh.map((i) => i.id) };
      return json(200, out);
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
    return null;
  };
}

