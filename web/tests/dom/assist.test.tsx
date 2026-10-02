import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { composer, openMenu, openTask, renderApp } from './render';

describe('assist', () => {
  // First in the file: the replies are kept per Task for the page's life.
  test('replies withheld while Background AI is paused are asked for again on the next look', async () => {
    renderApp('#task=t3');
    const real = window.fetch;
    let paused = true;
    let asked = 0;
    window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      if (!String(input).endsWith('/suggestions')) return real(input, init);
      asked++;
      if (paused) return new Response(JSON.stringify({ item_id: '', replies: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return real(input, init);
    }) as typeof fetch;
    await screen.findByRole('region', { name: 'Conversation' });
    await waitFor(() => expect(asked).toBeGreaterThan(0));
    const title = screen.getByRole('heading', { level: 1 }).textContent;
    await new Promise((r) => setTimeout(r, 200));
    expect(screen.queryByRole('group', { name: 'Suggested replies' })).toBeNull();
    // The limit is raised; another Task, then back: the service kept nothing, so the page asks again.
    paused = false;
    window.location.hash = '#task=t1';
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).not.toBe(title));
    window.location.hash = '#task=t3';
    expect(await screen.findByRole('group', { name: 'Suggested replies' })).toBeTruthy();
  });

  test('a suggested reply fills the composer and sends nothing', async () => {
    const { user, mock } = await openTask('t3');
    const group = within(await screen.findByRole('group', { name: 'Suggested replies' }));
    await user.click(group.getByRole('button', { name: 'Show me the diff' }));
    expect(composer().value).toBe('Show me the diff');
    expect(mock.received.filter((r) => r.route === 'prompt')).toHaveLength(0);
    expect(screen.queryByRole('group', { name: 'Suggested replies' })).toBeNull();
  });

  test('suggested replies float above the composer, out of its flow, so it keeps its size', async () => {
    await openTask('t3');
    const group = await screen.findByRole('group', { name: 'Suggested replies' });
    const form = composer().closest('form')!;
    // Anchored to the composer's top edge (the form is its positioning box) and out of the flow, so the
    // composer's height is the same with and without them; it never wraps, it scrolls sideways.
    expect(form.classList.contains('relative')).toBe(true);
    expect(group.parentElement).toBe(form);
    for (const c of ['absolute', 'bottom-full', 'inset-x-0', 'overflow-x-auto']) expect(group.classList.contains(c)).toBe(true);
    expect(group.classList.contains('flex-wrap')).toBe(false);
    // Only the chips take pointer events: the conversation beneath still scrolls and clicks.
    expect(group.classList.contains('pointer-events-none')).toBe(true);
    const chips = within(group).getAllByRole('button');
    expect(chips.length).toBeGreaterThan(0);
    for (const chip of chips) {
      expect(chip.classList.contains('pointer-events-auto')).toBe(true);
      expect(chip.classList.contains('shrink-0')).toBe(true);
    }
    // Focus order is unchanged: the chips come before the composer's text.
    expect(chips[0].compareDocumentPosition(composer()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  test('the composer has no saved prompts', async () => {
    await openTask('t3');
    expect(screen.queryByRole('button', { name: 'Saved prompts' })).toBeNull();
  });

  test('Run again opens a new task linked to the one it repeats', async () => {
    const { user } = await openTask('t3');
    const menu = await openMenu(user, 'Task actions');
    await user.click(menu.getByRole('menuitem', { name: 'Run again' }));
    expect(await screen.findByText(/Runs again the last message of/)).toBeTruthy();
    await waitFor(() => expect(window.location.hash).not.toBe('#task=t3'));
  });
});
