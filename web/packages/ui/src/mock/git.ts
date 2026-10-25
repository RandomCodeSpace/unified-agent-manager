// Development-only git routes for the in-browser mock service (see install.ts): the repository's
// state, branch switching and set-up. Paths are fictional.

import { LIVE, type GitFile, type GitState, type Project, type SessionSummary } from '../api';
import type { MockChange } from './data';

export interface GitHost {
  projects: () => Project[];
  tasks: () => SessionSummary[];
  task: (id: string) => SessionSummary | undefined;
  changes: () => Record<string, MockChange[]>;
  /** Paths a Task's edit tools touched, as the service records them. */
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

const hoursAgo = (h: number) => new Date(Date.now() - h * 3600_000).toISOString();

/** A Project's local and remote branches, newest work first within each. */
function branchesOf(p: Project) {
  const local = [p.branch ?? 'main', 'main', 'feat/settings-rail', 'fix/vterm-focus-replay', 'uam/receipt-stamps'];
  const remote = ['origin/main', 'origin/release/1.4', 'origin/release/1.3', 'origin/feat/settings-rail', 'origin/renovate/vite-8.x'];
  return [
    ...[...new Set(local)].map((name, i) => ({ name, remote: false, updated_at: hoursAgo(i * 30) })),
    ...remote.map((name, i) => ({ name, remote: true, updated_at: hoursAgo(2 + i * 40) })),
  ];
}

const slug = (text: string) => text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 48) || '7f3a9c21';

export function gitMock(host: GitHost) {
  const ahead: Record<string, number> = { p1: 1 };
  const behind: Record<string, number> = { p1: 2 };
  /** The changed files of a Project's folder not committed yet. */
  const pendingIn = (projectId: string) => [...(host.changes()[projectId] ?? []), ...(OTHER[projectId] ?? [])].length;

  // `?mock&gitidle` shows the panel as if no Task were mid-turn.
  const idle = new URLSearchParams(window.location.search).has('gitidle');
  const busyIn = (projectId: string) => {
    if (idle) return undefined;
    const running = host.tasks().filter((t) => t.project_id === projectId && LIVE.includes(t.state));
    if (running.length === 0) return undefined;
    const name = running[0].name || running[0].title || 'A thread';
    return running.length === 1 ? `“${name}” is still working in this repository. Wait for it to finish.` : `“${name}” and ${running.length - 1} other ${running.length === 2 ? 'thread' : 'threads'} are still working in this repository. Wait for them to finish.`;
  };

  const state = (t: SessionSummary): GitState => {
    const p = host.projects().find((x) => x.id === t.project_id);
    const busy = busyIn(t.project_id);
    if (!p || p.no_git) return { repo: false, reason: 'this directory is not in a Git working tree', can_init: p?.no_git === 'not_repository', ahead: 0, behind: 0, remote: false, has_commits: false, files: [], task_files_known: false, busy };
    const mine = new Set([...host.touched(t.id), ...(EDITED[t.id] ?? [])]);
    const others = new Set(host.tasks().filter((o) => o.id !== t.id).flatMap((o) => [...host.touched(o.id), ...(EDITED[o.id] ?? [])]));
    for (const c of OTHER[p.id] ?? []) others.add(c.path);
    const files: GitFile[] = [...(host.changes()[p.id] ?? []), ...(OTHER[p.id] ?? [])]
      .map(({ path, status, additions, deletions }) => ({ path, status, additions, deletions, mine: mine.has(path) || undefined, other_task: (!mine.has(path) && others.has(path)) || undefined }));
    return { repo: true, branch: p.branch, upstream: p.branch ? `origin/${p.branch}` : undefined, ahead: ahead[p.id] ?? 0, behind: behind[p.id] ?? 0, remote: true, has_commits: true, files, task_files_known: true, busy };
  };

  return {
    route(method: string, url: URL, body: Record<string, unknown>): Response | null {
      const b = url.pathname.match(/^\/api\/projects\/([^/]+)\/branches$/);
      if (b && method === 'GET') {
        const p = host.projects().find((x) => x.id === decodeURIComponent(b[1]));
        if (!p) return fail(404, 'project not found');
        if (p.no_git) return fail(409, 'this folder is not in a Git repository');
        const working = host.tasks().find((t) => t.project_id === p.id && t.workdir === p.dir && LIVE.includes(t.state) && (t.stage ?? 'active') === 'active');
        return json(200, { branches: branchesOf(p), uncommitted: pendingIn(p.id), ...(working ? { busy: { id: working.id, name: working.name || working.title || 'A Thread' } } : {}) });
      }
      const r = url.pathname.match(/^\/api\/sessions\/([^/]+)\/git(\/[a-z]+)?$/);
      if (!r) return null;
      const task = host.task(decodeURIComponent(r[1]));
      if (!task) return fail(404, 'session not found');
      const action = r[2] ?? '';
      if (action === '' && method === 'GET') return json(200, state(task));
      if (method !== 'POST') return fail(405, 'method not allowed');
      const st = state(task);
      const p = host.projects().find((x) => x.id === task.project_id)!;
      // A started Thread switches its Project folder's branch: the folder's Threads must have finished, and an
      // existing branch needs its changes committed.
      if (action === '/branch') {
        if (task.worktree) return fail(409, 'a Thread in its own worktree keeps its branch');
        const working = host.tasks().find((t) => t.project_id === p.id && t.workdir === p.dir && LIVE.includes(t.state) && (t.stage ?? 'active') === 'active');
        if (working) return fail(409, `“${working.name || working.title || 'A Thread'}” is working in this folder. Wait for it to finish.`, 'branch_busy');
        const n = pendingIn(p.id);
        if (body.mode === 'switch' && n) return fail(409, `${n} ${n === 1 ? 'file' : 'files'} not committed`, 'branch_dirty');
        let branch = String(body.branch ?? '');
        if (body.mode === 'branch') branch = `uam/${slug(task.name || task.title)}`;
        else if (body.remote) branch = branch.replace(/^[^/]+\//, '');
        host.projectChanged({ ...p, branch });
        return json(200, task);
      }
      if (st.busy) return fail(409, st.busy, 'git_busy');
      switch (action) {
        case '/init': {
          if (!st.can_init) return fail(409, 'this folder is already in a Git repository');
          const next = { ...p, no_git: undefined, branch: 'main' };
          host.projectChanged(next);
          return json(200, state(task));
        }
      }
      return fail(404, 'mock: no git route');
    },
  };
}
