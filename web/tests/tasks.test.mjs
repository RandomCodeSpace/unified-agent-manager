import assert from 'node:assert/strict';
import test from 'node:test';
import { filteredProject, groupTasks, mostRecentProject, newsReader, tasksOf, visibleProjects } from '../src/lib/tasks.ts';

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
  assert.equal(pageTitle(0, 'Fix redraw'), 'Fix redraw · UAM');
  assert.equal(pageTitle(2, 'Fix redraw'), '(2) Fix redraw · UAM');
  assert.equal(pageTitle(1, ''), '(1) New task · UAM');
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
