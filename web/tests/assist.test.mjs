import assert from 'node:assert/strict';
import test from 'node:test';
import { insertAt, suggestionKey } from '../src/lib/assist.ts';

test('an inserted text sits on its own line at the caret', () => {
  assert.deepEqual(insertAt('', 0, 'go'), { text: 'go', caret: 2 });
  assert.deepEqual(insertAt('ab', 1, 'go'), { text: 'a\ngo\nb', caret: 4 });
  assert.deepEqual(insertAt('a\n', 2, 'go'), { text: 'a\ngo', caret: 4 });
});

test('replies are suggested only after a completed turn that ends with the answer', () => {
  const items = [{ id: 'u', kind: 'user' }, { id: 'a', kind: 'assistant' }, { id: 's', kind: 'assistant', agent_id: 'sub' }];
  const done = { stage: 'active', state: 'completed', queued: 0, pending: 0 };
  assert.equal(suggestionKey(done, items), 'a');
  assert.equal(suggestionKey({ ...done, state: 'working' }, items), '');
  assert.equal(suggestionKey({ ...done, pending: 1 }, items), '');
  assert.equal(suggestionKey({ ...done, queued: 1 }, items), '');
  assert.equal(suggestionKey({ ...done, stage: 'settled' }, items), '');
  assert.equal(suggestionKey(done, [...items, { id: 't', kind: 'tool' }]), '');
  assert.equal(suggestionKey(done, [...items, { id: 'r', kind: 'reasoning' }]), 'a');
});
