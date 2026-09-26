import assert from 'node:assert/strict';
import test from 'node:test';
import { initialState, reducer } from '../src/state.ts';
import { ACTIVE_BYTES, ACTIVE_ITEMS, TAIL_ITEMS, accountItem, itemCursor } from '../src/lib/historyWindow.ts';
import { recentProjection } from '../src/lib/historyState.ts';
import { RecentTasks } from '../src/lib/recentTasks.ts';
import { detailReducer, emptyDetails } from '../src/lib/detail-state.ts';

const item = (id, text = `message ${id}`) => ({ id: String(id), kind: 'assistant', time: '', text });
const transcript = (count = 400) => Array.from({ length: count }, (_, index) => item(index));
const ids = items => items.map(item => item.id);
const update = (state, data) => reducer(state, { type: 'update', data: { session_id: 'task', ...data } });
const session = (items, before = '', epoch = 'epoch') => ({ id: 'task', project_id: 'project', representation: 'compact-v1', epoch, detail_stream: true, items, history_before: before, interactions: [], subagents: [], history_truncated: false });
const snapshot = (detail, seq = 10) => ({ seq, representation: 'compact-v1', epoch: detail.epoch, detail_stream: true, projects: [{ id: 'project' }], sessions: [detail], session: detail });
const loaded = (all = transcript(), recent = 50) => reducer({ ...initialState, selectedId: 'task' }, { type: 'snapshot', data: snapshot(session(all.slice(-recent), all.length > recent ? itemCursor(all.at(-recent).id) : '')) });
const cursorId = cursor => Buffer.from(cursor, 'base64url').toString('utf8');

function begin(state, direction = 'older') {
  const before = direction === 'older' ? state.detail.history_before : state.detail.history_after;
  assert.ok(before, `a ${direction} boundary must exist`);
  const next = reducer(state, { type: 'history_loading', sessionId: 'task', direction, before });
  assert.equal(next.historyRequest?.loading, true);
  return next;
}
function deliver(state, items, before = '', after = '', seq = 20, epoch = state.detail.epoch) {
  return reducer(state, { type: 'history_loaded', sessionId: 'task', before: state.historyRequest.before, page: { seq, epoch, items, before, after, representation: 'compact-v1' } });
}
function fetchPage(state, all, direction = 'older', count = 50) {
  state = begin(state, direction);
  const boundary = all.findIndex(item => item.id === cursorId(state.historyRequest.before));
  assert.ok(boundary >= 0);
  const start = direction === 'older' ? Math.max(0, boundary - count) : boundary + 1;
  const end = direction === 'older' ? boundary : Math.min(all.length, start + count);
  const page = all.slice(start, end);
  assert.ok(page.length);
  return deliver(state, page, start > 0 ? itemCursor(page[0].id) : '', end < all.length ? itemCursor(page.at(-1).id) : '');
}
function bounded(state) {
  const { items, recent_items: tail } = state.detail;
  assert.ok(items.length <= ACTIVE_ITEMS);
  assert.ok(items.length === 1 || items.reduce((bytes, item) => bytes + accountItem(item), 0) <= ACTIVE_BYTES);
  assert.equal(new Set(ids(items)).size, items.length);
  assert.ok(tail.length <= TAIL_ITEMS);
  assert.ok(tail.length === 1 || tail.reduce((bytes, item) => bytes + accountItem(item), 0) <= ACTIVE_BYTES);
  assert.equal(new Set(ids(tail)).size, tail.length);
}

