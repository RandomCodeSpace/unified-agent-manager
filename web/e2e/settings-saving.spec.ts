// Service settings: saved at once, still there after a reload, refused with the reason.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, newThread, openHome, send, test, threadState } from './harness';
import { keepSettings, openSettings, readSettings } from './settings-helpers';

test('Conversation, New threads, Background AI and Terminal settings are saved and still there after a reload', async ({ page, uam }) => {
  const restore = await keepSettings(uam, ['send_default', 'suggest_replies', 'thread_messages', 'compact_threshold', 'utility_daily_limit', 'terminal']);
  try {
    const composer = page.getByRole('region', { name: 'Composer' });
    const threads = page.getByRole('region', { name: 'New threads' });
    const threshold = threads.getByRole('combobox', { name: 'Compact the conversation when its context reaches' });
    const limit = page.getByRole('form', { name: 'Daily limit' }).getByRole('spinbutton', { name: 'Daily limit (calls)' });
    const terminal = page.getByRole('region', { name: 'Access' }).getByRole('switch', { name: 'Terminal' });

    await openSettings(page, 'Conversation');
    await composer.getByRole('radio', { name: 'After this turn' }).click();
    await expect(composer.getByRole('radio', { name: 'After this turn' })).toBeChecked();
    await composer.getByRole('switch', { name: 'Suggest replies' }).click();
    await expect(composer.getByRole('switch', { name: 'Suggest replies' })).not.toBeChecked();
    await composer.getByRole('switch', { name: 'Threads can message each other' }).click();
    await expect(composer.getByRole('switch', { name: 'Threads can message each other' })).toBeChecked();

    await openSettings(page, 'New threads');
    await threshold.click();
    await page.getByRole('option', { name: '60%', exact: true }).click();
    await expect(threshold).toHaveText('60%');

    await openSettings(page, 'Background AI');
    await limit.fill('50');
    await page.getByRole('form', { name: 'Daily limit' }).getByRole('button', { name: 'Save' }).click();
    await expect(page.getByRole('meter', { name: 'Background AI calls today' })).toHaveAttribute('aria-valuemax', '50');

    await openSettings(page, 'UAM and access');
    await terminal.click();
    await expect(terminal).toBeChecked();

    await expect.poll(() => readSettings(uam)).toMatchObject({ send_default: 'queue', suggest_replies: false, thread_messages: true, compact_threshold: 60, utility_daily_limit: 50, terminal: true });

    await page.reload();
    await openSettings(page, 'Conversation');
    await expect(composer.getByRole('radio', { name: 'After this turn' })).toBeChecked();
    await expect(composer.getByRole('switch', { name: 'Suggest replies' })).not.toBeChecked();
    await expect(composer.getByRole('switch', { name: 'Threads can message each other' })).toBeChecked();
    await openSettings(page, 'New threads');
    await expect(threshold).toHaveText('60%');
    await openSettings(page, 'Background AI');
    await expect(limit).toHaveValue('50');
    await openSettings(page, 'UAM and access');
    await expect(terminal).toBeChecked();

    // 80% is the default: choosing it again stores nothing.
    await openSettings(page, 'New threads');
    await threshold.click();
    await page.getByRole('option', { name: '80% (default)' }).click();
    await expect(threshold).toHaveText('80% (default)');
    await expect.poll(async () => 'compact_threshold' in await readSettings(uam)).toBe(false);
  } finally {
    await restore();
  }
});

