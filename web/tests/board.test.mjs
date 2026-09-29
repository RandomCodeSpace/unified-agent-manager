import assert from 'node:assert/strict';
import test from 'node:test';
import { applyBoardFrame, buildOutline, deriveBoard, deriveContainer, layoutMap, MAP_ROW, pendingRequests } from '../src/lib/board.ts';
import { initialState, reducer } from '../src/state.ts';

let seq = 0;
const card = (over) => {
  seq += 1;
  return { id: `c${seq}`, seq, project_id: 'p1', kind: 'subtask', parent_id: null, rank: seq, title: `Card ${seq}`, desc: '', win_condition: '', status: 'todo', prio: 0, labels: [], checklist: [], blocked: false, blocked_by: [], blocks: [], confirmed: true, pinned_sha: '', accept_cmd: null, paths: [], pending_requests: 0, revision: 1, created_at: '', updated_at: '', moved_at: '', ...over };
};
const leaf = (status, over = {}) => ({ status, confirmed: true, pending_requests: 0, ...over });
const request = (id, over = {}) => ({ id, card_id: 'c1', task_id: 't1', agent_id: '', kind: 'done', comment: '', payload: {}, evidence: {}, flags: [], base_revision: 1, status: 'pending', created_at: '', ...over });

test('a container derives its status from the confirmed leaves under it', () => {
  assert.equal(deriveContainer([]).status, 'planned');
  assert.equal(deriveContainer([leaf('todo', { confirmed: false })]).status, 'planned', 'suggestions alone never move it');
  assert.equal(deriveContainer([leaf('planned'), leaf('todo')]).status, 'planned');
  assert.equal(deriveContainer([leaf('done'), leaf('todo')]).status, 'doing');
  assert.equal(deriveContainer([leaf('done'), leaf('cancelled')]).status, 'done', 'cancelled leaves leave the count');
  assert.equal(deriveContainer([leaf('cancelled'), leaf('cancelled')]).status, 'cancelled');
});

test('a hold makes a container doing, and a hold or a pending request keeps it from done', () => {
  assert.equal(deriveContainer([leaf('doing', { held_by: 't1' }), leaf('planned')]).status, 'doing');
  assert.equal(deriveContainer([leaf('doing', { confirmed: false, held_by: 't1' })]).status, 'doing', 'a held suggestion still counts');
  assert.equal(deriveContainer([leaf('done'), leaf('done', { pending_requests: 1 })]).status, 'doing');
  assert.equal(deriveContainer([leaf('done'), leaf('cancelled', { held_by: 't2' })]).status, 'doing');
});

test('progress counts done over live confirmed leaves, and proposed ones apart', () => {
  const { progress } = deriveContainer([leaf('done'), leaf('todo'), leaf('cancelled'), leaf('todo', { confirmed: false }), leaf('cancelled', { confirmed: false })]);
  assert.deepEqual(progress, { done: 1, total: 2, proposed: 1 });
});

test('deriveBoard rolls leaves up through stories into epics and keeps unchanged cards', () => {
  const epic = card({ kind: 'epic', status: 'planned' });
  const story = card({ kind: 'story', parent_id: epic.id, status: 'planned' });
  const a = card({ parent_id: story.id, status: 'done' });
  const b = card({ parent_id: story.id, status: 'done' });
  const other = card({ kind: 'story', status: 'planned', progress: { done: 0, total: 0, proposed: 0 } });
  const out = deriveBoard([epic, story, a, b, other]);
  assert.equal(out[0].status, 'done');
  assert.deepEqual(out[0].progress, { done: 2, total: 2, proposed: 0 });
  assert.equal(out[1].status, 'done');
  assert.equal(out[2], a);
  assert.equal(out[4], other, 'an already derived container is the same object');
});

