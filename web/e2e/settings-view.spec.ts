// Settings opens over the open Thread and gives it back where it was.
import { type Page } from '@playwright/test';
import { conversation, expect, startThread, test, threadState } from './harness';

/** The conversation's scrolling element: the nearest ancestor of its log that scrolls. */
const scroller = (page: Page) => conversation(page).getByRole('log').evaluateHandle((el) => {
  for (let e: HTMLElement | null = el as HTMLElement; e; e = e.parentElement) {
    if (['auto', 'scroll'].includes(getComputedStyle(e).overflowY) && e.scrollHeight > e.clientHeight) return e;
  }
  throw new Error('the conversation does not scroll');
});

test('Settings over a Thread: the gear sets #settings, Esc and Close settings bring the Thread back at its scroll position, a reload stays on Settings', async ({ page, project, model }) => {
  model.reply('write a long answer', Array.from({ length: 120 }, (_, i) => `Long answer paragraph ${i + 1}.`).join('\n\n'));
  const thread = await startThread(page, project, 'write a long answer');
  await expect(conversation(page).getByText('Long answer paragraph 120.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  const middle = await (await scroller(page)).evaluate((el) => {
    el.scrollTop = Math.round((el.scrollHeight - el.clientHeight) / 2);
    return el.scrollTop;
  });
  expect(middle).toBeGreaterThan(0);
  const gear = page.getByRole('complementary').getByRole('button', { name: 'Settings', exact: true });
  const settings = page.getByRole('heading', { level: 1, name: 'Settings' });
  const backAt = async () => {
    await expect(page).toHaveURL(thread);
    await expect(conversation(page)).toBeVisible();
    await expect.poll(async () => Math.abs(await (await scroller(page)).evaluate((el) => el.scrollTop) - middle)).toBeLessThanOrEqual(2);
  };

  await gear.click();
  await expect(page).toHaveURL(/#settings$/);
  await expect(settings).toBeVisible();
  await expect(conversation(page)).toBeHidden();
  await page.keyboard.press('Escape');
  await backAt();

  await gear.click();
  await expect(settings).toBeVisible();
  await page.getByRole('button', { name: 'Close settings' }).click();
  await backAt();

  await gear.click();
  await expect(settings).toBeVisible();
  await page.reload();
  await expect(settings).toBeVisible();
  await expect(page).toHaveURL(/#settings$/);
});
