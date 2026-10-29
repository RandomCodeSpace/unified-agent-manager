import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, onTestFinished, test, vi } from 'vitest';
import { api } from '../../src/api';
import * as mockData from '../../src/mock/data';
import { headerRoom } from '../../src/components/Task';
import { composer, log, openMenu, openSubagentIndex, openTask, renderApp, sidebar, type User } from './render';

describe('changes', () => {
  test('the header count opens the Thread\'s changes as a stack of files, each with its diff', async () => {
    const { user } = await openTask('t1');
    await user.click(await screen.findByRole('button', { name: /^Open changes, 3 files$/ }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    expect(await sheet.findByText(/^3 files by this thread/)).toBeTruthy();
    // Only this Thread's changes: no scopes to pick, no commit form.
    expect(sheet.queryByRole('radio')).toBeNull();
    expect(sheet.queryByRole('region', { name: 'Commit' })).toBeNull();
    expect(sheet.getByText('CI workflow')).toBeTruthy();
    expect(await sheet.findByText(/func TestRedrawReplaysFocusEvents/)).toBeTruthy();
    await user.click(sheet.getByRole('button', { name: 'Refresh' }));
    expect(await sheet.findByText(/func TestRedrawReplaysFocusEvents/)).toBeTruthy();
    await user.click(sheet.getByRole('button', { name: 'Close changes' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Changes' })).toBeNull());
    await waitFor(() => expect(document.activeElement?.id).toBe('changes-link'));
  });

  test('a card names its file in ink after its muted folder, and the header count wraps rather than clips', async () => {
    const { user } = await openTask('t1');
    await user.click(await screen.findByRole('button', { name: /^Open changes/ }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    const card = within(await sheet.findByRole('region', { name: 'internal/vterm/redraw_test.go' }));
    const name = card.getByText('redraw_test.go');
    expect(name.className).toContain('text-ink');
    expect(card.getByText('internal/vterm/').className).toContain('text-muted');
    expect(name.parentElement!.className).toContain('truncate');
    expect(name.parentElement!.getAttribute('title')).toBe('internal/vterm/redraw_test.go');
    const viewed = sheet.getByText(/^\d+ of \d+ viewed$/);
    expect(viewed.className).toContain('whitespace-nowrap');
    expect(viewed.parentElement!.className).toContain('line-clamp-2');
    expect(viewed.parentElement!.className).not.toMatch(/\btruncate\b/);
  });

  test('a card\'s menu copies its path and reverts the whole file only once confirmed', async () => {
    const revert = vi.spyOn(api, 'revertChange');
    try {
      const { user } = await openTask('t8');
      await user.click(await screen.findByRole('button', { name: /^Open changes, 2 files$/ }));
      const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
      expect(await sheet.findByRole('region', { name: 'assets/theme.css' })).toBeTruthy();
      let menu = await openMenu(user, 'Actions for assets/theme.css');
      expect(menu.getByRole('menuitem', { name: 'Copy path' })).toBeTruthy();
      await user.click(menu.getByRole('menuitem', { name: 'Revert file…' }));
      const ask = within(await screen.findByRole('alertdialog'));
      expect(ask.getByText('Revert this file?')).toBeTruthy();
      expect(revert).not.toHaveBeenCalled();
      await user.click(ask.getByRole('button', { name: 'Cancel' }));
      expect(revert).not.toHaveBeenCalled();
      menu = await openMenu(user, 'Actions for assets/theme.css');
      await user.click(menu.getByRole('menuitem', { name: 'Revert file…' }));
      await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Revert file' }));
      await waitFor(() => expect(revert).toHaveBeenCalledWith('t8', 'assets/theme.css', undefined));
      await waitFor(() => expect(sheet.queryByRole('region', { name: 'assets/theme.css' })).toBeNull());
    } finally {
      revert.mockRestore();
    }
  });

  test('a hunk\'s Revert puts back that change alone', async () => {
    const revert = vi.spyOn(api, 'revertChange');
    try {
      const { user } = await openTask('t8');
      await user.click(await screen.findByRole('button', { name: /^Open changes/ }));
      const card = within(await within(await screen.findByRole('dialog', { name: 'Changes' })).findByRole('region', { name: 'templates/post.html' }));
      await user.click(await card.findByRole('button', { name: 'Revert' }));
      await waitFor(() => expect(revert).toHaveBeenCalledTimes(1));
      const [id, path, hunk] = revert.mock.calls[0];
      expect([id, path]).toEqual(['t8', 'templates/post.html']);
      expect(hunk).toMatch(/^@@ -\d+,\d+ \+\d+,\d+ @@\n/);
    } finally {
      revert.mockRestore();
    }
  });

  test('a turn\'s Changed chip opens the same sheet on that turn\'s file, and focus comes back to it', async () => {
    const { user } = await openTask('t1');
    const latest = screen.getAllByRole('button', { name: /^Changed \d+ files?$/ }).at(-1)!;
    await user.click(latest);
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    const card = within(await sheet.findByRole('region', { name: 'internal/vterm/redraw_test.go' }));
    expect(card.getByRole('button', { name: /diff of internal\/vterm\/redraw_test.go$/ }).getAttribute('aria-expanded')).toBe('true');
    await user.click(sheet.getByRole('button', { name: 'Close changes' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Changes' })).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(latest));
  });

  test('inline, Changes stays open for presses on the composer and the conversation\'s controls; a press on empty canvas closes it', async () => {
    const width = window.innerWidth;
    onTestFinished(() => { window.innerWidth = width; });
    window.innerWidth = 1440;
    const { user } = await openTask('t1');
    await user.click(await screen.findByRole('button', { name: /^Open changes/ }));
    const panel = () => document.querySelector('aside[aria-label="Changes"]:not([inert]) > :not([inert])');
    await waitFor(() => expect(panel()).not.toBeNull());
    await user.click(composer());
    const conversation = screen.getByRole('region', { name: 'Conversation' });
    await user.click(conversation.querySelector('button[aria-expanded="false"]')!);
    expect(panel()).not.toBeNull();
    await user.click(conversation);
    await waitFor(() => expect(panel()).toBeNull());
  });

  test('a Viewed mark made while comments send survives the send', async () => {
    localStorage.setItem('uam.review.t1', JSON.stringify({ viewed: {}, comments: [{ id: 'c1', path: '.github/workflows/ci.yml', line: 1, side: 'new', code: 'x', body: 'Please fix' }] }));
    const { user } = await openTask('t1');
    await user.click(await screen.findByRole('button', { name: /^Open changes/ }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    const send = await sheet.findByRole('button', { name: 'Send 1 comment to the thread' });
    const box = (await sheet.findAllByRole('checkbox', { name: /^Viewed / }))[0] as HTMLInputElement;
    fireEvent.click(send);
    fireEvent.click(box); // while the send is in flight
    expect(box.checked).toBe(true);
    await sheet.findByText(/Comments (sent|queued)/);
    expect(box.checked).toBe(true);
    const stored = JSON.parse(localStorage.getItem('uam.review.t1') ?? 'null') as { viewed: Record<string, string>; comments: unknown[] } | null;
    expect(Object.keys(stored?.viewed ?? {})).toHaveLength(1);
    expect(stored?.comments).toEqual([]);
  });

  test('comments the Thread refuses keep a note saying so', async () => {
    localStorage.setItem('uam.review.t1', JSON.stringify({ viewed: {}, comments: [{ id: 'c1', path: '.github/workflows/ci.yml', line: 1, side: 'new', code: 'x', body: 'Please fix' }] }));
    const prompt = vi.spyOn(api, 'prompt').mockResolvedValueOnce({ status: 'rejected' } as never);
    try {
      const { user } = await openTask('t1');
      await user.click(await screen.findByRole('button', { name: /^Open changes/ }));
      const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
      await user.click(await sheet.findByRole('button', { name: 'Send 1 comment to the thread' }));
      expect(await sheet.findByText('The thread did not take the comments.')).toBeTruthy();
    } finally {
      prompt.mockRestore();
    }
  });

  test('a project without Git offers no changes, and says why', async () => {
    const { user } = await openTask('t6');
    expect(screen.queryByRole('button', { name: /^Open changes/ })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Not a Git repository' }));
    expect(await screen.findByText(/is not in a Git repository/)).toBeTruthy();
  });

  test('inline, a panel replaced by another leaves at once; a plain close still slides it out first', async () => {
    const original = window.matchMedia.bind(window);
    const wide = vi.spyOn(window, 'matchMedia').mockImplementation((query) => (query === '(min-width: 1280px)' ? ({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList) : original(query)));
    const aside = (label: string) => document.querySelector(`aside[aria-label="${label}"]`);
    try {
      const { user } = await openTask('t-chart');
      // A lazy panel's first opening lets the outgoing one slide out while it loads: Changes has opened once first.
      await user.click(await screen.findByRole('button', { name: /^Open changes/ }));
      await waitFor(() => expect(aside('Changes')).toBeTruthy());
      await user.click(screen.getByRole('button', { name: 'Close changes' }));
      await waitFor(() => expect(aside('Changes')).toBeNull());
      await user.click(screen.getByRole('button', { name: 'Pinned charts, 2' }));
      await waitFor(() => expect(aside('Pinned charts')).toBeTruthy());
      await user.click(screen.getByRole('button', { name: /^Open changes/ }));
      expect(aside('Pinned charts')).toBeNull();
      await waitFor(() => expect(aside('Changes')).toBeTruthy());
      await user.click(screen.getByRole('button', { name: 'Close changes' }));
      expect(aside('Changes')).toBeTruthy();
      await waitFor(() => expect(aside('Changes')).toBeNull());
    } finally {
      wide.mockRestore();
    }
  });

  test('inline, a side panel stands back from Esc while Settings covers the Thread; the next Esc closes it', async () => {
    const original = window.matchMedia.bind(window);
    const wide = vi.spyOn(window, 'matchMedia').mockImplementation((query) => (query === '(min-width: 1280px)' ? ({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList) : original(query)));
    const aside = (label: string) => document.querySelector(`aside[aria-label="${label}"]`);
    const overSettings = async (user: User, label: string) => {
      await user.click((await sidebar()).getByRole('button', { name: 'Settings' }));
      await screen.findByRole('heading', { level: 1, name: 'Settings' });
      await user.keyboard('{Escape}');
      await waitFor(() => expect(screen.queryByRole('heading', { level: 1, name: 'Settings' })).toBeNull());
      expect(aside(label)).toBeTruthy();
      // Focus came back to the sidebar's Settings; a panel takes Esc from the page, not from another region.
      (document.activeElement as HTMLElement).blur();
      await user.keyboard('{Escape}');
      await waitFor(() => expect(aside(label)).toBeNull());
    };
    try {
      const { user } = await openTask('t-chart');
      await user.click(await screen.findByRole('button', { name: 'Pinned charts, 2' }));
      await waitFor(() => expect(aside('Pinned charts')).toBeTruthy());
      await overSettings(user, 'Pinned charts');
      await user.click(screen.getByRole('button', { name: /^Open changes/ }));
      await waitFor(() => expect(aside('Changes')).toBeTruthy());
      await overSettings(user, 'Changes');
    } finally {
      wide.mockRestore();
    }
  });
});

describe('subagents', () => {
  // The strip's sheet: one box for the subagents.
  const sheet = async () => within(await screen.findByRole('dialog', { name: 'Subagents' }));
  const live = () => within(screen.getByRole('region', { name: 'Subagents at work' }));
  const flashed = () => document.querySelector<HTMLElement>('.animate-flash');

  // The strip's Subagents tab: subagents by the message that started their reply, filters, and a pick that opens its detail in the same box.
  test('the strip\'s subagent list groups subagents by their message; a pick opens its detail in the same box, and ‹ All goes back', async () => {
    const { user } = await openTask('t8');
    // The header has no Subagents button any more: the strip counts them.
    expect(document.querySelector('header')!.textContent).not.toContain('Subagents');
    const index = await openSubagentIndex(user);
    const group = within(index.getByRole('region', { name: /^“Audit templates\/post\.html for accessibility problems/ }));
    expect(group.getByRole('button', { name: /^Jump to the subagents of/ })).toBeTruthy();
    expect(index.queryByText(/Turn \d/)).toBeNull();
    // Chips only for the states some subagent is in: none stopped here.
    expect(index.getAllByRole('button', { pressed: false }).map((b) => b.textContent)).toEqual(['Running 3', 'Failed 1', 'Done 1']);
    await user.click(index.getByRole('button', { name: 'Failed 1' }));
    expect(index.queryByRole('button', { name: /^Check the heading order/ })).toBeNull();
    await user.click(index.getByRole('button', { name: /^Run the accessibility linter/ }));
    // The same box: the detail takes the list's place, its way back focused, its place in the list said.
    expect(await index.findByRole('region', { name: 'Transcript of Run the accessibility linter' })).toBeTruthy();
    expect(index.getByText('1 of 1')).toBeTruthy();
    await waitFor(() => expect(document.activeElement?.textContent).toBe('All 1'));
    // The error is said once, under Error, not again as a result.
    expect(index.getByRole('region', { name: 'Error' }).textContent).toContain('axe-core is not installed in this project.');
    expect(index.queryByRole('region', { name: 'Result' })).toBeNull();
    await user.keyboard('{ArrowLeft}');
    // Back on the list, the row it came from lit and focused.
    expect(await index.findByRole('searchbox', { name: 'Search subagents' })).toBeTruthy();
    await waitFor(() => expect(document.activeElement?.getAttribute('aria-label')).toMatch(/^Run the accessibility linter, failed/));
    expect(document.activeElement?.getAttribute('aria-current')).toBe('true');
  });

  test('a row, the strip and the reply\'s chip open the same sheet: on that subagent, on the list, on the reply\'s list', async () => {
    const { user } = await openTask('t8');
    const link = screen.getByRole('button', { name: /^Subagents: .*Show the subagents$/ });
    const opened = async () => {
      const dialog = await screen.findByRole('dialog', { name: 'Subagents' });
      // The strip's own sheet: its subagents button controls it.
      expect(link.getAttribute('aria-controls')).toBe(dialog.id);
      expect(within(dialog).getByRole('heading', { name: 'Subagents' })).toBeTruthy();
      return dialog;
    };
    const closed = async () => {
      await user.keyboard('{Escape}');
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    };
    // A row: the sheet on that subagent's detail; Esc gives the focus back to the row.
    const row = live().getByRole('button', { name: /^Check contrast of the theme tokens, running/ });
    await user.click(row);
    const fromRow = await opened();
    expect(await within(fromRow).findByRole('region', { name: 'Transcript of Check contrast of the theme tokens' })).toBeTruthy();
    expect(within(fromRow).getByText('2 of 5')).toBeTruthy();
    await closed();
    expect(document.activeElement).toBe(row);
    // The strip: the same sheet on the whole list.
    await user.click(link);
    const fromStrip = await opened();
    expect(fromStrip.id).toBe(fromRow.id);
    expect(within(fromStrip).getByRole('searchbox', { name: 'Search subagents' }).getAttribute('placeholder')).toBe('Search 5 subagents');
    expect(within(fromStrip).queryByRole('button', { name: /^From / })).toBeNull();
    await closed();
    // The chip: the same sheet on the list filtered to its reply, until "From … ×" lifts it.
    await user.click(live().getByRole('button', { name: /^5 subagents · [\d.]+M tokens · 1 done · 1 failed · 3 running/ }));
    const fromChip = await opened();
    expect(fromChip.id).toBe(fromRow.id);
    const filter = within(fromChip).getByRole('button', { name: /^From .*show every message's subagents$/ });
    expect(within(fromChip).getAllByRole('button', { name: /, (running|failed|completed)/ })).toHaveLength(5);
    await user.click(filter);
    expect(within(fromChip).queryByRole('button', { name: /^From / })).toBeNull();
  });

  test('the strip is three sections, the list, the progress line and the subagents, whose button opens the sheet without a Todo tab', async () => {
    const { user } = await openTask('t8');
    // The todo list and the subagents are buttons of their own; the progress line between them waits on the model, plain words.
    const strip = screen.getByRole('button', { name: /Show the list$/ });
    const subagents = screen.getByRole('button', { name: 'Subagents: 5, 1 failed, 3 running, 1 done. Show the subagents' });
    const progress = screen.getByText(/^Waiting for the model, /, { selector: '.sr-only' }).parentElement!;
    expect(progress.tagName).toBe('SPAN');
    expect(strip.compareDocumentPosition(progress) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(progress.compareDocumentPosition(subagents) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(within(strip).getByText('3 of 8')).toBeTruthy();
    // ✗ failed, ● running, ✓ done: each its number behind its glyph, in its tone; none stopped, so no block for it.
    const cells = (blocks: string) => [...subagents.querySelectorAll(`${blocks} [title]`)].map((b) => [b.getAttribute('title'), b.className.match(/text-(error|accent|success|muted)/)?.[1], !!b.querySelector('svg')]);
    expect(cells('.max-sm\\:hidden')).toEqual([
      ['1 failed', 'error', true],
      ['3 running', 'accent', true],
      ['1 done', 'success', true],
    ]);
    // A phone keeps the failed cell and "3/5", running of all, so the progress line keeps its room.
    expect(cells('.sm\\:hidden')).toEqual([['1 failed', 'error', true]]);
    expect(within(subagents).getByText('3/5').classList.contains('sm:hidden')).toBe(true);
    // The subagents open the sheet: no tabs, the list under its head; ← and → do nothing there.
    await user.click(subagents);
    const reader = await screen.findByRole('dialog', { name: 'Subagents' });
    expect(within(reader).queryByRole('tab')).toBeNull();
    expect(within(reader).queryByText('Todo')).toBeNull();
    const search = await within(reader).findByRole('searchbox', { name: 'Search subagents' });
    within(reader).getByRole('button', { name: 'Close' }).focus();
    await user.keyboard('{ArrowRight}');
    await user.keyboard('{ArrowLeft}');
    expect(search.isConnected).toBe(true);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(document.activeElement).toBe(subagents);
    // The list opens its own reader; its open rows wait on others, so they are one list with the rails beside them, "now" at the row
    // the work is at, and Done is its own section (the contrast ⇄ tokens loop is met: tokens is done).
    await user.click(strip);
    const todo = await screen.findByRole('dialog', { name: 'Todo' });
    expect(within(todo).getAllByRole('region').map((r) => r.getAttribute('aria-label'))).toEqual(['Done']);
    expect(within(todo).getAllByRole('listitem')).toHaveLength(8);
    expect(within(todo).queryByText(/loop/)).toBeNull();
    expect(within(todo).getByText('now').parentElement!.textContent).toContain('Check contrast of the theme tokens');
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(document.activeElement).toBe(strip);
  });

  test('opening the subagents leaves the line alone: only the button pressed is expanded and lifted', async () => {
    const { user } = await openTask('t8');
    const line = screen.getByRole('button', { name: /Show the list$/ });
    const subagents = screen.getByRole('button', { name: /^Subagents: .*Show the subagents$/ });
    expect(line.contains(subagents)).toBe(false);
    await user.click(subagents);
    await screen.findByRole('dialog', { name: 'Subagents' });
    expect(subagents.getAttribute('aria-expanded')).toBe('true');
    expect(line.getAttribute('aria-expanded')).toBe('false');
  });

  test('a reply\'s chip waits while the live rows show all its subagents under their own', async () => {
    await openTask('t8');
    const chips = log().getAllByRole('button', { name: /^5 subagents/ });
    expect(chips).toHaveLength(1);
    expect(chips[0].closest('[aria-label="Subagents at work"]')).toBeTruthy();
  });

  test('subagents at work are plain one-line rows in one column: glyph, name and caption, a failed one\'s error as its caption', async () => {
    await openTask('t8');
    const list = screen.getByRole('region', { name: 'Subagents at work' }).querySelector('ul')!;
    expect(list.className).not.toContain('columns-');
    expect(list.className).toContain('max-w-2xl');
    // No card: no head line of its own, no progress bar, no description beside the name.
    const row = live().getByRole('button', { name: 'Run the accessibility linter, failed: axe-core is not installed in this project.' });
    expect(row.textContent).toContain('axe-core is not installed in this project.');
    expect(row.textContent).not.toContain('Run axe on the rendered post page');
    expect(row.querySelector('span.text-error')?.textContent).toBe('axe-core is not installed in this project.');
  });

  test('an earlier reply\'s chip opens the sheet on its list; a row opens its detail, which leads to where it was spawned', async () => {
    const { user } = await openTask('t22');
    const chip = log().getByRole('button', { name: /^12 subagents · [\d.]+M tokens · 11 done · 1 running/ });
    expect(chip.getAttribute('aria-expanded')).toBeNull();
    await user.click(chip);
    const list = await sheet();
    // That reply's rows alone, all of them: the running one first.
    const rows = list.getAllByRole('button', { name: /^Audit package / });
    expect(rows).toHaveLength(12);
    expect(rows[0].getAttribute('aria-label')).toMatch(/^Audit package internal\/store, running/);
    await user.click(list.getByRole('button', { name: /^Audit package internal\/web, completed/ }));
    // Read result first: what went back to the main agent leads the detail.
    const result = await list.findByRole('region', { name: 'Result' });
    // Its last message is that result: drawn once, not again under Work.
    const said = result.querySelector('p')!.textContent!;
    expect(said.length).toBeGreaterThan(10);
    await waitFor(() => expect(list.getByRole('region', { name: 'Work' }).textContent).not.toContain('Loading'));
    expect(list.getByRole('region', { name: 'Work' }).textContent).not.toContain(said);
    expect(list.queryByRole('textbox')).toBeNull();
    await user.click(list.getByRole('button', { name: 'Show where it was spawned' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    // Its reply has no rows of its own on the page: it lands on the chip that holds its call.
    await waitFor(() => expect(flashed()).toBe(chip));
  });

  test('Stop in the detail is confirmed; a failed stop says so on the row, an accepted one waits for the run to end', async () => {
    const { user } = await openTask('t8');
    await user.click(live().getByRole('button', { name: /^Check contrast of the theme tokens, running/ }));
    let open = await sheet();
    expect(await open.findByText(/is 2\.85:1, below AA/)).toBeTruthy();
    const refused = vi.spyOn(api, 'cancelSubagent').mockRejectedValueOnce(new Error('the provider did not answer'));
    await user.click(open.getByRole('button', { name: 'Stop subagent Check contrast of the theme tokens' }));
    const confirm = await screen.findByRole('alertdialog', { name: 'Stop subagent “Check contrast of the theme tokens”?' });
    await user.click(within(confirm).getByRole('button', { name: 'Stop subagent' }));
    await waitFor(() => expect(document.querySelector('[data-subagent-row] [role="alert"]')?.textContent).toBe('Could not stop it: the provider did not answer'));
    refused.mockRestore();
    // The confirmation sat outside the sheet: pressing in it left the sheet open.
    open = await sheet();
    await user.click(open.getByRole('button', { name: 'Stop subagent Check contrast of the theme tokens' }));
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Stop subagent' }));
    // Stopped, it stays open where it was read.
    expect(await open.findByText('Stopped before it finished.')).toBeTruthy();
  });

  test('picking a run never calls it gone while the transcript can still show it: the first run is the start, a fresh running run the end', async () => {
    const { user } = await openTask('t8');
    await user.click(live().getByRole('button', { name: /^Check contrast of the theme tokens, running/ }));
    const open = await sheet();
    const transcript = await open.findByRole('region', { name: 'Transcript of Check contrast of the theme tokens' });
    await open.findByText(/is 2\.85:1, below AA/);
    await user.click(open.getByRole('button', { name: /^Run 2 · / }));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(open.queryByText('That run is not in the retained transcript.')).toBeNull();
    transcript.scrollTop = 40;
    await user.click(open.getByRole('button', { name: /^Run 1 · / }));
    await waitFor(() => expect(transcript.scrollTop).toBe(0));
    expect(open.queryByText('That run is not in the retained transcript.')).toBeNull();
  });

  test('a subagent with several runs shows each in its detail\'s run strip', async () => {
    const { user } = await openTask('t22');
    await user.click(live().getByRole('button', { name: /^Audit package internal\/store, running/ }));
    const open = await sheet();
    const runs = (await open.findAllByRole('button', { name: /^Run \d · / })).map((b) => b.textContent);
    expect(runs).toEqual(['Run 1 · ✓ 2m 0s', 'Run 2 · ✓ 2m 0s', 'Run 3 · running']);
    await user.click(open.getByRole('button', { name: /^Run 2/ }));
    expect(open.queryByText('That run is not in the retained transcript.')).toBeNull();
  });

  test('a subagent another spawned sits under it, opens from its detail, and leads back to it; ↑ ↓ step through the list', async () => {
    const { user } = await openTask('t8');
    const names = live().getAllByRole('button', { name: /, (failed|running|completed)/ }).map((b) => b.getAttribute('aria-label')?.split(',')[0]);
    expect(names.indexOf('Verify store callers')).toBe(names.indexOf('Check contrast of the theme tokens') + 1);
    await user.click(live().getByRole('button', { name: /^Check contrast of the theme tokens, running/ }));
    const open = await sheet();
    await user.click(await open.findByRole('button', { name: /^Verify store callers$/ }));
    expect(await open.findByRole('region', { name: 'Transcript of Verify store callers' })).toBeTruthy();
    expect(open.getByText('3 of 5')).toBeTruthy();
    await user.click(open.getByRole('button', { name: 'Check contrast of the theme tokens' }));
    expect(await open.findByRole('region', { name: 'Transcript of Check contrast of the theme tokens' })).toBeTruthy();
    // The way back holds the focus; ↓ goes to the next row of the list.
    open.getByRole('button', { name: 'All 5' }).focus();
    await user.keyboard('{ArrowDown}');
    expect(await open.findByRole('region', { name: 'Transcript of Verify store callers' })).toBeTruthy();
    await user.click(open.getByRole('button', { name: 'Previous subagent' }));
    expect(await open.findByRole('region', { name: 'Transcript of Check contrast of the theme tokens' })).toBeTruthy();
  });

  // A background launch's call is never read, so its transcript must not wait for that body.
  test('a background subagent\'s transcript does not wait for a call body it never reads', async () => {
    const original = mockData.seed;
    const seed = vi.spyOn(mockData, 'seed').mockImplementation(() => {
      const state = original();
      const task = state.tasks.find((t) => t.id === 't15')!;
      task.subagents = (task.subagents ?? []).map((s) => (s.id === 'a5' ? { ...s, background: true } : s));
      return state;
    });
    try {
      const { user } = await openTask('t15');
      const index = await openSubagentIndex(user);
      await user.click(index.getByRole('button', { name: /^Audit the remaining packages/ }));
      expect(await index.findByRole('region', { name: 'Transcript of Audit the remaining packages' })).toBeTruthy();
      await new Promise((resolve) => setTimeout(resolve, 100));
      expect(index.queryByText('Details load as you scroll here…')).toBeNull();
    } finally {
      seed.mockRestore();
    }
  });

  test('older subagents load from the record on request and open in the sheet; where they were spawned is gone, and it says so', async () => {
    const { user } = await openTask('t15');
    const index = await openSubagentIndex(user);
    await user.click(index.getByRole('button', { name: 'Show older subagents' }));
    // Their calls are not in the history held here: they wait under "Earlier in the conversation".
    const earlier = within(await index.findByRole('region', { name: 'Earlier in the conversation' }));
    await user.click(earlier.getByRole('button', { name: /^Audit package batch 2/ }));
    expect(await index.findByRole('region', { name: 'Transcript of Audit package batch 2' })).toBeTruthy();
    await user.click(index.getByRole('button', { name: 'Show where it was spawned' }));
    // Locating pages through the record first; under coverage instrumentation that outlasts the default wait.
    const said = await screen.findAllByText(/^Where this subagent was spawned is not in the retained history/, {}, { timeout: 15000 });
    const alert = said.map((node) => node.closest<HTMLElement>('div[role="alert"]')).find(Boolean);
    await waitFor(() => expect(document.activeElement).toBe(alert));
  });

  test('the live rows list every subagent at work, running then failed first, with their tokens', async () => {
    await openTask('t22');
    expect(live().getByRole('button', { name: /^8 subagents · [\d.]+M tokens/ })).toBeTruthy();
    const rows = live().getAllByRole('button', { name: /^Audit package .*, (failed|running|completed|stopped)/ }).map((b) => b.getAttribute('aria-label') ?? '');
    expect(rows).toHaveLength(8);
    expect(rows.slice(0, 4).every((name) => /, running/.test(name))).toBe(true);
    expect(rows[4]).toMatch(/^Audit package cmd\/tool15, failed/);
  });

  test('a running subagent\'s row says what it is doing now, not what it was asked', async () => {
    await openTask('t22');
    const row = live().getByRole('button', { name: /^Audit package web\/src\/lib, running: Reading transcript\.ts · step 4/ });
    expect(row.textContent).toContain('Reading transcript.ts · step 4');
    expect(row.textContent).not.toContain('List the exported symbols');
  });

  test('a long reply\'s list takes a search by name or result; no subagent takes a follow-up', async () => {
    const { user } = await openTask('t22');
    await user.click(log().getByRole('button', { name: /^22 subagents · [\d.]+M tokens · 20 done · 2 failed/ }));
    const list = await sheet();
    await user.type(list.getByRole('searchbox', { name: 'Search subagents' }), 'TOOL9');
    const idle = await list.findByRole('button', { name: /^Audit package cmd\/tool9, idle/ });
    expect(list.getAllByRole('button', { name: /^Audit package / })).toHaveLength(1);
    await user.click(idle);
    expect(await list.findByRole('region', { name: 'Transcript of Audit package cmd/tool9' })).toBeTruthy();
    expect(list.queryByRole('textbox')).toBeNull();
  });

  test('an older message shows three rows, then "N more · Show all"; the newest shows all of its own', async () => {
    const { user } = await openTask('t22');
    const index = await openSubagentIndex(user);
    const newest = within(index.getByRole('region', { name: /^“Retry the failed ones/ }));
    expect(newest.queryByRole('button', { name: /more · Show all/ })).toBeNull();
    const older = within(index.getByRole('region', { name: /^“Yes, run the rest/ }));
    expect(older.getAllByRole('button', { name: /^Audit package / })).toHaveLength(3);
    await user.click(older.getByRole('button', { name: '19 more · Show all 22' }));
    expect(older.getAllByRole('button', { name: /^Audit package / })).toHaveLength(22);
  });

  test('at the end of the list the step buttons stay focusable and do nothing, so ↑ ↓ ← keep working; → never leaves the detail', async () => {
    const { user } = await openTask('t8');
    await user.click(live().getByRole('button', { name: /^Run the accessibility linter, failed/ }));
    const open = await sheet();
    expect(open.getByText('4 of 5')).toBeTruthy();
    const next = open.getByRole('button', { name: 'Next subagent' });
    await user.click(next);
    expect(await open.findByRole('region', { name: 'Transcript of Check the heading order' })).toBeTruthy();
    // The last one: Next says it cannot go on, keeps the focus, and a press does nothing.
    expect(next.getAttribute('aria-disabled')).toBe('true');
    expect(next.hasAttribute('disabled')).toBe(false);
    expect(document.activeElement).toBe(next);
    await user.click(next);
    expect(open.getByText('5 of 5')).toBeTruthy();
    // → stays in the detail.
    await user.keyboard('{ArrowRight}');
    expect(open.getByRole('region', { name: 'Transcript of Check the heading order' })).toBeTruthy();
    await user.keyboard('{ArrowUp}');
    expect(await open.findByRole('region', { name: 'Transcript of Run the accessibility linter' })).toBeTruthy();
    await user.keyboard('{ArrowLeft}');
    expect(await open.findByRole('searchbox', { name: 'Search subagents' })).toBeTruthy();
  });

  test('a reply\'s list offers no older subagents (they would join no reply); the whole list does', async () => {
    const { user } = await openTask('t15');
    await user.click(await screen.findByRole('button', { name: /^1 subagent · [\d.]+M tokens · 1 done/ }));
    const list = await sheet();
    expect(await list.findByRole('button', { name: /^From .*show every message's subagents$/ })).toBeTruthy();
    expect(list.queryByRole('button', { name: 'Show older subagents' })).toBeNull();
    await user.click(list.getByRole('button', { name: /^From / }));
    expect(list.getByRole('button', { name: 'Show older subagents' })).toBeTruthy();
  });

  test('with a search and a status filter the first message still shown shows all its rows', async () => {
    const { user } = await openTask('t22');
    const index = await openSubagentIndex(user);
    // Done ones named cmd/tool…: none in the newest message, so the one before it leads, uncapped.
    await user.click(index.getByRole('button', { name: 'Done 33' }));
    await user.type(index.getByRole('searchbox', { name: 'Search subagents' }), 'cmd/tool');
    expect(index.queryByRole('region', { name: /^“Retry the failed ones/ })).toBeNull();
    const first = within(index.getByRole('region', { name: /^“Yes, run the rest/ }));
    expect(first.getAllByRole('button', { name: /^Audit package cmd\/tool/ }).length).toBeGreaterThan(4);
    expect(first.queryByRole('button', { name: /more · Show all/ })).toBeNull();
  });

  test('hovering a row opens nothing: no peek, the sheet opens only on a press', async () => {
    const { user } = await openTask('t8');
    await user.hover(live().getByRole('button', { name: /^Run the accessibility linter, failed/ }));
    await new Promise((resolve) => setTimeout(resolve, 600));
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

// One mark moves in the pane: the strip's live line (r13 A). The header's state, the running step and everything else are still, in words and colour.
describe('one working ring per place', () => {
  const rings = (root: ParentNode) => [...root.querySelectorAll('.animate-spin')];

  test('while subagents run, only the live line\'s mark moves: the header, their rows, the strip\'s subagent blocks and the Changes dot are still', async () => {
    await openTask('t8');
    const pane = document.querySelector('main')!;
    const header = pane.querySelector('header')!;
    // The header: Working as its still fill; the strip says how many run in a still block.
    expect(rings(header)).toHaveLength(0);
    expect(within(header).getByRole('group', { name: 'State: Working' }).className).toContain('bg-accent-wash');
    const strip = screen.getByRole('button', { name: /Subagents: 5, 1 failed, 3 running, 1 done/ });
    expect(strip.querySelector('[title="3 running"]')!.className).toContain('text-accent');
    expect(rings(strip)).toHaveLength(0);
    // The live rows of the running ones: the step dots (no ring), "running" in their names.
    const live = screen.getByRole('region', { name: 'Subagents at work' });
    expect(rings(live)).toHaveLength(0);
    const running = within(live).getAllByRole('button', { name: /, running/ });
    expect(running.length).toBeGreaterThanOrEqual(2);
    expect(running.every((row) => row.querySelector('[data-step-mark]'))).toBe(true);
    // Their `task` calls are no step at the foot in Compact (the live set shows them), so the conversation does not turn.
    expect(rings(screen.getByRole('region', { name: 'Conversation' }))).toHaveLength(0);
    // Changed files keep a steady amber dot (once the changes load), and the status line's dot is still: nothing in the pane pulses.
    await waitFor(() => expect(document.getElementById('changes-link')!.querySelector('.bg-warning:not(.animate-pulse-dot)')).not.toBeNull());
    expect(pane.querySelectorAll('.animate-pulse-dot')).toHaveLength(0);
    // t8 waits on the model: the live line breathes (or, once the wait passes its threshold, holds a still clock), and nothing in the pane turns.
    expect(rings(pane)).toHaveLength(0);
    expect(screen.getByText(/^Waiting for the model, /, { selector: '.sr-only' })).toBeTruthy();
    expect(pane.querySelectorAll('.animate-breathe').length).toBeLessThanOrEqual(1);
  });

  test('a running subagent\'s step turns no ring: its running call carries the step dots, its thought streams unlabelled; its head says "Running"', async () => {
    const { user } = await openTask('t8');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.click(live.getByRole('button', { name: /^Survey templates for missing alt text and labels, running/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Subagents' });
    const transcript = await within(dialog).findByRole('region', { name: 'Transcript of Survey templates for missing alt text and labels' });
    expect(within(dialog).getByText('Running')).toBeTruthy();
    // First its bash call is the step: the step dots, never a ring (the strip's live line has the one ring).
    expect(rings(dialog)).toHaveLength(0);
    expect(transcript.querySelectorAll('[data-step-mark]').length).toBeLessThanOrEqual(1);
    // Then a thought streams (the mock's): the foot has no "Thinking…" label and no step mark.
    await waitFor(() => expect(transcript.querySelector('[data-step-mark]')).toBeNull(), { timeout: 3000 });
    expect(within(transcript).queryByText('Thinking…')).toBeNull();
  });

  test('between steps a subagent\'s foot is blank: no filler verb, no mark', async () => {
    const { user } = await openTask('t8');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.click(live.getByRole('button', { name: /^Verify store callers, running/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Subagents' });
    await within(dialog).findByRole('region', { name: /^Transcript of / });
    expect(within(dialog).queryByText(/…$/, { ignore: '.sr-only, textarea, input' })).toBeNull();
    expect(rings(dialog)).toHaveLength(0);
  });

  test('a running background shell reads "Running" in plain words, with no chip or dot', async () => {
    const { user } = await openTask('t1');
    await user.click(screen.getByRole('button', { name: 'Background tasks: 1 running' }));
    const dialog = await screen.findByRole('dialog', { name: 'Background tasks' });
    expect(within(dialog).getByText('Running').querySelector('.bg-accent')).toBeNull();
    expect(dialog.querySelector('.bg-accent')).toBeNull();
    expect(rings(dialog)).toHaveLength(0);
  });
});

describe('the Task header', () => {
  test('narrow, it drops the button labels, then folds Charts and Terminal into the menu; the title keeps its room', async () => {
    expect(headerRoom(0)).toBe('wide');
    expect(headerRoom(1100)).toBe('wide');
    expect(headerRoom(720)).toBe('snug');
    expect(headerRoom(520)).toBe('tight');
    // The title's own width comes before the button labels: a side panel at 1440 used to cut "Fix re-attach redraw regression" to "Fix …".
    expect(headerRoom(1056, 314)).toBe('wide');
    expect(headerRoom(1000, 400)).toBe('snug');
    expect(headerRoom(616, 238)).toBe('tight');
    // A very long title counts only up to its cap.
    expect(headerRoom(1200, 2000)).toBe('wide');
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
      return this.tagName === 'HEADER' ? 520 : 0;
    });
    try {
      const { user } = renderApp('#task=t21');
      await screen.findByRole('region', { name: 'Conversation' });
      const header = screen.getByRole('heading', { level: 1 }).closest('header')!;
      expect(screen.getByRole('heading', { level: 1 }).parentElement!.className).toContain('min-w-28');
      expect(within(header).queryByRole('button', { name: 'Files' })).toBeNull();
      expect(within(header).getByRole('button', { name: /^Open changes/ }).textContent).not.toContain('Changes');
      // Go to file lives under the composer, so the header neither shows nor folds it.
      const menu = await openMenu(user, 'Thread actions');
      expect(menu.queryByRole('menuitem', { name: /Go to file/ })).toBeNull();
    } finally {
      width.mockRestore();
    }
  });
});
