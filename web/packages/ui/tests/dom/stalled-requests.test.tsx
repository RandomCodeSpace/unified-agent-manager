// Requests the service never answers: they end in the states a failure shows, not in a load or a spinner for good.
import { StrictMode } from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { UamApp } from '@uam/ui';
import { ApiError, createApiClient, LONG_WRITE_TIMEOUT_MS, PROMPT_TIMEOUT_MS, READ_TIMEOUT_MS, WRITE_TIMEOUT_MS } from '../../src/api';
import { install } from '../../src/mock/install';
import { composer, log, openTask, sidebar } from './render';

const RECONNECTING = 'Connection lost, reconnecting. Work continues on the server.';

let restoreFetch: (() => void) | undefined;

afterEach(() => {
  vi.useRealTimers();
  restoreFetch?.();
  restoreFetch = undefined;
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

describe('a stalled read', () => {
  test('the sign-in check at startup is given up on after 60 s, and the sign-in screen shows', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    install();
    const answer = window.fetch;
    let asked = 0;
    window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
      if (!String(input).endsWith('/api/auth')) return answer(input, init);
      asked++;
      const signal = init?.signal;
      return new Promise<Response>((_resolve, reject) => signal?.addEventListener('abort', () => reject(signal.reason)));
    };
    render(<StrictMode><UamApp /></StrictMode>);
    await waitFor(() => expect(asked).toBeGreaterThan(0));
    await act(() => vi.advanceTimersByTimeAsync(READ_TIMEOUT_MS - 1000));
    expect(screen.queryByRole('heading', { name: 'Sign in to UAM' })).toBeNull();
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(await screen.findByRole('heading', { name: 'Sign in to UAM' })).toBeTruthy();
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

describe('a stalled write', () => {
  /** A service that takes every request and answers none; the requests it was sent. */
  function silent(): string[] {
    const asked: string[] = [];
    const before = window.fetch;
    restoreFetch = () => { window.fetch = before; };
    window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
      asked.push(`${init?.method} ${String(input)}`);
      const signal = init?.signal;
      return new Promise<Response>((_resolve, reject) => signal?.addEventListener('abort', () => reject(signal.reason)));
    };
    return asked;
  }
  /** What the write ended with, once it has. */
  function ending(write: Promise<unknown>): { error?: unknown } {
    const end: { error?: unknown } = {};
    write.catch((error: unknown) => { end.error = error; });
    return end;
  }

  test('is given up on after 2 minutes with the outcome not known, and is not sent again', async () => {
    vi.useFakeTimers();
    const asked = silent();
    const reported: ApiError[] = [];
    const end = ending(createApiClient(null, () => true, (error) => reported.push(error)).rename('t3', 'New name'));
    await vi.advanceTimersByTimeAsync(WRITE_TIMEOUT_MS - 1);
    expect(end.error).toBeUndefined();
    await vi.advanceTimersByTimeAsync(1);
    expect(end.error).toBeInstanceOf(ApiError);
    expect(end.error).toMatchObject({ status: 0, message: 'The server did not answer in 2 minutes, so it is not known whether this was done' });
    expect(reported).toEqual([end.error]);
    await vi.advanceTimersByTimeAsync(LONG_WRITE_TIMEOUT_MS);
    expect(asked).toEqual(['PATCH /api/sessions/t3']);
  });

  test('one that may run long by design keeps its 5 minutes', async () => {
    vi.useFakeTimers();
    const asked = silent();
    const end = ending(createApiClient().stage('t3', 'settle'));
    await vi.advanceTimersByTimeAsync(LONG_WRITE_TIMEOUT_MS - 1);
    expect(end.error).toBeUndefined();
    await vi.advanceTimersByTimeAsync(1);
    expect(end.error).toMatchObject({ status: 0, message: 'The server did not answer in 5 minutes, so it is not known whether this was done' });
    await vi.advanceTimersByTimeAsync(LONG_WRITE_TIMEOUT_MS);
    expect(asked).toEqual(['POST /api/sessions/t3/settle']);
  });

  test("the caller's own abort still ends it, with the caller's reason", async () => {
    vi.useFakeTimers();
    const asked = silent();
    const reported: ApiError[] = [];
    const controller = new AbortController();
    const end = ending(createApiClient(null, () => true, (error) => reported.push(error)).suggestions('t3', controller.signal));
    await vi.advanceTimersByTimeAsync(1000);
    const reason = new Error('left the thread');
    controller.abort(reason);
    await vi.advanceTimersByTimeAsync(0);
    expect(end.error).toBe(reason);
    await vi.advanceTimersByTimeAsync(LONG_WRITE_TIMEOUT_MS);
    expect(reported).toEqual([]);
    expect(asked).toEqual(['POST /api/sessions/t3/suggestions']);
  });
});

describe('a stalled download', () => {
  /** A service that takes every request and answers none. */
  function silent() {
    const before = window.fetch;
    restoreFetch = () => { window.fetch = before; };
    window.fetch = (_input: RequestInfo | URL, init?: RequestInit) => {
      const signal = init?.signal;
      return new Promise<Response>((_resolve, reject) => signal?.addEventListener('abort', () => reject(signal.reason)));
    };
  }

  test('the Markdown export is given up on after 60 s like any read', async () => {
    vi.useFakeTimers();
    silent();
    const reported: ApiError[] = [];
    let end: unknown;
    createApiClient(null, () => true, (error) => reported.push(error)).exportMarkdown('t3').catch((error: unknown) => { end = error; });
    await vi.advanceTimersByTimeAsync(READ_TIMEOUT_MS - 1);
    expect(end).toBeUndefined();
    await vi.advanceTimersByTimeAsync(1);
    expect(end).toBeInstanceOf(ApiError);
    expect(end).toMatchObject({ status: 0, message: 'The server did not answer in 60 seconds' });
    expect(reported).toEqual([end]);
  });

  test("the caller's own abort still ends it, with the caller's reason", async () => {
    vi.useFakeTimers();
    silent();
    const reported: ApiError[] = [];
    const controller = new AbortController();
    let end: unknown;
    createApiClient(null, () => true, (error) => reported.push(error)).exportMarkdown('t3', controller.signal).catch((error: unknown) => { end = error; });
    const reason = new Error('closed the menu');
    controller.abort(reason);
    await vi.advanceTimersByTimeAsync(READ_TIMEOUT_MS);
    expect(end).toBe(reason);
    expect(reported).toEqual([]);
  });
});
