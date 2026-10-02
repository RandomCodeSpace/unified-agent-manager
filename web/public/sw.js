// UAM's service worker: Web Push notices and notification clicks, nothing else. It has
// no fetch handler and caches nothing, so the app, its assets and the API always come
// from the service (src/lib/notify.ts registers it; docs/web.md, Notifications).

// Every push is shown: a push without a notification makes Safari revoke the subscription
// and Chrome show its own "updated in the background" notice. The service sends none for a
// Task a visible page shows (POST /api/viewing), so nothing here asks the pages first.

self.addEventListener('install', () => self.skipWaiting());

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
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      // A visible page keeps the badge at its own count (it knows this browser's unread marks); otherwise the service's applies.
      if (!windows.some((c) => c.visibilityState === 'visible')) await setBadge(notice.badge);
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
