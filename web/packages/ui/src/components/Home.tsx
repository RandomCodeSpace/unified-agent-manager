import { FolderPlus, SquarePen } from 'lucide-react';
import { useMemo } from 'react';
import type { Project, SessionSummary } from '../api';
import type { Machine } from '../FederationContext';
import { cn } from '../lib/cn';
import { taskStatus, type Unread } from '../lib/tasks';
import { ProjectBadge, Sep, TaskTitle, TONE_TEXT, dateTime, relTime, useMinuteTick } from './common';
import { Brand } from './Sidebar';
import { Button } from './ui/button';
import { InstanceBadge } from './InstanceBadge';

/** What Home reads of a machine: without connected instances, this one alone. */
type HomeMachine = Pick<Machine, 'id' | 'short' | 'hasNews'> & { state: { projects: Project[]; sessions: SessionSummary[] } };

export function Home({ projects, sessions, hasNews, newTaskReady = true, onNewTask, onAddProject, onSelect, machines, scope, onOpen }: Readonly<{
  projects: Project[];
  sessions: SessionSummary[];
  hasNews: Unread;
  newTaskReady?: boolean;
  onNewTask: () => void;
  onAddProject: () => void;
  onSelect: (id: string) => void;
  /** With connected instances: every machine, the one on screen with its live state; the lists then span them, each row naming its machine. */
  machines?: Machine[];
  /** The instance the Task list is scoped to, whose Tasks `machines` holds alone. */
  scope?: string;
  onOpen?: (machine: string, id: string) => void;
}>) {
  useMinuteTick();
  const own = useMemo(() => (machines ?? [{ id: '', short: '', state: { projects, sessions }, hasNews } satisfies HomeMachine] as HomeMachine[]).map((m) => ({ machine: m, projects: new Map(m.state.projects.map((project) => [project.id, project])), sessions: m.state.sessions })), [machines, projects, sessions, hasNews]);
  const recent = useMemo(() => own
    .flatMap(({ machine, projects: projectById, sessions: list }) => list
      .filter((session) => session.stage !== 'archived' && projectById.has(session.project_id))
      .map((session) => ({ machine, session, project: projectById.get(session.project_id)! })))
    .sort((a, b) => b.session.updated_at.localeCompare(a.session.updated_at) || b.session.created_at.localeCompare(a.session.created_at))
    .slice(0, 6), [own]);
  const projectCount = own.reduce((n, m) => n + m.projects.size, 0);
  const hasProjects = projectCount > 0;
  const hasTasks = own.some((m) => m.sessions.some((session) => m.projects.has(session.project_id)));
  let introduction = '';
  if (!hasTasks) introduction = projectCount === 1 ? 'Start your first thread in this project.' : 'Choose a project and start your first thread.';
  else if (!recent.length) introduction = 'Start a new thread.';
  // "Across all instances" only while the recent Tasks come from more than one.
  const instances = new Set(recent.map(({ machine }) => machine.id)).size;

  return (
    <section aria-label="Home" className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden">
      {/* Fills the pane (no centred column); the launcher's heading stays centred above the full-width list. */}
      <div className={cn('w-full px-5 pb-12 min-[960px]:px-10', hasProjects ? 'pt-10 min-[960px]:pt-16' : 'pt-20 min-[960px]:pt-32')}>
        <div className="text-center">
          <Brand className="mb-6 gap-2.5 min-[960px]:mb-7 [&>svg]:size-[34px] [&>span]:text-lg max-sm:hidden" />
          <h1 className="text-[25px] leading-tight font-semibold tracking-[-0.5px] text-ink min-[960px]:text-[28px]">
            {hasProjects ? 'What are you working on?' : 'Start with a project'}
          </h1>
          {(!hasProjects || introduction) && <p className="mt-3 text-chat leading-relaxed text-muted">
            {hasProjects ? introduction : <>Choose a folder on the machine running UAM.<br />Then start your first thread.</>}
          </p>}
          <Button variant="primary" size="lg" className="mt-6 h-10 gap-2 px-4" disabled={hasProjects && !newTaskReady} onClick={hasProjects ? onNewTask : onAddProject}>
            {hasProjects ? <SquarePen aria-hidden="true" /> : <FolderPlus aria-hidden="true" />}
            {hasProjects ? 'New thread' : 'Add project'}
          </Button>
          {!hasProjects && <p className="mt-4 text-caption text-muted">Already have a repository? Choose its folder.</p>}
        </div>

        {recent.length > 0 && (
          <section aria-label="Recent threads" className="mt-11 min-[960px]:mt-14">
            <div className="mb-3.5 flex items-center justify-between px-1 min-[960px]:px-2.5">
              <h2 className="text-title font-semibold text-ink">Recent threads</h2>
              <p className="text-caption text-muted">{scope ? `On ${scope}` : machines && instances > 1 ? 'Across all instances' : 'Across all projects'}</p>
            </div>
            <ul>
              {recent.map(({ machine, session, project }) => {
                const status = taskStatus(session, machine.hasNews(session));
                const updated = relTime(session.updated_at);
                return (
                  <li key={machines ? `${machine.id}\n${session.id}` : session.id}>
                    <button
                      type="button"
                      className="grid w-full grid-cols-[24px_minmax(0,1fr)_58px] items-center gap-x-2.5 gap-y-1 rounded-sm px-1 py-4 text-left transition-colors hover:bg-sunken focus-visible:-outline-offset-2 min-[960px]:grid-cols-[24px_minmax(0,1fr)_minmax(0,200px)_58px] min-[960px]:px-2.5"
                      onClick={() => (machines && onOpen ? onOpen(machine.id, session.id) : onSelect(session.id))}
                    >
                      <ProjectBadge badge={project.badge} className="size-6 text-[9px]" />
                      <span className="min-w-0">
                        <TaskTitle session={session} className="block truncate text-ui font-medium text-ink" />
                        <Sep />
                        <span className="mt-1 flex min-w-0 items-center gap-1.5 text-meta text-muted"><span className="truncate">{project.name}</span>{machines && <><Sep /><InstanceBadge id={machine.id} label={machine.short} /><span className="sr-only">{machine.short}</span></>}</span>
                      </span>
                      <span className={cn('col-span-2 col-start-2 row-start-2 min-w-0 break-words text-meta min-[960px]:col-span-1 min-[960px]:col-start-3 min-[960px]:row-start-1', TONE_TEXT[status.tone])}>
                        <Sep />{status.text}
                      </span>
                      <time dateTime={session.updated_at} title={dateTime(session.updated_at)} className="col-start-3 row-start-1 text-right text-meta text-muted tabular-nums min-[960px]:col-start-4">
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
