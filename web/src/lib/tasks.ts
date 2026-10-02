import type { Item, Project, SessionDetail, SessionState, SessionSummary } from '../api';

const readOnly = (s: SessionSummary): boolean => s.stage === 'settled' || s.stage === 'archived';

/** Keep this closure outside App: otherwise differently memoized callbacks can
 * chain shared App render contexts and retain prior transcripts on navigation. */
export function newsReader(selectedId: string | null, viewed: Readonly<Record<string, string>>, loadedAt: string) {
  return (session: SessionSummary) => session.id !== selectedId && session.updated_at > (viewed[session.id] ?? loadedAt);
}

/**
 * The state a Task shows on its card and header chip: Working while a subagent still runs after its turn completed (Copilot
 * lets background subagents outlive the turn that started them), and while the conversation compacts, unless it waits for the user.
 */
export function shownState(s: Pick<SessionSummary, 'state' | 'subagents_running' | 'compacting'>): SessionState {
  if (s.compacting && s.state !== 'awaiting_permission' && s.state !== 'awaiting_answer' && s.state !== 'starting') return 'working';
  return (s.state === 'completed' || s.state === 'idle') && s.subagents_running > 0 ? 'working' : s.state;
}

/**
 * The finish card follows a turn that completed, at the live end of the history: not while the
 * Task works (`live`, or a subagent still running after the turn, or compacting, keeps it Working).
 */
export function showsFinish(s: Pick<SessionDetail, 'state' | 'subagents_running' | 'compacting' | 'history_after' | 'turn_timings'>, items: readonly Item[], live: boolean): boolean {
  const last = s.turn_timings?.at(-1);
  return !live && shownState(s) !== 'working' && !s.history_after && items.some((item) => item.kind === 'assistant' && !item.agent_id)
    && (last ? last.state === 'completed' : s.state === 'completed');
}

/** Something waits in the Task: a positive count, or a bare flag (`pendingCount` in api.ts). */
function hasPending(s: Pick<SessionSummary, 'pending'>): boolean {
  if (typeof s.pending === 'number') return s.pending > 0;
  return !!s.pending;
}

/** Whether a Task changed since it was last opened in this browser (`newsReader`). */
export type Unread = (s: SessionSummary) => boolean;

const never: Unread = () => false;

/**
 * The Task list's Needs you group: a request waits (`needsYou` in api.ts), or the Task failed
 * or was interrupted and has not been opened since. Never the shelves.
 */
export function needsYouNow(s: SessionSummary, unread: Unread = never): boolean {
  if (readOnly(s)) return false;
  return s.state === 'awaiting_permission' || s.state === 'awaiting_answer' || hasPending(s) || ((s.state === 'failed' || s.state === 'interrupted') && unread(s));
}

/** The Needs you group's size: the count the tab title, the app badge and the drawer button carry. */
export function needsYouCount(sessions: readonly SessionSummary[], unread: Unread = never): number {
  return sessions.filter((s) => needsYouNow(s, unread)).length;
}

/** The Task list's groups, in order; the Settled and Archived shelves follow them. */
export type GroupKey = 'you' | 'review' | 'working' | 'idle';
export const GROUP_TITLES: Record<GroupKey, string> = { you: 'Needs you', review: 'Ready for review', working: 'Working', idle: 'Idle' };

/** The group an active Task belongs to: Needs you, then finished and not opened since (Ready for review), then Working, else Idle. */
export function groupOf(s: SessionSummary, unread: Unread): GroupKey {
  if (needsYouNow(s, unread)) return 'you';
  const state = shownState(s);
  if (state === 'completed' && unread(s)) return 'review';
  if (state === 'starting' || state === 'working') return 'working';
  return 'idle';
}

const byUpdated = (a: SessionSummary, b: SessionSummary) => b.updated_at.localeCompare(a.updated_at) || b.created_at.localeCompare(a.created_at);
const byCreated = (a: SessionSummary, b: SessionSummary) => b.created_at.localeCompare(a.created_at);

/**
 * Active Tasks by group. Each group lists its latest change first, except Working, which keeps
 * creation order so rows that are busy do not trade places.
 */
export function commandGroups(active: readonly SessionSummary[], unread: Unread): Record<GroupKey, SessionSummary[]> {
  const groups: Record<GroupKey, SessionSummary[]> = { you: [], review: [], working: [], idle: [] };
  for (const s of active) groups[groupOf(s, unread)].push(s);
  groups.you.sort(byUpdated);
  groups.review.sort(byUpdated);
  groups.working.sort(byCreated);
  groups.idle.sort(byUpdated);
  return groups;
}

/** Alt+J / Alt+K: the next (or previous) Task in `ids` after the open one, wrapping; the first (or last) when the open one is not among them. */
export function cycleTask(ids: readonly string[], current: string | null, step: 1 | -1): string | null {
  if (!ids.length) return null;
  const i = current ? ids.indexOf(current) : -1;
  if (i < 0) return step === 1 ? ids[0] : ids[ids.length - 1];
  return ids[(i + step + ids.length) % ids.length];
}

/** What a permission asks for, as the end of "Wants your OK to …". */
const PERMISSION_WORDS: Record<string, string> = {
  'Run shell command': 'run a shell command',
  'Write file': 'write a file',
  'Read file': 'read a file',
  'Access paths outside the workspace': 'use paths outside the project',
  'Fetch URL': 'fetch a web page',
  'Store memory': 'remember something',
};

