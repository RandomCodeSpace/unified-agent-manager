// The composer's toolbar at narrowing widths (lib/toolbarFold, Composer.tsx).
import { conversation, expect, startThread, test, threadState } from './harness';

test('the toolbar folds its labels as the window narrows, never overflows, and moves pickers into More on a phone', async ({ page, project, model }) => {
  model.reply('fold check', 'Folding reply.');
  await page.setViewportSize({ width: 1400, height: 900 });
  await startThread(page, project, 'fold check');
  await expect(conversation(page).getByText('Folding reply.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  const form = page.locator('form').filter({ has: page.getByRole('textbox', { name: 'Message' }) });
  const attach = form.getByRole('button', { name: /^Attach files/ });
  const modelButton = form.getByRole('button', { name: 'Model: Mock' });
  const run = form.getByRole('button', { name: /^Permissions and execution:/ });
  const more = form.getByRole('button', { name: 'More settings: effort, context, permissions and execution' });
  const sendButton = form.getByRole('button', { name: 'Send', exact: true });
  const fits = async () => {
    expect(await form.evaluate((f) => f.scrollWidth <= f.clientWidth)).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    for (const b of [attach, modelButton, sendButton]) await expect(b).toBeInViewport({ ratio: 1 });
  };

  await expect(run).toHaveText('Safe · Interactive', { useInnerText: true });
  await expect(modelButton).toHaveText('Mock', { useInnerText: true });
  await expect(more).toBeHidden();
  await fits();

  // The execution word goes first; the model's name stays.
  await page.setViewportSize({ width: 700, height: 900 });
  await expect(run).toHaveText('Safe', { useInnerText: true });
  await expect(modelButton).toHaveText('Mock', { useInnerText: true });
  await fits();

  // A phone puts the pickers in More and the model in a bottom sheet.
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(run).toBeHidden();
  await expect(more).toBeVisible();
  await fits();
  await more.click();
  const menu = page.getByRole('menu');
  await expect(menu).toContainText('Safe');
  await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();

  await modelButton.click();
  const sheet = page.getByRole('dialog', { name: 'Model' });
  await expect(sheet.getByRole('radio', { name: 'Mock' })).toBeChecked();
  const box = await sheet.boundingBox();
  expect(box).not.toBeNull();
  expect(box!.y + box!.height).toBeGreaterThanOrEqual(844 - 1);
  expect(box!.width).toBeGreaterThanOrEqual(390 - 16);
  await sheet.getByRole('button', { name: 'Close' }).click();
  await expect(sheet).toBeHidden();
});
