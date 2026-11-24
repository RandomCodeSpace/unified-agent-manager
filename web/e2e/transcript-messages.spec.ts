// What a message offers: Copy message, links that leave the app, rich blocks from exact Markdown shapes,
// the turn's steps (thoughts and tool calls) under its sign-off, and a message too long to keep whole.
import type { Page } from '@playwright/test';
import { conversation, expect, startThread, test, threadState } from './harness';
import { yolo } from './transcript-helpers';

test.use({ permissions: ['clipboard-read', 'clipboard-write'] });

const clipboard = (page: Page) => page.evaluate(() => navigator.clipboard.readText());

test('Copy message copies a message and the reply as written, and a link opens outside the app', async ({ page, project, model, context }) => {
  const reply = 'Here is **bold**, `code` and [the docs](http://127.0.0.1:9/docs).\n\n- item one\n- item two';
  model.reply('copy **this** prompt', reply);
  const url = await startThread(page, project, 'copy **this** prompt');
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.locator('[data-reply] strong')).toHaveText('bold');

  // The user's message: rendered as Markdown, copied as typed.
  const mine = conv.getByText('this', { exact: true });
  await expect(mine).toHaveJSProperty('tagName', 'STRONG');
  await mine.hover();
  await conv.getByRole('button', { name: 'Copy message' }).first().click();
  await expect(conv.getByRole('button', { name: 'Copied' })).toHaveCount(1);
  expect(await clipboard(page)).toBe('copy **this** prompt');

  // The sign-off copies the reply that ends the turn, its Markdown as written.
  const took = conv.getByRole('button', { name: /^Took \S+\. Show this turn's waybill$/ });
  await took.hover();
  const copyReply = conv.getByRole('button', { name: 'Copy message' }).last();
  await copyReply.click();
  await expect(conv.getByRole('button', { name: 'Copied' }).last()).toBeVisible();
  expect(await clipboard(page)).toBe(reply);

  // A link opens in a new tab with no way back to the app's window; the app stays where it was.
  const link = conv.getByRole('link', { name: 'the docs' });
  await expect(link).toHaveAttribute('target', '_blank');
  await expect(link).toHaveAttribute('rel', /noopener/);
  // Nothing listens there: the new tab gets a stand-in page, so the case stays on loopback.
  await context.route('http://127.0.0.1:9/**', (r) => r.fulfill({ contentType: 'text/html', body: '<title>Outside</title>' }));
  const popup = context.waitForEvent('page');
  await link.click();
  const opened = await popup;
  await expect(opened).toHaveTitle('Outside');
  expect(opened.url()).toBe('http://127.0.0.1:9/docs');
  expect(await opened.evaluate(() => window.opener)).toBeNull();
  expect(page.url()).toBe(url);
  await opened.close();
});

test('rich blocks render from exact Markdown shapes, and a list that breaks its shape stays a list', async ({ page, project, model }) => {
  model.reply('show rich blocks', [
    '> [!WARNING]\n> The cache is stale.',
    '- **Branch:** `main`\n- **Owner:** the web team\n- **SDK:** v1.0.26 → v1.0.28',
    'Stats:',
    '- **Tests:** 742\n- **Coverage:** 81%\n- **Time:** 1.2 s',
    '1. **Build** the binary\n2. **Deploy** it',
    'Tasks:',
    '- [x] write\n- [x] test\n- [ ] ship',
    'Checks:',
    '- ✅ Go tests\n- ❌ Sonar gate\n- ⚠️ Not tried live',
    'Findings:',
    '- **Medium:** retry has no cap\n- **High:** token in the log\n- **Low:** typo',
    'Plain:',
    '- **Label:** one\n- plain item',
  ].join('\n\n'));
  await startThread(page, project, 'show rich blocks');
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();
  const reply = conv.locator('[data-reply]');

  // An alert is a titled callout, its marker gone.
  const note = reply.getByRole('note');
  await expect(note).toHaveAttribute('data-callout', 'warning');
  await expect(note).toHaveText(/^\s*Warning\s+The cache is stale\.\s*$/);
  await expect(reply).not.toContainText('[!WARNING]');

  // Bold labels make a label/value grid (a change shows old and new); short figures make tiles.
  const [facts, tiles] = [reply.locator('dl').nth(0), reply.locator('dl').nth(1)];
  await expect(facts.locator('dt')).toHaveText(['Branch', 'Owner', 'SDK']);
  await expect(facts.locator('dd').nth(0)).toHaveText('main');
  await expect(facts.locator('dd').nth(2).locator('del')).toHaveText('v1.0.26');
  await expect(facts.locator('dd').nth(2).locator('strong')).toHaveText('v1.0.28');
  await expect(tiles).toHaveClass(/\bmd-tiles\b/);
  await expect(tiles.locator('dd')).toHaveText(['742', '81%', '1.2 s']);
  await expect(facts).not.toHaveClass(/\bmd-tiles\b/);

  // A numbered list of bold leads is a step list.
  await expect(reply.locator('ol.md-steps > li')).toHaveCount(2);
  // A task list counts what is done.
  await expect(reply.getByRole('progressbar', { name: '2 of 3 done' })).toBeVisible();
  // Status marks: failures first, a count line, each mark said in words.
  await expect(reply.locator('ul.md-status > li')).toHaveText(['Sonar gate, failed', 'Not tried live, warning', 'Go tests, passed']);
  await expect(reply.locator('.md-counts').filter({ hasText: 'passed' })).toHaveText('1 failed · 1 warning · 1 passed');
  // Severity labels: most severe first.
  await expect(reply.locator('ul.md-severity > li .md-level')).toHaveText(['High:', 'Medium:', 'Low:']);

  // One item without the shape keeps the whole list as written.
  const plain = reply.locator('ul').filter({ hasText: 'plain item' });
  await expect(plain.locator('li')).toHaveText(['Label: one', 'plain item']);
  await expect(reply.locator('dl')).toHaveCount(2);
});

test('the turn line counts thoughts and calls; each step is one line with its result, opening onto its text or its input and output', async ({ page, project, model, uam }) => {
  model.on({ toolCallId: 'call_fail' }, { content: 'Counted and listed.', reasoning: 'Both ran.' });
  model.on({ userMessage: 'think then run' }, {
    reasoning: 'I should count first.\nThen list the folder.',
    toolCalls: [
      { id: 'call_count', name: 'bash', arguments: JSON.stringify({ command: 'seq 1 10', description: 'Count to ten' }) },
      { id: 'call_fail', name: 'bash', arguments: JSON.stringify({ command: 'ls /no-such-dir', description: 'List a missing folder' }) },
    ],
  }, {}, 'tools for think then run');
  await startThread(page, project, 'think then run');
  await yolo(page, uam.api);
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByText('Counted and listed.')).toBeVisible();
  // Thinking is metadata: none of its text stands in the reply.
  await expect(conv.getByText(/I should count first/)).toHaveCount(0);

  const counts = conv.getByRole('button', { name: '2 thoughts, 2 commands. Show the activity of this turn' });
  await expect(counts).toHaveAttribute('aria-expanded', 'false');
  await counts.click();
  await expect(counts).toHaveAttribute('aria-expanded', 'true');
  const steps = conv.getByRole('list', { name: 'Steps' }).getByRole('listitem');
  await expect(steps).toHaveCount(4);
  // Each call is one line: what ran and its result.
  const ok = steps.nth(1).getByRole('button', { name: 'Ran seq 1 10 , ok' });
  const failed = steps.nth(2).getByRole('button', { name: 'Ran ls /no-such-dir , exit 2' });
  await expect(ok).toBeVisible();
  await expect(failed).toBeVisible();
  await expect(steps.nth(1)).not.toContainText('$ seq 1 10');

  // A thought opens onto its text.
  const thought = steps.nth(0).getByRole('button', { name: 'Thought', exact: true });
  await expect(steps.nth(0)).not.toContainText('I should count first.');
  await thought.click();
  await expect(thought).toHaveAttribute('aria-expanded', 'true');
  await expect(steps.nth(0)).toContainText('I should count first. Then list the folder.');
  await expect(steps.nth(0).getByRole('button', { name: 'Copy thinking' })).toBeAttached();

  // A call opens onto its input and output: three lines until Show all.
  await ok.click();
  await expect(ok).toHaveAttribute('aria-expanded', 'true');
  await expect(steps.nth(1)).toContainText('$ seq 1 10');
  await expect(steps.nth(1)).toContainText('exit 0');
  const all = steps.nth(1).getByRole('button', { name: /^Show all \d+ lines$/ });
  await expect(all).toBeVisible();
  const output = steps.nth(1).locator('pre').last();
  const clipped = () => output.evaluate((el) => el.scrollHeight > el.clientHeight);
  await expect(output).toContainText(/^1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n/);
  expect(await clipped()).toBe(true);
  await all.click();
  await expect.poll(clipped).toBe(false);

  await failed.click();
  await expect(steps.nth(2)).toContainText('$ ls /no-such-dir');
  await expect(steps.nth(2)).toContainText('No such file or directory');
});

