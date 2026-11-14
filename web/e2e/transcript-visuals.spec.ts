// Visuals in replies: Mermaid diagrams, editable drawings, uam_chart cards, and what a reload brings back.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test, threadState } from './harness';
import { expectPinned, pageErrors } from './transcript-helpers';

test.use({ permissions: ['clipboard-read', 'clipboard-write'] });

const COMMITS = {
  title: 'Commits per day', kind: 'bar', x: 'day', y: ['commits'],
  data: [{ day: 'Mon', commits: 3 }, { day: 'Tue', commits: 5 }, { day: 'Wed', commits: 2 }],
};

test('a Mermaid diagram renders from the sandboxed frame, toggles to its code, opens large, and a broken one says why', async ({ page, project, model }) => {
  const errors = pageErrors(page);
  model.reply('draw a flow', 'A flow:\n\n```mermaid\ngraph TD; A[Start]-->B[End]\n```\n');
  model.reply('draw a broken flow', 'Broken:\n\n```mermaid\ngraph TD; A-->\n```\n');
  await startThread(page, project, 'draw a flow');
  const conv = conversation(page);
  const diagram = conv.getByRole('img', { name: 'Diagram' });
  await expect(diagram).toBeVisible({ timeout: 30_000 });
  expect(await diagram.getAttribute('src')).toMatch(/^data:image\/svg\+xml/);
  await expect(page.locator('iframe[sandbox="allow-scripts"][src$="/diagram-frame.html"]')).toBeAttached();

  const view = conv.getByRole('group', { name: 'View' });
  await view.getByRole('button', { name: 'Code' }).click();
  await expect(diagram).toHaveCount(0);
  await expect(conv.locator('pre')).toContainText('A[Start]-->B[End]');
  await view.getByRole('button', { name: 'Diagram' }).click();
  await expect(diagram).toBeVisible();

  await conv.getByRole('button', { name: 'Open diagram' }).click();
  const large = page.getByRole('dialog').or(page.getByRole('complementary', { name: 'Preview Diagram' }));
  await expect(large.getByRole('img', { name: 'Diagram' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(large).toHaveCount(0);

  await send(page, 'draw a broken flow');
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByText(/^Diagram could not be rendered: \S/)).toBeVisible({ timeout: 30_000 });
  await expect(conv.locator('pre', { hasText: 'graph TD; A-->' }).last()).toBeVisible();
  await expect(diagram).toHaveCount(1);
  expect(errors).toEqual([]);
});

test('Edit as drawing: Cancel writes nothing, Create writes the drawing and opens it, Delete removes it', async ({ page, project, model, workdir }) => {
  const errors = pageErrors(page);
  model.reply('draw for editing', 'Edit me:\n\n```mermaid\ngraph TD; A-->B\n```\n');
  await startThread(page, project, 'draw for editing');
  const conv = conversation(page);
  await expect(conv.getByRole('img', { name: 'Diagram' })).toBeVisible({ timeout: 30_000 });
  await expect(threadState(page, 'Finished')).toBeVisible();
  const docs = path.join(workdir, 'docs');
  const drawings = () => (fs.existsSync(docs) ? fs.readdirSync(docs).filter((f) => f.endsWith('.excalidraw')) : []);

  const ask = page.getByRole('alertdialog', { name: 'Create an editable drawing?' });
  await conv.getByRole('button', { name: 'Edit as drawing' }).click();
  await ask.getByRole('button', { name: 'Cancel' }).click();
  await expect(ask).toHaveCount(0);
  expect(drawings()).toEqual([]);

  await conv.getByRole('button', { name: 'Edit as drawing' }).click();
  await ask.getByRole('button', { name: 'Create drawing' }).click();
  await expect.poll(drawings).toHaveLength(1);
  const [file] = drawings();
  expect(JSON.parse(fs.readFileSync(path.join(docs, file), 'utf8')).type).toBe('excalidraw');
  // A new drawing opens in the editor.
  const editor = page.getByRole('dialog', { name: `Edit ${file}` });
  await expect(editor.locator('canvas').first()).toBeVisible({ timeout: 30_000 });
  await editor.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(editor).toHaveCount(0);
  await expect(conv.getByRole('img', { name: 'Diagram' })).toBeVisible();
  await expect(conv.getByRole('button', { name: 'Open drawing' })).toBeVisible();
  await expect(conv.getByRole('button', { name: 'Edit drawing' })).toBeVisible();

  await conv.getByRole('button', { name: 'Delete', exact: true }).click();
  const remove = page.getByRole('alertdialog', { name: `Delete ${file}?` });
  await remove.getByRole('button', { name: 'Delete drawing' }).click();
  await expect(conv.getByRole('button', { name: 'Edit drawing' })).toHaveCount(0);
  expect(drawings()).toEqual([]);
  await expect(conv.getByRole('button', { name: 'Edit as drawing' })).toBeVisible();
  expect(errors).toEqual([]);
});

test('a uam_chart card draws, shows its rows as a table, copies CSV, survives a reload and retries a failed read', async ({ page, project, model }) => {
  model.toolRound('chart the commits', [{ id: 'call_chart', name: 'uam_chart', arguments: COMMITS }], 'Here is the chart.');
  await startThread(page, project, 'chart the commits');
  const conv = conversation(page);
  await expect(conv.getByText('Here is the chart.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  const card = conv.getByRole('figure', { name: 'Chart: Commits per day' });
  const drawn = card.locator('canvas, svg').first();
  await expect(drawn).toBeVisible();
  expect(await drawn.evaluate((el) => el.getBoundingClientRect().width * el.getBoundingClientRect().height)).toBeGreaterThan(0);

  const tools = card.getByRole('button', { name: 'Chart tools' });
  await tools.click();
  await page.getByRole('button', { name: 'Table', exact: true }).click();
  const rows = card.getByRole('region', { name: 'Rows of Commits per day' });
  await expect(rows.getByRole('row', { name: /Mon\s*3/ })).toBeVisible();
  await expect(rows.getByRole('row', { name: /Tue\s*5/ })).toBeVisible();
  await expect(rows.getByRole('row', { name: /Wed\s*2/ })).toBeVisible();
  // The tools stay open after Table; close them, then open them afresh.
  const copy = page.getByRole('button', { name: 'Copy CSV' });
  await page.keyboard.press('Escape');
  await expect(copy).toHaveCount(0);
  await page.mouse.move(0, 0);
  await tools.click();
  await copy.click();
  await expect(page.getByRole('button', { name: 'Copied' })).toBeVisible();
  const csv = await page.evaluate(() => navigator.clipboard.readText());
  expect(csv.trim().split(/\r?\n/)).toEqual(['day,commits', 'Mon,3', 'Tue,5', 'Wed,2']);
  await page.getByRole('button', { name: 'Chart', exact: true }).click();
  await expect(rows).toHaveCount(0);
  await expect(drawn).toBeVisible();

  await page.reload();
  await expect(card.locator('canvas, svg').first()).toBeVisible();

  let failed = false;
  await page.route(/\/api\/sessions\/[^/]+\/chart\?/, (route) => {
    if (failed) return route.continue();
    failed = true;
    return route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'mock chart failure' }) });
  });
  await page.reload();
  const alert = conv.getByRole('alert').filter({ hasText: 'Could not load this chart:' });
  await expect(alert).toBeVisible();
  await alert.getByRole('button', { name: 'Retry' }).click();
  await expect(card.locator('canvas, svg').first()).toBeVisible();
  await expect(alert).toHaveCount(0);
});

