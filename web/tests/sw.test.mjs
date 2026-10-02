import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const source = readFileSync(new URL('../public/sw.js', import.meta.url), 'utf8');

/** Runs public/sw.js against fake window clients; returns its push handler's effects. */
function worker(windows, userAgent = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36') {
  const listeners = {};
  const shown = [];
  const badges = [];
  const posted = [];
  const clients = windows.map((w) => ({
    visibilityState: w.visible ? 'visible' : 'hidden',
    frameType: 'top-level',
    // A page answers "which Task do you show" with the one it shows.
    postMessage: (msg, ports) => {
      posted.push(msg);
      if (msg.type === 'uam-viewing') ports[0].postMessage({ task: w.task, visible: w.visible });
    },
  }));
  const self = {
    addEventListener: (name, fn) => { listeners[name] = fn; },
    skipWaiting: () => {},
    clients: { matchAll: async () => clients },
    registration: { showNotification: async (title, options) => { shown.push({ title, options }); } },
    navigator: { userAgent, setAppBadge: async (n) => { badges.push(n); }, clearAppBadge: async () => { badges.push(0); } },
  };
  class MessageChannel {
    constructor() {
      this.port1 = {};
      this.port2 = { postMessage: (data) => this.port1.onmessage?.({ data }) };
    }
  }
  vm.runInNewContext(source, { self, navigator: self.navigator, MessageChannel, setTimeout, clearTimeout, Promise });
  return {
    shown, badges, posted,
    push: async (notice) => {
      let done;
      listeners.push({ data: { json: () => notice }, waitUntil: (p) => { done = p; } });
      await done;
    },
  };
}

const notice = { title: 'Fix it finished', task: 't1', kind: 'finished', key: 't1:finished:9', badge: 2 };

test('every push shows its notification, even with the Task on a visible page (the service sends none for that)', async () => {
  const sw = worker([{ visible: true, task: 't1' }]);
  await sw.push(notice);
  assert.deepEqual(sw.shown.map((n) => n.title), ['Fix it finished']);
  assert.equal(sw.shown[0].options.tag, 'uam-task-t1');
  assert.ok(sw.posted.some((m) => m.type === 'uam-shown' && m.key === 't1:finished:9'));
});

test('the pushed badge applies only while no page is visible: a visible page keeps its own count', async () => {
  const open = worker([{ visible: true, task: 't2' }]);
  await open.push(notice);
  assert.deepEqual(open.badges, []);
  const away = worker([{ visible: false, task: 't2' }]);
  await away.push(notice);
  assert.deepEqual(away.badges, [2]);
  const closed = worker([]);
  await closed.push({ ...notice, badge: 0 });
  assert.deepEqual(closed.badges, [0]);
});
