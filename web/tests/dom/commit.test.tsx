import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { defaultSelection } from '../../src/components/CommitPanel';
import { composer, openTask, renderApp } from './render';

/** Renders the app on a Task with the mock's Tasks counted as idle for git (`?gitidle`). */
async function openIdle(id: string) {
  const rendered = renderApp(`?gitidle#task=${id}`);
  await screen.findByRole('region', { name: 'Conversation' });
  await waitFor(() => expect(composer()?.disabled).toBe(false));
  return rendered;
}

async function commitPanel(user: ReturnType<typeof renderApp>['user']) {
  await user.click(await screen.findByRole('button', { name: /^Open changes/ }));
  const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
  return within(await sheet.findByRole('region', { name: 'Commit' }));
}

describe('commit panel', () => {
  test('drafts a message, commits the chosen files and leaves other tasks\' files out', async () => {
    const { user } = await openIdle('t3');
    const panel = await commitPanel(user);
    await user.click(panel.getByRole('button', { name: /^Commit/ }));
    const own = await panel.findByRole('checkbox', { name: /docs\/terminal\.md/ });
    expect((own as HTMLInputElement).checked).toBe(true);
    expect((panel.getByRole('checkbox', { name: /internal\/vterm\/redraw\.go/ }) as HTMLInputElement).checked).toBe(false);
    expect(panel.getByText('4 files from other tasks are not included')).toBeTruthy();
    expect(panel.getByText('No task is running in this repository')).toBeTruthy();

    await user.click(panel.getByRole('button', { name: 'Generate' }));
    const message = panel.getByRole('textbox', { name: 'Commit message' }) as HTMLTextAreaElement;
    await waitFor(() => expect(message.value).toMatch(/^fix\(vterm\): replay focus events on re-attach\n/));
    expect(panel.getByText('Generated from this task’s changes')).toBeTruthy();
    expect(panel.getByText(/Conventional Commits, from its last 20 commits/)).toBeTruthy();
    expect(panel.getByText('44/72 characters in the subject', { exact: false })).toBeTruthy();
    expect(panel.getByRole('button', { name: 'Regenerate' })).toBeTruthy();

    // An edit drops the "generated" label; the counter follows the subject.
    await user.clear(message);
    await user.type(message, 'docs: describe the terminal line');
    expect(panel.queryByText('Generated from this task’s changes')).toBeNull();
    expect(panel.getByText(/32\/72 characters in the subject/)).toBeTruthy();
    await user.click(panel.getByRole('button', { name: 'Commit 2 files' }));
    expect(await panel.findByText('Committed 2 files as 3f9c2e1.')).toBeTruthy();
    await waitFor(() => expect(panel.queryByRole('checkbox', { name: /docs\/terminal\.md/ })).toBeNull());
  });

  test('with Background AI paused, Generate says so and the message stays editable', async () => {
    const { user } = await openIdle('t3');
    await fetch('/api/settings', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ utility_daily_limit: 0 }) });
    const panel = await commitPanel(user);
    await user.click(panel.getByRole('button', { name: /^Commit/ }));
    await user.click(await panel.findByRole('button', { name: 'Generate' }));
    expect(await panel.findByText(/Background AI is off.*You can still write the message yourself\./)).toBeTruthy();
    const message = panel.getByRole('textbox', { name: 'Commit message' }) as HTMLTextAreaElement;
    await user.type(message, 'fix: by hand');
    expect(message.value).toBe('fix: by hand');
  });

  // The service decides what git allows (git_actions_test.go); the panel shows its words.
  test('shows the service\'s refusal as an alert, then the next action\'s outcome in its place', async () => {
    const { user } = await openIdle('t3');
    const panel = await commitPanel(user);
    await user.click(panel.getByRole('button', { name: 'Push' }));
    expect((await panel.findByRole('alert')).textContent).toMatch(/Pull first, then push:\n ! \[rejected\]/);
    await user.click(panel.getByRole('button', { name: 'Pull' }));
    expect((await panel.findByText('Pulled 2 new commits.')).getAttribute('role')).toBe('status');
    expect(panel.queryByRole('alert')).toBeNull();
  });

  test('says which task is mid-turn and keeps the writes off', async () => {
    const { user } = await openTask('t3');
    const panel = await commitPanel(user);
    expect(await panel.findByText(/still working in this repository/)).toBeTruthy();
    expect((panel.getByRole('button', { name: 'Push' }) as HTMLButtonElement).disabled).toBe(true);
    expect((panel.getByRole('button', { name: 'Pull' }) as HTMLButtonElement).disabled).toBe(true);
  });

  // The Changes column has a fixed height and does not scroll; expanded, the form is taller
  // than what a short window leaves it. It must shrink into that space and scroll itself, with
  // the actions inside, or they sit below the window (jsdom has no layout: this pins the classes).
  test('in Changes, the expanded form scrolls inside its own region so the actions stay reachable', async () => {
    const { user } = await openIdle('t3');
    const panel = await commitPanel(user);
    await user.click(panel.getByRole('button', { name: /^Commit/ }));
    const region = screen.getByRole('dialog', { name: 'Changes' }).querySelector('section[aria-label="Commit"]')!;
    expect([...region.classList]).toEqual(expect.arrayContaining(['min-h-0', 'shrink', 'overflow-y-auto']));
    expect(region.classList.contains('shrink-0')).toBe(false);
    for (const name of [/^Commit \d+ files?$/, 'Commit and push']) expect(region.contains(await panel.findByRole('button', { name }))).toBe(true);
  });

  test('sets up git in a project that has none', async () => {
    const { user } = await openIdle('t6');
    await user.click(screen.getByRole('button', { name: 'Not a Git repository' }));
    await user.click(await screen.findByRole('button', { name: 'Set up git here' }));
    expect(await screen.findByRole('button', { name: /^Open changes/ })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Not a Git repository' })).toBeNull();
  });
});

