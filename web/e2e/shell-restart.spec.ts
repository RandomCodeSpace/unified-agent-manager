import { conversation, expect, newThread, send, test, threadState } from './harness';
import { ownService } from './shell-helpers';

const LOST = 'Connection lost, reconnecting. Work continues on the server.';

test('a service restart reconnects the open page and resends nothing', async ({ browser, uam, workdir, model }) => {
  model.reply('ping before restart', 'pong before restart');
  model.reply('again after restart', 'ok after restart');
  const own = await ownService(browser, uam.mock.url, workdir);
  try {
    const { page } = own;
    await page.goto('/');
    await expect(page.getByRole('region', { name: 'Home' })).toBeVisible();
    await newThread(page, own.project);
    await send(page, 'ping before restart');
    await expect(conversation(page).getByText('pong before restart')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    const url = page.url();
    const served = model.served().length;

    const status = page.getByRole('status').filter({ hasText: LOST });
    const restarted = own.restart();
    await expect(status).toBeAttached();
    await restarted;
    await expect(status).toHaveCount(0, { timeout: 30_000 });
    await expect(page.getByRole('status').filter({ hasText: /^Connected$/ })).toBeAttached();

    await expect(page).toHaveURL(url);
    await expect(page.getByRole('heading', { name: 'Sign in to UAM' })).toBeHidden();
    await expect(conversation(page).getByText('ping before restart')).toBeVisible();
    await expect(conversation(page).getByText('pong before restart')).toBeVisible();
    expect(model.served()).toHaveLength(served);

    await send(page, 'again after restart');
    await expect(conversation(page).getByText('ok after restart')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
  } finally {
    await own.stop();
  }
});

test('a turn running when the service restarts reads Interrupted and is not resent', async ({ browser, uam, workdir, model }) => {
  // Streamed in full, the reply would take about a minute.
  model.reply('slow turn cut by restart', Array.from({ length: 60 }, (_, i) => `cut line ${i + 1}`).join('\n\n'), { latency: 250, chunkSize: 4 });
  model.reply('continue after the interruption', 'Done after the interruption.');
  const own = await ownService(browser, uam.mock.url, workdir);
  try {
    const { page } = own;
    await page.goto('/');
    await expect(page.getByRole('region', { name: 'Home' })).toBeVisible();
    await newThread(page, own.project);
    await send(page, 'slow turn cut by restart');
    await expect(conversation(page).getByText('cut line 1', { exact: true })).toBeVisible();
    const row = page.getByRole('list', { name: 'Unsettled threads' }).getByRole('button', { name: new RegExp(`^${own.project.name} Working `) });
    await expect(row).toBeVisible();

    await own.restart();
    await expect(threadState(page, 'Interrupted')).toBeVisible({ timeout: 30_000 });
    await expect(page.getByRole('list', { name: 'Unsettled threads' }).getByRole('button', { name: new RegExp(`^${own.project.name} Interrupted `) })).toBeVisible();
    expect(model.served().filter((s) => s.fixture === 'reply slow turn cut by restart')).toHaveLength(1);

    await send(page, 'continue after the interruption');
    await expect(conversation(page).getByText('Done after the interruption.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    expect(model.served().filter((s) => s.fixture === 'reply slow turn cut by restart')).toHaveLength(1);
  } finally {
    await own.stop();
  }
});
