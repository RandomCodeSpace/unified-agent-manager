import assert from 'node:assert/strict';
import test from 'node:test';
import { filteredProject, groupTasks, mostRecentProject, newsReader, showsFinish, shownState, tasksOf, visibleProjects } from '../src/lib/tasks.ts';

const p1 = { id: 'p1', name: 'one', dir: '/one', created_at: '2026-09-20T10:00:00Z' };
const p2 = { id: 'p2', name: 'two', dir: '/two', created_at: '2026-09-23T10:00:00Z' };
const s = (id, project_id, created_at, extra = {}) => ({ id, project_id, created_at, updated_at: created_at, state: 'completed', ...extra });

test('a project lists its tasks newest first', () => {
  const list = [s('a', 'p1', '2026-09-21T00:00:00Z'), s('b', 'p2', '2026-09-22T00:00:00Z'), s('c', 'p1', '2026-09-24T00:00:00Z')];
  assert.deepEqual(tasksOf(list, 'p1').map((t) => t.id), ['c', 'a']);
});

test('active tasks come first; settled and archived go to their shelves', () => {
  const list = [s('a', 'p1', '1'), s('b', 'p1', '2', { stage: 'settled' }), s('c', 'p1', '3', { stage: 'archived' }), s('d', 'p1', '4', { stage: 'active' })];
  const g = groupTasks(list);
  assert.deepEqual(g.active.map((t) => t.id), ['a', 'd']);
  assert.deepEqual(g.settled.map((t) => t.id), ['b']);
  assert.deepEqual(g.archived.map((t) => t.id), ['c']);
});

test('the project filter shows one project while it exists, else every project', () => {
  assert.deepEqual(visibleProjects([p1, p2], null), [p1, p2]);
  assert.deepEqual(visibleProjects([p1, p2], 'p2'), [p2]);
  assert.deepEqual(visibleProjects([p1, p2], 'gone'), [p1, p2]);
  assert.deepEqual(visibleProjects([], 'p1'), []);
  assert.equal(filteredProject([p1, p2], 'p1'), p1);
  assert.equal(filteredProject([p1, p2], 'gone'), null);
  assert.equal(filteredProject([p1, p2], null), null);
});

test('New task targets the open task\'s project, else the project with the newest activity', () => {
  const list = [s('a', 'p1', '2026-09-24T09:00:00Z')];
  assert.equal(mostRecentProject([p1, p2], list, 'a')?.id, 'p1');
  assert.equal(mostRecentProject([p1, p2], list, null)?.id, 'p1');
  assert.equal(mostRecentProject([p1, p2], [], null)?.id, 'p2');
  assert.equal(mostRecentProject([], [], null), undefined);
});


test('sidebar search combines title, project and branch words, including shelved tasks', async () => {
  const { matchesTask } = await import('../src/lib/tasks.ts');
  const project = { name: 'Unified agent manager', dir: '/repo/uam', branch: 'feat/web' };
  const task = { name: '', title: 'Review development status', stage: 'archived' };
  assert.equal(matchesTask(task, project, ' REVIEW   web '), true);
  assert.equal(matchesTask(task, project, 'uam status'), true);
  assert.equal(matchesTask(task, project, 'missing'), false);
  assert.equal(matchesTask(task, project, '  '), true);
});


test('flat sidebar retains every lifecycle, filters projects and searches without regrouping by project', async () => {
  const { sidebarTasks } = await import('../src/lib/tasks.ts');
  const projects = [{ id: 'a', name: 'Alpha', dir: '/a', branch: 'main' }, { id: 'b', name: 'Beta', dir: '/b', branch: 'topic' }];
  const sessions = [
    { id: 'old', project_id: 'a', title: 'Older', created_at: '2026-01-01', stage: 'active' },
    { id: 'new', project_id: 'b', title: 'Newest', created_at: '2026-03-01', stage: 'settled' },
    { id: 'archived', project_id: 'a', title: 'Archive', created_at: '2026-02-01', stage: 'archived' },
    { id: 'orphan', project_id: 'missing', title: 'Unavailable', created_at: '2026-04-01' },
  ];
  assert.deepEqual(sidebarTasks(projects, sessions, null).map((t) => t.id), ['new', 'archived', 'old']);
  assert.deepEqual(sidebarTasks(projects, sessions, 'a').map((t) => t.id), ['archived', 'old']);
  assert.deepEqual(sidebarTasks(projects, sessions, null, 'topic newest').map((t) => t.id), ['new']);
  assert.deepEqual(sidebarTasks(projects, sessions, 'a', 'topic'), []);
  assert.deepEqual(sidebarTasks(projects, sessions, 'deleted').map((t) => t.id), ['new', 'archived', 'old']);
  assert.equal(sessions[0].id, 'old');
});

