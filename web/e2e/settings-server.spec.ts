// Settings about the server itself: the Copilot CLI card, the UAM version, session stats and connected instances.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { binary, conversation, expect, startService, startThread, stopService, test, threadState } from './harness';
import { binaryWith, openSettings, quietBackground, threadId, tlsFront } from './settings-helpers';
import { ownService } from './shell-helpers';

type Api = { api: (method: string, route: string, body?: unknown) => Promise<unknown> };

test('the Copilot CLI card shows the installed version, then a newer release with its update, or why the check failed', async ({ page, uam }) => {
  const real = (await uam.api('GET', '/api/providers/copilot/cli')) as { installed: string };
  expect(real.installed).toMatch(/^\d+\.\d+\.\d+/);
  await openSettings(page, 'GitHub Copilot');
  const card = page.getByRole('group', { name: 'Copilot CLI' });
  await expect(card.getByText(real.installed, { exact: true })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Check again' })).toBeEnabled();

  // What the release check answers depends on the network: the next two reads are answered here.
  const checked_at = new Date().toISOString();
  let cli: Record<string, unknown> = { installed: real.installed, latest: '9.9.9', update_available: true, state: 'idle', checked_at };
  await page.route(/\/api\/providers\/copilot\/cli(\?|$)/, (route) => route.fulfill({ json: cli }));
  await card.getByRole('button', { name: 'Check again' }).click();
  await expect(card.getByRole('status').first()).toHaveText(/^9\.9\.9 available · Checked /);
  await expect(card.getByText(real.installed, { exact: true })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Update to 9.9.9' })).toBeVisible();

  cli = { installed: real.installed, latest: real.installed, update_available: false, state: 'idle', checked_at, check_error: 'the registry answered 503 Service Unavailable' };
  await card.getByRole('button', { name: 'Check again' }).click();
  await expect(card.getByText('Could not check for a newer release: the registry answered 503 Service Unavailable', { exact: true })).toBeVisible();
  await expect(card.getByRole('button', { name: /^Update to/ })).toHaveCount(0);
  await expect(card.getByRole('alert')).toHaveCount(0);
});

test('UAM shows the running version; an installed binary that cannot be read gives the reason and no Restart; another version installed there is named, and Restart runs it in place', async ({ browser }) => {
  // A service of the test's own, started from a copy of the binary so the copy can be replaced, on a port it gets
  // again after its restart.
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'uam-e2e-bin-'));
  const bin = path.join(root, 'uam');
  fs.copyFileSync(binary(), bin);
  fs.chmodSync(bin, 0o755);
  const running = execFileSync(bin, ['version'], { encoding: 'utf8' }).trim();
  // Installs a program at the binary's path the way an installer does: a new file renamed over it.
  const install = (script: string) => {
    fs.writeFileSync(`${bin}.new`, script, { mode: 0o755 });
    fs.renameSync(`${bin}.new`, bin);
  };
  const port = await new Promise<number>((resolve) => {
    const probe = net.createServer().listen(0, '127.0.0.1', () => {
      const { port } = probe.address() as net.AddressInfo;
      probe.close(() => resolve(port));
    });
  });
  const { proc, url } = await startService(bin, root, `127.0.0.1:${port}`);
  const context = await browser.newContext({ baseURL: url, viewport: { width: 1280, height: 900 } });
  try {
    const token = fs.readFileSync(path.join(root, 'config', 'web-token'), 'utf8').trim();
    expect((await context.request.post(`${url}/api/login`, { data: { token } })).status()).toBe(204);
    const page = await context.newPage();
    const rail = page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'UAM and access', exact: true });
    const gear = page.getByRole('complementary').getByRole('button', { name: 'Settings', exact: true });
    // The service looks at the file when Settings reads the status, at most every 5 s, and runs its `version`.
    const status = async () => (await context.request.get(`${url}/api/service`)).json() as Promise<{ installed: string; restart: string; error?: string }>;

    await openSettings(page, 'UAM and access');
    const card = page.getByRole('region', { name: 'UAM', exact: true }).getByRole('group', { name: 'Version' });
    await expect(card.getByText(running, { exact: true })).toBeVisible();
    await expect(card.getByRole('button', { name: 'Restart' })).toHaveCount(0);
    await expect(card.getByText(/is installed/)).toHaveCount(0);
    await expect(rail).toHaveAccessibleDescription('');

    install('#!/bin/sh\nexit 3\n');
    await expect.poll(async () => (await status()).error).toBeTruthy();
    await page.reload();
    await openSettings(page, 'UAM and access');
    await expect(card.getByText(/^Could not read the installed UAM: /)).toBeVisible();
    await expect(card.getByRole('button', { name: 'Restart' })).toHaveCount(0);
    await expect(card.getByText(/is installed/)).toHaveCount(0);
    await expect(card.getByText(running, { exact: true })).toBeVisible();

    // The other version: it reports its own version and is otherwise the same program.
    fs.copyFileSync(binary(), `${bin}.next`);
    fs.chmodSync(`${bin}.next`, 0o755);
    install(`#!/bin/sh\nif [ "$1" = version ]; then echo v9.9.9-pack; exit 0; fi\nexec "${bin}.next" "$@"\n`);
    await expect.poll(async () => (await status()).installed).toBe('v9.9.9-pack');
    await page.reload();
    await openSettings(page, 'UAM and access');
    await expect(card.getByText(`UAM v9.9.9-pack is installed (running ${running}).`, { exact: true })).toBeVisible();
    await expect(card.getByText(running, { exact: true })).toBeVisible();
    await expect(rail).toHaveAccessibleDescription('Restart available');
    await expect(gear).toHaveAccessibleDescription(/UAM v9\.9\.9-pack installed; restart to run it/);
    expect(await status()).toEqual({ running, installed: 'v9.9.9-pack', restart: 'available' });

    // No Thread works or waits, so Restart runs the installed file at once, in the same process.
    await card.getByRole('button', { name: 'Restart' }).click();
    const log = () => fs.readFileSync(path.join(root, 'cache', 'uam.log'), 'utf8');
    await expect.poll(log).toContain('uam web restarting');
    await expect.poll(async () => (await context.request.get(`${url}/api/service`).catch(() => null))?.status() ?? 0, { timeout: 30_000 }).toBe(200);
    expect(proc.exitCode).toBeNull();
    // The page is still signed in and reads the restarted service: it runs what is installed, nothing is left to restart.
    await page.reload();
    await openSettings(page, 'UAM and access');
    await expect(card.getByText(running, { exact: true })).toBeVisible();
    await expect(card.getByRole('button', { name: 'Restart' })).toHaveCount(0);
    await expect(card.getByText(/is installed/)).toHaveCount(0);
    await expect(page.getByRole('heading', { name: 'Sign in to UAM' })).toBeHidden();
    expect((await status()).restart).toBe('none');
  } finally {
    await context.close();
    await stopService(proc);
    fs.rmSync(root, { recursive: true, force: true, maxRetries: 3 });
  }
});

