// Development-only git routes for the in-browser mock service (see install.ts): the commit
// panel's state, a canned Utility draft, commit, push, pull and set-up. Paths are fictional.

import { LIVE, type GitFile, type GitState, type Project, type SessionSummary } from '../api';
import type { MockChange } from './data';

export interface GitHost {
  projects: () => Project[];
  tasks: () => SessionSummary[];
  task: (id: string) => SessionSummary | undefined;
  changes: () => Record<string, MockChange[]>;
  /** Paths a Task's edit tools touched, as the service reads them from its transcript. */
  touched: (taskId: string) => string[];
  projectChanged: (p: Project) => void;
}

function json(status: number, body?: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}
const fail = (status: number, error: string, code?: string) => json(status, { error, ...(code ? { code } : {}) });

/** Changes no Task in the mock made, so the panel shows files left out. */
const OTHER: Record<string, MockChange[]> = {
  p1: [
    { path: 'web/src/App.tsx', status: 'modified', additions: 4, deletions: 1, patch: '' },
    { path: 'Makefile', status: 'modified', additions: 1, deletions: 0, patch: '' },
  ],
};

/** Files the mock's transcripts do not show a Task editing, as if they did. */
const EDITED: Record<string, string[]> = { t3: ['internal/vterm/redraw_test.go', 'docs/terminal.md'] };

const DRAFT = `fix(vterm): replay focus events on re-attach

Redraw now re-emits ?1004 after the private-mode replay, so a
re-attached client receives focus events again.`;

export function gitMock(host: GitHost) {
  const ahead: Record<string, number> = { p1: 1 };
  const behind: Record<string, number> = { p1: 2 };
  const committed = new Set<string>();

  // `?mock&gitidle` shows the panel as if no Task were mid-turn.
  const idle = new URLSearchParams(window.location.search).has('gitidle');
  const busyIn = (projectId: string) => {
    if (idle) return undefined;
    const running = host.tasks().filter((t) => t.project_id === projectId && LIVE.includes(t.state) && t.state !== 'starting');
    if (running.length === 0) return undefined;
    const name = running[0].name || running[0].title || 'A task';
    return running.length === 1 ? `“${name}” is still working in this repository. Wait for its turn to finish.` : `“${name}” and ${running.length - 1} other ${running.length === 2 ? 'task' : 'tasks'} are still working in this repository. Wait for their turns to finish.`;
  };

  const state = (t: SessionSummary): GitState => {
    const p = host.projects().find((x) => x.id === t.project_id);
    const busy = busyIn(t.project_id);
    if (!p || p.no_git) return { repo: false, reason: 'this directory is not in a Git working tree', can_init: p?.no_git === 'not_repository', ahead: 0, behind: 0, remote: false, has_commits: false, files: [], task_files_known: false, busy };
    const mine = new Set([...host.touched(t.id), ...(EDITED[t.id] ?? [])]);
    const others = new Set(host.tasks().filter((o) => o.id !== t.id).flatMap((o) => [...host.touched(o.id), ...(EDITED[o.id] ?? [])]));
    for (const c of OTHER[p.id] ?? []) others.add(c.path);
    const files: GitFile[] = [...(host.changes()[p.id] ?? []), ...(OTHER[p.id] ?? [])]
      .filter((c) => !committed.has(`${p.id}:${c.path}`))
      .map(({ path, status, additions, deletions }) => ({ path, status, additions, deletions, mine: mine.has(path) || undefined, other_task: (!mine.has(path) && others.has(path)) || undefined }));
    return { repo: true, branch: p.branch, upstream: p.branch ? `origin/${p.branch}` : undefined, ahead: ahead[p.id] ?? 0, behind: behind[p.id] ?? 0, remote: true, has_commits: true, files, task_files_known: true, busy };
  };

  return {
    route(method: string, url: URL, body: Record<string, unknown>): Response | null {
      const r = url.pathname.match(/^\/api\/sessions\/([^/]+)\/git(\/[a-z]+)?$/);
      if (!r) return null;
      const task = host.task(decodeURIComponent(r[1]));
      if (!task) return fail(404, 'session not found');
      const action = r[2] ?? '';
      if (action === '' && method === 'GET') return json(200, state(task));
      if (method !== 'POST') return fail(405, 'method not allowed');
      const st = state(task);
      const paths = Array.isArray(body.paths) ? (body.paths as string[]) : [];
      if (action === '/message') {
        if (paths.length === 0) return fail(400, 'choose at least one file to describe');
        return json(200, { message: DRAFT, conventional: true, model: 'gpt-6-luna' });
      }
      if (st.busy) return fail(409, st.busy, 'git_busy');
      const p = host.projects().find((x) => x.id === task.project_id)!;
      switch (action) {
        case '/init': {
          if (!st.can_init) return fail(409, 'this folder is already in a Git repository');
          const next = { ...p, no_git: undefined, branch: 'main' };
          host.projectChanged(next);
          return json(200, state(task));
        }
        case '/commit': {
          if (!String(body.message ?? '').trim()) return fail(400, 'write a commit message first');
          if (paths.length === 0) return fail(400, 'choose at least one file to commit');
          const bad = paths.find((x) => !st.files.some((f) => f.path === x));
          if (bad) return fail(400, `${bad} is not a changed file in this repository`);
          for (const x of paths) committed.add(`${p.id}:${x}`);
          ahead[p.id] = (ahead[p.id] ?? 0) + 1;
          return json(200, { summary: `Committed ${paths.length} ${paths.length === 1 ? 'file' : 'files'} as 3f9c2e1.`, commit: '3f9c2e1' });
        }
        case '/push':
          if ((behind[p.id] ?? 0) > 0) return fail(409, 'The remote has commits this branch doesn\'t. Pull first, then push:\n ! [rejected]        HEAD -> main (fetch first)');
          ahead[p.id] = 0;
          return json(200, { summary: `Pushed ${p.branch ?? 'main'} to origin.` });
        case '/pull': {
          const n = behind[p.id] ?? 0;
          behind[p.id] = 0;
          return json(200, { summary: n ? `Pulled ${n} new ${n === 1 ? 'commit' : 'commits'}.` : 'Already up to date.' });
        }
      }
      return fail(404, 'mock: no git route');
    },
  };
}
