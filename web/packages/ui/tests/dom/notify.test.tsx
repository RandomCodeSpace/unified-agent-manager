import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { handleNotice, NOTIFY_KEY, PUSH_GRACE_MS, SEEN_KEY, setViewing, setNotificationSource, startNotifications, type Notice } from '../../src/lib/notify';
import { renderApp, sidebar } from './render';

/** A browser's Notification API: permission granted (or not) on request, and every notification shown. */
function fakeNotifications(answer: NotificationPermission) {
  const shown: { title: string; options?: NotificationOptions & { renotify?: boolean } }[] = [];
  class FakeNotification {
    static permission: NotificationPermission = 'default';
    static requestPermission = vi.fn(async () => {
      FakeNotification.permission = answer;
      return answer;
    });
    onclick: (() => void) | null = null;
    constructor(title: string, options?: NotificationOptions) {
      shown.push({ title, options });
    }
    close() {}
  }
  vi.stubGlobal('Notification', FakeNotification);
  return { shown, FakeNotification };
}

async function notifySwitch() {
  const { user } = renderApp('#settings');
  await user.click(await screen.findByRole('button', { name: 'This browser', exact: true }));
  const card = within(await screen.findByRole('region', { name: 'This browser' }));
  return { card, toggle: card.getByRole('switch', { name: 'Notify me when a task needs me or finishes' }) };
}

const notice = (over: Partial<Notice> = {}): Notice => {
  const n = { seq: 1, session_id: 'task-b', kind: 'question' as const, title: 'Fix it needs you: Which colour?', ...over };
  return { key: `${n.session_id}:${n.kind}:${n.seq}`, ...n };
};

