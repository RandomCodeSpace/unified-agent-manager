import { conversation, expect, newThread, openHome, send, startThread, test } from './harness';

test('Settings survives a reload over its Thread; Back and Forward move between Threads; an unknown fragment lands on Home', async ({ page, project, model }) => {
  model.reply('history thread one', 'Reply in history one.').title('history thread one', 'History one');
  model.reply('history thread two', 'Reply in history two.').title('history thread two', 'History two');
  const one = await startThread(page, project, 'history thread one');
  await expect(conversation(page).getByText('Reply in history one.')).toBeVisible();
  await newThread(page, project);
  await send(page, 'history thread two');
  await expect(conversation(page).getByText('Reply in history two.')).toBeVisible();
  const two = page.url();
  const heading = (name: string) => page.getByRole('heading', { level: 1, name });
  await expect(heading('History two')).toBeVisible();

  await page.getByRole('navigation', { name: 'Threads' }).getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(page).toHaveURL(/#settings$/);
  await page.reload();
  await expect(page).toHaveURL(/#settings$/);
  await expect(page.getByRole('navigation', { name: 'Settings sections' })).toBeVisible();
  // After a reload Settings has no Thread beneath it: the fragment names only Settings.
  await page.keyboard.press('Escape');
  await expect(page.getByRole('navigation', { name: 'Settings sections' })).toBeHidden();

  const rows = page.getByRole('list', { name: 'Unsettled threads' });
  await rows.getByRole('button', { name: / History two / }).click();
  await expect(page).toHaveURL(two);
  await rows.getByRole('button', { name: / History one / }).click();
  await expect(page).toHaveURL(one);
  await expect(heading('History one')).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(two);
  await expect(heading('History two')).toBeVisible();
  await expect(conversation(page).getByText('Reply in history two.')).toBeVisible();
  await page.goForward();
  await expect(page).toHaveURL(one);
  await expect(conversation(page).getByText('Reply in history one.')).toBeVisible();

  await page.goto('/#nope');
  await expect(page.getByRole('region', { name: 'Home' })).toBeVisible();
  await expect(page).not.toHaveURL(/#nope/);
});

test('the Theme choice applies at once and is kept across a reload; Match system follows the OS', async ({ page }) => {
  const theme = () => page.evaluate(() => document.documentElement.dataset.theme);
  await page.emulateMedia({ colorScheme: 'dark' });
  await openHome(page);
  await page.getByRole('complementary').getByRole('button', { name: 'Settings', exact: true }).click();
  const appearance = () => page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'Appearance & alerts', exact: true }).click();
  await appearance();
  const group = page.getByRole('radiogroup', { name: 'Theme' });
  const pick = (name: string) => group.getByRole('radio', { name, exact: true }).click();
  try {
    await expect(group.getByRole('radio', { name: 'Match system' })).toBeChecked();
    await expect.poll(theme).toBe('dark');
    await page.emulateMedia({ colorScheme: 'light' });
    await expect.poll(theme).toBe('light');
    const light = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);

    await pick('Dark');
    await expect.poll(theme).toBe('dark');
    // The scheme the page has before React runs: no flash of the OS scheme on a reload.
    await page.addInitScript(() => {
      document.addEventListener('DOMContentLoaded', () => { (window as unknown as { early: string }).early = document.documentElement.dataset.theme ?? ''; });
    });
    await page.reload();
    expect(await page.evaluate(() => (window as unknown as { early: string }).early)).toBe('dark');
    expect(await page.evaluate(() => getComputedStyle(document.body).backgroundColor)).not.toBe(light);
    await appearance();
    await expect(group.getByRole('radio', { name: 'Dark' })).toBeChecked();

    await pick('Light');
    await page.emulateMedia({ colorScheme: 'dark' });
    await page.reload();
    expect(await page.evaluate(() => (window as unknown as { early: string }).early)).toBe('light');
    await appearance();
    await expect(group.getByRole('radio', { name: 'Light' })).toBeChecked();
  } finally {
    await page.evaluate(() => localStorage.removeItem('uam.theme'));
  }
});