test('the needs-you count follows the sidebar rows: attention states and pending requests, never the shelves', async () => {
  const { needsYouCount, pageTitle } = await import('../src/lib/tasks.ts');
  const list = [
    s('a', 'p1', '1', { state: 'awaiting_permission' }),
    s('b', 'p1', '2', { state: 'awaiting_answer' }),
    s('c', 'p1', '3', { state: 'working', pending: 2 }),
    s('d', 'p1', '4', { state: 'working', pending: true }),
    s('e', 'p1', '5', { state: 'working', pending: 0 }),
    s('f', 'p1', '6', { state: 'awaiting_permission', stage: 'archived' }),
    s('g', 'p1', '7', { state: 'completed' }),
  ];
  assert.equal(needsYouCount(list), 4);
  assert.equal(needsYouCount([]), 0);
  assert.equal(pageTitle(0, null), 'UAM');
  assert.equal(pageTitle(3, null), '(3) UAM');
  assert.equal(pageTitle(0, 'Fix redraw'), 'UAM - Fix redraw');
  assert.equal(pageTitle(2, 'Fix redraw'), '(2) UAM - Fix redraw');
  assert.equal(pageTitle(1, ''), '(1) UAM - New task');
});

test('project search matches every word in the name or directory, names first', async () => {
  const { searchProjects } = await import('../src/lib/tasks.ts');
  const projects = [
    { id: 'a', name: 'dotfiles', dir: '/home/dev/dotfiles' },
    { id: 'b', name: 'notes-site', dir: '/home/dev/projects/notes-site' },
    { id: 'c', name: 'uam', dir: '/home/dev/projects/unified-agent-manager' },
  ];
  assert.deepEqual(searchProjects(projects, '').map((p) => p.id), ['a', 'b', 'c']);
  assert.deepEqual(searchProjects(projects, 'Notes').map((p) => p.id), ['b']);
  // A directory-only hit ranks after a name hit.
  assert.deepEqual(searchProjects(projects, 'projects').map((p) => p.id), ['b', 'c']);
  assert.deepEqual(searchProjects(projects, 'unified manager').map((p) => p.id), ['c']);
  assert.deepEqual(searchProjects(projects, 'missing'), []);
});

test('the palette starts on the filtered project, else the most recently active one', async () => {
  const { newTaskProject } = await import('../src/lib/tasks.ts');
  const sessions = [s('x', 'p1', '2026-09-24T00:00:00Z')];
  assert.equal(newTaskProject([p1, p2], sessions, 'p2', null)?.id, 'p2');
  assert.equal(newTaskProject([p1, p2], sessions, null, null)?.id, 'p1');
  // A filter naming a removed Project counts as none.
  assert.equal(newTaskProject([p1, p2], sessions, 'gone', null)?.id, 'p1');
  assert.equal(newTaskProject([p1, p2], sessions, null, 'x')?.id, 'p1');
});

test('unread checks use only the selected ID and recorded visit times', () => {
  const hasNews = newsReader('selected', { visited: '2026-09-25T10:00:00Z' }, '2026-09-25T09:00:00Z');
  assert.equal(hasNews({ id: 'selected', updated_at: '2026-09-25T11:00:00Z' }), false);
  assert.equal(hasNews({ id: 'visited', updated_at: '2026-09-25T10:00:00Z' }), false);
  assert.equal(hasNews({ id: 'visited', updated_at: '2026-09-25T10:00:01Z' }), true);
  assert.equal(hasNews({ id: 'unvisited', updated_at: '2026-09-25T08:59:59Z' }), false);
  assert.equal(hasNews({ id: 'unvisited', updated_at: '2026-09-25T09:00:01Z' }), true);
});

test('a task whose turn ended shows Working while a subagent still runs', () => {
  assert.equal(shownState({ state: 'completed', subagents_running: 2 }), 'working');
  assert.equal(shownState({ state: 'idle', subagents_running: 1 }), 'working');
  assert.equal(shownState({ state: 'completed', subagents_running: 0 }), 'completed');
  // A request waiting for the user, or a failure, still says so.
  assert.equal(shownState({ state: 'awaiting_permission', subagents_running: 1 }), 'awaiting_permission');
  assert.equal(shownState({ state: 'failed', subagents_running: 1 }), 'failed');
});

