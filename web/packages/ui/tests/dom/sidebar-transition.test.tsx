import { act, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { SessionSummary } from '../../src/api';
import { renderApp, sidebar } from './render';

// happy-dom has no View Transitions API: this stand-in counts the transitions React starts and runs their update.
const startViewTransition = vi.fn(({ update }: { update: () => void }) => {
  const done = Promise.resolve().then(update);
  return { ready: done, finished: done, updateCallbackDone: done, skipTransition() {} };
});

beforeEach(() => {
  startViewTransition.mockClear();
  Object.defineProperty(document, 'startViewTransition', { configurable: true, value: startViewTransition });
});

afterEach(() => {
  delete (document as { startViewTransition?: unknown }).startViewTransition;
});

/** The app on Home with its Task list loaded, and a way to send Task list events down its stream. */
async function home() {
  renderApp();
  const Source = window.EventSource;
  let sessions: SessionSummary[] = [];
  let stream: EventSource | undefined;
  window.EventSource = class extends Source {
    constructor(url: string | URL, options?: EventSourceInit) {
      super(url, options);
      // The Task list's own stream; the connected-instances notification stream sends a snapshot too.
      if (String(url).startsWith('/api/events?')) this.addEventListener('snapshot', (e) => {
        stream = e.target as EventSource;
        sessions = (JSON.parse((e as MessageEvent).data) as { sessions: SessionSummary[] }).sessions;
      });
    }
  };
  await sidebar();
  await waitFor(() => expect(stream).toBeDefined());
  // Work the start left pending (a Task list update from the connected-instances registry) lands first.
  await act(async () => {});
  startViewTransition.mockClear();
  let seq = 1_000_000;
  const send = (name: string, payload: object) => act(() => {
    stream!.dispatchEvent(new MessageEvent(name, { data: JSON.stringify({ seq: ++seq, ...payload }) }));
  });
  const active = sessions.find((s) => (s.stage ?? 'active') === 'active' && s.state !== 'failed')!;
  return { send, active, completed: sessions.find((s) => s.id === 't3')! };
}

const row = (id: string) => document.querySelector(`nav[aria-label="Threads"] [data-task-row="${id}"]`);

describe('Task list quiet regrouping', () => {
  test('a minute refreshes an unchanged Task time without a view transition', async () => {
    // Keep the mock's request timeouts real; only the relative-time interval advances.
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    const now = Date.now();
    const clock = vi.spyOn(Date, 'now').mockReturnValue(now);
    try {
      const { send, active } = await home();
      const updatedAt = new Date(now - 3 * 60_000).toISOString();
      send('session', { session: { ...active, updated_at: updatedAt } });
      await waitFor(() => expect(row(active.id)?.querySelector('time')?.textContent).toBe('3m'));
      await act(async () => {});
      startViewTransition.mockClear();

      clock.mockReturnValue(now + 60_000);
      await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
      expect(row(active.id)?.querySelector('time')?.textContent).toBe('4m');
      expect(row(active.id)?.querySelector('time')?.getAttribute('datetime')).toBe(updatedAt);
      expect(startViewTransition).not.toHaveBeenCalled();
    } finally {
      clock.mockRestore();
      vi.useRealTimers();
    }
  });

  test('a Task changing in place (state, title, time) repaints its row without one', async () => {
    const { send, active } = await home();
    send('session', { session: { ...active, state: 'failed', name: 'Changed in place', updated_at: new Date().toISOString() } });
    await waitFor(() => expect(row(active.id)?.textContent).toMatch(/Error.*Changed in place/));
    await act(async () => {});
    expect(startViewTransition).not.toHaveBeenCalled();
  });

  test('an automatic Settled move waits for five quiet seconds while status stays live', async () => {
    const { send, completed } = await home();
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      send('session', { session: { ...completed, stage: 'settled', settled_by: 'auto', settled_at: new Date().toISOString() } });
      await act(async () => {});
      expect(document.querySelector(`ul[aria-label="Unsettled threads"] [data-task-row="${completed.id}"]`)).not.toBeNull();
      expect(row(completed.id)?.textContent).toContain('Settled');
      expect(startViewTransition).not.toHaveBeenCalled();
      await act(async () => { await vi.advanceTimersByTimeAsync(4999); });
      expect(document.querySelector(`ul[aria-label="Unsettled threads"] [data-task-row="${completed.id}"]`)).not.toBeNull();
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(document.querySelector(`ul[aria-label="Unsettled threads"] [data-task-row="${completed.id}"]`)).toBeNull();
      expect(startViewTransition).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  test('automatic regrouping waits until keyboard focus leaves the list and it stays quiet', async () => {
    const { send, completed } = await home();
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      row(completed.id)!.querySelector<HTMLButtonElement>('[data-nav]')!.focus();
      send('session', { session: { ...completed, stage: 'settled', settled_by: 'auto', settled_at: new Date().toISOString() } });
      await act(async () => {});
      await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
      expect(document.querySelector(`ul[aria-label="Unsettled threads"] [data-task-row="${completed.id}"]`)).not.toBeNull();
      document.querySelector<HTMLInputElement>('input[aria-label="Search threads"]')!.focus();
      await act(async () => { await vi.advanceTimersByTimeAsync(4999); });
      expect(document.querySelector(`ul[aria-label="Unsettled threads"] [data-task-row="${completed.id}"]`)).not.toBeNull();
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(document.querySelector(`ul[aria-label="Unsettled threads"] [data-task-row="${completed.id}"]`)).toBeNull();
      expect(startViewTransition).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  test('resumed work cancels a pending automatic move', async () => {
    const { send, completed } = await home();
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      send('session', { session: { ...completed, stage: 'settled', settled_by: 'auto', settled_at: new Date().toISOString() } });
      await act(async () => {});
      await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
      send('session', { session: { ...completed, state: 'working' } });
      await act(async () => {});
      await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
      expect(document.querySelector(`ul[aria-label="Unsettled threads"] [data-task-row="${completed.id}"]`)).not.toBeNull();
      expect(row(completed.id)?.textContent).toContain('Working');
      expect(startViewTransition).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  test('a Task entering the list appears immediately without an animation', async () => {
    const { send, active } = await home();
    send('session', { session: { ...active, id: 'entering', name: 'Entering', created_at: new Date().toISOString() } });
    await waitFor(() => expect(row('entering')).not.toBeNull());
    expect(startViewTransition).not.toHaveBeenCalled();
  });

  test('a Task leaving the list disappears immediately without an animation', async () => {
    const { send, active } = await home();
    send('session_removed', { session_id: active.id });
    await waitFor(() => expect(row(active.id)).toBeNull());
    expect(startViewTransition).not.toHaveBeenCalled();
  });

  test('a manually settled Task moves immediately without a view transition', async () => {
    const { send, active } = await home();
    send('session', { session: { ...active, stage: 'settled', settled_at: new Date().toISOString() } });
    await waitFor(() => expect(document.querySelector(`ul[aria-label="Unsettled threads"] [data-task-row="${active.id}"]`)).toBeNull());
    expect(startViewTransition).not.toHaveBeenCalled();
  });
});
