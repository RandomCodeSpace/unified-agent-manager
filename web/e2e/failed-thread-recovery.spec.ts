import { conversation, expect, startThread, test, threadState } from './harness';

test('a credentials error shows one sentence, folds detail, and opens Settings at the account section', async ({ page, project, model }) => {
  model.on(
    { userMessage: 'check credentials' },
    { status: 401, error: { message: 'Bad credentials', type: 'authentication_error' } },
    {},
    'fail 401 credentials',
  );
  await startThread(page, project, 'check credentials');
  await expect(threadState(page, 'Error')).toBeVisible({ timeout: 45_000 });
  await expect(conversation(page).getByRole('alert')).toHaveText('Sign in to your account in Settings, then send your message again.');
  await expect(conversation(page).getByText(/HTTP 401/)).toBeHidden();
  await conversation(page).getByText('Details', { exact: true }).click();
  await expect(conversation(page).getByText(/HTTP 401/)).toBeVisible();

  const signIn = page.getByRole('button', { name: 'Sign in in Settings' });
  const resend = page.getByRole('button', { name: 'Resend last prompt' });
  await expect(signIn).toBeVisible();
  await expect(resend).toBeVisible();
  const [signInBox, resendBox] = await Promise.all([signIn.boundingBox(), resend.boundingBox()]);
  expect(signInBox!.x).toBeLessThan(resendBox!.x);

  await signIn.click();
  await expect(page.getByRole('heading', { name: 'GitHub Copilot' })).toBeVisible();
});