test('a compacting Task shows Working and its row says Compacting', async () => {
  const { taskStatus } = await import('../src/lib/tasks.ts');
  for (const state of ['idle', 'completed', 'working', 'cancelled']) assert.equal(shownState({ state, subagents_running: 0, compacting: true }), 'working');
  // A request waiting for the user still wins.
  assert.equal(shownState({ state: 'awaiting_answer', subagents_running: 0, compacting: true }), 'awaiting_answer');
  assert.equal(taskStatus(s('t', 'p1', '2026-10-01T12:00:00Z', { state: 'completed', compacting: true }), true).text, 'Compacting…');
  assert.equal(taskStatus(s('t', 'p1', '2026-10-01T12:00:00Z', { state: 'awaiting_permission', compacting: true })).text, 'Wants your OK to continue');
});

test('the Task list groups: Needs you, Ready for review, Working, Idle', async () => {
  const { commandGroups, groupOf, needsYouCount } = await import('../src/lib/tasks.ts');
  const unread = (t) => t.id.endsWith('*');
  const list = [
    s('ask', 'p1', '1', { state: 'awaiting_answer' }),
    s('pending', 'p1', '2', { state: 'working', pending: 1 }),
    s('failed*', 'p1', '3', { state: 'failed' }),
    s('failed-read', 'p1', '4', { state: 'failed' }),
    s('broke*', 'p1', '5', { state: 'interrupted' }),
    s('done*', 'p1', '6', { state: 'completed' }),
    s('done-read', 'p1', '7', { state: 'completed' }),
    s('helpers*', 'p1', '8', { state: 'completed', subagents_running: 1 }),
    s('work', 'p1', '9', { state: 'working' }),
    s('stopped*', 'p1', '10', { state: 'cancelled' }),
  ];
  assert.deepEqual(list.map((t) => groupOf(t, unread)), ['you', 'you', 'you', 'idle', 'you', 'review', 'idle', 'working', 'working', 'idle']);
  const g = commandGroups(list, unread);
  // Latest change first; Working keeps creation order so busy rows hold still.
  assert.deepEqual(g.you.map((t) => t.id), ['broke*', 'failed*', 'pending', 'ask']);
  assert.deepEqual(g.working.map((t) => t.id), ['work', 'helpers*']);
  assert.equal(needsYouCount(list, unread), 4);
  assert.equal(needsYouCount([s('x*', 'p1', '1', { state: 'failed', stage: 'settled' })], unread), 0);
});

test('the open Task keeps the group it had when it was opened', async () => {
  const { commandGroups, newsReader, placeReader } = await import('../src/lib/tasks.ts');
  const done = s('done', 'p1', '1', { state: 'completed', updated_at: '2026-10-05T10:00:00Z' });
  const other = s('other', 'p1', '2', { state: 'completed', updated_at: '2026-10-05T10:00:00Z' });
  const before = '2026-10-05T09:00:00Z';
  // Opening it marks it read: by news alone it would drop from Ready for review to Idle under the pointer.
  const news = newsReader('done', { done: '2026-10-05T10:00:01Z', other: '2026-10-05T10:00:01Z' }, before);
  assert.deepEqual(commandGroups([done, other], news).review.map((t) => t.id), []);
  const place = placeReader(news, 'done', before, before);
  assert.deepEqual(commandGroups([done, other], place).review.map((t) => t.id), ['done']);
  // Once another Task is open, it is placed by news again.
  assert.equal(placeReader(news, 'other', undefined, before)(done), false);
});

