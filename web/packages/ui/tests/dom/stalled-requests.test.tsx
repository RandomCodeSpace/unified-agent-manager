// Requests the service never answers: they end in the states a failure shows, not in a load or a spinner for good.
import { StrictMode } from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { UamApp } from '@uam/ui';
import { PROMPT_TIMEOUT_MS } from '../../src/api';
import { install } from '../../src/mock/install';
import { composer, log, openTask, sidebar } from './render';

const RECONNECTING = 'Connection lost, reconnecting. Work continues on the server.';

afterEach(() => {
  vi.useRealTimers();
});

describe('a stalled event stream', () => {
  test('without a snapshot in 20 s it shows the lost connection and is reopened', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    install();
    const Mocked = window.EventSource;
    let hold = true;
    const held: string[] = [];
    // The main stream is held without an answer, as a proxy that buffers it would; the others work.
    function Holding(url: string) {
      if (hold && new URL(url, window.location.origin).pathname === '/api/events') {
        held.push(url);
        return Object.assign(new EventTarget(), { url, readyState: 0, onopen: null, onerror: null, close() { this.readyState = 2; } });
      }
      return new Mocked(url);
    }
    window.EventSource = Object.assign(Holding, { CONNECTING: 0, OPEN: 1, CLOSED: 2 }) as unknown as typeof EventSource;
    render(<StrictMode><UamApp /></StrictMode>);
    await waitFor(() => expect(held.length).toBeGreaterThan(0));
    const opened = held.length;
    expect(screen.queryAllByText(RECONNECTING)).toHaveLength(0);
    await act(() => vi.advanceTimersByTimeAsync(20_000));
    await screen.findAllByText(RECONNECTING);
    expect(held.length).toBe(opened + 1);
    // The next stream answers: the list comes and the notice goes.
    hold = false;
    await act(() => vi.advanceTimersByTimeAsync(20_000));
    await sidebar();
    await waitFor(() => expect(screen.queryAllByText(RECONNECTING)).toHaveLength(0));
  });
});

describe('a stalled send', () => {
  test('fails after 30 s with the draft kept, and sending again reuses its request', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { user } = await openTask('t3');
    const answer = window.fetch;
    const sent: string[] = [];
    let hold = true;
    window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/api/sessions/t3/prompt')) {
        sent.push((JSON.parse(String(init?.body)) as { request_id: string }).request_id);
        const signal = init?.signal;
        if (hold) return new Promise<Response>((_resolve, reject) => signal?.addEventListener('abort', () => reject(signal.reason)));
      }
      return answer(input, init);
    };
    await user.type(composer(), 'Cover TERM=vt100 too');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(sent).toHaveLength(1));
    await act(() => vi.advanceTimersByTimeAsync(PROMPT_TIMEOUT_MS));
    expect(await screen.findByText(/^The server did not answer in 30 seconds\. Nothing will be retried automatically\./)).toBeTruthy();
    expect(composer().value).toBe('Cover TERM=vt100 too');
    expect(composer().disabled).toBe(false);
    hold = false;
    await user.keyboard('{Enter}');
    expect(await log().findByText('Cover TERM=vt100 too')).toBeTruthy();
    await waitFor(() => expect(composer().value).toBe(''));
    expect(sent).toEqual([sent[0], sent[0]]);
  });
});
