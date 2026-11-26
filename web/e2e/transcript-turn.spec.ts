// A turn's foot and what it links to: the sign-off, receipts, file reference tags, background command output.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, startThread, test, threadState } from './harness';
import { yolo } from './transcript-helpers';

test('the turn shows Working with a live clock, then signs off after the reply with its time and counts', async ({ page, project, model, uam }) => {
  const reply = Array.from({ length: 20 }, (_, i) => `sign-off line ${i + 1}`).join('\n\n');
  model.toolRound('run then sign off', [{ id: 'call_sleep', name: 'bash', arguments: { command: 'sleep 3; echo slept', description: 'Wait three seconds' } }], reply);
  await startThread(page, project, 'run then sign off');
  await yolo(page, uam.api);
  const conv = conversation(page);
  const working = conv.getByText('Working', { exact: true }).first();
  await expect(working).toBeVisible();
  const clock = async () => Number(/(\d+)s/.exec((await working.locator('..').textContent()) ?? '')?.[1] ?? NaN);
  const first = await clock();
  await expect.poll(clock).toBeGreaterThan(first);

  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByText('Working', { exact: true })).toHaveCount(0);
  const took = conv.getByRole('button', { name: /^Took \d+s\. Show this turn's waybill$/ });
  await expect(took).toHaveCount(1);
  await expect(conv.getByRole('button', { name: '1 command. Show the activity of this turn' })).toBeVisible();
  // The sign-off follows the reply's last words, on screen and in the DOM.
  const last = conv.getByText('sign-off line 20', { exact: true });
  // On the last line's own line when it fits, else under it: never above it.
  const [lastBox, tookBox] = [await last.boundingBox(), await took.boundingBox()];
  expect(tookBox!.y).toBeGreaterThanOrEqual(lastBox!.y - 1);
  expect(await last.evaluate((el, sign) => !!(el.compareDocumentPosition(sign!) & Node.DOCUMENT_POSITION_FOLLOWING), await took.elementHandle())).toBe(true);

  await took.hover();
  await expect(conv.getByRole('button', { name: 'Copy message' }).last()).toBeVisible();
  await expect(conv.getByRole('button', { name: 'Turn actions' })).toBeVisible();
  await took.click();
  const waybill = page.getByRole('dialog', { name: "This turn's waybill" });
  await expect(waybill.getByRole('heading', { name: 'Waybill' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(waybill).toHaveCount(0);
});

test('receipts stamp the claims the turn record contradicts, list the unrecorded, and open the evidence', async ({ page, project, model, uam, workdir }) => {
  fs.writeFileSync(path.join(workdir, 'package.json'), JSON.stringify({ name: 'fixture', scripts: { test: 'exit 1' } }));
  model.toolRound('claim things', [
    { id: 'call_create', name: 'create', arguments: { path: path.join(workdir, 'a.py'), file_text: 'x=1\n' } },
    { id: 'call_test', name: 'bash', arguments: { command: 'npm test', description: 'Run the tests' } },
  ], 'I updated `b.py` and created `a.py`. I ran `npm test` and all tests pass.');
  await startThread(page, project, 'claim things');
  await yolo(page, uam.api);
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(fs.readFileSync(path.join(workdir, 'a.py'), 'utf8')).toBe('x=1\n');

  const stamps = conv.getByRole('list', { name: 'Receipts' }).getByRole('listitem');
  await expect(stamps).toHaveCount(2);
  await expect(stamps.filter({ hasText: 'npm test' })).toContainText('exit 1');
  await expect(stamps.filter({ hasText: /all tests pass/i })).toContainText('last test run exit 1');
  await expect(stamps.filter({ hasText: 'a.py' })).toHaveCount(0);
  await expect(stamps.filter({ hasText: 'b.py' })).toHaveCount(0);
  // b.py was never touched: "no record", not a contradiction.
  const none = conv.getByRole('button', { name: '1 claim has no record in this turn' });
  await none.click();
  await expect(conv.getByRole('list', { name: 'Claims with no record in this turn' })).toContainText('b.py');

  // A stamp opens the call that is its evidence: the turn's activity unfolds to the npm test row.
  const call = conv.locator('[id^="item-"]', { hasText: 'npm test' });
  await expect(call).toHaveCount(0);
  // Why a stamp is red is a tip on keyboard focus, not a native title.
  const stamp = stamps.filter({ hasText: /all tests pass/i }).getByRole('button');
  await expect(stamp).not.toHaveAttribute('title');
  await page.keyboard.press('Shift');
  await stamp.focus();
  await expect(page.getByRole('tooltip')).toHaveText('Contradicted by the record. Show the call.');
  await stamp.click();
  await expect(call).toBeInViewport();

  await page.reload();
  await expect(conv.getByRole('list', { name: 'Receipts' }).getByRole('listitem')).toHaveCount(2);
});

// A reference renders only once its block stops streaming: that alone, with no new text, must ask for it.
test('a file reference tag opens the preview at the cited line, and a Markdown link to a file previews it', async ({ page, project, model, workdir }) => {
  fs.mkdirSync(path.join(workdir, 'src'));
  fs.writeFileSync(path.join(workdir, 'src', 'app.go'), Array.from({ length: 40 }, (_, i) => `// line ${i + 1}`).join('\n') + '\n');
  fs.mkdirSync(path.join(workdir, 'app', '[id]'), { recursive: true });
  fs.writeFileSync(path.join(workdir, 'app', '[id]', 'page.tsx'), 'export default function Page() {}\n');
  model.reply('cite a file', 'See `src/app.go:12`, `src/app.go:20-25`, `app/[id]/page.tsx` and [the readme](README.md).');
  await startThread(page, project, 'cite a file');
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();
  const tag = conv.getByRole('link', { name: /^Open \S*src\/app\.go at line 12$/ });
  await expect(tag).toContainText('app.go');
  await expect(tag).toContainText('12');
  // A line range and a route folder name link too.
  await expect(conv.getByRole('link', { name: /^Open \S*src\/app\.go at line 20$/ })).toBeVisible();
  await expect(conv.getByRole('link', { name: /^Open \S*app\/\[id\]\/page\.tsx$/ })).toBeVisible();
  await tag.click();
  const preview = page.getByRole('complementary', { name: 'Preview app.go' });
  await expect(preview).toBeVisible();
  await expect(preview.getByText('// line 12', { exact: true })).toBeVisible();
  await expect(preview.locator('li[data-marked]')).toHaveText('12');
  await page.keyboard.press('Escape');
  await expect(preview).toHaveCount(0);
  await expect(tag).toBeFocused();

  await conv.getByRole('link', { name: /README\.md/ }).click();
  const readme = page.getByRole('complementary', { name: 'Preview README.md' });
  await expect(readme).toBeVisible();
  await expect(page.getByRole('complementary', { name: /^Preview / })).toHaveCount(1);
});

test('a background command\'s output opens in the Command output panel, and Jump to latest shows its end', async ({ page, project, model, uam }) => {
  model.toolRound('count in the background', [{ id: 'call_bg', name: 'bash', arguments: { command: 'seq 1 5000; sleep 20', description: 'Count to 5000', mode: 'async', detach: true } }], 'Started the count.');
  await startThread(page, project, 'count in the background');
  await yolo(page, uam.api);
  const conv = conversation(page);
  await expect(conv.getByText('Started the count.')).toBeVisible();
  await page.getByRole('button', { name: /^Background tasks: 1 running/ }).click();
  await page.getByRole('button', { name: 'Show output of background task: Count to 5000' }).click();
  const panel = page.getByRole('complementary', { name: 'Command output' }).or(page.getByRole('dialog', { name: 'Command output' }));
  const out = panel.getByRole('region', { name: 'Output of seq 1 5000; sleep 20' });
  // The panel reads from the start in chunks; Jump to latest reads the newest chunk and stays at the end.
  await expect(out.locator('pre')).toContainText(/^1\n2\n3\n/);
  await out.getByRole('button', { name: 'Jump to latest' }).click();
  await expect(out.locator('pre')).toHaveText(/\n4999\n5000\s*$/);
  await expect(out.getByText('Running. New output appears here.')).toBeVisible();
  await expect.poll(() => out.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight)).toBeLessThan(24);
  const before = await conv.evaluate((el) => el.scrollTop);
  await panel.getByRole('button', { name: 'Close command output' }).click();
  await expect(panel).toHaveCount(0);
  expect(Math.abs((await conv.evaluate((el) => el.scrollTop)) - before)).toBeLessThanOrEqual(2);
});

test('Finish evidence: Changes opens with the checks, the claims and what nothing backs, and the turn\'s files', async ({ page, project, model, uam, workdir }) => {
  fs.writeFileSync(path.join(workdir, 'package.json'), JSON.stringify({ name: 'fixture', scripts: { test: 'exit 0', build: 'exit 1' } }));
  // The edit first, then the checks, so the test run comes after every change.
  model.on({ toolCallId: 'call_build' }, { content: 'I created `b.py`. All tests pass. The build passes. I updated the README.' }, {}, 'result of call_build');
  model.on({ toolCallId: 'call_edit' }, {
    toolCalls: [
      { id: 'call_tests', name: 'bash', arguments: JSON.stringify({ command: 'npm test', description: 'Run the tests' }) },
      { id: 'call_build', name: 'bash', arguments: JSON.stringify({ command: 'npm run build', description: 'Build' }) },
    ],
  }, {}, 'checks after call_edit');
  model.on({ userMessage: 'finish with evidence' }, {
    toolCalls: [{ id: 'call_edit', name: 'create', arguments: JSON.stringify({ path: path.join(workdir, 'b.py'), file_text: 'y=2\n' }) }],
  }, {}, 'edit for finish with evidence');
  await startThread(page, project, 'finish with evidence');
  await yolo(page, uam.api);
  await expect(threadState(page, 'Finished')).toBeVisible();

  const changes = page.getByRole('group', { name: 'Version control' }).getByRole('button', { name: /^Open changes.+evidence available/ });
  await changes.click();
  const card = page.getByRole('region', { name: 'Finished — check the evidence' });
  await expect(card).toContainText('2 claims not verified');
  const rows = card.getByRole('listitem');
  // The checks first, failures first, each with its command, exit status and output.
  await expect(rows.nth(0)).toHaveText(/^Ran the build\s*npm run build · exit 1 · /);
  await expect(rows.nth(0).getByRole('img', { name: 'Failed' })).toBeVisible();
  await expect(rows.nth(1)).toHaveText(/^Ran the tests\s*npm test · exit 0 · /);
  await expect(rows.nth(1).getByRole('img', { name: 'Passed' })).toBeVisible();
  // Then each claim with what backs it, or why nothing does.
  const claim = (words: string) => rows.filter({ hasText: `“${words}”` });
  await expect(claim('All tests pass.')).toContainText('Backed by npm test · exit 0');
  await expect(claim('All tests pass.').getByRole('img', { name: 'Verified' })).toBeVisible();
  await expect(claim('The build passes.')).toContainText('Not verified · the last run failed (exit 1)');
  await expect(claim('I updated the README.')).toContainText('Not verified · no edit to README in this turn');
  await expect(claim('I updated the README.').getByRole('img', { name: 'Not verified' })).toBeVisible();
  // Then the files the turn changed.
  await expect(card.getByRole('heading', { name: 'Changed in this turn' })).toBeVisible();
  await expect(rows.filter({ hasText: 'b.py' })).toHaveText(/b\.py\s*\+1 −0/);

  await rows.nth(0).getByRole('button', { name: 'Show output' }).click();
  const output = page.getByRole('complementary', { name: 'Command output' }).or(page.getByRole('dialog', { name: 'Command output' }));
  await expect(output.getByRole('region', { name: 'Output of npm run build' })).toContainText('exit 1');
});
