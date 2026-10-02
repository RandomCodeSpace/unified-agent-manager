// Notifications when a Task needs you or finishes, turned on per browser in Settings (`uam.notify`).
// Where Web Push works the service worker (public/sw.js) shows them, with no page open too:
// mode `push`; an open page still shows a notice whose push has not come within PUSH_GRACE_MS
// (the service may not reach the push service). Where push does not work an open page shows the
// service's `notify` events itself: mode `page`. Either way the open Task on a visible page is
// not announced (the page tells the service which Task it shows, so no push goes out for it),
// each notice shows once across tabs, and nothing here surfaces an error beyond the Settings switch.
import { api } from '../api';

export const NOTIFY_KEY = 'uam.notify';
/** The notices a page already showed or skipped, by key, so other tabs skip them (`claimNotice`). */
export const SEEN_KEY = 'uam.notified';
const SEEN_FOR_MS = 10 * 60 * 1000;
const SUBSCRIBE_WAIT_MS = 15_000;
/** How long an open page in push mode waits for the push before showing a notice itself. */
export const PUSH_GRACE_MS = 6000;

export type NotifyMode = 'push' | 'page';

/** The service's `notify` event: a Task moved into needing you, or finished. */
export interface Notice {
  seq: number;
  session_id: string;
  kind: 'question' | 'permission' | 'failed' | 'finished';
  title: string;
  /** The notice's name in every tab and in its push, made by the service. */
  key: string;
}

/** A stored value as a mode; anything else is off. */
export function parseNotifyMode(raw: string | null): NotifyMode | null {
  return raw === 'push' || raw === 'page' ? raw : null;
}

/** Takes `key` for this tab: the record to store (older entries dropped), or null when another tab took it. */
export function claimNotice(seen: Readonly<Record<string, number>>, key: string, now: number): Record<string, number> | null {
  if (key in seen && now - seen[key] < SEEN_FOR_MS) return null;
  const next: Record<string, number> = {};
  for (const [k, at] of Object.entries(seen)) if (now - at < SEEN_FOR_MS) next[k] = at;
  next[key] = now;
  return next;
}

/** An iPhone or iPad browser tab, where notifications need the Home Screen app. */
export function needsHomeScreen(ua: string, touchPoints: number, standalone: boolean): boolean {
  const ios = /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && touchPoints > 1);
  return ios && !standalone;
}

export type NotifySupport = 'ok' | 'home-screen' | 'none';

export function notifySupport(): NotifySupport {
  const standalone = window.matchMedia?.('(display-mode: standalone)').matches || (navigator as { standalone?: boolean }).standalone === true;
  if (needsHomeScreen(navigator.userAgent, navigator.maxTouchPoints ?? 0, standalone)) return 'home-screen';
  return typeof Notification === 'undefined' ? 'none' : 'ok';
}

/** The stored mode while the browser still allows notifications; null when off. */
export function loadNotifyMode(): NotifyMode | null {
  try {
    if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return null;
    return parseNotifyMode(localStorage.getItem(NOTIFY_KEY));
  } catch {
    return null;
  }
}

function saveNotifyMode(mode: NotifyMode | null): void {
  try {
    if (mode) localStorage.setItem(NOTIFY_KEY, mode);
    else localStorage.removeItem(NOTIFY_KEY);
  } catch {
    // Storage refused: notifications stay as they were for this page.
  }
}

function keyBytes(base64url: string): Uint8Array<ArrayBuffer> {
  const b64 = base64url.replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4));
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

function sameKey(a: ArrayBuffer | null | undefined, b: Uint8Array): boolean {
  if (!a || a.byteLength !== b.length) return false;
  const x = new Uint8Array(a);
  return x.every((v, i) => v === b[i]);
}

function within<T>(promise: Promise<T>, ms: number): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('timed out')), ms);
    promise.then(
      (v) => { clearTimeout(timer); resolve(v); },
      (e: unknown) => { clearTimeout(timer); reject(e instanceof Error ? e : new Error(String(e))); },
    );
  });
}

/** The service worker's registration, registering it first; null where there is none (or it is refused). */
async function worker(): Promise<ServiceWorkerRegistration | null> {
  if (!('serviceWorker' in navigator)) return null;
  try {
    await navigator.serviceWorker.register('/sw.js', { scope: '/' });
    return await within(navigator.serviceWorker.ready, SUBSCRIBE_WAIT_MS);
  } catch {
    return null;
  }
}

/** Subscribes this browser to the service's pushes (again: the call is idempotent); false where push is unavailable. */
async function subscribePush(): Promise<boolean> {
  const reg = await worker();
  if (!reg || !('pushManager' in reg)) return false;
  try {
    const key = keyBytes((await api.pushKey()).public_key);
    let sub = await reg.pushManager.getSubscription();
    if (sub && !sameKey(sub.options.applicationServerKey, key)) {
      await sub.unsubscribe();
      sub = null;
    }
    sub ??= await within(reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key }), SUBSCRIBE_WAIT_MS);
    await api.pushSubscribe(sub.toJSON());
    return true;
  } catch {
    return false;
  }
}

