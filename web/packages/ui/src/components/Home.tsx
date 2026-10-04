import { FolderPlus, SquarePen } from 'lucide-react';
import { useMemo } from 'react';
import type { Project, SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { taskStatus, type Unread } from '../lib/tasks';
import { ProjectBadge, Sep, TaskTitle, TONE_TEXT, relTime, useMinuteTick } from './common';
import { Brand } from './Sidebar';
import { Button } from './ui/button';

export function Home({ projects, sessions, hasNews, newTaskReady = true, onNewTask, onAddProject, onSelect }: Readonly<{
  projects: Project[];
  sessions: SessionSummary[];
  hasNews: Unread;
  newTaskReady?: boolean;
  onNewTask: () => void;
  onAddProject: () => void;
  onSelect: (id: string) => void;
}>) {
  useMinuteTick();
  const projectById = useMemo(() => new Map(projects.map((project) => [project.id, project])), [projects]);
  const recent = useMemo(() => sessions
    .filter((session) => session.stage !== 'archived' && projectById.has(session.project_id))
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at) || b.created_at.localeCompare(a.created_at))
    .slice(0, 6), [sessions, projectById]);
  const hasProjects = projects.length > 0;
  const hasTasks = sessions.some((session) => projectById.has(session.project_id));
  let introduction = 'Start a new task, or pick up where you left off.';
  if (!hasTasks) introduction = projects.length === 1 ? 'Start your first task in this project.' : 'Choose a project and start your first task.';
  else if (!recent.length) introduction = 'Start a new task.';

  return (
    <section aria-label="Home" className="min-h-0 flex-1 overflow-y-auto">
      <div className={cn('mx-auto w-full max-w-[790px] px-5 pb-12 min-[960px]:px-8', hasProjects ? 'pt-10 min-[960px]:pt-16' : 'pt-20 min-[960px]:pt-32')}>
        <div className="text-center">
          <Brand className="mb-6 gap-2.5 min-[960px]:mb-7 [&>svg]:size-[34px] [&>span]:text-lg" />
          <h1 className="text-[25px] leading-tight font-semibold tracking-[-0.5px] text-ink min-[960px]:text-[28px]">
            {hasProjects ? 'What are you working on?' : 'Start with a project'}
          </h1>
          <p className="mt-3 text-chat leading-relaxed text-muted">
            {hasProjects ? introduction : <>Choose a folder on the machine running UAM.<br />Then start your first task.</>}
          </p>
          <Button variant="primary" size="lg" className="mt-6 h-10 gap-2 px-4" disabled={hasProjects && !newTaskReady} onClick={hasProjects ? onNewTask : onAddProject}>
            {hasProjects ? <SquarePen aria-hidden="true" /> : <FolderPlus aria-hidden="true" />}
            {hasProjects ? 'New task' : 'Add project'}
          </Button>
          {!hasProjects && <p className="mt-4 text-caption text-muted">Already have a repository? Choose its folder.</p>}
        </div>

        {recent.length > 0 && (
          <section aria-label="Recent tasks" className="mt-11 min-[960px]:mt-14">
            <div className="mb-3.5 flex items-center justify-between px-1 min-[960px]:px-2.5">
              <h2 className="text-title font-semibold text-ink">Recent tasks</h2>
              <span className="text-caption text-muted">All projects</span>
            </div>
            <ul>
              {recent.map((session) => {
                const project = projectById.get(session.project_id)!;
                const status = taskStatus(session, hasNews(session));
                const updated = relTime(session.updated_at);
                return (
                  <li key={session.id}>
                    <button
                      type="button"
                      className="grid w-full grid-cols-[24px_minmax(0,1fr)_58px] items-center gap-x-2.5 gap-y-1 rounded-sm px-1 py-4 text-left transition-colors hover:bg-sunken focus-visible:-outline-offset-2 min-[960px]:grid-cols-[24px_minmax(0,1fr)_minmax(0,200px)_58px] min-[960px]:px-2.5"
                      onClick={() => onSelect(session.id)}
                    >
                      <ProjectBadge badge={project.badge} className="size-6 text-[9px]" />
                      <span className="min-w-0">
                        <TaskTitle session={session} className="block truncate text-ui font-medium text-ink" />
                        <Sep />
                        <span className="mt-1 block truncate text-meta text-muted">{project.name}</span>
                      </span>
                      <span className={cn('col-span-2 col-start-2 row-start-2 min-w-0 break-words text-meta min-[960px]:col-span-1 min-[960px]:col-start-3 min-[960px]:row-start-1', TONE_TEXT[status.tone])}>
                        <Sep />{status.text}
                      </span>
                      <time dateTime={session.updated_at} title={new Date(session.updated_at).toLocaleString()} className="col-start-3 row-start-1 text-right text-meta text-muted tabular-nums min-[960px]:col-start-4">
                        <Sep />{updated === 'now' ? 'now' : `${updated} ago`}
                      </time>
                    </button>
                  </li>
                );
              })}
            </ul>
          </section>
        )}
      </div>
    </section>
  );
}
