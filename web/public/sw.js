// UAM's service worker: Web Push notices and notification clicks, nothing else. It has
// no fetch handler and caches nothing, so the app, its assets and the API always come
// from the service (src/lib/notify.ts registers it; docs/web.md, Notifications).

// Safari revokes a push subscription whose pushes show no notification, so there every
// push is shown, even for the Task on screen.
const mustShow = /AppleWebKit/.test(navigator.userAgent) && !/Chrome|Chromium|Edg/.test(navigator.userAgent);

self.addEventListener('install', () => self.skipWaiting());

/** Whether a visible page shows this Task: the page answers over a message channel. */
function viewing(client, task) {
  return new Promise((resolve) => {
    const channel = new MessageChannel();
    const timer = setTimeout(() => resolve(false), 500);
    channel.port1.onmessage = (event) => {
      clearTimeout(timer);
      resolve(!!event.data && event.data.visible === true && event.data.task === task);
    };
    client.postMessage({ type: 'uam-viewing' }, [channel.port2]);
  });
}

async function setBadge(count) {
  if (typeof count !== 'number' || !self.navigator.setAppBadge) return;
  try {
    if (count > 0) await self.navigator.setAppBadge(count);
    else await self.navigator.clearAppBadge();
  } catch {
    // The badge is a hint; a platform that refuses it changes nothing else.
  }
}

self.addEventListener('push', (event) => {
  let notice = null;
  try {
    notice = event.data ? event.data.json() : null;
  } catch {
    notice = null;
  }
  if (!notice || typeof notice.title !== 'string' || typeof notice.task !== 'string') return;
  event.waitUntil(
    (async () => {
      await setBadge(notice.badge);
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      if (!mustShow) {
        const shown = await Promise.all(windows.filter((c) => c.visibilityState === 'visible').map((c) => viewing(c, notice.task)));
        // The page showing the Task took the notice itself.
        if (shown.includes(true)) return;
      }
      await self.registration.showNotification(notice.title, {
        tag: `uam-task-${notice.task}`,
        renotify: true,
        icon: '/icon-192.png',
        data: { task: notice.task },
      });
      // Open pages wait a moment for the push before showing a notice themselves: it came.
      if (typeof notice.key === 'string') for (const c of windows) c.postMessage({ type: 'uam-shown', key: notice.key });
    })(),
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const task = event.notification.data && typeof event.notification.data.task === 'string' ? event.notification.data.task : '';
  event.waitUntil(
    (async () => {
      const windows = (await self.clients.matchAll({ type: 'window', includeUncontrolled: true })).filter((c) => c.frameType !== 'nested');
      const client = windows.find((c) => c.focused) || windows[0];
      if (client) {
        try {
          await client.focus();
        } catch {
          // Focus is the browser's call; the Task still opens in that window.
        }
        if (task) client.postMessage({ type: 'uam-open', task });
        return;
      }
      await self.clients.openWindow(task ? `/#task=${encodeURIComponent(task)}` : '/');
    })(),
  );
});
