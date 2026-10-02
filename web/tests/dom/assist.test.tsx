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

  test('a saved prompt is found by name and inserted; the composer text can be saved as one', async () => {
    const { user, mock } = await openTask('t3');
    await user.click(screen.getByRole('button', { name: 'Saved prompts' }));
    const search = await screen.findByRole('textbox', { name: 'Search saved prompts by name' });
    await user.type(search, 'commit');
    expect(screen.queryByText('Review the diff')).toBeNull();
    // The finish card's commit panel has a "Commit and push" of its own.
    await user.click(within(search.closest<HTMLElement>('[role="dialog"]')!).getByRole('button', { name: /Commit and push/ }));
    expect(composer().value).toBe('Commit the change with a conventional commit message and push the branch.');

    await user.click(screen.getByRole('button', { name: 'Saved prompts' }));
    await user.click(await screen.findByRole('button', { name: 'Save as prompt' }));
    await user.type(screen.getByRole('textbox', { name: 'Prompt name' }), 'Ship it');
    await user.click(screen.getByRole('button', { name: 'Save prompt' }));
    expect(await screen.findByRole('button', { name: /Ship it/ })).toBeTruthy();
    // Saving sits inside the composer's React tree: it must not send the message too.
    expect(mock.received.filter((r) => r.route === 'prompt')).toHaveLength(0);
    expect(composer().value).toBe('Commit the change with a conventional commit message and push the branch.');
  });

  test('Run again opens a new task linked to the one it repeats', async () => {
    const { user } = await openTask('t3');
    const menu = await openMenu(user, 'Task actions');
    await user.click(menu.getByRole('menuitem', { name: 'Run again' }));
    expect(await screen.findByText(/Runs again the last message of/)).toBeTruthy();
    await waitFor(() => expect(window.location.hash).not.toBe('#task=t3'));
  });
});