test('older and forward traversal restore a long single turn without gaps, duplicates, or unbounded records', () => {
  const all = transcript(400);
  let state = loaded(all);
  const visited = new Set(ids(state.detail.items));
  for (const direction of ['older', 'newer']) {
    let requests = 0;
    while (direction === 'older' ? state.detail.history_before : state.detail.history_after) {
      assert.ok(++requests <= 10, 'each page must advance its boundary');
      state = fetchPage(state, all, direction);
      bounded(state);
      const visible = state.detail.items.map(item => Number(item.id));
      for (let i = 1; i < visible.length; i++) assert.equal(visible[i], visible[i - 1] + 1);
      for (const item of state.detail.items) visited.add(item.id);
    }
    assert.equal(direction === 'older' ? state.detail.items[0].id : state.detail.items.at(-1).id, direction === 'older' ? '0' : '399');
  }
  assert.deepEqual([...visited].sort((a, b) => Number(a) - Number(b)), ids(all));
  assert.ok(state.detail.history_index.length <= 2000);
  assert.ok(state.detail.history_index.every(item => item.text === undefined && item.tool?.input === undefined && item.tool?.output === undefined));
});

test('live tail updates while reading older messages do not move the reader or become a middle-page cache', () => {
  const all = transcript(250);
  let state = loaded(all);
  while (state.detail.history_before) state = fetchPage(state, all);
  const oldPage = ids(state.detail.items);
  state = update(state, { name: 'item', seq: 21, append: true, item: item(250, 'new live reply') });
  state = update(state, { name: 'delta', seq: 22, item_id: '249', kind: 'assistant', text: ' final suffix' });
  state = update(state, { name: 'item', seq: 23, item: item(20, 'old visible correction') });
  assert.deepEqual(ids(state.detail.items), oldPage);
  assert.equal(state.detail.items.find(item => item.id === '20').text, 'old visible correction');
  assert.equal(state.detail.recent_items.at(-1).text, 'new live reply');
  assert.equal(state.detail.recent_items.find(item => item.id === '249').text, 'message 249 final suffix');
  const cache = new RecentTasks();
  cache.remember(recentProjection(state.detail));
  const saved = cache.get('task');
  assert.deepEqual(ids(saved.items), Array.from({ length: 50 }, (_, i) => String(i + 201)));
  assert.equal(saved.history_after, '');
  assert.equal(saved.recent_items, undefined);
  assert.equal(saved.history_index, undefined);
  state = reducer(state, { type: 'history_latest', sessionId: 'task' });
  assert.deepEqual(ids(state.detail.items), ids(saved.items));
  assert.equal(state.detail.history_after, '');
  assert.equal(cursorId(state.detail.history_before), '201');
  bounded(state);
});

test('window snapshots preserve a huge message intact and directional cursors preserve Unicode identity', () => {
  const huge = item('巨大-🙂', '界'.repeat(ACTIVE_BYTES));
  let state = loaded([item('old'), huge], 2);
  assert.deepEqual(state.detail.items, [huge]);
  assert.equal(state.detail.items[0], huge);
  assert.equal(cursorId(state.detail.history_before), huge.id);
  bounded(state);
  state = begin(state);
  state = deliver(state, [item('前-🙂')]);
  assert.deepEqual(ids(state.detail.items), ['前-🙂']);
  assert.equal(cursorId(state.detail.history_after), '前-🙂');
  assert.equal(state.detail.recent_items[0], huge);
  state = begin(state, 'newer');
  state = deliver(state, [huge], itemCursor(huge.id));
  assert.deepEqual(state.detail.items, [huge]);
  assert.equal(state.detail.history_after, '');
});

test('UTF-16 byte bounds apply to snapshots, older pages, and growing live messages', () => {
  // Each text is 1.8 MB in UTF-16, so two fit the 4 MiB budget and three do not.
  const text = '界🙂'.repeat(300_000);
  const all = Array.from({ length: 5 }, (_, index) => item(index, text));
  let state = loaded(all, all.length);
  assert.deepEqual(ids(state.detail.items), ['3', '4']);
  assert.deepEqual(ids(state.detail.recent_items), ['3', '4']);
  bounded(state);

  state = update(state, { name: 'delta', seq: 21, item_id: '4', kind: 'assistant', text });
  assert.deepEqual(ids(state.detail.items), ['4']);
  assert.deepEqual(ids(state.detail.recent_items), ['4']);
  assert.equal(state.detail.items[0].text, text + text);
  bounded(state);

  state = fetchPage(state, all);
  assert.deepEqual(ids(state.detail.items), ['0', '1']);
  assert.equal(cursorId(state.detail.history_after), '1');
  assert.equal(state.detail.recent_items[0].text, text + text);
  bounded(state);
});

