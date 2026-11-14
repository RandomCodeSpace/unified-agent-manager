// A Project folder that is not a git repository (docs/web.md, "No Git").
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, startThread, test, threadState, type Project } from './harness';
import { git } from './workspace-helpers';

test('Set up git here makes the folder a repository and the Thread offers Changes', async ({ page, uam, model, workdir }) => {
  // A sibling of the test's repository, so in no repository; its path still holds the test's directory, which scopes the scripts.
  const dir = `${workdir}-plain`;
  fs.mkdirSync(dir);
  fs.writeFileSync(path.join(dir, 'x.txt'), 'x\n');
  const project = (await uam.api('POST', '/api/projects', { dir })) as Project;
  model.reply('no git here', 'Reply without git.');
  await startThread(page, project, 'no git here');
  await expect(conversation(page).getByText('Reply without git.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  const changes = page.getByRole('button', { name: /^Open changes/ });
  await expect(changes).toHaveCount(0);

  await page.getByRole('button', { name: 'Not a Git repository' }).click();
  const popover = page.getByRole('dialog', { name: 'Not a Git repository' });
  await expect(popover).toContainText(dir);
  await popover.getByRole('button', { name: 'Set up git here' }).click();
  await expect.poll(() => fs.existsSync(path.join(dir, '.git'))).toBe(true);
  expect(git(dir, 'rev-parse', '--is-inside-work-tree')).toBe('true');
  expect(() => git(dir, 'rev-parse', '--verify', 'HEAD')).toThrow();

  // Without a reload.
  await expect(page.getByRole('button', { name: 'Not a Git repository' })).toHaveCount(0);
  await expect(changes).toBeVisible();
});
