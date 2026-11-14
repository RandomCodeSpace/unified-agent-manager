// Token usage: the Usage popover beside Settings and a Thread's Tools → Usage tab, with the scripted token counts.
import { type Page } from '@playwright/test';
import { conversation, expect, newThread, openHome, send, startThread, test, threadState } from './harness';
import { keepSettings, quietBackground, readSettings, threadId, withUsage } from './settings-helpers';

/** The Usage popover's opener in the sidebar. */
const usageButton = (page: Page) => page.getByRole('complementary').getByRole('button', { name: 'Usage and AI', exact: true });

/** The popover's box lies inside the window, its body needs no scrolling of its own, and the page does not scroll sideways (#619). */
async function expectFits(page: Page) {
  const dialog = page.getByRole('dialog', { name: 'Usage' });
  // Measured once its opening animation (a sheet slides up on a phone) has ended.
  await dialog.evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)));
  const box = (await dialog.boundingBox())!;
  const view = page.viewportSize()!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(view.width);
  expect(box.y + box.height).toBeLessThanOrEqual(view.height);
  const overflow = await dialog.evaluate((el) => [el, ...el.querySelectorAll('*')]
    .filter((e) => ['auto', 'scroll'].includes(getComputedStyle(e).overflowY))
    .map((e) => e.scrollHeight - e.clientHeight));
  expect(overflow.length).toBeGreaterThan(0);
  for (const extra of overflow) expect(extra).toBeLessThanOrEqual(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
}

test('the Usage popover shows a turn\'s exact tokens per period, adds the next turn while open, and fits the window', async ({ page, uam, project, model }) => {
  const restore = await keepSettings(uam, ['custom_models']);
  const quiet = await quietBackground(uam);
  try {
    // A model of its own: the worker's other cases add usage to the mock's model.
    const tag = Math.random().toString(36).slice(2, 8);
    const name = `Usage ${tag}`;
    const mock = (await readSettings(uam)).custom_models!.find((m) => m.model_id === 'mock-1')!;
    await uam.api('PATCH', '/api/settings', {
      custom_models: [...(await readSettings(uam)).custom_models!.map(({ key_present: _, ...m }) => m), { name: mock.name, display_name: name, base_url: mock.base_url, model_id: `usage-${tag}`, api_key_env: '' }],
    });
    model.on({ userMessage: 'count my first tokens' }, withUsage('First counted reply.', 1000, 50));
    model.on({ userMessage: 'count my second tokens' }, withUsage('Second counted reply.', 1000, 50));
    const dialog = page.getByRole('dialog', { name: 'Usage' });
    const period = dialog.getByRole('group', { name: 'Usage period' });
    const split = dialog.getByRole('img', { name: new RegExp(`^${name} token split: `) });

    await openHome(page);
    await usageButton(page).click();
    await expect(dialog.getByText('Total tokens')).toBeVisible();
    await expect(dialog.getByRole('alert')).toHaveCount(0);
    await dialog.getByRole('group', { name: 'Usage view' }).getByRole('button', { name: /^All models/ }).click();
    await expect(split).toHaveCount(0);
    await dialog.getByRole('button', { name: 'Close usage' }).click();
    await expect(dialog).toBeHidden();

    await newThread(page, project);
    await page.getByRole('button', { name: /^Model:/ }).click();
    await page.getByRole('menu').getByRole('menuitemradio', { name }).click();
    await expect(page.getByRole('button', { name: `Model: ${name}` })).toBeVisible();
    await send(page, 'count my first tokens');
    await expect(conversation(page).getByText('First counted reply.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();

    await usageButton(page).click();
    await expect(period.getByRole('button', { name: 'Today' })).toHaveAttribute('aria-pressed', 'true');
    await dialog.getByRole('group', { name: 'Usage view' }).getByRole('button', { name: /^All models/ }).click();
    await expect(split).toHaveAccessibleName(`${name} token split: Input 1,000; Output 50; Cache 0`);
    await period.getByRole('button', { name: 'Lifetime' }).click();
    await expect(split).toHaveAccessibleName(`${name} token split: Input 1,000; Output 50; Cache 0`);
    // The popover reads usage again every 15 s while it is open.
    await uam.api('POST', `/api/sessions/${threadId(page)}/prompt`, { text: 'count my second tokens', request_id: crypto.randomUUID() });
    await expect(split).toHaveAccessibleName(`${name} token split: Input 2,000; Output 100; Cache 0`, { timeout: 30_000 });
    await period.getByRole('button', { name: 'Today' }).click();
    await expect(split).toHaveAccessibleName(`${name} token split: Input 2,000; Output 100; Cache 0`);
    expect(model.served().map((s) => s.request.model)).toEqual([`usage-${tag}`, `usage-${tag}`]);

    await dialog.getByRole('group', { name: 'Usage view' }).getByRole('button', { name: 'Overview' }).click();
    await expectFits(page);
    await dialog.getByRole('button', { name: 'Close usage' }).click();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await expect(threadState(page, 'Finished')).toBeVisible();
    // On a phone the sidebar is a drawer.
    await page.getByRole('button', { name: 'Show sidebar' }).click();
    await page.getByRole('dialog', { name: 'Projects' }).getByRole('button', { name: 'Usage and AI', exact: true }).click();
    await expect(dialog.getByText('Total tokens')).toBeVisible();
    await expectFits(page);
  } finally {
    await quiet();
    await restore();
  }
});

test('a Thread\'s Tools → Usage tab counts its turns, and still does once the Thread is settled and reopened', async ({ page, uam, project, model }) => {
  const quiet = await quietBackground(uam);
  try {
    model.on({ userMessage: 'thread usage turn one' }, withUsage('Turn one counted.', 1000, 50));
    model.on({ userMessage: 'thread usage turn two' }, withUsage('Turn two counted.', 1000, 50));
    await startThread(page, project, 'thread usage turn one');
    await expect(threadState(page, 'Finished')).toBeVisible();
    await send(page, 'thread usage turn two');
    await expect(conversation(page).getByText('Turn two counted.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();

    const tools = page.getByRole('dialog', { name: 'Tools' });
    const usage = tools.getByRole('tabpanel', { name: 'Usage' }).getByRole('region', { name: 'This Thread' });
    const expectCounts = async () => {
      await expect(usage.getByText('Tokens in').locator('xpath=following-sibling::*[1]')).toHaveText('2,000');
      await expect(usage.getByText('Tokens out').locator('xpath=following-sibling::*[1]')).toHaveText('100');
      await expect(usage.getByText(/^2 calls · /)).toBeVisible();
    };
    const openUsage = async () => {
      await page.getByRole('button', { name: /^Tools\. / }).click();
      await tools.getByRole('tab', { name: 'Usage' }).click();
    };
    await openUsage();
    await expectCounts();
    await usage.getByRole('button', { name: 'Refresh' }).click();
    await expectCounts();
    await page.keyboard.press('Escape');
    await expect(tools).toBeHidden();

    // Settling closes the conversation; the counts come from Copilot's record after that.
    await page.getByRole('button', { name: 'Thread actions' }).click();
    await page.getByRole('menuitem', { name: 'Settle', exact: true }).click();
    await expect(threadState(page, 'Settled')).toBeVisible();
    await page.reload();
    await expect(threadState(page, 'Settled')).toBeVisible();
    await openUsage();
    await expectCounts();
    expect(model.served()).toHaveLength(2);
  } finally {
    await quiet();
  }
});