test('on a phone the sidebar is a drawer: Esc closes it every time and a tap opens a Thread', async ({ browser, page: desktop, uam, project, model }) => {
  model.reply('drawer alpha prompt', 'Drawer alpha reply.').title('drawer alpha prompt', 'Drawer alpha');
  model.reply('drawer beta prompt', 'Drawer beta reply.').title('drawer beta prompt', 'Drawer beta');
  const context = await browser.newContext({ baseURL: uam.url, viewport: { width: 390, height: 844 }, hasTouch: true });
  try {
    expect((await context.request.post('/api/login', { data: { token: uam.token } })).status()).toBe(204);
    // The Threads are made at desktop size; the phone opens on Home with both.
    const alpha = await startThread(desktop, project, 'drawer alpha prompt');
    await expect(conversation(desktop).getByText('Drawer alpha reply.')).toBeVisible();
    await newThread(desktop, project);
    await send(desktop, 'drawer beta prompt');
    await expect(conversation(desktop).getByText('Drawer beta reply.')).toBeVisible();

    const page = await context.newPage();
    await openHome(page);
    const toggle = page.getByRole('button', { name: /^Show sidebar/ });
    const sheet = page.getByRole('dialog', { name: 'Projects' });
    const drawer = sheet.getByRole('navigation', { name: 'Threads' });
    const rows = drawer.getByRole('list', { name: 'Unsettled threads' });
    for (let i = 0; i < 3; i++) {
      await toggle.tap();
      await expect(drawer).toBeVisible();
      await expect(rows.getByRole('button', { name: / Drawer alpha / })).toBeVisible();
      await expect(rows.getByRole('button', { name: / Drawer beta / })).toBeVisible();
      await expect(sheet).toBeFocused();
      await page.keyboard.press('Escape');
      await expect(drawer).toBeHidden();
      await expect(toggle).toBeFocused();
    }

    await toggle.tap();
    await drawer.getByRole('searchbox', { name: 'Search threads' }).fill('Drawer');
    await rows.or(drawer).getByRole('button', { name: / Drawer alpha / }).first().tap();
    await expect(drawer).toBeHidden();
    await expect(page).toHaveURL(alpha);
    await expect(conversation(page).getByText('Drawer alpha reply.')).toBeVisible();
    await toggle.tap();
    await expect(drawer.getByRole('searchbox', { name: 'Search threads' })).toHaveValue('Drawer');
  } finally {
    await context.close();
  }
});

test('a narrow header shrinks the parent Thread crumb instead of drawing it over the title', async ({ page, project, model, uam }) => {
  model.toolRound('start a helper thread', [{ id: 'call_spawn', name: 'uam_create_task', arguments: { project: project.id, prompt: 'helper thread task', name: 'Helper', worktree: false } }], 'Helper started.');
  model.reply('helper thread task', 'Helper reply.');
  const url = await startThread(page, project, 'start a helper thread');
  await expect(conversation(page).getByText('Helper started.')).toBeVisible();
  // A long parent name, set by hand so the background title cannot replace it.
  const parent = 'Investigate the flaky arm64 runners and the export retries together';
  await uam.api('PATCH', `/api/sessions/${url.split('#task=')[1]}`, { name: parent });
  await expect(page.getByRole('heading', { level: 1, name: parent })).toBeVisible();

  await page.getByRole('navigation', { name: 'Threads' }).getByRole('button', { name: /Finished Helper/ }).click();
  const title = page.getByRole('heading', { level: 1, name: 'Helper' });
  await expect(title).toBeVisible();
  // Just above the phone breakpoint, where the header has least room.
  await page.setViewportSize({ width: 490, height: 800 });
  const crumb = page.getByRole('button', { name: parent, exact: true });
  await expect(crumb).toBeVisible();
  // How far the crumb's right edge runs past the title's left edge.
  await expect.poll(async () => {
    const [c, t] = [await crumb.boundingBox(), await title.boundingBox()];
    return Math.max(0, c!.x + c!.width - t!.x);
  }).toBeLessThanOrEqual(1);
});
