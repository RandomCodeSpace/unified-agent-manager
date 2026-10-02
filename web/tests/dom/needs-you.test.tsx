// The Task list groups by what each Task needs (Needs you, Ready for review, Working, Idle) and
// Alt+J / Alt+K walk the Needs you group; answering happens in the Task, not the list.
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { renderApp, sidebar } from './render';

const row = (id: string) => within(document.querySelector<HTMLElement>(`[data-task-row="${id}"]`)!);
const header = () => screen.getByRole('heading', { level: 1 });

describe('the Task list', () => {
  test('groups the active Tasks by state, each with its count', async () => {
    // Tasks never opened here and changed since the first visit are unread: t4 failed, t5 was interrupted, t3 finished.
    // Ready for review also holds t-chart, the chart demo (mock/charts.ts), and t21, which works on a planner subtask.
    localStorage.setItem('uam.viewedSince', JSON.stringify('2020-01-01T00:00:00Z'));
    renderApp('?planner=unset');
    const side = await sidebar();
    const titles = side.getAllByRole('heading', { level: 2 }).map((h) => h.textContent);
    expect(titles).toEqual(['Needs you9Alt+J next', 'Ready for review5', 'Working3', 'Idle2']);
    const you = within(side.getByRole('region', { name: /Needs you/ }));
    expect(you.getByRole('button', { name: /Bump GitHub Actions pins.*Stopped with an error/ })).toBeTruthy();
    expect(you.getByRole('button', { name: /Tidy zsh startup.*Wants your OK to run a shell command outside the project/ })).toBeTruthy();
    expect(within(side.getByRole('region', { name: /Ready for review/ })).getByRole('button', { name: /Doctor: add terminal line.*Ready for review: Explained how a dumb terminal/ })).toBeTruthy();
    // The title, the badge and the drawer button count the same group.
    expect(document.title).toBe('(9) UAM');
  });

  test('a Needs you row only says what it waits on; answering happens in the Task', async () => {
    const { user } = renderApp();
    await sidebar();
    for (const id of ['t17', 't6']) {
      expect(row(id).queryByRole('button', { name: /^(Answer|Reply…|Allow|Don’t allow|Don't allow)$/ })).toBeNull();
      expect(row(id).queryByRole('group')).toBeNull();
    }
    await user.click(row('t17').getByRole('button', { name: /Set up the dependency lockfile/ }));
    await waitFor(() => expect(header().textContent).toBe('Set up the dependency lockfile'));
  });

  test('a Task row stays two lines: the status line is one truncated line, its whole text in the row title', async () => {
    renderApp();
    const side = await sidebar();
    const rows = side.getByRole('region', { name: /Needs you/ }).querySelectorAll<HTMLElement>('[data-task-row]');
    expect(rows.length).toBeGreaterThan(2);
    for (const el of rows) {
      const button = el.querySelector<HTMLButtonElement>('button[data-nav]')!;
      // Line one (badge, name, changes, time) and the status line; nothing else stacks in the row.
      expect(button.children).toHaveLength(2);
      const status = button.children[1] as HTMLElement;
      expect(status.classList.contains('truncate')).toBe(true);
      expect(el.querySelector('[class*="line-clamp"]')).toBeNull();
      expect(button.title).toContain(status.textContent!.replace(/^, /, ''));
    }
    // The long ask is cut on the row, never lost: the title holds all of it.
    expect(row('t19').getByRole('button', { name: /Cross-compile the release binaries/ }).title).toMatch(/Asks: Pick every platform the release should ship\. Each one is a CI job/);
  });

  test('Alt+J and Alt+K walk the Needs you group and wrap; a key that types in a field is left alone', async () => {
    renderApp();
    const side = await sidebar();
    const ids = Array.from(side.getByRole('region', { name: /Needs you/ }).querySelectorAll('[data-task-row]')).map((el) => el.getAttribute('data-task-row'));
    expect(ids.length).toBeGreaterThan(2);
    const press = (code: 'KeyJ' | 'KeyK', target: Element = document.body, key = code === 'KeyJ' ? 'j' : 'k') => fireEvent.keyDown(target, { code, key, altKey: true });
    press('KeyJ');
    await waitFor(() => expect(window.location.hash).toBe(`#task=${ids[0]}`));
    press('KeyJ');
    await waitFor(() => expect(window.location.hash).toBe(`#task=${ids[1]}`));
    press('KeyK');
    press('KeyK');
    await waitFor(() => expect(window.location.hash).toBe(`#task=${ids.at(-1)}`));
    // macOS Option+J types "∆" in a text field: that stays the field's.
    press('KeyJ', side.getByRole('searchbox', { name: 'Search tasks' }), '∆');
    await new Promise((r) => setTimeout(r, 50));
    expect(window.location.hash).toBe(`#task=${ids.at(-1)}`);
  });

  test('Alt+J, Alt+K and Alt+N still work while a tooltip is open', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const first = side.getByRole('region', { name: /Needs you/ }).querySelector('[data-task-row]')!.getAttribute('data-task-row');
    // Base UI opens a tip on hover or keyboard focus; it is no menu or dialog.
    await user.hover(side.getByRole('button', { name: 'Add project' }));
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).not.toBeNull());
    fireEvent.keyDown(document.body, { code: 'KeyJ', key: 'j', altKey: true });
    await waitFor(() => expect(window.location.hash).toBe(`#task=${first}`));
    await user.hover(side.getByRole('button', { name: 'Add project' }));
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).not.toBeNull());
    fireEvent.keyDown(document.body, { code: 'KeyN', key: 'n', altKey: true });
    expect(await screen.findByRole('dialog', { name: /New task/ })).toBeTruthy();
  });

  test('Alt+J pressed the moment the Needs you rows appear opens the first of them', async () => {
    renderApp();
    // Pressed from the commit that adds the rows, before React's passive effects run.
    const firstRow = () => [...document.querySelectorAll('section')].find((s) => s.querySelector('h2')?.textContent?.startsWith('Needs you'))?.querySelector('[data-task-row]');
    let first: string | null = null;
    const seen = new MutationObserver(() => {
      const row = first === null && firstRow();
      if (!row) return;
      first = row.getAttribute('data-task-row');
      fireEvent.keyDown(document.body, { code: 'KeyJ', key: 'j', altKey: true });
    });
    seen.observe(document.body, { subtree: true, childList: true });
    await waitFor(() => expect(first).not.toBeNull());
    seen.disconnect();
    await waitFor(() => expect(window.location.hash).toBe(`#task=${first}`));
  });

  test('the app badge is written by a visible page only; a hidden one leaves it to the service worker', async () => {
    const badges: number[] = [];
    Object.defineProperty(navigator, 'setAppBadge', { configurable: true, value: async (n: number) => { badges.push(n); } });
    Object.defineProperty(navigator, 'clearAppBadge', { configurable: true, value: async () => { badges.push(0); } });
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    try {
      localStorage.setItem('uam.viewedSince', JSON.stringify('2020-01-01T00:00:00Z'));
      renderApp('?planner=unset');
      await sidebar();
      await waitFor(() => expect(document.title).toBe('(9) UAM'));
      expect(badges).toEqual([]);
      visibility.mockReturnValue('visible');
      document.dispatchEvent(new Event('visibilitychange'));
      await waitFor(() => expect(badges.at(-1)).toBe(9));
    } finally {
      visibility.mockRestore();
      Reflect.deleteProperty(navigator, 'setAppBadge');
      Reflect.deleteProperty(navigator, 'clearAppBadge');
    }
  });

  test('read marks keep only existing Tasks, and do not advance while the page is hidden', async () => {
    localStorage.setItem('uam.viewed', JSON.stringify({ 'deleted-long-ago': '2026-01-01T00:00:00Z' }));
    localStorage.setItem('uam.looked', JSON.stringify({ 'deleted-long-ago': '2026-01-01T00:00:00Z' }));
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    try {
      renderApp('#task=t3');
      await screen.findByRole('region', { name: 'Conversation' });
      await waitFor(() => expect(localStorage.getItem('uam.viewed')).toBe('{}'));
      expect(localStorage.getItem('uam.looked')).toBe('{}');
      // Back on screen: the open Task is seen now.
      visibility.mockReturnValue('visible');
      hidden.mockReturnValue(false);
      document.dispatchEvent(new Event('visibilitychange'));
      await waitFor(() => expect(Object.keys(JSON.parse(localStorage.getItem('uam.viewed') ?? '{}') as object)).toEqual(['t3']));
      expect(Object.keys(JSON.parse(localStorage.getItem('uam.looked') ?? '{}') as object)).toEqual(['t3']);
    } finally {
      visibility.mockRestore();
      hidden.mockRestore();
    }
  });
});
