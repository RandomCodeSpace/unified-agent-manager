import assert from 'node:assert/strict';
import test from 'node:test';
import { MOTION_KEY, defaultMotion, parseMotion } from '../src/lib/motion.ts';

test('a stored choice wins; nothing stored, or garbage, is the default', () => {
  assert.equal(MOTION_KEY, 'uam.motion');
  assert.equal(parseMotion(null), 'on');
  assert.equal(parseMotion('on'), 'on');
  assert.equal(parseMotion('system'), 'system');
  assert.equal(parseMotion('reduce'), 'on');
  assert.equal(parseMotion(''), 'on');
  assert.equal(parseMotion(null, 'system'), 'system');
  assert.equal(parseMotion('garbage', 'system'), 'system');
  assert.equal(parseMotion('on', 'system'), 'on');
});

test('the default matches the system on a coarse pointer (phones) and is on elsewhere', (t) => {
  const had = Object.hasOwn(globalThis, 'matchMedia');
  const original = globalThis.matchMedia;
  t.after(() => {
    if (had) globalThis.matchMedia = original;
    else delete globalThis.matchMedia;
  });
  delete globalThis.matchMedia;
  assert.equal(defaultMotion(), 'on');
  globalThis.matchMedia = (query) => ({ matches: query === '(pointer: coarse)' });
  assert.equal(defaultMotion(), 'system');
  globalThis.matchMedia = () => ({ matches: false });
  assert.equal(defaultMotion(), 'on');
});
