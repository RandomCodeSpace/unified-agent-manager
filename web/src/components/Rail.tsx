import { needsYou, readOnly, stageLabel, type Project, type SessionSummary } from '../api';
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

/** Attention mark at the end of a task row: a "Needs you" dot beats the new-activity dot. */
export function Attention({ session }: { session: SessionSummary }) {
  const { hasNews } = useApp();
  if (needsYou(session)) {
    return (
      <span className="dot-attention" title="Needs you">
        <span className="sr-only">Needs you</span>
      </span>
    );
  }
  if (hasNews(session)) {
    return (
      <span className="news" title="New activity">
        <span className="sr-only">New activity</span>
      </span>
    );
  }
  return null;
}

function RailTask({ session, selected, onSelect }: { session: SessionSummary; selected: boolean; onSelect: (id: string) => void }) {
  return (
    <li>
      <button type="button" className="rail-task" aria-current={selected ? 'true' : undefined} onClick={() => onSelect(session.id)}>
        <StateMark state={session.state} label={false} />
        <Sep />
        <TaskTitle session={session} className="rail-item-name" />
        {readOnly(session) && <span className="caption">{stageLabel(session)}</span>}
        <Attention session={session} />
      </button>
    </li>
  );
}

export function Rail({
  projects,
  sessions,
  selectedId,
  actions,
  authRequired,
  onLogout,
  connection,
}: {
  projects: Project[];
  sessions: SessionSummary[];
  selectedId: string | null;
  actions: WorkspaceActions;
  authRequired: boolean;
  onLogout: () => void;
  connection: Connection;
}) {
  const needs = sessions.filter(needsYou).sort((a, b) => (a.updated_at < b.updated_at ? 1 : -1));
  return (
    <nav className="rail" aria-label="Projects">
      <div className="rail-top">
        <button type="button" className="wordmark" onClick={actions.onHome}>
          uam
        </button>
      </div>
      {needs.length > 0 && (
        <section aria-label="Needs you">
          <div className="rail-group-label eyebrow">
            Needs you{' '}
            <span className="count count-attention num" aria-hidden="true">
              · {needs.length}
            </span>
          </div>
          <ul className="rail-tasks">
            {needs.map((t) => (
              <RailTask key={t.id} session={t} selected={t.id === selectedId} onSelect={actions.onSelect} />
            ))}
          </ul>
        </section>
      )}
      <div className="rail-group-label eyebrow">Projects</div>
      {projects.length === 0 && <p className="caption pad">No projects yet.</p>}
      {projects.map((p) => {
        const tasks = tasksOf(sessions, p.id);
        const hidden = actions.collapsed.has(p.id);
        const attention = tasks.filter(needsYou).length;
        return (
          <section key={p.id} className="rail-section" aria-label={p.name}>
            <button type="button" className="rail-project" aria-expanded={!hidden} onClick={() => actions.onToggleProject(p.id)}>
              <span className="chev" aria-hidden="true" />
              <span className="rail-project-name">{p.name}</span>
              <span className="count num">{tasks.length}</span>
              {hidden && attention > 0 && (
                <span className="count count-attention num">
                  {attention}
                  <span className="sr-only"> need you</span>
                </span>
              )}
            </button>
            {!hidden && (
              <ul className="rail-tasks">
                {tasks.map((t) => (
                  <RailTask key={t.id} session={t} selected={t.id === selectedId} onSelect={actions.onSelect} />
                ))}
                <li>
                  <button type="button" className="rail-task rail-task-new" onClick={() => actions.onNewTask(p.id)}>
                    <span aria-hidden="true">+</span> New task
                  </button>
                </li>
              </ul>
            )}
          </section>
        );
      })}
      <div className="rail-footer">
        <div className="rail-actions">
          <button type="button" className="btn btn-ghost btn-sm" onClick={actions.onAddProject}>
            + Add project
          </button>
          {authRequired && (
            <button type="button" className="btn btn-ghost btn-sm" onClick={onLogout}>
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
