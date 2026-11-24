// A Thread's branch in the Project folder, and the git rules agents get (docs/web.md, "Branches and worktrees", "Commits & pull requests").
import { type Page } from '@playwright/test';
import { conversation, expect, newThread, openHome, send, test, threadState, type Model } from './harness';
import { openSettings } from './settings-helpers';
import { git } from './workspace-helpers';

/** Picks `option` in a draft's branch menu, which opens from the button named `Branch <current>`. */
async function pickBranch(page: Page, current: string, option: RegExp) {
  await page.getByRole('button', { name: `Branch ${current}`, exact: true }).click();
  const menu = page.getByRole('dialog', { name: 'Switch the branch' });
  await menu.getByRole('option', { name: option }).click();
  await expect(menu).toBeHidden();
}

/** Starts a Thread with `prompt` in a draft already set up, and waits for its reply. */
async function sendAndFinish(page: Page, model: Model, prompt: string) {
  model.reply(prompt, `Done: ${prompt}.`);
  await send(page, prompt);
  await expect(page).toHaveURL(/#task=[0-9a-f-]{36}$/);
  await expect(conversation(page).getByText(`Done: ${prompt}.`)).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
}

test('New branch makes a uam/ branch in the Project folder, and Switch checks out an existing one', async ({ page, project, model, workdir }) => {
  git(workdir, 'branch', 'feature/x');
  await openHome(page);
  await newThread(page, project);
  await pickBranch(page, 'main', /^New branch from main/);
  await expect(page.getByRole('button', { name: 'Branch New branch', exact: true })).toBeVisible();
  await sendAndFinish(page, model, 'start on a new branch');
  const made = git(workdir, 'branch', '--show-current');
  expect(made).toMatch(/^uam\/[0-9a-f]{8}$/);
  await expect(page.getByRole('button', { name: `Branch ${made}`, exact: true })).toBeVisible();

  await newThread(page, project);
  await pickBranch(page, made, /^feature\/x/);
  await expect(page.getByRole('button', { name: 'Branch feature/x', exact: true })).toBeVisible();
  await sendAndFinish(page, model, 'switch to feature x');
  expect(git(workdir, 'branch', '--show-current')).toBe('feature/x');
  // A switch makes no branch.
  expect(git(workdir, 'branch', '--list').split('\n').map((b) => b.replace(/^[*+ ]+/, '')).sort()).toEqual(['feature/x', 'main', made].sort());
});

/** The system message of the first request the agent of the Thread started with `prompt` sent. */
const systemOf = (model: Model, prompt: string) => {
  const served = model.served().find((s) => s.fixture === `reply ${prompt}`);
  return JSON.stringify(served?.request.messages.filter((m) => m.role === 'system') ?? []);
};

test('the commit and pull request instructions save and reset in Settings and reach Threads opened afterwards, with the co-author rule', async ({ page, uam, project, model }) => {
  const start = async (prompt: string) => {
    model.reply(prompt, `Done: ${prompt}.`);
    await openHome(page);
    await newThread(page, project);
    await send(page, prompt);
    await expect(conversation(page).getByText(`Done: ${prompt}.`)).toBeVisible();
  };
  try {
    await start('rules by default');
    const byDefault = systemOf(model, 'rules by default');
    expect(byDefault).toContain('Follow Conventional Commits');
    expect(byDefault).toContain('no Co-authored-by trailer and no line crediting an AI, agent or tool');

    await openSettings(page, 'Instructions');
    const commits = page.getByRole('textbox', { name: 'Commit messages' });
    const requests = page.getByRole('textbox', { name: 'Pull and merge requests' });
    const formOf = (box: typeof commits) => page.locator('form').filter({ has: box });
    await expect(commits).toHaveValue('');
    await expect(commits).toHaveAttribute('placeholder', /^Follow Conventional Commits/);
    await commits.fill('Start every subject with WIDGET.');
    await formOf(commits).getByRole('button', { name: 'Save' }).click();
    await requests.fill('Title merge requests with GADGET.');
    await formOf(requests).getByRole('button', { name: 'Save' }).click();
    await expect.poll(async () => (await uam.api('GET', '/api/settings')) as Record<string, unknown>).toMatchObject({ commit_prompt: 'Start every subject with WIDGET.', gitlab_mr_prompt: 'Title merge requests with GADGET.' });
    await page.getByRole('switch', { name: 'Co-author' }).click();
    await page.getByRole('textbox', { name: 'Co-author name' }).fill('Ada <ada@example.com>');
    await formOf(page.getByRole('textbox', { name: 'Co-author name' })).getByRole('button', { name: 'Save' }).click();
    await expect.poll(async () => ((await uam.api('GET', '/api/settings')) as { coauthor?: unknown }).coauthor).toEqual({ enabled: true, name: 'Ada <ada@example.com>' });
    await page.reload();
    await openSettings(page, 'Instructions');
    await expect(commits).toHaveValue('Start every subject with WIDGET.');
    await expect(requests).toHaveValue('Title merge requests with GADGET.');

    await start('rules once saved');
    const saved = systemOf(model, 'rules once saved');
    expect(saved).toContain('Start every subject with WIDGET.');
    expect(saved).toContain('Title merge requests with GADGET.');
    expect(saved).not.toContain('Follow Conventional Commits');
    expect(saved).toContain('End every commit message with the trailer \\"Co-authored-by: Ada <ada@example.com>\\"');

    await openSettings(page, 'Instructions');
    await formOf(commits).getByRole('button', { name: 'Reset to default' }).click();
    await expect(commits).toHaveValue('');
    await formOf(requests).getByRole('button', { name: 'Reset to default' }).click();
    await expect(requests).toHaveValue('');
    // Empty is the default, which the settings leave out.
    await expect.poll(async () => {
      const s = (await uam.api('GET', '/api/settings')) as Record<string, unknown>;
      return [s.commit_prompt ?? '', s.gitlab_mr_prompt ?? ''];
    }).toEqual(['', '']);
  } finally {
    await uam.api('PATCH', '/api/settings', { commit_prompt: '', gitlab_mr_prompt: '', coauthor: { enabled: false, name: '' } });
  }
});
