// Settings → GitHub Copilot with the pack's signed-out CLI: the account and Restart Copilot runtime.
import { conversation, expect, newThread, openHome, send, startThread, test, threadState } from './harness';
import { openSettings } from './settings-helpers';

test('signed-out Copilot shows a banner with Open Settings, and the account says Signed out with Copilot\'s reason', async ({ page, uam }) => {
  await openHome(page);
  const banner = page.getByRole('main').getByRole('status').filter({ hasText: 'GitHub Copilot is signed out. Sign in in Settings.' });
  await expect(banner).toBeVisible();
  await banner.getByRole('button', { name: 'Open Settings' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible();
  await expect(page).toHaveURL(/#settings$/);
  await expect(banner.getByRole('button', { name: 'Open Settings' })).toHaveCount(0);

  await openSettings(page, 'GitHub Copilot');
  const rail = page.getByRole('navigation', { name: 'Settings sections' });
  await expect(rail.getByText('Signed out', { exact: true })).toBeVisible();
  const account = page.getByRole('region', { name: 'GitHub Copilot' });
  const state = account.getByRole('status').filter({ hasText: 'Copilot says:' });
  await expect(state).toContainText('Signed out');
  await expect(state).toContainText('Threads cannot start or run until Copilot is signed in. Copilot says: Not authenticated');
  await expect(account.getByRole('button', { name: 'Sign in with GitHub' })).toBeEnabled();
  await expect(account.getByRole('textbox', { name: 'Or sign in with a token' })).toBeEditable();
  await expect(account.getByRole('button', { name: 'Sign in', exact: true })).toBeDisabled();

  await account.getByRole('button', { name: 'Check again' }).first().click();
  await expect(state).toContainText('Copilot says: Not authenticated');
  await expect(account.getByRole('alert')).toHaveCount(0);
  expect(await uam.api('GET', '/api/providers/copilot/account')).toMatchObject({ signed_in: false });
});

test('Restart Copilot runtime names the waiting Thread, stops it, and both Threads answer on a fresh runtime', async ({ page, project, model }) => {
  model.reply('restart leaves this idle', 'Idle before the restart.').title('restart leaves this idle', 'Idle thread title');
  // A file creation waits for permission, so the turn stays open.
  model.on({ userMessage: 'restart stops this waiting turn' }, { toolCalls: [{ id: 'call_hold', name: 'create', arguments: JSON.stringify({ path: 'held.txt', file_text: 'held' }) }] });
  model.title('restart stops this waiting turn', 'Waiting thread title');
  model.reply('after restart the waiting one', 'Waiting thread answers again.');
  model.reply('after restart the idle one', 'Idle thread answers again.');

  const idle = await startThread(page, project, 'restart leaves this idle');
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1, name: 'Idle thread title' })).toBeVisible();
  await newThread(page, project);
  await send(page, 'restart stops this waiting turn');
  await expect(threadState(page, 'Needs permission')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1, name: 'Waiting thread title' })).toBeVisible();
  const waiting = page.url();

  await openSettings(page, 'GitHub Copilot');
  await page.getByRole('button', { name: 'Restart Copilot runtime' }).click();
  const confirm = page.getByRole('alertdialog', { name: 'Restart the Copilot runtime?' });
  await expect(confirm.getByRole('listitem').filter({ hasText: 'Waiting thread title' })).toHaveCount(1);
  await expect(confirm.getByRole('listitem').filter({ hasText: 'Idle thread title' })).toHaveCount(0);
  await confirm.getByRole('button', { name: 'Restart', exact: true }).click();
  await expect(page.getByRole('group', { name: 'Copilot CLI' }).getByRole('status').filter({ hasText: /^Copilot restarted\. \d+ threads? stopped; send their message again\.$/ })).toBeVisible({ timeout: 30_000 });

  await page.goto(waiting);
  // The header's state says what stopped it.
  await expect(threadState(page, 'Stopped').getByTitle('Copilot runtime restarted by the owner', { exact: true })).toBeVisible();
  await send(page, 'after restart the waiting one');
  await expect(conversation(page).getByText('Waiting thread answers again.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await page.goto(idle);
  await expect(conversation(page).getByText('Idle before the restart.')).toBeVisible();
  await send(page, 'after restart the idle one');
  await expect(conversation(page).getByText('Idle thread answers again.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await openHome(page);
});