test('a board frame applies only at the next revision; a jump is a gap', () => {
  const a = card({ id: 'a' });
  const b = card({ id: 'b' });
  const data = { cards: [a, b], requests: [request('r1', { card_id: 'a' }), request('r2', { card_id: 'b' })], revision: 4 };
  assert.deepEqual(applyBoardFrame(data, { revision: 4, cards: [], removed: [], requests: [] }), { kind: 'ignored' });
  assert.deepEqual(applyBoardFrame(data, { revision: 6, cards: [], removed: [], requests: [] }), { kind: 'gap' });

  const edited = { ...a, title: 'Edited' };
  const added = card({ id: 'n' });
  const out = applyBoardFrame(data, { revision: 5, cards: [edited, added], removed: ['b'], requests: [request('r1', { card_id: 'a', status: 'accepted' }), request('r3', { card_id: 'a' })] });
  assert.equal(out.kind, 'applied');
  assert.equal(out.data.revision, 5);
  assert.deepEqual(out.data.cards.map((c) => c.id).sort(), ['a', 'n']);
  assert.equal(out.data.cards.find((c) => c.id === 'a').title, 'Edited');
  assert.deepEqual(out.data.requests.map((r) => r.id), ['r3'], 'decided requests leave, and a removed card takes its own');
});

const follow = (state, key, data) => reducer(reducer(state, { type: 'board_loading', key }), { type: 'board_loaded', key, data });
const frame = (revision, over = {}) => ({ name: 'board', seq: revision, project_id: 'p1', revision, cards: [], removed: [], requests: [], ...over });
const update = (state, data) => reducer(state, { type: 'update', data });

test('frames that arrive while a board loads replay on its reply', () => {
  let state = reducer(initialState, { type: 'board_loading', key: 'p1' });
  state = update(state, frame(3, { cards: [card({ id: 'late' })] }));
  state = update(state, frame(4, { cards: [card({ id: 'later' })] }));
  assert.equal(state.boards.p1.buffered.length, 2);
  state = reducer(state, { type: 'board_loaded', key: 'p1', data: { cards: [], requests: [], revision: 3 } });
  const board = state.boards.p1;
  assert.equal(board.loading, false);
  assert.equal(board.stale, false);
  assert.equal(board.data.revision, 4, 'the frame already in the reply is skipped, the next applies');
  assert.deepEqual(board.data.cards.map((c) => c.id), ['later']);
});

test('a revision gap marks the board stale, and frames for boards not followed are ignored', () => {
  let state = follow(initialState, 'p1', { cards: [], requests: [], revision: 2 });
  const before = state;
  state = update(state, frame(9, { project_id: 'p2' }));
  assert.equal(state, before);
  state = update(state, frame(5));
  assert.equal(state.boards.p1.stale, true);
  assert.equal(state.boards.p1.data.revision, 2);
  state = update(state, frame(3));
  assert.equal(state.boards.p1.data.revision, 2, 'a stale board waits for its fetch');
});

test('the Unassigned list follows frames with an empty project id', () => {
  let state = follow(initialState, 'unassigned', { cards: [], requests: [], revision: 1 });
  state = update(state, frame(2, { project_id: '', cards: [card({ id: 'u', project_id: '' })] }));
  assert.deepEqual(state.boards.unassigned.data.cards.map((c) => c.id), ['u']);
});

test('a new snapshot marks every board stale, even one in flight', () => {
  let state = follow(initialState, 'p1', { cards: [], requests: [], revision: 2 });
  state = reducer(state, { type: 'board_loading', key: 'unassigned' });
  state = reducer(state, { type: 'snapshot', data: { name: 'snapshot', seq: 1, sessions: [], projects: [] } });
  assert.equal(state.boards.p1.stale, true);
  state = reducer(state, { type: 'board_loaded', key: 'unassigned', data: { cards: [], requests: [], revision: 1 } });
  assert.equal(state.boards.unassigned.loading, false);
  assert.equal(state.boards.unassigned.stale, true, 'the reply may predate the new stream');
});

test('too many frames during a fetch give up the buffer and fetch again', () => {
  let state = reducer(initialState, { type: 'board_loading', key: 'p1' });
  for (let r = 2; r <= 70; r += 1) state = update(state, frame(r));
  assert.equal(state.boards.p1.loading, true);
  assert.equal(state.boards.p1.stale, true);
  assert.deepEqual(state.boards.p1.buffered, []);
  state = reducer(state, { type: 'board_loaded', key: 'p1', data: { cards: [], requests: [], revision: 1 } });
  assert.equal(state.boards.p1.stale, true);
});