test('a long live stream keeps the historical reading window fixed and only the last recent page reusable', () => {
  const all = transcript(400);
  let state = loaded(all);
  while (state.detail.history_before) state = fetchPage(state, all);
  const reading = state.detail.items;
  const after = state.detail.history_after;
  for (let id = 400; id < 650; id++) {
    state = update(state, { name: 'item', seq: id, append: true, item: item(id) });
    assert.deepEqual(state.detail.items, reading);
    assert.equal(state.detail.history_after, after);
    bounded(state);
  }
  const expected = Array.from({ length: 50 }, (_, index) => String(600 + index));
  assert.deepEqual(ids(state.detail.recent_items), expected);
  assert.deepEqual(ids(recentProjection(state.detail).items), expected);
  state = reducer(state, { type: 'history_latest', sessionId: 'task' });
  assert.deepEqual(ids(state.detail.items), expected);
  assert.equal(cursorId(state.detail.history_before), '600');
  assert.equal(state.detail.history_after, '');
  bounded(state);
});

test('trims during page hydration remove visible, recent and incoming copies without resurrection', () => {
  const all = transcript(200);
  let state = fetchPage(loaded(all), all);
  state = begin(state);
  state = update(state, { name: 'items_trimmed', seq: 31, items: [{ id: '95' }, { id: '110' }, { id: '185' }] });
  state = deliver(state, [...all.slice(50, 100), all[110]], itemCursor('50'), '', 30);
  for (const records of [state.detail.items, state.detail.recent_items, state.detail.history_index]) {
    assert.ok(records.every(item => !['95', '110', '185'].includes(item.id)));
  }
  assert.equal(state.detail.history_truncated, true);
  assert.equal(state.historyRequest, null);
  bounded(state);
});

test('history replacement and a new service epoch discard pending pages and old windows', () => {
  const all = transcript(250);
  let state = begin(fetchPage(loaded(all), all));
  const pending = { type: 'history_loaded', sessionId: 'task', before: state.historyRequest.before, page: { seq: 30, epoch: 'epoch', items: all.slice(100, 150), before: itemCursor('100') } };
  state = update(state, { name: 'history', seq: 31, history: 'loaded', history_before: '', history_truncated: false, items: [item('replacement')], subagents: [] });
  assert.equal(reducer(state, pending), state);
  assert.deepEqual(ids(state.detail.items), ['replacement']);
  assert.deepEqual(ids(state.detail.recent_items), ['replacement']);
  assert.deepEqual(ids(state.detail.history_index), ['replacement']);
  assert.equal(state.detail.history_after, '');

  state = begin(loaded(all));
  const mismatched = { ...pending, before: state.historyRequest.before, page: { ...pending.page, epoch: 'new' } };
  assert.equal(reducer(state, mismatched), state);
  state = reducer(state, { type: 'snapshot', data: snapshot(session([item('new-instance')], '', 'new'), 1) });
  assert.equal(reducer(state, pending), state);
  assert.equal(state.historyRequest, null);
  assert.equal(state.detail.epoch, 'new');
  assert.deepEqual(ids(state.detail.history_index), ['new-instance']);
  state = update(state, { name: 'delta', seq: 2, item_id: 'new-instance', kind: 'assistant', text: ' resumed' });
  assert.equal(state.detail.items[0].text, 'message new-instance resumed');
});

