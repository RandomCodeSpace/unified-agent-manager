import { useEffect, useRef } from 'react';
import { modelName, needsYou, readOnly, stageLabel, type Project, type SessionSummary } from '../api';
import { STATE_LABELS, Sep, StateMark, TaskTitle, relTime, useApp } from './common';
import { Attention, tasksOf, type WorkspaceActions } from './Rail';

/**
 * Home screen (and the root on narrow screens): the "Needs you" rows first (tasks waiting for
 * input or with new activity), then every project with its tasks. Rows, not cards.
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
  const needs = sessions.filter((s) => !readOnly(s) && (needsYou(s) || hasNews(s))).sort((a, b) => (a.updated_at < b.updated_at ? 1 : -1));
  const attention = needs.filter(needsYou).length;
  const byId = new Map(projects.map((p) => [p.id, p]));

  return (
    <div className="deck">
      <div className="deck-head">
        <h1 className="display-md">
          {needs.length > 0 ? (
            <>
              Needs you
              {attention > 0 && (
                <span className="count count-attention num" aria-label={`${attention} waiting`}>
                  {' '}
                  · {attention}
                </span>
              )}
            </>
          ) : (
            'Projects'
          )}
        </h1>
        <span className="spacer" />
        <button type="button" className="btn btn-ghost btn-sm" onClick={actions.onAddProject}>
          + Add project
        </button>
      </div>
      {needs.length > 0 && (
        <section className="deck-section" aria-label="Needs you">
          <ul className="rows">
            {needs.map((t) => (
              <li key={t.id}>
                <button type="button" className="deck-row" onClick={() => actions.onSelect(t.id)}>
                  <StateMark state={t.state} label={false} />
                  <Sep />
                  <span className="deck-row-main">
                    <TaskTitle session={t} className="deck-row-title" />
                  </span>
                  <span className="deck-row-sub deck-row-project">{byId.get(t.project_id)?.name ?? ''}</span>
                  <span className="deck-row-sub deck-row-state">{needsYou(t) ? STATE_LABELS[t.state] : 'New activity'}</span>
                  <span className="deck-row-time num">{relTime(t.updated_at)}</span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
      {needs.length > 0 && <div className="eyebrow deck-eyebrow">Projects</div>}
      {projects.length === 0 && (
        <section className="deck-section">
          <p className="caption pad">No projects yet. Add a directory on the host to start tasks in it.</p>
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
    <section className="deck-section" aria-labelledby={`project-${p.id}`}>
      <div className="deck-section-head">
        <h2 id={`project-${p.id}`} ref={heading} tabIndex={-1} className="project-name" title={p.name}>
          {p.name}
        </h2>
        <span className="mono caption project-path" title={p.dir}>
          {p.dir}
        </span>
        <span className="spacer" />
        <div className="deck-actions">
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => actions.onNewTask(p.id)}>
            New task
          </button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => actions.onRenameProject(p)}>
            Rename
          </button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => actions.onRemoveProject(p)}>
            Remove
          </button>
          <button type="button" className="btn btn-ghost btn-sm" aria-expanded={!hidden} onClick={() => actions.onToggleProject(p.id)}>
            {hidden ? `Show ${tasks.length}` : 'Hide'}
          </button>
        </div>
      </div>
      {!hidden && (
        <ul className="rows">
          {tasks.map((t) => (
            <li key={t.id}>
              <button type="button" className="deck-row" onClick={() => actions.onSelect(t.id)}>
                <StateMark state={t.state} label={false} />
                <Sep />
                <span className="deck-row-main">
                  <TaskTitle session={t} className="deck-row-title" />
                  <Attention session={t} />
                </span>
                <span className="deck-row-sub deck-row-state">{readOnly(t) ? stageLabel(t) : STATE_LABELS[t.state]}</span>
                <span className="deck-row-sub deck-row-model mono">{modelName(meta, t.provider, t.model)}</span>
                <span className="deck-row-time num">{relTime(t.updated_at)}</span>
              </button>
            </li>
          ))}
          {tasks.length === 0 && <li className="caption pad">No tasks yet.</li>}
        </ul>
      )}
    </section>
  );
}