test.describe('session stats', () => {
  test.use({ permissions: ['clipboard-read', 'clipboard-write'] });

  const prompts = async (uam: Api) => Number(((await uam.api('GET', '/api/session-stats')) as { stats: { key: string; value: string }[] }).stats.find((s) => s.key === 'work.prompts')!.value);

  test('Session stats counts the journals by group, writes the counts to its file, counts again on Scan again and copies the lines', async ({ page, uam, project, model }) => {
    const quiet = await quietBackground(uam);
    try {
      model.reply('first counted prompt', 'Counted once.');
      model.reply('second counted prompt', 'Counted twice.');
      const before = await prompts(uam);
      await startThread(page, project, 'first counted prompt');
      await expect(conversation(page).getByText('Counted once.')).toBeVisible();
      await expect(threadState(page, 'Finished')).toBeVisible();
      const thread = threadId(page);
      await expect.poll(() => prompts(uam)).toBe(before + 1);

      await openSettings(page, 'Session stats');
      const section = page.getByRole('region', { name: 'Session stats', exact: true });
      const file = path.join(uam.home, '..', 'config', 'session-stats.txt');
      await expect(section.getByText(path.resolve(file), { exact: true })).toBeVisible();
      await section.getByRole('radiogroup', { name: 'View' }).getByRole('radio', { name: 'Every counter' }).click();
      // One section per key prefix; a count is the value beside its name.
      for (const group of ['sessions', 'work', 'friction', 'approvals', 'turns', 'tokens']) await expect(section.getByRole('region', { name: group, exact: true })).toBeVisible();
      const counted = section.getByRole('region', { name: 'work', exact: true }).getByText('prompts', { exact: true }).locator('xpath=following-sibling::*[1]');
      await expect(counted).toHaveText(String(before + 1));
      expect(fs.readFileSync(file, 'utf8')).toContain(`\nwork.prompts=${before + 1}\n`);

      // Another prompt is counted only once the page scans again.
      await uam.api('POST', `/api/sessions/${thread}/prompt`, { text: 'second counted prompt', request_id: crypto.randomUUID() });
      await expect.poll(() => prompts(uam)).toBe(before + 2);
      await expect(counted).toHaveText(String(before + 1));
      await section.getByRole('button', { name: 'Scan again' }).click();
      await expect(counted).toHaveText(String(before + 2));
      expect(fs.readFileSync(file, 'utf8')).toContain(`\nwork.prompts=${before + 2}\n`);

      await section.getByRole('button', { name: 'Copy', exact: true }).click();
      await expect(section.getByRole('button', { name: 'Copied' })).toBeVisible();
      const copied = await page.evaluate(() => navigator.clipboard.readText());
      expect(copied).toMatch(/^scan\.at=\d{4}-\d{2}-\d{2}T/);
      expect(copied).toContain(`\nwork.prompts=${before + 2}\n`);
      expect(copied).toContain('\nsessions.total=');
    } finally {
      await quiet();
    }
  });
});