export type EnableResult = { mode: NotifyMode } | { error: string };

/** The Settings switch turned on: asks for permission (only here), then prefers push. */
export async function enableNotifications(): Promise<EnableResult> {
  if (typeof Notification === 'undefined') return { error: 'This browser cannot show notifications.' };
  let permission: NotificationPermission;
  try {
    permission = await Notification.requestPermission();
  } catch {
    permission = Notification.permission;
  }
  if (permission === 'denied') return { error: 'Notifications are blocked for this site. Allow them in the browser’s site settings, then try again.' };
  if (permission !== 'granted') return { error: 'Notifications were not allowed.' };
  const mode: NotifyMode = (await subscribePush()) ? 'push' : 'page';
  saveNotifyMode(mode);
  return { mode };
}

/** The switch turned off: this browser's push subscription goes too. */
export async function disableNotifications(): Promise<void> {
  saveNotifyMode(null);
  try {
    const sub = await (await navigator.serviceWorker?.getRegistration('/'))?.pushManager.getSubscription();
    if (!sub) return;
    await api.pushUnsubscribe(sub.endpoint).catch(() => {});
    await sub.unsubscribe();
  } catch {
    // Nothing to undo, or the browser refused: the service forgets it once the push service says it is gone.
  }
}

let viewing: string | null = null;
/** What the service last heard this tab shows; undefined before the first report. */
let reported: string | undefined;

/** Tells the service the Task this tab shows while it is visible ("" otherwise), when that changed or `again`. */
function report(again = false): void {
  const task = document.visibilityState === 'visible' ? (viewing ?? '') : '';
  if (!again && task === reported) return;
  reported = task;
  api.viewing(task).catch(() => {
    reported = undefined;
  });
}

/** The Task this page shows, if any; it is not announced while the page is visible, and the service pushes nothing for it. */
export function setViewing(id: string | null): void {
  viewing = id;
  report();
}

/** A new event stream opened: the service learns again what this tab shows, for that stream. */
export function streamOpened(): void {
  report(true);
}

function openTask(id: string): void {
  window.focus();
  const hash = `#task=${encodeURIComponent(id)}`;
  if (window.location.hash !== hash) window.location.hash = hash;
}

/** Answers the service worker (is the Task on screen? open a Task) and keeps a push subscription current. */
export function startNotifications(): () => void {
  const container = 'serviceWorker' in navigator ? navigator.serviceWorker : null;
  const onMessage = (event: MessageEvent) => {
    const data = event.data as { type?: string; task?: unknown; key?: unknown } | null;
    if (data?.type === 'uam-viewing') event.ports[0]?.postMessage({ task: viewing, visible: document.visibilityState === 'visible' });
    else if (data?.type === 'uam-open' && typeof data.task === 'string') openTask(data.task);
    else if (data?.type === 'uam-shown' && typeof data.key === 'string') void claim(data.key);
  };
  container?.addEventListener('message', onMessage);
  container?.startMessages();
  const onVisibility = () => report();
  document.addEventListener('visibilitychange', onVisibility);
  // A subscription the push service rotated, or a service that lost it, is sent again; where push stopped working, the page takes over.
  if (loadNotifyMode() === 'push') void subscribePush().then((ok) => { if (!ok && loadNotifyMode() === 'push') saveNotifyMode('page'); });
  return () => {
    container?.removeEventListener('message', onMessage);
    document.removeEventListener('visibilitychange', onVisibility);
  };
}

async function claim(key: string): Promise<boolean> {
  const take = () => {
    try {
      const next = claimNotice(JSON.parse(localStorage.getItem(SEEN_KEY) ?? '{}') as Record<string, number>, key, Date.now());
      if (next) localStorage.setItem(SEEN_KEY, JSON.stringify(next));
      return next !== null;
    } catch {
      return true;
    }
  };
  // The lock makes check-and-take atomic across this browser's tabs.
  if (navigator.locks) return navigator.locks.request('uam-notify', take);
  return take();
}

/** A `notify` event: shown once across tabs (and the push), unless a visible page shows that Task. */
export async function handleNotice(n: Notice): Promise<void> {
  const mode = loadNotifyMode();
  if (!mode) return;
  const onScreen = () => document.visibilityState === 'visible' && viewing === n.session_id;
  // A page that shows the Task claims the notice first, so other tabs skip it; one that opens it while waiting does too.
  if (!onScreen()) await new Promise((r) => setTimeout(r, mode === 'push' ? PUSH_GRACE_MS : 300));
  if (!loadNotifyMode() || !(await claim(n.key)) || onScreen()) return;
  const options: NotificationOptions & { renotify?: boolean } = { tag: `uam-task-${n.session_id}`, renotify: true, icon: '/icon-192.png', data: { task: n.session_id } };
  try {
    const reg = await navigator.serviceWorker?.getRegistration('/');
    if (reg) return await reg.showNotification(n.title, options);
  } catch {
    // Fall back to a page notification.
  }
  try {
    const note = new Notification(n.title, options);
    note.onclick = () => {
      note.close();
      openTask(n.session_id);
    };
  } catch {
    // Notifications are unavailable here after all.
  }
}
