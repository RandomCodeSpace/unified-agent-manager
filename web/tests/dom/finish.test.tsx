// The Task pane on reopening and after a turn: the "Since you left" strip and the finish card (mock t20).
import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api, type TurnEvidence } from '../../src/api';
import { log, openTask, sidebar } from './render';

const before = (minutes: number) => new Date(Date.now() - minutes * 60000).toISOString();
/** Marks t20 as last looked at `minutes` ago, as an earlier visit would have. */
const seen = (minutes: number) => localStorage.setItem('uam.looked', JSON.stringify({ t20: before(minutes) }));

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

  test('it counts from the owner’s last look, not from the Task’s last update', async () => {
    // The Task last changed 42 minutes ago; the owner watched it until 30 minutes ago.
    localStorage.setItem('uam.viewed', JSON.stringify({ t20: before(69.5) }));
    seen(30);
    await openTask('t20');
    await screen.findByRole('heading', { name: 'Finished — check the evidence' });
    expect(screen.queryByText(/^Since you left/)).toBeNull();
  });

  test('leaving a Task marks the last look', async () => {
    seen(500);
    const { user } = await openTask('t20');
    const aside = await sidebar();
    await user.click(aside.getByRole('button', { name: /Fix re-attach redraw regression/ }));
    await waitFor(() => expect(Date.now() - Date.parse((JSON.parse(localStorage.getItem('uam.looked') ?? '{}') as Record<string, string>).t20 ?? '')).toBeLessThan(5000));
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
    // The service reads the turn; the card shows its reading once it arrives.
    expect(await card.findByText('2 claims not verified')).toBeTruthy();
    const tests = card.getByText('Ran the tests').closest('li')!;
    expect(tests.textContent).toContain('go test ./internal/vterm/... -run Redraw · exit 0 · 1 package ok · 1s');
    expect(card.getByText('Ran go vet').closest('li')!.textContent).toContain('piped, so the exit status is the last command’s');
    expect(card.getByText(/vterm tests pass/).closest('li')!.textContent).toContain('Backed by go test ./internal/vterm/... -run Redraw · exit 0');
    expect(card.getByText('“go vet is clean.”').closest('li')!.textContent).toContain('Not verified · the last run’s result is unclear');
    expect(card.getByText(/describes the new replay order/).closest('li')!.textContent).toContain('Not verified · no edit to docs/terminal.md in this turn');
    expect(within(card.getByText('Changed in this turn').parentElement!).getByText('internal/vterm/redraw_test.go')).toBeTruthy();

    expect(card.queryByRole('button', { name: 'Ask for changes' })).toBeNull();
    await user.click(card.getByRole('button', { name: 'Review changes' }));
    expect(await screen.findByRole('dialog', { name: 'Changes' })).toBeTruthy();
  });

  test('the finish card and the answered question sit one step below the canvas, on tint-well', async () => {
    await openTask('t20');
    const finish = (await screen.findByRole('heading', { name: 'Finished — check the evidence' })).closest('section')!;
    const question = screen.getByRole('region', { name: 'Question' });
    for (const card of [finish, question]) {
      expect(card.classList).toContain('bg-tint-well');
      expect(card.classList).not.toContain('bg-raised');
    }
  });

  test('Show output opens the check’s whole output', async () => {
    const { user } = await openTask('t20');
    const tests = (await screen.findByText('Ran the tests')).closest('li')!;
    await user.click(within(tests).getByRole('button', { name: 'Show output' }));
    const output = await screen.findByRole('region', { name: 'Output of go test ./internal/vterm/... -run Redraw' });
    expect(output.textContent).toContain('--- PASS: TestRedrawReplaysFocusEvents');
  });

  test('a chat-only turn has no card: no check, claim, edited file or change to commit', async () => {
    const evidence = vi.spyOn(api, 'evidence');
    await openTask('t-chart');
    await waitFor(() => expect(evidence).toHaveBeenCalled());
    // Every read answered and rendered: still no card under the answer.
    await act(async () => { await Promise.allSettled(evidence.mock.results.map((r) => r.value as Promise<unknown>)); });
    expect(log().getByText(/September had/)).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Finished — check the evidence' })).toBeNull();
    expect(screen.queryByText('No tests, builds or linters ran in this turn.')).toBeNull();
    evidence.mockRestore();
  });

  test('claims with no checks show the claims and one line saying nothing ran, and no empty rows', async () => {
    await openTask('t3');
    const section = (await screen.findByRole('heading', { name: 'Finished — check the evidence' })).closest('section')!;
    const card = within(section);
    expect(card.getByText('1 claim not verified')).toBeTruthy();
    expect(card.getByText(/doctor tests pass/).closest('li')!.textContent).toContain('Not verified · no test run in this turn');
    expect(card.getAllByText('No tests, builds or linters ran in this turn.')).toHaveLength(1);
    expect(card.queryByText('Changed in this turn')).toBeNull();
    expect(card.queryByRole('button', { name: 'Review changes' })).toBeNull();
    // The Task's own uncommitted files keep the commit panel; no part of the card is empty.
    expect(await card.findByRole('region', { name: 'Commit' })).toBeTruthy();
    for (const part of section.children) expect(part.textContent?.trim()).not.toBe('');
  });

  test('no card flashes while the evidence loads', async () => {
    const real = api.evidence.bind(api);
    const held: (() => void)[] = [];
    let released = false;
    const evidence = vi.spyOn(api, 'evidence').mockImplementation((...args) => released ? real(...args) : new Promise<TurnEvidence>((resolve, reject) => {
      held.push(() => { real(...args).then(resolve, reject); });
    }));
    await openTask('t20');
    await waitFor(() => expect(held.length).toBeGreaterThan(0));
    expect(screen.queryByRole('heading', { name: 'Finished — check the evidence' })).toBeNull();
    expect(document.getElementById('finish-title')).toBeNull();
    released = true;
    for (const release of held) release();
    expect(await screen.findByRole('heading', { name: 'Finished — check the evidence' })).toBeTruthy();
    evidence.mockRestore();
  });

  test('no card while the Task works', async () => {
    await openTask('t1');
    expect(screen.queryByRole('heading', { name: 'Finished — check the evidence' })).toBeNull();
  });
});
