// The sidebar's Done section: Tasks the service judged done (mock d1–d9), above the Settled shelf.
import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import type { SessionSummary } from '../../src/api';
import { renderApp, sidebar, type User } from './render';

const nav = () => document.querySelector<HTMLElement>('nav[aria-label="Tasks"]')!;
const doneHeader = () => within(nav()).getByRole('button', { name: /^Done \d+$/ });
const settledHeader = () => within(nav()).getByRole('button', { name: /^Settled \d+$/ });
const row = (id: string) => nav().querySelector<HTMLElement>(`[data-task-row="${id}"]`);
/** The Done rows on screen, in order (a closed section's rows are inert). */
const doneRows = () => {
  const header = doneHeader();
  return Array.from(nav().querySelectorAll<HTMLElement>('[data-task-row^="d"]')).filter((el) => header.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING && !el.closest('[inert]')).map((el) => el.dataset.taskRow);
};

async function tipOf(user: User, el: HTMLElement) {
  await user.hover(el);
  const text = await waitFor(() => {
    const tip = document.querySelector('[data-popup="tooltip"]');
    expect(tip?.textContent).toBeTruthy();
    return tip!.textContent!;
  });
  await user.unhover(el);
  await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).toBeNull());
  return text;
}

/** The app with its Task list loaded, and a way to send Task list events down its stream. */
async function withStream() {
  const rendered = renderApp();
  const Source = window.EventSource;
  let sessions: SessionSummary[] = [];
  let stream: EventSource | undefined;
  window.EventSource = class extends Source {
    constructor(url: string | URL, options?: EventSourceInit) {
      super(url, options);
      if (String(url).startsWith('/api/events?')) this.addEventListener('snapshot', (e) => {
        stream = e.target as EventSource;
        sessions = (JSON.parse((e as MessageEvent).data) as { sessions: SessionSummary[] }).sessions;
      });
    }
  };
  await sidebar();
  await waitFor(() => expect(stream).toBeDefined());
  await act(async () => {});
  let seq = 1_000_000;
  const send = (payload: object) => act(() => {
    stream!.dispatchEvent(new MessageEvent('session', { data: JSON.stringify({ seq: ++seq, ...payload }) }));
  });
  return { ...rendered, send, session: (id: string) => sessions.find((s) => s.id === id)! };
}

describe('the Done section', () => {
  test('sits above Settled with compact rows, newest judgement first, eight until Show all', async () => {
    const { user } = renderApp();
    await sidebar();
    const header = doneHeader();
    expect(header.textContent).toBe('Done 9');
    expect(header.getAttribute('aria-expanded')).toBe('true');
    expect(header.compareDocumentPosition(settledHeader()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // Not in the active list; one line, badge and title, no status.
    expect(nav().querySelector('ul[aria-label="Unsettled tasks"] [data-task-row="d1"]')).toBeNull();
    expect(row('d1')!.textContent).toBe('UMunified-agent-manager, Rename SessionSummary.stage values');
    expect(doneRows()).toEqual(['d1', 'd2', 'd3', 'd4', 'd5', 'd6', 'd7', 'd8']);

    await user.click(within(nav()).getByRole('button', { name: 'Show all 9' }));
    expect(doneRows()).toEqual(['d1', 'd2', 'd3', 'd4', 'd5', 'd6', 'd7', 'd8', 'd9']);
    await user.click(within(nav()).getByRole('button', { name: 'Show fewer' }));
    expect(doneRows()).toHaveLength(8);
  });

  test('keeps the open Task on screen past the eight', async () => {
    renderApp('#task=d9');
    await sidebar();
    expect(doneRows()).toEqual(['d1', 'd2', 'd3', 'd4', 'd5', 'd6', 'd7', 'd8', 'd9']);
    expect(row('d9')!.querySelector('[aria-current="true"]')).not.toBeNull();
  });

  test('collapses, and remembers it like the shelves', async () => {
    const { user } = renderApp();
    await sidebar();
    await user.click(doneHeader());
    expect(doneHeader().getAttribute('aria-expanded')).toBe('false');
    expect(doneRows()).toEqual([]);
    expect(JSON.parse(localStorage.getItem('uam.shelves')!)).toMatchObject({ 'all:done': false });
    await user.click(doneHeader());
    expect(doneHeader().getAttribute('aria-expanded')).toBe('true');
  });

  test('a Task leaves it for the active list once the service drops the judgement', async () => {
    const { send, session } = await withStream();
    expect(doneRows()).toContain('d2');
    send({ session: { ...session('d2'), done_at: undefined, done_item_id: undefined, done_line: undefined, state: 'working' } });
    await waitFor(() => expect(nav().querySelector('ul[aria-label="Unsettled tasks"] [data-task-row="d2"]')).not.toBeNull());
    expect(doneHeader().textContent).toBe('Done 8');
  });

  test('a row\'s tip says why it is done; an auto-settled row\'s says it settled on its own', async () => {
    const { user } = renderApp();
    await sidebar();
    const done = await tipOf(user, row('d1')!.querySelector('button')!);
    expect(done).toContain('Renamed the stage values; the store migration and its tests pass.');
    expect(done).toMatch(/Done /);
    await user.click(settledHeader());
    const settled = await tipOf(user, await screen.findByRole('button', { name: /Explain the attach status bar design/ }));
    expect(settled).toContain('Settled automatically after 7 quiet days');
  });
});
