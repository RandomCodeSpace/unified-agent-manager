// Background AI: the Utility model that titles Threads, its daily limit and its log.
import { conversation, expect, newThread, openHome, send, startThread, test, threadState } from './harness';
import { keepSettings, openSettings, readSettings, threadId } from './settings-helpers';

type UtilityCall = { purpose: string; task_id?: string; outcome?: string; reason?: string; model?: string };
type UtilityLog = { calls: UtilityCall[] };

const callsFor = async (uam: { api: (m: string, r: string) => Promise<unknown> }, id: string) =>
  ((await uam.api('GET', '/api/utility')) as UtilityLog).calls.filter((c) => c.task_id === id);

test('with no priced model the Thread\'s own model writes its title; Utility model None keeps the first message and asks nothing', async ({ page, uam, project, model }) => {
  const restore = await keepSettings(uam, ['title_model', 'suggest_replies']);
  try {
    await uam.api('PATCH', '/api/settings', { title_model: { copilot: '' }, suggest_replies: false });
    const utility = page.getByRole('combobox', { name: 'GitHub Copilot utility model' });
    await openSettings(page, 'Background AI');
    await expect(utility).toHaveText('Cheapest (none priced)');

    model.reply('utility model titles this thread', 'Titled by the utility model.').title('utility model titles this thread', 'Utility default title');
    await startThread(page, project, 'utility model titles this thread');
    await expect(page.getByRole('heading', { level: 1, name: 'Utility default title' })).toBeVisible();
    await expect(page.getByRole('list', { name: 'Unsettled threads' }).getByRole('listitem').filter({ hasText: 'Utility default title' })).toHaveCount(1);
    const titled = model.served().find((s) => s.fixture === 'title utility model titles this thread')!;
    expect(titled.request.model).toBe('mock-1');
    expect(titled.request.tools ?? []).toEqual([]);

    await openSettings(page, 'Background AI');
    await utility.click();
    await page.getByRole('option', { name: 'None (no utility AI)' }).click();
    await expect(utility).toHaveText('None (no utility AI)');
    await expect.poll(async () => (await readSettings(uam)).title_model?.copilot).toBe('none');
    await page.reload();
    await openSettings(page, 'Background AI');
    await expect(utility).toHaveText('None (no utility AI)');

    model.reply('utility none keeps this message', 'No title asked for.').title('utility none keeps this message', 'A title nobody asked for');
    await openHome(page);
    await newThread(page, project);
    await send(page, 'utility none keeps this message');
    await expect(conversation(page).getByText('No title asked for.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    await page.reload();
    await expect(page.getByRole('heading', { level: 1, name: 'utility none keeps this message' })).toBeVisible();
    expect(await callsFor(uam, threadId(page))).toEqual([]);
    expect(model.served().map((s) => s.fixture)).not.toContain('title utility none keeps this message');
  } finally {
    await restore();
  }
});

test('Background AI logs a title call with its Thread; a daily limit of 0 skips the next and logs why; No daily limit keeps the limit', async ({ page, uam, project, model }) => {
  const restore = await keepSettings(uam, ['title_model', 'suggest_replies', 'utility_daily_limit', 'utility_unlimited']);
  try {
    await uam.api('PATCH', '/api/settings', { title_model: { copilot: '' }, suggest_replies: false });
    model.reply('background log records this title', 'Reply before the logged title.').title('background log records this title', 'Logged title thread');
    const first = await startThread(page, project, 'background log records this title');
    await expect(page.getByRole('heading', { level: 1, name: 'Logged title thread' })).toBeVisible();
    const firstId = threadId(page);
    await expect.poll(async () => (await callsFor(uam, firstId)).map((c) => [c.purpose, c.outcome ?? ''])).toEqual([['title', 'ok']]);

    await openSettings(page, 'Background AI');
    await page.getByRole('button', { name: 'Show log' }).click();
    const entry = page.locator('#utility-log').getByRole('listitem').filter({ has: page.getByRole('button', { name: 'Logged title thread' }) });
    await expect(entry).toHaveCount(1);
    await expect(entry).toContainText('Thread title');
    // The model, the sizes and the tokens the provider reported (no ≈).
    await expect(entry).toContainText(/mock\/mock-1 · \d+ → 19 characters · [\d.,K]+ in, \d+ out tokens/);
    await expect(entry).not.toContainText(/Skipped|Failed/);
    await entry.getByRole('button', { name: 'Logged title thread' }).click();
    await expect(page).toHaveURL(first);

    await openSettings(page, 'Background AI');
    const limit = page.getByRole('form', { name: 'Daily limit' });
    await limit.getByRole('spinbutton', { name: 'Daily limit (calls)' }).fill('0');
    await limit.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByRole('main').getByRole('status').filter({ hasText: 'Background AI is off.' })).toBeVisible();

    model.reply('background off keeps this message', 'Reply with Background AI off.').title('background off keeps this message', 'A title past the limit');
    await openHome(page);
    await newThread(page, project);
    await send(page, 'background off keeps this message');
    await expect(threadState(page, 'Finished')).toBeVisible();
    const offId = threadId(page);
    await expect.poll(async () => (await callsFor(uam, offId)).map((c) => [c.purpose, c.outcome, c.reason])).toEqual([['title', 'skipped', 'off']]);
    await expect(page.getByRole('heading', { level: 1, name: 'background off keeps this message' })).toBeVisible();
    expect(model.served().map((s) => s.fixture)).not.toContain('title background off keeps this message');

    await openSettings(page, 'Background AI');
    await page.getByRole('button', { name: 'Show log' }).click();
    const skipped = page.locator('#utility-log').getByRole('listitem').filter({ has: page.getByRole('button', { name: 'background off keeps this message' }) });
    await expect(skipped).toContainText('Skipped: Background AI off');
    await page.getByRole('switch', { name: 'No daily limit' }).click();
    await expect(page.getByRole('switch', { name: 'No daily limit' })).toBeChecked();
    await expect.poll(() => readSettings(uam)).toMatchObject({ utility_unlimited: true, utility_daily_limit: 0 });
    await expect(page.getByText('calls today · no daily limit')).toBeVisible();
  } finally {
    await restore();
  }
});
