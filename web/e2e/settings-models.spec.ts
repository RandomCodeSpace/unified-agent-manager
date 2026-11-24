// Settings → Models: which models the menus offer, the ones subagents may use, token prices, and a refused save.
import { type Page } from '@playwright/test';
import { script, subagentAsked } from './activity-helpers';
import { conversation, expect, newThread, openHome, send, test, threadState } from './harness';
import { addMockModels, keepSettings, openSettings, pickModel, quietBackground, readSettings, tag, withUsage } from './settings-helpers';

type Api = { api: (method: string, route: string, body?: unknown) => Promise<unknown> };
type Meta = { providers: { name: string; models: { id: string }[] }[] };

/** Shows every Copilot model again. */
const showAll = (uam: Api) => uam.api('PATCH', '/api/settings', { hidden_models: { copilot: [] } });
const hidden = async (uam: Api) => ((await readSettings(uam)).hidden_models as Record<string, string[]> | undefined)?.copilot ?? [];
const subagentModels = async (uam: Api) => ((await readSettings(uam)).subagent_models as Record<string, string[]> | undefined)?.copilot ?? [];
const settingsError = (page: Page) => page.getByRole('main').getByRole('alert').filter({ hasText: 'Could not save the setting:' });

test('a save the service refuses puts the old value back and says why', async ({ page, uam }) => {
  try {
    // The service keeps one model visible: with every other one hidden, hiding the mock is refused.
    const others = ((await uam.api('GET', '/api/meta')) as Meta).providers.find((p) => p.name === 'copilot')!.models.map((m) => m.id).filter((id) => id !== 'mock/mock-1');
    await uam.api('PATCH', '/api/settings', { hidden_models: { copilot: others } });

    await openSettings(page, 'Models');
    const show = page.getByRole('group', { name: 'mock models' }).getByRole('switch', { name: 'Show Mock', exact: true });
    await expect(show).toBeChecked();
    await show.click();
    await expect(settingsError(page)).toHaveText('Could not save the setting: at least one GitHub Copilot model must stay visible');
    await expect(show).toBeChecked();
    await expect(show).toBeEnabled();
    expect(await hidden(uam)).toEqual(others);
    await page.reload();
    await openSettings(page, 'Models');
    await expect(show).toBeChecked();
    await expect(settingsError(page)).toHaveCount(0);
  } finally {
    await showAll(uam);
  }
});

