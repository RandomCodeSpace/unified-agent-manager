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
  return { send, active };
}

const row = (id: string) => document.querySelector(`nav[aria-label="Tasks"] [data-task-row="${id}"]`);

describe('Task list view transition', () => {
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

  test('a Task entering the list starts one', async () => {
    const { send, active } = await home();
    send('session', { session: { ...active, id: 'entering', name: 'Entering', created_at: new Date().toISOString() } });
    await waitFor(() => expect(row('entering')).not.toBeNull());
    expect(startViewTransition).toHaveBeenCalled();
  });

  test('a Task leaving the list starts one', async () => {
    const { send, active } = await home();
    send('session_removed', { session_id: active.id });
    await waitFor(() => expect(row(active.id)).toBeNull());
    expect(startViewTransition).toHaveBeenCalled();
  });

  test('a Task moving to the Settled shelf starts one', async () => {
    const { send, active } = await home();
    send('session', { session: { ...active, stage: 'settled', settled_at: new Date().toISOString() } });
    await waitFor(() => expect(document.querySelector(`ul[aria-label="Unsettled tasks"] [data-task-row="${active.id}"]`)).toBeNull());
    expect(startViewTransition).toHaveBeenCalled();
  });
});
