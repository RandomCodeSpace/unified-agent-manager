// Visuals in replies: Mermaid diagrams, editable drawings, uam_chart cards, and what a reload brings back.
import fs from 'node:fs';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import path from 'node:path';
import { conversation, expect, lastUserText, send, startThread, test, threadState, toolResult } from './harness';
import { expectPinned, pageErrors, yolo } from './transcript-helpers';

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

test('a chart command runs in the Thread folder only in Yolo mode, and one printing over 1 MiB is refused', async ({ page, project, model, uam, workdir }) => {
  const chart = (id: string, title: string, command: string) => ({ id, name: 'uam_chart', arguments: { title, kind: 'bar', x: 'day', y: ['commits'], format: 'csv', command } });
  const rows = "printf 'day,commits\\nMon,3\\nTue,5\\n'";
  model.toolRound('chart in safe mode', [chart('call_safe', 'Safe commits', `touch ran-in-safe; ${rows}`)], 'Safe mode refused it.');
  model.toolRound('chart in yolo mode', [chart('call_yolo', 'Yolo commits', `touch ran-in-yolo; ${rows}`)], 'Yolo drew it.');
  model.toolRound('chart too much', [chart('call_big', 'Too much', 'head -c 1100000 /dev/zero | tr "\\0" a')], 'Too much output.');
  await startThread(page, project, 'chart in safe mode');
  const conv = conversation(page);
  await expect(conv.getByText('Safe mode refused it.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  // Safe: uam runs nothing, and tells the agent to run the command with its shell tool.
  expect(toolResult(model, 'call_safe')).toContain('Safe mode, so uam does not run commands for it');
  expect(fs.existsSync(path.join(workdir, 'ran-in-safe'))).toBe(false);
  await expect(conv.getByRole('figure', { name: 'Chart: Safe commits' })).toHaveCount(0);

  await yolo(page, uam.api);
  await send(page, 'chart in yolo mode');
  await expect(conv.getByText('Yolo drew it.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  // Yolo: it ran in the Thread folder, and the card holds what it printed.
  expect(fs.existsSync(path.join(workdir, 'ran-in-yolo'))).toBe(true);
  const card = conv.getByRole('figure', { name: 'Chart: Yolo commits' });
  await expect(card.locator('canvas, svg').first()).toBeVisible();
  await card.getByRole('button', { name: 'Chart tools' }).click();
  await page.getByRole('button', { name: 'Table', exact: true }).click();
  const table = card.getByRole('region', { name: 'Rows of Yolo commits' });
  await expect(table.getByRole('row', { name: /Mon\s*3/ })).toBeVisible();
  await expect(table.getByRole('row', { name: /Tue\s*5/ })).toBeVisible();
  await page.keyboard.press('Escape');

  await send(page, 'chart too much');
  await expect(conv.getByText('Too much output.')).toBeVisible();
  expect(toolResult(model, 'call_big')).toContain('the command printed more than 1024 KiB');
  await expect(conv.getByRole('figure', { name: 'Chart: Too much' })).toHaveCount(0);
});

test('Use selection in message sends the shapes selected on the drawing with the next message', async ({ page, project, model, workdir }) => {
  model.reply('draw for selecting', 'Edit me:\n\n```mermaid\ngraph TD; A[Alpha]-->B[Beta]\n```\n');
  model.reply('make this red', 'Made it red.');
  await startThread(page, project, 'draw for selecting');
  const conv = conversation(page);
  await expect(conv.getByRole('img', { name: 'Diagram' })).toBeVisible({ timeout: 30_000 });
  await expect(threadState(page, 'Finished')).toBeVisible();
  await conv.getByRole('button', { name: 'Edit as drawing' }).click();
  await page.getByRole('alertdialog', { name: 'Create an editable drawing?' }).getByRole('button', { name: 'Create drawing' }).click();
  const docs = path.join(workdir, 'docs');
  await expect.poll(() => (fs.existsSync(docs) ? fs.readdirSync(docs) : [])).toHaveLength(1);
  const [file] = fs.readdirSync(docs);
  const editor = page.getByRole('dialog', { name: `Edit ${file}` });
  const canvas = editor.locator('canvas').last();
  await expect(canvas).toBeVisible({ timeout: 30_000 });
  const use = editor.getByRole('button', { name: 'Use selection in message' });
  await expect(use).toBeDisabled();

  // Select every shape and arrow on the canvas.
  await canvas.click({ position: { x: 5, y: 5 } });
  await page.keyboard.press('ControlOrMeta+a');
  await use.click();
  await expect(editor).toHaveCount(0);
  // The composer's chip counts them; its × would take them back.
  const chip = page.getByText('· 3 selected', { exact: true });
  await expect(chip).toBeVisible();
  await expect(page.getByRole('button', { name: `Remove the selection in ${file}` })).toBeVisible();

  await send(page, 'make this red');
  await expect(conv.getByText('Made it red.')).toBeVisible();
  const sent = lastUserText(model.served().find((s) => s.fixture === 'reply make this red')!.request);
  expect(sent).toContain(`The user selected in docs/${file}: "Alpha", "Beta", edge "Alpha" → "Beta"`);
  await expect(chip).toHaveCount(0);
});

/**
 * A stand-in for a draw.io page on loopback: it speaks the embed protocol uam uses (init, load,
 * export as PNG or as the selection's JSON) and, on its one button, autosaves the loaded file with
 * Alpha renamed to Gamma, as an edit in draw.io would.
 */
const DRAWIO_STAND_IN = `<!doctype html><title>draw.io stand-in</title><button id="save">Rename Alpha</button><script>
const PNG = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==';
let xml = '';
const post = (m) => parent.postMessage(JSON.stringify(m), '*');
addEventListener('message', (e) => {
  const m = JSON.parse(e.data);
  if (m.action === 'load') { xml = m.xml; post({ event: 'load' }); }
  if (m.action === 'export' && m.format === 'png') post({ event: 'export', format: 'png', data: PNG });
  if (m.action === 'export' && m.format === 'json') post({ event: 'export', format: 'json', data: { pages: [{ cells: [] }] } });
});
document.getElementById('save').onclick = () => post({ event: 'autosave', xml: xml.replaceAll('Alpha', 'Gamma') });
post({ event: 'init' });
</script>`;

test('Settings → Diagram editor checks and enables a draw.io origin, and a drawing then opens and saves in draw.io there', async ({ page, project, model, uam, workdir }) => {
  const server = http.createServer((_, res) => res.writeHead(200, { 'Content-Type': 'text/html' }).end(DRAWIO_STAND_IN));
  server.listen(0, '127.0.0.1');
  await new Promise((r) => server.once('listening', r));
  const origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  model.reply('draw for draw.io', 'Edit me:\n\n```mermaid\ngraph TD; A[Alpha]-->B[Beta]\n```\n');
  try {
    await page.goto('/#settings');
    await page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'Conversation', exact: true }).click();
    const section = page.getByRole('region', { name: 'Diagram editor' }).or(page.locator('section', { has: page.getByRole('heading', { name: 'Diagram editor' }) }));
    await section.getByRole('radio', { name: 'draw.io' }).click();
    const enable = section.getByRole('switch', { name: 'Enable draw.io' });
    await section.getByRole('textbox', { name: 'Origin' }).fill(origin);
    await expect(enable).toBeDisabled();
    await section.getByRole('button', { name: 'Check' }).click();
    await expect(section.getByRole('status').filter({ hasText: 'Self-hosted' })).toHaveText(/Self-hosted · No CORS · Framing allowed/);
    await enable.click();
    await expect(enable).toBeChecked();
    expect(await uam.api('GET', '/api/settings')).toMatchObject({ drawio: { origin, enabled: true } });

    // The page admits the origin's frame from its next load.
    await startThread(page, project, 'draw for draw.io');
    const conv = conversation(page);
    await expect(conv.getByRole('img', { name: 'Diagram' })).toBeVisible({ timeout: 30_000 });
    await expect(threadState(page, 'Finished')).toBeVisible();
    await conv.getByRole('button', { name: 'Edit as drawing' }).click();
    await page.getByRole('alertdialog', { name: 'Create an editable drawing?' }).getByRole('button', { name: 'Create drawing' }).click();
    const docs = path.join(workdir, 'docs');
    await expect.poll(() => (fs.existsSync(docs) ? fs.readdirSync(docs) : [])).toHaveLength(1);
    const [file] = fs.readdirSync(docs);
    expect(file).toMatch(/\.drawio$/);
    expect(fs.readFileSync(path.join(docs, file), 'utf8')).toContain('Alpha');

    const editor = page.getByRole('dialog', { name: `Edit ${file}` });
    const frame = editor.locator(`iframe[src^="${origin}/?embed=1&proto=json"]`);
    await expect(frame).toBeVisible();
    await editor.frameLocator(`iframe[src^="${origin}/"]`).getByRole('button', { name: 'Rename Alpha' }).click();
    await expect.poll(() => fs.readFileSync(path.join(docs, file), 'utf8')).toContain('Gamma');
    expect(fs.readFileSync(path.join(docs, file), 'utf8')).not.toContain('Alpha');
  } finally {
    await uam.api('PATCH', '/api/settings', { drawio: { origin, enabled: false } });
    server.close();
  }
});
