import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { saveDensity } from '../../src/lib/density';
import { log, openMenu, openTask } from './render';

describe('changes', () => {
  test('the header count opens the changes sheet; a file shows its diff', async () => {
    const { user } = await openTask('t1');
    await user.click(await screen.findByRole('button', { name: 'Open changes, 3 files' }));
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
    await user.click(await screen.findByRole('button', { name: /^Open changes, 2 files/ }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    const menu = await openMenu(user, 'Actions for assets/theme.css');
    await user.click(menu.getByRole('menuitem', { name: 'Open diff' }));
    expect(await sheet.findByText('--muted: #6b6560;')).toBeTruthy();
  });

  test('the transcript\'s changed-files line opens the same sheet', async () => {
    const { user } = await openTask('t1');
    await user.click(screen.getAllByRole('button', { name: 'View changes' })[0]);
    expect(await screen.findByRole('dialog', { name: 'Changes' })).toBeTruthy();
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
  });
});

describe('subagents', () => {
  // The header's index: subagents by the message that started their reply, filters, and a pick that lands on the row.
  test('the header index groups subagents by their message; a pick expands its row in the conversation', async () => {
    const { user } = await openTask('t8');
    await user.click(screen.getByRole('button', { name: /^Subagents, 4, \d running/ }));
    const index = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    const group = within(index.getByRole('region', { name: /^“Audit templates\/post\.html for accessibility problems/ }));
    expect(group.getByRole('button', { name: /^Jump to the subagents of/ })).toBeTruthy();
    expect(index.queryByText(/Turn \d/)).toBeNull();
    await user.click(index.getByRole('button', { name: 'Failed 1' }));
    expect(index.queryByRole('button', { name: /^Check the heading order/ })).toBeNull();
    await user.click(index.getByRole('button', { name: /^Run the accessibility linter/ }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Subagents' })).toBeNull());
    // Its reply's list opened on the way and the row expanded onto its transcript, with focus on the row.
    const region = within(await screen.findByRole('region', { name: 'Subagent Run the accessibility linter' }));
    expect(await region.findByText(/axe-core. is not installed|is not installed and I was told/)).toBeTruthy();
    await waitFor(() => expect(document.activeElement?.getAttribute('aria-expanded')).toBe('true'));
    expect(document.activeElement?.closest('#item-i5')).toBeTruthy();
  });

  test('a reply\'s chip waits while the live card shows all its subagents', async () => {
    await openTask('t8');
    expect(screen.getByRole('region', { name: 'Subagents at work' })).toBeTruthy();
    expect(log().queryByRole('button', { name: /^4 subagents/ })).toBeNull();
  });

  test('an earlier reply\'s chip opens its list; a row folds on Esc and locates its call', async () => {
    const { user } = await openTask('t22');
    const chip = log().getByRole('button', { name: /^12 subagents · 11 done · 1 running/ });
    expect(chip.getAttribute('aria-expanded')).toBe('false');
    await user.click(chip);
    const list = within(await log().findByRole('region', { name: '12 subagents' }));
    await user.click(list.getByRole('button', { name: /^Done/ }));
    const row = list.getByRole('button', { name: /^Audit package internal\/web, completed: internal\/web: 2 exports without callers/ });
    await user.click(row);
    expect(row.getAttribute('aria-expanded')).toBe('true');
    const region = within(await screen.findByRole('region', { name: 'Subagent Audit package internal/web' }));
    expect(await region.findByText('Result sent to the main agent')).toBeTruthy();
    expect(region.queryByRole('form', { name: /^Follow up with subagent/ })).toBeNull();
    fireEvent.keyDown(region.getByRole('region', { name: 'Transcript of Audit package internal/web' }), { key: 'Escape' });
    await waitFor(() => expect(row.getAttribute('aria-expanded')).toBe('false'));
    expect(document.activeElement).toBe(row);
    const menu = await openMenu(user, 'Actions for subagent Audit package internal/web');
    await user.click(menu.getByRole('menuitem', { name: 'Show where it was spawned' }));
    await waitFor(() => expect(document.getElementById('item-sc2')?.classList.contains('animate-flash')).toBe(true));
  });

  test('a subagent that ran again after its reply says when, and leads to the reply that run started in', async () => {
    const { user } = await openTask('t22');
    await user.click(log().getByRole('button', { name: /^12 subagents/ }));
    const list = within(await log().findByRole('region', { name: '12 subagents' }));
    expect(list.getByText(/3 runs/)).toBeTruthy();
    await user.click(list.getByRole('button', { name: /^ran again [^·]+ ↓$/ }));
    // The third reply ("Retry the failed ones and do web/ too.") started that run: its turn line flashes.
    await waitFor(() => expect(document.querySelector('[data-reply="d6"] .animate-flash')).toBeTruthy());
    expect(document.body.textContent).not.toMatch(/turn \d/i);
  });

  test('a row open in the live card stays while its subagent stops; Stop is in its menu, and a failed stop says so on the row', async () => {
    const { user } = await openTask('t8');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.click(live.getByRole('button', { name: /^Check contrast of the theme tokens, running/ }));
    expect(await screen.findByRole('region', { name: 'Subagent Check contrast of the theme tokens' })).toBeTruthy();
    const refused = vi.spyOn(api, 'cancelSubagent').mockRejectedValueOnce(new Error('the provider did not answer'));
    let menu = await openMenu(user, 'Actions for subagent Check contrast of the theme tokens');
    await user.click(menu.getByRole('menuitem', { name: 'Stop subagent' }));
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Stop subagent' }));
    expect((await live.findByRole('alert')).textContent).toBe('Could not stop it: the provider did not answer');
    refused.mockRestore();
    menu = await openMenu(user, 'Actions for subagent Check contrast of the theme tokens');
    await user.click(menu.getByRole('menuitem', { name: 'Stop subagent' }));
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Stop subagent' }));
    // Stopped, it is no longer one the card lists, yet the open row stays where it is read.
    expect(await live.findByRole('button', { name: /^Check contrast of the theme tokens, stopped/ })).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Subagent Check contrast of the theme tokens' })).toBeTruthy();
    // Folded, it goes back behind the card's counts.
    await user.click(live.getByRole('button', { name: /^Check contrast of the theme tokens, stopped/ }));
    await waitFor(() => expect(live.queryByRole('button', { name: /^Check contrast of the theme tokens/ })).toBeNull());
  });

  test('Detailed: a long reply\'s activity holds its grouped list instead of a row per subagent', async () => {
    saveDensity('detailed');
    try {
      const { user } = await openTask('t22');
      const run = log().getByRole('button', { name: /^22 subagents/ });
      await user.click(run);
      const list = within(await log().findByRole('region', { name: '22 subagents' }));
      expect(list.getByRole('button', { name: /^Done/ }).getAttribute('aria-expanded')).toBe('false');
      // Only the failed two are drawn until Done opens.
      expect(list.getAllByRole('button', { name: /^Audit package cmd\/tool\d+, / })).toHaveLength(2);
    } finally {
      saveDensity('compact');
    }
  });

  test('a subagent with several runs lists them in its row', async () => {
    const { user } = await openTask('t22');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.click(live.getByRole('button', { name: /^Audit package internal\/store, running/ }));
    const region = within(await screen.findByRole('region', { name: 'Subagent Audit package internal/store' }));
    const runs = within(region.getByRole('list', { name: 'Runs' })).getAllByRole('button').map((b) => b.textContent);
    expect(runs).toHaveLength(3);
    expect(runs[0]).toMatch(/^Run 1 · [^·]+ · started by the agent · ✓ 2m 0s$/);
    expect(runs[1]).toMatch(/^Run 2 · [^·]+ · your follow-up · ✓ 2m 0s$/);
    expect(runs[2]).toMatch(/^Run 3 · [^·]+ · resumed by the agent · running$/);
    await user.click(region.getByRole('button', { name: /^Run 2/ }));
    expect(region.queryByText('That run is not in the retained transcript.')).toBeNull();
  });

  test('stopping a running subagent is confirmed first', async () => {
    const { user } = await openTask('t8');
    const live = within(screen.getByRole('region', { name: 'Subagents at work' }));
    await user.click(live.getByRole('button', { name: /^Check contrast of the theme tokens, running/ }));
    const agent = within(await screen.findByRole('region', { name: 'Subagent Check contrast of the theme tokens' }));
    expect(await agent.findByText(/is 2\.85:1, below AA/)).toBeTruthy();
    // Running, it takes no follow-up.
    expect(agent.queryByRole('form', { name: /^Follow up with subagent/ })).toBeNull();
    await user.click(agent.getByRole('button', { name: 'Stop subagent Check contrast of the theme tokens' }));
    const confirm = await screen.findByRole('alertdialog', { name: 'Stop subagent “Check contrast of the theme tokens”?' });
    await user.click(within(confirm).getByRole('button', { name: 'Stop subagent' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
  });

  test('older subagents load from the record on request', async () => {
    const { user } = await openTask('t15');
    await user.click(screen.getByRole('button', { name: 'Subagents, 1 or more' }));
    const index = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    await user.click(index.getByRole('button', { name: 'Show older subagents' }));
    expect(await index.findByRole('button', { name: /^Audit package batch 2/ })).toBeTruthy();
    // Their calls are not in the history held here: they wait under "Earlier in the conversation", and open right there.
    const earlier = within(index.getByRole('region', { name: 'Earlier in the conversation' }));
    await user.click(earlier.getByRole('button', { name: /^Audit package batch 2, completed/ }));
    expect(await earlier.findByRole('region', { name: 'Subagent Audit package batch 2' })).toBeTruthy();
    // Where it was spawned is gone: said above the composer, and focused.
    const menu = await openMenu(user, 'Actions for subagent Audit package batch 2');
    await user.click(menu.getByRole('menuitem', { name: 'Show where it was spawned' }));
    const said = await screen.findAllByText(/^Where this subagent was spawned is not in the retained history/);
    const alert = said.map((node) => node.closest<HTMLElement>('div[role="alert"]')).find(Boolean);
    await waitFor(() => expect(document.activeElement).toBe(alert));
  });

  test('the live card lists failed then running subagents, five at most, and a resumed one leads to its first reply', async () => {
    const { user } = await openTask('t22');
    const card = screen.getByRole('region', { name: 'Subagents at work' });
    const live = within(card);
    expect(live.getByText('8 subagents')).toBeTruthy();
    const rows = () => live.getAllByRole('button', { name: /^Audit package .*, (failed|running|completed|stopped)/ }).map((b) => b.getAttribute('aria-label') ?? '');
    expect(rows()).toHaveLength(5);
    expect(rows()[0]).toMatch(/^Audit package cmd\/tool15, failed/);
    expect(rows().slice(1).every((name) => /, running/.test(name))).toBe(true);
    await user.click(live.getByRole('button', { name: /2 done · 1 stopped/ }));
    expect(rows()).toHaveLength(8);
    // The first pilot audit runs again: its tag says so and finds its row in the first reply's list, opening it.
    const firstChip = log().getByRole('button', { name: /^12 subagents/ });
    expect(firstChip.getAttribute('aria-expanded')).toBe('false');
    await user.click(live.getByRole('button', { name: /^resumed by the agent · first ran [^·]+ ↑$/ }));
    await waitFor(() => expect(document.getElementById('item-sc1')?.classList.contains('animate-flash')).toBe(true));
    expect(firstChip.getAttribute('aria-expanded')).toBe('true');
  });

  test('a long reply groups its list by status with a filter; an idle subagent takes a follow-up', async () => {
    const { user } = await openTask('t22');
    await user.click(log().getByRole('button', { name: /^22 subagents · 20 done · 2 failed/ }));
    const list = within(await log().findByRole('region', { name: '22 subagents' }));
    expect(list.getByRole('button', { name: /^Failed/ }).getAttribute('aria-expanded')).toBe('true');
    const done = list.getByRole('button', { name: /^Done/ });
    expect(done.getAttribute('aria-expanded')).toBe('false');
    expect(list.queryByRole('button', { name: /^Audit package cmd\/tool9,/ })).toBeNull();
    await user.type(list.getByRole('searchbox', { name: 'Filter 22 subagents by name or result' }), 'TOOL9');
    const idle = await list.findByRole('button', { name: /^Audit package cmd\/tool9, idle/ });
    expect(list.getAllByRole('button', { name: /^Audit package / })).toHaveLength(1);
    await user.click(idle);
    const region = within(await screen.findByRole('region', { name: 'Subagent Audit package cmd/tool9' }));
    expect(region.getByRole('form', { name: 'Follow up with subagent Audit package cmd/tool9' })).toBeTruthy();
  });
});