test('Connected instances: a pairing that fails says why, clears the access key and saves nothing; a private address needs its switch', async ({ page, uam }) => {
  const listed = async () => ((await uam.api('GET', '/api/connections')) as { connections: unknown[] }).connections;
  expect(await listed()).toEqual([]);
  await openSettings(page, 'Connected instances');
  const section = page.getByRole('region', { name: 'Connected instances', exact: true });
  await expect(section.getByText('No connected instances yet. Your local threads are available as usual.')).toBeVisible();
  await section.getByRole('button', { name: 'Add instance' }).click();
  const form = section.getByRole('form', { name: 'Add an instance' });
  const connect = form.getByRole('button', { name: 'Connect instance' });
  const key = form.getByRole('textbox', { name: 'Access key' });
  const failure = section.getByRole('alert').filter({ hasText: 'Could not update connected instances:' });
  await expect(connect).toBeDisabled();
  await form.getByRole('textbox', { name: 'Name' }).fill('Pack peer');
  // Nothing listens there.
  await form.getByRole('textbox', { name: 'Web URL' }).fill('https://127.0.0.1:1');
  await key.fill(uam.token);
  await expect(form.getByRole('switch', { name: 'Allow a private network address' })).not.toBeChecked();
  await connect.click();
  // A loopback or private address is dialled only once the switch allows it.
  await expect(failure).toHaveText('Could not update connected instances: this instance uses a private address; explicitly allow this destination');
  await expect(key).toHaveValue('');
  await expect(form.getByRole('textbox', { name: 'Name' })).toHaveValue('Pack peer');
  await expect(form.getByRole('textbox', { name: 'Web URL' })).toHaveValue('https://127.0.0.1:1');
  expect(await listed()).toEqual([]);

  await form.getByRole('switch', { name: 'Allow a private network address' }).click();
  await key.fill(uam.token);
  await connect.click();
  await expect(failure).toHaveText('Could not update connected instances: could not securely connect to the target instance');
  await expect(connect).toBeDisabled();
  await expect(key).toHaveValue('');
  expect(await listed()).toEqual([]);
  await form.getByRole('button', { name: 'Cancel' }).click();
  await expect(form).toHaveCount(0);
  await page.reload();
  await openSettings(page, 'Connected instances');
  await expect(section.getByText('No connected instances yet. Your local threads are available as usual.')).toBeVisible();
});

