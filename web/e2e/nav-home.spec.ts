import path from 'node:path';
import { conversation, expect, newThread, send, test, threadState, type Project } from './harness';
import { ownService } from './shell-helpers';

/** The mock model as the Copilot provider lists it, for Threads made through the API. */
const MOCK_MODEL_ID = 'mock/mock-1';

// Its own service: the Needs you count and Home's lists cover every Thread of the service.
test('Home and the Needs you count: no Project, nothing needs you with New thread in the header, Budgets, then two Threads that need you grouped under Permissions, in the title, the toggle, the app badge and Alt+J / Alt+K', async ({ browser, uam, workdir, model }) => {
  model.title('needs you one', 'Waiting one').toolRound('needs you one', [{ id: 'call_one', name: 'create', arguments: { path: path.join(workdir, 'one.txt'), file_text: 'one\n' } }], 'One written.');
  model.title('needs you two', 'Waiting two').toolRound('needs you two', [{ id: 'call_two', name: 'create', arguments: { path: path.join(workdir, 'two.txt'), file_text: 'two\n' } }], 'Two written.');
  const own = await ownService(browser, uam.mock.url, workdir);
  try {
    const { page, context } = own;
    const api = async (method: string, route: string, data?: unknown) => {
      const res = await context.request.fetch(route, { method, data });
      expect(res.ok(), `${method} ${route}: ${res.status()}`).toBe(true);
      return res.status() === 204 ? null : res.json();
    };
    await context.addInitScript(() => {
      const w = window as unknown as { badges: number[] };
      w.badges = [];
      Object.defineProperty(navigator, 'setAppBadge', { configurable: true, value: async (n: number) => { w.badges.push(n); } });
      Object.defineProperty(navigator, 'clearAppBadge', { configurable: true, value: async () => { w.badges.push(0); } });
    });
    const badge = () => page.evaluate(() => (window as unknown as { badges: number[] }).badges.at(-1) ?? 0);
    const home = page.getByRole('region', { name: 'Home' });
    const heading = (name: string) => home.getByRole('heading', { level: 1, name });

    // No Project yet.
    await api('DELETE', `/api/projects/${own.project.id}`);
    await page.goto('/');
    await expect(heading('Start with a project')).toBeVisible();
    await expect(home.getByRole('button', { name: 'New thread' })).toHaveCount(0);
    await expect(home.getByRole('region', { name: 'Needs you' })).toHaveCount(0);
    await home.getByRole('button', { name: 'Add project' }).click();
    await expect(page.getByRole('dialog', { name: 'Choose a project folder' })).toBeVisible();
    await page.keyboard.press('Escape');

    // A Project with a budgeted Thread, and nothing that needs you.
    const project = (await api('POST', '/api/projects', { dir: workdir })) as Project;
    const budgeted = (await api('POST', '/api/sessions', { project_id: project.id, provider: 'copilot', model: MOCK_MODEL_ID, name: 'Budgeted idle' })) as { id: string };
    await api('PATCH', `/api/sessions/${budgeted.id}`, { budget: { credits: 50 } });
    await page.reload();
    // No hero: the page opens on Needs you, and New thread is its header button.
    await expect(heading('Needs you')).toBeVisible();
    await expect(home.getByText('What are you working on?')).toHaveCount(0);
    await expect(home.getByText('Nothing needs you.')).toBeVisible();
    await home.getByRole('button', { name: 'New thread' }).click();
    await expect(page.getByText(`New thread in ${project.name}`, { exact: true })).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Message' })).toBeVisible();
    await page.goto('/');
    await expect(home.getByRole('region', { name: 'Budgets' }).getByRole('button', { name: /Budgeted idle/ })).toContainText('of 50 AI credits');
    await expect(page).toHaveTitle('UAM');

    // Two Threads that wait on a permission.
    for (const prompt of ['needs you one', 'needs you two']) {
      await newThread(page, project);
      await send(page, prompt);
      await expect(threadState(page, 'Needs permission')).toBeVisible();
    }
    const toggle = page.getByRole('button', { name: /^Hide sidebar/ });
    await page.goto('/');
    await expect(page).toHaveTitle('(2) UAM');
    await expect(toggle).toHaveAccessibleName('Hide sidebar, 2 need you');
    const needs = home.getByRole('region', { name: 'Needs you' });
    await expect(heading('Needs you 2')).toBeVisible();
    await expect(needs.getByRole('button')).toHaveCount(2);
    // Grouped by what you owe: both wait on a permission, so Answers is not shown; the row says only the request.
    await expect(needs.getByRole('heading', { level: 2, name: 'Permissions 2' })).toBeVisible();
    await expect(needs.getByText('Allow or deny one action')).toBeVisible();
    await expect(needs.getByRole('region', { name: 'Answers' })).toHaveCount(0);
    await expect(needs.getByRole('region', { name: 'Permissions' }).getByRole('button', { name: /Waiting two/ })).toContainText('Write a file');
    await expect(needs.getByText(/Needs permission/)).toHaveCount(0);
    await expect(home.getByText('Nothing needs you.')).toHaveCount(0);
    await expect.poll(badge).toBe(2);

    // Alt+J and Alt+K walk them in list order (latest activity first) and wrap.
    await page.keyboard.press('Alt+j');
    await expect(page).toHaveTitle('(2) UAM - Waiting two');
    await page.keyboard.press('Alt+j');
    await expect(page).toHaveTitle('(2) UAM - Waiting one');
    await page.keyboard.press('Alt+j');
    await expect(page).toHaveTitle('(2) UAM - Waiting two');
    await page.keyboard.press('Alt+k');
    await expect(page).toHaveTitle('(2) UAM - Waiting one');
    await expect(page.getByRole('heading', { level: 1, name: 'Waiting one' })).toBeVisible();

    await conversation(page).getByRole('button', { name: 'Allow once' }).click();
    await expect(conversation(page).getByText('One written.')).toBeVisible();
    await expect(page).toHaveTitle('(1) UAM - Waiting one');
    await expect(toggle).toHaveAccessibleName('Hide sidebar, 1 need you');
    await expect.poll(badge).toBe(1);
    await page.goto('/');
    await expect(needs.getByRole('button')).toHaveCount(1);
    await expect(needs.getByRole('button', { name: /Waiting two/ })).toBeVisible();
  } finally {
    await own.stop();
  }
});
