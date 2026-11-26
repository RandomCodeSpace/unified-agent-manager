// What the composer offers beyond plain text: the CLI's slash commands and the suggested reply.
import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { aside, isSuggestion, SECOND_MODEL, sidebarRow, suggest, useMockUtility, useSecondModel } from './composer-helpers';
import { conversation, expect, lastUserText, send, startThread, test, threadState } from './harness';

/** The message box: a combobox while one of its lists is open. */
const messageBox = (page: Page) => page.getByRole('textbox', { name: 'Message' }).or(page.getByRole('combobox', { name: 'Message' }));

/** Types `command` whole and runs it with its Run button, which names the command (`name`, when `command` uses an alias). */
async function runCommand(page: Page, command: string, name = command.split(' ')[0]) {
  await messageBox(page).fill(command);
  await page.getByRole('button', { name: `Run ${name}`, exact: true }).click();
  await expect(messageBox(page)).toHaveValue('');
}

test('/ lists the real CLI catalogue; unsupported commands are dimmed with the reason; /context and /env open their output', async ({ page, project, model }) => {
  model.reply('before commands', 'Ready for commands.');
  await startThread(page, project, 'before commands');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const conv = conversation(page);
  await expect(conv.getByText('You:')).toHaveCount(1);

  const box = page.getByRole('textbox', { name: 'Message' }).or(page.getByRole('combobox', { name: 'Message' }));
  await box.click();
  await page.keyboard.type('/');
  const list = page.getByRole('listbox', { name: 'Commands' });
  const options = list.getByRole('option');
  await expect(options.first()).toHaveAccessibleName(/^\/btw\. /);
  await expect(options.first()).toHaveAttribute('aria-selected', 'true');
  const share = list.getByRole('option', { name: /^\/share\. / });
  await expect(share).toHaveAccessibleName('/share. Publishing and remote sessions are not supported by the web client');
  await expect(share).toHaveAttribute('aria-disabled', 'true');
  await expect(list.getByRole('option', { name: /^\/env\. / })).not.toHaveAttribute('aria-disabled', 'true');

  await page.keyboard.type('re');
  await expect(options.first()).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('ArrowDown');
  await expect(options.nth(1)).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('ArrowUp');
  await expect(options.first()).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Escape');
  await expect(list).toBeHidden();
  await expect(box).toHaveValue('/re');

  await box.fill('');
  // The first Enter picks the highlighted command, the second runs it. Each
  // key waits for what the one before it did, or a slow page runs neither.
  const run = async (command: string) => {
    await page.keyboard.type(command);
    await expect(options.first()).toHaveAccessibleName(new RegExp(`^${command}\\. `));
    await expect(options.first()).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('Enter');
    await expect(page.getByRole('button', { name: `Run ${command}` })).toBeVisible();
    await page.keyboard.press('Enter');
  };
  await run('/context');
  const output = page.getByRole('complementary', { name: 'Command output' });
  await expect(output.getByRole('region', { name: 'Output of /context' })).toContainText('mock/mock-1');
  await expect(box).toHaveValue('');

  await run('/env');
  const env = output.getByRole('region', { name: 'Output of /env' });
  await expect(env.getByText('Custom Instructions', { exact: true })).toBeVisible();
  await expect(env.getByRole('listitem').first()).toBeVisible();
  await output.getByRole('button', { name: 'Close command output' }).click();
  await expect(output).toBeHidden();

  // Commands add nothing to the conversation and ask the model nothing.
  await expect(conv.getByText('You:')).toHaveCount(1);
  expect(model.served().map((s) => s.fixture)).toEqual(['reply before commands']);
});