describe('notifications', () => {
  beforeEach(() => {
    // The page reports the Task it shows (POST /api/viewing); tests without the mock service answer it here.
    vi.stubGlobal('fetch', async () => new Response(null, { status: 204 }));
    setNotificationSource(null);
    setViewing(null);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  test('the switch asks for permission only when turned on, and works without push', async () => {
    const { FakeNotification } = fakeNotifications('granted');
    const { card, toggle } = await notifySwitch();
    expect(FakeNotification.requestPermission).not.toHaveBeenCalled();
    expect(toggle.getAttribute('aria-checked')).toBe('false');
    toggle.click();
    await waitFor(() => expect(toggle.getAttribute('aria-checked')).toBe('true'));
    expect(FakeNotification.requestPermission).toHaveBeenCalledTimes(1);
    // happy-dom has no service worker: the open page shows them.
    expect(localStorage.getItem(NOTIFY_KEY)).toBe('page');
    expect(card.getByText(/only while UAM is open in a tab/)).toBeTruthy();
    toggle.click();
    await waitFor(() => expect(toggle.getAttribute('aria-checked')).toBe('false'));
    expect(localStorage.getItem(NOTIFY_KEY)).toBeNull();
  });

  test('a refusal leaves the switch off and says how to allow them', async () => {
    fakeNotifications('denied');
    const { toggle } = await notifySwitch();
    toggle.click();
    expect((await screen.findByRole('alert')).textContent).toMatch(/blocked for this site/);
    expect(toggle.getAttribute('aria-checked')).toBe('false');
    expect(localStorage.getItem(NOTIFY_KEY)).toBeNull();
  });

  test('an iPhone tab explains the Home Screen app instead', async () => {
    fakeNotifications('granted');
    vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 Safari/604.1');
    const { card, toggle } = await notifySwitch();
    expect(card.getByText(/Add to Home Screen/)).toBeTruthy();
    expect(toggle.hasAttribute('data-disabled') || toggle.getAttribute('aria-disabled') === 'true').toBe(true);
  });

  test('a notice shows once across tabs, never for the Task on screen, and not while off', async () => {
    const { shown, FakeNotification } = fakeNotifications('granted');
    await handleNotice(notice());
    expect(shown).toHaveLength(0); // off
    FakeNotification.permission = 'granted';
    localStorage.setItem(NOTIFY_KEY, 'page');
    setViewing('task-a');
    await handleNotice(notice({ session_id: 'task-a', seq: 2 }));
    expect(shown).toHaveLength(0);
    await handleNotice(notice({ seq: 3 }));
    expect(shown).toEqual([{ title: 'Fix it needs you: Which colour?', options: expect.objectContaining({ tag: 'uam-task-task-b', data: { task: 'task-b', key: 'task-b:question:3' } }) }]);
    // Another tab got the same event: it is already taken.
    await handleNotice(notice({ seq: 3 }));
    expect(shown).toHaveLength(1);
  });

  test('in push mode a page shows a notice only when its push has not come', async () => {
    const { shown, FakeNotification } = fakeNotifications('granted');
    FakeNotification.permission = 'granted';
    localStorage.setItem(NOTIFY_KEY, 'push');
    vi.useFakeTimers();
    try {
      // The service worker showed this one and told the page (uam-shown): it is taken.
      localStorage.setItem(SEEN_KEY, JSON.stringify({ [notice({ seq: 5 }).key]: Date.now() }));
      let pending = handleNotice(notice({ seq: 5 }));
      await vi.advanceTimersByTimeAsync(PUSH_GRACE_MS);
      await pending;
      expect(shown).toHaveLength(0);
      pending = handleNotice(notice({ seq: 6 }));
      await vi.advanceTimersByTimeAsync(PUSH_GRACE_MS - 100);
      expect(shown).toHaveLength(0);
      await vi.advanceTimersByTimeAsync(100);
      await pending;
      expect(shown).toHaveLength(1);
    } finally {
      vi.useRealTimers();
    }
  });

  test('a Task opened while its notice waits for the push is not announced', async () => {
    const { shown, FakeNotification } = fakeNotifications('granted');
    FakeNotification.permission = 'granted';
    localStorage.setItem(NOTIFY_KEY, 'push');
    setViewing('task-a');
    vi.useFakeTimers();
    try {
      const pending = handleNotice(notice({ seq: 7 }));
      await vi.advanceTimersByTimeAsync(1000);
      setViewing('task-b'); // the owner opens it from Needs you
      await vi.advanceTimersByTimeAsync(PUSH_GRACE_MS);
      await pending;
      expect(shown).toHaveLength(0);
    } finally {
      vi.useRealTimers();
    }
  });

  test('the selected Task hidden behind Routines is announced', async () => {
    const { shown, FakeNotification } = fakeNotifications('granted');
    FakeNotification.permission = 'granted';
    localStorage.setItem(NOTIFY_KEY, 'page');
    const { user } = renderApp('#task=t3');
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Routines of unified-agent-manager' }));
    await screen.findByRole('heading', { level: 1, name: 'Routines' });
    await handleNotice(notice({ session_id: 't3', seq: 8 }));
    expect(shown.map((n) => n.title)).toEqual(['Fix it needs you: Which colour?']);
  });
  test('connected notices qualify visibility and cancel pending fallback after removal', async () => {
    const { shown, FakeNotification } = fakeNotifications('granted');
    FakeNotification.permission = 'granted';
    localStorage.setItem(NOTIFY_KEY, 'page');
    const streams: EventTarget[] = [];
    class NoticeStream extends EventTarget {
      constructor() { super(); streams.push(this); }
      close() {}
    }
    vi.stubGlobal('EventSource', NoticeStream);
    const stop = startNotifications();
    const attention = (enabled: boolean) => streams[0].dispatchEvent(new MessageEvent('attention', { data: JSON.stringify(enabled ? [{ connection_id: 'saved-b', instance_id: 'b', generation: 3, attention: 1, fresh: true }] : []) }));
    const remote = notice({ home_id: 'a', instance_id: 'b', connection_id: 'saved-b', generation: 3, key: 'b:epoch:1' });
    vi.useFakeTimers();
    try {
      attention(true);
      setNotificationSource({ id: 'saved-c', instance_id: 'c', generation: 3 });
      setViewing('task-b');
      let pending = handleNotice(remote);
      await vi.advanceTimersByTimeAsync(300);
      await pending;
      expect(shown).toHaveLength(1);
      expect(shown[0].options?.tag).toBe('uam-task:b:task-b');
      expect(shown[0].options?.data.connection_id).toBe('saved-b');
      setNotificationSource({ id: 'saved-b', instance_id: 'b', generation: 3 });
      setViewing('task-b');
      await handleNotice({ ...remote, key: 'b:epoch:2' });
      expect(shown).toHaveLength(1);
      setViewing(null);
      pending = handleNotice({ ...remote, key: 'b:epoch:3' });
      attention(false);
      await vi.advanceTimersByTimeAsync(300);
      await pending;
      expect(shown).toHaveLength(1);
    } finally {
      stop();
      setNotificationSource(null);
      vi.useRealTimers();
    }
  });

});
