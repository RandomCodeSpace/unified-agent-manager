// Rewind to before a prompt, with the files (docs/web.md, "Rewind to before this prompt").
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test, threadState } from './harness';
import { allowEach, capturedTurn, startYoloThread } from './workspace-helpers';

test('rewinding the last turn with its files restores the disk and shortens the conversation', async ({ page, project, model, workdir }) => {
  const readme = path.join(workdir, 'README.md');
  const created = path.join(workdir, 'new.txt');
  model.reply('first rewind prompt', 'First turn kept.');
  model.toolRound('second rewind prompt', [
    { id: 'call_edit', name: 'edit', arguments: { path: readme, old_str: '# Fixture', new_str: '# Changed fixture' } },
    { id: 'call_create', name: 'create', arguments: { path: created, file_text: 'x\ny\n' } },
  ], 'Second turn edited.');
  model.reply('third rewind prompt', 'Third turn after the rewind.');
  await startThread(page, project, 'first rewind prompt');
  const conv = conversation(page);
  await expect(conv.getByText('First turn kept.')).toBeVisible();
  await send(page, 'second rewind prompt');
  await allowEach(page, 2);
  await expect(conv.getByText('Second turn edited.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(fs.readFileSync(readme, 'utf8')).toBe('# Changed fixture\n');
  await capturedTurn(page, 0);

  await conv.getByRole('button', { name: 'Turn actions' }).nth(1).click();
  await page.getByRole('menuitem', { name: 'Rewind to before this prompt…' }).click();
  const panel = page.getByRole('dialog', { name: 'Undo this turn?' });
  const files = panel.getByRole('region', { name: 'Files' });
  await expect(files).toContainText('2 files restored');
  await expect(files.getByRole('listitem').filter({ hasText: 'new.txt' })).toContainText('−2 removed');
  await expect(files.getByRole('listitem').filter({ hasText: 'README.md' })).toBeVisible();
  await expect(panel.getByRole('radio', { name: 'Conversation and files' })).toBeChecked();
  await panel.getByRole('button', { name: 'Undo turn' }).click();

  const done = page.getByRole('dialog', { name: 'Rewind' });
  await expect(done.getByRole('status')).toHaveText('Rewound.');
  await expect(done.getByRole('listitem').filter({ hasText: 'README.md' })).toContainText('restored');
  await expect(done.getByRole('listitem').filter({ hasText: 'new.txt' })).toContainText('restored');
  expect(fs.readFileSync(readme, 'utf8')).toBe('# Fixture\n');
  expect(fs.existsSync(created)).toBe(false);
  await done.getByRole('button', { name: 'Close' }).click();

  await expect(conv.getByText('Second turn edited.')).toHaveCount(0);
  await expect(conv.getByText('second rewind prompt')).toHaveCount(0);
  await expect(conv.getByText('first rewind prompt')).toBeVisible();
  await expect(conv.getByText('First turn kept.')).toBeVisible();
  await send(page, 'third rewind prompt');
  await expect(conv.getByText('Third turn after the rewind.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
});

test('a file edited outside after the rewind preview is kept, never overwritten', async ({ page, project, model, workdir }) => {
  const readme = path.join(workdir, 'README.md');
  const created = path.join(workdir, 'new.txt');
  model.toolRound('first stale prompt', [{ id: 'call_edit', name: 'edit', arguments: { path: readme, old_str: '# Fixture', new_str: '# Version one' } }], 'First turn edited.');
  model.toolRound('second stale prompt', [{ id: 'call_create', name: 'create', arguments: { path: created, file_text: 'later\n' } }], 'Second turn created.');
  await startThread(page, project, 'first stale prompt');
  const conv = conversation(page);
  await allowEach(page, 1);
  await expect(conv.getByText('First turn edited.')).toBeVisible();
  await capturedTurn(page, 0);
  await send(page, 'second stale prompt');
  await allowEach(page, 1);
  await expect(conv.getByText('Second turn created.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await capturedTurn(page, 1);

  await conv.getByRole('button', { name: 'Turn actions' }).first().click();
  await page.getByRole('menuitem', { name: 'Rewind to before this prompt…' }).click();
  const panel = page.getByRole('dialog', { name: 'Rewind to before this prompt?' });
  await expect(panel).toContainText('the 1 after it');
  await expect(panel.getByRole('region', { name: 'Files' })).toContainText('2 files restored');
  // Written after the preview was read: the rewind must leave it alone.
  fs.writeFileSync(readme, 'user\n');
  const rewind = panel.getByRole('button', { name: 'Rewind', exact: true });
  await rewind.click();

  // A stale preview is refused and read again, then needs a second press.
  const done = page.getByRole('dialog', { name: 'Rewind', exact: true });
  await expect(done.or(panel.getByRole('alert'))).toBeVisible();
  if (await panel.getByRole('alert').isVisible()) {
    await expect(panel.getByRole('region', { name: 'Files' })).toBeVisible();
    await rewind.click();
  }
  await expect(done.getByRole('listitem').filter({ hasText: 'README.md' })).toContainText('kept');
  await expect(done.getByRole('listitem').filter({ hasText: 'new.txt' })).toContainText('restored');
  expect(fs.readFileSync(readme, 'utf8')).toBe('user\n');
  expect(fs.existsSync(created)).toBe(false);
  await done.getByRole('button', { name: 'Close' }).click();
  await expect(conv.getByText('first stale prompt')).toHaveCount(0);
  await expect(conv.getByText('second stale prompt')).toHaveCount(0);
});

test('Conversation only shortens the conversation and leaves every file as it is', async ({ page, project, model, workdir }) => {
  const readme = path.join(workdir, 'README.md');
  const created = path.join(workdir, 'kept.txt');
  model.toolRound('first keep prompt', [{ id: 'call_edit', name: 'edit', arguments: { path: readme, old_str: '# Fixture', new_str: '# Kept edit' } }], 'First edit made.');
  model.toolRound('second keep prompt', [{ id: 'call_create', name: 'create', arguments: { path: created, file_text: 'kept\n' } }], 'Second file made.');
  await startYoloThread(page, project, 'first keep prompt');
  const conv = conversation(page);
  await expect(conv.getByText('First edit made.')).toBeVisible();
  await capturedTurn(page, 0);
  await send(page, 'second keep prompt');
  await expect(conv.getByText('Second file made.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await capturedTurn(page, 1);

  // Both turns' menus are back once the Thread is idle again.
  await expect(conv.getByRole('button', { name: 'Turn actions' })).toHaveCount(2);
  await conv.getByRole('button', { name: 'Turn actions' }).first().click();
  await page.getByRole('menuitem', { name: 'Rewind to before this prompt…' }).click();
  const panel = page.getByRole('dialog', { name: 'Rewind to before this prompt?' });
  await expect(panel.getByRole('region', { name: 'Files' })).toContainText('2 files restored');
  await panel.getByRole('radio', { name: 'Conversation only' }).click();
  await expect(panel.getByRole('radio', { name: 'Conversation only' })).toBeChecked();
  await panel.getByRole('button', { name: 'Rewind', exact: true }).click();
  const done = page.getByRole('dialog', { name: 'Rewind', exact: true });
  await expect(done.getByRole('status')).toHaveText('Rewound.');
  await expect(done.getByRole('listitem')).toHaveCount(0);
  await done.getByRole('button', { name: 'Close' }).click();

  await expect(conv.getByText('first keep prompt')).toHaveCount(0);
  await expect(conv.getByText('Second file made.')).toHaveCount(0);
  expect(fs.readFileSync(readme, 'utf8')).toBe('# Kept edit\n');
  expect(fs.readFileSync(created, 'utf8')).toBe('kept\n');
});

test('Rewind is offered only while the Thread is idle: not while it works, waits for an answer or runs a background command', async ({ page, project, model }) => {
  model.reply('idle gate one', 'Gate one done.');
  model.toolRound('idle gate slow', [{ id: 'call_slow', name: 'bash', arguments: { command: 'sleep 120', description: 'Long sleep' } }], 'Slept.');
  model.toolRound('idle gate ask', [{ id: 'call_ask', name: 'ask_user', arguments: { question: 'Go on?', choices: ['Yes', 'No'] } }], 'Answered.');
  model.toolRound('idle gate background', [{ id: 'call_bg', name: 'bash', arguments: { command: 'sleep 300', description: 'Sleeper', mode: 'async', detach: true } }], 'Sleeper started.');
  await startYoloThread(page, project, 'idle gate one');
  const conv = conversation(page);
  await expect(conv.getByText('Gate one done.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  const first = conv.getByRole('button', { name: 'Turn actions' }).first();
  const rewind = page.getByRole('menuitem', { name: 'Rewind to before this prompt…' });
  const offered = async (yes: boolean) => {
    // Until the Thread is idle again, no turn menu on screen offers it (past turns may show none at all).
    if (!yes) {
      for (const menu of await conv.getByRole('button', { name: 'Turn actions' }).all()) {
        await menu.click();
        await expect(page.getByRole('menu', { name: 'Turn actions' })).toBeVisible();
        await expect(rewind).toHaveCount(0);
        await page.keyboard.press('Escape');
        await expect(page.getByRole('menu')).toBeHidden();
      }
      return;
    }
    await first.click();
    await expect(rewind).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('menu')).toBeHidden();
  };
  await offered(true);

  await send(page, 'idle gate slow');
  await expect(conv.getByRole('button', { name: /sleep 120 , running/ })).toBeVisible();
  await expect(threadState(page, 'Working')).toBeVisible();
  await offered(false);
  await page.getByRole('button', { name: 'Stop turn' }).click();
  await expect(threadState(page, 'Stopped')).toBeVisible();

  await send(page, 'idle gate ask');
  await expect(threadState(page, 'Needs answer')).toBeVisible();
  await offered(false);
  await page.getByRole('textbox', { name: 'Your answer' }).fill('Yes');
  await page.getByRole('button', { name: 'Answer', exact: true }).click();
  await expect(conv.getByText('Answered.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await send(page, 'idle gate background');
  await expect(conv.getByText('Sleeper started.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Background tasks: 1 running' })).toBeVisible();
  await offered(false);
  await page.getByRole('button', { name: 'Background tasks: 1 running' }).click();
  const tasks = page.getByRole('dialog', { name: 'Background tasks' });
  await tasks.getByRole('button', { name: 'Stop background task: Sleeper' }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: /^Stop/ }).click();
  await expect(tasks.getByRole('listitem').filter({ hasText: 'Sleeper' })).toContainText('Cancelled');
  await tasks.getByRole('button', { name: 'Close' }).click();
  await expect(page.getByRole('button', { name: 'Background tasks: 0 running' })).toBeVisible();
  await offered(true);
});

test('Edit and resend rewinds to before the prompt and sends the edited text once', async ({ page, project, model }) => {
  model.reply('resend original prompt', 'Original reply.');
  model.reply('a later prompt', 'Later reply.');
  model.reply('resend edited prompt', 'Edited reply.');
  await startYoloThread(page, project, 'resend original prompt');
  const conv = conversation(page);
  await expect(conv.getByText('Original reply.')).toBeVisible();
  await send(page, 'a later prompt');
  await expect(conv.getByText('Later reply.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await conv.getByRole('button', { name: 'Turn actions' }).first().click();
  await page.getByRole('menuitem', { name: 'Edit and resend…' }).click();
  const composer = page.getByRole('textbox', { name: 'Message' });
  await expect(composer).toHaveValue('resend original prompt');
  const strip = page.getByRole('group', { name: 'Editing a past prompt' });
  await expect(strip.getByRole('status')).toHaveText('Removes this prompt and the 1 after it');
  // Nothing changes until the send.
  await expect(conv.getByText('Later reply.')).toBeVisible();
  await composer.fill('resend edited prompt');
  await page.getByRole('button', { name: 'Rewind and send', exact: true }).click();

  await expect(conv.getByText('Edited reply.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(strip).toHaveCount(0);
  await expect(conv.getByText('resend edited prompt')).toBeVisible();
  await expect(conv.getByText('resend original prompt')).toHaveCount(0);
  await expect(conv.getByText('Original reply.')).toHaveCount(0);
  await expect(conv.getByText('Later reply.')).toHaveCount(0);
  const edited = model.served().filter((s) => s.fixture === 'reply resend edited prompt');
  expect(edited).toHaveLength(1);
  // The model saw the edit alone, without the rewound prompts.
  const said = JSON.stringify(edited[0].request.messages);
  expect(said).not.toContain('resend original prompt');
  expect(said).not.toContain('a later prompt');
});
