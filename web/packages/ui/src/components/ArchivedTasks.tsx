import { Ellipsis } from 'lucide-react';
import { useContext, useState } from 'react';
import { taskName, type SessionSummary } from '../api';
import { matchesTask } from '../lib/tasks';
import { Note, ProjectBadge, TaskTitle, dateTime, timeAgo, useMinuteTick } from './common';
import { TaskList, taskMenuItems, useTaskActions } from './taskActions';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Menu } from './ui/menu';

/** The filter box shows once the list is longer than this. */
const FILTER_FROM = 8;

/**
 * Settings → Archived: the archived Tasks of the instance on screen, every Project, newest archived first. They have no
 * sidebar shelf (its search still finds them). A row opens its Task; its menu is the Task menu's (Delete, Run again, Export).
 */
export function ArchivedTasks() {
  const { sessions, projects } = useContext(TaskList);
  const a = useTaskActions();
  useMinuteTick();
  const [filter, setFilter] = useState('');
  const projectOf = new Map(projects.map((p) => [p.id, p]));
  const archivedAt = (s: SessionSummary) => s.archived_at ?? s.updated_at;
  const archived = sessions.filter((s) => s.stage === 'archived' && projectOf.has(s.project_id)).sort((x, y) => archivedAt(y).localeCompare(archivedAt(x)));
  const shown = filter.trim() ? archived.filter((s) => matchesTask(s, projectOf.get(s.project_id)!, filter)) : archived;
  if (archived.length === 0) return <Note>No archived tasks. A task you archive is listed here, read-only.</Note>;
  return (
    <>
      {archived.length > FILTER_FROM && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <Input type="search" size="md" aria-label="Filter archived tasks" placeholder="Filter by task, project or branch" className="w-64 max-w-full" value={filter} onChange={(e) => setFilter(e.target.value)} />
          <span aria-live="polite" className="text-caption text-muted tabular-nums">{shown.length === archived.length ? `${archived.length} tasks` : `${shown.length} of ${archived.length} tasks`}</span>
        </div>
      )}
      {shown.length > 0 ? (
        <ul aria-label="Archived tasks" className="-mx-2 flex flex-col gap-px">
          {shown.map((s) => {
            const project = projectOf.get(s.project_id)!;
            const at = archivedAt(s);
            return (
              <li key={s.id} className="flex min-w-0 items-center gap-1">
                <button type="button" className="flex min-h-9 min-w-0 flex-1 items-center gap-2 rounded-sm px-2 text-left text-ui text-body transition-colors duration-100 hover:bg-tint-hover focus-visible:-outline-offset-2 pointer-coarse:min-h-11" onClick={() => a.select(s.id)}>
                  <ProjectBadge badge={project.badge} />
                  <span className="sr-only">{project.name}, </span>
                  <TaskTitle session={s} className="min-w-0 flex-1 truncate" />
                  <span aria-hidden="true" className="max-w-48 min-w-0 shrink truncate text-meta text-muted max-sm:hidden">{project.name}</span>
                  <span className="sr-only">, archived </span>
                  <time dateTime={at} title={dateTime(at)} className="shrink-0 text-meta text-muted tabular-nums">{timeAgo(at)}</time>
                </button>
                <Menu.Root modal={false}>
                  <Menu.Trigger render={<Button size="icon" aria-label={`Actions for ${taskName(s) || 'New task'}`} className="shrink-0 text-muted" />}>
                    <Ellipsis />
                  </Menu.Trigger>
                  <Menu.Content align="end">
                    <Menu.Actions items={taskMenuItems(s, a, 'row')} />
                  </Menu.Content>
                </Menu.Root>
              </li>
            );
          })}
        </ul>
      ) : (
        <Note>No archived tasks match “{filter.trim()}”.</Note>
      )}
    </>
  );
}
