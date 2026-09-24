import type { Project, SessionSummary } from '../api';

const readOnly = (s: SessionSummary): boolean => s.stage === 'settled' || s.stage === 'archived';

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
