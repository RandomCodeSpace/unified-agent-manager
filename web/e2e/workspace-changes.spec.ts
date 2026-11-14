// What Changes attributes to a Thread, and a turn's own changes (docs/web.md, "Changes").
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test, threadState } from './harness';
import { allowEach, bash, capturedTurn, chain, commit, git } from './workspace-helpers';

test("a turn's changes stay as that turn left them after a later turn", async ({ page, project, model, workdir }) => {
  const one = path.join(workdir, 'one.txt');
  model.toolRound('first turn writes', [{ id: 'call_one', name: 'create', arguments: { path: one, file_text: '1\n' } }], 'First turn wrote.');
  model.toolRound('second turn writes', [
    { id: 'call_two', name: 'create', arguments: { path: path.join(workdir, 'two.txt'), file_text: '2\n' } },
    { id: 'call_uno', name: 'edit', arguments: { path: one, old_str: '1', new_str: 'uno' } },
  ], 'Second turn wrote.');
  await startThread(page, project, 'first turn writes');
  const conv = conversation(page);
  await allowEach(page, 1);
  await expect(conv.getByText('First turn wrote.')).toBeVisible();
  await capturedTurn(page, 0);
  await send(page, 'second turn writes');
  await allowEach(page, 2);
  await expect(conv.getByText('Second turn wrote.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await capturedTurn(page, 1);
  await expect(conv.getByRole('button', { name: /^Changed 2 files/ })).toBeVisible();
  const first = conv.getByRole('button', { name: /^Changed 1 file/ });
  await expect(first).toHaveAccessibleName(/^Changed 1 file \+1/);
  await first.click();
  const reader = page.getByRole('dialog', { name: "This turn's changes" });
  await expect(reader.getByRole('listitem')).toHaveCount(1);
  await expect(reader.getByRole('listitem')).toContainText('one.txt');
  await reader.getByRole('button', { name: 'Open current changes' }).click();
  await expect(reader).toBeHidden();
  const files = page.getByRole('complementary', { name: 'Changes' }).getByRole('navigation', { name: 'Changed files' }).getByRole('button');
  await expect(files).toHaveCount(2);
  await expect(files.nth(0)).toHaveAccessibleName(/^Go to one\.txt/);
  await expect(files.nth(1)).toHaveAccessibleName(/^Go to two\.txt/);
  expect(fs.readFileSync(one, 'utf8')).toBe('uno\n');

  await conv.getByRole('button', { name: 'Turn actions' }).first().click();
  await expect(page.getByRole('menuitem', { name: "This turn's changes", exact: true })).toBeVisible();
});

test('a shell-written file reads Session tracking and cannot be reverted; outside changes sit apart', async ({ page, project, model, workdir }) => {
  fs.writeFileSync(path.join(workdir, 'outside.txt'), 'kept\n');
  git(workdir, 'add', 'outside.txt');
  git(workdir, 'commit', '-q', '-m', 'add outside');
  chain(model, 'write by shell and edit', [
    [bash('call_shell', 'echo shell > shell.txt')],
    [{ id: 'call_edit', name: 'edit', arguments: { path: path.join(workdir, 'README.md'), old_str: '# Fixture', new_str: '# Edited' } }],
  ], 'Shell and edit done.');
  await startThread(page, project, 'write by shell and edit');
  await allowEach(page, 1);
  await allowEach(page, 1);
  await expect(conversation(page).getByText('Shell and edit done.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(fs.readFileSync(path.join(workdir, 'shell.txt'), 'utf8')).toBe('shell\n');
  // Changed outside the Thread, after its turn.
  fs.appendFileSync(path.join(workdir, 'outside.txt'), 'x\n');

  await page.getByRole('button', { name: /^Open changes, 2 files/ }).click();
  const panel = page.getByRole('complementary', { name: 'Changes' });
  await panel.getByRole('button', { name: 'Refresh' }).click();
  const shell = panel.getByRole('region', { name: 'shell.txt', exact: true });
  await expect(shell.getByText('Session tracking', { exact: true })).toBeVisible();
  await shell.getByRole('button', { name: 'Actions for shell.txt' }).click();
  await expect(page.getByRole('menuitem', { name: 'Revert file…' })).toBeDisabled();
  await page.keyboard.press('Escape');

  const readme = panel.getByRole('region', { name: 'README.md', exact: true });
  await expect(readme.getByText('Session tracking', { exact: true })).toHaveCount(0);
  await readme.getByRole('button', { name: 'Actions for README.md' }).click();
  await expect(page.getByRole('menuitem', { name: 'Revert file…' })).toBeEnabled();
  await page.keyboard.press('Escape');

  const other = panel.getByRole('region', { name: 'Also changed in this folder' });
  await expect(other.getByRole('listitem')).toHaveCount(1);
  await expect(other.getByRole('listitem')).toContainText('outside.txt');
  await expect(other.getByRole('checkbox')).toHaveCount(0);
  await expect(other.getByRole('button')).toHaveCount(0);
  await expect(panel.getByRole('navigation', { name: 'Changed files' }).getByRole('button', { name: /^Go to outside\.txt/ })).toHaveCount(0);
});

test('a file the Thread committed stays in Changes, and the filters split committed from not committed', async ({ page, project, model, workdir }) => {
  chain(model, 'commit one keep one', [
    [{ id: 'call_c1', name: 'create', arguments: { path: path.join(workdir, 'c1.txt'), file_text: '1\n' } }],
    [bash('call_commit', commit('feat: add c1', 'c1.txt'))],
    [{ id: 'call_c2', name: 'create', arguments: { path: path.join(workdir, 'c2.txt'), file_text: '2\n' } }],
  ], 'Committed one, kept one.');
  await startThread(page, project, 'commit one keep one');
  // One request per round, in turn: create, the commit, create.
  for (let i = 0; i < 3; i++) await allowEach(page, 1);
  await expect(conversation(page).getByText('Committed one, kept one.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(git(workdir, 'log', '-1', '--format=%s')).toBe('feat: add c1');
  expect(git(workdir, 'status', '--porcelain')).toBe('?? c2.txt');

  await page.getByRole('button', { name: /^Open changes, 2 files/ }).click();
  const panel = page.getByRole('complementary', { name: 'Changes' });
  const files = panel.getByRole('navigation', { name: 'Changed files' }).getByRole('button');
  const show = panel.getByRole('radiogroup', { name: 'Show files' });
  await expect(files).toHaveCount(2);
  await expect(panel.getByText('feat: add c1')).toBeVisible();

  await show.getByRole('radio', { name: /^Committed/ }).click();
  await expect(panel.getByRole('region', { name: 'c1.txt', exact: true })).toBeVisible();
  await expect(panel.getByRole('region', { name: 'c2.txt', exact: true })).toHaveCount(0);
  await expect(panel.getByText('feat: add c1')).toBeVisible();

  await show.getByRole('radio', { name: /^Not committed/ }).click();
  await expect(panel.getByRole('region', { name: 'c2.txt', exact: true })).toBeVisible();
  await expect(panel.getByRole('region', { name: 'c1.txt', exact: true })).toHaveCount(0);

  await show.getByRole('radio', { name: /^All/ }).click();
  await expect(panel.getByRole('region', { name: 'c1.txt', exact: true })).toBeVisible();
  await expect(panel.getByRole('region', { name: 'c2.txt', exact: true })).toBeVisible();
});