test('Connected instances: a pairing over HTTPS lists the instance as Connected; it is disabled, enabled, renamed and removed', async ({ browser, uam, workdir }) => {
  // This instance trusts the certificate of an HTTPS front on the worker's service, which it pairs with.
  const front = await tlsFront(uam.url);
  const wrapped = binaryWith({ SSL_CERT_FILE: front.cert });
  const own = await ownService(browser, uam.mock.url, workdir, wrapped.bin);
  try {
    const { page } = own;
    const listed = async () => ((await (await own.context.request.get(`${own.url}/api/connections`)).json()) as { connections: Record<string, unknown>[] }).connections;
    await openSettings(page, 'Connected instances');
    const section = page.getByRole('region', { name: 'Connected instances', exact: true });
    await section.getByRole('button', { name: 'Add instance' }).click();
    const form = section.getByRole('form', { name: 'Add an instance' });
    await form.getByRole('textbox', { name: 'Name' }).fill('Pack peer');
    await form.getByRole('textbox', { name: 'Web URL' }).fill(front.url);
    await form.getByRole('textbox', { name: 'Access key' }).fill(uam.token);
    await form.getByRole('switch', { name: 'Allow a private network address' }).click();
    await form.getByRole('button', { name: 'Connect instance' }).click();
    await expect(form).toHaveCount(0);
    const card = section.getByRole('region', { name: 'Pack peer', exact: true });
    await expect(card.getByText(front.url, { exact: true })).toBeVisible();
    await expect(card.getByRole('status')).toHaveText('Connected');
    const peer = card.getByRole('switch', { name: 'Enable Pack peer' });
    await expect(peer).toBeChecked();
    expect(await listed()).toMatchObject([{ label: 'Pack peer', base_url: front.url, enabled: true, allow_private: true }]);
    // The access key was spent on the pairing: the saved record holds none.
    expect(JSON.stringify(await listed())).not.toContain(uam.token);

    await peer.click();
    await expect(peer).not.toBeChecked();
    await expect(card.getByRole('status')).toHaveText('Disabled');
    await expect.poll(async () => (await listed())[0].enabled).toBe(false);
    await peer.click();
    await expect(peer).toBeChecked();
    await expect(card.getByRole('status')).toHaveText('Connected');
    await expect.poll(async () => (await listed())[0].enabled).toBe(true);

    await card.getByRole('button', { name: 'Edit connection' }).click();
    const edit = section.getByRole('form', { name: 'Edit Pack peer' });
    await expect(edit.getByRole('textbox', { name: 'Web URL' })).toHaveValue(front.url);
    await expect(edit.getByRole('textbox', { name: 'New access key (optional)' })).toHaveValue('');
    await edit.getByRole('textbox', { name: 'Name' }).fill('Pack peer renamed');
    await edit.getByRole('button', { name: 'Save connection' }).click();
    const renamed = section.getByRole('region', { name: 'Pack peer renamed', exact: true });
    await expect(renamed.getByRole('status')).toHaveText('Connected');
    await expect(card).toHaveCount(0);
    expect(await listed()).toMatchObject([{ label: 'Pack peer renamed', base_url: front.url, enabled: true }]);
    await page.reload();
    await openSettings(page, 'Connected instances');
    await expect(renamed.getByRole('status')).toHaveText('Connected');

    await renamed.getByRole('button', { name: 'Remove connection' }).click();
    const confirm = page.getByRole('alertdialog', { name: 'Remove Pack peer renamed?' });
    await expect(confirm).toContainText('Threads and files on the other instance are not deleted.');
    await confirm.getByRole('button', { name: 'Remove connection' }).click();
    await expect(renamed).toHaveCount(0);
    await expect(section.getByText('No connected instances yet. Your local threads are available as usual.')).toBeVisible();
    expect(await listed()).toEqual([]);
  } finally {
    await own.stop();
    wrapped.remove();
    await front.close();
  }
});
