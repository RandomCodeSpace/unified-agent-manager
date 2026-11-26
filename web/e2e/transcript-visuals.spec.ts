// Visuals in replies: Mermaid diagrams (the React Flow view and the Mermaid image), uam_chart cards, and what a reload brings back.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test, threadState, toolResult } from './harness';
import { expectPinned, pageErrors, yolo } from './transcript-helpers';
import { systemText, useMockUtility } from './composer-helpers';
import { startYoloThread } from './workspace-helpers';

test.use({ permissions: ['clipboard-read', 'clipboard-write'] });

const COMMITS = {
  title: 'Commits per day', kind: 'bar', x: 'day', y: ['commits'],
  data: [{ day: 'Mon', commits: 3 }, { day: 'Tue', commits: 5 }, { day: 'Wed', commits: 2 }],
};

test('a Mermaid flowchart opens in the React Flow view, toggles to the Mermaid image and its code, and opens large', async ({ page, project, model }) => {
  const errors = pageErrors(page);
  model.reply('draw a flow', 'A flow:\n\n```mermaid\ngraph TD; A[Start]-->|go| B{Ready?}\nB --> C[(Store)]\n```\n');
  await startThread(page, project, 'draw a flow');
  const conv = conversation(page);
  const view = conv.getByRole('group', { name: 'View' });
  await expect(view.getByRole('button', { name: 'React Flow' })).toHaveAttribute('aria-pressed', 'true', { timeout: 30_000 });
  const flow = conv.getByRole('figure', { name: 'Diagram' });
  for (const label of ['Start', 'Ready?', 'Store', 'go']) await expect(flow.getByText(label, { exact: true })).toBeVisible();
  // A node selected lights its edges; the label of an edge it does not touch dims.
  await flow.getByText('Store', { exact: true }).click();
  await expect(flow.locator('.flow-edge.is-hot')).toHaveCount(1);
  await expect(flow.locator('.flow-edge-label.is-dim')).toHaveText('go');
  // A dragged node takes its edges along, off ELK's route, until Reset layout puts it back.
  const store = await flow.locator('.react-flow__node', { hasText: 'Store' }).boundingBox();
  await page.mouse.move(store!.x + store!.width / 2, store!.y + store!.height / 2);
  await page.mouse.down();
  await page.mouse.move(store!.x + store!.width / 2 + 120, store!.y + store!.height / 2, { steps: 8 });
  await page.mouse.up();
  await flow.getByRole('button', { name: 'Reset layout' }).click();
  await expect(flow.getByRole('button', { name: 'Reset layout' })).toHaveCount(0);

  await view.getByRole('button', { name: 'Mermaid' }).click();
  const diagram = conv.getByRole('img', { name: 'Diagram' });
  await expect(diagram).toBeVisible({ timeout: 30_000 });
  await expect(flow).toHaveCount(0);
  expect(await diagram.getAttribute('src')).toMatch(/^data:image\/svg\+xml/);
  await expect(page.locator('iframe[sandbox="allow-scripts"][src$="/diagram-frame.html"]')).toBeAttached();

  await view.getByRole('button', { name: 'Code' }).click();
  await expect(diagram).toHaveCount(0);
  await expect(conv.locator('pre')).toContainText('A[Start]-->|go| B{Ready?}');
  await view.getByRole('button', { name: 'Mermaid' }).click();
  await expect(diagram).toBeVisible();

  await conv.getByRole('button', { name: 'Open diagram' }).click();
  const large = page.getByRole('dialog').or(page.getByRole('complementary', { name: 'Preview Diagram' }));
  await expect(large.getByRole('img', { name: 'Diagram' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(large).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('a diagram Pele cannot read opens in React Flow through one Utility-model conversion, and one the model cannot convert falls back to Mermaid', async ({ page, project, model, uam }) => {
  const errors = pageErrors(page);
  const restore = await useMockUtility(uam);
  try {
    const graph = {
      direction: 'TB', groups: [],
      nodes: [{ id: 'Idle', label: 'Idle', kind: 'step', group: '' }, { id: 'Busy', label: 'Busy', kind: 'decision', group: '' }],
      edges: [{ from: 'Idle', to: 'Busy', label: 'start', dashed: false, thick: false, head: 'end' }],
    };
    let asked = 0;
    uam.scripts.push({
      match: { predicate: (req) => systemText(req).includes('You convert a Mermaid diagram') },
      response: (req) => {
        asked++;
        uam.served.push({ fixture: 'diagram graph', request: req });
        return { content: JSON.stringify(req).includes('stateDiagram') ? JSON.stringify(graph) : 'There is no graph here.' };
      },
    });
    uam.apply();
    model.reply('draw the states', 'States:\n\n```mermaid\nstateDiagram-v2\n  Idle --> Busy: start\n```\n');
    model.reply('draw a broken flow', 'Broken:\n\n```mermaid\ngraph TD; A-->\n```\n');
    await startThread(page, project, 'draw the states');
    const conv = conversation(page);
    const view = conv.getByRole('group', { name: 'View' });
    await expect(view.getByRole('button', { name: 'React Flow' })).toHaveAttribute('aria-pressed', 'true', { timeout: 30_000 });
    const flow = conv.getByRole('figure', { name: 'Diagram' });
    for (const label of ['Idle', 'Busy', 'start']) await expect(flow.getByText(label, { exact: true })).toBeVisible({ timeout: 60_000 });
    // Back and forth costs nothing more: the graph is kept per source.
    await view.getByRole('button', { name: 'Mermaid' }).click();
    await view.getByRole('button', { name: 'React Flow' }).click();
    await expect(flow.getByText('Busy', { exact: true })).toBeVisible();
    expect(asked).toBe(1);

    // A flowchart neither Pele nor the model can read falls back to Mermaid, which says why it cannot draw it either; React Flow stays a click away.
    await send(page, 'draw a broken flow');
    await expect(threadState(page, 'Finished')).toBeVisible();
    await expect(conv.getByText(/^React Flow view unavailable: \S/)).toBeVisible({ timeout: 60_000 });
    await expect(conv.getByText(/^Diagram could not be rendered: \S/)).toBeVisible({ timeout: 30_000 });
    await expect(conv.locator('pre', { hasText: 'graph TD; A-->' }).last()).toBeVisible();
    await expect(view.last().getByRole('button')).toHaveText(['React Flow', 'Mermaid', 'Code']);
    await expect(view.last().getByRole('button', { name: 'Mermaid' })).toHaveAttribute('aria-pressed', 'true');
    expect(asked).toBe(2);
    const log = (await uam.api('GET', '/api/utility')) as { calls: { purpose: string; valid?: boolean }[] };
    // Newest first: the broken flowchart's failed conversion, then the state diagram's.
    expect(log.calls.filter((c) => c.purpose === 'diagram-graph').map((c) => c.valid)).toEqual([false, true]);
  } finally {
    await restore();
  }
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
  await expect(conv.getByRole('figure', { name: 'Diagram' }).getByText('A', { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(threadState(page, 'Finished')).toBeVisible();
  await send(page, 'rich turn two');
  await expect(conv.getByText('Done', { exact: true })).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByRole('button', { name: /^Took \S+\. Show this turn's waybill$/ }).last()).toBeVisible();

  await page.reload();
  await expect(conv.getByRole('row', { name: /alpha\s*1/ })).toBeVisible();
  await expect(conv.getByRole('row', { name: /beta\s*2/ })).toBeVisible();
  await expect(conv.getByRole('figure', { name: 'Diagram' }).getByText('B', { exact: true })).toBeAttached({ timeout: 30_000 });
  await expect(conv.locator('pre', { hasText: 'func main() {}' })).toBeAttached();
  await expect(conv.getByRole('button', { name: 'Copy code' })).toHaveCount(2);
  await expect(conv.getByRole('figure', { name: 'Chart: Commits per day' })).toBeVisible();
  await expect(conv.getByRole('button', { name: /^Took \S+\. Show this turn's waybill$/ }).last()).toBeVisible();
  for (const prompt of ['rich turn one', 'rich turn two']) await expect(conv.getByText(prompt, { exact: true })).toHaveCount(1);
  await expectPinned(page);
});

test('a running uam_chart command shows its progress on the tool row until it ends', async ({ page, project, model, workdir }) => {
  // The command waits for the spec to create "release", so the row is caught while it runs.
  const command = "while [ ! -e release ]; do sleep 0.1; done; printf 'day,commits\\nMon,3\\n'";
  model.toolRound('chart slowly', [{ id: 'call_slow', name: 'uam_chart', arguments: { title: 'Slow commits', kind: 'bar', x: 'day', y: ['commits'], format: 'csv', command } }], 'Slow chart drawn.');
  await startYoloThread(page, project, 'chart slowly');
  const conv = conversation(page);
  const progress = conv.getByText("Running the chart's command");
  await expect(progress).toBeVisible();
  fs.writeFileSync(path.join(workdir, 'release'), '');
  await expect(conv.getByText('Slow chart drawn.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(progress).toHaveCount(0);
  await expect(conv.getByRole('figure', { name: 'Chart: Slow commits' })).toBeVisible();
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
