import type { Project, SessionSummary } from '../api';

const readOnly = (s: SessionSummary): boolean => s.stage === 'settled' || s.stage === 'archived';

/** The sidebar's "Needs permission/answer" rows (`needsYou` in api.ts, minus the shelves): the count the tab title and app badge carry. */
export function needsYouCount(sessions: readonly SessionSummary[]): number {
  return sessions.filter((s) => !readOnly(s) && (s.state === 'awaiting_permission' || s.state === 'awaiting_answer' || (typeof s.pending === 'number' ? s.pending : s.pending ? 1 : 0) > 0)).length;
}

/** The document title: the needs-you count first, then the open Task's name, then the app. */
export function pageTitle(needsYou: number, taskName: string | null): string {
  const count = needsYou > 0 ? `(${needsYou}) ` : '';
  return taskName === null ? `${count}UAM` : `${count}${taskName || 'New task'} · UAM`;
}

/** A project's Tasks, newest first. */
export function tasksOf(sessions: SessionSummary[], projectId: string): SessionSummary[] {
  return sessions.filter((s) => s.project_id === projectId).sort((a, b) => (a.created_at < b.created_at ? 1 : -1));
}

/** Active Tasks, then the two shelves. */
export function groupTasks(tasks: SessionSummary[]) {
  return {
    active: tasks.filter((t) => !readOnly(t)),
    settled: tasks.filter((t) => t.stage === 'settled'),
    archived: tasks.filter((t) => t.stage === 'archived'),
  };
}

/** The Project a sidebar filter names, while it exists; null means every Project shows (no filter, or a remembered one that was removed). */
export function filteredProject(projects: Project[], filter: string | null): Project | null {
  return (filter && projects.find((p) => p.id === filter)) || null;
}

/** The Projects the sidebar lists: the filtered one alone, else all of them. */
export function visibleProjects(projects: Project[], filter: string | null): Project[] {
  const chosen = filteredProject(projects, filter);
  return chosen ? [chosen] : projects;
}

/** The project New task should target: the open Task's, else the one with the newest activity (a Task's update or the project's creation). */
export function mostRecentProject(projects: Project[], sessions: SessionSummary[], selectedId: string | null): Project | undefined {
  const selected = sessions.find((s) => s.id === selectedId);
  if (selected) return projects.find((p) => p.id === selected.project_id) ?? projects[0];
  let best: Project | undefined;
  let bestAt = '';
  for (const p of projects) {
    const at = sessions.filter((s) => s.project_id === p.id).reduce((m, s) => (s.updated_at > m ? s.updated_at : m), p.created_at);
    if (at > bestAt) {
      best = p;
      bestAt = at;
    }
  }
  return best ?? projects[0];
}

/** Local sidebar search. Every word may match the title, project, directory or branch. */
export function matchesTask(task: SessionSummary, project: Project, query: string): boolean {
  const words = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  const text = [task.name, task.title, project.name, project.dir, project.branch].filter(Boolean).join(' ').toLocaleLowerCase();
  return words.every((word) => text.includes(word));
}

/** One flat Task list across the visible Projects, newest first, including both shelves. */
export function sidebarTasks(projects: Project[], sessions: SessionSummary[], filter: string | null, query = ''): SessionSummary[] {
  const visible = new Map(visibleProjects(projects, filter).map((project) => [project.id, project]));
  return sessions.filter((task) => {
    const project = visible.get(task.project_id);
    return !!project && matchesTask(task, project, query);
  }).sort((a, b) => b.created_at.localeCompare(a.created_at));
}
