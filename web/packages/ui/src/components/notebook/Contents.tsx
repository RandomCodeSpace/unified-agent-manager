import { List, SquarePen } from 'lucide-react';
import { memo, useMemo, useState, type ComponentProps } from 'react';
import { readOnly, type Project, type SessionDetail, type SessionSummary } from '../../api';
import { byActivity, needsYouNow, shownState, type Unread } from '../../lib/tasks';
import { turnList, type Turn } from '../../lib/turns';
import { useMinuteTick } from '../common';
import { Sidebar, useAnyProject } from '../Sidebar';
import { Button } from '../ui/button';
import { ThreadCard } from './ThreadCard';
import { TurnList } from './TurnList';

const HEADING = 'px-2 pt-2 pb-1 text-eyebrow text-muted uppercase';

// "All Threads" and "Back to this Thread" replace each other: focus follows to the one now on screen.
const ALL_ID = 'contents-all';
const BACK_ID = 'contents-back';
const focusNext = (id: string) => requestAnimationFrame(() => document.getElementById(id)?.focus());

/** Another Thread shows under "Also running" while it works, waits on you, or finished and was not read. */
function running(s: SessionSummary, unread: Unread): boolean {
  if (readOnly(s)) return false;
  const state = shownState(s);
  return needsYouNow(s, unread) || state === 'working' || state === 'starting' || (state === 'completed' && unread(s));
}

/** The two sections and the two buttons that stand where the sidebar's list does. */
const Sections = memo(function Sections({ turns, projects, sessions, selectedId, hasNews, onAll, onNewThread }: Readonly<{ turns: readonly Turn[] | null; projects: Project[]; sessions: SessionSummary[]; selectedId: string; hasNews: Unread; onAll: () => void; onNewThread: () => void }>) {
  const minute = useMinuteTick();
  const anyProject = useAnyProject(projects);
  const others = useMemo(() => {
    const projectMap = new Map(projects.map((project) => [project.id, project]));
    // Those that wait on you first, then the latest activity, as the sidebar orders its list.
    return sessions
      .filter((s) => s.id !== selectedId && projectMap.has(s.project_id) && running(s, hasNews))
      .sort((a, b) => Number(needsYouNow(b, hasNews)) - Number(needsYouNow(a, hasNews)) || byActivity(a, b))
      .map((session) => ({ session, project: projectMap.get(session.project_id)! }));
  }, [projects, sessions, selectedId, hasNews]);
  return (
    <div className="flex min-h-0 flex-1 flex-col px-2 pb-3">
      {turns && (
        // With cards under it the list keeps to its share of the column and scrolls inside it.
        <section aria-label="This Thread" className={others.length ? 'flex max-h-2/5 min-h-0 shrink-0 flex-col' : 'flex min-h-0 flex-col'}>
          <h2 className={HEADING}>This Thread</h2>
          <TurnList turns={turns} />
        </section>
      )}
      {others.length > 0 && (
        <section aria-label="Also running" className="flex min-h-0 flex-col">
          <h2 className={HEADING}>Also running</h2>
          <ul className="-mx-1 flex min-h-0 flex-col gap-2 overflow-y-auto overscroll-contain px-1 pb-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            {others.map(({ session, project }) => <ThreadCard key={session.id} session={session} project={project} unread={hasNews(session)} minute={minute} />)}
          </ul>
        </section>
      )}
      <div className="flex shrink-0 gap-2 pt-2">
        <Button id={ALL_ID} variant="secondary" className="flex-1 bg-raised shadow-raised" onClick={onAll}>
          <List aria-hidden="true" />
          All Threads
        </Button>
        {anyProject && (
          <Button variant="primary" className="flex-1" onClick={onNewThread}>
            <SquarePen aria-hidden="true" />
            New thread
          </Button>
        )}
      </div>
    </div>
  );
});

/**
 * The notebook layout's left column (lib/layout): the sidebar itself, with the open Thread's turns
 * and the other active Threads where its list stands. "All Threads" brings the list back, with all
 * the sidebar has; a search always lists its matches. Without an open Thread it is the sidebar.
 */
export default function Contents({ detail, ...sidebar }: Readonly<ComponentProps<typeof Sidebar> & { /** The Thread on screen, or null. */ detail: SessionDetail | null }>) {
  const { projects, sessions, selectedId, hasNews, actions } = sidebar;
  const [all, setAll] = useState(false);
  // Opening a Thread from the list shows that Thread's contents.
  const [seen, setSeen] = useState(selectedId);
  if (seen !== selectedId) {
    setSeen(selectedId);
    setAll(false);
  }
  // The detail changes with every streamed token, its turns only with a message or a turn's end: the
  // list is kept until then, so the column below does not render with the stream.
  const turnsNow = JSON.stringify(detail ? turnList(detail) : null);
  const turns = useMemo(() => JSON.parse(turnsNow) as Turn[] | null, [turnsNow]);
  const onNewThread = actions.onNewTask;
  const contents = useMemo(
    () => (selectedId && !all ? <Sections turns={turns} projects={projects} sessions={sessions} selectedId={selectedId} hasNews={hasNews} onAll={() => { setAll(true); focusNext(BACK_ID); }} onNewThread={onNewThread} /> : undefined),
    [selectedId, all, turns, projects, sessions, hasNews, onNewThread],
  );
  const back = useMemo(
    () => (selectedId && all ? (
      <div className="flex shrink-0 px-2 pb-1">
        <Button id={BACK_ID} size="sm" variant="secondary" className="bg-raised shadow-raised" onClick={() => { setAll(false); focusNext(ALL_ID); }}>Back to this Thread</Button>
      </div>
    ) : undefined),
    [selectedId, all],
  );
  return <Sidebar {...sidebar} contents={contents} listHead={back} />;
}
