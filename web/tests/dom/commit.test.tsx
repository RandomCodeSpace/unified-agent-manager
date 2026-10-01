import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
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
    expect(panel.getByText('3 files from other tasks are not included')).toBeTruthy();
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

  test('push refuses until a pull fast-forwards, then pushes', async () => {
    const { user } = await openIdle('t3');
    const panel = await commitPanel(user);
    await user.click(panel.getByRole('button', { name: 'Push' }));
    expect(await panel.findByText(/Pull first, then push/)).toBeTruthy();
    await user.click(panel.getByRole('button', { name: 'Pull' }));
    expect(await panel.findByText('Pulled 2 new commits.')).toBeTruthy();
    await user.click(panel.getByRole('button', { name: 'Push' }));
    expect(await panel.findByText(/^Pushed .* to origin\.$/)).toBeTruthy();
  });

  test('says which task is mid-turn and keeps the writes off', async () => {
    const { user } = await openTask('t3');
    const panel = await commitPanel(user);
    expect(await panel.findByText(/still working in this repository/)).toBeTruthy();
    expect((panel.getByRole('button', { name: 'Push' }) as HTMLButtonElement).disabled).toBe(true);
    expect((panel.getByRole('button', { name: 'Pull' }) as HTMLButtonElement).disabled).toBe(true);
  });

  test('sets up git in a project that has none', async () => {
    const { user } = await openIdle('t6');
    await user.click(screen.getByRole('button', { name: 'Not a Git repository' }));
    await user.click(await screen.findByRole('button', { name: 'Set up git here' }));
    expect(await screen.findByRole('button', { name: /^Open changes/ })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Not a Git repository' })).toBeNull();
  });
});
