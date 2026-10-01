import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { handleNotice, noticeKey, NOTIFY_KEY, PUSH_GRACE_MS, SEEN_KEY, setViewing, type Notice } from '../../src/lib/notify';
import { renderApp } from './render';

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
  renderApp('#settings');
  const card = within(await screen.findByRole('region', { name: 'This browser' }));
  return { card, toggle: card.getByRole('switch', { name: 'Notify me when a Task needs me or finishes' }) };
}

const notice = (over: Partial<Notice> = {}): Notice => ({ seq: 1, session_id: 'task-b', kind: 'question', title: 'Fix it needs you: Which colour?', ...over });

describe('notifications', () => {
  beforeEach(() => setViewing(null));
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
    expect(shown).toEqual([{ title: 'Fix it needs you: Which colour?', options: expect.objectContaining({ tag: 'uam-task-task-b', data: { task: 'task-b' } }) }]);
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
      localStorage.setItem(SEEN_KEY, JSON.stringify({ [noticeKey(notice({ seq: 5 }))]: Date.now() }));
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
});