/** How long ago, in words: "12m", "3h", "2d". */
function span(ms: number): string {
  const m = Math.floor(ms / 60000);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  return h < 24 ? `${h}h` : `${Math.floor(h / 24)}d`;
}

/** Quiet this long and a Working row says so. */
export const QUIET_MS = 3 * 60_000;

/**
 * A Task row's one plain line: what waits for you, or how the Task stands. Working says only how
 * long it has been quiet (from the provider's last event), never what it is doing.
 */
export function taskStatus(s: SessionSummary, unread: boolean, now = Date.now()): { text: string; tone: 'attention' | 'accent' | 'success' | 'error' | 'warning' | 'muted' } {
  if (s.stage === 'settled') return { text: 'Settled', tone: 'muted' };
  if (s.stage === 'archived') return { text: 'Archived', tone: 'muted' };
  const ask = s.ask;
  if (ask?.kind === 'permission') {
    const what = PERMISSION_WORDS[ask.title] ?? (ask.title ? ask.title.charAt(0).toLowerCase() + ask.title.slice(1) : 'continue');
    return { text: `Wants your OK to ${what}`, tone: 'attention' };
  }
  if (ask?.kind === 'question') return { text: `Asks: ${ask.title}`, tone: 'attention' };
  if (s.state === 'awaiting_permission') return { text: 'Wants your OK to continue', tone: 'attention' };
  if (s.state === 'awaiting_answer' || hasPending(s)) return { text: 'Has a question for you', tone: 'attention' };
  const state = shownState(s);
  switch (state) {
    case 'starting':
      return { text: 'Starting', tone: 'accent' };
    case 'working': {
      if (s.compacting) return { text: 'Compacting…', tone: 'accent' };
      const quiet = now - Date.parse(s.event_at ?? s.updated_at);
      return { text: quiet >= QUIET_MS ? `Working · quiet ${span(quiet)}` : 'Working', tone: 'accent' };
    }
    // The last turn's outcome line ("Fixed the flaky test; 3 files changed; tests pass") says what finished.
    case 'completed':
      if (unread) return { text: s.outcome ? `Ready for review: ${s.outcome}` : 'Finished, ready for your review', tone: 'success' };
      return { text: s.outcome || 'Finished', tone: 'muted' };
    case 'failed':
      return { text: 'Stopped with an error', tone: 'error' };
    case 'interrupted':
      return { text: 'Interrupted before it finished', tone: 'warning' };
    case 'cancelled':
      return { text: 'You stopped it', tone: 'muted' };
    case 'closed':
      return { text: 'Conversation closed', tone: 'muted' };
    default:
      return { text: 'Waiting for your message', tone: 'muted' };
  }
}

/** The document title: the needs-you count first, then the open Task's name, then the app. */
export function pageTitle(needsYou: number, taskName: string | null): string {
  const count = needsYou > 0 ? `(${needsYou}) ` : '';
  return taskName === null ? `${count}UAM` : `${count}${taskName || 'New task'} · UAM`;
}

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

/** The Project a sidebar filter names, while it exists; null means every Project shows (no filter, or a remembered one that was removed). */
export function filteredProject(projects: Project[], filter: string | null): Project | null {
  return (filter && projects.find((p) => p.id === filter)) || null;
}

/** The Projects the sidebar lists: the filtered one alone, else all of them. */
export function visibleProjects(projects: Project[], filter: string | null): Project[] {
  const chosen = filteredProject(projects, filter);
  return chosen ? [chosen] : projects;
}

/**
 * Local Project search for the filter dropdown and the New task palette: every word must
 * appear in the name or the directory. Name matches rank before directory-only ones; the
 * given order holds otherwise. An empty query lists them all.
 */
export function searchProjects(projects: Project[], query: string): Project[] {
  const words = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return projects;
  const hits = (text: string) => words.every((word) => text.includes(word));
  const byName = projects.filter((p) => hits(p.name.toLocaleLowerCase()));
  const byDir = projects.filter((p) => !byName.includes(p) && hits(`${p.name} ${p.dir}`.toLocaleLowerCase()));
  return [...byName, ...byDir];
}

/** The Project the New task palette highlights first: the filtered one, else the one with the newest activity. */
export function newTaskProject(projects: Project[], sessions: SessionSummary[], filter: string | null, selectedId: string | null): Project | undefined {
  return mostRecentProject(visibleProjects(projects, filter), sessions, selectedId);
}

/** The Project with the newest activity: the open Task's, else the one whose Task updated last (or that was created last). */
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

/** Local sidebar search. Every word may match the title, project, directory or branch. */
export function matchesTask(task: SessionSummary, project: Project, query: string): boolean {
  const words = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  const text = [task.name, task.title, project.name, project.dir, project.branch].filter(Boolean).join(' ').toLocaleLowerCase();
  return words.every((word) => text.includes(word));
}

/** One flat Task list across the visible Projects, newest first, including both shelves. */
export function sidebarTasks(projects: Project[], sessions: SessionSummary[], filter: string | null, query = ''): SessionSummary[] {
  const visible = new Map(visibleProjects(projects, filter).map((project) => [project.id, project]));
  return sessions.filter((task) => {
    const project = visible.get(task.project_id);
    return !!project && matchesTask(task, project, query);
  }).sort((a, b) => b.created_at.localeCompare(a.created_at));
}
