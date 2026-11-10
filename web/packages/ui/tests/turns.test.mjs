import assert from 'node:assert/strict';
import test from 'node:test';
import { turnList } from '../src/lib/turns.ts';

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
