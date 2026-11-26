import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { conversation, expect, newThread, openHome, send, startThread, test, threadState, type Project } from './harness';

/** The mock model as the Copilot provider lists it, for Threads made through the API. */
const MOCK_MODEL_ID = 'mock/mock-1';

type Api = (method: string, route: string, body?: unknown) => Promise<unknown>;

/** Makes a Thread with no message yet (Idle) and returns its ID. */
const idleThread = async (api: Api, project: Project, name: string) =>
  ((await api('POST', '/api/sessions', { project_id: project.id, provider: 'copilot', model: MOCK_MODEL_ID, name })) as { id: string }).id;

const threads = (page: Page) => page.getByRole('navigation', { name: 'Threads' });
const unsettled = (page: Page) => page.getByRole('list', { name: 'Unsettled threads' });
/** A sidebar row by its Thread's title: its name is "<project> [branch] [changes] <state> <title> <time>", the branch only off the Project folder's. */
const rowOf = (page: Page, title: string) => unsettled(page).getByRole('button', { name: new RegExp(` ${title} `) });

test('one flat list across Projects, latest activity first whatever the state; the Project filter is kept; search finds names, titles, Projects, branches and archived Threads', async ({ page, uam, project, model }) => {
  // A second Project on its own branch.
  const dir = path.join(uam.home, 'projects', `other-${Math.random().toString(36).slice(2, 8)}`);
  fs.mkdirSync(dir, { recursive: true });
  execFileSync('git', ['init', '-q', '-b', 'topic-kumquat-branch'], { cwd: dir });
  const other = (await uam.api('POST', '/api/projects', { dir })) as Project;

  model.reply('busy oldest prompt', 'Oldest reply.').title('busy oldest prompt', 'Lychee title oldest');
  await startThread(page, project, 'busy oldest prompt');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const middle = await idleThread(uam.api, other, 'Middle idle row');
  const newest = await idleThread(uam.api, project, 'Newest idle row');
  // The oldest Thread changes last and moves to the top, above the newer Idle ones.
  model.reply('oldest changes again', 'Oldest changed last.');
  await send(page, 'oldest changes again');
  await expect(conversation(page).getByText('Oldest changed last.')).toBeVisible();

  await expect(rowOf(page, 'Middle idle row')).toHaveAccessibleName(new RegExp(`^${other.name} Idle `));
  await expect(rowOf(page, 'Lychee title oldest')).toHaveAccessibleName(new RegExp(`^${project.name} Finished `));
  const top = async (title: string) => (await rowOf(page, title).boundingBox())!.y;
  expect(await top('Lychee title oldest')).toBeLessThan(await top('Newest idle row'));
  expect(await top('Newest idle row')).toBeLessThan(await top('Middle idle row'));

  // The Project filter shows one Project's Threads and is kept per browser.
  await page.getByRole('button', { name: /^Project filter: / }).click();
  await page.getByRole('combobox', { name: 'Search projects' }).fill(other.name);
  await page.getByRole('option', { name: new RegExp(`^${other.name}`) }).click();
  const filter = page.getByRole('button', { name: `Project filter: ${other.name}`, exact: true });
  await expect(filter).toBeVisible();
  await expect(rowOf(page, 'Middle idle row')).toBeVisible();
  await expect(rowOf(page, 'Newest idle row')).toHaveCount(0);
  await page.reload();
  await expect(filter).toBeVisible();
  await expect(rowOf(page, 'Middle idle row')).toBeVisible();
  await expect(rowOf(page, 'Lychee title oldest')).toHaveCount(0);
  await filter.click();
  await page.getByRole('option', { name: 'All projects' }).click();
  await expect(rowOf(page, 'Newest idle row')).toBeVisible();

  // Search: a name, a title, a Project name, a branch, and an archived Thread.
  const search = threads(page).getByRole('searchbox', { name: 'Search threads' });
  const found = (title: string) => threads(page).getByRole('button', { name: new RegExp(` ${title} `) });
  await search.fill('Newest idle');
  await expect(found('Newest idle row')).toBeVisible();
  await expect(found('Middle idle row')).toHaveCount(0);
  await search.fill('Lychee');
  await expect(found('Lychee title oldest')).toBeVisible();
  await expect(found('Newest idle row')).toHaveCount(0);
  await search.fill(other.name);
  await expect(found('Middle idle row')).toBeVisible();
  await expect(found('Newest idle row')).toHaveCount(0);
  await search.fill('kumquat-branch');
  await expect(found('Middle idle row')).toBeVisible();
  await expect(found('Lychee title oldest')).toHaveCount(0);
  await uam.api('POST', `/api/sessions/${newest}/archive`, {});
  await search.fill('Newest idle');
  await expect(found('Newest idle row')).toBeVisible();
  await search.fill('');
  await expect(rowOf(page, 'Newest idle row')).toHaveCount(0);
  await uam.api('POST', `/api/sessions/${middle}/archive`, {});
});

