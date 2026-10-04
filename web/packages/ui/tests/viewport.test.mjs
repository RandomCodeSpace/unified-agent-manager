import assert from 'node:assert/strict';
import test from 'node:test';
import { trackViewport } from '../src/lib/viewport.ts';

function fakeWindow() {
  const props = new Map();
  const dataset = {};
  const style = { setProperty: (k, v) => props.set(k, v), removeProperty: (k) => props.delete(k) };
  const vv = Object.assign(new EventTarget(), { height: 800, pageTop: 0, scale: 1 });
  const win = Object.assign(new EventTarget(), {
    innerHeight: 800,
    scrollX: 0,
    scrollY: 0,
    visualViewport: vv,
    document: { documentElement: { style, dataset } },
    scrollTo(x, y) {
      win.scrollX = x;
      win.scrollY = y;
    },
  });
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
  move({ height: 420, pageTop: 150 });
  assert.equal(props.get('--app-height'), '420px');
  assert.equal(props.get('--app-top'), '150px');
  assert.ok('keyboard' in dataset);
  move({ height: 800, pageTop: 0 });
  assert.equal(props.size, 0);
  assert.ok(!('keyboard' in dataset));
  stop();
  move({ height: 420, pageTop: 150 });
  assert.equal(props.size, 0);
});

test('a page the browser scrolled for the keyboard follows the scroll, and goes back to its top once it closes', () => {
  const { win, vv, props, move } = fakeWindow();
  trackViewport(win);
  move({ height: 420, pageTop: 0 });
  // iOS scrolls the window instead of panning the visual viewport.
  win.scrollY = 160;
  vv.pageTop = 160;
  win.dispatchEvent(new Event('scroll'));
  assert.equal(props.get('--app-top'), '160px');
  // It closes the keyboard and leaves the window scrolled.
  move({ height: 800, pageTop: 160 });
  assert.equal(win.scrollY, 0);
  assert.equal(props.size, 0);
});

test('pinch zoom and browser chrome leave the page alone', () => {
  const { win, props, move } = fakeWindow();
  trackViewport(win);
  win.scrollY = 300;
  move({ height: 400, pageTop: 300, scale: 2 });
  assert.equal(props.size, 0);
  assert.equal(win.scrollY, 300);
  win.scrollY = 0;
  move({ height: 744, pageTop: 0, scale: 1 });
  assert.equal(props.size, 0);
});
