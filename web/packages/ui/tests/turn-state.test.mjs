import assert from 'node:assert/strict';
import test from 'node:test';
// api.ts (LIVE) does not load under node's type stripping; its value, as the status line reads it.
const LIVE = ['starting', 'working', 'awaiting_permission', 'awaiting_answer'];
import { isRealUserTurn, openCall, turnBusy, turnState } from '../src/lib/turnState.ts';

const STATES = ['working', 'completed', 'cancelled', 'failed', 'unknown'];
const SESSION_STATES = ['starting', 'working', 'awaiting_permission', 'awaiting_answer', 'idle', 'interrupted', 'cancelled', 'failed', 'completed', 'closed'];
const timing = (state, ended) => ({ id: 't1', started_at: '2026-01-01T00:00:00Z', state, ...(ended ? { ended_at: '2026-01-01T00:00:05Z' } : {}) });

test('turnState: no timing is idle, whatever the stream', () => {
  for (const connected of [true, false]) assert.equal(turnState(undefined, connected), 'idle');
});

test('turnState: every timing state, with and without an end, connected or not', () => {
  for (const state of STATES) {
    for (const ended of [true, false]) {
      for (const connected of [true, false]) {
        const want = ended ? 'ended' : state === 'unknown' && connected ? 'cut' : 'running';
        assert.equal(turnState(timing(state, ended), connected), want, `${state} ended=${ended} connected=${connected}`);
      }
    }
  }
});

test('turnState: an empty end is no end', () => {
  assert.equal(turnState({ ...timing('working'), ended_at: '' }, true), 'running');
  assert.equal(turnState({ ...timing('unknown'), ended_at: '' }, true), 'cut');
});

// The status line's rule (#434): the last timing ended or was cut off while the Thread is live, so it belongs to the last turn.
test('turnState: the status line rule, for every timing state x connected x Thread state', () => {
  for (const state of [undefined, ...STATES]) {
    for (const ended of [true, false]) {
      for (const connected of [true, false]) {
        for (const sessionState of SESSION_STATES) {
          const turn = state && timing(state, ended);
          const before = !!((turn?.ended_at || (connected && turn?.state === 'unknown')) && LIVE.includes(sessionState));
          const s = turnState(turn, connected);
          assert.equal((s === 'ended' || s === 'cut') && LIVE.includes(sessionState), before, `${state} ended=${ended} connected=${connected} ${sessionState}`);
        }
      }
    }
  }
});

// The transcript's rule (#434): a turn is open until it ends, or until the service cut it off while the stream is up.
test('turnState: the transcript rule, for every timing state x connected', () => {
  for (const state of [undefined, ...STATES]) {
    for (const ended of [true, false]) {
      for (const connected of [true, false]) {
        const t = state && timing(state, ended);
        const cut = connected && t?.state === 'unknown' && !t.ended_at;
        const s = turnState(t, connected);
        assert.equal(s === 'cut', cut, `${state} ended=${ended} connected=${connected}`);
        assert.equal(s === 'idle' || s === 'running', !(t?.ended_at || cut), `${state} ended=${ended} connected=${connected}`);
      }
    }
  }
});

test('isRealUserTurn: an ordinary user message, not a steer or an autopilot one', () => {
  assert.equal(isRealUserTurn({ kind: 'user' }), true);
  assert.equal(isRealUserTurn({ kind: 'user', delivery: '' }), true);
  assert.equal(isRealUserTurn({ kind: 'user', delivery: 'steer' }), false);
  assert.equal(isRealUserTurn({ kind: 'user', delivery: 'autopilot' }), false);
  for (const kind of ['assistant', 'reasoning', 'tool', 'system']) assert.equal(isRealUserTurn({ kind }), false);
});

test('openCall: pending and running only', () => {
  assert.equal(openCall('pending'), true);
  assert.equal(openCall('running'), true);
  for (const status of ['completed', 'failed', undefined, '']) assert.equal(openCall(status), false);
});

test('turnBusy: starting and working only', () => {
  for (const state of SESSION_STATES) assert.equal(turnBusy(state), state === 'starting' || state === 'working', state);
});
