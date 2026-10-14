// The Task pane on reopening and after a turn: the "Since you left" strip and branch evidence (mock t20).
import { act, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, onTestFinished, test, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { UamApp } from '@uam/ui';
import { api, newRequestId, type TurnEvidence } from '../../src/api';
import { install } from '../../src/mock/install';
import * as data from '../../src/mock/data';
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
    await screen.findByRole('button', { name: /evidence available/ });
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
    await screen.findByRole('button', { name: /evidence available/ });
    expect(screen.queryByText(/^Since you left/)).toBeNull();
  });

  test('leaving for Settings counts as leaving: what the Task does meanwhile is unseen, and the strip shows on the way back', async () => {
    seen(500);
    const { user } = await openTask('t20');
    await user.click(await screen.findByRole('button', { name: 'Dismiss' }));
    const aside = await sidebar();
    await user.click(aside.getByRole('button', { name: 'Settings' }));
    await screen.findByRole('heading', { name: 'Settings' });
    const looked = () => (JSON.parse(localStorage.getItem('uam.looked') ?? '{}') as Record<string, string>).t20;
    const left = looked();
    expect(Date.now() - Date.parse(left)).toBeLessThan(5000);
    // The Task takes a message while Settings covers it: its frames do not count as looks.
    await api.prompt('t20', 'Run the tests again', newRequestId());
    await api.cancel('t20');
    await act(() => new Promise((r) => setTimeout(r, 100)));
    expect(looked()).toBe(left);
    await user.click(screen.getByRole('button', { name: 'Close settings' }));
    const strip = await screen.findByText(/^Since you left/);
    expect(strip.closest('p')?.textContent).toMatch(/^Since you left \(<1 min\)/);
  });
});

