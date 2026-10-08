import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { headerRoom } from '../../src/components/Task';
import { saveDensity } from '../../src/lib/density';
import { VERBS } from '../../src/lib/verbs';
import { log, openMenu, openTask, renderApp } from './render';

describe('changes', () => {
  test('the header count opens the changes sheet; a file shows its diff', async () => {
    const { user } = await openTask('t1');
    await user.click(await screen.findByRole('button', { name: /^Open changes.*3 files$/ }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    expect(await sheet.findByText(/^3 files/)).toBeTruthy();
    expect(sheet.getByText(/^Files this task's agent edited/)).toBeTruthy();
    expect(sheet.getByText('CI workflow')).toBeTruthy();
    await user.click(sheet.getByRole('radio', { name: 'All changes · 4' }));
    expect(await sheet.findByText(/^All uncommitted project changes vs HEAD/)).toBeTruthy();
    await user.click(await sheet.findByTitle('internal/vterm/redraw_test.go'));
    expect(await sheet.findByText(/func TestRedrawReplaysFocusEvents/)).toBeTruthy();
    expect(sheet.getByTitle('internal/vterm/redraw_test.go').getAttribute('aria-pressed')).toBe('true');
    await user.click(sheet.getByRole('button', { name: 'Refresh' }));
    expect(await sheet.findByText(/func TestRedrawReplaysFocusEvents/)).toBeTruthy();
    await user.click(sheet.getByRole('button', { name: 'Close changes' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Changes' })).toBeNull());
    await waitFor(() => expect(document.activeElement?.id).toBe('changes-link'));
  });

  test('a file row has actions for its diff and its path', async () => {
    const { user } = await openTask('t8');
    await user.click(await screen.findByRole('button', { name: /^Open changes.*2 files$/ }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    const menu = await openMenu(user, 'Actions for assets/theme.css');
    await user.click(menu.getByRole('menuitem', { name: 'Open diff' }));
    expect(await sheet.findByText('--muted: #6b6560;')).toBeTruthy();
  });

  test('the transcript\'s changed-files line opens the same sheet on that turn, and focus comes back to it', async () => {
    const { user } = await openTask('t1');
    const links = screen.getAllByRole('button', { name: 'View changes' });
    const latest = links.at(-1)!;
    await user.click(latest);
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    const lastTurn = sheet.getByRole('radio', { name: /^Last turn/ });
    expect(lastTurn.getAttribute('aria-checked') ?? String((lastTurn as HTMLInputElement).checked)).toBe('true');
    await user.click(sheet.getByRole('button', { name: 'Close changes' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Changes' })).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(latest));
  });

  test('a Viewed mark made while comments send survives the send', async () => {
    localStorage.setItem('uam.review.t1', JSON.stringify({ viewed: {}, comments: [{ id: 'c1', path: '.github/workflows/ci.yml', line: 1, side: 'new', code: 'x', body: 'Please fix' }] }));
    const { user } = await openTask('t1');
    await user.click(await screen.findByRole('button', { name: /^Open changes/ }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    const send = await sheet.findByRole('button', { name: 'Send 1 comment to the task' });
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

  test('a project without Git offers no changes, and says why', async () => {
    const { user } = await openTask('t6');
    expect(screen.queryByRole('button', { name: /^Open changes/ })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Not a Git repository' }));
    expect(await screen.findByText(/is not in a Git repository/)).toBeTruthy();
  });
});

describe('files', () => {
  test('the files sheet lists folders first, opens a folder and shows a file', async () => {
    const { user } = await openTask('t3');
    await user.click(screen.getByRole('button', { name: 'Browse files' }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Files' }));
    const tree = within(await sheet.findByRole('list', { name: 'Project files' }));
    const cmd = await tree.findByRole('button', { name: 'cmd' });
    expect(cmd.getAttribute('aria-expanded')).toBe('false');
    await user.click(cmd);
    await waitFor(() => expect(cmd.getAttribute('aria-expanded')).toBe('true'));
    await user.click(await tree.findByRole('button', { name: 'doctor.go' }));
    const dialog = screen.getByRole('dialog', { name: 'Files' });
    await waitFor(() => expect(dialog.textContent).toContain('const line1 = "cmd/doctor.go";'));
    expect(sheet.getByRole('link', { name: 'Download' }).getAttribute('download')).toBe('doctor.go');
    await user.click(sheet.getByRole('button', { name: 'Refresh files' }));
    expect(await tree.findByRole('button', { name: 'doctor.go' })).toBeTruthy();
    await user.click(tree.getByRole('button', { name: 'cmd' }));
    await waitFor(() => expect(tree.queryByRole('button', { name: 'doctor.go' })).toBeNull());
    await user.click(sheet.getByRole('button', { name: 'Close files' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Files' })).toBeNull());
    await waitFor(() => expect(document.activeElement?.id).toBe('files-link'));
  });

  test('Esc closes the open file before the panel; an HTML page shows its source', async () => {
    const { user } = await openTask('t8');
    await user.click(screen.getByRole('button', { name: 'Browse files' }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Files' }));
    const tree = within(await sheet.findByRole('list', { name: 'Project files' }));
    await user.click(await tree.findByRole('button', { name: /^templates/ }));
    const html = await tree.findByRole('button', { name: /^post\.html/ });
    await user.click(html);
    await waitFor(() => expect(screen.getByRole('dialog', { name: 'Files' }).textContent).toContain('<!doctype html>'));
    expect(sheet.queryByText(/This file is not shown here/)).toBeNull();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(sheet.queryByText(/<!doctype html>/)).toBeNull());
    expect(screen.getByRole('dialog', { name: 'Files' })).toBeTruthy();
    expect(document.activeElement).toBe(html);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Files' })).toBeNull());
  });
});

describe('subagents', () => {
  const panel = async () => within(await screen.findByRole('dialog', { name: 'Subagent transcript' }));

  // The header's index: subagents by the message that started their reply, filters, and a pick that lands on the row.
  test('the header index groups subagents by their message; a pick lands on its row and opens its transcript', async () => {
    const { user } = await openTask('t8');
    await user.click(screen.getByRole('button', { name: /^Subagents, 5, \d running/ }));
    const index = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    const group = within(index.getByRole('region', { name: /^“Audit templates\/post\.html for accessibility problems/ }));
    expect(group.getByRole('button', { name: /^Jump to the subagents of/ })).toBeTruthy();
    expect(index.queryByText(/Turn \d/)).toBeNull();
    await user.click(index.getByRole('button', { name: 'Failed 1' }));
    expect(index.queryByRole('button', { name: /^Check the heading order/ })).toBeNull();
    await user.click(index.getByRole('button', { name: /^Run the accessibility linter/ }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Subagents' })).toBeNull());
    // It lands on its row and its transcript opens beside it, focus inside.
    const open = await panel();
    expect(await open.findByRole('region', { name: 'Transcript of Run the accessibility linter' })).toBeTruthy();
    await waitFor(() => expect(document.activeElement?.closest('[role="dialog"]')).toBeTruthy());
  });

  test('a reply\'s chip waits while the live set shows all its subagents', async () => {
    await openTask('t8');
    expect(screen.getByRole('region', { name: 'Subagents at work' })).toBeTruthy();
    expect(log().queryByRole('button', { name: /^5 subagents/ })).toBeNull();
  });

  test('a few subagents read as one column, each row naming what it was asked to do beside its name', async () => {
    await openTask('t8');
    const live = screen.getByRole('region', { name: 'Subagents at work' });
    // Four families: one column, not spread into columns across a wide conversation.
    const list = live.querySelector('ul')!;
    expect(list.className).not.toContain('columns-');
    expect(list.className).toContain('max-w-2xl');
    // The task call's description sits beside the name, and the row's name carries it too.
    const row = within(live).getByRole('button', { name: /^Run the accessibility linter, failed: axe-core is not installed in this project\. · Run axe on the rendered post page/ });
    expect(row.textContent).toContain('Run axe on the rendered post page and report violations.');
  });

  test('an earlier reply\'s chip opens its rows; a row opens its transcript, which leads to where it was spawned', async () => {
    const { user } = await openTask('t22');
    const chip = log().getByRole('button', { name: /^12 subagents · [\d.]+M tokens · 11 done · 1 running/ });
    expect(chip.getAttribute('aria-expanded')).toBe('false');
    await user.click(chip);
    const list = within(await log().findByRole('region', { name: '12 subagents' }));
    // One line each, no groups to open: the running one first.
    const rows = list.getAllByRole('button', { name: /^Audit package / });
    expect(rows).toHaveLength(12);
    expect(rows[0].getAttribute('aria-label')).toMatch(/^Audit package internal\/store, running/);
    await user.click(list.getByRole('button', { name: /^Audit package internal\/web, completed/ }));
    const open = await panel();
    // Read result first: what went back to the main agent leads the panel.
    const result = await open.findByRole('region', { name: 'Result' });
    // Its last message is that result: drawn once, not again under Work.
    const said = result.querySelector('p')!.textContent!;
    expect(said.length).toBeGreaterThan(10);
    await waitFor(() => expect(open.getByRole('region', { name: 'Work' }).textContent).not.toContain('Loading'));
    expect(open.getByRole('region', { name: 'Work' }).textContent).not.toContain(said);
    expect(open.queryByRole('textbox')).toBeNull();
    await user.click(open.getByRole('button', { name: 'Show where it was spawned' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Subagent transcript' })).toBeNull());
    await waitFor(() => expect(document.getElementById('item-sc2')?.classList.contains('animate-flash')).toBe(true));
  });

  test('Stop in the transcript is confirmed; a failed stop says so on the row, an accepted one waits for the run to end', async () => {
    const { user } = await openTask('t8');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.click(live.getByRole('button', { name: /^Check contrast of the theme tokens, running/ }));
    let open = await panel();
    expect(await open.findByText(/is 2\.85:1, below AA/)).toBeTruthy();
    const refused = vi.spyOn(api, 'cancelSubagent').mockRejectedValueOnce(new Error('the provider did not answer'));
    await user.click(open.getByRole('button', { name: 'Stop subagent Check contrast of the theme tokens' }));
    const confirm = await screen.findByRole('alertdialog', { name: 'Stop subagent “Check contrast of the theme tokens”?' });
    await user.click(within(confirm).getByRole('button', { name: 'Stop subagent' }));
    await waitFor(() => expect(document.querySelector('[data-subagent-row] [role="alert"]')?.textContent).toBe('Could not stop it: the provider did not answer'));
    refused.mockRestore();
    open = await panel();
    await user.click(open.getByRole('button', { name: 'Stop subagent Check contrast of the theme tokens' }));
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Stop subagent' }));
    // Stopped, it stays open where it was read.
    expect(await open.findByText('Stopped before it finished.')).toBeTruthy();
  });

  test('picking a run never calls it gone while the transcript can still show it: the first run is the start, a fresh running run the end', async () => {
    const { user } = await openTask('t8');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.click(live.getByRole('button', { name: /^Check contrast of the theme tokens, running/ }));
    const open = await panel();
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

  test('a subagent with several runs shows each in its transcript\'s run strip', async () => {
    const { user } = await openTask('t22');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.click(live.getByRole('button', { name: /^Audit package internal\/store, running/ }));
    const open = await panel();
    const runs = open.getAllByRole('button', { name: /^Run \d · / }).map((b) => b.textContent);
    expect(runs).toEqual(['Run 1 · ✓ 2m 0s', 'Run 2 · ✓ 2m 0s', 'Run 3 · running']);
    await user.click(open.getByRole('button', { name: /^Run 2/ }));
    expect(open.queryByText('That run is not in the retained transcript.')).toBeNull();
  });

  test('a subagent another spawned sits under it, and opens from its transcript with the way back', async () => {
    const { user } = await openTask('t8');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    const names = live.getAllByRole('button', { name: /, (failed|running|completed)/ }).map((b) => b.getAttribute('aria-label')?.split(',')[0]);
    expect(names.indexOf('Verify store callers')).toBe(names.indexOf('Check contrast of the theme tokens') + 1);
    await user.click(live.getByRole('button', { name: /^Check contrast of the theme tokens, running/ }));
    let open = await panel();
    await user.click(open.getByRole('button', { name: /^Verify store callers$/ }));
    open = await panel();
    expect(await open.findByRole('region', { name: 'Transcript of Verify store callers' })).toBeTruthy();
    await user.click(open.getByRole('button', { name: 'Check contrast of the theme tokens' }));
    expect(await open.findByRole('region', { name: 'Transcript of Check contrast of the theme tokens' })).toBeTruthy();
  });

  test('Detailed: a long reply\'s activity holds its list instead of a row per subagent', async () => {
    saveDensity('detailed');
    try {
      const { user } = await openTask('t22');
      await user.click(log().getByRole('button', { name: /^22 subagents/ }));
      const list = within(await log().findByRole('region', { name: '22 subagents' }));
      const rows = list.getAllByRole('button', { name: /^Audit package / });
      expect(rows).toHaveLength(22);
      expect(rows.slice(0, 2).every((row) => /, failed/.test(row.getAttribute('aria-label') ?? ''))).toBe(true);
    } finally {
      saveDensity('compact');
    }
  });

  test('older subagents load from the record on request and open by the header; where they were spawned is gone, and it says so', async () => {
    const { user } = await openTask('t15');
    await user.click(screen.getByRole('button', { name: 'Subagents, 1 or more' }));
    const index = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    await user.click(index.getByRole('button', { name: 'Show older subagents' }));
    // Their calls are not in the history held here: they wait under "Earlier in the conversation".
    const earlier = within(await index.findByRole('region', { name: 'Earlier in the conversation' }));
    await user.click(earlier.getByRole('button', { name: /^Audit package batch 2/ }));
    const open = await panel();
    expect(await open.findByRole('region', { name: 'Transcript of Audit package batch 2' })).toBeTruthy();
    await user.click(open.getByRole('button', { name: 'Show where it was spawned' }));
    // Locating pages through the record first; under coverage instrumentation that outlasts the default wait.
    const said = await screen.findAllByText(/^Where this subagent was spawned is not in the retained history/, {}, { timeout: 15000 });
    const alert = said.map((node) => node.closest<HTMLElement>('div[role="alert"]')).find(Boolean);
    await waitFor(() => expect(document.activeElement).toBe(alert));
  });

  test('the live set lists every subagent at work, failed then running first, with their tokens', async () => {
    await openTask('t22');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    expect(live.getByText(/^8 subagents · [\d.]+M tokens$/)).toBeTruthy();
    const rows = live.getAllByRole('button', { name: /^Audit package .*, (failed|running|completed|stopped)/ }).map((b) => b.getAttribute('aria-label') ?? '');
    expect(rows).toHaveLength(8);
    expect(rows[0]).toMatch(/^Audit package cmd\/tool15, failed/);
    expect(rows.slice(1, 5).every((name) => /, running/.test(name))).toBe(true);
  });

  test('a long reply\'s list takes a filter by name or result; no subagent takes a follow-up', async () => {
    const { user } = await openTask('t22');
    await user.click(log().getByRole('button', { name: /^22 subagents · [\d.]+M tokens · 20 done · 2 failed/ }));
    const list = within(await log().findByRole('region', { name: '22 subagents' }));
    await user.type(list.getByRole('searchbox', { name: 'Filter 22 subagents by name or result' }), 'TOOL9');
    const idle = await list.findByRole('button', { name: /^Audit package cmd\/tool9, idle/ });
    expect(list.getAllByRole('button', { name: /^Audit package / })).toHaveLength(1);
    await user.click(idle);
    const open = await panel();
    expect(await open.findByRole('region', { name: 'Transcript of Audit package cmd/tool9' })).toBeTruthy();
    expect(open.queryByRole('textbox')).toBeNull();
  });

  test('hovering a row peeks at it; Full transcript opens it', async () => {
    const { user } = await openTask('t8');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.hover(live.getByRole('button', { name: /^Run the accessibility linter, failed/ }));
    const peek = within(await screen.findByRole('dialog', { name: 'Subagent' }, { timeout: 3000 }));
    expect(peek.getByText('Failed')).toBeTruthy();
    expect(peek.getByText('41.8K tokens')).toBeTruthy();
    expect(peek.getByText('3 tool calls')).toBeTruthy();
    await user.click(peek.getByRole('button', { name: 'Full transcript' }));
    expect(await (await panel()).findByRole('region', { name: 'Transcript of Run the accessibility linter' })).toBeTruthy();
  });
});

// The ring turns only at the Task's state glyph and the step in progress; whatever else runs is still, in words and colour.
describe('one working ring per place', () => {
  const rings = (root: ParentNode) => [...root.querySelectorAll('.animate-spin')];

  test('while subagents run, only the state glyph turns: their rows, the Subagents button and the Changes dot are still', async () => {
    await openTask('t8');
    const pane = document.querySelector('main')!;
    const header = pane.querySelector('header')!;
    // The header: the state chip turns; the Subagents button says how many run, still.
    expect(rings(header).map((ring) => ring.parentElement?.parentElement?.textContent)).toEqual(['Working']);
    const index = screen.getByRole('button', { name: /^Subagents, 5, 3 running/ });
    expect(index.textContent).toContain('3 running');
    // The live set's running rows: a still `accent` dot, "running" in their names.
    const live = screen.getByRole('region', { name: 'Subagents at work' });
    expect(rings(live)).toHaveLength(0);
    const running = within(live).getAllByRole('button', { name: /, running/ });
    expect(running.length).toBeGreaterThanOrEqual(2);
    expect(running.every((row) => row.querySelector('.bg-accent'))).toBe(true);
    // Their `task` calls are no step at the foot in Compact (the live set shows them), so the conversation does not turn.
    expect(rings(screen.getByRole('region', { name: 'Conversation' }))).toHaveLength(0);
    // Changed files keep a steady amber dot, and the status line's dot is still: nothing in the pane pulses.
    expect(document.getElementById('changes-link')!.querySelector('.bg-warning:not(.animate-pulse-dot)')).not.toBeNull();
    expect(pane.querySelectorAll('.animate-pulse-dot')).toHaveLength(0);
    expect(rings(pane)).toHaveLength(1);
  });

  test('a running subagent\'s transcript turns only at its running call; its head says "Running", still', async () => {
    const { user } = await openTask('t8');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.click(live.getByRole('button', { name: /^Survey templates for missing alt text and labels, running/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Subagent transcript' });
    const transcript = await within(dialog).findByRole('region', { name: 'Transcript of Survey templates for missing alt text and labels' });
    expect(within(dialog).getByText('Running')).toBeTruthy();
    // Its bash call is the step: the panel's one ring.
    await waitFor(() => expect(rings(transcript)).toHaveLength(1));
    expect(rings(dialog)).toHaveLength(1);
  });

  test('between steps a subagent\'s foot is its gerund, words only', async () => {
    const { user } = await openTask('t8');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.click(live.getByRole('button', { name: /^Verify store callers, running/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Subagent transcript' });
    await within(dialog).findByText(new RegExp(`^(${VERBS.join('|')})…$`));
    expect(rings(dialog)).toHaveLength(0);
  });

  test('Detailed: an open run hands its ring to the running call inside it', async () => {
    saveDensity('detailed');
    try {
      const { user } = await openTask('t1');
      const conversation = screen.getByRole('region', { name: 'Conversation' });
      const run = rings(conversation)[0]?.closest<HTMLElement>('[data-activity] > button');
      expect(rings(conversation)).toHaveLength(1);
      expect(run).toBeTruthy();
      await user.click(run!);
      // Open, the run's chevron stands still in `accent` and the closed run of calls inside turns.
      expect(rings(run!)).toHaveLength(0);
      expect(run!.querySelector('.text-accent')).not.toBeNull();
      const calls = await waitFor(() => {
        const found = rings(conversation)[0]?.closest<HTMLElement>('[data-tool-run] > button');
        expect(found).toBeTruthy();
        return found!;
      });
      expect(rings(conversation)).toHaveLength(1);
      await user.click(calls);
      // Open too, the running call's own row is the step.
      await waitFor(() => expect(rings(calls)).toHaveLength(0));
      expect(rings(conversation)).toHaveLength(1);
      expect(rings(conversation)[0].closest('[data-tool-run] > button')).toBeNull();
    } finally {
      saveDensity('compact');
    }
  });

  test('a running background shell reads "Running" beside a still dot', async () => {
    const { user } = await openTask('t1');
    await user.click(screen.getByRole('button', { name: 'Background tasks: 1 running' }));
    const dialog = await screen.findByRole('dialog', { name: 'Background tasks' });
    expect(within(dialog).getByText('Running').querySelector('.bg-accent')).not.toBeNull();
    expect(rings(dialog)).toHaveLength(0);
  });
});

describe('the Task header', () => {
  test('narrow, it drops the button labels, then folds Files and Terminal into the menu; the title keeps its room', async () => {
    expect(headerRoom(0)).toBe('wide');
    expect(headerRoom(1100)).toBe('wide');
    expect(headerRoom(720)).toBe('snug');
    expect(headerRoom(520)).toBe('tight');
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
      return this.tagName === 'HEADER' ? 520 : 0;
    });
    try {
      const { user } = renderApp('#task=t21');
      await screen.findByRole('region', { name: 'Conversation' });
      const header = screen.getByRole('heading', { level: 1 }).closest('header')!;
      expect(screen.getByRole('heading', { level: 1 }).parentElement!.className).toContain('min-w-28');
      expect(within(header).queryByRole('button', { name: 'Browse files' })).toBeNull();
      expect(within(header).getByRole('button', { name: /^Open changes/ }).textContent).not.toContain('Changes');
      const menu = await openMenu(user, 'Task actions');
      expect(menu.getByRole('menuitem', { name: 'Browse files' })).toBeTruthy();
    } finally {
      width.mockRestore();
    }
  });
});
