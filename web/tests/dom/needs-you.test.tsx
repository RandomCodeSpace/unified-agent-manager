// The Task list groups by what each Task needs (Needs you, Ready for review, Working, Idle) and
// answers a waiting request in place; Alt+J / Alt+K walk the Needs you group.
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp, sidebar } from './render';

const row = (id: string) => within(document.querySelector<HTMLElement>(`[data-task-row="${id}"]`)!);
const header = () => screen.getByRole('heading', { level: 1 });

describe('the Task list', () => {
  test('groups the active Tasks by state, each with its count', async () => {
    // Tasks never opened here and changed since the first visit are unread: t4 failed, t5 was interrupted, t3 finished.
    localStorage.setItem('uam.viewedSince', JSON.stringify('2020-01-01T00:00:00Z'));
    renderApp('?planner=unset');
    const side = await sidebar();
    const titles = side.getAllByRole('heading', { level: 2 }).map((h) => h.textContent);
    expect(titles).toEqual(['Needs you9Alt+J next', 'Ready for review2', 'Working3', 'Idle2']);
    const you = within(side.getByRole('region', { name: /Needs you/ }));
    expect(you.getByRole('button', { name: /Bump GitHub Actions pins.*Stopped with an error/ })).toBeTruthy();
    expect(you.getByRole('button', { name: /Tidy zsh startup.*Wants your OK to run a shell command outside the project/ })).toBeTruthy();
    expect(within(side.getByRole('region', { name: /Ready for review/ })).getByRole('button', { name: /Doctor: add terminal line.*Finished, ready for your review/ })).toBeTruthy();
    // The title, the badge and the drawer button count the same group.
    expect(document.title).toBe('(9) UAM');
  });

  test('answers a question from its chips: the recommended choice is staged, Answer sends it', async () => {
    const { user, mock } = renderApp();
    await sidebar();
    const r = row('t17');
    expect(r.getByRole('button', { name: 'pnpm (Recommended)' }).getAttribute('aria-pressed')).toBe('true');
    await user.click(r.getByRole('button', { name: 'npm' }));
    expect(r.getByRole('button', { name: 'pnpm (Recommended)' }).getAttribute('aria-pressed')).toBe('false');
    await user.click(r.getByRole('button', { name: 'Answer' }));
    await waitFor(() => expect(mock.received.find((x) => x.route === 'answer')).toEqual({ route: 'answer', session: 't17', body: { answers: [['npm']] } }));
    // The Task leaves Needs you; nothing opened.
    await waitFor(() => expect(document.querySelector('[data-task-row="t17"] [role="group"]')).toBeNull());
    expect(window.location.hash).toBe('');
  });

  test('allows a permission in place, and Reply… opens the Task', async () => {
    const { user, mock } = renderApp();
    await sidebar();
    const r = row('t6');
    expect(r.getByText('chmod 0644 ~/.config/zsh/aliases.zsh && rm -f ~/.zcompdump*')).toBeTruthy();
    await user.click(r.getByRole('button', { name: 'Allow' }));
    await waitFor(() => expect(mock.received.find((x) => x.route === 'answer')).toEqual({ route: 'answer', session: 't6', body: { decision: 'once' } }));
    await user.click(row('t17').getByRole('button', { name: 'Reply…' }));
    await waitFor(() => expect(header().textContent).toBe('Set up the dependency lockfile'));
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
});
