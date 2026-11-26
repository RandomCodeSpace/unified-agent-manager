// The composer's Tools panel: its tabs, the context bar, the Thread's budget, its custom agent and loaded skills, and output styles.
import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { after } from './activity-helpers';
import { systemText } from './composer-helpers';
import { conversation, expect, newThread, openHome, send, startThread, test, threadState } from './harness';
import { openSettings, readSettings } from './settings-helpers';

const toolsButton = (page: Page) => page.getByRole('button', { name: /^Tools/ });
const panel = (page: Page) => page.getByRole('dialog', { name: 'Tools' });

/** Opens Tools on the tab named `tab`. */
async function openTools(page: Page, tab: string) {
  await toolsButton(page).click();
  await panel(page).getByRole('tab', { name: tab, exact: true }).click();
  await expect(panel(page).getByRole('tabpanel', { name: tab })).toBeVisible();
}

async function closeTools(page: Page) {
  await page.keyboard.press('Escape');
  await expect(panel(page)).toBeHidden();
}

const writeAgent = (dir: string) => {
  fs.mkdirSync(path.join(dir, '.github/agents'), { recursive: true });
  fs.writeFileSync(path.join(dir, '.github/agents/reviewer.agent.md'), '---\nname: reviewer\ndescription: Reviews the fixture.\n---\nYou review the fixture and nothing else.\n');
};

