import { expect, test } from './harness';

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
