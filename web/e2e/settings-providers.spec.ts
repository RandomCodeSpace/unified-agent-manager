// Settings → Custom providers: the environment-variable key source, editing and removing, and the vision mark.
import { type Page } from '@playwright/test';
import { conversation, expect, newThread, openHome, send, test, threadState } from './harness';
import { keepSettings, openSettings, pickModel, readSettings, tag } from './settings-helpers';

type Api = { api: (method: string, route: string, body?: unknown) => Promise<unknown> };

// A 2×2 PNG.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAEElEQVR4nGP4z8AARAwQCgAf7gP9i18U1AAAAABJRU5ErkJggg==', 'base64');

const providers = (page: Page) => page.getByRole('region', { name: 'Custom providers' });
const saveError = (page: Page) => page.getByRole('main').getByRole('alert').filter({ hasText: 'Could not save the custom models:' });
const modelsOf = async (uam: Api, name: string) => (await readSettings(uam)).custom_models!.filter((m) => m.name === name);

/** Adds the typed model ID to the open provider form's checklist, checked. */
async function addId(page: Page, id: string) {
  const form = providers(page).getByRole('form');
  await form.getByRole('textbox', { name: 'Add a model ID the endpoint does not list' }).fill(id);
  await form.getByRole('button', { name: 'Add ID' }).click();
  await expect(form.getByRole('group', { name: 'Models to offer' }).getByRole('checkbox', { name: id, exact: true })).toBeChecked();
}

test('a key from an environment variable: the service refuses a name outside UAM_BYOM_ and a base URL with credentials, and an unset variable is named where the provider shows', async ({ page, uam, project }) => {
  const restore = await keepSettings(uam, ['custom_models']);
  try {
    const name = `env${tag()}`;
    const variable = `UAM_BYOM_PACK_${tag().toUpperCase()}`;
    await openSettings(page, 'Custom providers');
    await providers(page).getByRole('button', { name: 'Add provider' }).click();
    const form = providers(page).getByRole('form', { name: 'Add a provider' });
    await form.getByRole('textbox', { name: 'Provider name' }).fill(name);
    await form.getByRole('textbox', { name: 'Base URL' }).fill(`${uam.mock.url}/v1`);
    await form.getByRole('radiogroup', { name: 'API key source' }).getByRole('radio', { name: 'Environment variable' }).click();
    await expect(form.getByRole('textbox', { name: 'API key', exact: true })).toHaveCount(0);
    await expect(form.getByText('Export this variable where the service starts, then restart the service.')).toBeVisible();
    await addId(page, 'env-1');

    // Any other variable of the service's environment stays out of reach.
    await form.getByRole('textbox', { name: 'API key variable' }).fill('HOME');
    await form.getByRole('button', { name: 'Save provider' }).click();
    await expect(saveError(page)).toHaveText("Could not save the custom models: API key variable must be named UAM_BYOM_<NAME>, with letters, digits and '_' after the prefix, at most 64 bytes; only such variables are sent to custom model endpoints");
    await expect(form).toBeVisible();
    expect(await modelsOf(uam, name)).toEqual([]);

    await form.getByRole('textbox', { name: 'API key variable' }).fill(variable);
    await form.getByRole('textbox', { name: 'Base URL' }).fill('http://user:secret@127.0.0.1:9/v1');
    await form.getByRole('button', { name: 'Save provider' }).click();
    await expect(saveError(page)).toHaveText('Could not save the custom models: base URL must be an http or https URL of at most 512 bytes, without credentials, query or fragment');
    expect(await modelsOf(uam, name)).toEqual([]);

    await form.getByRole('textbox', { name: 'Base URL' }).fill(`${uam.mock.url}/v1`);
    await form.getByRole('button', { name: 'Save provider' }).click();
    await expect(form).toBeHidden();
    await expect(saveError(page)).toHaveCount(0);
    const saved = providers(page).getByRole('region', { name: `${name} models` });
    await expect(saved.getByText(`${variable} is not set in the service's environment`, { exact: true })).toBeVisible();
    expect(await modelsOf(uam, name)).toMatchObject([{ model_id: 'env-1', api_key_env: variable, key_present: false }]);
    // The rail marks the page, and Models says it beside the provider's models.
    await expect(page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'Custom providers', exact: true })).toHaveAccessibleDescription(new RegExp(`^API key missing: .*${name}`));
    await openSettings(page, 'Models');
    await expect(page.getByRole('group', { name: `${name} models` }).getByText(`${variable} is not set in the service's environment. Check it in Custom providers.`)).toBeVisible();

    // The edit form reads the source back.
    await openSettings(page, 'Custom providers');
    await saved.getByRole('button', { name: 'Edit', exact: true }).click();
    const edit = providers(page).getByRole('form', { name: `Edit provider ${name}` });
    await expect(edit.getByRole('radiogroup', { name: 'API key source' }).getByRole('radio', { name: 'Environment variable' })).toBeChecked();
    await expect(edit.getByRole('textbox', { name: 'API key variable' })).toHaveValue(variable);
    await edit.getByRole('button', { name: 'Cancel' }).click();

    // Chosen for a Thread, the model fails with the variable's name.
    await openHome(page);
    await newThread(page, project);
    await pickModel(page, 'env-1', name);
    await send(page, 'run without the key');
    await expect(page.getByRole('main').getByText(`Could not start the thread: could not start a GitHub Copilot conversation: custom model ${name}/env-1 needs its API key in the environment variable ${variable}, which is not set in the uam web service's environment; export it where the service starts and restart the service`)).toBeVisible();
    // The draft stays, with the message, to send once the model is changed.
    await expect(page.getByText(`New thread in ${project.name}`, { exact: true })).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Message' })).toHaveValue('run without the key');
  } finally {
    await restore();
  }
});

