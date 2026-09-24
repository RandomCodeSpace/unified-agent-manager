import { needsYou, type Project, type SessionSummary } from '../api';
import type { Connection } from '../state';
import { Sep, StateMark, TaskTitle, useApp } from './common';

/** Navigation and project actions shared by the rail and the deck. */
export interface WorkspaceActions {
  onSelect: (id: string) => void;
  onHome: () => void;
  onNewTask: (projectId: string) => void;
  onAddProject: () => void;
  onRenameProject: (p: Project) => void;
  onRemoveProject: (p: Project) => void;
  /** UI-local "Hide" of a project's task list. */
  collapsed: ReadonlySet<string>;
  onToggleProject: (id: string) => void;
}

export function tasksOf(sessions: SessionSummary[], projectId: string): SessionSummary[] {
  return sessions.filter((s) => s.project_id === projectId).sort((a, b) => (a.created_at < b.created_at ? 1 : -1));
}

export const CONNECTION_TEXT: Record<Connection, string> = {
  connecting: 'Connecting…',
  connected: 'Connected',
  reconnecting: 'Connection lost, reconnecting. Work continues on the server.',
  offline: 'Offline, retrying. Work continues on the server.',
};

/** Attention mark for a task row: "Needs you" beats the new-activity dot. */
export function Attention({ session }: { session: SessionSummary }) {
  const { hasNews } = useApp();
  if (needsYou(session)) return <span className="badge-pill badge-attn">Needs you</span>;
  if (hasNews(session)) {
    return (
      <span className="news" title="New activity">
        <span className="sr-only">New activity</span>
      </span>
    );
  }
  return null;
}

export function Rail({
  projects,
  sessions,
  selectedId,
  actions,
  theme,
  onToggleTheme,
  authRequired,
  onLogout,
  connection,
}: {
  projects: Project[];
  sessions: SessionSummary[];
  selectedId: string | null;
  actions: WorkspaceActions;
  theme: 'light' | 'dark';
  onToggleTheme: () => void;
  authRequired: boolean;
  onLogout: () => void;
  connection: Connection;
}) {
  return (
    <nav className="rail" aria-label="Projects">
      <button type="button" className="rail-brand display" onClick={actions.onHome}>
        uam
      </button>
      {projects.length === 0 && <p className="muted small">No projects yet.</p>}
      {projects.map((p) => {
        const tasks = tasksOf(sessions, p.id);
        const hidden = actions.collapsed.has(p.id);
        const attention = tasks.filter(needsYou).length;
        return (
          <section key={p.id} className="rail-section" aria-label={p.name}>
            <button type="button" className="rail-head" aria-expanded={!hidden} onClick={() => actions.onToggleProject(p.id)}>
              <span className="label rail-project">{p.name}</span>
              <span className="rail-count muted">{tasks.length}</span>
              {hidden && attention > 0 && <span className="badge-pill badge-attn">{attention}</span>}
            </button>
            {!hidden && (
              <ul className="rail-items">
                {tasks.map((t) => (
                  <li key={t.id}>
                    <button
                      type="button"
                      className="rail-item"
                      aria-current={t.id === selectedId ? 'true' : undefined}
                      onClick={() => actions.onSelect(t.id)}
                    >
                      <StateMark state={t.state} label={false} />
                      <Sep />
                      <TaskTitle session={t} className="rail-item-name" />
                      <Attention session={t} />
                    </button>
                  </li>
                ))}
                <li>
                  <button type="button" className="rail-item rail-item-new" onClick={() => actions.onNewTask(p.id)}>
                    <span aria-hidden="true">+</span> New task
                  </button>
                </li>
              </ul>
            )}
          </section>
        );
      })}
      <div className="rail-foot">
        <button type="button" className="pill pill-text pill-sm" onClick={actions.onAddProject}>
          + Add project
        </button>
        <div className="row wrap rail-tools">
          <button type="button" className="pill pill-text pill-sm" onClick={onToggleTheme} aria-pressed={theme === 'dark'}>
            {theme === 'dark' ? 'Light' : 'Dark'}
          </button>
          {authRequired && (
            <button type="button" className="pill pill-text pill-sm" onClick={onLogout}>
              Log out
            </button>
          )}
        </div>
        <span className={`conn conn-${connection}`} role="status">
          <span className="mark-dot" aria-hidden="true" />
          {CONNECTION_TEXT[connection]}
        </span>
      </div>
    </nav>
  );
}
