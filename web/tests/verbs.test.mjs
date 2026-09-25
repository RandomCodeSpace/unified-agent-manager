import assert from 'node:assert/strict';
import test from 'node:test';
import { VERBS, turnVerb } from '../src/lib/verbs.ts';

test('the list is curated: distinct gerunds, none of the words that mean something else', () => {
  assert.equal(new Set(VERBS).size, VERBS.length);
  assert.ok(VERBS.length >= 20);
  for (const verb of VERBS) {
    assert.match(verb, /^[A-Z][a-z]+ing( [a-z]+)?$/);
    assert.ok(!['Working', 'Thinking', 'Running'].includes(verb), verb);
  }
});

test('the same turn always gets the same verb, from the list', () => {
  for (const key of ['start', 'i1', 'msg_01HZX', '']) {
    const verb = turnVerb(key);
    assert.ok(VERBS.includes(verb), verb);
    assert.equal(turnVerb(key), verb);
    assert.equal(turnVerb(`${key}`), verb);
  }
});

test('different turns spread over most of the list', () => {
  const seen = new Set();
  for (let i = 0; i < 200; i++) seen.add(turnVerb(`msg_${i.toString(36)}`));
  assert.ok(seen.size >= VERBS.length - 2, `${seen.size} of ${VERBS.length}`);
  assert.notEqual(turnVerb('i1'), turnVerb('i2'));
});
