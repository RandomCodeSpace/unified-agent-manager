import { expect, startThread, test, threadState } from './harness';

const MOCK_MODEL_ID = 'mock/mock-1';

test('Home Needs you lists a question under Answers with inline Markdown stripped', async ({ page, project, model }) => {
  model.title('ask with markdown', 'Release matrix').toolRound(
    'ask with markdown',
    [{
      id: 'call_md_ask',
      name: 'ask_user',
      arguments: {
        question: 'Pick `darwin` if signing is **not** __ready__ — see [the docs](https://example.com).',
        choices: ['linux/amd64', 'darwin/arm64'],
      },
    }],
    'Noted.',
  );
  await startThread(page, project, 'ask with markdown');
  await expect(threadState(page, 'Needs answer')).toBeVisible();

  await page.goto('/');
  const needs = page.getByRole('region', { name: 'Needs you' });
  await expect(needs.getByRole('heading', { level: 2, name: 'Answers 1' })).toBeVisible();
  await expect(needs.getByText('The agent asked you something')).toBeVisible();
  await expect(needs.getByRole('region', { name: 'Permissions' })).toHaveCount(0);
  const row = needs.getByRole('region', { name: 'Answers' }).getByRole('button', { name: /Release matrix|ask with markdown/ });
  await expect(row).toBeVisible();
  await expect(row).not.toContainText('Needs answer');
  await expect(row).toContainText('Pick darwin if signing is not ready — see the docs.');
  await expect(row).not.toContainText('**');
  await expect(row).not.toContainText('__');
  await expect(row).not.toContainText('`');
  await expect(row).not.toContainText('](');
});

test('opening Settings clears the sidebar Thread highlight until Settings closes', async ({ page, uam, project }) => {
  const id = ((await uam.api('POST', '/api/sessions', {
    project_id: project.id,
    provider: 'copilot',
    model: MOCK_MODEL_ID,
    name: 'Sidebar highlight check',
  })) as { id: string }).id;
  await page.goto(`/#task=${id}`);
  await expect(page.getByRole('heading', { level: 1, name: 'Sidebar highlight check' })).toBeVisible();

  const nav = page.getByRole('navigation', { name: 'Threads' });
  const row = nav.getByRole('button', { name: / Sidebar highlight check / });
  await expect(row).toHaveAttribute('aria-current', 'true');

  await nav.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible();
  await expect(row).not.toHaveAttribute('aria-current', 'true');
  await expect(nav.locator('button[aria-current="true"]')).toHaveCount(0);

  await page.keyboard.press('Escape');
  await expect(page.getByRole('heading', { level: 1, name: 'Sidebar highlight check' })).toBeVisible();
  await expect(row).toHaveAttribute('aria-current', 'true');
});