test('overlapping page corrections yield to a newer live item before or during the request', () => {
  for (const timing of ['before', 'during']) {
    let state = loaded(transcript(200));
    const live = { name: 'item', seq: 25, item: item(150, 'authoritative live correction') };
    if (timing === 'before') state = update(state, live);
    state = begin(state);
    if (timing === 'during') state = update(state, live);
    state = deliver(state, [...transcript(150).slice(100), item(150, 'older page value')], itemCursor('100'), '', 20);
    assert.equal(state.detail.items.find(item => item.id === '150').text, 'authoritative live correction');
    assert.equal(state.detail.recent_items.find(item => item.id === '150').text, 'authoritative live correction');
    assert.equal(new Set(ids(state.detail.items)).size, state.detail.items.length);
  }
});

test('older and newer page fetches place a confirmed idle steer before retained later content', () => {
  const start = { id: 'start', kind: 'user', time: '2026-09-26T10:00:00Z' };
  const receipt = { id: 'steer', kind: 'user', delivery: 'steer', steer_status: 'accepted', text: 'next', time: '2026-09-26T10:00:01Z' };
  const oldAnswer = { id: 'old-answer', kind: 'assistant', text: 'live correction', time: '2026-09-26T10:00:02Z' };
  const echo = { id: 'steer', kind: 'user', text: 'next', time: '2026-09-26T10:00:03Z' };
  const newAnswer = { id: 'new-answer', kind: 'assistant', text: 'new turn', time: '2026-09-26T10:00:04Z' };
  const held = [start, receipt, oldAnswer, newAnswer];
  const page = [start, { ...oldAnswer, text: 'stale page' }, echo, newAnswer];
  for (const direction of ['older', 'newer']) {
    const boundary = itemCursor('start');
    let state = { ...initialState, selectedId: 'task', detailSeq: 10, historyItemSeq: { 'old-answer': 25 }, detail: {
      ...session(held, direction === 'older' ? boundary : ''),
      history_after: direction === 'newer' ? boundary : '',
      recent_items: held, history_index: held,
    } };
    state = begin(state, direction);
    state = deliver(state, page, '', '', 20);
    for (const items of [state.detail.items, state.detail.recent_items, state.detail.history_index]) {
      assert.deepEqual(ids(items), ['start', 'old-answer', 'steer', 'new-answer'], `${direction} page order`);
    }
    assert.equal(state.detail.items[1].text, 'live correction', `${direction} kept a newer live correction`);
    assert.equal(state.detail.items[2].time, echo.time, `${direction} used provider echo time`);
  }
});

test('an HTTP page covering future SSE updates suppresses only its own items', () => {
  let state = begin(loaded(transcript(200)));
  state = deliver(state, transcript(150).slice(100), itemCursor('100'), '', 20);
  state = update(state, { name: 'delta', seq: 15, item_id: '100', kind: 'assistant', text: ' already captured' });
  assert.equal(state.detail.items.find(item => item.id === '100').text, 'message 100');
  state = update(state, { name: 'delta', seq: 16, item_id: '199', kind: 'assistant', text: ' independent live suffix' });
  assert.equal(state.detail.items.find(item => item.id === '199').text, 'message 199 independent live suffix');
  assert.equal(state.detail.recent_items.find(item => item.id === '199').text, 'message 199 independent live suffix');
  state = update(state, { name: 'delta', seq: 21, item_id: '100', kind: 'assistant', text: ' later' });
  assert.equal(state.detail.items.find(item => item.id === '100').text, 'message 100 later');
});

test('a forward page cannot replace a newer live value held in the detached recent tail', () => {
  const all = transcript(250);
  let state = loaded(all);
  while (state.detail.history_before) state = fetchPage(state, all);
  state = update(state, { name: 'item', seq: 35, item: item(249, 'newer live final') });
  state = fetchPage(state, all, 'newer');
  state = begin(state, 'newer');
  state = deliver(state, all.slice(200), itemCursor('200'), '', 30);
  assert.equal(state.detail.items.find(item => item.id === '249').text, 'newer live final');
  assert.equal(state.detail.recent_items.find(item => item.id === '249').text, 'newer live final');
});