/** The finish card's commit panel on the finished mock Task t20, with git idle. */
async function finishCommit() {
  const rendered = await openIdle('t20');
  const card = (await screen.findByRole('heading', { name: 'Finished — check the evidence' })).closest('section')!;
  const panel = await within(card).findByRole('region', { name: 'Commit' });
  return { ...rendered, card, panel: within(panel) };
}

describe('finish card commit', () => {
  test('keeps the outcome in view after its files are committed, a failed push included', async () => {
    const { user, card, panel } = await finishCommit();
    // The card's panel keeps its natural height in the conversation's scroll; only Changes makes it scroll.
    const region = card.querySelector('section[aria-label="Commit"]')!;
    expect(region.classList.contains('shrink-0')).toBe(true);
    expect(region.classList.contains('overflow-y-auto')).toBe(false);
    await user.type(await panel.findByRole('textbox', { name: 'Commit message' }), 'fix(vterm): replay focus events');
    await user.click(panel.getByRole('button', { name: 'Commit and push' }));
    // The commit took the Task's files, so the Changes list empties; the push was refused.
    const outcome = await within(card).findByText(/^Committed 2 files as 3f9c2e1\. The push failed: .*Pull first, then push/);
    await waitFor(() => expect(within(card).queryByRole('checkbox', { name: /redraw\.go$/ })).toBeNull());
    expect(outcome.isConnected).toBe(true);
  });

  test('shares one draft with the Changes panel', async () => {
    const { user, panel } = await finishCommit();
    await user.type(await panel.findByRole('textbox', { name: 'Commit message' }), 'fix: one');
    await user.click(screen.getByRole('button', { name: 'Review changes' }));
    const sheet = within(await screen.findByRole('dialog', { name: 'Changes' }));
    const other = within(await sheet.findByRole('region', { name: 'Commit' }));
    await user.click(other.getByRole('button', { name: /^Commit/ }));
    const message = (await other.findByRole('textbox', { name: 'Commit message' })) as HTMLTextAreaElement;
    expect(message.value).toBe('fix: one');
    await user.type(message, ' and two');
    await user.click(other.getByRole('checkbox', { name: /redraw\.go$/ }));
    // The sheet is modal: the card behind it is hidden from the accessibility tree, not gone.
    expect((panel.getByRole('textbox', { name: 'Commit message', hidden: true }) as HTMLTextAreaElement).value).toBe('fix: one and two');
    expect((panel.getByRole('checkbox', { name: /redraw\.go$/, hidden: true }) as HTMLInputElement).checked).toBe(false);
  });
});

describe('default selection', () => {
  test('checks only the files this task edited, never another task’s or an unknown one', () => {
    const files = [
      { path: 'mine.go', status: 'modified', additions: 1, deletions: 0, mine: true },
      { path: 'unknown.go', status: 'modified', additions: 1, deletions: 0 },
      { path: 'theirs.go', status: 'modified', additions: 1, deletions: 0, other_task: true },
    ];
    // With this Task's edits unknown (after a restart) other Tasks' edits may be unknown too.
    expect(defaultSelection(files)).toEqual(['mine.go']);
  });
});