/** Shows only `project`'s Threads, so the list holds no other test's rows. */
async function filterTo(page: Page, project: Project) {
  await page.getByRole('button', { name: /^Project filter: / }).click();
  await page.getByRole('combobox', { name: 'Search projects' }).fill(project.name);
  await page.getByRole('option', { name: new RegExp(`^${project.name}`) }).click();
  await expect(page.getByRole('button', { name: `Project filter: ${project.name}`, exact: true })).toBeVisible();
}

test('the list is one tab stop moved by arrows, End and Enter; a Thread renames from the row menu, F2, a double-click and the header menu, kept after a reload', async ({ page, uam, project }) => {
  const alpha = await idleThread(uam.api, project, 'Kbd alpha');
  const beta = await idleThread(uam.api, project, 'Kbd beta');
  const settled = await idleThread(uam.api, project, 'Kbd settled');
  await uam.api('POST', `/api/sessions/${settled}/settle`, {});
  await openHome(page);
  await filterTo(page, project);
  const a = rowOf(page, 'Kbd alpha');
  const b = rowOf(page, 'Kbd beta');
  const focusedRows = unsettled(page).locator(':focus');

  // Settings sits at the sidebar's foot, just after the list and its shelf.
  const gear = threads(page).getByRole('button', { name: 'Settings', exact: true });
  await gear.focus();
  await page.keyboard.press('Shift+Tab');
  await expect(b).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(focusedRows).toHaveCount(0);
  await expect(threads(page).getByRole('button', { name: /^Settled/ })).not.toBeFocused();
  await page.keyboard.press('Tab');
  await expect(b).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(a).toBeFocused();
  // The stop follows focus: the list is left and re-entered at the row last focused.
  await page.keyboard.press('Tab');
  await expect(gear).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(a).toBeFocused();
  await page.keyboard.press('End');
  const shelf = threads(page).getByRole('button', { name: /^Settled/ });
  await expect(shelf).toBeFocused();
  await page.keyboard.press('ArrowUp');
  await expect(a).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(new RegExp(`#task=${alpha}$`));
  await expect(page.getByRole('heading', { level: 1, name: 'Kbd alpha' })).toBeVisible();

  const nameBox = threads(page).getByRole('textbox', { name: 'Thread name' });
  // The row menu, from the keyboard.
  await a.focus();
  await page.keyboard.press('Shift+F10');
  await page.getByRole('menu').getByRole('menuitem', { name: 'Rename' }).click();
  await nameBox.fill('Kbd alpha menu');
  await page.keyboard.press('Enter');
  await expect(rowOf(page, 'Kbd alpha menu')).toBeFocused();
  await expect(page.getByRole('heading', { level: 1, name: 'Kbd alpha menu' })).toBeVisible();

  // F2 on a row that is not open.
  await b.focus();
  await page.keyboard.press('F2');
  await expect(nameBox).toBeFocused();
  await nameBox.fill('Kbd beta f2');
  await page.keyboard.press('Enter');
  await expect(rowOf(page, 'Kbd beta f2')).toBeFocused();

  // A double-click opens the Thread with its rename.
  await rowOf(page, 'Kbd beta f2').dblclick();
  await expect(page).toHaveURL(new RegExp(`#task=${beta}$`));
  await expect(nameBox).toBeFocused();
  await nameBox.fill('Kbd beta double');
  await page.keyboard.press('Enter');
  await expect(rowOf(page, 'Kbd beta double')).toBeFocused();

  // The header menu.
  await page.getByRole('main').getByRole('button', { name: 'Thread actions' }).click();
  await page.getByRole('menu').getByRole('menuitem', { name: 'Rename' }).click();
  const header = page.getByRole('main').getByRole('textbox', { name: 'Thread name' });
  await header.fill('Kbd beta header');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { level: 1, name: 'Kbd beta header' })).toBeVisible();

  await page.reload();
  await expect(rowOf(page, 'Kbd alpha menu')).toBeVisible();
  await expect(rowOf(page, 'Kbd beta header')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1, name: 'Kbd beta header' })).toBeVisible();
});