test('Edit keeps the saved models checked; unchecking one asks before it is removed; a model and the provider are removed after a confirmation', async ({ page, uam }) => {
  const restore = await keepSettings(uam, ['custom_models']);
  try {
    const name = `edit${tag()}`;
    const before = (await readSettings(uam)).custom_models!.map(({ key_present: _, ...m }) => m);
    await uam.api('PATCH', '/api/settings', {
      custom_models: [...before, ...['a-1', 'a-2', 'a-3'].map((model_id) => ({ name, display_name: model_id, base_url: `${uam.mock.url}/v1`, model_id, api_key: 'edit-key', api_key_env: '' }))],
    });
    const ids = async () => (await modelsOf(uam, name)).map((m) => m.model_id);

    await openSettings(page, 'Custom providers');
    const saved = providers(page).getByRole('region', { name: `${name} models` });
    await saved.getByRole('button', { name: 'Edit', exact: true }).click();
    const form = providers(page).getByRole('form', { name: `Edit provider ${name}` });
    const offered = form.getByRole('group', { name: 'Models to offer' });
    await expect(form.getByRole('textbox', { name: 'Provider name' })).toHaveValue(name);
    await expect(form.getByRole('textbox', { name: 'Base URL' })).toHaveValue(`${uam.mock.url}/v1`);
    for (const id of ['a-1', 'a-2', 'a-3']) await expect(offered.getByRole('checkbox', { name: id, exact: true })).toBeChecked();
    await expect(form.getByText('3 selected', { exact: true })).toBeVisible();

    await offered.getByRole('checkbox', { name: 'a-3', exact: true }).uncheck();
    await expect(form.getByText('2 selected', { exact: true })).toBeVisible();
    await form.getByRole('button', { name: 'Save provider' }).click();
    const replace = page.getByRole('alertdialog', { name: 'Remove models from this provider?' });
    await expect(replace.getByText(`${name}/a-3`, { exact: true })).toBeVisible();
    await expect(replace.getByText(`${name}/a-2`, { exact: true })).toHaveCount(0);
    // Cancel saves nothing and keeps the form as it was.
    await replace.getByRole('button', { name: 'Cancel' }).click();
    await expect(replace).toBeHidden();
    await expect(form).toBeVisible();
    await expect(offered.getByRole('checkbox', { name: 'a-3', exact: true })).not.toBeChecked();
    expect(await ids()).toEqual(['a-1', 'a-2', 'a-3']);

    await form.getByRole('button', { name: 'Save provider' }).click();
    await replace.getByRole('button', { name: 'Save and remove models' }).click();
    await expect(form).toBeHidden();
    await expect(saved.getByRole('button', { name: `Remove ${name}/a-3` })).toHaveCount(0);
    await expect(saved.getByRole('button', { name: `Remove ${name}/a-2` })).toBeVisible();
    // The key was left blank: the saved one stays with the models kept.
    await expect(saved.getByText('API key saved')).toBeVisible();
    expect(await modelsOf(uam, name)).toMatchObject([{ model_id: 'a-1', key_present: true }, { model_id: 'a-2', key_present: true }]);

    await saved.getByRole('button', { name: `Remove ${name}/a-2` }).click();
    const one = page.getByRole('alertdialog', { name: `Remove ${name}/a-2?` });
    await expect(one).toContainText('It leaves the model menus. Threads already using it keep it until their model is changed.');
    await one.getByRole('button', { name: 'Cancel' }).click();
    await expect(one).toBeHidden();
    expect(await ids()).toEqual(['a-1', 'a-2']);
    await saved.getByRole('button', { name: `Remove ${name}/a-2` }).click();
    await one.getByRole('button', { name: 'Remove model' }).click();
    await expect(saved.getByRole('button', { name: `Remove ${name}/a-2` })).toHaveCount(0);
    await expect.poll(ids).toEqual(['a-1']);

    await saved.getByRole('button', { name: `Remove provider ${name}` }).click();
    const all = page.getByRole('alertdialog', { name: `Remove provider ${name}?` });
    await expect(all).toContainText('Its one model leaves the model menus. Threads already using one keep it until their model is changed.');
    await all.getByRole('button', { name: 'Remove provider' }).click();
    await expect(saved).toHaveCount(0);
    await expect.poll(ids).toEqual([]);
    await page.reload();
    await openSettings(page, 'Custom providers');
    await expect(providers(page).getByRole('region', { name: 'mock models' })).toBeVisible();
    await expect(saved).toHaveCount(0);
  } finally {
    await restore();
  }
});