test('a dropped board stops following, and pending requests sum across boards', () => {
  let state = follow(initialState, 'p1', { cards: [], requests: [request('r1'), request('r2')], revision: 1 });
  state = follow(state, 'unassigned', { cards: [], requests: [request('r3')], revision: 1 });
  assert.equal(pendingRequests(state.boards), 3);
  state = reducer(state, { type: 'board_dropped', key: 'p1' });
  assert.deepEqual(Object.keys(state.boards), ['unassigned']);
  assert.equal(pendingRequests(state.boards), 1);
});

test('the outline sets unconfirmed children apart as suggestions, and filters apply', () => {
  const e1 = card({ kind: 'epic' });
  const s1 = card({ kind: 'story', parent_id: e1.id });
  const s2 = card({ kind: 'story', parent_id: e1.id, confirmed: false });
  const s2a = card({ parent_id: s2.id, confirmed: false });
  const gone = card({ parent_id: s1.id, status: 'cancelled' });
  const e2 = card({ kind: 'epic', confirmed: false });
  const cards = [e1, s1, s2, s2a, gone, e2];

  const all = buildOutline(cards, { epic: null, showCancelled: false });
  assert.deepEqual(all.roots.map((n) => n.card.id), [e1.id]);
  assert.deepEqual(all.suggested.map((n) => n.card.id), [e2.id]);
  assert.deepEqual(all.roots[0].children.map((n) => n.card.id), [s1.id]);
  assert.deepEqual(all.roots[0].suggested.map((n) => n.card.id), [s2.id]);
  assert.deepEqual(all.roots[0].suggested[0].children.map((n) => n.card.id), [s2a.id], 'a suggestion keeps its own children');
  assert.deepEqual(all.roots[0].children[0].children, []);

  const shown = buildOutline(cards, { epic: e1.id, showCancelled: true });
  assert.deepEqual(shown.roots[0].children[0].children.map((n) => n.card.id), [gone.id]);
  assert.deepEqual(shown.suggested, []);
});

test('the map lays kinds out in columns, centres parents on their children and never stacks rows', () => {
  const epic = card({ kind: 'epic' });
  const story = card({ kind: 'story', parent_id: epic.id });
  const a = card({ parent_id: story.id });
  const b = card({ parent_id: story.id, blocked_by: [] });
  const c = card({ parent_id: story.id });
  const loose = card({ blocked_by: [a.id, 'not-drawn'] });
  const hidden = card({ parent_id: story.id, status: 'cancelled' });
  const { nodes, edges, width, height } = layoutMap([epic, story, a, b, c, loose, hidden], { epic: null, showCancelled: false });
  const at = (x) => nodes.find((n) => n.card.id === x.id);

  assert.equal(nodes.length, 6, 'cancelled cards stay off the map unless shown');
  assert.ok(at(epic).x < at(story).x && at(story).x < at(a).x);
  assert.equal(at(loose).x, at(a).x, 'a subtask the root holds sits in the subtask column');
  assert.equal(at(story).y, (at(a).y + at(c).y) / 2);
  assert.equal(at(epic).y, at(story).y);
  const leaves = [a, b, c, loose].map((x) => at(x).y);
  for (let i = 1; i < leaves.length; i += 1) assert.ok(leaves[i] - leaves[i - 1] >= MAP_ROW, 'leaves a row apart at least');
  assert.equal(at(loose).y - at(c).y, MAP_ROW * 1.5, 'sibling trees a half row further apart');

  assert.deepEqual(edges.filter((e) => e.kind === 'blocker'), [{ from: a.id, to: loose.id, kind: 'blocker' }]);
  assert.equal(edges.filter((e) => e.kind === 'parent').length, 4);
  assert.ok(width > at(a).x && height > at(loose).y);
});

test('the map filtered to one epic draws only its subtree', () => {
  const e1 = card({ kind: 'epic' });
  const e2 = card({ kind: 'epic' });
  const s = card({ kind: 'story', parent_id: e2.id });
  const { nodes } = layoutMap([e1, e2, s], { epic: e2.id, showCancelled: false });
  assert.deepEqual(nodes.map((n) => n.card.id).sort(), [e2.id, s.id].sort());
  assert.equal(nodes.find((n) => n.card.id === s.id).y, 0);
});
