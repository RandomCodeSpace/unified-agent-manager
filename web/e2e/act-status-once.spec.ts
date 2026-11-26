// A waiting Thread says its state once per place: while the request shows where it is answered, the header's state
// label and the live line leave the asking to it.
import type { Page } from '@playwright/test';
import { conversation, expect, startThread, test, threadState, type Project } from './harness';

const row = (page: Page, project: Project, title: string) =>
  page.getByRole('list', { name: 'Unsettled threads' }).getByRole('button', { name: new RegExp(`^${project.name} `) }).filter({ hasText: title });
/** The live line's words over the composer, as drawn. */
const liveLine = (page: Page) => page.getByRole('main').getByText('Waiting on you', { exact: true });

test('a permission card: "Needs permission" stands in the sidebar row alone, and the header label returns once answered', async ({ page, project, model }) => {
  model.toolRound('needs a permission', [{ id: 'call_perm', name: 'bash', arguments: { command: 'printf hi > hi.txt', description: 'Say hi' } }], 'Said hi.')
    .title('needs a permission', 'Waiting thread');
  await startThread(page, project, 'needs a permission');
  // The header is still named for the state; the card's title says what is asked.
  await expect(threadState(page, 'Needs permission')).toBeVisible();
  const card = conversation(page).getByRole('group', { name: 'Run shell command' });
  await expect(card.getByRole('heading', { name: 'Run shell command' })).toBeVisible();
  await expect(row(page, project, 'Waiting thread').getByText('Needs permission')).toBeVisible();
  // Regression: the header's label, a chip on the card and the live line each repeated the sidebar row's words.
  await expect(page.getByRole('main').getByText('Needs permission')).toHaveCount(0);
  await expect(page.getByText('Needs permission')).toHaveCount(1);
  // The live line keeps the wait's clock, which nothing else shows, without the state or the tool.
  await expect(liveLine(page)).toBeVisible();
  await expect(page.getByRole('main').getByText('approve bash')).toHaveCount(0);

  await card.getByRole('button', { name: 'Allow once' }).click();
  await expect(conversation(page).getByText('Said hi.')).toBeVisible();
  await expect(threadState(page, 'Finished').getByText('Finished', { exact: true })).toBeVisible();
});

test('a question in the composer: its own "Needs answer" is the one in the Thread, and the header label returns once answered', async ({ page, project, model }) => {
  model.toolRound('ask the colour', [{ id: 'call_colour', name: 'ask_user', arguments: { question: 'Which colour?', choices: ['Blue', 'Red'] } }], 'Colour noted.')
    .title('ask the colour', 'Asking thread');
  await startThread(page, project, 'ask the colour');
  await expect(threadState(page, 'Needs answer')).toBeVisible();
  await expect(page.getByRole('group', { name: 'Which colour?' })).toBeVisible();
  await expect(row(page, project, 'Asking thread').getByText('Needs answer')).toBeVisible();
  await expect(threadState(page, 'Needs answer').getByText('Needs answer')).toHaveCount(0);
  await expect(page.getByRole('main').getByText('Needs answer')).toHaveCount(1);
  await expect(liveLine(page)).toBeVisible();

  await page.getByRole('textbox', { name: 'Your answer' }).press('Enter');
  await expect(conversation(page).getByText('Colour noted.')).toBeVisible();
  await expect(threadState(page, 'Finished').getByText('Finished', { exact: true })).toBeVisible();
});
