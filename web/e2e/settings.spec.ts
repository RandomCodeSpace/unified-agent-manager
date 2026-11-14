import { conversation, expect, newThread, send, test, threadState } from './harness';

test('a custom provider added in Settings with Load models runs a Thread and survives a reload', async ({ page, uam, project, model }) => {
  // Each run adds its own provider: the service is shared by the tests of a worker.
  const name = `ui${Math.random().toString(36).slice(2, 8)}`;
  model.on({ userMessage: 'use the new provider', model: 'gpt-4o' }, { content: 'Reply from the provider added in Settings.' });

  await page.goto('/#settings');
  await page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'Custom providers' }).click();
  const section = page.getByRole('region', { name: 'Custom providers' });
  await section.getByRole('button', { name: 'Add provider' }).click();
  const form = section.getByRole('form', { name: 'Add a provider' });
  await form.getByRole('textbox', { name: 'Provider name' }).fill(name);
  await form.getByRole('textbox', { name: 'Base URL' }).fill(`${uam.mock.url}/v1`);
  await form.getByRole('textbox', { name: 'API key' }).fill('ui-key');
  await form.getByRole('button', { name: 'Load models' }).click();
  // aimock's /v1/models lists its default models.
  const offered = form.getByRole('group', { name: 'Models to offer' });
  await offered.getByRole('checkbox', { name: 'gpt-4o', exact: true }).check();
  await form.getByRole('button', { name: 'Save provider' }).click();
  await expect(form).toBeHidden();
  const saved = section.getByRole('region', { name: `${name} models` });
  await expect(saved).toContainText(`${uam.mock.url}/v1`);
  await expect(saved.getByText('API key saved')).toBeVisible();
  await expect(saved.getByRole('button', { name: `Remove ${name}/gpt-4o` })).toBeVisible();
  await page.getByRole('button', { name: 'Close settings' }).click();

  await newThread(page, project);
  await page.getByRole('button', { name: /^Model:/ }).click();
  await page.getByRole('menu').getByRole('group', { name }).getByRole('menuitemradio', { name: 'gpt-4o' }).click();
  await expect(page.getByRole('button', { name: 'Model: gpt-4o' })).toBeVisible();
  await send(page, 'use the new provider');
  await expect(conversation(page).getByText('Reply from the provider added in Settings.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(model.served().map((s) => s.request.model)).toEqual(['gpt-4o']);

  await page.goto('/#settings');
  await page.reload();
  await page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'Custom providers' }).click();
  await expect(section.getByRole('region', { name: `${name} models` }).getByText('API key saved')).toBeVisible();
});