test('New threads defaults (model and Yolo) are what the next new Thread starts with, after a reload', async ({ page, uam, project, model }) => {
  const restore = await keepSettings(uam, ['task_defaults', 'custom_models']);
  try {
    const mock = (await readSettings(uam)).custom_models!.find((m) => m.model_id === 'mock-1')!;
    // A second model of the mock's provider; the saved key stays with the provider.
    await uam.api('PATCH', '/api/settings', {
      custom_models: [...(await readSettings(uam)).custom_models!.map(({ key_present: _, ...m }) => m), { name: mock.name, display_name: 'Mock Two', base_url: mock.base_url, model_id: 'mock-2', api_key_env: '' }],
    });
    model.on({ userMessage: 'run on the default model', model: 'mock-2' }, { content: 'Reply from the default model.' });

    await openSettings(page, 'New threads');
    const threads = page.getByRole('region', { name: 'New threads' });
    await threads.getByRole('combobox', { name: 'Model', exact: true }).click();
    await page.getByRole('option', { name: 'Mock Two' }).click();
    await expect(threads.getByRole('combobox', { name: 'Model', exact: true })).toHaveText('Mock Two');
    await threads.getByRole('combobox', { name: 'Mode', exact: true }).click();
    await page.getByRole('option', { name: 'Yolo' }).click();
    await expect(threads.getByRole('combobox', { name: 'Mode', exact: true })).toHaveText('Yolo');
    await expect.poll(async () => (await readSettings(uam)).task_defaults).toMatchObject({ provider: 'copilot', model: 'mock/mock-2', mode: 'yolo' });

    await page.reload();
    await openHome(page);
    await newThread(page, project);
    await expect(page.getByRole('button', { name: 'Model: Mock Two' })).toBeVisible();
    await expect(page.getByRole('button', { name: /^Permissions and execution: Yolo/ })).toBeVisible();
    await send(page, 'run on the default model');
    await expect(conversation(page).getByText('Reply from the default model.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    expect(model.served().map((s) => s.request.model)).toEqual(['mock-2']);
  } finally {
    await restore();
  }
});

test('a typed API key is never shown or returned again, is not stored in plain text, and an edit with the key left blank keeps it', async ({ page, uam, project, model }) => {
  const restore = await keepSettings(uam, ['custom_models']);
  try {
    const name = `key${Math.random().toString(36).slice(2, 8)}`;
    const key = `typed-key-${Math.random().toString(36).slice(2, 10)}`;
    model.on({ userMessage: 'answer with the kept key', model: 'keyed-1' }, { content: 'Answered with the kept key.' });
    await openSettings(page, 'Custom providers');
    const section = page.getByRole('region', { name: 'Custom providers' });
    await section.getByRole('button', { name: 'Add provider' }).click();
    const added = section.getByRole('form', { name: 'Add a provider' });
    await added.getByRole('textbox', { name: 'Provider name' }).fill(name);
    await added.getByRole('textbox', { name: 'Base URL' }).fill(`${uam.mock.url}/v1`);
    await added.getByRole('textbox', { name: 'API key' }).fill(key);
    await added.getByRole('textbox', { name: 'Add a model ID the endpoint does not list' }).fill('keyed-1');
    await added.getByRole('button', { name: 'Add ID' }).click();
    await added.getByRole('button', { name: 'Save provider' }).click();
    await expect(added).toBeHidden();
    const saved = section.getByRole('region', { name: `${name} models` });
    await expect(saved.getByText('API key saved')).toBeVisible();

    const entry = (await readSettings(uam)).custom_models!.find((m) => m.name === name)!;
    expect(entry).toMatchObject({ model_id: 'keyed-1', key_present: true });
    expect(entry).not.toHaveProperty('api_key');
    const config = path.join(uam.home, '..', 'config');
    expect(fs.readFileSync(path.join(config, 'sessions.json'), 'utf8')).not.toContain(key);
    expect(fs.statSync(path.join(config, 'byom.key')).mode & 0o777).toBe(0o600);

    await saved.getByRole('button', { name: 'Edit' }).click();
    const edit = section.getByRole('form', { name: `Edit provider ${name}` });
    await expect(edit.getByRole('textbox', { name: 'API key' })).toHaveValue('');
    await expect(edit.getByText('Leave blank to keep the saved key, or enter a new key to replace it.')).toBeVisible();
    await edit.getByRole('button', { name: 'Save provider' }).click();
    await expect(edit).toBeHidden();
    await expect(saved.getByText('API key saved')).toBeVisible();

    await page.getByRole('button', { name: 'Close settings' }).click();
    await newThread(page, project);
    await page.getByRole('button', { name: /^Model:/ }).click();
    await page.getByRole('menu').getByRole('group', { name }).getByRole('menuitemradio', { name: 'keyed-1' }).click();
    await send(page, 'answer with the kept key');
    await expect(conversation(page).getByText('Answered with the kept key.')).toBeVisible();
    // aimock's journal redacts the Authorization header, so the key itself is not compared.
    expect(model.served().map((s) => s.request.model)).toEqual(['keyed-1']);
  } finally {
    await restore();
  }
});

test('Load models says why the endpoint failed, and a provider the service refuses is not saved', async ({ page, uam }) => {
  const restore = await keepSettings(uam, ['custom_models']);
  try {
    await openSettings(page, 'Custom providers');
    const section = page.getByRole('region', { name: 'Custom providers' });
    await section.getByRole('button', { name: 'Add provider' }).click();
    const form = section.getByRole('form', { name: 'Add a provider' });
    await form.getByRole('textbox', { name: 'Provider name' }).fill('bad name');
    // aimock serves no models list under this path.
    await form.getByRole('textbox', { name: 'Base URL' }).fill(`${uam.mock.url}/missing`);
    await form.getByRole('textbox', { name: 'API key' }).fill('ui-key');
    await form.getByRole('button', { name: 'Load models' }).click();
    await expect(form.getByRole('alert')).toHaveText(/^Could not load the models: .*404/);
    await expect(form.getByRole('group', { name: 'Models to offer' })).toBeHidden();

    await form.getByRole('textbox', { name: 'Add a model ID the endpoint does not list' }).fill('typed-1');
    await form.getByRole('button', { name: 'Add ID' }).click();
    await form.getByRole('button', { name: 'Save provider' }).click();
    await expect(page.getByRole('main').getByRole('alert').filter({ hasText: 'Could not save the custom models:' })).toHaveText("Could not save the custom models: provider name must be 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit");
    await expect(form).toBeVisible();
    expect((await readSettings(uam)).custom_models!.map((m) => m.name)).not.toContain('bad name');
    await page.reload();
    await openSettings(page, 'Custom providers');
    await expect(section.getByRole('region', { name: 'bad name models' })).toHaveCount(0);
  } finally {
    await restore();
  }
});
