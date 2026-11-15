import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { binary, expect, startService, stopService, test } from './harness';

const DRAFT_KEY = 'uam.draft.thread-from-before';

test('a state file that cannot be read at start is reported and the drafts stay', async ({ browser }) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'uam-e2e-set-aside-'));
  const config = path.join(root, 'config');
  fs.mkdirSync(config, { recursive: true, mode: 0o700 });
  fs.writeFileSync(path.join(config, 'sessions.json'), '{"sessions": ');
  const { proc, url } = await startService(binary(), root);
  const context = await browser.newContext({ baseURL: url, viewport: { width: 1280, height: 900 } });
  try {
    const token = fs.readFileSync(path.join(config, 'web-token'), 'utf8').trim();
    expect((await context.request.post(`${url}/api/login`, { data: { token } })).status()).toBe(204);
    // The draft is in storage before the page reads its first snapshot, once.
    await context.addInitScript((key) => {
      if (sessionStorage.getItem('seeded')) return;
      localStorage.setItem(key, JSON.stringify({ text: 'unsent words' }));
      sessionStorage.setItem('seeded', '1');
    }, DRAFT_KEY);
    const page = await context.newPage();
    await page.goto('/');
    const backups = fs.readdirSync(config).filter((f) => f.startsWith('sessions.json.bak.'));
    expect(backups).toHaveLength(1);
    const notice = page.getByRole('status').filter({ hasText: 'was set aside' });
    await expect(notice).toContainText(path.join(config, backups[0]));
    await expect(notice).toContainText('restart uam');
    await expect(notice).not.toContainText('UAM tries again');
    await page.reload();
    await expect(notice).toBeVisible();
    expect(await page.evaluate((key) => localStorage.getItem(key), DRAFT_KEY)).toContain('unsent words');
    expect(fs.existsSync(path.join(config, 'sessions.json'))).toBe(false);
  } finally {
    await context.close();
    await stopService(proc);
    fs.rmSync(root, { recursive: true, force: true, maxRetries: 3 });
  }
});
