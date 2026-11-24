import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { expect, test } from './harness';
import { ownService } from './shell-helpers';

test('a wrong token is refused; the real token signs in and the session survives a reload', async ({ browser, uam }) => {
  const context = await browser.newContext({ baseURL: uam.url, viewport: { width: 1280, height: 900 } });
  try {
    const page = await context.newPage();
    await page.goto('/');
    const signIn = page.getByRole('heading', { name: 'Sign in to UAM' });
    const token = page.getByRole('textbox', { name: 'Access token' });
    await expect(signIn).toBeVisible();

    await token.fill('wrong-token-0000000000000000');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('alert')).toHaveText('That token was not accepted.');
    await expect(signIn).toBeVisible();
    expect(await context.cookies()).toEqual([]);

    await token.fill(uam.token);
    await page.getByRole('button', { name: 'Sign in' }).click();
    const home = page.getByRole('region', { name: 'Home' });
    await expect(home).toBeVisible();
    await expect(signIn).toBeHidden();
    const cookies = await context.cookies();
    expect(cookies).toHaveLength(1);
    expect(cookies[0].httpOnly).toBe(true);

    await page.reload();
    await expect(home).toBeVisible();
    await expect(signIn).toBeHidden();
  } finally {
    await context.close();
  }
});

test('Log out ends the session on the server, a new token ends the others, and sign-in is rate limited', async ({ browser, uam, workdir }) => {
  const own = await ownService(browser, uam.mock.url, workdir);
  const other = await browser.newContext({ baseURL: own.url, viewport: { width: 1280, height: 900 } });
  try {
    const tokenFile = path.join(own.root, 'config', 'web-token');
    expect((await other.request.post('/api/login', { data: { token: fs.readFileSync(tokenFile, 'utf8').trim() } })).status()).toBe(204);
    const { page } = own;
    const signIn = (p: Page) => p.getByRole('heading', { name: 'Sign in to UAM' });
    // The cookie as a client that kept a copy would replay it.
    const cookie = (await own.context.cookies()).map((c) => `${c.name}=${c.value}`).join('; ');
    const replay = async () => (await fetch(`${own.url}/api/sessions`, { headers: { Cookie: cookie } })).status;
    expect(await replay()).toBe(200);

    await page.goto('/#settings');
    await page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'UAM and access', exact: true }).click();
    await page.getByRole('button', { name: 'Log out' }).click();
    await expect(signIn(page)).toBeVisible();
    await expect(page).toHaveTitle('UAM');
    expect(await replay()).toBe(401);
    await own.restart();
    expect(await replay()).toBe(401);
    expect((await other.request.get('/api/sessions')).status()).toBe(200);

    // A new token and a restart end every session, also one with a view open.
    const view = await other.newPage();
    await view.goto('/');
    await expect(view.getByRole('region', { name: 'Home' })).toBeVisible();
    const replaced = 'e2e-replaced-token-0123456789abcdef';
    fs.writeFileSync(tokenFile, `${replaced}\n`);
    await own.restart();
    await expect(signIn(view)).toBeVisible({ timeout: 30_000 });
    await expect(view).toHaveTitle('UAM');

    // Ten wrong tokens are checked and refused; the eleventh attempt is not checked at all.
    const field = view.getByRole('textbox', { name: 'Access token' });
    for (let i = 0; i < 10; i++) {
      await field.fill(`wrong-token-${i}-0000000000000000`);
      const answer = view.waitForResponse((r) => r.url().endsWith('/api/login'));
      await view.getByRole('button', { name: 'Sign in' }).click();
      expect((await answer).status()).toBe(401);
      await expect(view.getByRole('alert')).toHaveText('That token was not accepted.');
    }
    await field.fill(replaced);
    await view.getByRole('button', { name: 'Sign in' }).click();
    await expect(view.getByRole('alert')).toHaveText('Too many sign-in attempts. Wait a few minutes, then try again.');
    await expect(signIn(view)).toBeVisible();
  } finally {
    await other.close();
    await own.stop();
  }
});