test('a suggested reply shows as ghost text after a turn; Right Arrow takes it without sending; a reload asks for no other', async ({ page, uam, project, model }) => {
  const restore = await useMockUtility(uam);
  try {
    model.reply('should I run the tests?', 'Yes, they cover the change. Shall I run them now?');
    suggest(uam, 'should I run the tests?', 'Run the tests now');
    model.reply('keep going', 'Going'.padEnd(400, '.'), { latency: 250, chunkSize: 4 });
    await startThread(page, project, 'should I run the tests?');
    await expect(threadState(page, 'Finished')).toBeVisible();

    const box = page.getByRole('textbox', { name: 'Message' });
    const ghost = 'Suggestion: Run the tests now, press Right Arrow to use it';
    await expect(box).toHaveAccessibleDescription(ghost);
    await expect(box).toHaveValue('');
    await box.press('ArrowRight');
    await expect(box).toHaveValue('Run the tests now');
    expect(await box.evaluate((el: HTMLTextAreaElement) => el.selectionStart)).toBe('Run the tests now'.length);
    await expect(box).not.toHaveAccessibleDescription(/Suggestion/);

    await box.fill('');
    await expect(box).toHaveAccessibleDescription(ghost);
    await box.pressSequentially('x');
    await expect(box).not.toHaveAccessibleDescription(/Suggestion/);
    await box.press('Backspace');
    await expect(box).toHaveAccessibleDescription(ghost);
    await page.getByRole('button', { name: 'Use suggestion' }).click();
    await expect(box).toHaveValue('Run the tests now');
    await box.fill('');
    // The draft is written shortly after the last change; reload once it no longer holds the suggestion.
    const id = page.url().split('#task=')[1];
    await expect.poll(() => page.evaluate((k) => localStorage.getItem(k) ?? '', `uam.draft.${id}`)).not.toContain('Run the tests now');

    await page.reload();
    await expect(box).toHaveAccessibleDescription(ghost);
    expect(model.served().filter((s) => isSuggestion(s.request))).toHaveLength(1);
    expect(model.served().map((s) => s.fixture)).toEqual(['reply should I run the tests?', 'suggest should I run the tests?']);

    // None while a turn runs.
    await box.fill('keep going');
    await box.press('Enter');
    await expect(threadState(page, 'Working')).toBeVisible();
    await expect(box).toHaveValue('');
    await expect(box).not.toHaveAccessibleDescription(/Suggestion/);
    await page.getByRole('button', { name: 'Stop turn' }).click();
    await expect(threadState(page, 'Stopped')).toBeVisible();
  } finally {
    await restore();
  }
});

test('no suggested reply when the agent asks nothing, and no suggestion call', async ({ page, uam, project, model }) => {
  const restore = await useMockUtility(uam);
  try {
    model.reply('report the build', 'The build passed in 42 s.');
    // Shows if the call is made: the turn must not ask.
    suggest(uam, 'report the build', 'Yes, do it');
    const answered = page.waitForResponse((r) => r.url().endsWith('/suggestions') && r.ok());
    await startThread(page, project, 'report the build');
    await expect(threadState(page, 'Finished')).toBeVisible();
    expect(await (await answered).json()).toMatchObject({ replies: [] });
    await expect(page.getByRole('textbox', { name: 'Message' })).not.toHaveAccessibleDescription(/Suggestion/);
    expect(model.served().map((s) => s.fixture)).toEqual(['reply report the build']);
  } finally {
    await restore();
  }
});

