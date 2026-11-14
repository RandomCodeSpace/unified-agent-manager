import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, newThread, send, startThread, test, threadState, type Project } from './harness';
import type { Page } from '@playwright/test';

const unsettled = (page: Page) => page.getByRole('list', { name: 'Unsettled threads' });
/** The sidebar row of the Thread titled `title`: its name is "<project> [changes] <state> <title> <branch> <time>". */
const row = (page: Page, project: Project, title: string, state = '\\S+') =>
  unsettled(page).getByRole('button', { name: new RegExp(`^${project.name} (.* )?${state} ${title} `) });
/** The open Thread's compact row under the Settled shelf (pinned below it while the shelf is closed). */
const settledRow = (page: Page, project: Project, title: string) =>
  page.getByRole('navigation', { name: 'Threads' }).getByRole('button', { name: new RegExp(`^${project.name}, ${title}`) });
const menu = async (page: Page) => {
  await page.getByRole('main').getByRole('button', { name: 'Thread actions' }).click();
  return page.getByRole('menu');
};

test('the sidebar row follows the turn through a permission request, and Settle and Archive wait for it', async ({ page, project, model, workdir }) => {
  model.title('make the held file', 'Held thread');
  model.toolRound('make the held file', [{ id: 'call_held', name: 'create', arguments: { path: path.join(workdir, 'held.txt'), file_text: 'held\n' } }], 'Held file made.');
  await startThread(page, project, 'make the held file');
  await expect(threadState(page, 'Needs permission')).toBeVisible();
  await expect(row(page, project, 'Held thread', 'Needs permission')).toBeVisible();

  const items = await menu(page);
  await expect(items.getByRole('menuitem', { name: /^Settle/ })).toHaveAttribute('aria-disabled', 'true');
  await expect(items.getByRole('menuitem', { name: /^Archive/ })).toHaveAttribute('aria-disabled', 'true');
  await expect(items.getByText(/Stop the turn, answer what is waiting/).first()).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(items).toBeHidden();
  await expect(page.getByRole('button', { name: 'Settle Held thread' })).toHaveCount(0);

  await conversation(page).getByRole('button', { name: 'Allow once' }).click();
  await expect(conversation(page).getByText('Held file made.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(row(page, project, 'Held thread', 'Finished')).toBeVisible();
});

test('Settle with the Thread\'s own unsaved file asks once beside the row; Settle anyway keeps the file', async ({ page, project, model, workdir }) => {
  const file = path.join(workdir, 'notes.txt');
  model.title('write the notes file', 'Notes thread');
  model.toolRound('write the notes file', [{ id: 'call_notes', name: 'create', arguments: { path: file, file_text: 'x\n' } }], 'Notes created.');
  await startThread(page, project, 'write the notes file');
  await conversation(page).getByRole('button', { name: 'Allow once' }).click();
  await expect(threadState(page, 'Finished')).toBeVisible();
  const own = row(page, project, 'Notes thread', 'Finished');
  await expect(own).toContainText('1 file changed by this thread');

  const guard = page.getByRole('alertdialog', { name: 'Settle with unsaved work?' });
  const settle = page.getByRole('button', { name: 'Settle Notes thread' });
  // The open popover makes the sidebar inert, so the row is measured first.
  const at = await own.boundingBox();
  await own.hover();
  await settle.click();
  await expect(guard).toBeVisible();
  await expect(page.getByRole('alertdialog')).toHaveCount(1);
  await expect(guard.getByRole('list', { name: 'Not committed' })).toHaveText('notes.txt');
  await expect(guard.getByRole('button', { name: 'Open Changes' })).toBeVisible();
  const box = await guard.boundingBox();
  expect(box!.y).toBeLessThanOrEqual(at!.y + at!.height + 24);
  expect(box!.y + box!.height).toBeGreaterThanOrEqual(at!.y - 24);

  await guard.getByRole('button', { name: 'Cancel' }).click();
  await expect(guard).toBeHidden();
  await expect(own).toBeVisible();

  await own.hover();
  await settle.click();
  await guard.getByRole('button', { name: 'Settle anyway' }).click();
  await expect(guard).toBeHidden();
  await expect(unsettled(page).getByRole('button', { name: / Notes thread / })).toHaveCount(0);
  await expect(settledRow(page, project, 'Notes thread')).toBeVisible();
  expect(fs.readFileSync(file, 'utf8')).toBe('x\n');
});

test('Archive counts only the Thread\'s own files in a shared folder', async ({ page, project, model, workdir }) => {
  fs.writeFileSync(path.join(workdir, 'mine.txt'), 'by hand\n');
  model.title('quiet thread here', 'Quiet thread').reply('quiet thread here', 'Nothing to change.');
  model.title('write the b file', 'Writer thread');
  model.toolRound('write the b file', [{ id: 'call_b', name: 'create', arguments: { path: path.join(workdir, 'b.txt'), file_text: 'b\n' } }], 'B written.');
  const quiet = await startThread(page, project, 'quiet thread here');
  await expect(threadState(page, 'Finished')).toBeVisible();
  await newThread(page, project);
  await send(page, 'write the b file');
  await conversation(page).getByRole('button', { name: 'Allow once' }).click();
  await expect(conversation(page).getByText('B written.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  const writer = page.url();

  await page.goto(quiet);
  await expect(page.getByRole('heading', { level: 1, name: 'Quiet thread' })).toBeVisible();
  await (await menu(page)).getByRole('menuitem', { name: 'Archive' }).click();
  const plain = page.getByRole('alertdialog', { name: 'Archive this thread?' });
  await expect(plain).toBeVisible();
  await plain.getByRole('button', { name: 'Archive thread' }).click();
  await expect(plain).toBeHidden();
  await expect(unsettled(page).getByRole('button', { name: / Quiet thread / })).toHaveCount(0);

  await page.goto(writer);
  await expect(page.getByRole('heading', { level: 1, name: 'Writer thread' })).toBeVisible();
  await (await menu(page)).getByRole('menuitem', { name: 'Archive' }).click();
  const guard = page.getByRole('alertdialog', { name: 'Archive with unsaved work?' });
  await expect(guard.getByRole('list', { name: 'Not committed' })).toHaveText('b.txt');
  await guard.getByRole('button', { name: 'Archive anyway' }).click();
  await expect(guard).toBeHidden();
  await expect(unsettled(page).getByRole('button', { name: / Writer thread / })).toHaveCount(0);
  expect(fs.existsSync(path.join(workdir, 'mine.txt')) && fs.existsSync(path.join(workdir, 'b.txt'))).toBe(true);
});

test('a clean Thread settles without asking, opens read-only and Reopen continues the same conversation', async ({ page, project, model }) => {
  model.title('remember the word kumquat', 'Kumquat thread').reply('remember the word kumquat', 'Noted the word.');
  model.reply('which word did I give', 'kumquat it was');
  await startThread(page, project, 'remember the word kumquat');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const own = row(page, project, 'Kumquat thread', 'Finished');
  await own.hover();
  await page.getByRole('button', { name: 'Settle Kumquat thread' }).click();
  await expect(page.getByRole('alertdialog')).toHaveCount(0);
  await expect(own).toHaveCount(0);
  await expect(settledRow(page, project, 'Kumquat thread')).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'Message' })).toHaveCount(0);

  await (await menu(page)).getByRole('menuitem', { name: 'Reopen' }).click();
  await expect(row(page, project, 'Kumquat thread', 'Idle')).toBeVisible();
  await send(page, 'which word did I give');
  await expect(conversation(page).getByText('kumquat it was')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(own).toBeVisible();
  const second = model.served().find((s) => s.fixture === 'reply which word did I give')!.request;
  expect(second.messages.some((m) => m.role === 'user' && JSON.stringify(m.content).includes('remember the word kumquat'))).toBe(true);
});

test('an archived Thread is found in Settings, unarchives as Finished without a turn, and Delete removes it', async ({ page, project, model }) => {
  model.title('zeta archive prompt', 'Zeta thread').reply('zeta archive prompt', 'Zeta reply.');
  const url = await startThread(page, project, 'zeta archive prompt');
  const id = url.split('#task=')[1];
  await expect(threadState(page, 'Finished')).toBeVisible();
  const archive = async () => {
    await (await menu(page)).getByRole('menuitem', { name: 'Archive' }).click();
    await page.getByRole('alertdialog', { name: 'Archive this thread?' }).getByRole('button', { name: 'Archive thread' }).click();
    await expect(unsettled(page).getByRole('button', { name: / Zeta thread / })).toHaveCount(0);
  };
  await archive();
  const served = model.served().length;

  await page.getByRole('searchbox', { name: 'Search threads' }).fill('Zeta');
  await expect(page.getByRole('navigation', { name: 'Threads' }).getByRole('button', { name: /Zeta thread/ })).toBeVisible();
  await page.getByRole('searchbox', { name: 'Search threads' }).fill('');

  const archived = async () => {
    await page.goto('/#settings');
    await page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'Archived', exact: true }).click();
    return page.getByRole('list', { name: 'Archived threads' });
  };
  let list = await archived();
  await expect(list.getByRole('listitem').filter({ hasText: 'Zeta thread' })).toContainText(project.name);
  await list.getByRole('listitem').filter({ hasText: 'Zeta thread' }).getByRole('button', { name: /Zeta thread/ }).first().click();
  await expect(page).toHaveURL(new RegExp(`#task=${id}$`));
  await expect(page.getByRole('textbox', { name: 'Message' })).toHaveCount(0);

  list = await archived();
  await list.getByRole('listitem').filter({ hasText: 'Zeta thread' }).getByRole('button', { name: 'Actions for Zeta thread' }).click();
  await page.getByRole('menuitem', { name: 'Unarchive' }).click();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(row(page, project, 'Zeta thread', 'Finished')).toBeVisible();
  expect(model.served()).toHaveLength(served);

  await archive();
  list = await archived();
  await list.getByRole('listitem').filter({ hasText: 'Zeta thread' }).getByRole('button', { name: 'Actions for Zeta thread' }).click();
  await page.getByRole('menuitem', { name: 'Delete' }).click();
  const confirm = page.getByRole('alertdialog', { name: /Delete “Zeta thread”\?/ });
  await confirm.getByRole('button', { name: 'Delete' }).click();
  await expect(list.getByRole('listitem').filter({ hasText: 'Zeta thread' })).toHaveCount(0);
  const sessions = await (await page.request.get('/api/sessions')).text();
  expect(sessions).not.toContain(id);
});
