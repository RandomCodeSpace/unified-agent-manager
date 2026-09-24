import assert from 'node:assert/strict';
import test from 'node:test';
import { groupTasks, mostRecentProject, tasksOf } from '../src/lib/tasks.ts';

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

test('New task targets the open task\'s project, else the project with the newest activity', () => {
  const list = [s('a', 'p1', '2026-09-24T09:00:00Z')];
  assert.equal(mostRecentProject([p1, p2], list, 'a')?.id, 'p1');
  assert.equal(mostRecentProject([p1, p2], list, null)?.id, 'p1');
  assert.equal(mostRecentProject([p1, p2], [], null)?.id, 'p2');
  assert.equal(mostRecentProject([], [], null), undefined);
});
