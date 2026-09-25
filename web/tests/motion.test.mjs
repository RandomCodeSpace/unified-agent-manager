import assert from 'node:assert/strict';
import test from 'node:test';
import { MOTION_KEY, parseMotion } from '../src/lib/motion.ts';

test('motion is on unless the browser stored "Match system"', () => {
  assert.equal(MOTION_KEY, 'uam.motion');
  assert.equal(parseMotion(null), 'on');
  assert.equal(parseMotion('on'), 'on');
  assert.equal(parseMotion('system'), 'system');
  assert.equal(parseMotion('reduce'), 'on');
  assert.equal(parseMotion(''), 'on');
});
