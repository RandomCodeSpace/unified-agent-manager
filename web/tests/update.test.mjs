import assert from 'node:assert/strict';
import test from 'node:test';
import { CHECK_GAP_MS, checkDue, decideUpdate } from '../src/lib/update.ts';

const clear = { draft: false, popup: false };

test('the same version, or none yet, is not an update', () => {
  assert.equal(decideUpdate('v0.9.0', 'v0.9.0', clear), 'none');
  assert.equal(decideUpdate(undefined, 'v0.9.1', clear), 'none');
  assert.equal(decideUpdate('v0.9.0', undefined, clear), 'none');
  assert.equal(decideUpdate('v0.9.0', 'v0.9.0', { draft: true, popup: true }), 'none');
});

test('a new version reloads when nothing would be lost', () => {
  assert.equal(decideUpdate('v0.9.0', 'v0.9.1', clear), 'reload');
  assert.equal(decideUpdate('v0.9.1', 'v0.9.0', clear), 'reload');
});

test('a draft or an open popup turns the reload into an offer', () => {
  assert.equal(decideUpdate('v0.9.0', 'v0.9.1', { draft: true, popup: false }), 'offer');
  assert.equal(decideUpdate('v0.9.0', 'v0.9.1', { draft: false, popup: true }), 'offer');
  assert.equal(decideUpdate('v0.9.0', 'v0.9.1', { draft: true, popup: true }), 'offer');
});

test('visibility checks are throttled to the gap', () => {
  assert.equal(checkDue(1_000, 1_000 + CHECK_GAP_MS - 1), false);
  assert.equal(checkDue(1_000, 1_000 + CHECK_GAP_MS), true);
  assert.equal(checkDue(0, 5_000, 5_000), true);
});
