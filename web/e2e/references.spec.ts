import { conversation, expect, send, startThread, test, threadState } from './harness';

const FOUND = [
  '## Issues found',
  '',
  '| # | Title | Status |',
  '|---|---|---|',
  '| 4 | Login times out after the SSO redirect | Open |',
  '| 5 | Crash when opening an empty project | Open |',
  '| 6 | Update the onboarding copy | Closed |',
  '| 8 | Retry the export when the queue is full | Open |',
  '| 9 | Chart labels unreadable in dark mode | Open |',
  '| 17 | Flaky pipeline on arm64 runners | Open |',
].join('\n');
const STATUS = 'Issues 4, 8, 9 and 17 are still open; 5 and 6 are closed. Issue 31 came up last week.';

test('the sign-off lists the numbered issues a reply cites and each jumps to its definition', async ({ page, project, model }) => {
  model.reply('list the tracker issues', FOUND);
  model.reply('where are we on them', STATUS);
  await startThread(page, project, 'list the tracker issues');
  await expect(conversation(page).getByText('Login times out after the SSO redirect')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await send(page, 'where are we on them');
  await expect(conversation(page).getByText(/Issue\s+31 came up last week/)).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  // One disclosure on the sign-off: 7 cited; 4, 5, 8, 9 and 17 recorded Open, 6 Closed; 31 is nowhere in the Thread.
  const ledger = conversation(page).getByRole('button', { name: /^7 issues, 5 open, 1 closed, 1 unknown\. Show the references this reply cites$/ });
  await expect(ledger).toHaveText('7 issues · 5 open · 1 closed · 1 unknown');
  await expect(ledger).toHaveAttribute('aria-expanded', 'false');
  await ledger.click();
  await expect(ledger).toHaveAttribute('aria-expanded', 'true');

  const rows = conversation(page).getByRole('list', { name: 'References this reply cites' }).getByRole('listitem');
  await expect(rows).toHaveCount(7);
  // In the reply's order, each with the title the first reply gave it.
  await expect(rows.nth(0)).toContainText('#4');
  await expect(rows.nth(0)).toContainText('Login times out after the SSO redirect');
  await expect(rows.nth(1)).toContainText('Retry the export when the queue is full');
  await expect(rows.nth(2)).toContainText('Chart labels unreadable in dark mode');
  await expect(rows.nth(3)).toContainText('Flaky pipeline on arm64 runners');
  await expect(rows.nth(4)).toContainText('Crash when opening an empty project');
  await expect(rows.nth(5)).toContainText('Update the onboarding copy');
  // The reply calls 5 closed; the Thread recorded it Open.
  await expect(rows.nth(4)).toContainText('reply says closed');
  await expect(rows.nth(5)).not.toContainText('reply says');
  await expect(rows.nth(6)).toContainText('#31');
  await expect(rows.nth(6)).toContainText('no definition in this Thread');

  // A row jumps to the reply that defined the issue.
  await rows.nth(0).getByRole('button').click();
  await expect(page.locator(':focus')).toContainText('Issues found');

  // A cited number's definition is a tip on keyboard focus (a native title never shows there, nor on a phone).
  const cite = conversation(page).getByRole('button', { name: /^#5: / });
  await expect(cite).not.toHaveAttribute('title');
  await page.keyboard.press('Shift');
  await cite.focus();
  await expect(page.getByRole('tooltip')).toHaveText('Crash when opening an empty project');
  await page.keyboard.press('Escape');

  // A cited number in the prose opens the list again on its row.
  await ledger.click();
  await expect(ledger).toHaveAttribute('aria-expanded', 'false');
  await conversation(page).getByRole('button', { name: /^#31: no definition in this Thread/ }).click();
  await expect(ledger).toHaveAttribute('aria-expanded', 'true');
  await expect(rows.nth(6)).toBeVisible();
});
