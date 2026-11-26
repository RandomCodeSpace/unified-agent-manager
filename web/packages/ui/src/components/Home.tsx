import { FolderPlus, SquarePen } from 'lucide-react';
import { Fragment, useMemo, type ReactNode } from 'react';
import { readOnly, type Project, type SessionSummary } from '../api';
import type { Machine } from '../FederationContext';
import { cn } from '../lib/cn';
import { formatCredits } from '../lib/cost';
import { byActivity, needsYouNow, taskNeed, taskStatus, type Unread } from '../lib/tasks';
import { Num, ProjectBadge, Sep, TaskTitle, TONE_TEXT, dateTime, timeAgo, useMinuteTick, type Tone } from './common';
import { Brand } from './Sidebar';
import { Button, rowClass } from './ui/button';
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
  // Active Threads only, newest first; the sidebar lists them all for the instance on screen, so Home keeps what it cannot show.
  const active = useMemo(() => own
    .flatMap(({ machine, projects: projectById, sessions: list }) => list
      .filter((session) => !readOnly(session) && projectById.has(session.project_id))
      .map((session) => ({ machine, session, project: projectById.get(session.project_id)! })))
    .sort((a, b) => byActivity(a.session, b.session)), [own]);
  const needs = active.filter(({ machine, session }) => needsYouNow(session, machine.hasNews));
  const budgets = active.filter(({ session }) => session.budget?.credits || session.budget?.quota_percent);
  const projectCount = own.reduce((n, m) => n + m.projects.size, 0);
  const hasProjects = projectCount > 0;
  const hasTasks = own.some((m) => m.sessions.some((session) => m.projects.has(session.project_id)));
  let introduction = '';
  if (!hasTasks) introduction = projectCount === 1 ? 'Start your first thread in this project.' : 'Choose a project and start your first thread.';
  else if (!needs.length) introduction = 'Nothing needs you.';
  // "Across all instances" only while the rows come from more than one.
  const instances = new Set([...needs, ...budgets].map(({ machine }) => machine.id)).size;
  const where = scope ? `On ${scope}` : machines && instances > 1 ? 'Across all instances' : 'Across all projects';
  const open = (machine: HomeMachine, id: string) => (machines && onOpen ? onOpen(machine.id, id) : onSelect(id));

  return (
    <section aria-label="Home" className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden">
      {/* Fills the pane (no centred column). Needs you is the page; only the first run, with no Project, is a centred launcher. */}
      <div className={cn('w-full px-5 pb-12 min-[960px]:px-10', hasProjects ? 'pt-3 min-[960px]:pt-6' : 'pt-20 min-[960px]:pt-32')}>
        {hasProjects ? (
          <div className="mb-2.5 flex min-h-9 items-center gap-3 px-1 min-[960px]:mb-3.5 min-[960px]:px-2.5">
            <div className="flex min-w-0 flex-1 flex-col min-[960px]:flex-row min-[960px]:items-baseline min-[960px]:gap-3">
              <h1 className="text-display-md text-ink">Needs you{needs.length > 0 && <> <span className="font-medium text-muted tabular-nums">{needs.length}</span></>}</h1>
              <p className="text-caption text-muted">{needs.length > 0 ? where : introduction}</p>
            </div>
            <Button variant="primary" size="lg" disabled={!newTaskReady} onClick={onNewTask}><SquarePen aria-hidden="true" />New thread</Button>
          </div>
        ) : (
          <div className="text-center">
            <Brand className="mb-6 gap-2.5 min-[960px]:mb-7 [&>svg]:size-[34px] max-sm:hidden" />
            <h1 className="text-display-md text-ink">Start with a project</h1>
            <p className="mt-3 text-chat leading-relaxed text-muted">Choose a folder on the machine running UAM.<br />Then start your first thread.</p>
            <Button variant="primary" size="lg" className="mt-6 h-10 gap-2 px-4" onClick={onAddProject}><FolderPlus aria-hidden="true" />Add project</Button>
            <p className="mt-4 text-caption text-muted">Already have a repository? Choose its folder.</p>
          </div>
        )}

        {needs.length > 0 && <NeedsYou rows={needs} named={!!machines} onOpen={open} />}
        {budgets.length > 0 && (
          <section aria-label="Budgets" className="mt-8">
            <div className="mb-1 flex items-center justify-between px-1 min-[960px]:px-2.5">
              <h2 className="text-title font-semibold text-ink">Budgets</h2>
              <p className="text-caption text-muted">{where}</p>
            </div>
            <Rows rows={budgets} named={!!machines} onOpen={open} line={({ session }) => budgetStatus(session.budget!)} />
          </section>
        )}
      </div>
    </section>
  );
}

type Row = { machine: HomeMachine; session: SessionSummary; project: Project };
type Line = { text: ReactNode; tone: Tone };

/** The Needs you groups, in order. The heading says what a row owes, in the attention tone, so the row carries the request alone. */
const GROUPS = [
  { kind: 'answer', label: 'Answers', note: 'The agent asked you something' },
  { kind: 'permission', label: 'Permissions', note: 'Allow or deny one action' },
  // Needs you without a request: an unread error or interruption, or a Thread message held for your OK.
  { kind: 'other', label: 'Other', note: 'Stopped, or holding a message' },
] as const;

