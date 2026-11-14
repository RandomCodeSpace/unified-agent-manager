import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, startThread, test, threadState } from './harness';

test('the files the agent creates and edits are counted and listed in Changes, with their diff', async ({ page, project, model, workdir }) => {
  model.toolRound('make two changes', [
    { id: 'call_create', name: 'create', arguments: { path: path.join(workdir, 'notes.txt'), file_text: 'first line\nsecond line\n' } },
    { id: 'call_edit', name: 'edit', arguments: { path: path.join(workdir, 'README.md'), old_str: '# Fixture', new_str: '# Edited fixture' } },
  ], 'Both files changed.');
  await startThread(page, project, 'make two changes');
  // Safe mode asks for each write; both requests show at once.
  const allow = conversation(page).getByRole('button', { name: 'Allow once' });
  await expect(allow).toHaveCount(2);
  await allow.first().click();
  await expect(allow).toHaveCount(1);
  await allow.click();
  await expect(conversation(page).getByText('Both files changed.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(fs.readFileSync(path.join(workdir, 'notes.txt'), 'utf8')).toBe('first line\nsecond line\n');
  expect(fs.readFileSync(path.join(workdir, 'README.md'), 'utf8')).toBe('# Edited fixture\n');

  const open = page.getByRole('button', { name: /^Open changes, 2 files/ });
  await open.click();
  const panel = page.getByRole('complementary', { name: 'Changes' });
  const files = panel.getByRole('navigation', { name: 'Changed files' }).getByRole('button');
  await expect(files).toHaveCount(2);
  await expect(files.nth(0)).toHaveAccessibleName('Go to README.md, +1 −1');
  await expect(files.nth(1)).toHaveAccessibleName('Go to notes.txt, +2 −0');

  const readme = panel.getByRole('region', { name: 'README.md' });
  await files.nth(0).click();
  await expect(readme.getByRole('row', { name: /- # Fixture$/ })).toBeVisible();
  await expect(readme.getByRole('row', { name: /\+ # Edited fixture$/ })).toBeVisible();
  const notes = panel.getByRole('region', { name: 'notes.txt' });
  await expect(notes.getByRole('row', { name: /\+ second line$/ })).toBeVisible();
});