describe('evidence inside Changes', () => {
  test('shows the checks before the claims and marks what nothing backs', async () => {
    const { user } = await openTask('t20');
    const trigger = await screen.findByRole('button', { name: /Open changes.+evidence available/ });
    expect(trigger.closest('header')).not.toBeNull();
    const vcs = within(screen.getByRole('group', { name: 'Version control' }));
    const changes = vcs.getByRole('button');
    // The branch is under the composer, not on Changes.
    expect(changes.textContent).not.toContain('feat/web-project-defaults-and-sidebar');
    expect(changes).toBe(trigger);
    expect(vcs.getAllByRole('button')).toHaveLength(1);
    expect(screen.queryByRole('heading', { name: 'Finished — check the evidence' })).toBeNull();
    expect(log().queryByText('Ran the tests')).toBeNull();
    expect(trigger.querySelector('.bg-warning')).not.toBeNull();
    expect(trigger.querySelector('.animate-pulse-dot')).toBeNull();
    await user.click(trigger);
    const card = within(await screen.findByRole('region', { name: 'Finished — check the evidence' }));
    expect(card.queryByRole('region', { name: 'Commit' })).toBeNull();
    expect(card.queryByRole('textbox', { name: 'Commit message' })).toBeNull();
    // The service reads the turn; the card shows its reading once it arrives.
    expect(await card.findByText('2 claims not verified')).toBeTruthy();
    const tests = card.getByText('Ran the tests').closest('li')!;
    expect(tests.textContent).toContain('go test ./internal/vterm/... -run Redraw · exit 0 · 1 package ok · 1s');
    expect(card.getByText('Ran go vet').closest('li')!.textContent).toContain('piped');
    expect(card.getByText(/vterm tests pass/).closest('li')!.textContent).toContain('Backed by go test ./internal/vterm/... -run Redraw · exit 0');
    expect(card.getByText('“go vet is clean.”').closest('li')!.textContent).toContain('Not verified · the last run’s result is unclear');
    expect(card.getByText(/describes the new replay order/).closest('li')!.textContent).toContain('Not verified · no edit to docs/terminal.md in this turn');
    expect(within(card.getByText('Changed in this turn').parentElement!).getByText('internal/vterm/redraw_test.go')).toBeTruthy();

    expect(card.queryByRole('button', { name: 'Ask for changes' })).toBeNull();
    expect(card.queryByRole('button', { name: 'Review changes' })).toBeNull();
    const panel = await screen.findByRole('dialog', { name: 'Changes' });
    expect(within(panel).getByRole('region', { name: 'Finished — check the evidence' })).toBeTruthy();
    // The repository read starts when Changes opens, independently of the evidence.
    expect(await within(panel).findByRole('region', { name: 'Commit' })).toBeTruthy();
    expect(screen.queryByRole('dialog', { name: 'Finished — check the evidence' })).toBeNull();
  });

  test('keyboard and close button dismiss Changes and restore branch focus', async () => {
    const { user } = await openTask('t20');
    const trigger = await screen.findByRole('button', { name: /evidence available/ });
    trigger.focus();
    await user.keyboard('{Enter}');
    const dialog = await screen.findByRole('dialog', { name: 'Changes' });
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    // The sheet focuses Refresh, whose tooltip owns Escape. Move to Close first.
    await user.tab();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Changes' })).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(trigger));
    await user.click(trigger);
    await user.click(await screen.findByRole('button', { name: 'Close changes' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Changes' })).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  test('Show output opens the check’s whole output', async () => {
    const { user } = await openTask('t20');
    await user.click(await screen.findByRole('button', { name: /evidence available/ }));
    const tests = (await screen.findByText('Ran the tests')).closest('li')!;
    await user.click(within(tests).getByRole('button', { name: 'Show output' }));
    const output = await screen.findByRole('region', { name: 'Output of go test ./internal/vterm/... -run Redraw' });
    expect(output.textContent).toContain('--- PASS: TestRedrawReplaysFocusEvents');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Changes' })).toBeNull());
  });

  test('a chat-only turn has no review dot and opens Changes directly', async () => {
    const evidence = vi.spyOn(api, 'evidence');
    const { user } = await openTask('t-chart');
    await waitFor(() => expect(evidence).toHaveBeenCalled());
    // Every read answered and rendered: still no card under the answer.
    await act(async () => { await Promise.allSettled(evidence.mock.results.map((r) => r.value as Promise<unknown>)); });
    expect(log().getByText(/September had/)).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Finished — check the evidence' })).toBeNull();
    expect(screen.queryByRole('button', { name: /evidence available/ })).toBeNull();
    expect(screen.queryByText('No tests, builds or linters ran in this turn.')).toBeNull();
    const trigger = within(screen.getByRole('group', { name: 'Version control' })).getByRole('button');
    // Nothing to review is no dot at all, not a grey one: the state never rests on colour alone.
    expect(trigger.querySelector('.rounded-full')).toBeNull();
    await user.click(trigger);
    expect(await screen.findByRole('dialog', { name: 'Changes' })).toBeTruthy();
    evidence.mockRestore();
  });

  test('claims with no checks show the claims and one line saying nothing ran, and no empty rows', async () => {
    const { user } = await openTask('t3');
    await user.click(await screen.findByRole('button', { name: /evidence available/ }));
    const section = await screen.findByRole('region', { name: 'Finished — check the evidence' });
    const card = within(section);
    expect(card.getByText('1 claim not verified')).toBeTruthy();
    expect(card.getByText(/doctor tests pass/).closest('li')!.textContent).toContain('Not verified · no test run in this turn');
    expect(card.getAllByText('No tests, builds or linters ran in this turn.')).toHaveLength(1);
    expect(card.queryByText('Changed in this turn')).toBeNull();
    expect(card.queryByRole('button', { name: 'Review changes' })).toBeNull();
    expect(card.queryByRole('region', { name: 'Commit' })).toBeNull();
    for (const part of section.children) expect(part.textContent?.trim()).not.toBe('');
  });

  test('opening a finished Task reads its evidence once, not again when Changes first lists its files', async () => {
    const evidence = vi.spyOn(api, 'evidence');
    history.replaceState(null, '', '/#task=t20');
    install();
    // Without StrictMode's development double mount: the reads a production page makes.
    render(<UamApp />);
    await screen.findByRole('button', { name: /not committed, evidence available/ });
    await new Promise((r) => setTimeout(r, 300));
    expect(evidence.mock.calls.filter(([id, look]) => id === 't20' && !look)).toHaveLength(1);
    evidence.mockRestore();
  });

  test('coming back to a finished Task reads its evidence once more, not twice', async () => {
    const evidence = vi.spyOn(api, 'evidence');
    history.replaceState(null, '', '/#task=t20');
    install();
    render(<UamApp />);
    const user = userEvent.setup();
    await screen.findByRole('button', { name: /not committed, evidence available/ });
    const aside = await sidebar();
    await user.click(aside.getByRole('button', { name: /Fix re-attach redraw regression/ }));
    await waitFor(() => expect(screen.queryByRole('button', { name: /evidence available/ })).toBeNull());
    evidence.mockClear();
    await user.click(aside.getAllByRole('button', { name: /Replay focus events on re-attach/ })[0]);
    await screen.findByRole('button', { name: /not committed, evidence available/ });
    await new Promise((r) => setTimeout(r, 300));
    // The other read of the same route is "Since you left" (`since`/`until`), from the look that ended on leaving.
    expect(evidence.mock.calls.filter(([id, look]) => id === 't20' && !look)).toHaveLength(1);
    expect(evidence.mock.calls.filter(([id, look]) => id === 't20' && look)).toHaveLength(1);
    evidence.mockRestore();
  });

  test('coming back to a kept Task from another reads its evidence once more, not twice', async () => {
    // As the service streams it: compact snapshots, so a Task left is kept and shown at once on coming back.
    const state = data.seed();
    Object.assign(state.tasks.find((t) => t.id === 't20')!, { representation: 'compact-v1', detail_stream: true });
    vi.spyOn(data, 'seed').mockReturnValue(state);
    history.replaceState(null, '', '/#task=t20');
    install();
    const Source = window.EventSource;
    onTestFinished(() => { window.EventSource = Source; vi.restoreAllMocks(); });
    let epoch = '';
    window.EventSource = class extends Source {
      addEventListener(type: string, listener: EventListenerOrEventListenerObject, options?: boolean | AddEventListenerOptions) {
        if (type !== 'snapshot' || typeof listener !== 'function') return super.addEventListener(type, listener, options);
        super.addEventListener(type, (e) => {
          const snapshot = JSON.parse((e as MessageEvent).data);
          epoch ||= snapshot.session?.epoch;
          listener.call(this, new MessageEvent(type, { data: JSON.stringify({ ...snapshot, representation: 'compact-v1', epoch, detail_stream: true }) }));
        }, options);
      }
    };
    const evidence = vi.spyOn(api, 'evidence');
    render(<UamApp />);
    const user = userEvent.setup();
    await screen.findByRole('button', { name: /not committed, evidence available/ });
    const aside = await sidebar();
    await user.click(aside.getByRole('button', { name: /Fix re-attach redraw regression/ }));
    await waitFor(() => expect(screen.queryByRole('button', { name: /evidence available/ })).toBeNull());
    await new Promise((r) => setTimeout(r, 300));
    evidence.mockClear();
    await user.click(aside.getAllByRole('button', { name: /Replay focus events on re-attach/ })[0]);
    await screen.findByRole('button', { name: /not committed, evidence available/ });
    await new Promise((r) => setTimeout(r, 300));
    // The kept Task is on screen at once and its fresh detail replaces it in place: one view, one read of each.
    expect(evidence.mock.calls.filter(([id, look]) => id === 't20' && !look)).toHaveLength(1);
    expect(evidence.mock.calls.filter(([id, look]) => id === 't20' && look)).toHaveLength(1);
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
    expect(screen.queryByRole('button', { name: /evidence available/ })).toBeNull();
    released = true;
    for (const release of held) release();
    expect(await screen.findByRole('button', { name: /evidence available/ })).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Finished — check the evidence' })).toBeNull();
    evidence.mockRestore();
  });

  test('while the Task works, changed files show a steady amber dot and open Changes without an evidence popover', async () => {
    const { user } = await openTask('t1');
    expect(screen.queryByRole('button', { name: /evidence available/ })).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Finished — check the evidence' })).toBeNull();
    const trigger = await screen.findByRole('button', { name: /^Open changes.*4 not committed$/ });
    // The count is the folder's (git_pending, with the Thread); the dot waits for Changes to list the Thread's files.
    await waitFor(() => expect(trigger.querySelector('.bg-warning')).not.toBeNull());
    expect(trigger.querySelector('.animate-pulse-dot')).toBeNull();
    // The folder's count leaves untracked files out, and its tip says so.
    await user.hover(trigger);
    expect(await screen.findByText('4 tracked files not committed · untracked files are not counted')).toBeTruthy();
    await user.click(trigger);
    expect(await screen.findByRole('dialog', { name: 'Changes' })).toBeTruthy();
  });
});
