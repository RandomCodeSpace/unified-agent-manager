import { useEffect, useRef } from 'react';
import { modelName, needsYou, taskName, type Project, type SessionSummary } from '../api';
import { STATE_LABELS, Sep, StateMark, TaskTitle, relTime, useApp } from './common';
import { Attention, tasksOf, type WorkspaceActions } from './Rail';

/**
 * Home screen (and the root on narrow screens): a "Needs you" deck of tasks waiting for
 * input or with new activity, then every project with its tasks.
 */
export function Deck({
  projects,
  sessions,
  actions,
  highlightId,
}: {
  projects: Project[];
  sessions: SessionSummary[];
  actions: WorkspaceActions;
  /** Project heading to scroll to and focus (after adding a directory that already had one). */
  highlightId: string | null;
}) {
  const { hasNews } = useApp();
  const needs = sessions.filter((s) => needsYou(s) || hasNews(s)).sort((a, b) => (a.updated_at < b.updated_at ? 1 : -1));
  const byId = new Map(projects.map((p) => [p.id, p]));

  return (
    <div className="deck">
      <span className="orb orb-mint" aria-hidden="true" />
      <span className="orb orb-lavender" aria-hidden="true" />
      <div className="deck-head">
        <h1 className="display deck-title">Projects</h1>
        <span className="spacer" />
        <button type="button" className="pill pill-outline pill-sm" onClick={actions.onAddProject}>
          + Add project
        </button>
      </div>
      {needs.length > 0 && (
        <section className="needs" aria-label="Needs you">
          <div className="label">Needs you</div>
          <ul className="rows">
            {needs.map((t) => (
              <li key={t.id}>
                <button type="button" className="row-btn" onClick={() => actions.onSelect(t.id)}>
                  <StateMark state={t.state} label={false} />
                  <Sep />
                  <span className="row-main">
                    <span className="row-name">{taskName(t) || 'Untitled task'}</span>
                    <span className="row-sub">{needsYou(t) ? STATE_LABELS[t.state] : 'New activity'}</span>
                  </span>
                  <span className="row-col">{byId.get(t.project_id)?.name ?? ''}</span>
                  <span className="row-col">{needsYou(t) ? '' : 'New activity'}</span>
                  <span className="row-time muted">{relTime(t.updated_at)}</span>
                  <span className="row-badge">
                    <Attention session={t} />
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
      {projects.length === 0 && (
        <section className="project">
          <h2 className="display project-name">No projects yet</h2>
          <p className="muted">Add a directory on the host to start tasks in it.</p>
        </section>
      )}
      {projects.map((p) => (
        <ProjectSection key={p.id} project={p} tasks={tasksOf(sessions, p.id)} actions={actions} highlight={p.id === highlightId} />
      ))}
    </div>
  );
}

function ProjectSection({
  project: p,
  tasks,
  actions,
  highlight,
}: {
  project: Project;
  tasks: SessionSummary[];
  actions: WorkspaceActions;
  highlight: boolean;
}) {
  const { meta } = useApp();
  const heading = useRef<HTMLHeadingElement>(null);
  const hidden = actions.collapsed.has(p.id);

  useEffect(() => {
    if (!highlight) return;
    heading.current?.scrollIntoView({ block: 'center' });
    heading.current?.focus();
  }, [highlight]);

  return (
    <section className="project" aria-labelledby={`project-${p.id}`}>
      <div className="project-head">
        <h2 id={`project-${p.id}`} ref={heading} tabIndex={-1} className="display project-name">
          {p.name}
        </h2>
        <span className="mono project-path" title={p.dir}>
          {p.dir}
        </span>
        <span className="spacer" />
        <button type="button" className="pill pill-primary pill-sm" onClick={() => actions.onNewTask(p.id)}>
          New task
        </button>
        <button type="button" className="pill pill-text pill-sm" onClick={() => actions.onRenameProject(p)}>
          Rename
        </button>
        <button type="button" className="pill pill-text pill-sm" onClick={() => actions.onRemoveProject(p)}>
          Remove
        </button>
        <button type="button" className="pill pill-text pill-sm" aria-expanded={!hidden} onClick={() => actions.onToggleProject(p.id)}>
          {hidden ? `Show ${tasks.length}` : 'Hide'}
        </button>
      </div>
      {!hidden && (
        <ul className="rows">
          {tasks.map((t) => (
            <li key={t.id}>
              <button type="button" className="row-btn" onClick={() => actions.onSelect(t.id)}>
                <StateMark state={t.state} label={false} />
                <Sep />
                <span className="row-main">
                  <TaskTitle session={t} className="row-name" />
                  <span className="row-sub">
                    {STATE_LABELS[t.state]} · {modelName(meta, t.provider, t.model)}
                  </span>
                </span>
                <span className="row-col">{STATE_LABELS[t.state]}</span>
                <span className="row-col">{modelName(meta, t.provider, t.model)}</span>
                <span className="row-time muted">{relTime(t.updated_at)}</span>
                <span className="row-badge">
                  <Attention session={t} />
                </span>
              </button>
            </li>
          ))}
          {tasks.length === 0 && <li className="muted small pad">No tasks yet.</li>}
        </ul>
      )}
    </section>
  );
}
