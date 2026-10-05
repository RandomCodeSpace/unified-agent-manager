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
  const opened = [];
  const live = new Map();
  const clients = windows.map((w) => ({
    visibilityState: w.visible ? 'visible' : 'hidden',
    frameType: 'top-level',
    focus: async () => {},
    // A page answers "which Task do you show" with the one it shows.
    postMessage: (msg, ports) => {
      posted.push(msg);
      if (msg.type === 'uam-viewing') ports[0].postMessage({ task: w.task, visible: w.visible });
    },
  }));
  const self = {
    addEventListener: (name, fn) => { listeners[name] = fn; },
    skipWaiting: () => {},
    clients: { matchAll: async () => clients, openWindow: async (url) => { opened.push(url); } },
    registration: { showNotification: async (title, options) => { shown.push({ title, options }); live.set(options.tag, { data: options.data }); }, getNotifications: async ({ tag }) => live.has(tag) ? [live.get(tag)] : [] },
    navigator: { userAgent, setAppBadge: async (n) => { badges.push(n); }, clearAppBadge: async () => { badges.push(0); } },
  };
  class MessageChannel {
    constructor() {
      this.port1 = {};
      this.port2 = { postMessage: (data) => this.port1.onmessage?.({ data }) };
    }
  }
  vm.runInNewContext(source, { self, navigator: self.navigator, MessageChannel, setTimeout, clearTimeout, Promise, URLSearchParams });
  return {
    shown, badges, posted, opened,
    click: async (data) => {
      let done;
      listeners.notificationclick({ notification: { data, close() {} }, waitUntil: (p) => { done = p; } });
      await done;
    },
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


test('equal Task IDs from different sources have independent cards and qualified clicks', async () => {
  const sw = worker([]);
  const remote = { ...notice, home_id: 'a', instance_id: 'b', connection_id: 'saved-b', generation: 3 };
  await sw.push(remote);
  await sw.push({ ...remote, instance_id: 'c', connection_id: 'saved-c', key: 'c:epoch:1' });
  assert.deepEqual(sw.shown.map((n) => n.options.tag), ['uam-task:b:t1', 'uam-task:c:t1']);
  await sw.click(sw.shown[0].options.data);
  assert.deepEqual(sw.opened, ['/#task=t1&home=a&instance=b&connection=saved-b&generation=3']);
  const open = worker([{ visible: true }]);
  await open.click(sw.shown[1].options.data);
  assert.equal(open.posted[0].connection_id, 'saved-c');
  assert.equal(open.posted[0].instance_id, 'c');
  assert.equal(open.posted[0].generation, 3);
});

test('a duplicate push still shows visibly but does not request a repeated alert', async () => {
  const sw = worker([]);
  await sw.push(notice);
  await sw.push(notice);
  await sw.push({ ...notice, key: 'next-event' });
  assert.deepEqual(sw.shown.map((n) => n.options.renotify), [true, false, true]);
});

test('partial background coverage uses an unnumbered badge and legacy clicks still work', async () => {
  const sw = worker([]);
  await sw.push({ ...notice, badge_partial: true });
  assert.deepEqual(sw.badges, [undefined]);
  await sw.click({ task: 'old-task' });
  assert.deepEqual(sw.opened, ['/#task=old-task']);
  await sw.click({ task: 'old-task', connection_id: 'incomplete' });
  assert.deepEqual(sw.opened, ['/#task=old-task', '/']);
});
