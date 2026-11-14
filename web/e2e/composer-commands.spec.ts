// What the composer offers beyond plain text: the CLI's slash commands and the suggested reply.
import { isSuggestion, suggest, useMockUtility } from './composer-helpers';
import { conversation, expect, startThread, test, threadState } from './harness';

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
  await page.keyboard.type('/context');
  // The first Enter picks the highlighted command, the second runs it.
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: 'Run /context' })).toBeVisible();
  await page.keyboard.press('Enter');
  const output = page.getByRole('complementary', { name: 'Command output' });
  await expect(output.getByRole('region', { name: 'Output of /context' })).toContainText('mock/mock-1');
  await expect(box).toHaveValue('');

  await page.keyboard.type('/env');
  await page.keyboard.press('Enter');
  await page.keyboard.press('Enter');
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
    model.reply('should I run the tests?', 'Yes, they cover the change.');
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
