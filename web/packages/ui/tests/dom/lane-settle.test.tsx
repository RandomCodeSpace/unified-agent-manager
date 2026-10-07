// A lane Task uam retired is settled, not archived, and cannot be reopened: its lane is removed
// (ADR 0006 §5.6). It reads like any settled Task, and Reopen stays in view, disabled, with the
// service's reason.
import { StrictMode } from 'react';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test } from 'vitest';
import { UamApp } from '@uam/ui';
import { install } from '../../src/mock/install';

const REASON = '#7 ran in a lane that is removed; its work landed as abc1234. Read the transcript here; the work is on the integration branch.';

type Json = Record<string, unknown>;
const isJson = (v: unknown): v is Json => !!v && typeof v === 'object' && !Array.isArray(v);
const retireTask = (t: unknown) => (isJson(t) && t.id === 't11' ? { ...t, retired: REASON } : t);

/** Marks the mock's settled Task t11 as a lane Task uam retired in a frame or reply: a summary or detail, or the snapshot and session frames that carry one. */
function retire(data: unknown): unknown {
  if (!isJson(data)) return data;
  if (data.id === 't11') return retireTask(data);
  return { ...data, ...(Array.isArray(data.sessions) ? { sessions: data.sessions.map(retireTask) } : {}), ...(isJson(data.session) ? { session: retireTask(data.session) } : {}) };
}

/** Renders the app on t11, retired, and lists the Reopen requests it sends. */
function renderRetired() {
  history.replaceState(null, '', '/#task=t11');
  install();
  const MockSource = window.EventSource;
  // happy-dom calls dispatchEvent again while it dispatches: an event made here passes as it is.
  const patched = new WeakSet<Event>();
  window.EventSource = class extends MockSource {
    override dispatchEvent(e: Event): boolean {
      if (patched.has(e) || !(e instanceof MessageEvent) || typeof e.data !== 'string') return super.dispatchEvent(e);
      const next = new MessageEvent(e.type, { data: JSON.stringify(retire(JSON.parse(e.data))) });
      patched.add(next);
      return super.dispatchEvent(next);
    }
  };
  const mockFetch = window.fetch;
  const reopens: string[] = [];
  window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.endsWith('/reopen')) reopens.push(url);
    const res = await mockFetch(input, init);
    if (!res.headers.get('Content-Type')?.includes('application/json')) return res;
    return new Response(JSON.stringify(retire(await res.json())), { status: res.status, headers: res.headers });
  }) as typeof fetch;
  const user = userEvent.setup();
  render(
    <StrictMode>
      <UamApp />
    </StrictMode>,
  );
  return { user, reopens };
}

describe('a lane Task uam retired', () => {
  test('its composer says why it stays settled, and Reopen is disabled', async () => {
    const { user, reopens } = renderRetired();
    expect(await screen.findByText(`Settled. ${REASON}`)).toBeTruthy();
    const reopen = screen.getByRole('button', { name: 'Reopen' });
    expect(reopen.getAttribute('aria-disabled')).toBe('true');
    await user.click(reopen);
    expect(reopens).toEqual([]);
    expect(screen.getByText(`Settled. ${REASON}`)).toBeTruthy();
  });

  test('its Task menu offers Reopen disabled, with the reason under it', async () => {
    const { user } = renderRetired();
    await screen.findByText(`Settled. ${REASON}`);
    await user.click(await screen.findByRole('button', { name: 'Task actions' }));
    const menu = within(await screen.findByRole('menu'));
    expect(menu.getByRole('menuitem', { name: 'Reopen' }).getAttribute('aria-disabled')).toBe('true');
    expect(menu.getByText(REASON)).toBeTruthy();
  });
});