test('a newer forward page refreshes the latest projection before returning to latest or caching', () => {
  const all = transcript(250);
  let state = loaded(all);
  while (state.detail.history_before) state = fetchPage(state, all);
  state = fetchPage(state, all, 'newer');
  state = begin(state, 'newer');
  state = deliver(state, [...all.slice(200, 249), item(249, 'newly captured correction')], itemCursor('200'), '', 30);
  assert.equal(state.detail.items.find(item => item.id === '249').text, 'newly captured correction');
  const cache = new RecentTasks();
  cache.remember(recentProjection(state.detail));
  assert.equal(cache.get('task').items.at(-1).text, 'newly captured correction');
  state = reducer(state, { type: 'history_latest', sessionId: 'task' });
  assert.equal(state.detail.items.at(-1).text, 'newly captured correction');
});

test('a terminal forward page advances the recent tail ahead of delayed SSE append frames', () => {
  const all = transcript(300);
  let state = loaded(all.slice(0, 250));
  while (state.detail.history_before) state = fetchPage(state, all);
  while (state.detail.history_after) state = fetchPage(state, all, 'newer');
  const expected = ids(all.slice(-50));
  assert.deepEqual(ids(state.detail.recent_items), expected);
  state = update(state, { name: 'item', seq: 19, append: true, item: item(299, 'stale queued append') });
  assert.equal(state.detail.items.at(-1).text, 'message 299');
  state = update(state, { name: 'delta', seq: 21, item_id: '299', kind: 'assistant', text: ' later' });
  state = reducer(state, { type: 'history_latest', sessionId: 'task' });
  assert.deepEqual(ids(state.detail.items), expected);
  assert.equal(state.detail.items.at(-1).text, 'message 299 later');
  assert.equal(state.detail.history_after, '');
  bounded(state);
});

test('live appends during a terminal forward request do not leave a gap in the reading window', () => {
  const all = transcript(321);
  let state = loaded(all.slice(0, 300));
  while (state.detail.history_before) state = fetchPage(state, all);
  state = fetchPage(fetchPage(state, all, 'newer'), all, 'newer');
  state = begin(state, 'newer');
  for (let id = 300; id < 320; id++) state = update(state, { name: 'item', seq: id - 279, append: true, item: all[id] });
  state = deliver(state, all.slice(250, 300), itemCursor('250'), '', 20);
  state = update(state, { name: 'item', seq: 41, append: true, item: all[320] });
  const visible = state.detail.items.map(item => Number(item.id));
  for (let index = 1; index < visible.length; index++) assert.equal(visible[index], visible[index - 1] + 1);
  if (state.detail.history_after) assert.equal(cursorId(state.detail.history_after), state.detail.items.at(-1).id);
  else assert.equal(state.detail.items.at(-1).id, '320');
  const expected = ids(all.slice(-50));
  assert.deepEqual(ids(state.detail.recent_items), expected);
  state = reducer(state, { type: 'history_latest', sessionId: 'task' });
  assert.deepEqual(ids(state.detail.items), expected);
  bounded(state);
});

test('an oversized forward-page correction remains a single complete item in the recent projection', () => {
  const all = transcript(250);
  let state = loaded(all);
  while (state.detail.history_before) state = fetchPage(state, all);
  state = fetchPage(state, all, 'newer');
  state = deliver(begin(state, 'newer'), all.slice(200, 249), itemCursor('200'), itemCursor('248'), 30);
  const huge = item(249, '界'.repeat(ACTIVE_BYTES));
  state = deliver(begin(state, 'newer'), [huge], itemCursor('249'), '', 31);
  assert.deepEqual(ids(state.detail.items), ['249']);
  assert.deepEqual(ids(state.detail.recent_items), ['249']);
  assert.ok(state.detail.items[0] === huge && state.detail.recent_items[0] === huge);
  assert.equal(cursorId(state.detail.recent_before), '249');
  state = reducer(state, { type: 'history_latest', sessionId: 'task' });
  assert.deepEqual(ids(state.detail.items), ['249']);
  assert.ok(state.detail.items[0] === huge);
  bounded(state);
});

