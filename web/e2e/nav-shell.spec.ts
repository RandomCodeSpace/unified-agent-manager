import path from 'node:path';
import type { Page } from '@playwright/test';
import { conversation, expect, send, startThread, test, threadState, type Project } from './harness';

/** The mock model as the Copilot provider lists it, for Threads made through the API. */
const MOCK_MODEL_ID = 'mock/mock-1';

type Api = (method: string, route: string, body?: unknown) => Promise<unknown>;

const idleThread = async (api: Api, project: Project, name: string) =>
  ((await api('POST', '/api/sessions', { project_id: project.id, provider: 'copilot', model: MOCK_MODEL_ID, name })) as { id: string }).id;

const threads = (page: Page) => page.getByRole('navigation', { name: 'Threads' });
const menu = async (page: Page) => {
  await page.getByRole('main').getByRole('button', { name: 'Thread actions' }).click();
  return page.getByRole('menu');
};

test('Settings opens over the Thread; Esc and × bring it back with its scroll position, open rows and Changes panel, and focus on the gear', async ({ page, project, model, workdir }) => {
  const long = Array.from({ length: 120 }, (_, i) => `scroll line ${i + 1}`).join('\n\n');
  model.toolRound('write and talk long', [{ id: 'call_long', name: 'create', arguments: { path: path.join(workdir, 'long.txt'), file_text: 'long\n' } }], long);
  await startThread(page, project, 'write and talk long');
  await conversation(page).getByRole('button', { name: 'Allow once' }).click();
  await expect(conversation(page).getByText('scroll line 120', { exact: true })).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  const fold = conversation(page).getByRole('button', { name: /activity of this turn/ });
  await fold.click();
  await expect(fold).toHaveAttribute('aria-expanded', 'true');
  await page.getByRole('main').getByRole('button', { name: /^Open changes/ }).click();
  const changes = page.getByRole('complementary', { name: 'Changes' });
  await expect(changes).toBeVisible();
  const scroller = conversation(page);
  await scroller.evaluate((el) => { el.scrollTop = Math.round((el.scrollHeight - el.clientHeight) / 2); });
  const at = await scroller.evaluate((el) => el.scrollTop);
  expect(at).toBeGreaterThan(200);

  const gear = threads(page).getByRole('button', { name: 'Settings', exact: true });
  const sections = page.getByRole('navigation', { name: 'Settings sections' });
  for (const close of ['Escape', 'button']) {
    await gear.click();
    await expect(sections).toBeVisible();
    await expect(page).toHaveURL(/#settings/);
    // The Thread stays mounted under Settings, out of reach.
    await expect(conversation(page)).toBeHidden();
    if (close === 'Escape') await page.keyboard.press('Escape');
    else await page.getByRole('main').getByRole('button', { name: /^Close/ }).first().click();
    await expect(sections).toBeHidden();
    await expect(page).toHaveURL(/#task=/);
    await expect(gear).toBeFocused();
    await expect(fold).toHaveAttribute('aria-expanded', 'true');
    await expect(changes).toBeVisible();
    expect(Math.abs((await scroller.evaluate((el) => el.scrollTop)) - at)).toBeLessThanOrEqual(2);
  }
});

test('Ctrl+B folds the sidebar to the rail, kept per browser, with the same Thread and URL; the rail opens the same things', async ({ page, uam, project }) => {
  const id = await idleThread(uam.api, project, 'Rail kept thread');
  await page.goto(`/#task=${id}`);
  const title = page.getByRole('heading', { level: 1, name: 'Rail kept thread' });
  await expect(title).toBeVisible();
  const rail = page.getByRole('navigation', { name: 'Sidebar' });
  await expect(threads(page).getByRole('searchbox', { name: 'Search threads' })).toBeVisible();

  await page.keyboard.press('Control+b');
  await expect(rail).toBeVisible();
  await expect(threads(page).getByRole('searchbox', { name: 'Search threads' })).toBeHidden();
  await expect(page).toHaveURL(new RegExp(`#task=${id}$`));
  await expect(title).toBeVisible();
  await page.reload();
  await expect(title).toBeVisible();
  await expect(rail).toBeVisible();
  await expect(threads(page).getByRole('searchbox', { name: 'Search threads' })).toBeHidden();

  await rail.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'Settings sections' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(title).toBeVisible();
  await rail.getByRole('button', { name: 'New thread', exact: true }).click();
  const palette = page.getByRole('dialog', { name: 'New thread' });
  await expect(palette.or(page.getByText(/^New thread in /)).first()).toBeVisible();
  await page.keyboard.press('Escape');
  await page.goto(`/#task=${id}`);
  await expect(title).toBeVisible();

  await rail.getByRole('button', { name: /^Show sidebar/ }).click();
  await expect(rail).toBeHidden();
  await expect(threads(page).getByRole('button', { name: / Rail kept thread / })).toHaveAttribute('aria-current', 'true');
  await page.reload();
  await expect(threads(page).getByRole('searchbox', { name: 'Search threads' })).toBeVisible();
  await expect(rail).toHaveCount(0);
});

test('Close conversation asks first, closes the CLI session, and the next message reopens it with the same history', async ({ page, project, model }) => {
  model.title('remember the colour teal', 'Teal thread').reply('remember the colour teal', 'Teal noted.');
  model.reply('which colour was it', 'It was teal.');
  await startThread(page, project, 'remember the colour teal');
  await expect(threadState(page, 'Finished')).toBeVisible();

  await (await menu(page)).getByRole('menuitem', { name: 'Close conversation' }).click();
  const confirm = page.getByRole('alertdialog', { name: 'Close this conversation?' });
  await confirm.getByRole('button', { name: 'Close conversation' }).click();
  await expect(confirm).toBeHidden();
  await expect(threadState(page, 'Idle')).toBeVisible();
  const items = await menu(page);
  await expect(items.getByRole('menuitem', { name: 'Rename' })).toBeVisible();
  await expect(items.getByRole('menuitem', { name: 'Close conversation' })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(items).toBeHidden();

  await send(page, 'which colour was it');
  await expect(conversation(page).getByText('It was teal.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  const next = model.served().find((s) => s.fixture === 'reply which colour was it')!.request;
  expect(next.messages.some((m) => m.role === 'user' && JSON.stringify(m.content).includes('remember the colour teal'))).toBe(true);
  await expect((await menu(page)).getByRole('menuitem', { name: 'Close conversation' })).toBeVisible();
  await page.keyboard.press('Escape');
});