test('@ lists the repository\'s files and directories, untracked included and ignored left out, matched by letters in order; picking inserts the path', async ({ page, project, model, workdir }) => {
  fs.mkdirSync(path.join(workdir, 'src/deep'), { recursive: true });
  fs.writeFileSync(path.join(workdir, 'src/deep/widget.ts'), 'export {};\n');
  fs.writeFileSync(path.join(workdir, 'untracked-note.md'), 'not added\n');
  fs.writeFileSync(path.join(workdir, '.gitignore'), 'secret-ignored.log\n');
  fs.writeFileSync(path.join(workdir, 'secret-ignored.log'), 'ignored\n');
  model.reply('before files', 'Ready for files.');
  model.reply('please read', 'Read it.');
  await startThread(page, project, 'before files');
  await expect(threadState(page, 'Finished')).toBeVisible();

  const box = messageBox(page);
  const list = page.getByRole('listbox', { name: 'Files' });
  const options = list.getByRole('option');
  await box.click();
  await page.keyboard.type('please read @');
  await expect(list.getByRole('option', { name: /README\.md$/ })).toBeVisible();
  await expect(list.getByRole('option', { name: /untracked-note\.md$/ })).toBeVisible();
  // Letters in order, not a substring: "wdgt" finds widget.ts.
  await page.keyboard.type('wdgt');
  await expect(options).toHaveCount(1);
  await expect(options.first()).toHaveAccessibleName('File src/deep/widget.ts');
  for (let i = 0; i < 4; i++) await page.keyboard.press('Backspace');
  await page.keyboard.type('secret');
  await expect(list.getByRole('option', { name: /secret-ignored/ })).toHaveCount(0);
  for (let i = 0; i < 6; i++) await page.keyboard.press('Backspace');
  await page.keyboard.type('deep');
  await expect(list.getByRole('option', { name: /^(Directory|Folder) src\/deep\/?$/ })).toBeVisible();
  for (let i = 0; i < 4; i++) await page.keyboard.press('Backspace');

  await page.keyboard.type('widg');
  await expect(options.first()).toHaveAccessibleName('File src/deep/widget.ts');
  await expect(options.first()).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Enter');
  await expect(list).toBeHidden();
  await expect(box).toHaveValue('please read @src/deep/widget.ts ');
  const chip = page.getByRole('button', { name: 'Remove file reference src/deep/widget.ts' });
  await expect(chip).toBeVisible();

  await page.keyboard.press('Enter');
  await expect(conversation(page).getByText('Read it.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(chip).toHaveCount(0);
  const sent = model.served().find((s) => s.fixture === 'reply please read')!.request;
  expect(JSON.stringify(sent.messages.at(-1))).toContain('src/deep/widget.ts');
});

test('$ lists skills only, the Project\'s among them; Esc closes the list', async ({ page, project, model, workdir }) => {
  fs.mkdirSync(path.join(workdir, '.github/skills/tidy-up'), { recursive: true });
  fs.writeFileSync(path.join(workdir, '.github/skills/tidy-up/SKILL.md'), '---\nname: tidy-up\ndescription: Tidies the fixture.\n---\nTidy.\n');
  model.reply('before skills', 'Ready for skills.');
  await startThread(page, project, 'before skills');
  await expect(threadState(page, 'Finished')).toBeVisible();

  const box = messageBox(page);
  await box.click();
  await page.keyboard.type('$');
  const skills = page.getByRole('listbox', { name: 'Skills' });
  await expect(skills.getByRole('option', { name: '/tidy-up. Tidies the fixture.' })).toBeVisible();
  await expect(skills.getByRole('option', { name: /^\/uam\. / })).toBeVisible();
  const names = await skills.getByRole('option').evaluateAll((els) => els.map((el) => el.getAttribute('aria-label') ?? el.textContent ?? ''));
  await page.keyboard.press('Escape');
  await expect(skills).toBeHidden();
  await expect(box).toHaveValue('$');

  // The same Thread's / list has the commands too; none of them was in the $ list.
  await box.fill('');
  await page.keyboard.type('/');
  const commands = page.getByRole('listbox', { name: 'Commands' });
  await expect(commands.getByRole('option', { name: /^\/compact\. / })).toBeVisible();
  await expect(commands.getByRole('option', { name: '/tidy-up. Tidies the fixture.' })).toBeVisible();
  expect(await commands.getByRole('option').count()).toBeGreaterThan(names.length);
  for (const command of ['btw', 'compact', 'context', 'rename', 'model']) expect(names.filter((n) => n.startsWith(`/${command}`)), names.join()).toEqual([]);
  await page.keyboard.press('Escape');
  await expect(commands).toBeHidden();
  expect(model.served()).toHaveLength(1);
});

test('/rename, /model and /yolo change the Thread; the changes are saved', async ({ page, uam, project, model }) => {
  const restore = await useSecondModel(uam);
  try {
    model.reply('before control', 'Ready for control.').title('before control', 'Control row');
    const id = (await startThread(page, project, 'before control')).split('#task=')[1];
    await expect(threadState(page, 'Finished')).toBeVisible();
    await expect(sidebarRow(page, 'Control row')).toBeVisible();

    await runCommand(page, '/rename Renamed by command');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Renamed by command');
    await expect(sidebarRow(page, 'Renamed by command')).toBeVisible();

    await runCommand(page, `/model mock/${SECOND_MODEL.modelId}`);
    await expect(page.getByRole('button', { name: `Model: ${SECOND_MODEL.displayName}` })).toBeVisible();

    const mode = page.getByRole('button', { name: /^Permissions and execution: / });
    await runCommand(page, '/yolo on', '/allow-all');
    await expect(mode).toHaveAccessibleName('Permissions and execution: Yolo · Interactive');
    expect(await uam.api('GET', `/api/sessions/${id}`)).toMatchObject({ name: 'Renamed by command', model: `mock/${SECOND_MODEL.modelId}`, mode: 'yolo' });
    await runCommand(page, '/yolo off', '/allow-all');
    await expect(mode).toHaveAccessibleName('Permissions and execution: Safe · Interactive');

    await page.reload();
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Renamed by command');
    await expect(page.getByRole('button', { name: `Model: ${SECOND_MODEL.displayName}` })).toBeVisible();
    await expect(mode).toHaveAccessibleName('Permissions and execution: Safe · Interactive');
    // No command asked the model anything.
    expect(model.served().map((s) => s.fixture).filter((f) => !f.startsWith('title'))).toEqual(['reply before control']);
  } finally {
    await restore();
  }
});

test('/compact says Compacting while it runs, then leaves a notice in the conversation', async ({ page, project, model }) => {
  model.reply('something to compact', 'An answer worth keeping.');
  // Compaction is a model call of the Thread's own: it asks for a summary of the conversation.
  model.on({ predicate: (req) => /summar/i.test(lastUserText(req)) }, { content: 'Summary: the user asked for something to compact.' }, { latency: 400, chunkSize: 8 }, 'compaction');
  await startThread(page, project, 'something to compact');
  await expect(threadState(page, 'Finished')).toBeVisible();

  // The command returns when the compaction ends, so the box is not awaited here.
  await messageBox(page).fill('/compact');
  await page.getByRole('button', { name: 'Run /compact', exact: true }).click();
  await expect(page.getByText(/^Compacting the conversation/).first()).toBeVisible();
  await expect(conversation(page).getByText(/^Compacted the conversation/)).toBeVisible();
  await expect(page.getByText(/^Compacting the conversation/)).toHaveCount(0);
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(model.served().map((s) => s.fixture)).toEqual(['reply something to compact', 'compaction']);
  // The earlier turn is still shown.
  await expect(conversation(page).getByText('An answer worth keeping.')).toBeVisible();
});

test('/btw asks an aside: tagged as not sent to the agent, answered in a reader over the composer, nothing added to the conversation', async ({ page, uam, project, model }) => {
  model.reply('main topic', 'The main answer.');
  aside(uam, 'what was the topic?', 'The topic was the main one.');
  await startThread(page, project, 'main topic');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const conv = conversation(page);
  await expect(conv.getByText('You:')).toHaveCount(1);

  const box = messageBox(page);
  const tag = page.getByText('Aside · not sent to the agent', { exact: true });
  await expect(tag).toHaveCount(0);
  const composer = (await page.locator('form').filter({ has: box }).boundingBox())!;
  await box.fill('/btw what was the topic?');
  await expect(tag).toBeVisible();
  await page.getByRole('button', { name: 'Ask aside', exact: true }).click();

  const reader = page.getByRole('dialog', { name: 'Asides in this Thread' });
  await expect(reader.getByText('The topic was the main one.')).toBeVisible();
  await expect(reader.getByText('what was the topic?')).toBeVisible();
  await expect(reader.getByText('after the turn ended')).toBeVisible();
  await expect(reader).toContainText('Never sent to the agent.');
  // Over the composer: the reader stands on the composer's top edge, in its column.
  const r = (await reader.boundingBox())!;
  expect(r.x).toBeGreaterThanOrEqual(composer.x - 1);
  expect(r.x + r.width).toBeLessThanOrEqual(composer.x + composer.width + 1);
  expect(r.y + r.height).toBeLessThanOrEqual(composer.y + composer.height);
  expect(r.y + r.height).toBeGreaterThan(composer.y - 120);
  await page.keyboard.press('Escape');
  await expect(reader).toBeHidden();
  await expect(box).toHaveValue('');
  await expect(tag).toHaveCount(0);

  // The turn carries a chip; the conversation has no new message and the agent was asked nothing.
  await expect(conv.getByRole('button', { name: '1 aside in this turn' })).toBeVisible();
  await expect(conv.getByText('You:')).toHaveCount(1);
  await expect(conv.getByText('what was the topic?')).toHaveCount(0);
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(model.served().map((s) => s.fixture)).toEqual(['reply main topic', 'aside what was the topic?']);
  expect(model.served()[1].request.tools ?? []).toHaveLength(0);

  // Kept with the Thread: a bare /btw lists it after a reload, and the next turn's model request has none of it.
  await page.reload();
  await expect(conv.getByText('The main answer.')).toBeVisible();
  await box.fill('/btw');
  await page.getByRole('button', { name: 'Show asides', exact: true }).click();
  await expect(reader.getByText('The topic was the main one.')).toBeVisible();
  await page.keyboard.press('Escape');
  model.reply('next question', 'The next answer.');
  await send(page, 'next question');
  await expect(conv.getByText('The next answer.')).toBeVisible();
  expect(JSON.stringify(model.served().at(-1)!.request.messages)).not.toContain('The topic was the main one.');
});

test('a command list that fails to load holds slash input and offers Retry commands', async ({ page, project, model }) => {
  model.reply('before the outage', 'Ready.');
  await startThread(page, project, 'before the outage');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const conv = conversation(page);

  let fail = true;
  await page.route('**/api/sessions/*/commands', (route) => (fail ? route.fulfill({ status: 500, json: { error: 'the catalogue is away' } }) : route.continue()));
  const box = messageBox(page);
  await box.click();
  await page.keyboard.type('/compact');
  const retry = page.getByRole('button', { name: 'Retry commands' });
  await expect(retry).toBeVisible();
  await expect(page.getByRole('alert').filter({ has: retry })).toContainText(/the catalogue is away/i);
  // Held: neither Enter nor Send hands the text to the model as a prompt.
  await expect(page.getByRole('button', { name: 'Send. Commands could not be loaded. Retry the command list.' })).toHaveAttribute('aria-disabled', 'true');
  await page.keyboard.press('Enter');
  await page.keyboard.press('Enter');
  await expect(box).toHaveValue('/compact');
  await expect(conv.getByText('You:')).toHaveCount(1);

  fail = false;
  await retry.click();
  await expect(retry).toHaveCount(0);
  await expect(box).toBeFocused();
  await expect(page.getByRole('listbox', { name: 'Commands' }).getByRole('option', { name: /^\/compact\. / })).toBeVisible();
  await expect(box).toHaveValue('/compact');
  await expect(conv.getByText('You:')).toHaveCount(1);
  expect(model.served()).toHaveLength(1);
});
