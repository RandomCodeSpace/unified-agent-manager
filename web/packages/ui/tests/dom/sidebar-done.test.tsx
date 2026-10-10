// The sidebar's Done section: Tasks the service judged done (mock d1–d9, plus d10–d11 where the cap matters), above the Settled shelf.
import { act, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { SessionSummary } from '../../src/api';
import * as data from '../../src/mock/data';
import { renderApp, sidebar, type User } from './render';

afterEach(() => vi.restoreAllMocks());

/** The mock with d10 and d11 judged done after d9, past Done's ten. */
function elevenDone() {
  const state = data.seed();
  const d9 = state.tasks.find((t) => t.id === 'd9')!;
  for (const n of [10, 11]) state.tasks.push({ ...d9, id: `d${n}`, done_at: new Date(Date.parse(d9.done_at!) - n * 60_000).toISOString() });
  vi.spyOn(data, 'seed').mockReturnValue(state);
}

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
  test('sits above Settled with compact rows, newest judgement first, ten until Show all', async () => {
    elevenDone();
    const { user } = renderApp();
    await sidebar();
    const header = doneHeader();
    expect(header.textContent).toBe('Done 11');
    expect(header.getAttribute('aria-expanded')).toBe('true');
    expect(header.compareDocumentPosition(settledHeader()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // Not in the active list; one line, badge and title, no status.
    expect(nav().querySelector('ul[aria-label="Unsettled tasks"] [data-task-row="d1"]')).toBeNull();
    expect(row('d1')!.textContent).toBe('UMunified-agent-manager, Rename SessionSummary.stage values');
    const ten = ['d1', 'd2', 'd3', 'd4', 'd5', 'd6', 'd7', 'd8', 'd9', 'd10'];
    expect(doneRows()).toEqual(ten);

    await user.click(within(nav()).getByRole('button', { name: 'Show all 11' }));
    expect(doneRows()).toEqual([...ten, 'd11']);
    await user.click(within(nav()).getByRole('button', { name: 'Show fewer' }));
    expect(doneRows()).toEqual(ten);
  });

  test('keeps the open Task on screen past the ten', async () => {
    elevenDone();
    renderApp('#task=d11');
    await sidebar();
    expect(doneRows()).toEqual(['d1', 'd2', 'd3', 'd4', 'd5', 'd6', 'd7', 'd8', 'd9', 'd10', 'd11']);
    expect(row('d11')!.querySelector('[aria-current="true"]')).not.toBeNull();
  });

  test('opening Settled closes Done and opening Done closes Settled, remembered', async () => {
    const { user } = renderApp();
    await sidebar();
    await user.click(settledHeader());
    expect(settledHeader().getAttribute('aria-expanded')).toBe('true');
    expect(doneHeader().getAttribute('aria-expanded')).toBe('false');
    expect(doneRows()).toEqual([]);
    expect(JSON.parse(localStorage.getItem('uam.shelves')!)).toMatchObject({ 'all:done': false, 'all:settled': true });
    await user.click(doneHeader());
    expect(doneHeader().getAttribute('aria-expanded')).toBe('true');
    expect(settledHeader().getAttribute('aria-expanded')).toBe('false');
    expect(JSON.parse(localStorage.getItem('uam.shelves')!)).toMatchObject({ 'all:done': true, 'all:settled': false });
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

  test('Settled shows the latest ten settled until Show all', async () => {
    const state = data.seed();
    const base = state.tasks.find((t) => t.id === 't11')!;
    for (let i = 1; i <= 11; i++) state.tasks.push({ ...base, id: `s${i}`, name: `Settled chore ${i}`, settled_at: new Date(Date.now() - i * 60_000).toISOString() });
    vi.spyOn(data, 'seed').mockReturnValue(state);
    const { user } = renderApp();
    await sidebar();
    await user.click(settledHeader());
    expect(settledHeader().textContent).toBe('Settled 12');
    const settledRows = () => Array.from(nav().querySelectorAll<HTMLElement>('[data-task-row^="s"], [data-task-row="t11"]')).filter((el) => !el.closest('[inert]')).map((el) => el.dataset.taskRow);
    expect(settledRows()).toEqual(['s1', 's2', 's3', 's4', 's5', 's6', 's7', 's8', 's9', 's10']);
    await user.click(within(nav()).getByRole('button', { name: 'Show all 12' }));
    expect(settledRows()).toEqual(['s1', 's2', 's3', 's4', 's5', 's6', 's7', 's8', 's9', 's10', 's11', 't11']);
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
