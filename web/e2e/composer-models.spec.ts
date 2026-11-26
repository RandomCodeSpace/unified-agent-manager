// The toolbar's pickers: the Thread's model and its permissions and execution.
import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { SECOND_MODEL, useSecondModel } from './composer-helpers';
import { conversation, expect, send, startThread, test, threadState } from './harness';

const create = (id: string, file: string) => ({ id, name: 'create', arguments: { path: file, file_text: `${id}\n` } });

/** Picks `name` in the composer's Model menu. */
async function pickModel(page: Page, from: string, name: string) {
  await page.getByRole('button', { name: `Model: ${from}` }).click();
  await page.getByRole('menuitemradio', { name, exact: true }).click();
  await expect(page.getByRole('button', { name: `Model: ${name}` })).toBeVisible();
}

test('the Model picker shows each model with its brand mark and switches the Thread between turns; the choice is saved', async ({ page, uam, project, model }) => {
  const restore = await useSecondModel(uam);
  try {
    model.on({ userMessage: 'first on the mock', model: 'mock-1' }, { content: 'Mock answered.' }, {}, 'mock turn');
    model.on({ userMessage: 'then on the other', model: SECOND_MODEL.modelId }, { content: 'The other answered.' }, {}, 'second turn');
    const url = await startThread(page, project, 'first on the mock');
    await expect(conversation(page).getByText('Mock answered.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();

    // A brand mark is its own drawing; a model of no known brand keeps the generic chip glyph.
    const brand = 'svg:not(.lucide)';
    const trigger = page.getByRole('button', { name: /^Model: / });
    await expect(trigger).toHaveAccessibleName('Model: Mock');
    await expect(trigger.locator(brand)).toHaveCount(0);
    await trigger.click();
    const mock = page.getByRole('menuitemradio', { name: 'Mock', exact: true });
    const second = page.getByRole('menuitemradio', { name: SECOND_MODEL.displayName, exact: true });
    await expect(mock).toBeChecked();
    await expect(second).not.toBeChecked();
    await expect(second.locator(brand)).toBeVisible();
    await expect(mock.locator(brand)).toHaveCount(0);
    await second.click();
    await expect(trigger).toHaveAccessibleName(`Model: ${SECOND_MODEL.displayName}`);
    await expect(trigger.locator(brand)).toBeVisible();

    await send(page, 'then on the other');
    await expect(conversation(page).getByText('The other answered.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    expect(model.served().map((s) => [s.fixture, s.request.model])).toEqual([['mock turn', 'mock-1'], ['second turn', SECOND_MODEL.modelId]]);

    expect(((await uam.api('GET', `/api/sessions/${url.split('#task=')[1]}`)) as { model: string }).model).toBe(`mock/${SECOND_MODEL.modelId}`);
    await page.reload();
    await expect(conversation(page).getByText('The other answered.')).toBeVisible();
    await expect(trigger).toHaveAccessibleName(`Model: ${SECOND_MODEL.displayName}`);
  } finally {
    await restore();
  }
});

test('a draft on another model than the running turn cannot steer: Enter queues it and Send now is dimmed with the reason', async ({ page, uam, project, model, workdir }) => {
  const restore = await useSecondModel(uam);
  try {
    model.toolRound('hold on the mock', [create('call_model_hold', path.join(workdir, 'held.txt'))], 'Held turn done.');
    model.on({ userMessage: 'for the other model', model: SECOND_MODEL.modelId }, { content: 'The other model took it.' }, {}, 'queued on second');
    await startThread(page, project, 'hold on the mock');
    const allow = conversation(page).getByRole('button', { name: 'Allow once' });
    await expect(allow).toBeVisible();
    await expect(page.getByRole('button', { name: 'Send now', exact: true })).toBeVisible();

    await pickModel(page, 'Mock', SECOND_MODEL.displayName);
    const reason = 'Model, effort and context changes apply to the next turn; Send now keeps the current turn’s settings.';
    await expect(page.getByText(reason, { exact: true })).toBeVisible();
    await expect(page.getByText(/^Current model: Mock · /)).toBeVisible();
    await expect(page.getByRole('button', { name: 'Send now', exact: true })).toHaveCount(0);
    const box = page.getByRole('textbox', { name: 'Message' });
    await box.fill('for the other model');
    await expect(page.getByRole('button', { name: 'After this turn', exact: true })).toBeEnabled();
    const more = page.getByRole('button', { name: 'More send options' });
    await more.click();
    const now = page.getByRole('menuitem', { name: /Send now/ });
    await expect(now).toHaveAttribute('aria-disabled', 'true');
    await expect(now).toHaveAccessibleDescription(reason);
    await page.keyboard.press('Escape');
    // The closing menu hands focus back to its button; a key pressed before that goes to the menu.
    await expect(now).toHaveCount(0);
    await expect(more).toBeFocused();

    await box.press('Enter');
    await expect(box).toHaveValue('');
    await expect(page.getByText('1 waiting', { exact: true })).toBeVisible();
    await expect(conversation(page).getByText('for the other model')).toHaveCount(0);
    expect(model.served().map((s) => s.fixture)).toEqual(['tools for hold on the mock']);

    await allow.click();
    await expect(conversation(page).getByText('The other model took it.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    // The running turn kept its model; the queued message ran on the draft's.
    expect(model.served().map((s) => [s.fixture, s.request.model])).toEqual([['tools for hold on the mock', 'mock-1'], ['result of call_model_hold', 'mock-1'], ['queued on second', SECOND_MODEL.modelId]]);
  } finally {
    await restore();
  }
});

test('Permissions and execution: Yolo runs a tool without asking, Safe asks again, and Yolo with Autopilot is confirmed first', async ({ page, project, model, workdir }) => {
  model.reply('modes please', 'Ready.');
  model.toolRound('write freely', [create('call_yolo', path.join(workdir, 'yolo.txt'))], 'Wrote it without asking.');
  model.toolRound('write carefully', [create('call_safe', path.join(workdir, 'safe.txt'))], 'Wrote it after asking.');
  await startThread(page, project, 'modes please');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const conv = conversation(page);
  const trigger = page.getByRole('button', { name: /^Permissions and execution: / });
  const menu = page.getByRole('menu', { name: /^Permissions and execution/ });
  const item = (name: string) => menu.getByRole('menuitemradio', { name: new RegExp(`^${name}\\b`) });
  const open = async () => {
    await trigger.click();
    await expect(menu).toBeVisible();
  };
  /** A choice leaves the menu open, also behind the confirmation. */
  const close = async () => {
    await expect(menu).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(menu).toBeHidden();
  };
  /** Opens the menu, picks `name`, and leaves the menu closed. */
  const pick = async (name: string) => {
    await open();
    await item(name).click();
    await close();
  };

  await expect(trigger).toHaveAccessibleName('Permissions and execution: Safe · Interactive');
  await open();
  for (const [name, checked] of [['Safe', true], ['Allow all', false], ['Interactive', true], ['Autopilot', false]] as const) {
    if (checked) await expect(item(name)).toBeChecked();
    else await expect(item(name)).not.toBeChecked();
  }
  await item('Allow all').click();
  await close();
  await expect(trigger).toHaveAccessibleName('Permissions and execution: Allow all · Interactive');

  await send(page, 'write freely');
  await expect(conv.getByText('Wrote it without asking.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByRole('button', { name: 'Allow once' })).toHaveCount(0);
  expect(fs.readFileSync(path.join(workdir, 'yolo.txt'), 'utf8')).toBe('call_yolo\n');

  // The riskiest pair is asked about; Cancel leaves the Thread as it was.
  const confirm = page.getByRole('alertdialog', { name: 'Switch to Allow all with Autopilot?' });
  await open();
  await item('Autopilot').click();
  await expect(confirm).toContainText('The agent will allow every permission request itself and keep working between turns without waiting for you.');
  await confirm.getByRole('button', { name: 'Cancel' }).click();
  await expect(confirm).toHaveCount(0);
  await close();
  await expect(trigger).toHaveAccessibleName('Permissions and execution: Allow all · Interactive');
  await open();
  await item('Autopilot').click();
  await confirm.getByRole('button', { name: 'Switch' }).click();
  await expect(confirm).toHaveCount(0);
  await close();
  await expect(trigger).toHaveAccessibleName(/^Permissions and execution: Allow all · Autopilot/);

  // Leaving the pair is not asked about.
  await pick('Interactive');
  await expect(trigger).toHaveAccessibleName('Permissions and execution: Allow all · Interactive');
  await pick('Safe');
  await expect(trigger).toHaveAccessibleName('Permissions and execution: Safe · Interactive');
  await expect(confirm).toHaveCount(0);
  await send(page, 'write carefully');
  const allow = conv.getByRole('button', { name: 'Allow once' });
  await expect(allow).toBeVisible();
  expect(fs.existsSync(path.join(workdir, 'safe.txt'))).toBe(false);
  await allow.click();
  await expect(conv.getByText('Wrote it after asking.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
});