test('Enable vision is offered for checked models only and saved per model: the model with it takes an image, the one without takes text files only', async ({ page, uam, project, model }) => {
  const restore = await keepSettings(uam, ['custom_models']);
  try {
    const name = `vis${tag()}`;
    model.on({ userMessage: 'what is in the picture', model: 'see-1' }, { content: 'A tiny square.' });

    await openSettings(page, 'Custom providers');
    await providers(page).getByRole('button', { name: 'Add provider' }).click();
    const form = providers(page).getByRole('form', { name: 'Add a provider' });
    await form.getByRole('textbox', { name: 'Provider name' }).fill(name);
    await form.getByRole('textbox', { name: 'Base URL' }).fill(`${uam.mock.url}/v1`);
    await form.getByRole('textbox', { name: 'API key', exact: true }).fill('vision-key');
    await addId(page, 'see-1');
    await addId(page, 'blind-1');
    const offered = form.getByRole('group', { name: 'Models to offer' });
    const vision = (id: string) => offered.getByRole('checkbox', { name: `Enable vision for ${id}` });
    await expect(vision('see-1')).toBeEnabled();
    await expect(vision('see-1')).not.toBeChecked();
    // A model that is not offered cannot be marked.
    await offered.getByRole('checkbox', { name: 'blind-1', exact: true }).uncheck();
    await expect(vision('blind-1')).toBeDisabled();
    await offered.getByRole('checkbox', { name: 'blind-1', exact: true }).check();
    await expect(vision('blind-1')).toBeEnabled();
    await vision('see-1').check();
    await form.getByRole('button', { name: 'Save provider' }).click();
    await expect(form).toBeHidden();
    const stored = await modelsOf(uam, name);
    expect(stored.find((m) => m.model_id === 'see-1')).toMatchObject({ vision: true });
    expect(stored.find((m) => m.model_id === 'blind-1')!.vision ?? false).toBe(false);

    // The edit form reads the marks back.
    await providers(page).getByRole('region', { name: `${name} models` }).getByRole('button', { name: 'Edit', exact: true }).click();
    const edit = providers(page).getByRole('form', { name: `Edit provider ${name}` });
    await expect(edit.getByRole('checkbox', { name: 'Enable vision for see-1' })).toBeChecked();
    await expect(edit.getByRole('checkbox', { name: 'Enable vision for blind-1' })).not.toBeChecked();
    await edit.getByRole('button', { name: 'Cancel' }).click();

    await openHome(page);
    await newThread(page, project);
    await pickModel(page, 'blind-1', name);
    await expect(page.getByRole('button', { name: /^Attach files.*blind-1 takes text files only/ })).toBeVisible();
    await pickModel(page, 'see-1', name);
    const attach = page.getByRole('button', { name: /^Attach files/ });
    await expect(attach).not.toHaveAccessibleName(/text files only/);
    const chooser = page.waitForEvent('filechooser');
    await attach.click();
    await (await chooser).setFiles([{ name: 'square.png', mimeType: 'image/png', buffer: PNG }]);
    await expect(page.getByRole('button', { name: 'Remove square.png' })).toBeVisible();
    await send(page, 'what is in the picture');
    await expect(conversation(page).getByText('A tiny square.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    const [served] = model.served();
    expect(served.request.model).toBe('see-1');
    expect(JSON.stringify([...served.request.messages].reverse().find((m) => m.role === 'user')!.content)).toContain('image_url');
  } finally {
    await restore();
  }
});
