import assert from 'node:assert/strict';
import test from 'node:test';
import { applyBoardFrame, boardOf, buildOutline, deriveBoard, deriveContainer, fitView, layoutMap, MAP_MAX_K, MAP_MIN_K, MAP_ROW, openBlockerSeqs, openingView, pendingRequests, linkTargets, isStarted, lockedReason, taskCard, waitsOf, nextSubtask, layoutLevel, wrapText, GRAPH_NODE, shownProgress, runSummary } from '../src/lib/board.ts';
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

test('progress as shown counts every live subtask, proposals included, and names the proposals', () => {
  assert.deepEqual(shownProgress({ done: 0, total: 1, proposed: 2 }), { done: 0, total: 3, proposed: 2, fraction: 0, short: '0/3 · 2 proposed', text: '0/3 done · 2 proposed' });
  assert.deepEqual(shownProgress({ done: 1, total: 2, proposed: 0 }), { done: 1, total: 2, proposed: 0, fraction: 0.5, short: '1/2', text: '1/2 done' });
  assert.equal(shownProgress({ done: 0, total: 0, proposed: 0 }).fraction, 0);
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

  const proposed = buildOutline([...cards, card({ parent_id: e2.id, confirmed: false })], { epic: e2.id, showCancelled: false });
  assert.deepEqual(proposed.roots.map((n) => n.card.id), [e2.id], 'the epic filter shows an epic an agent proposed');
  assert.equal(proposed.roots[0].children.length, 1, 'with its own suggestions under it');
  assert.deepEqual(proposed.suggested, []);
});

