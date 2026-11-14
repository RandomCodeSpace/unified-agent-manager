// Rewind to before a prompt, with the files (docs/web.md, "Rewind to before this prompt").
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test, threadState } from './harness';
import { allowEach, capturedTurn } from './workspace-helpers';

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
