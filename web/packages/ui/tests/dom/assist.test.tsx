import { screen, waitFor } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { composer, openMenu, openTask, renderApp } from './render';

/** The suggestion's accessible description, present while the ghost shows. */
const ghost = () => document.getElementById('composer-suggestion');

describe('assist', () => {
  // First in the file: the suggestions are kept per Task for the page's life.
  test('a suggestion withheld while Background AI is paused is asked for again on the next look', async () => {
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
    expect(ghost()).toBeNull();
    expect(composer().placeholder).not.toBe('');
    // The limit is raised; another Task, then back: the service kept nothing, so the page asks again.
    paused = false;
    window.location.hash = '#task=t1';
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).not.toBe(title));
    window.location.hash = '#task=t3';
    await waitFor(() => expect(ghost()).not.toBeNull());
  });

  test('after a finished turn the first suggestion is ghost text in the empty composer, described, not its value', async () => {
    await openTask('t3');
    await waitFor(() => expect(ghost()).not.toBeNull());
    const box = composer();
    expect(box.value).toBe('');
    // It stands in for the placeholder: the native one steps aside, and the ghost is read as the box's description.
    expect(box.placeholder).toBe('');
    expect(box.getAttribute('aria-describedby')).toBe('composer-suggestion');
    expect(ghost()!.textContent).toBe('Suggestion: Run the whole test suite, press Right Arrow to use it');
    // The visible copy is hidden from screen readers and sits over the textarea, out of its flow, so the composer keeps its height.
    const shown = screen.getByText('Run the whole test suite');
    expect(shown.getAttribute('aria-hidden')).toBe('true');
    expect(shown.classList.contains('line-clamp-2')).toBe(true);
    const layer = shown.parentElement!;
    expect(layer.classList.contains('absolute')).toBe(true);
    expect(layer.classList.contains('pointer-events-none')).toBe(true);
    expect(layer.parentElement).toBe(box.parentElement);
    // Only the first of the replies shows.
    expect(screen.queryByText('Show me the diff')).toBeNull();
  });

  test('Right Arrow or End in the empty composer uses the suggestion and sends nothing; typing replaces it; clearing brings it back', async () => {
    const { user, mock } = await openTask('t3');
    await waitFor(() => expect(ghost()).not.toBeNull());
    await user.click(composer());
    await user.keyboard('{ArrowRight}');
    expect(composer().value).toBe('Run the whole test suite');
    expect(composer().selectionStart).toBe('Run the whole test suite'.length);
    expect(ghost()).toBeNull();
    // Once there is text, the arrow keys move the caret as usual.
    await user.keyboard('{ArrowLeft}{ArrowRight}');
    expect(composer().value).toBe('Run the whole test suite');
    expect(mock.received.filter((r) => r.route === 'prompt')).toHaveLength(0);

    await user.clear(composer());
    await waitFor(() => expect(ghost()).not.toBeNull());
    await user.keyboard('{End}');
    expect(composer().value).toBe('Run the whole test suite');

    await user.clear(composer());
    await waitFor(() => expect(ghost()).not.toBeNull());
    await user.keyboard('Wait');
    expect(composer().value).toBe('Wait');
    expect(ghost()).toBeNull();
    expect(composer().getAttribute('aria-describedby')).toBeNull();
    expect(mock.received.filter((r) => r.route === 'prompt')).toHaveLength(0);
  });

  test('the visible Use button at the end of the ghost text uses the suggestion', async () => {
    const { user, mock } = await openTask('t3');
    const use = await screen.findByRole('button', { name: 'Use suggestion' });
    // Shown on every pointer, so the ghost is never only a keyboard affordance; an arrow alone, named for assistive tech.
    expect(use.classList.contains('hidden')).toBe(false);
    expect(use.textContent).toBe('');
    expect(use.querySelector('svg')).toBeTruthy();
    await user.click(use);
    expect(composer().value).toBe('Run the whole test suite');
    expect(document.activeElement).toBe(composer());
    expect(mock.received.filter((r) => r.route === 'prompt')).toHaveLength(0);
  });

  test('no suggestion while the Task works, waits for an answer, or is read-only', async () => {
    const { user } = await openTask('t3');
    await waitFor(() => expect(ghost()).not.toBeNull());
    // Sending starts a turn: the ghost goes with it.
    await user.click(composer());
    await user.keyboard('Go on{Enter}');
    await waitFor(() => expect(composer().value).toBe(''));
    await new Promise((r) => setTimeout(r, 100));
    expect(ghost()).toBeNull();
    expect(composer().placeholder).not.toBe('');
    for (const id of ['t1', 't2', 't12']) {
      window.location.hash = `#task=${id}`;
      await waitFor(() => expect(window.location.hash).toBe(`#task=${id}`));
      await new Promise((r) => setTimeout(r, 150));
      expect(ghost()).toBeNull();
      expect(screen.queryByRole('button', { name: 'Use suggestion' })).toBeNull();
    }
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