test('unknown deltas and evicted metadata updates do not recreate omitted transcript bodies', () => {
  const all = transcript(250);
  let state = loaded(all);
  while (state.detail.history_before) state = fetchPage(state, all);
  const visible = ids(state.detail.items);
  const tail = ids(state.detail.recent_items);
  state = update(state, { name: 'delta', seq: 21, item_id: 'never-loaded', kind: 'assistant', text: 'unknown body' });
  state = update(state, { name: 'delta', seq: 22, item_id: '175', kind: 'assistant', text: 'omitted body' });
  assert.deepEqual(ids(state.detail.items), visible);
  assert.deepEqual(ids(state.detail.recent_items), tail);
  assert.ok(state.detail.history_index.every(item => !item.text));
});

test('an evicted item correction updates metadata without appending an old message to the latest window', () => {
  const all = transcript(250);
  let state = fetchPage(fetchPage(loaded(all), all), all);
  state = update(state, { name: 'item', seq: 30, append: true, item: item(250) });
  const visible = ids(state.detail.items);
  const recent = ids(state.detail.recent_items);
  assert.equal(state.detail.history_after, '');
  assert.ok(!visible.includes('100'));
  assert.ok(state.detail.history_index.some(item => item.id === '100'));

  const ended_at = '2026-09-26T12:00:00Z';
  state = update(state, { name: 'item', seq: 31, item: { ...item(100, 'corrected evicted message'), ended_at } });
  assert.deepEqual(ids(state.detail.items), visible);
  assert.deepEqual(ids(state.detail.recent_items), recent);
  assert.equal(state.detail.history_index.find(item => item.id === '100').ended_at, ended_at);
  assert.equal(state.detail.history_index.find(item => item.id === '100').text, undefined);
  bounded(state);
});

const agentFrame = (state, name, seq, data = {}) => detailReducer(state, { type: 'frame', data: { name, seq, epoch: 'epoch', session_id: 'task', agent_id: 'helper', ...data } });
const agentInterests = state => detailReducer(state, { type: 'interests', bodies: [], agentId: 'helper' });
function loadedAgent(all) {
  let state = agentInterests(emptyDetails('epoch'));
  state = agentFrame(state, 'detail_snapshot', 10, { items: all.slice(-50), before: itemCursor(all.at(-50).id) });
  return agentFrame(state, 'detail_ready', 10);
}
function fetchAgentPage(state, all, direction = 'older') {
  const before = direction === 'older' ? state.agent.before : state.agent.after;
  assert.ok(before, `a ${direction} agent boundary must exist`);
  state = detailReducer(state, { type: 'page_start', agentId: 'helper', before, direction, token: 1 });
  assert.equal(state.agent.page?.before, before);
  const boundary = all.findIndex(item => item.id === cursorId(before));
  assert.ok(boundary >= 0);
  const start = direction === 'older' ? Math.max(0, boundary - 50) : boundary + 1;
  const end = direction === 'older' ? boundary : Math.min(all.length, start + 50);
  const items = all.slice(start, end);
  assert.ok(items.length);
  return detailReducer(state, { type: 'page_done', agentId: 'helper', before, token: 1, page: { seq: 20, epoch: 'epoch', representation: 'compact-v1', items, before: start ? itemCursor(items[0].id) : '', after: end < all.length ? itemCursor(items.at(-1).id) : '' } });
}
function boundedAgent(state) {
  bounded({ detail: { items: state.agent.items, recent_items: state.agent.recentItems } });
  assert.ok(state.agent.index.every(item => item.text === undefined));
}

