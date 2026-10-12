// Settings → Worktrees in mock mode: the Thread worktrees under ~/.uam/worktrees, the disk, and the removal log.
// `?mock&disk=low` drops free space under the 10% line.
import type { DiskInfo, RemovedWorktree, WorktreeEntry, Worktrees } from '../api';

const GB = 1024 ** 3;
const MB = 1024 ** 2;
const ROOT = '/home/user/.uam/worktrees';
const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();

const row = (project: string, slug: string, size: number, rest: Partial<WorktreeEntry>): WorktreeEntry =>
  ({ path: `${ROOT}/${project}/${slug}`, branch: `uam/${slug}`, size_bytes: size, uncommitted: 0, unpushed: 0, ...rest });

export function worktreesMock() {
  const low = new URLSearchParams(window.location.search).get('disk') === 'low';
  const disk: DiskInfo = low ? { total_bytes: 387 * GB, free_bytes: 31 * GB, low: true } : { total_bytes: 387 * GB, free_bytes: 64 * GB, low: false };
  let rows: WorktreeEntry[] = [
    row('unified-agent-manager', 'fix-reattach-redraw', 1.92 * GB, { project_id: 'p1', task_id: 't1', thread: 'Fix re-attach redraw regression', stage: 'active', uncommitted: 2, unpushed: 3 }),
    row('unified-agent-manager', 'doctor-terminal-line', 1.71 * GB, { project_id: 'p1', task_id: 't3', thread: 'Doctor: add terminal line', stage: 'active' }),
    row('unified-agent-manager', 'pin-actions-node-24', 1.4 * GB, { project_id: 'p1', task_id: 't12', thread: 'Pin GitHub Actions to Node 24', stage: 'archived', uncommitted: 1, unpushed: 2 }),
    row('notes-site', 'a11y-post-template', 412 * MB, { project_id: 'p3', task_id: 't8', thread: 'Accessibility pass on the post template', stage: 'active', unpushed: 1 }),
  ];
  const removed: RemovedWorktree[] = [
    { path: `${ROOT}/unified-agent-manager/drop-vterm-build-tag`, branch: 'uam/drop-vterm-build-tag', project_id: 'p1', thread: 'Drop the vterm build tag from CI', size_bytes: 1.58 * GB, at: ago(140) },
    { path: `${ROOT}/notes-site/rss-footer-link`, branch: 'uam/rss-footer-link', project_id: 'p3', thread: 'Add an RSS link to the footer', size_bytes: 371 * MB, at: ago(60 * 26) },
  ];
  return {
    disk,
    route(method: string, path: string, body: Record<string, unknown>): Response | null {
      if (path === '/api/worktrees' && method === 'GET') {
        const list: Worktrees = { root: ROOT, disk, worktrees: rows, removed };
        return new Response(JSON.stringify(list), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      if (path === '/api/worktrees/remove' && method === 'POST') {
        const w = rows.find((r) => r.path === body.path);
        if (!w) return new Response(JSON.stringify({ error: 'worktree not found' }), { status: 404 });
        if (w.uncommitted && !body.force) {
          return new Response(JSON.stringify({ error: `${w.uncommitted} changed files are not committed and would be lost.`, code: 'worktree_unsaved', refs: [String(w.uncommitted)] }), { status: 409 });
        }
        rows = rows.filter((r) => r !== w);
        disk.free_bytes += w.size_bytes ?? 0;
        return new Response(null, { status: 204 });
      }
      return null;
    },
  };
}
