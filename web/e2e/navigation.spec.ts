import { conversation, expect, newThread, send, startThread, test, threadState } from './harness';

test('a reload and a deep link restore the same Project and Thread', async ({ page, context, project, model }) => {
  model.reply('first deep thread', 'Reply in thread A.').title('first deep thread', 'Deep thread A');
  model.reply('second deep thread', 'Reply in thread B.').title('second deep thread', 'Deep thread B');
  const a = await startThread(page, project, 'first deep thread');
  await expect(conversation(page).getByText('Reply in thread A.')).toBeVisible();
  await newThread(page, project);
  await send(page, 'second deep thread');
  await expect(conversation(page).getByText('Reply in thread B.')).toBeVisible();
  const b = page.url();
  expect(b).not.toBe(a);

  await page.reload();
  await expect(page).toHaveURL(b);
  await expect(conversation(page).getByText('Reply in thread B.')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1, name: 'Deep thread B' })).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  const other = await context.newPage();
  await other.goto(a);
  await expect(conversation(other).getByText('Reply in thread A.')).toBeVisible();
  await expect(other.getByRole('heading', { level: 1, name: 'Deep thread A' })).toBeVisible();
  await expect(conversation(other).getByText('Reply in thread B.')).toHaveCount(0);
  await expect(other.getByRole('list', { name: 'Unsettled threads' }).getByRole('listitem').filter({ hasText: 'Deep thread A' })).toContainText(project.name);
  await other.close();
});