/** The row's tip, which spells out its status ("Finished, unread"). */
async function tipOf(page: Page, title: string) {
  await rowOf(page, title).hover();
  const tip = page.getByRole('tooltip');
  await expect(tip).toContainText(title);
  const text = (await tip.textContent()) ?? '';
  await page.mouse.move(0, 0);
  await expect(tip).toBeHidden();
  return text;
}

test('a Thread that finishes while another is open is unread until opened, in each browser on its own', async ({ browser, page, uam, project, model }) => {
  const other = await idleThread(uam.api, project, 'Quiet bystander');
  model.reply('slow late prompt', Array.from({ length: 8 }, (_, i) => `late line ${i + 1}`).join('\n\n'), { latency: 100, chunkSize: 4 });
  model.title('slow late prompt', 'Late worker');
  // A second browser that visited before the turn.
  const second = await browser.newContext({ baseURL: uam.url, viewport: { width: 1280, height: 900 } });
  try {
    expect((await second.request.post('/api/login', { data: { token: uam.token } })).status()).toBe(204);
    const elsewhere = await second.newPage();
    await openHome(elsewhere);
    await filterTo(elsewhere, project);

    await openHome(page);
    await filterTo(page, project);
    const url = await startThread(page, project, 'slow late prompt');
    await rowOf(page, 'Quiet bystander').click();
    await expect(page).toHaveURL(new RegExp(`#task=${other}$`));
    await expect(rowOf(page, 'Late worker')).toHaveAccessibleName(/ Finished /);
    expect(await tipOf(page, 'Late worker')).toContain('Finished, unread');
    await expect(rowOf(elsewhere, 'Late worker')).toHaveAccessibleName(/ Finished /);
    expect(await tipOf(elsewhere, 'Late worker')).toContain('Finished, unread');

    await rowOf(page, 'Late worker').click();
    await expect(page).toHaveURL(url);
    await expect(conversation(page).getByText('late line 8', { exact: true })).toBeVisible();
    await rowOf(page, 'Quiet bystander').click();
    expect(await tipOf(page, 'Late worker')).not.toContain('unread');
    await page.reload();
    expect(await tipOf(page, 'Late worker')).not.toContain('unread');
    // Opening it in one browser leaves it unread in the other.
    expect(await tipOf(elsewhere, 'Late worker')).toContain('Finished, unread');
  } finally {
    await second.close();
  }
});