test('subagent history traverses both directions without gaps while its detached live tail stays current', () => {
  const all = transcript(400).map(item => ({ ...item, agent_id: 'helper' }));
  let state = loadedAgent(all);
  const visited = new Set(ids(state.agent.items));
  let requests = 0;
  while (state.agent.before) {
    assert.ok(++requests <= 10);
    state = fetchAgentPage(state, all);
    boundedAgent(state);
    for (const item of state.agent.items) visited.add(item.id);
  }
  assert.deepEqual([...visited].sort((a, b) => Number(a) - Number(b)), ids(all));
  const reading = state.agent.items;
  state = agentFrame(state, 'item', 25, { item: { ...all[399], text: 'newer live final' } });
  const appended = { ...item(400), agent_id: 'helper' };
  state = agentFrame(state, 'item', 26, { append: true, item: appended });
  assert.deepEqual(state.agent.items, reading);
  assert.equal(state.agent.recentItems.find(item => item.id === '399').text, 'newer live final');

  requests = 0;
  while (state.agent.after) {
    assert.ok(++requests <= 10);
    state = fetchAgentPage(state, [...all, appended], 'newer');
    boundedAgent(state);
    const visible = state.agent.items.map(item => Number(item.id));
    for (let index = 1; index < visible.length; index++) assert.equal(visible[index], visible[index - 1] + 1);
  }
  assert.equal(state.agent.items.at(-1).id, '400');
  assert.equal(state.agent.items.find(item => item.id === '399').text, 'newer live final');
  state = detailReducer(state, { type: 'latest', agentId: 'helper' });
  assert.deepEqual(ids(state.agent.items), Array.from({ length: 50 }, (_, index) => String(351 + index)));
  assert.equal(state.agent.items.find(item => item.id === '399').text, 'newer live final');
  assert.equal(state.agent.after, '');
});

test('subagent reconnect refreshes the held range and recent tail atomically without hydrating their gap', () => {
  const all = transcript(400).map(item => ({ ...item, agent_id: 'helper' }));
  let state = loadedAgent(all);
  while (state.agent.before) state = fetchAgentPage(state, all);
  const reading = state.agent.items;
  const recent = state.agent.recentItems;
  state = agentInterests(state);
  const freshTail = all.slice(-51).filter(item => item.id !== '380').map(item => ({ ...item, text: `fresh ${item.id}` }));
  state = agentFrame(state, 'detail_snapshot', 30, { range: true, items: freshTail, before: itemCursor('349') });
  for (const start of [100, 50, 0]) {
    const items = all.slice(start, start + 50).filter(item => item.id !== '20').map(item => ({ ...item, text: `fresh ${item.id}` }));
    state = agentFrame(state, 'detail_page', 30, { scope: 'window', items, before: start ? itemCursor(String(start)) : '', after: itemCursor(String(start + 49)) });
    assert.deepEqual(state.agent.items, reading);
    assert.deepEqual(state.agent.recentItems, recent);
  }
  state = agentFrame(state, 'detail_ready', 30);
  assert.deepEqual(ids(state.agent.items), ids(all.slice(0, 150).filter(item => item.id !== '20')));
  assert.ok(state.agent.items.every(item => item.text === `fresh ${item.id}`));
  assert.deepEqual(state.agent.recentItems, freshTail);
  assert.equal(cursorId(state.agent.after), '149');
  assert.equal(state.agent.pending, undefined);
  assert.equal(state.agent.loading, false);
  assert.ok(state.agent.index.every(item => !['20', '380'].includes(item.id)), 'refreshed ranges discard metadata for trimmed records');
  assert.ok(state.agent.index.some(item => item.id === '175'), 'the intentionally skipped gap keeps compact metadata');
  boundedAgent(state);

  state = agentInterests(state);
  state = agentFrame(state, 'detail_snapshot', 31, { range: true, range_reset: true, items: freshTail, before: itemCursor('349') });
  state = agentFrame(state, 'detail_ready', 31);
  assert.deepEqual(state.agent.items, freshTail);
  assert.equal(state.agent.after, '');
  assert.equal(cursorId(state.agent.before), '349');
});

