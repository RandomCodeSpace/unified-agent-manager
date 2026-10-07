// Unsettled Tasks share one flat list; status stays in each row.
// Alt+J / Alt+K still visit only Tasks needing attention.
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { renderApp, sidebar, type User } from './render';

const row = (id: string) => within(document.querySelector<HTMLElement>(`[data-task-row="${id}"]`)!);

/** The text of a row's tip (a styled tooltip, like the shelf rows', not a native title). */
async function tipOf(user: User, el: HTMLElement) {
  expect(el.hasAttribute('title')).toBe(false);
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
const header = () => screen.getByRole('heading', { level: 1 });

describe('the Task list', () => {
  test('keeps every unsettled Task in one flat list with its status', async () => {
    // Tasks never opened here and changed since the first visit are unread: t4 failed, t5 was interrupted, t3 finished.
    // Ready for review also holds t-chart, the chart demo (mock/charts.ts), and t21, which called an MCP server's tools;
    // Working holds t22, whose subagents still run after its turn.
    localStorage.setItem('uam.viewedSince', JSON.stringify('2020-01-01T00:00:00Z'));
    const { user } = renderApp();
    const side = await sidebar();
    expect(side.queryAllByRole('heading', { level: 2 })).toHaveLength(0);
    expect(side.getByRole('list', { name: 'Unsettled tasks' }).querySelectorAll('[data-task-row]')).toHaveLength(20);
    const you = within(side.getByRole('list', { name: 'Unsettled tasks' }));
    expect(you.getByRole('button', { name: /Error.*Bump GitHub Actions pins/ })).toBeTruthy();
    expect(await tipOf(user, you.getByRole('button', { name: /Input.*Tidy zsh startup/ }))).toContain('Wants your OK to run a shell command outside the project');
    expect(await tipOf(user, you.getByRole('button', { name: /Review.*Doctor: add terminal line/ }))).toContain('Ready for review: Explained how a dumb terminal');
    // The title and app badge still count only Tasks needing attention.
    await waitFor(() => expect(document.title).toBe('(9) UAM'));
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

  test('a Task row keeps project and state above title, muted branch and time', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const rows = side.getByRole('list', { name: 'Unsettled tasks' }).querySelectorAll<HTMLElement>('[data-task-row]');
    expect(rows.length).toBeGreaterThan(2);
    for (const el of rows) {
      const button = el.querySelector<HTMLButtonElement>('button[data-nav]')!;
      // Project and state are the first line; task, branch and time share the second.
      expect(button.children).toHaveLength(2);
      const first = button.children[0] as HTMLElement;
      const second = button.children[1] as HTMLElement;
      const status = first.lastElementChild as HTMLElement;
      expect(first.querySelector('[aria-hidden="true"]')).toBeTruthy();
      expect(first.querySelector('.truncate')?.classList.contains('sr-only')).toBe(false);
      expect(first.querySelector('time')).toBeNull();
      expect(second.querySelector('time')?.getAttribute('dateTime')).toBeTruthy();
      expect(status.textContent!.replace(/^, /, '')).toMatch(/^(Input|Starting|Working|Compacting|Review|Finished|Error|Interrupted|Stopped|Closed|Idle)$/);
      expect(status.querySelector('svg[aria-hidden="true"], span[aria-hidden="true"]')).toBeTruthy();
      expect(el.querySelector('[class*="line-clamp"]')).toBeNull();
      expect(button.hasAttribute('title')).toBe(false);
    }
    const example = row('t1').getByRole('button', { name: /Fix re-attach redraw regression/ });
    const [projectLine, taskLine] = Array.from(example.children) as HTMLElement[];
    expect(within(projectLine).getByText('unified-agent-manager')).toBeTruthy();
    expect(within(taskLine).getByText('Fix re-attach redraw regression')).toBeTruthy();
    const branch = within(taskLine).getByText('feat/web-project-defaults-and-sidebar-revamp').parentElement!;
    expect(branch.classList.contains('text-muted')).toBe(true);
    expect(branch.classList.contains('font-normal')).toBe(true);
    expect(await tipOf(user, example)).toContain('Project branch: feat/web-project-defaults-and-sidebar-revamp');
    // A project without a branch omits it, without adding a placeholder or third line.
    const noBranch = row('t6').getByRole('button', { name: /Tidy zsh startup/ });
    expect(noBranch.querySelector('[title^="Project branch:"]')).toBeNull();
    // Only Input appears on the row; its tip holds the complete request.
    expect(row('t19').getByText('Input')).toBeTruthy();
    expect(row('t19').queryByText(/Pick every platform/)).toBeNull();
    expect(await tipOf(user, row('t19').getByRole('button', { name: /Cross-compile the release binaries/ }))).toMatch(/Asks: Pick every platform the release should ship\. Each one is a CI job/);
  });

  test('every Task row in every group is a card; the open one is tinted instead', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(row('t17').getByRole('button', { name: /Set up the dependency lockfile/ }));
    await waitFor(() => expect(header().textContent).toBe('Set up the dependency lockfile'));
    {
      const rows = side.getByRole('list', { name: 'Unsettled tasks' }).querySelectorAll<HTMLElement>('[data-task-row]');
      expect(rows.length).toBeGreaterThan(0);
      for (const el of rows) {
        const card = el.querySelector<HTMLButtonElement>('button[data-nav]')!;
        expect(el.classList.contains('lift')).toBe(true);
        expect(card.classList.contains('shadow-raised')).toBe(true);
        const selected = card.getAttribute('aria-current') === 'true';
        expect(card.classList.contains(selected ? 'bg-tint-selected' : 'bg-raised')).toBe(true);
        expect(card.classList.contains(selected ? 'bg-raised' : 'bg-tint-selected')).toBe(false);
      }
    }
    expect(document.querySelector('[data-task-row="t17"] button[data-nav]')!.getAttribute('aria-current')).toBe('true');
  });

  test('Alt+J and Alt+K walk the Tasks that need you, in list order, and wrap; a key that types in a field is left alone', async () => {
    renderApp();
    const side = await sidebar();
    const ids = Array.from(side.getByRole('list', { name: 'Unsettled tasks' }).querySelectorAll('[data-task-row]'))
      .filter((el) => within(el as HTMLElement).queryByText('Input'))
      .map((el) => el.getAttribute('data-task-row'));
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
    const first = Array.from(side.getByRole('list', { name: 'Unsettled tasks' }).querySelectorAll('[data-task-row]')).find((el) => within(el as HTMLElement).queryByText('Input'))!.getAttribute('data-task-row');
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
    const firstRow = () => Array.from(document.querySelectorAll('[aria-label="Unsettled tasks"] [data-task-row]')).find((el) => within(el as HTMLElement).queryByText('Input'));
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
      renderApp();
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