test('a chart option the library rejects shows its reason in place, and the Thread carries on', async ({ page, project, model }) => {
  const errors = pageErrors(page);
  model.toolRound('chart something odd', [{
    id: 'call_odd', name: 'uam_chart', arguments: { title: 'Odd chart', kind: 'echarts', options: { series: [{ type: 'line', data: [1, 2, 3] }] } },
  }], 'after chart');
  model.reply('carry on', 'Carried on fine.');
  await startThread(page, project, 'chart something odd');
  const conv = conversation(page);
  await expect(conv.getByText('after chart')).toBeVisible();
  await expect(conv.getByText(/^The chart could not be drawn: \S/)).toBeVisible();
  const card = conv.getByRole('figure', { name: 'Chart: Odd chart' });
  await card.getByRole('button', { name: 'Chart tools' }).click();
  await expect(page.getByRole('button', { name: 'Copy JSON' })).toBeVisible();
  await page.keyboard.press('Escape');

  await send(page, 'carry on');
  await expect(conv.getByText('Carried on fine.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(errors).toEqual([]);
});

test('a reload restores a finished rich conversation: table, diagram, code, chart and sign-off', async ({ page, project, model }) => {
  model.reply('rich turn one', [
    '| Name | Size |\n|---|---|\n| alpha | 1 |\n| beta | 2 |',
    '```mermaid\ngraph TD; A-->B\n```',
    '```go\nfunc main() {}\n```',
  ].join('\n\n'));
  model.toolRound('rich turn two', [{ id: 'call_rich', name: 'uam_chart', arguments: COMMITS }], 'Done');
  await startThread(page, project, 'rich turn one');
  const conv = conversation(page);
  await expect(conv.getByRole('img', { name: 'Diagram' })).toBeVisible({ timeout: 30_000 });
  await expect(threadState(page, 'Finished')).toBeVisible();
  await send(page, 'rich turn two');
  await expect(conv.getByText('Done', { exact: true })).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByRole('button', { name: /^Took \S+\. Show this turn's waybill$/ }).last()).toBeVisible();

  await page.reload();
  await expect(conv.getByRole('row', { name: /alpha\s*1/ })).toBeVisible();
  await expect(conv.getByRole('row', { name: /beta\s*2/ })).toBeVisible();
  await expect(conv.getByRole('img', { name: 'Diagram' })).toBeAttached({ timeout: 30_000 });
  await expect(conv.locator('pre', { hasText: 'func main() {}' })).toBeAttached();
  await expect(conv.getByRole('button', { name: 'Copy code' })).toHaveCount(2);
  await expect(conv.getByRole('figure', { name: 'Chart: Commits per day' })).toBeVisible();
  await expect(conv.getByRole('button', { name: /^Took \S+\. Show this turn's waybill$/ }).last()).toBeVisible();
  for (const prompt of ['rich turn one', 'rich turn two']) await expect(conv.getByText(prompt, { exact: true })).toHaveCount(1);
  await expectPinned(page);
});