test('an oversized subagent forward-page correction keeps only that complete item in the recent tail', () => {
  const all = transcript(250).map(item => ({ ...item, agent_id: 'helper' }));
  let state = loadedAgent(all);
  while (state.agent.before) state = fetchAgentPage(state, all);
  state = fetchAgentPage(state, all, 'newer');
  const huge = { ...item(249, '界'.repeat(ACTIVE_BYTES)), agent_id: 'helper' };
  for (const page of [
    { seq: 30, items: all.slice(200, 249), before: itemCursor('200'), after: itemCursor('248') },
    { seq: 31, items: [huge], before: itemCursor('249'), after: '' },
  ]) {
    const before = state.agent.after;
    state = detailReducer(state, { type: 'page_start', agentId: 'helper', before, direction: 'newer', token: page.seq });
    state = detailReducer(state, { type: 'page_done', agentId: 'helper', before, token: page.seq, page: { ...page, epoch: 'epoch', representation: 'compact-v1' } });
  }
  assert.deepEqual(ids(state.agent.items), ['249']);
  assert.deepEqual(ids(state.agent.recentItems), ['249']);
  assert.ok(state.agent.items[0] === huge && state.agent.recentItems[0] === huge);
  assert.equal(cursorId(state.agent.recentBefore), '249');
  state = detailReducer(state, { type: 'latest', agentId: 'helper' });
  assert.deepEqual(ids(state.agent.items), ['249']);
  assert.ok(state.agent.items[0] === huge);
  boundedAgent(state);
});

test('a terminal subagent forward page advances its recent tail ahead of delayed SSE append frames', () => {
  const all = transcript(300).map(item => ({ ...item, agent_id: 'helper' }));
  let state = loadedAgent(all.slice(0, 250));
  while (state.agent.before) state = fetchAgentPage(state, all);
  while (state.agent.after) state = fetchAgentPage(state, all, 'newer');
  const expected = ids(all.slice(-50));
  assert.deepEqual(ids(state.agent.recentItems), expected);
  state = agentFrame(state, 'item', 19, { append: true, item: { ...all[299], text: 'stale queued append' } });
  assert.equal(state.agent.items.at(-1).text, 'message 299');
  state = agentFrame(state, 'delta', 21, { item_id: '299', kind: 'assistant', text: ' later' });
  state = detailReducer(state, { type: 'latest', agentId: 'helper' });
  assert.deepEqual(ids(state.agent.items), expected);
  assert.equal(state.agent.items.at(-1).text, 'message 299 later');
  assert.equal(state.agent.after, '');
  boundedAgent(state);
});

test('subagent appends during a terminal forward request do not leave a gap in its reading window', () => {
  const all = transcript(321).map(item => ({ ...item, agent_id: 'helper' }));
  let state = loadedAgent(all.slice(0, 300));
  while (state.agent.before) state = fetchAgentPage(state, all);
  state = fetchAgentPage(fetchAgentPage(state, all, 'newer'), all, 'newer');
  const before = state.agent.after;
  state = detailReducer(state, { type: 'page_start', agentId: 'helper', before, direction: 'newer', token: 30 });
  for (let id = 300; id < 320; id++) state = agentFrame(state, 'item', id - 279, { append: true, item: all[id] });
  state = detailReducer(state, { type: 'page_done', agentId: 'helper', before, token: 30, page: { seq: 20, epoch: 'epoch', representation: 'compact-v1', items: all.slice(250, 300), before: itemCursor('250'), after: '' } });
  state = agentFrame(state, 'item', 41, { append: true, item: all[320] });
  const visible = state.agent.items.map(item => Number(item.id));
  for (let index = 1; index < visible.length; index++) assert.equal(visible[index], visible[index - 1] + 1);
  if (state.agent.after) assert.equal(cursorId(state.agent.after), state.agent.items.at(-1).id);
  else assert.equal(state.agent.items.at(-1).id, '320');
  const expected = ids(all.slice(-50));
  assert.deepEqual(ids(state.agent.recentItems), expected);
  state = detailReducer(state, { type: 'latest', agentId: 'helper' });
  assert.deepEqual(ids(state.agent.items), expected);
  boundedAgent(state);
});
