// Threads in their own git worktree (docs/web.md, "Branches and worktrees", "Settle, archive, and delete").
import fs from 'node:fs';
import path from 'node:path';
import { type Page } from '@playwright/test';
import { conversation, expect, newThread, openHome, send, test, threadState, type Project } from './harness';
import { allowEach, bash, commit, git, worktrees } from './workspace-helpers';

/** Starts a Thread in a new worktree of `project` with `prompt`, and returns the worktree git made. */
async function startWorktreeThread(page: Page, project: Project, prompt: string) {
  await openHome(page);
  await newThread(page, project);
  await page.getByRole('radiogroup', { name: 'Where this Thread runs' }).getByRole('radio', { name: 'New worktree' }).click();
  await send(page, prompt);
  await expect(page).toHaveURL(/#task=[0-9a-f-]{36}$/);
  await expect.poll(() => worktrees(project.dir).length).toBe(1);
  return worktrees(project.dir)[0];
}

/** Settles the open Thread from its header menu. */
async function settle(page: Page) {
  await page.getByRole('button', { name: 'Thread actions' }).click();
  await page.getByRole('menuitem', { name: /^Settle/ }).click();
}

test("a Thread in a new worktree works on its own uam/ branch, and the Project folder's branch is untouched", async ({ page, project, model, workdir }) => {
  model.toolRound('work in a worktree', [bash('call_where', 'pwd > where.txt && echo w > b.txt')], 'Worked in the worktree.');
  const tree = await startWorktreeThread(page, project, 'work in a worktree');
  await allowEach(page, 1);
  await expect(conversation(page).getByText('Worked in the worktree.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  expect(tree.branch).toMatch(/^uam\//);
  expect(path.dirname(tree.path)).toBe(`${workdir}.worktrees`);
  expect(fs.readFileSync(path.join(tree.path, 'where.txt'), 'utf8').trim()).toBe(tree.path);
  expect(fs.readFileSync(path.join(tree.path, 'b.txt'), 'utf8')).toBe('w\n');
  expect(fs.existsSync(path.join(workdir, 'b.txt'))).toBe(false);
  expect(fs.existsSync(path.join(workdir, 'where.txt'))).toBe(false);
  expect(git(workdir, 'branch', '--show-current')).toBe('main');

  await expect(page.getByRole('button', { name: `Worktree, branch ${tree.branch}`, exact: true })).toBeVisible();
  await page.getByRole('button', { name: /^Open changes, 2 files/ }).click();
  const files = page.getByRole('complementary', { name: 'Changes' }).getByRole('navigation', { name: 'Changed files' }).getByRole('button');
  await expect(files).toHaveCount(2);
  await expect(files.nth(0)).toHaveAccessibleName(/^Go to b\.txt/);
  await expect(files.nth(1)).toHaveAccessibleName(/^Go to where\.txt/);
});

test('settling a worktree Thread asks over uncommitted work; once committed it settles, removes the worktree, keeps the branch, and Reopen brings it back', async ({ page, project, model }) => {
  model.toolRound('leave dirty work', [bash('call_dirty', 'echo dirty >> README.md')], 'Left a dirty file.');
  model.toolRound('commit the work', [bash('call_commit', commit('feat: d', 'README.md'))], 'Committed the work.');
  model.toolRound('where am I now', [bash('call_back', 'git branch --show-current > br.txt && pwd >> br.txt')], 'Back in the worktree.');
  const tree = await startWorktreeThread(page, project, 'leave dirty work');
  const conv = conversation(page);
  await allowEach(page, 1);
  await expect(conv.getByText('Left a dirty file.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await settle(page);
  const guard = page.getByRole('alertdialog', { name: 'Settle with unsaved work?' });
  await expect(guard.getByRole('list', { name: 'Not committed' })).toHaveText('README.md');
  await expect(guard.getByRole('button', { name: 'Open Changes' })).toBeVisible();
  await expect(guard.getByRole('button', { name: 'Settle anyway' })).toBeVisible();
  await guard.getByRole('button', { name: 'Cancel' }).click();
  await expect(guard).toBeHidden();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await send(page, 'commit the work');
  await allowEach(page, 1);
  await expect(conv.getByText('Committed the work.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(git(tree.path, 'status', '--porcelain')).toBe('');

  // Clean, and no remote to push to: it settles at once.
  await settle(page);
  await expect(threadState(page, 'Settled')).toBeVisible();
  await expect(guard).toHaveCount(0);
  await expect.poll(() => worktrees(project.dir)).toEqual([]);
  expect(fs.existsSync(tree.path)).toBe(false);
  expect(git(project.dir, 'log', '-1', '--format=%s', tree.branch)).toBe('feat: d');

  await page.getByRole('button', { name: 'Thread actions' }).click();
  await page.getByRole('menuitem', { name: 'Reopen' }).click();
  await send(page, 'where am I now');
  await allowEach(page, 1);
  await expect(conv.getByText('Back in the worktree.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(worktrees(project.dir)).toEqual([tree]);
  expect(fs.readFileSync(path.join(tree.path, 'br.txt'), 'utf8')).toBe(`${tree.branch}\n${tree.path}\n`);
  expect(fs.readFileSync(path.join(tree.path, 'README.md'), 'utf8')).toBe('# Fixture\ndirty\n');
  expect(git(project.dir, 'branch', '--list', 'uam/*').split('\n')).toHaveLength(1);
  await expect(page.getByRole('button', { name: `Worktree, branch ${tree.branch}`, exact: true })).toBeVisible();
});

test('Settle anyway on a worktree with uncommitted work keeps its folder', async ({ page, project, model }) => {
  model.toolRound('leave work behind', [bash('call_left', 'echo left >> README.md && echo new > new.txt')], 'Left work behind.');
  const tree = await startWorktreeThread(page, project, 'leave work behind');
  await allowEach(page, 1);
  await expect(conversation(page).getByText('Left work behind.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await settle(page);
  const guard = page.getByRole('alertdialog', { name: 'Settle with unsaved work?' });
  await expect(guard.getByRole('list', { name: 'Not committed' })).toHaveText('README.md');
  await guard.getByRole('button', { name: 'Settle anyway' }).click();
  await expect(guard).toBeHidden();
  await expect(threadState(page, 'Settled')).toBeVisible();
  expect(worktrees(project.dir)).toEqual([tree]);
  expect(fs.readFileSync(path.join(tree.path, 'README.md'), 'utf8')).toBe('# Fixture\nleft\n');
  expect(fs.readFileSync(path.join(tree.path, 'new.txt'), 'utf8')).toBe('new\n');
});
