// UAM's service worker: Web Push notices and notification clicks, nothing else. It has
// no fetch handler and caches nothing, so the app, its assets and the API always come
// from the service (src/lib/notify.ts registers it; docs/web.md, Notifications).

// Every push is shown: a push without a notification makes Safari revoke the subscription
// and Chrome show its own "updated in the background" notice. The service sends none for a
// Task a visible page shows (POST /api/viewing), so nothing here asks the pages first.

self.addEventListener('install', () => self.skipWaiting());

async function setBadge(count, partial) {
  if (!self.navigator.setAppBadge || !partial && typeof count !== 'number') return;
  try {
    if (partial) await self.navigator.setAppBadge();
    else if (count > 0) await self.navigator.setAppBadge(count);
    else await self.navigator.clearAppBadge();
  } catch {
    // The badge is a hint; a platform that refuses it changes nothing else.
  }
}


// Keep the full source identity through warm and cold clicks. An incomplete
// connected target is refused instead of falling back to a same-named local Task.
function targetOf(notice) {
  if (!notice || typeof notice.task !== 'string' || !notice.task) return null;
  const remote = ['home_id', 'instance_id', 'connection_id', 'generation'].some((key) => key in notice);
  if (!remote) return { task: notice.task };
  if (typeof notice.home_id !== 'string' || !notice.home_id || typeof notice.instance_id !== 'string' || !notice.instance_id ||
      typeof notice.connection_id !== 'string' || !notice.connection_id || !Number.isSafeInteger(notice.generation) || notice.generation <= 0) return null;
  return { task: notice.task, home_id: notice.home_id, instance_id: notice.instance_id, connection_id: notice.connection_id, generation: notice.generation };
}

function targetURL(target) {
  const params = new URLSearchParams({ task: target.task });
  if (target.connection_id) {
    params.set('home', target.home_id);
    params.set('instance', target.instance_id);
    params.set('connection', target.connection_id);
    params.set('generation', String(target.generation));
  }
  return `/#${params}`;
}

self.addEventListener('push', (event) => {
  let notice = null;
  try {
    notice = event.data ? event.data.json() : null;
  } catch {
    notice = null;
  }
  const target = targetOf(notice);
  if (!target || typeof notice.title !== 'string') return;
  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      // A visible page keeps the badge at its own count (it knows this browser's unread marks); otherwise the service's applies.
      if (!windows.some((c) => c.visibilityState === 'visible')) await setBadge(notice.badge, notice.badge_partial === true);
      const tag = target.instance_id ? `uam-task:${target.instance_id}:${target.task}` : `uam-task-${target.task}`;
      const existing = await self.registration.getNotifications?.({ tag }) ?? [];
      const repeated = typeof notice.key === 'string' && existing.some((note) => note.data?.key === notice.key);
      await self.registration.showNotification(notice.title, {
        tag,
        renotify: !repeated,
        icon: '/icon-192.png',
        data: { ...target, ...(typeof notice.key === 'string' ? { key: notice.key } : {}) },
      });
      // Open pages wait a moment for the push before showing a notice themselves: it came.
      if (typeof notice.key === 'string') for (const c of windows) c.postMessage({ type: 'uam-shown', key: notice.key, ...(target.home_id ? { home_id: target.home_id } : {}) });
    })(),
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const target = targetOf(event.notification.data);
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
        if (target) client.postMessage({ type: 'uam-open', ...target });
        return;
      }
      await self.clients.openWindow(target ? targetURL(target) : '/');
    })(),
  );
});