test('a row counts only the files its own Thread changed, at page load and when a turn ends', async ({ page, project, model, workdir }) => {
  model.title('make the counted file', 'Counted thread');
  model.toolRound('make the counted file', [{ id: 'call_counted', name: 'create', arguments: { path: path.join(workdir, 'counted.txt'), file_text: 'counted\n' } }], 'Counted file made.');
  model.reply('nothing more to change', 'Nothing changed.');
  await startThread(page, project, 'make the counted file');
  await conversation(page).getByRole('button', { name: 'Allow once' }).click();
  await expect(conversation(page).getByText('Counted file made.')).toBeVisible();
  const row = rowOf(page, 'Counted thread');
  await expect(row).toContainText('1 file changed by this thread');

  // Work in the folder that is not the Thread's never counts, also after a reload.
  fs.writeFileSync(path.join(workdir, 'README.md'), '# Changed by hand\n');
  fs.writeFileSync(path.join(workdir, 'by-hand.txt'), 'by hand\n');
  await page.reload();
  await expect(row).toContainText('1 file changed by this thread');
  await send(page, 'nothing more to change');
  await expect(conversation(page).getByText('Nothing changed.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(row).toContainText('1 file changed by this thread');
  await expect(threads(page)).not.toContainText(/not committed|not pushed/);
});

test('Settled: settling while it is closed flashes its header; open, it lists the newest ten, then Show all, scrolling within 40% of the height', async ({ page, uam, project }) => {
  await page.setViewportSize({ width: 1280, height: 600 });
  const names = Array.from({ length: 11 }, (_, i) => `Shelf ${String(i + 1).padStart(2, '0')}`);
  for (const name of names.slice(0, 10)) {
    const id = await idleThread(uam.api, project, name);
    await uam.api('POST', `/api/sessions/${id}/settle`, {});
  }
  await idleThread(uam.api, project, names[10]);
  await openHome(page);
  await filterTo(page, project);
  const header = threads(page).getByRole('button', { name: /^Settled \d+/ });
  const shelfRow = (name: string) => threads(page).getByRole('button', { name: `${project.name}, ${name}`, exact: true });
  await expect(header).toHaveAccessibleName('Settled 10');
  await expect(header).toHaveAttribute('aria-expanded', 'false');
  await expect(shelfRow(names[9])).toBeHidden();

  // A row settled while the shelf is closed: its header flashes where it went.
  await rowOf(page, names[10]).hover();
  await page.getByRole('button', { name: `Settle ${names[10]}` }).click();
  await expect(header).toHaveAccessibleName('Settled 11');
  await expect.poll(() => header.evaluate((el) => el.getAnimations({ subtree: true }).length)).toBeGreaterThan(0);

  await header.click();
  await expect(header).toHaveAttribute('aria-expanded', 'true');
  for (const name of names.slice(1)) await expect(shelfRow(name)).toBeVisible();
  await expect(shelfRow(names[0])).toHaveCount(0);
  // Newest settled first; settled rows stay readable, quiet by colour and not by opacity.
  expect((await shelfRow(names[10]).boundingBox())!.y).toBeLessThan((await shelfRow(names[1]).boundingBox())!.y);
  expect(await shelfRow(names[5]).evaluate((el) => getComputedStyle(el).opacity)).toBe('1');

  await threads(page).getByRole('button', { name: 'Show all 11' }).click();
  await expect(shelfRow(names[0])).toHaveCount(1);
  await expect(threads(page).getByRole('button', { name: 'Show fewer' })).toBeAttached();
  // The open rows scroll on their own within 40% of the window; the page itself does not scroll.
  const scroller = await shelfRow(names[0]).evaluate((el) => {
    let box = el.parentElement;
    while (box && getComputedStyle(box).overflowY !== 'auto') box = box.parentElement;
    return box ? { client: box.clientHeight, scroll: box.scrollHeight, window: window.innerHeight } : null;
  });
  expect(scroller).not.toBeNull();
  expect(scroller!.client).toBeLessThanOrEqual(scroller!.window * 0.4 + 1);
  expect(scroller!.scroll).toBeGreaterThan(scroller!.client);
  await shelfRow(names[10]).scrollIntoViewIfNeeded();
  await expect(shelfRow(names[0])).not.toBeInViewport();
  await shelfRow(names[0]).scrollIntoViewIfNeeded();
  await expect(shelfRow(names[0])).toBeInViewport();
  await expect(shelfRow(names[10])).not.toBeInViewport();
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
  await expect(threads(page).getByRole('searchbox', { name: 'Search threads' })).toBeInViewport();

  await header.click();
  await expect(header).toHaveAttribute('aria-expanded', 'false');
  await expect(shelfRow(names[5])).toBeHidden();
});

test('from 3 characters search also lists the Threads whose conversation holds every word, from the CLI index', async ({ page, uam, project, model }) => {
  model.reply('name a rare fruit', 'The answer is the quuxberry from the hills.');
  const url = await startThread(page, project, 'name a rare fruit');
  await expect(conversation(page).getByText('The answer is the quuxberry from the hills.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  // Named by hand, so no name or title holds the words.
  await uam.api('PATCH', `/api/sessions/${url.split('#task=')[1]}`, { name: 'Fruit chat' });
  await openHome(page);

  const search = threads(page).getByRole('searchbox', { name: 'Search threads' });
  const found = threads(page).getByRole('region', { name: 'In conversations' });
  await search.fill('qu');
  await expect(threads(page).getByRole('status').filter({ hasText: /matching thread|No thread names match/ })).toBeVisible();
  await expect(found).toHaveCount(0);
  await search.fill('quuxberry hills');
  await expect(threads(page).getByText('No thread names match')).toBeVisible();
  const hit = found.getByRole('button', { name: /^Fruit chat/ });
  await expect(hit).toBeVisible();
  await expect(hit.locator('mark').filter({ hasText: 'quuxberry' })).toBeVisible();
  // Every word must be there.
  await search.fill('quuxberry valleys');
  await expect(found.getByText('No conversation mentions it.')).toBeVisible();
  await search.fill('quuxberry hil');
  await hit.click();
  await expect(page).toHaveURL(url);
  await expect(page.getByRole('heading', { level: 1, name: 'Fruit chat' })).toBeVisible();
});

test('the draft picks where the Thread runs; a Thread in its own worktree shows the worktree\'s branch in its row, one on the folder\'s branch shows none', async ({ page, uam, project, model, workdir }) => {
  model.title('run me in a worktree', 'Worktree row thread').reply('run me in a worktree', 'Running in the worktree.');
  await openHome(page);
  await filterTo(page, project);
  await newThread(page, project);
  const place = page.getByRole('radiogroup', { name: 'Where this Thread runs' });
  await expect(place.getByRole('radio', { name: 'Project folder' })).toBeChecked();
  await place.getByRole('radio', { name: 'New worktree' }).click();
  await expect(place.getByRole('radio', { name: 'New worktree' })).toBeChecked();
  await send(page, 'run me in a worktree');
  await expect(conversation(page).getByText('Running in the worktree.')).toBeVisible();
  const trees = execFileSync('git', ['worktree', 'list', '--porcelain'], { cwd: workdir }).toString().split('\n\n').slice(1).filter(Boolean);
  expect(trees).toHaveLength(1);
  const branch = /^branch refs\/heads\/(.*)$/m.exec(trees[0])![1];
  expect(branch).toMatch(/^uam\//);
  // The branch follows the Project's name, before the state.
  await expect(rowOf(page, 'Worktree row thread')).toHaveAccessibleName(new RegExp(`^${project.name} ${branch.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&')} `));
  expect(execFileSync('git', ['branch', '--show-current'], { cwd: workdir }).toString().trim()).toBe('main');
  // A Thread in the folder is on the folder's branch (main, which has no origin to call it the default): only its tip names it.
  await idleThread(uam.api, project, 'Folder row thread');
  await expect(rowOf(page, 'Folder row thread')).toHaveAccessibleName(new RegExp(`^${project.name} Idle Folder row thread \\S+$`));
  await rowOf(page, 'Folder row thread').hover();
  await expect(page.getByRole('tooltip')).toContainText('main');
});

test('the header names its buttons in words: New thread in the bar; the Project filter, Routines and Add project in a row under it, in the phone\'s drawer too', async ({ page, project }) => {
  await openHome(page);
  const named = (name: string) => threads(page).getByRole('button', { name, exact: true });
  const check = async () => {
    await expect(named('New thread')).toHaveText('New thread');
    await expect(named('Project filter: all projects')).toHaveText('All projects');
    await expect(named('Routines')).toHaveText('Routines');
    await expect(named('Add project')).toHaveText('Add project');
    // One row under the bar, inside the sidebar's width.
    const [bar, ...row] = await Promise.all(['New thread', 'Project filter: all projects', 'Routines', 'Add project'].map(async (name) => (await named(name).boundingBox())!));
    const side = (await threads(page).boundingBox())!;
    for (const box of row) {
      expect(box.y).toBeGreaterThanOrEqual(bar.y + bar.height);
      expect(box.y).toBe(row[0].y);
      expect(box.x + box.width).toBeLessThanOrEqual(side.x + side.width);
    }
  };
  await check();
  // The filter's button names the chosen Project, after its badge.
  await filterTo(page, project);
  await expect(named(`Project filter: ${project.name}`)).toContainText(project.name);
  await named(`Project filter: ${project.name}`).click();
  await page.getByRole('option', { name: 'All projects' }).click();
  await named('Routines').click();
  await expect(page).toHaveURL(/#routines$/);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: /^Show sidebar/ }).click();
  await check();
});
