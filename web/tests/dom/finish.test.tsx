// The Task pane on reopening and after a turn: the "Since you left" strip and the finish card (mock t20).
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { composer, openTask } from './render';

/** Marks t20 as last seen `minutes` ago, as an earlier visit would have. */
const seen = (minutes: number) => localStorage.setItem('uam.viewed', JSON.stringify({ t20: new Date(Date.now() - minutes * 60000).toISOString() }));

describe('since you left', () => {
  test('a Task that changed since the last look says what happened and jumps to the first new item', async () => {
    seen(69.5);
    // Only the conversation scrolls: scrollIntoView would also slide the app's clipped ancestors.
    const into = vi.spyOn(Element.prototype, 'scrollIntoView').mockImplementation(() => {});
    const scroll = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'scrollTo', { value: scroll, configurable: true, writable: true });
    const { user } = await openTask('t20');
    const strip = await screen.findByText(/^Since you left/);
    expect(strip.closest('p')?.textContent).toBe('Since you left (1 h): the agent finished, ran the tests plus 1 other command, and changed 2 files.');
    await user.click(screen.getByRole('button', { name: 'Jump to where you stopped' }));
    await waitFor(() => expect(scroll).toHaveBeenCalled());
    expect(into).not.toHaveBeenCalled();
    expect(screen.queryByText(/^Since you left/)).toBeNull();
    into.mockRestore();
    delete (HTMLElement.prototype as Partial<HTMLElement>).scrollTo;
  });

  test('it can be dismissed, and a Task with nothing new has none', async () => {
    seen(69.5);
    const { user } = await openTask('t20');
    await user.click(await screen.findByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText(/^Since you left/)).toBeNull();
  });

  test('a Task never opened in this browser has no strip', async () => {
    await openTask('t20');
    await screen.findByRole('heading', { name: 'Finished — check the evidence' });
    expect(screen.queryByText(/^Since you left/)).toBeNull();
  });
});

describe('finish card', () => {
  test('shows the checks before the claims and marks what nothing backs', async () => {
    const { user } = await openTask('t20');
    const card = within((await screen.findByRole('heading', { name: 'Finished — check the evidence' })).closest('section')!);
    expect(card.getByText('2 claims not verified')).toBeTruthy();
    const tests = card.getByText('Ran the tests').closest('li')!;
    expect(tests.textContent).toContain('go test ./internal/vterm/... -run Redraw · exit 0 · 1 package ok · 1s');
    expect(card.getByText('Ran go vet').closest('li')!.textContent).toContain('piped, so the exit status is the last command’s');
    expect(card.getByText(/vterm tests pass/).closest('li')!.textContent).toContain('Backed by go test ./internal/vterm/... -run Redraw · exit 0');
    expect(card.getByText('“go vet is clean.”').closest('li')!.textContent).toContain('Not verified · the last run’s result is unclear');
    expect(card.getByText(/describes the new replay order/).closest('li')!.textContent).toContain('Not verified · no edit to docs/terminal.md in this turn');
    expect(card.getByText('internal/vterm/redraw_test.go')).toBeTruthy();

    await user.click(card.getByRole('button', { name: 'Ask for changes' }));
    expect(document.activeElement).toBe(composer());
    await user.click(card.getByRole('button', { name: 'Review changes' }));
    expect(await screen.findByRole('dialog', { name: 'Changes' })).toBeTruthy();
  });

  test('Show output opens the check’s whole output', async () => {
    const { user } = await openTask('t20');
    const tests = (await screen.findByText('Ran the tests')).closest('li')!;
    await user.click(within(tests).getByRole('button', { name: 'Show output' }));
    const output = await screen.findByRole('region', { name: 'Output of go test ./internal/vterm/... -run Redraw' });
    expect(output.textContent).toContain('--- PASS: TestRedrawReplaysFocusEvents');
  });

  test('no card while the Task works', async () => {
    await openTask('t1');
    expect(screen.queryByRole('heading', { name: 'Finished — check the evidence' })).toBeNull();
  });
});
