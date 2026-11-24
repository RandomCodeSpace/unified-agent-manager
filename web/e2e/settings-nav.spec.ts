// Settings as a place: the rail and its search, the phone's list, help tips, this browser's theme and motion, Log out.
import { type Page } from '@playwright/test';
import { expect, test } from './harness';
import { keepSettings, openSettings, readSettings } from './settings-helpers';

const rail = (page: Page) => page.getByRole('navigation', { name: 'Settings sections' });
const find = (page: Page) => rail(page).getByRole('searchbox', { name: 'Find a setting' });
const railPage = (page: Page, label: string) => rail(page).getByRole('button', { name: label, exact: true });

test('the rail lists the pages by group; Find a setting narrows them by words, Enter opens the first, and a page keeps its form once shown', async ({ page }) => {
  await page.goto('/#settings');
  const pagesOf = (group: string) => rail(page).getByRole('group', { name: group, exact: true }).getByRole('button');
  await expect(pagesOf('Threads')).toHaveText(['New threads', 'Conversation', 'Projects & worktrees', 'Archived']);
  await expect(pagesOf('Models & AI')).toHaveText(['Models', 'Background AI', 'Custom providers']);
  await expect(pagesOf('Accounts')).toHaveText(['GitHub Copilot', 'GitLab']);
  await expect(pagesOf('Agent toolkit')).toHaveText(['Agents', 'Skills', 'Hooks', 'Instructions', 'MCP servers']);
  await expect(pagesOf('Replies')).toHaveText(['Output styles']);
  await expect(pagesOf('Server')).toHaveText(['UAM and access', 'Connected instances', 'Session stats']);
  await expect(pagesOf('This browser')).toHaveText(['Appearance & alerts']);
  // A wide screen opens on New threads; a page not shown yet is not in the document.
  await expect(railPage(page, 'New threads')).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('region', { name: 'New threads' })).toBeVisible();
  const providers = page.getByRole('region', { name: 'Custom providers', includeHidden: true });
  await expect(providers).toHaveCount(0);

  // Every word must match: "key" alone also finds Conversation (keyboard), "byom key" only Custom providers.
  await find(page).fill('key');
  await expect(rail(page).getByRole('button')).toHaveText(['Conversation', 'Custom providers']);
  await find(page).fill('byom key');
  await expect(rail(page).getByRole('button')).toHaveText(['Custom providers']);
  await expect(page.getByRole('button', { name: 'Models & AI Custom providers' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'New threads' })).toBeHidden();
  await find(page).press('Enter');
  await expect(find(page)).toHaveValue('');
  await expect(railPage(page, 'Custom providers')).toHaveAttribute('aria-current', 'page');
  await expect(rail(page).getByRole('button')).toHaveCount(19);
  await expect(page.getByRole('region', { name: 'Custom providers' })).toBeVisible();

  // The page stays mounted, hidden, once shown: its open form keeps what was typed.
  await page.getByRole('region', { name: 'Custom providers' }).getByRole('button', { name: 'Add provider' }).click();
  const name = page.getByRole('form', { name: 'Add a provider' }).getByRole('textbox', { name: 'Provider name' });
  await name.fill('kept-while-away');
  await railPage(page, 'Conversation').click();
  await expect(page.getByRole('region', { name: 'Composer' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Custom providers' })).toBeHidden();
  await expect(providers).toHaveCount(1);
  await railPage(page, 'Custom providers').click();
  await expect(name).toHaveValue('kept-while-away');

  // No match says so; Escape clears the search first and leaves Settings open.
  await find(page).fill('zzzz nothing');
  await expect(rail(page).getByRole('status')).toHaveText('No setting matches “zzzz nothing”.');
  await expect(rail(page).getByRole('button')).toHaveCount(0);
  await find(page).press('Escape');
  await expect(find(page)).toHaveValue('');
  await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible();
  await expect(name).toHaveValue('kept-while-away');
});

test.describe('at phone width', () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test('Settings is a list of pages with their current values; a page opens with focus on its back button, and back returns focus to its row', async ({ page, uam }) => {
    const restore = await keepSettings(uam, ['suggest_replies']);
    try {
      await page.goto('/#settings');
      const conversation = railPage(page, 'Conversation');
      await expect(conversation).toHaveAccessibleDescription('Enter while running: Send now · Suggested replies on · Excalidraw');
      await expect(railPage(page, 'UAM and access')).toHaveAccessibleDescription(/^Running \S+ · Terminal off$/);
      await expect(railPage(page, 'Appearance & alerts')).toHaveAccessibleDescription('Theme: Match system');
      await expect(railPage(page, 'Custom providers')).toHaveAccessibleDescription(/^mock · \d+ models?$/);
      await expect(railPage(page, 'GitHub Copilot')).toHaveAccessibleDescription(/^Signed out/);
      // The list is the whole view: no page beside it.
      await expect(page.getByRole('region', { name: 'New threads' })).toBeHidden();

      await conversation.click();
      const back = page.getByRole('button', { name: 'All settings' });
      await expect(back).toBeFocused();
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Conversation · Settings');
      await expect(rail(page)).toBeHidden();
      await page.getByRole('region', { name: 'Composer' }).getByRole('switch', { name: 'Suggest replies' }).click();
      await expect.poll(async () => (await readSettings(uam)).suggest_replies).toBe(false);

      await back.click();
      await expect(conversation).toBeFocused();
      await expect(conversation).toHaveAccessibleDescription('Enter while running: Send now · Suggested replies off · Excalidraw');
      await expect(page.getByRole('region', { name: 'Composer' })).toBeHidden();
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Settings');
    } finally {
      await restore();
    }
  });
});

test.describe('help tips', () => {
  // A tap needs a touch screen; the mouse and the keyboard work beside it.
  test.use({ hasTouch: true });

  test('a help tip beside a label opens on hover, on keyboard focus and on a tap; a disabled control\'s reason stays on the page', async ({ page }) => {
    await openSettings(page, 'Conversation');
    const composer = page.getByRole('region', { name: 'Composer' });
    const help = composer.getByRole('button', { name: 'About Suggest replies' });
    const toggle = composer.getByRole('switch', { name: 'Suggest replies' });
    const tip = page.getByRole('tooltip').filter({ hasText: 'written by the Utility model' });
    await expect(toggle).toBeChecked();
    await expect(tip).toBeHidden();

    await help.hover();
    await expect(tip).toBeVisible();
    // The taps happen with the mouse resting on the button: desktop Chromium keeps a mouse a phone does not have,
    // and reports it leaving when the page under it changes, which would close the tip a tap opened.
    await help.tap();
    await expect(tip).toBeHidden();
    await help.tap();
    await expect(tip).toBeVisible();
    await page.getByRole('heading', { level: 1, name: 'Settings' }).hover();
    await expect(tip).toBeHidden();

    // Tab from the control before it: the keyboard reaches the tip's button.
    await composer.getByRole('radio', { name: 'After this turn' }).focus();
    await page.keyboard.press('Tab');
    await expect(help).toBeFocused();
    await expect(tip).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(tip).toBeHidden();
    await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible();
    // Reading the help changed nothing.
    await expect(toggle).toBeChecked();

    // The mock model has no effort levels: the reason is on the page, not behind a tip.
    await openSettings(page, 'New threads');
    const threads = page.getByRole('region', { name: 'New threads' });
    const effort = threads.getByRole('combobox', { name: 'Effort' });
    await expect(effort).toBeDisabled();
    await expect(effort).toHaveAccessibleDescription('Model default.');
    await expect(threads.getByRole('button', { name: 'About Effort' })).toHaveCount(0);
    await expect(threads.getByRole('button', { name: 'About Mode' })).toBeVisible();
    // On the page means drawn at its size; help behind a tip is kept for screen readers in one pixel.
    const drawn = async (text: string) => (await threads.getByText(text, { exact: true }).first().boundingBox())!.width;
    expect(await drawn('Model default.')).toBeGreaterThan(40);
    expect(await drawn('Asks first.')).toBeLessThanOrEqual(1);
  });
});

test('Theme and Motion are kept in this browser, applied at once and still after a reload; Match system follows the OS', async ({ page, uam }) => {
  await page.emulateMedia({ colorScheme: 'light' });
  await openSettings(page, 'Appearance & alerts');
  const section = page.getByRole('region', { name: 'Appearance & alerts' });
  const theme = section.getByRole('radiogroup', { name: 'Theme' });
  const motion = section.getByRole('radiogroup', { name: 'Motion' });
  const root = page.locator('html');
  const stored = (key: string) => page.evaluate((k) => localStorage.getItem(k), key);
  await expect(theme.getByRole('radio', { name: 'Match system' })).toBeChecked();
  await expect(root).toHaveAttribute('data-theme', 'light');
  expect(await stored('uam.theme')).toBeNull();

  await theme.getByRole('radio', { name: 'Dark' }).click();
  await expect(root).toHaveAttribute('data-theme', 'dark');
  expect(await stored('uam.theme')).toBe('dark');
  await motion.getByRole('radio', { name: 'Match system' }).click();
  await expect(root).toHaveAttribute('data-motion', 'system');
  expect(await stored('uam.motion')).toBe('system');
  // This browser's alone: the service keeps neither.
  const saved = await readSettings(uam);
  expect(saved).not.toHaveProperty('theme');
  expect(saved).not.toHaveProperty('motion');

  await page.reload();
  await expect(root).toHaveAttribute('data-theme', 'dark');
  await openSettings(page, 'Appearance & alerts');
  await expect(theme.getByRole('radio', { name: 'Dark' })).toBeChecked();
  await expect(motion.getByRole('radio', { name: 'Match system' })).toBeChecked();
  await expect(root).toHaveAttribute('data-theme', 'dark');
  await expect(root).toHaveAttribute('data-motion', 'system');

  await theme.getByRole('radio', { name: 'Light' }).click();
  await expect(root).toHaveAttribute('data-theme', 'light');
  expect(await stored('uam.theme')).toBe('light');
  await page.emulateMedia({ colorScheme: 'dark' });
  await expect(root).toHaveAttribute('data-theme', 'light');

  await theme.getByRole('radio', { name: 'Match system' }).click();
  await expect(root).toHaveAttribute('data-theme', 'dark');
  expect(await stored('uam.theme')).toBe('system');
  await page.emulateMedia({ colorScheme: 'light' });
  await expect(root).toHaveAttribute('data-theme', 'light');

  await motion.getByRole('radio', { name: 'Always on' }).click();
  await expect(root).not.toHaveAttribute('data-motion');
  expect(await stored('uam.motion')).toBe('on');
});

test('Log out in UAM and access ends this browser\'s session and returns to sign-in', async ({ page }) => {
  await openSettings(page, 'UAM and access');
  expect((await page.request.get('/api/settings')).status()).toBe(200);
  await page.getByRole('region', { name: 'Access' }).getByRole('button', { name: 'Log out' }).click();
  await expect(page.getByRole('heading', { name: 'Sign in to UAM' })).toBeVisible();
  await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toHaveCount(0);
  // The cookie no longer opens the service, also for a fresh page load.
  expect((await page.request.get('/api/settings')).status()).toBe(401);
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Sign in to UAM' })).toBeVisible();
});
