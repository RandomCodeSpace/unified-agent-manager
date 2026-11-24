// Threads in their own git worktree (docs/web.md, "Branches and worktrees", "Settle, archive, and delete").
import fs from 'node:fs';
import path from 'node:path';
import { type Page } from '@playwright/test';
import { conversation, expect, send, test, threadState } from './harness';
import { openSettings } from './settings-helpers';
import { allowEach, bash, commit, git, startWorktreeThread, worktrees } from './workspace-helpers';

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

test('settling keeps a worktree that holds an ignored file, and Settings counts the file as not committed', async ({ page, project, model, workdir }) => {
  fs.writeFileSync(path.join(workdir, '.gitignore'), 'out/\nnode_modules/\n');
  git(workdir, 'add', '.gitignore');
  git(workdir, 'commit', '-q', '-m', 'ignore out');
  model.toolRound('write a report', [bash('call_report', 'mkdir -p out node_modules && echo r > out/report.csv && echo x > node_modules/x')], 'Wrote the report.');
  const tree = await startWorktreeThread(page, project, 'write a report');
  await allowEach(page, 1);
  await expect(conversation(page).getByText('Wrote the report.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(git(tree.path, 'status', '--porcelain')).toBe('');

  // Nothing git tracks is pending, so it settles without asking.
  await settle(page);
  await expect(threadState(page, 'Settled')).toBeVisible();

  await openSettings(page, 'Projects & worktrees');
  const row = page.getByRole('list', { name: 'Worktrees' }).getByRole('listitem').filter({ hasText: tree.branch });
  // out/ counts; node_modules/ is made again by an install and does not.
  await expect(row).toContainText('1 not committed');
  expect(fs.readFileSync(path.join(tree.path, 'out/report.csv'), 'utf8')).toBe('r\n');
  expect(worktrees(project.dir)).toEqual([tree]);

  await row.getByRole('button', { name: `Remove worktree ${tree.branch}` }).click();
  const confirm = page.getByRole('alertdialog');
  await expect(confirm).toContainText('1 file is not committed and will be lost.');
  await confirm.getByRole('button', { name: 'Cancel' }).click();
  expect(fs.existsSync(path.join(tree.path, 'out/report.csv'))).toBe(true);
});

test("a new worktree starts where the repository's worktreeBaseRef says and goes where the user's worktreePathTemplate says", async ({ page, uam, project, model, workdir }) => {
  // origin's default branch is main; the Project folder works on topic, a commit ahead.
  const origin = `${workdir}-origin.git`;
  git(path.dirname(workdir), 'clone', '-q', '--bare', workdir, origin);
  git(workdir, 'remote', 'add', 'origin', origin);
  git(workdir, 'fetch', '-q', 'origin');
  git(workdir, 'remote', 'set-head', 'origin', 'main');
  git(workdir, 'switch', '-q', '-c', 'topic');
  fs.mkdirSync(path.join(workdir, '.github', 'copilot'), { recursive: true });
  // A repository cannot choose where it is checked out: its template is not used.
  fs.writeFileSync(path.join(workdir, '.github', 'copilot', 'settings.json'), JSON.stringify({ worktreeBaseRef: 'defaultBranch', worktreePathTemplate: `${workdir}-repo-says/{branchSlug}` }));
  fs.writeFileSync(path.join(workdir, 'topic.txt'), 'topic\n');
  git(workdir, 'add', '.github/copilot/settings.json', 'topic.txt');
  git(workdir, 'commit', '-q', '-m', 'topic');
  const main = git(workdir, 'rev-parse', 'origin/main');

  const user = path.join(uam.home, '.copilot', 'settings.json');
  const before = fs.existsSync(user) ? fs.readFileSync(user, 'utf8') : null;
  const template = `${uam.home}/trees/{repo}/{branchSlug}`;
  fs.mkdirSync(path.dirname(user), { recursive: true });
  fs.writeFileSync(user, JSON.stringify({ ...(before ? JSON.parse(before) : {}), worktreePathTemplate: template }));
  try {
    model.reply('start from the default branch', 'Started from the default branch.');
    const tree = await startWorktreeThread(page, project, 'start from the default branch');
    await expect(conversation(page).getByText('Started from the default branch.')).toBeVisible();
    expect(tree.path).toBe(`${uam.home}/trees/${path.basename(workdir)}/${tree.branch.replaceAll('/', '-')}`);
    expect(fs.existsSync(`${workdir}-repo-says`)).toBe(false);
    expect(git(tree.path, 'rev-parse', 'HEAD')).toBe(main);
    expect(fs.existsSync(path.join(tree.path, 'topic.txt'))).toBe(false);
    const system = JSON.stringify(model.served()[0].request.messages.filter((m) => m.role === 'system'));
    expect(system).toContain(`on the branch ${tree.branch}, started from main`);
    expect(git(workdir, 'branch', '--show-current')).toBe('topic');
  } finally {
    if (before === null) fs.rmSync(user, { force: true });
    else fs.writeFileSync(user, before);
  }
});

test('Branch from here on a worktree Thread makes a new worktree on a new branch at its commit, with its uncommitted files', async ({ page, project, model, workdir }) => {
  fs.writeFileSync(path.join(workdir, '.gitignore'), '*.log\n');
  git(workdir, 'add', '.gitignore');
  git(workdir, 'commit', '-q', '-m', 'ignore logs');
  model.toolRound('work to branch from', [bash('call_work', `echo c > c.txt && ${commit('feat: c', 'c.txt')} && echo dirty >> README.md && echo u > u.txt && echo ignored > x.log`)], 'Work to branch from.');
  const source = await startWorktreeThread(page, project, 'work to branch from', true);
  const conv = conversation(page);
  await expect(conv.getByText('Work to branch from.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  const head = git(source.path, 'rev-parse', 'HEAD');
  const status = git(source.path, 'status', '--porcelain');
  const url = page.url();

  await conv.getByRole('button', { name: 'Turn actions' }).first().click();
  await page.getByRole('menuitem', { name: 'Branch from here' }).click();
  const fork = page.getByRole('dialog', { name: 'Branch from here' });
  await fork.getByRole('button', { name: 'Branch thread' }).click();
  await expect(page).not.toHaveURL(url);
  await expect.poll(() => worktrees(project.dir).length).toBe(2);
  const branched = worktrees(project.dir).find((w) => w.path !== source.path)!;

  expect(branched.branch).toMatch(/^uam\//);
  expect(branched.branch).not.toBe(source.branch);
  expect(git(branched.path, 'rev-parse', 'HEAD')).toBe(head);
  expect(fs.readFileSync(path.join(branched.path, 'README.md'), 'utf8')).toBe('# Fixture\ndirty\n');
  expect(fs.readFileSync(path.join(branched.path, 'u.txt'), 'utf8')).toBe('u\n');
  expect(fs.existsSync(path.join(branched.path, 'x.log'))).toBe(false);
  await expect(page.getByRole('button', { name: `Worktree, branch ${branched.branch}`, exact: true })).toBeVisible();
  // The source is as it was.
  expect(git(source.path, 'rev-parse', 'HEAD')).toBe(head);
  expect(git(source.path, 'status', '--porcelain')).toBe(status);
  expect(fs.readFileSync(path.join(source.path, 'x.log'), 'utf8')).toBe('ignored\n');
});

test('Settings lists the worktrees biggest first, with each Thread, its stage and what is not saved', async ({ page, project, model }) => {
  model.title('make a big tree', 'Big tree');
  model.title('make a small tree', 'Small tree');
  model.toolRound('make a big tree', [bash('call_big', 'head -c 3000000 /dev/zero > big.bin')], 'Big tree made.');
  model.toolRound('make a small tree', [bash('call_small', 'echo s >> README.md')], 'Small tree made.');
  const big = await startWorktreeThread(page, project, 'make a big tree', true);
  await expect(conversation(page).getByText('Big tree made.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  const small = await startWorktreeThread(page, project, 'make a small tree', true);
  await expect(conversation(page).getByText('Small tree made.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await settle(page);
  await page.getByRole('alertdialog', { name: 'Settle with unsaved work?' }).getByRole('button', { name: 'Settle anyway' }).click();
  await expect(threadState(page, 'Settled')).toBeVisible();

  await openSettings(page, 'Projects & worktrees');
  const rows = page.getByRole('list', { name: 'Worktrees' }).getByRole('listitem');
  const bigRow = rows.filter({ hasText: big.branch });
  const smallRow = rows.filter({ hasText: small.branch });
  await expect(bigRow).toContainText('Big tree');
  await expect(bigRow).toContainText('1 not committed');
  await expect(bigRow).toContainText('3 MB');
  await expect(bigRow).not.toContainText('Settled');
  await expect(smallRow).toContainText('Small tree');
  await expect(smallRow).toContainText('Settled');
  await expect(smallRow).toContainText('1 not committed');
  // Other tests of this worker may have left worktrees; these two keep their order among them.
  const order = await rows.evaluateAll((items, branches) => branches.map((b) => items.findIndex((li) => li.textContent?.includes(b))), [big.branch, small.branch]);
  expect(order[0]).toBeGreaterThanOrEqual(0);
  expect(order[0]).toBeLessThan(order[1]);
});