test('the map lays kinds out in columns, centres parents on their children and never stacks rows', () => {
  const epic = card({ kind: 'epic' });
  const story = card({ kind: 'story', parent_id: epic.id });
  const a = card({ parent_id: story.id });
  const b = card({ parent_id: story.id, blocked_by: [] });
  const c = card({ parent_id: story.id, blocked_by: [a.id, 'not-drawn'] });
  // A link across levels, made before links joined one level only, is no edge.
  const loose = card({ blocked_by: [a.id] });
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

  assert.deepEqual(edges.filter((e) => e.kind === 'blocker'), [{ from: a.id, to: c.id, kind: 'blocker' }]);
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

test('the map opens at scale 1 with its roots a margin in from the top-left', () => {
  const epic = card({ kind: 'epic' });
  const story = card({ kind: 'story', parent_id: epic.id });
  const layout = layoutMap([epic, story, card({ parent_id: story.id })], { epic: null, showCancelled: false });
  assert.deepEqual(openingView(layout, 24), { k: 1, x: 24, y: 24 });
  // A board of loose subtasks (Unassigned) starts at its subtask column, not at empty epic space.
  const loose = layoutMap([card({})], { epic: null, showCancelled: false });
  assert.equal(loose.nodes[0].x > 0, true);
  assert.deepEqual(openingView(loose, 24), { k: 1, x: 24 - loose.nodes[0].x, y: 24 });
});

test('Fit shows the whole plan within the zoom limits', () => {
  assert.ok(MAP_MIN_K >= 0.4 && MAP_MAX_K <= 2);
  const leaves = (n) => {
    const story = card({ kind: 'story' });
    return [story, ...Array.from({ length: n }, () => card({ parent_id: story.id }))];
  };
  // A small plan never grows past 1, centred across, from the top.
  const small = layoutMap(leaves(2), { epic: null, showCancelled: false });
  const fitSmall = fitView(small, 2000, 1000, 24);
  assert.equal(fitSmall.k, 1);
  assert.equal(fitSmall.y, 24);
  const left = small.nodes.find((n) => n.card.kind === 'story').x;
  assert.equal(fitSmall.x, (2000 - (small.width - left)) / 2 - left);
  // A taller one shrinks until it fits.
  const tall = layoutMap(leaves(30), { epic: null, showCancelled: false });
  const fitTall = fitView(tall, 1200, 900, 24);
  assert.ok(fitTall.k < 1 && fitTall.k >= MAP_MIN_K);
  assert.ok(tall.height * fitTall.k <= 900 - 48 + 0.001);
  // One too tall even at the limit stops there, from the top; one too wide as well starts at its left edge.
  const huge = layoutMap(leaves(300), { epic: null, showCancelled: false });
  const fitHuge = fitView(huge, 1200, 900, 24);
  assert.equal(fitHuge.k, MAP_MIN_K);
  assert.equal(fitHuge.y, 24);
  const hugeLeft = huge.nodes.find((n) => n.card.kind === 'story').x;
  assert.equal(fitHuge.x, (1200 - (huge.width - hugeLeft) * MAP_MIN_K) / 2 - hugeLeft * MAP_MIN_K);
  assert.equal(fitView(huge, 200, 900, 24).x, 24 - hugeLeft * MAP_MIN_K);
});

test('map nodes come parents first, so Tab goes epic, story, subtask', () => {
  const epic = card({ kind: 'epic' });
  const story = card({ kind: 'story', parent_id: epic.id });
  const a = card({ parent_id: story.id });
  const b = card({ parent_id: story.id });
  const loose = card({});
  const { nodes } = layoutMap([loose, b, a, story, epic], { epic: null, showCancelled: false });
  assert.deepEqual(nodes.map((n) => n.card.id), [epic.id, story.id, a.id, b.id, loose.id]);
});

test('an unchanged card at the same place keeps its map node, so the Map memo holds', () => {
  const story = card({ kind: 'story' });
  const a = card({ parent_id: story.id });
  const b = card({ parent_id: story.id });
  const at = (layout, id) => layout.nodes.find((n) => n.card.id === id);
  const first = layoutMap([story, a, b], { epic: null, showCancelled: false });
  const renamed = { ...b, title: 'Renamed' };
  const second = layoutMap([story, a, renamed], { epic: null, showCancelled: false });
  assert.equal(at(second, a.id), at(first, a.id));
  assert.equal(at(second, story.id), at(first, story.id));
  assert.notEqual(at(second, b.id), at(first, b.id));
  assert.equal(at(second, b.id).card, renamed);
  const third = layoutMap([story, card({ parent_id: story.id, rank: -1 }), a, renamed], { epic: null, showCancelled: false });
  assert.notEqual(at(third, a.id), at(second, a.id), 'the same card one row down is a new node');
  assert.equal(at(third, a.id).y, MAP_ROW);
});

test('open blockers are a plain string, empty when none is open', () => {
  const done = card({ status: 'done' });
  const open = card({});
  const blocked = card({ blocked_by: [done.id, open.id, 'gone'] });
  const byId = new Map([done, open, blocked].map((c) => [c.id, c]));
  assert.equal(openBlockerSeqs(blocked, byId), `#${open.seq}`);
  assert.equal(openBlockerSeqs(open, byId), '');
});

test('a card waits on its parents’ open blockers too, named with the parent they come through', () => {
  const epicB = card({ kind: 'epic' });
  const epicA = card({ kind: 'epic', blocked_by: [epicB.id] });
  const s2 = card({ kind: 'story', parent_id: epicA.id });
  const s1 = card({ kind: 'story', parent_id: epicA.id, blocked_by: [s2.id] });
  const two = card({ parent_id: s1.id });
  const one = card({ parent_id: s1.id, blocked_by: [two.id] });
  const byId = new Map([epicB, epicA, s2, s1, two, one].map((c) => [c.id, c]));
  assert.equal(openBlockerSeqs(one, byId), `#${two.seq}, #${s2.seq} (via its story #${s1.seq}), #${epicB.seq} (via its epic #${epicA.seq})`);
  assert.equal(openBlockerSeqs(s2, byId), `#${epicB.seq} (via its epic #${epicA.seq})`);
});

test('a card links only with cards of its kind under its parent that have not started', () => {
  const epic = card({ kind: 'epic' });
  const other = card({ kind: 'epic' });
  const story = card({ kind: 'story', parent_id: epic.id });
  const otherStory = card({ kind: 'story', parent_id: other.id });
  const sibling = card({ kind: 'story', parent_id: epic.id, confirmed: false });
  const one = card({ parent_id: story.id });
  const held = card({ parent_id: story.id, status: 'doing', held_by: 't1' });
  const done = card({ parent_id: story.id, status: 'done' });
  const gone = card({ parent_id: story.id, status: 'cancelled' });
  const peer = card({ parent_id: story.id });
  const cards = [epic, other, story, otherStory, sibling, one, held, done, gone, peer];
  assert.deepEqual(linkTargets(story, cards).map((c) => c.id), [sibling.id]);
  assert.deepEqual(linkTargets(one, cards).map((c) => c.id), [peer.id]);
  assert.deepEqual(linkTargets(epic, cards).map((c) => c.id), [other.id]);
  assert.deepEqual(linkTargets({ ...one, blocks: [peer.id] }, cards), [], 'the other way round would close a cycle');
  assert.equal(isStarted(held) && isStarted(done) && !isStarted(one) && !isStarted(story), true);
  assert.equal(lockedReason(held), 'In progress: release it to change the plan.');
  assert.equal(lockedReason(done), 'Done: move it back to To do to change the plan.');
  assert.equal(lockedReason(one), null);
});

const snapshot = (boards) => ({ type: 'snapshot', data: { name: 'snapshot', seq: 3, sessions: [], projects: [], boards } });

test('a snapshot with Board revisions marks stale only the Boards that moved or are missing', () => {
  let state = follow(initialState, 'p1', { cards: [], requests: [], revision: 4 });
  state = follow(state, 'p3', { cards: [], requests: [], revision: 2 });
  state = follow(state, 'unassigned', { cards: [], requests: [], revision: 7 });
  state = follow(state, 'p9', { cards: [], requests: [], revision: 1 });
  const before = state.boards;
  state = reducer(state, snapshot({ p1: 4, p3: 5, '': 7 }));
  assert.equal(state.boards.p1, before.p1, 'the same revision: kept as it is');
  assert.equal(state.boards.unassigned, before.unassigned, 'the Unassigned list is the empty key');
  assert.equal(state.boards.p3.stale, true, 'moved while no stream was open');
  assert.equal(state.boards.p9.stale, true, 'missing from the snapshot');
});

test('a Board still loading at a snapshot is fetched again even when the revisions match', () => {
  let state = reducer(initialState, { type: 'board_loading', key: 'p1' });
  state = reducer(state, snapshot({ p1: 1 }));
  state = reducer(state, { type: 'board_loaded', key: 'p1', data: { cards: [], requests: [], revision: 1 } });
  assert.equal(state.boards.p1.stale, true, 'the reply may predate the new stream');
});

test('a card opened from outside the planner is found on its own Board', () => {
  const here = card({ project_id: 'p3' });
  const boards = { p1: { data: { cards: [card({})], requests: [], revision: 1 } }, p3: { data: { cards: [here], requests: [], revision: 2 } }, unassigned: { data: null } };
  assert.equal(boardOf(boards, here.id), 'p3');
  assert.equal(boardOf(boards, 'gone'), undefined);
});

test('a Task works on the subtask it holds, else one it finished; a released one is not its card', () => {
  const held = card({ status: 'doing', held_by: 't1', worked_by: 't1' });
  const finished = card({ status: 'done', worked_by: 't2' });
  const released = card({ status: 'todo', worked_by: 't3' });
  const cards = [held, finished, released];
  assert.equal(taskCard(cards, 't1'), held);
  assert.equal(taskCard(cards, 't2'), finished);
  assert.equal(taskCard(cards, 't3'), undefined);
  assert.equal(taskCard(cards, 'none'), undefined);
});

test('a card waits for its own open blockers, then its story’s and its epic’s, nearest first', () => {
  const epicA = card({ kind: 'epic' });
  const epicB = card({ kind: 'epic', blocked_by: [epicA.id] });
  const s1 = card({ kind: 'story', parent_id: epicB.id });
  const s2 = card({ kind: 'story', parent_id: epicB.id, blocked_by: [s1.id] });
  const a = card({ parent_id: s2.id, status: 'done' });
  const b = card({ parent_id: s2.id, blocked_by: [a.id] });
  const c = card({ parent_id: s2.id, blocked_by: [b.id] });
  const byId = new Map([epicA, epicB, s1, s2, a, b, c].map((x) => [x.id, x]));
  assert.deepEqual(waitsOf(c, byId).map((w) => [w.card.id, w.via?.id]), [[b.id, undefined], [s1.id, s2.id], [epicA.id, epicB.id]]);
  assert.deepEqual(waitsOf(b, byId).map((w) => w.card.id), [s1.id, epicA.id], 'a done blocker no longer holds it back');
  const index = new Map([[s2.id, [a, b, c]]]);
  assert.equal(nextSubtask(s2, index, byId), undefined, 'every subtask waits on its story’s blocker');
});

test('next is the first subtask not started with nothing open in its way, a confirmed one before a proposal', () => {
  const s = card({ kind: 'story' });
  const done = card({ parent_id: s.id, status: 'done' });
  const waiting = card({ parent_id: s.id, status: 'planned' });
  const free = card({ parent_id: s.id, status: 'todo' });
  const proposal = card({ parent_id: s.id, status: 'planned', confirmed: false });
  const held = card({ parent_id: s.id, status: 'doing', held_by: 't1' });
  waiting.blocked_by = [free.id];
  const kids = [done, waiting, proposal, free, held];
  const byId = new Map([s, ...kids].map((x) => [x.id, x]));
  const index = new Map([[s.id, kids]]);
  assert.equal(nextSubtask(s, index, byId), free, 'not the blocked one before it, nor the proposal');
  assert.equal(nextSubtask(s, index, byId, free.id), proposal, 'a proposal only when no confirmed one is free');
  free.status = 'done';
  assert.equal(nextSubtask(s, index, byId, free.id), waiting, 'its blocker is done');
  waiting.blocked = true;
  assert.equal(nextSubtask(s, index, byId, free.id), proposal, 'flagged blocked');
  proposal.blocked_by = [held.id];
  waiting.blocked_by = [held.id];
  assert.equal(nextSubtask(s, index, byId, free.id), undefined, 'none is free');
});

test('one level lays out in layers by its longest chain of blockers, left to right or top to bottom', () => {
  const a = card({});
  const b = card({ blocked_by: [a.id] });
  const c = card({ blocked_by: [a.id] });
  const d = card({ blocked_by: [b.id, c.id] });
  const outside = 'not-in-this-level';
  const e = card({ blocked_by: [outside] });
  const lr = layoutLevel([a, b, c, d, e], 'lr');
  const at = (l, x) => l.nodes.find((n) => n.card === x);
  const { w, h, main, cross } = GRAPH_NODE;
  // Layers: a and e first, b and c second, d last.
  assert.deepEqual([a, b, c, d, e].map((x) => at(lr, x).x), [0, w + main, w + main, 2 * (w + main), 0]);
  assert.equal(lr.width, 3 * w + 2 * main);
  assert.equal(lr.height, 2 * h + cross);
  assert.deepEqual(lr.edges.map((ed) => `${ed.from.card.id}>${ed.to.card.id}`).sort(), [`${a.id}>${b.id}`, `${a.id}>${c.id}`, `${b.id}>${d.id}`, `${c.id}>${d.id}`].sort(), 'a blocker outside the level draws no edge');
  const tb = layoutLevel([a, b, c, d, e], 'tb');
  assert.equal(at(tb, d).y, 2 * (h + main));
  assert.equal(tb.width, 2 * w + cross);
  // A cycle (refused by the service) is drawn, not followed forever.
  const x = card({});
  const y = card({ blocked_by: [x.id] });
  x.blocked_by = [y.id];
  assert.equal(layoutLevel([x, y], 'lr').nodes.length, 2);
  assert.deepEqual(layoutLevel([], 'lr'), { nodes: [], edges: [], width: 0, height: 0, dir: 'lr' });
});

test('graph titles wrap between words and end in an ellipsis when cut', () => {
  assert.deepEqual(wrapText('#40 Write the formula from the release archives', 24, 2), ['#40 Write the formula', 'from the release…']);
  assert.deepEqual(wrapText('#3 Short', 24, 2), ['#3 Short']);
  assert.deepEqual(wrapText('#9 Supercalifragilisticexpialidocious', 12, 2), ['#9', 'Supercalifr…']);
  assert.deepEqual(wrapText('', 24, 2), []);
});

test('a run counts lanes at work, subtasks ready to start and subtasks waiting, and nothing paused or proposed', () => {
  const epic = card({ kind: 'epic', run: { provider: 'copilot', model: 'm', effort: '', context_size: 'default', mode: 'yolo', parallel: 2, approved_at: '' } });
  const story = card({ kind: 'story', parent_id: epic.id });
  const lane = { branch: 'uam-plan-x', landed_sha: '', reverted_sha: '' };
  const working = card({ parent_id: story.id, status: 'doing', held_by: 't1', lane });
  const asking = card({ parent_id: story.id, status: 'doing', held_by: 't2', lane, pending_requests: 1 });
  const ready = card({ parent_id: story.id });
  const after = card({ parent_id: story.id, blocked_by: [ready.id] });
  const flagged = card({ parent_id: story.id, blocked: true });
  const proposed = card({ parent_id: story.id, confirmed: false });
  const pausedStory = card({ kind: 'story', parent_id: epic.id, paused: 'owner' });
  const held = card({ parent_id: pausedStory.id });
  const done = card({ parent_id: story.id, status: 'done' });
  const other = card({});
  const byId = new Map([epic, story, working, asking, ready, after, flagged, proposed, pausedStory, held, done, other].map((c) => [c.id, c]));
  assert.deepEqual(runSummary(epic, byId), { running: 1, parallel: 2, ready: 1, waiting: 2 });
});
