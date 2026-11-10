import assert from 'node:assert/strict';
import test from 'node:test';
import { applyLayout, currentLayout, parseLayout, subscribeLayout } from '../src/lib/layout.ts';

test('a stored layout is the notebook only when it says so', () => {
  assert.equal(parseLayout('notebook'), 'notebook');
  assert.equal(parseLayout('classic'), 'classic');
  assert.equal(parseLayout(null), 'classic');
  assert.equal(parseLayout('garbage'), 'classic');
});

test('a change of layout reaches its listeners once', () => {
  applyLayout('classic');
  let calls = 0;
  const stop = subscribeLayout(() => calls++);
  applyLayout('notebook');
  applyLayout('notebook');
  assert.equal(currentLayout(), 'notebook');
  assert.equal(calls, 1);
  stop();
  applyLayout('classic');
  assert.equal(calls, 1);
});