/** Needs you, grouped by what you owe, each group in the list's order; a group without rows is not shown. */
function NeedsYou({ rows, named, onOpen }: Readonly<{ rows: Row[]; named: boolean; onOpen: (machine: HomeMachine, id: string) => void }>) {
  return (
    <section aria-label="Needs you">
      {GROUPS.map(({ kind, label, note }) => {
        const owed = rows.filter(({ session }) => (taskNeed(session)?.kind ?? 'other') === kind);
        if (!owed.length) return null;
        return (
          <section key={kind} aria-label={label} className="mt-5.5 first:mt-0">
            <div className="mb-1 flex items-baseline gap-2 px-1 text-caption min-[960px]:px-2.5">
              <h2 className="font-semibold text-attention">{label} <span className="font-medium text-muted tabular-nums">{owed.length}</span></h2>
              <p className="text-muted">{note}</p>
            </div>
            <Rows rows={owed} named={named} onOpen={onOpen} line={needLine} />
          </section>
        );
      })}
    </section>
  );
}

/** What a Needs you row says under its title: the request alone, else (nothing asked) the Thread's status. */
function needLine({ machine, session }: Row): Line {
  const text = taskNeed(session)?.text ?? taskStatus(session, machine.hasNews(session)).text;
  return { text: text.charAt(0).toUpperCase() + text.slice(1), tone: 'muted' };
}

/** A Thread budget in a few words: reached, else what is spent of each limit set. */
function budgetStatus(b: NonNullable<SessionSummary['budget']>): { text: ReactNode; tone: Tone } {
  if (b.reached) return { text: 'Budget reached', tone: 'warning' };
  const parts: ReactNode[] = [];
  if (b.credits) parts.push(<><Num value={b.spent} format={formatCredits} /> of <Num value={b.credits} format={formatCredits} /> AI credits</>);
  if (b.quota_percent) parts.push(<>quota {b.quota_used === undefined ? 'not read yet' : `${formatCredits(b.quota_used)}%`} of {b.quota_percent}%</>);
  return { text: parts.map((part, i) => <Fragment key={i}>{i > 0 && ' · '}{part}</Fragment>), tone: 'muted' };
}

/**
 * A Home list: a Thread per row, opening it. Title and Project on one line, `line` on one line under it, the time
 * opposite; with connected instances each row names its machine. Under 960px nothing hovers, so the Project moves
 * into the second line, which wraps to two.
 */
function Rows({ rows, named, onOpen, line }: Readonly<{ rows: Row[]; named: boolean; onOpen: (machine: HomeMachine, id: string) => void; line: (row: Row) => Line }>) {
  return (
    <ul>
      {rows.map((row) => {
        const { machine, session, project } = row;
        const shown = line(row);
        const place = <><Sep />{project.name}{named && <><Sep /> <InstanceBadge id={machine.id} label={machine.short} /><span className="sr-only">{machine.short}</span></>}</>;
        return (
          <li key={named ? `${machine.id}\n${session.id}` : session.id}>
            <button
              type="button"
              className={cn(rowClass, 'grid grid-cols-[24px_minmax(0,1fr)_auto] items-start gap-x-2.5 gap-y-0.5 px-1 py-3 focus-visible:-outline-offset-2 min-[960px]:grid-cols-[24px_minmax(0,1fr)_58px] min-[960px]:items-center min-[960px]:p-2.5')}
              onClick={() => onOpen(machine, session.id)}
            >
              <ProjectBadge badge={project.badge} className="size-6 text-[9px]" />
              <span className="contents min-[960px]:block min-[960px]:min-w-0">
                <span className="contents min-[960px]:flex min-[960px]:min-w-0 min-[960px]:items-baseline min-[960px]:gap-2.5">
                  <TaskTitle session={session} className="col-start-2 row-start-1 min-w-0 truncate text-ui leading-6 font-medium text-ink min-[960px]:leading-[1.4]" />
                  <span className="hidden min-w-0 shrink-[10] truncate text-meta text-muted min-[960px]:block">{place}</span>
                </span>
                <span title={typeof shown.text === 'string' ? shown.text : undefined} className={cn('col-span-2 col-start-2 row-start-2 line-clamp-2 text-caption min-[960px]:mt-0.5 min-[960px]:block min-[960px]:truncate', TONE_TEXT[shown.tone])}>
                  <span className="text-muted min-[960px]:hidden">{place}<span aria-hidden="true" className="mx-1.5 text-faint">·</span></span>
                  <Sep />{shown.text}
                </span>
              </span>
              <time dateTime={session.updated_at} title={dateTime(session.updated_at)} className="col-start-3 row-start-1 text-right text-meta leading-6 whitespace-nowrap text-muted tabular-nums min-[960px]:leading-[1.3]">
                <Sep />{timeAgo(session.updated_at)}
              </time>
            </button>
          </li>
        );
      })}
    </ul>
  );
}