test('a reply too long to keep whole shows its start and Show full message, which shows it all as plain text; Copy copies it all', async ({ page, project, model }) => {
  // Just over the service's 4 MiB bound on one message's text (internal/web/items.go, maxItemText).
  const body = Array.from({ length: 4400 }, (_, i) => `row ${String(i).padStart(5, '0')} ${'y'.repeat(1000)}`).join('\n\n');
  const reply = `A **long** start.\n\n${body}\n\nThe very end.`;
  model.reply('too long to keep', reply, { chunkSize: 1 << 20 });
  await startThread(page, project, 'too long to keep');
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible({ timeout: 60_000 });
  const start = conv.locator('[data-reply] p', { hasText: /^A long start\.$/ });
  await expect(start.locator('strong')).toHaveText('long');
  await expect(conv.getByText('Shortened here to save memory.')).toBeVisible();
  await expect(conv.getByText('The very end.')).toHaveCount(0);

  // Copy reads the whole text first.
  await conv.getByRole('button', { name: /^Took \S+\. Show this turn's waybill$/ }).hover();
  await conv.getByRole('button', { name: 'Copy message' }).last().click();
  await expect(conv.getByRole('button', { name: 'Copied' })).toBeVisible();
  const copied = await clipboard(page);
  expect(copied.length).toBe(reply.length);
  expect(copied.endsWith('The very end.')).toBe(true);

  await conv.getByRole('button', { name: 'Show full message' }).click();
  await expect(conv.getByText(/The very end\.$/)).toBeAttached();
  await expect(conv.getByText('Shortened here to save memory.')).toHaveCount(0);
  // Plain text now: the Markdown as written, not rendered.
  await expect(conv.getByText(/^A \*\*long\*\* start\./)).toBeAttached();
  await expect(start).toHaveCount(0);
});
