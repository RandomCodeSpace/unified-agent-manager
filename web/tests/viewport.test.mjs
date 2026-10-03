import assert from 'node:assert/strict';
import test from 'node:test';
import { trackViewport } from '../src/lib/viewport.ts';

function fakeWindow() {
  const props = new Map();
  const dataset = {};
  const style = { setProperty: (k, v) => props.set(k, v), removeProperty: (k) => props.delete(k) };
  const vv = Object.assign(new EventTarget(), { height: 800, offsetTop: 0, scale: 1 });
  const win = { innerHeight: 800, visualViewport: vv, document: { documentElement: { style, dataset } } };
  const move = (next) => {
    Object.assign(vv, next);
    vv.dispatchEvent(new Event('resize'));
  };
  return { win, vv, props, dataset, move };
}

test('the shell follows what the keyboard leaves visible, and lets go once it closes', () => {
  const { win, props, dataset, move } = fakeWindow();
  const stop = trackViewport(win);
  assert.equal(props.size, 0);
  move({ height: 420, offsetTop: 150 });
  assert.equal(props.get('--app-height'), '420px');
  assert.equal(props.get('--app-top'), '150px');
  assert.ok('keyboard' in dataset);
  move({ height: 800, offsetTop: 0 });
  assert.equal(props.size, 0);
  assert.ok(!('keyboard' in dataset));
  stop();
  move({ height: 420, offsetTop: 150 });
  assert.equal(props.size, 0);
});

test('pinch zoom and browser chrome leave the shell alone', () => {
  const { win, props, move } = fakeWindow();
  trackViewport(win);
  move({ height: 400, offsetTop: 200, scale: 2 });
  assert.equal(props.size, 0);
  move({ height: 744, offsetTop: 0, scale: 1 });
  assert.equal(props.size, 0);
});
