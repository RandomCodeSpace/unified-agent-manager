import assert from 'node:assert/strict';
import test from 'node:test';
import { threadTurns, turnList } from '../src/lib/turns.ts';

const at = (minute) => `2026-11-10T10:${String(minute).padStart(2, '0')}:00Z`;
const user = (id, minute, text, extra = {}) => ({ id, kind: 'user', time: at(minute), text, ...extra });
const reply = (id, minute) => ({ id, kind: 'assistant', time: at(minute), text: 'ok' });
const timing = (n, message, minute, state) => ({ id: `tt${n}`, user_item_id: message, started_at: at(minute), state, ...(state === 'working' ? {} : { ended_at: at(minute + 1) }) });
const rows = (detail) => turnList(detail).map((t) => [t.number, t.key, t.text, t.word]);

test('turns are the messages in order, numbered by their timing, with a word for how each stands', () => {
  const detail = {
    state: 'working',
    items: [user('u1', 1, 'First\nsecond line'), reply('a1', 2), user('u2', 3, 'Second'), reply('a2', 4), user('u3', 5, 'Third'), reply('a3', 6), user('u4', 7, 'Fourth'), user('s1', 8, 'A steer', { delivery: 'immediate' })],
    turn_timings: [timing(1, 'u1', 1, 'completed'), timing(2, 'u2', 3, 'failed'), timing(3, 'u3', 5, 'cancelled'), timing(4, 'u4', 7, 'working')],
    queue: [{ request_id: 'q1', text: 'Then this', queued_at: at(9) }],
  };
  assert.deepEqual(rows(detail), [
    [1, 'u1', 'First', 'Done'],
    [2, 'u2', 'Second', 'Failed'],
    [3, 'u3', 'Third', 'Stopped'],
    [4, 'u4', 'Fourth', 'Running'],
    [5, 'queue:q1', 'Then this', 'Queued'],
  ]);
  assert.equal(turnList(detail).at(-1).queued, true);
});

test('the running turn of a Thread that waits on you asked you; one left open by a Thread that stopped was cut off', () => {
  const detail = { items: [user('u1', 1, 'Go')], turn_timings: [timing(1, 'u1', 1, 'working')] };
  assert.equal(turnList({ ...detail, state: 'awaiting_permission' })[0].word, 'Asked you');
  assert.equal(turnList({ ...detail, state: 'awaiting_answer' })[0].word, 'Asked you');
  assert.equal(turnList({ ...detail, state: 'interrupted' })[0].word, 'Stopped');
});

test('the outline names messages of pages not loaded, and a timing without its message keeps its row', () => {
  const detail = {
    state: 'completed',
    outline: [user('u1', 1, 'From the outline'), user('u3', 5, 'Loaded')],
    history_index: [user('u3', 5, 'Loaded'), reply('a3', 6)],
    items: [],
    turn_timings: [timing(1, 'u1', 1, 'completed'), timing(2, 'u2', 3, 'completed'), timing(3, 'u3', 5, 'completed')],
  };
  assert.deepEqual(rows(detail), [
    [1, 'u1', 'From the outline', 'Done'],
    [2, 'u2', '', 'Done'],
    [3, 'u3', 'Loaded', 'Done'],
  ]);
});

test('a Thread without timings numbers its turns by place and words the last from its own state', () => {
  const detail = { state: 'completed', items: [user('u1', 1, 'One'), reply('a1', 2), user('u2', 3, 'Two'), reply('a2', 4)] };
  assert.deepEqual(rows(detail), [
    [1, 'u1', 'One', undefined],
    [2, 'u2', 'Two', 'Done'],
  ]);
  assert.equal(turnList({ ...detail, state: 'working' })[1].word, 'Running');
});

test('what came before the first message is the start turn', () => {
  const detail = { state: 'completed', items: [reply('a0', 1), user('u1', 2, 'One'), reply('a1', 3)], turn_timings: [{ id: 'tt0', started_at: at(1), ended_at: at(2), state: 'completed' }, timing(2, 'u1', 2, 'completed')] };
  assert.deepEqual(rows(detail), [
    [1, 'start', '', 'Done'],
    [2, 'u1', 'One', 'Done'],
  ]);
  // Not at the Thread's beginning: the window's first row belongs to an earlier message.
  assert.equal(turnList({ ...detail, history_before: 'cursor' })[0].key, 'u1');
});

// The Contents list draws `turns`; a cell looks its turn up in the same `turns` by its reply key
// (Transcript); the composer's cell says `next`. One derivation, so these hold for every Thread.
test('the list, the cells and the next turn agree, with and without turn timings', () => {
  const items = [user('u1', 1, 'One'), reply('a1', 2), user('u2', 3, 'Two'), reply('a2', 4), user('u3', 5, 'Three')];
  const queue = [{ request_id: 'q1', text: 'Four', queued_at: at(6) }, { request_id: 'q2', text: 'Five', queued_at: at(7) }];
  const timings = [timing(1, 'u1', 1, 'completed'), timing(2, 'u2', 3, 'completed'), timing(3, 'u3', 5, 'working')];
  const fixtures = {
    'with timings': { state: 'working', items, turn_timings: timings },
    'with timings and a queue': { state: 'working', items, turn_timings: timings, queue },
    'without timings': { state: 'completed', items },
    'without timings, with a queue': { state: 'working', items, queue },
    'with timings, only the last page loaded': { state: 'working', outline: items.filter((i) => i.kind === 'user'), history_index: items.slice(4), history_before: 'cursor', items: items.slice(4), turn_timings: timings },
    'without timings, only the last page loaded': { state: 'completed', outline: items.filter((i) => i.kind === 'user'), history_index: items.slice(4), history_before: 'cursor', items: items.slice(4) },
    'no turn yet': { state: 'idle', items: [] },
  };
  for (const [name, detail] of Object.entries(fixtures)) {
    const { turns, next } = threadTurns(detail);
    const listed = turns.map((t) => t.number);
    // The list counts every turn from 1, queued messages after those that ran.
    assert.deepEqual(listed, turns.map((_, i) => i + 1), name);
    // A cell reads the list's own row: each key names one turn, so its number and word are the list's.
    const cells = new Map(turns.map((t) => [t.key, t]));
    assert.equal(cells.size, turns.length, name);
    for (const t of turns) assert.deepEqual([cells.get(t.key).number, cells.get(t.key).word], [t.number, t.word], name);
    // The composer's cell takes the number after the last one listed.
    assert.equal(next, turns.length + 1, name);
  }
  // The last turn says how the Thread stands, also without a timing for it.
  assert.equal(turnList(fixtures['without timings']).at(-1).word, 'Done');
  assert.equal(turnList(fixtures['without timings, only the last page loaded']).find((t) => t.key === 'u3').number, 3);
});

test('a message whose timing has not landed yet has no number, and the next turn takes the one after the timings', () => {
  const detail = { state: 'working', items: [user('u1', 1, 'One'), reply('a1', 2), user('u2', 3, 'Two')], turn_timings: [timing(1, 'u1', 1, 'completed')] };
  assert.deepEqual(threadTurns(detail).turns.map((t) => t.number), [1, undefined]);
  assert.equal(threadTurns(detail).next, 2);
});

test('a message the index names without its words takes them from the loaded page', () => {
  const detail = { state: 'completed', history_index: [{ id: 'u1', kind: 'user', time: at(1) }, { id: 'u2', kind: 'user', time: at(3) }], items: [user('u2', 3, 'Loaded words')] };
  assert.deepEqual(turnList(detail).map((t) => t.text), ['', 'Loaded words']);
});