test('Tools opens one panel of tabs that the arrow keys switch and Esc closes; the context bar, its share and the Context tab follow the usage', async ({ page, project, model, workdir }) => {
  writeAgent(workdir);
  model.reply('small start', 'Ready.');
  model.reply('grow the context', 'Grown.');
  await startThread(page, project, 'small start');
  await expect(threadState(page, 'Finished')).toBeVisible();

  // The bar is the button's drawing of the share its name states, with the share in words beside it; the mark stands at the
  // compaction threshold (80%). No ring: the Working mark is the only one.
  const button = toolsButton(page);
  const share = async () => {
    const name = (await button.getAttribute('aria-label')) ?? '';
    const m = /^Tools\. Context ([\d.]+K?) of 128K tokens · (\d+)%$/.exec(name);
    expect(m, name).not.toBeNull();
    return { used: m![1], percent: Number(m![2]) };
  };
  const mark = button.locator('[data-compact-mark]');
  const barFill = async () => Number(/width: ([\d.]+)%/.exec((await mark.locator('xpath=preceding-sibling::span[1]').getAttribute('style')) ?? '')?.[1]);
  await expect(button).toHaveAccessibleName(/^Tools\. Context .* of 128K tokens · \d+%$/);
  const before = await share();
  expect(before.percent).toBeGreaterThan(0);
  expect(Math.abs((await barFill()) - before.percent)).toBeLessThanOrEqual(1);
  await expect(button).toContainText(`${before.percent}%`);
  await expect(mark).toHaveAttribute('style', /left: 80%/);
  await expect(button.locator('circle')).toHaveCount(0);

  await button.click();
  const tools = panel(page);
  const tabs = tools.getByRole('tablist', { name: 'Tool' }).getByRole('tab');
  await expect(tabs).toHaveText(['Context', 'Usage', 'Agent', 'Style']);
  await expect(tools.getByRole('tab', { name: 'Context' })).toHaveAttribute('aria-selected', 'true');
  await expect(tools.getByRole('tab', { name: 'Context' })).toBeFocused();
  const context = tools.getByRole('tabpanel', { name: 'Context' });
  // The tab counts the conversation afresh, so its total can differ a little from the bar's last report.
  const total = async () => Number(/([\d.]+)K of 128K tokens/.exec(await context.innerText())?.[1]);
  await expect(context).toContainText(/[\d.]+K of 128K tokens/);
  const totalBefore = await total();
  expect(Math.abs((totalBefore / 128) * 100 - before.percent)).toBeLessThan(2);
  await expect(context).toContainText('Counted for Mock');
  await expect(context).toContainText('Compacts at 102.4K tokens');
  await expect(context).toContainText(/Latest prompt: [\d,]+ tokens/);
  /** The token count beside `term` in the Context tab's list. */
  const count = async (term: string) => Number((await context.getByRole('term').filter({ hasText: term }).locator('xpath=following-sibling::dd[1]').innerText()).replace(/[^\d]/g, ''));
  expect(await count('System and instructions')).toBeGreaterThan(0);
  expect(await count('Tool definitions')).toBeGreaterThan(0);
  const messages = await count('Messages');
  expect(messages).toBeGreaterThan(0);

  for (const name of ['Usage', 'Agent', 'Style', 'Context']) {
    await page.keyboard.press('ArrowRight');
    await expect(tools.getByRole('tab', { name, exact: true })).toHaveAttribute('aria-selected', 'true');
    await expect(tools.getByRole('tabpanel', { name })).toBeVisible();
    await expect(tools.getByRole('tabpanel')).toHaveCount(1);
  }
  await page.keyboard.press('ArrowLeft');
  await expect(tools.getByRole('tab', { name: 'Style', exact: true })).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Escape');
  await expect(tools).toBeHidden();
  await expect(button).toBeFocused();

  // A long message fills the context: the bar, the button's name and the tab's counts all move with it.
  await send(page, `grow the context ${'lorem ipsum dolor sit amet '.repeat(1500)}`);
  await expect(conversation(page).getByText('Grown.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect.poll(async () => (await share()).percent).toBeGreaterThan(before.percent + 3);
  const after = await share();
  expect(Math.abs((await barFill()) - after.percent)).toBeLessThanOrEqual(1);
  await expect(button).toContainText(`${after.percent}%`);
  await button.click();
  // It opens on the tab last shown.
  await expect(tools.getByRole('tab', { name: 'Style', exact: true })).toHaveAttribute('aria-selected', 'true');
  await tools.getByRole('tab', { name: 'Context', exact: true }).click();
  await expect.poll(total).toBeGreaterThan(totalBefore + 3);
  await expect.poll(() => count('Messages')).toBeGreaterThan(messages + 3000);
  await closeTools(page);
});

test('a Thread\'s budget is set, shown with what is spent, kept over a reload and removed in Tools, Usage', async ({ page, uam, project, model }) => {
  model.reply('budget please', 'Ready.');
  const id = (await startThread(page, project, 'budget please')).split('#task=')[1];
  await expect(threadState(page, 'Finished')).toBeVisible();
  const budgetOf = async () => ((await uam.api('GET', `/api/sessions/${id}`)) as { budget?: { credits: number; quota_percent: number; spent: number } }).budget;

  await openTools(page, 'Usage');
  const budget = panel(page).getByRole('region', { name: 'Budget' });
  await expect(budget).toContainText('No limit. Set how many AI credits this Thread may still spend, or at what account quota it stops.');
  await budget.getByRole('button', { name: 'Set a budget' }).click();
  const set = budget.getByRole('button', { name: 'Set budget' });
  await expect(set).toBeDisabled();
  await budget.getByRole('textbox', { name: 'Stop at' }).fill('120');
  await expect(budget.getByRole('textbox', { name: 'Stop at' })).toHaveAttribute('aria-invalid', 'true');
  await expect(set).toBeDisabled();
  await budget.getByRole('textbox', { name: 'Stop at' }).fill('80');
  await budget.getByRole('textbox', { name: 'Stop after' }).fill('12.5');
  await set.click();
  await expect(budget).toContainText('0 of 12.5 AI credits since set · 0%');
  await expect(budget).toContainText('stops at 12.5');
  await expect(budget).toContainText('stops at 80%');
  await expect(budget.getByRole('button', { name: 'Change' })).toHaveAttribute('aria-expanded', 'false');
  await expect(budget).not.toContainText('No limit.');
  expect(await budgetOf()).toMatchObject({ credits: 12.5, quota_percent: 80, spent: 0 });

  await page.reload();
  await expect(conversation(page).getByText('Ready.')).toBeVisible();
  await openTools(page, 'Usage');
  await expect(budget).toContainText('0 of 12.5 AI credits since set · 0%');
  await budget.getByRole('button', { name: 'Change' }).click();
  await expect(budget.getByRole('textbox', { name: 'Stop after' })).toHaveValue('12.5');
  await budget.getByRole('button', { name: 'Remove' }).click();
  await expect(budget).toContainText('No limit.');
  await expect(budget.getByRole('button', { name: 'Set a budget' })).toBeVisible();
  await expect.poll(budgetOf).toBeUndefined();
  await closeTools(page);
  expect(model.served()).toHaveLength(1);
});

test('the Agent tab picks a Project custom agent between turns only, and lists the skills the agent loaded', async ({ page, uam, project, model, workdir }) => {
  writeAgent(workdir);
  fs.mkdirSync(path.join(workdir, '.github/skills/tidy-up'), { recursive: true });
  fs.writeFileSync(path.join(workdir, '.github/skills/tidy-up/SKILL.md'), '---\nname: tidy-up\ndescription: Tidies the fixture.\n---\nTidy the fixture.\n');
  model.reply('agent tab', 'Ready.');
  model.toolRound('hold the agent', [{ id: 'call_agent_hold', name: 'create', arguments: { path: path.join(workdir, 'held.txt'), file_text: 'held\n' } }], 'Held turn done.');
  // A loaded skill's text follows its tool result as a message of its own, so the reply is matched by the result, wherever it stands.
  model.on({ predicate: after('call_skill') }, { content: 'Skill loaded.' }, {}, 'after the skill');
  model.on({ userMessage: 'load the skill' }, { toolCalls: [{ id: 'call_skill', name: 'skill', arguments: JSON.stringify({ skill: 'tidy-up' }) }] }, {}, 'load the skill');
  const id = (await startThread(page, project, 'agent tab')).split('#task=')[1];
  await expect(threadState(page, 'Finished')).toBeVisible();
  const conv = conversation(page);
  const agentOf = async () => ((await uam.api('GET', `/api/sessions/${id}`)) as { agent?: string }).agent ?? '';

  await openTools(page, 'Agent');
  const agents = panel(page).getByRole('radiogroup', { name: 'Agent' });
  const reviewer = agents.getByRole('radio', { name: /^reviewer/ });
  await expect(agents.getByRole('radio', { name: 'Default' })).toBeChecked();
  await expect(reviewer).toContainText('Reviews the fixture.');
  await expect(panel(page).getByRole('list', { name: 'Skills loaded' })).toHaveCount(0);
  await reviewer.click();
  await expect.poll(agentOf).toBe('reviewer');
  await expect(reviewer).toBeChecked();
  await closeTools(page);

  // While a turn runs the choice is locked, with the reason.
  await send(page, 'hold the agent');
  const allow = conv.getByRole('button', { name: 'Allow once' });
  await expect(allow).toBeVisible();
  await openTools(page, 'Agent');
  await expect(reviewer).toBeChecked();
  await expect(reviewer).toBeDisabled();
  await expect(agents.getByRole('radio', { name: 'Default' })).toBeDisabled();
  await expect(panel(page).getByText('The agent changes between turns.')).toBeVisible();
  await closeTools(page);
  await allow.click();
  await expect(conv.getByText('Held turn done.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  // The turn ran as the picked agent: its instructions are in the model's system prompt.
  expect(systemText(model.served().find((s) => s.fixture === 'tools for hold the agent')!.request)).toContain('You review the fixture and nothing else.');

  await uam.api('PATCH', `/api/sessions/${id}`, { mode: 'yolo' });
  await expect(page.getByRole('button', { name: /^Permissions and execution: Allow all/ })).toBeVisible();
  await send(page, 'load the skill');
  await expect(conv.getByText('Skill loaded.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await openTools(page, 'Agent');
  const loaded = panel(page).getByRole('list', { name: 'Skills loaded' }).getByRole('listitem');
  await expect(loaded).toHaveCount(1);
  await expect(loaded).toContainText('tidy-up');
  await expect(loaded).toContainText('Tidies the fixture.');
  await expect(reviewer).toBeChecked();
  await expect(reviewer).toBeEnabled();
  await closeTools(page);
});

test('an output style added in Settings is offered in a new Thread\'s Style tab, reaches the agent, and is fixed once the Thread starts', async ({ page, uam, project, model }) => {
  try {
    const text = 'Answer as a ship captain would, in one line.';
    model.reply('style check', 'Aye.');
    await openSettings(page, 'Output styles');
    const library = page.getByRole('region', { name: 'Output styles' });
    await library.getByRole('button', { name: 'Add style' }).click();
    await library.getByRole('textbox', { name: 'Style name' }).fill('Captain');
    await library.getByRole('textbox', { name: 'Style text' }).fill(text);
    await library.getByRole('button', { name: 'Save library' }).click();
    await expect.poll(async () => (await readSettings(uam)).output_styles).toEqual([{ name: 'Captain', text }]);

    await openHome(page);
    await newThread(page, project);
    await openTools(page, 'Style');
    const styles = panel(page).getByRole('radiogroup', { name: 'Output style' });
    const captain = styles.getByRole('radio', { name: /^Captain/ });
    await expect(captain).toContainText(text);
    await expect(captain).not.toBeChecked();
    await captain.click();
    await expect(captain).toBeChecked();
    await closeTools(page);
    await send(page, 'style check');
    await expect(conversation(page).getByText('Aye.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    expect(JSON.stringify(model.served().find((s) => s.fixture === 'reply style check')!.request.messages)).toContain(text);

    // Started, the Thread shows the style it began with and offers no change.
    await openTools(page, 'Style');
    await expect(captain).toBeChecked();
    await expect(captain).toBeDisabled();
    for (const radio of await styles.getByRole('radio').all()) await expect(radio).toBeDisabled();
    await expect(panel(page).getByText('Fixed when the Thread started.')).toBeVisible();
    await closeTools(page);
  } finally {
    await uam.api('PATCH', '/api/settings', { output_styles: [] });
  }
});