test('a Task row says in plain words what it needs or how it stands', async () => {
  const { taskStatus } = await import('../src/lib/tasks.ts');
  const now = Date.parse('2026-10-01T12:00:00Z');
  const at = (min) => new Date(now - min * 60000).toISOString();
  const status = (extra, unread = false) => taskStatus(s('t', 'p1', at(60), extra), unread, now).text;
  assert.equal(status({ state: 'awaiting_answer', ask: { id: 'q', kind: 'question', title: 'Which option?' } }), 'Asks: Which option?');
  assert.equal(status({ state: 'awaiting_permission', ask: { id: 'p', kind: 'permission', title: 'Run shell command' } }), 'Wants your OK to run a shell command');
  assert.equal(status({ state: 'awaiting_permission', ask: { id: 'p', kind: 'permission', title: 'Confirm deploy' } }), 'Wants your OK to confirm deploy');
  assert.equal(status({ state: 'awaiting_permission' }), 'Wants your OK to continue');
  assert.equal(status({ state: 'working', updated_at: at(30), event_at: at(12) }), 'Working · quiet 12m');
  assert.equal(status({ state: 'working', updated_at: at(30), event_at: at(1) }), 'Working');
  assert.equal(status({ state: 'working', updated_at: at(90) }), 'Working · quiet 1h');
  assert.equal(status({ state: 'completed' }, true), 'Finished, ready for your review');
  assert.equal(status({ state: 'completed' }), 'Finished');
  assert.equal(status({ state: 'completed', outcome: 'Fixed the test; tests pass' }), 'Fixed the test; tests pass');
  assert.equal(status({ state: 'completed', outcome: 'Fixed the test' }, true), 'Ready for review: Fixed the test');
  assert.equal(status({ state: 'failed' }), 'Stopped with an error');
  assert.equal(status({ state: 'cancelled' }), 'You stopped it');
  // uam stopped it, not the owner: the service says what did.
  assert.equal(status({ state: 'cancelled', state_detail: "Stopped at the routine's time limit (5 min)" }), "Stopped at the routine's time limit (5 min)");
  assert.equal(status({ state: 'completed', stage: 'settled' }), 'Settled');
});

test('compact Task labels retain request priority and distinguish working, review and finished states', async () => {
  const { taskStatus } = await import('../src/lib/tasks.ts');
  const label = (extra, unread = false) => taskStatus(s('t', 'p1', '2026-10-01T12:00:00Z', extra), unread).label;
  for (const state of ['awaiting_answer', 'awaiting_permission']) assert.equal(label({ state, compacting: true }), 'Input');
  assert.equal(label({ state: 'working', pending: 1 }), 'Input');
  assert.equal(label({ state: 'working', ask: { kind: 'question', title: 'Which one?' } }), 'Input');
  assert.equal(label({ state: 'completed', compacting: true }, true), 'Compacting');
  assert.equal(label({ state: 'completed', subagents_running: 1 }, true), 'Working');
  assert.equal(label({ state: 'working', event_at: '2020-01-01T00:00:00Z' }), 'Working');
  assert.equal(label({ state: 'completed', outcome: 'A long outcome' }, true), 'Review');
  assert.equal(label({ state: 'completed', outcome: 'A long outcome' }), 'Finished');
  for (const [state, expected] of Object.entries({ starting: 'Starting', failed: 'Error', interrupted: 'Interrupted', cancelled: 'Stopped', closed: 'Closed', idle: 'Idle' })) assert.equal(label({ state }), expected);
  assert.equal(label({ state: 'completed', stage: 'settled' }), 'Settled');
  assert.equal(label({ state: 'completed', stage: 'archived' }), 'Archived');
});

test('Alt+J and Alt+K cycle through the Needs you Tasks and wrap', async () => {
  const { cycleTask } = await import('../src/lib/tasks.ts');
  assert.equal(cycleTask([], 'a', 1), null);
  assert.equal(cycleTask(['a', 'b', 'c'], null, 1), 'a');
  assert.equal(cycleTask(['a', 'b', 'c'], 'gone', -1), 'c');
  assert.equal(cycleTask(['a', 'b', 'c'], 'c', 1), 'a');
  assert.equal(cycleTask(['a', 'b', 'c'], 'a', -1), 'c');
  assert.equal(cycleTask(['a', 'b', 'c'], 'b', 1), 'c');
});

test('the finish card follows a completed turn, not while a subagent or compaction keeps the Task working', () => {
  const items = [{ id: 'u', kind: 'user', time: 't' }, { id: 'a', kind: 'assistant', time: 't', text: 'Done.' }];
  const done = { state: 'completed', subagents_running: 0, turn_timings: [{ id: 'tt', started_at: 't', ended_at: 't', state: 'completed' }] };
  assert.equal(showsFinish(done, items, false), true);
  // The turn completed, but a subagent still runs or the conversation compacts: Working, no card yet.
  assert.equal(showsFinish({ ...done, subagents_running: 1 }, items, false), false);
  assert.equal(showsFinish({ ...done, compacting: true }, items, false), false);
  assert.equal(showsFinish(done, items, true), false);
  assert.equal(showsFinish({ ...done, history_after: 'c' }, items, false), false);
  assert.equal(showsFinish({ ...done, turn_timings: [{ id: 'tt', started_at: 't', state: 'failed' }] }, items, false), false);
  assert.equal(showsFinish(done, items.slice(0, 1), false), false);
});