test('a model hidden in Settings leaves the model menu; a Thread already on it keeps it, says "Hidden in Settings" and still runs on it', async ({ page, uam, project, model }) => {
  const restore = await keepSettings(uam, ['custom_models']);
  try {
    const id = `hide-${tag()}`;
    const name = `Hide ${id}`;
    await addMockModels(uam, [{ model_id: id, display_name: name }]);
    model.on({ userMessage: 'before it is hidden', model: id }, { content: 'Answered before hiding.' });
    model.on({ userMessage: 'after it is hidden', model: id }, { content: 'Answered while hidden.' });

    await openHome(page);
    await newThread(page, project);
    await pickModel(page, name);
    await send(page, 'before it is hidden');
    await expect(conversation(page).getByText('Answered before hiding.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    const thread = page.url();

    await openSettings(page, 'Models');
    const show = page.getByRole('group', { name: 'mock models' }).getByRole('switch', { name: `Show ${name}` });
    await expect(show).toBeChecked();
    await show.click();
    await expect(show).not.toBeChecked();
    await expect.poll(() => hidden(uam)).toEqual([`mock/${id}`]);
    await page.reload();
    await openSettings(page, 'Models');
    await expect(show).not.toBeChecked();
    // The defaults for new Threads no longer offer it either.
    await openSettings(page, 'New threads');
    await page.getByRole('region', { name: 'New threads' }).getByRole('combobox', { name: 'Model', exact: true }).click();
    await expect(page.getByRole('option', { name: 'Mock', exact: true })).toBeVisible();
    await expect(page.getByRole('option', { name })).toHaveCount(0);
    await page.keyboard.press('Escape');

    await page.goto(thread);
    await expect(page.getByRole('button', { name: `Model: ${name}` })).toBeVisible();
    await expect(page.getByText('Hidden in Settings', { exact: true })).toBeVisible();
    await send(page, 'after it is hidden');
    await expect(conversation(page).getByText('Answered while hidden.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    expect(model.served().map((s) => s.request.model)).toEqual([id, id]);

    await newThread(page, project);
    await expect(page.getByText('Hidden in Settings', { exact: true })).toHaveCount(0);
    await page.getByRole('button', { name: /^Model:/ }).click();
    await expect(page.getByRole('menu').getByRole('menuitemradio', { name: 'Mock', exact: true })).toBeVisible();
    await expect(page.getByRole('menu').getByRole('menuitemradio', { name })).toHaveCount(0);
    await page.keyboard.press('Escape');
  } finally {
    await showAll(uam);
    await restore();
  }
});

test('Subagent models: add, Make fallback and Remove are saved, and a subagent started outside the list runs on the fallback', async ({ page, uam, project, model }) => {
  const restore = await keepSettings(uam, ['custom_models']);
  try {
    const t = tag();
    const [two, three] = [{ model_id: `sub2-${t}`, display_name: `Sub Two ${t}` }, { model_id: `sub3-${t}`, display_name: `Sub Three ${t}` }];
    await addMockModels(uam, [two, three]);
    script(model, { userMessage: 'delegate under the limit' }, [
      [{ id: 'call_limited', name: 'task', arguments: { name: 'limited', description: 'Report', prompt: 'Report the limit', agent_type: 'general-purpose' } }],
      'The subagent reported.',
    ], {}, 'main');
    model.on(subagentAsked('Report the limit'), { content: 'Reporting from the subagent.' }, {}, 'subagent');

    await openSettings(page, 'Models');
    const section = page.getByRole('region', { name: 'Subagent models' });
    const add = section.getByRole('combobox', { name: 'Add a GitHub Copilot subagent model' });
    await expect(section.getByText('Any model', { exact: true })).toBeVisible();
    for (const m of [two, three]) {
      await add.click();
      await page.getByRole('option', { name: m.display_name }).click();
      await expect(section.getByRole('button', { name: `Remove ${m.display_name} from subagent models` })).toBeVisible();
    }
    await expect(section.getByText('Any model', { exact: true })).toHaveCount(0);
    await expect.poll(() => subagentModels(uam)).toEqual([`mock/${two.model_id}`, `mock/${three.model_id}`]);
    // The first is the fallback: it has the chip and no Make fallback of its own.
    await expect(section.getByText('Fallback', { exact: true })).toHaveCount(1);
    await expect(section.getByRole('button', { name: `Make ${two.display_name} the fallback` })).toHaveCount(0);

    await section.getByRole('button', { name: `Make ${three.display_name} the fallback` }).click();
    await expect.poll(() => subagentModels(uam)).toEqual([`mock/${three.model_id}`, `mock/${two.model_id}`]);
    await expect(section.getByRole('button', { name: `Make ${two.display_name} the fallback` })).toBeVisible();
    await section.getByRole('button', { name: `Remove ${two.display_name} from subagent models` }).click();
    await expect.poll(() => subagentModels(uam)).toEqual([`mock/${three.model_id}`]);
    await expect(section.getByRole('button', { name: `Remove ${two.display_name} from subagent models` })).toHaveCount(0);
    await page.reload();
    await openSettings(page, 'Models');
    await expect(section.getByRole('button', { name: `Remove ${three.display_name} from subagent models` })).toBeVisible();
    await expect(section.getByText('Fallback', { exact: true })).toHaveCount(1);

    // The Thread runs on the mock, which the list leaves out, and the agent names no model: the subagent gets the fallback.
    await openHome(page);
    await newThread(page, project);
    await send(page, 'delegate under the limit');
    await expect(conversation(page).getByText('The subagent reported.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    const served = model.served();
    expect(served.filter((s) => s.fixture.startsWith('main')).map((s) => s.request.model)).toEqual(['mock-1', 'mock-1']);
    expect(served.filter((s) => s.fixture === 'subagent').map((s) => s.request.model)).toEqual([three.model_id]);
  } finally {
    await uam.api('PATCH', '/api/settings', { subagent_models: { copilot: [] } });
    await restore();
  }
});

test('Token costs: an unpriced model says so and has no cost; saved input and output prices give its usage an estimated cost', async ({ page, uam, project, model }) => {
  const restore = await keepSettings(uam, ['custom_models']);
  const quiet = await quietBackground(uam);
  try {
    const id = `price-${tag()}`;
    const name = `Price ${id}`;
    await addMockModels(uam, [{ model_id: id, display_name: name }]);
    model.on({ userMessage: 'spend some tokens', model: id }, withUsage('Tokens spent.', 1234, 66));

    await openHome(page);
    await newThread(page, project);
    await pickModel(page, name);
    await send(page, 'spend some tokens');
    await expect(conversation(page).getByText('Tokens spent.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();

    const usage = page.getByRole('dialog', { name: 'Usage' });
    // The model's line in the popover's All models view: its cost, tokens and cache hit in full.
    const costLine = async (cost: string) => {
      await page.getByRole('complementary').getByRole('button', { name: 'Usage and AI', exact: true }).click();
      await usage.getByRole('group', { name: 'Usage view' }).getByRole('button', { name: /^All models/ }).click();
      await expect(usage.getByRole('img', { name: `${name} token split: Input 1,234; Output 66; Cache 0` })).toBeVisible();
      await expect(usage.getByTitle(`Estimated cost ${cost} in USD · 1,300 tokens`, { exact: false })).toBeVisible();
      await usage.getByRole('button', { name: 'Close usage' }).click();
      await expect(usage).toBeHidden();
    };
    await costLine('—');

    await openSettings(page, 'Models');
    const costs = page.getByRole('region', { name: 'Token costs' });
    const pick = async () => {
      await costs.getByRole('combobox', { name: 'Model' }).click();
      await page.getByRole('option', { name: `copilot · mock/${id}` }).click();
    };
    await pick();
    await expect(costs.getByText('Unpriced. Add input and output prices to estimate cost.')).toBeVisible();
    const save = costs.getByRole('button', { name: 'Save token prices' });
    await expect(save).toBeDisabled();
    await costs.getByRole('spinbutton', { name: 'Input' }).fill('1000');
    await expect(save).toBeDisabled();
    await costs.getByRole('spinbutton', { name: 'Output' }).fill('2000');
    await save.click();
    await expect.poll(async () => ((await readSettings(uam)).token_prices as Record<string, Record<string, unknown>> | undefined)?.copilot?.[`mock/${id}`]).toEqual({ input: 1000, output: 2000 });
    // Priced now, it is listed with the other priced models.
    await costs.getByRole('checkbox', { name: 'Show all models' }).check();
    await pick();
    await expect(costs.getByText('Manual prices', { exact: true })).toBeVisible();
    await expect(costs.getByRole('spinbutton', { name: 'Input' })).toHaveValue('1000');
    await expect(costs.getByRole('spinbutton', { name: 'Output' })).toHaveValue('2000');

    // 1,234 input tokens at $1,000 and 66 output tokens at $2,000 per million.
    await costLine('$1.37');

    await pick();
    await costs.getByRole('button', { name: 'Remove override' }).click();
    await expect.poll(async () => ((await readSettings(uam)).token_prices as Record<string, Record<string, unknown>> | undefined)?.copilot?.[`mock/${id}`]).toBeUndefined();
    await pick();
    await expect(costs.getByText('Unpriced. Add input and output prices to estimate cost.')).toBeVisible();
    await costLine('—');
    expect(model.served()).toHaveLength(1);
  } finally {
    await uam.api('PATCH', '/api/settings', { token_prices: {} });
    await quiet();
    await restore();
  }
});
