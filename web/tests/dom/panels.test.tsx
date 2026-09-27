import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { openMenu, openTask } from './render';

describe('changes', () => {
  test('the header count opens the changes sheet; a file shows its diff', async () => {
    const { user } = await openTask('t1');
    await user.click(await screen.findByRole('button', { name: 'Open changes, 3 files' }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    expect(await sheet.findByText(/^3 files/)).toBeTruthy();
    expect(sheet.getByText('All uncommitted project changes vs HEAD.')).toBeTruthy();
    await user.click(sheet.getByTitle('internal/vterm/redraw_test.go'));
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
  test('the header lists subagents by status; one opens to its transcript', async () => {
    const { user } = await openTask('t8');
    await user.click(screen.getByRole('button', { name: /^Subagents, 4, \d running/ }));
    const panel = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    const running = within(panel.getByRole('region', { name: 'Running' }));
    expect(running.getByText('Survey templates for missing alt text and labels')).toBeTruthy();
    const failed = panel.getByRole('region', { name: 'Failed' });
    const toggle = within(failed).getByRole('button', { name: /Failed/ });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    await user.click(toggle);
    await user.click(await within(failed).findByText('Run the accessibility linter'));
    const agent = within(await screen.findByRole('dialog', { name: 'Subagent Run the accessibility linter' }));
    expect(await agent.findByText(/axe-core. is not installed|is not installed and I was told/)).toBeTruthy();
    await user.click(agent.getByRole('button', { name: 'Back to the subagent list' }));
    expect(await screen.findByRole('dialog', { name: 'Subagents' })).toBeTruthy();
  });

  test('a completed subagent shows its result and the row menu locates its call', async () => {
    const { user } = await openTask('t8');
    await user.click(screen.getByRole('button', { name: /^Subagents, 4/ }));
    const panel = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    await user.click(panel.getByRole('button', { name: /Completed/ }));
    const menu = await openMenu(user, 'Actions for subagent Check the heading order');
    await user.click(menu.getByRole('menuitem', { name: 'Open' }));
    const agent = within(await screen.findByRole('dialog', { name: 'Subagent Check the heading order' }));
    expect(await agent.findByText('Result sent to the main agent')).toBeTruthy();
    expect(agent.getByText(/The byline skips a level/)).toBeTruthy();
    await user.click(agent.getByRole('button', { name: 'Close subagents' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Subagent/ })).toBeNull());
    await user.click(screen.getByRole('button', { name: /^Subagents, 4/ }));
    await user.click(within(await screen.findByRole('dialog', { name: 'Subagents' })).getByRole('button', { name: /Completed/ }));
    const again = await openMenu(user, 'Actions for subagent Check the heading order');
    await user.click(again.getByRole('menuitem', { name: 'Show where it was spawned' }));
    await waitFor(() => expect(document.getElementById('item-i6')?.classList.contains('animate-flash')).toBe(true));
  });

  test('stopping a running subagent is confirmed first', async () => {
    const { user } = await openTask('t8');
    await user.click(screen.getByRole('button', { name: /^Subagents, 4/ }));
    const panel = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    await user.click(panel.getByText('Check contrast of the theme tokens'));
    const agent = within(await screen.findByRole('dialog', { name: 'Subagent Check contrast of the theme tokens' }));
    expect(await agent.findByText(/is 2\.85:1, below AA/)).toBeTruthy();
    await user.click(agent.getByRole('button', { name: 'Stop subagent Check contrast of the theme tokens' }));
    const confirm = await screen.findByRole('alertdialog', { name: 'Stop subagent “Check contrast of the theme tokens”?' });
    await user.click(within(confirm).getByRole('button', { name: 'Stop subagent' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
  });

  test('older subagents load from the record on request', async () => {
    const { user } = await openTask('t15');
    await user.click(screen.getByRole('button', { name: 'Subagents, 1 or more' }));
    const panel = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    await user.click(panel.getByRole('button', { name: 'Show older subagents' }));
    await user.click(await panel.findByRole('button', { name: /Completed/ }));
    expect(await panel.findByText('Audit package batch 2')).toBeTruthy();
  });
});
