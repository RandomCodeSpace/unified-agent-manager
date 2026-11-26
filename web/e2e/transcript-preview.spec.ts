// The file preview beside the conversation: a Markdown file rendered or as source, and reloading when the file changes.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test, threadState } from './harness';
import { threadId, yolo } from './transcript-helpers';

test('a Markdown preview renders and toggles to source, reloads in place when a tool edits the file, and Reload reads other changes', async ({ page, project, model, uam, workdir }) => {
  const file = path.join(workdir, 'notes.md');
  model.toolRound('write notes', [{ id: 'call_md', name: 'create', arguments: { path: file, file_text: '# Notes title\n\n- first point\n- second point\n' } }], 'Wrote the notes.');
  model.toolRound('edit notes', [{ id: 'call_edit', name: 'edit', arguments: { path: file, old_str: '- second point', new_str: '- edited point' } }], 'Edited the notes.');
  await startThread(page, project, 'write notes');
  await yolo(page, uam.api);
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();

  // A file tool's row opens what it wrote.
  await conv.getByRole('button', { name: '1 file changed. Show the activity of this turn' }).click();
  await conv.getByRole('button', { name: /^Actions for create / }).click();
  await page.getByRole('menuitem', { name: 'Open notes.md' }).click();
  const preview = page.getByRole('complementary', { name: 'Preview notes.md' });
  await expect(preview.getByRole('heading', { level: 1, name: 'Notes title' })).toBeVisible();
  await expect(preview.getByRole('listitem')).toHaveText(['first point', 'second point']);
  await expect(preview).not.toContainText('# Notes title');

  const as = preview.getByRole('radiogroup', { name: 'Show the file as' });
  await expect(as.getByRole('radio', { name: 'Preview' })).toBeChecked();
  await as.getByRole('radio', { name: 'Source' }).click();
  await expect(as.getByRole('radio', { name: 'Source' })).toBeChecked();
  await expect(preview).toContainText('# Notes title');
  await expect(preview.getByRole('heading', { name: 'Notes title' })).toHaveCount(0);
  await as.getByRole('radio', { name: 'Preview' }).click();
  await expect(preview.getByRole('heading', { level: 1, name: 'Notes title' })).toBeVisible();

  // A tool in the Thread edits the open file: the preview shows the new text without a press.
  await send(page, 'edit notes');
  await expect(conv.getByText('Edited the notes.')).toBeVisible();
  expect(fs.readFileSync(file, 'utf8')).toContain('- edited point');
  await expect(preview.getByRole('listitem')).toHaveText(['first point', 'edited point']);

  // A change from outside the Thread shows after Reload.
  fs.writeFileSync(file, '# Notes title\n\n- changed outside\n');
  await expect(preview.getByRole('listitem')).toHaveText(['first point', 'edited point']);
  await preview.getByRole('button', { name: 'Reload' }).click();
  await expect(preview.getByRole('listitem')).toHaveText(['changed outside']);
});

test('a page built from several files runs in the preview, reloads when its style sheet changes, shows its source and a phone width, and its folder opens in a tab', async ({ page, project, model, uam, workdir }) => {
  const site = path.join(workdir, 'site');
  fs.mkdirSync(site);
  model.toolRound('build the site', [
    { id: 'call_html', name: 'create', arguments: { path: path.join(site, 'index.html'), file_text: '<!doctype html><link rel="stylesheet" href="style.css"><h1>Site title</h1><p id="out"></p><script src="app.js"></script>\n' } },
    { id: 'call_css', name: 'create', arguments: { path: path.join(site, 'style.css'), file_text: 'h1 { color: rgb(200, 0, 0) }\n' } },
    { id: 'call_js', name: 'create', arguments: { path: path.join(site, 'app.js'), file_text: "document.getElementById('out').textContent = 'ran by app.js';\n" } },
  ], 'Built `site/index.html`.');
  model.toolRound('recolour the title', [{ id: 'call_recolour', name: 'edit', arguments: { path: path.join(site, 'style.css'), old_str: 'rgb(200, 0, 0)', new_str: 'rgb(0, 0, 200)' } }], 'Recoloured the title.');
  await startThread(page, project, 'build the site');
  await yolo(page, uam.api);
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();

  await conv.getByRole('link', { name: 'Open site/index.html' }).click();
  const preview = page.getByRole('complementary', { name: 'Preview index.html' });
  const frame = preview.locator('iframe').contentFrame();
  await expect(frame.getByText('ran by app.js')).toBeVisible();
  const colour = () => frame.getByRole('heading', { name: 'Site title' }).evaluate(el => getComputedStyle(el).color);
  await expect.poll(colour).toBe('rgb(200, 0, 0)');

  // The agent edits only the style sheet: the page loads again with it.
  await send(page, 'recolour the title');
  await expect(conv.getByText('Recoloured the title.')).toBeVisible();
  await expect.poll(colour).toBe('rgb(0, 0, 200)');

  await preview.getByRole('radio', { name: 'Phone' }).click();
  await expect.poll(async () => (await preview.locator('iframe').boundingBox())?.width).toBe(390);
  await preview.getByRole('radio', { name: 'Source' }).click();
  await expect(preview).toContainText('<h1>Site title</h1>');
  await expect(preview.locator('iframe')).toHaveCount(0);

  // The folder, named without its slash, opens its index.html with the assets beside it.
  const tab = await page.context().newPage();
  await tab.goto(`${uam.url}/api/sessions/${threadId(page)}/files/view/site`);
  await expect(tab).toHaveURL(/\/files\/key\/[^/]+\/site\/$/);
  await expect(tab.getByText('ran by app.js')).toBeVisible();
  await tab.close();
});
