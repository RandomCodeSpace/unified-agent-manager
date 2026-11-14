// How a reply's Markdown renders: structure, code highlighting and copy, unsafe HTML, wide and long tables.
import { conversation, expect, startThread, test, threadState } from './harness';
import { pageErrors } from './transcript-helpers';

test.use({ permissions: ['clipboard-read', 'clipboard-write'] });

test('Markdown structure renders, and a code block is highlighted and copies its text', async ({ page, project, model }) => {
  model.reply('show structure', [
    '# Title', '## Sub', '- first bullet\n- second bullet', '1. one\n2. two\n3. three',
    'Some **bold** words.', '> quoted words', 'Inline `x` code.', 'first half\nsecond half',
    '```python\ndef f():\n    return 1\n```',
  ].join('\n\n'));
  await startThread(page, project, 'show structure');
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();
  const reply = conv.locator('[data-reply]');
  await expect(reply.getByRole('heading', { name: 'Title' })).toBeVisible();
  await expect(reply.getByRole('heading', { name: 'Sub' })).toBeVisible();
  await expect(reply.locator('ul > li')).toHaveText(['first bullet', 'second bullet']);
  await expect(reply.locator('ol > li')).toHaveText(['one', 'two', 'three']);
  await expect(reply.locator('strong')).toHaveText('bold');
  await expect(reply.locator('blockquote')).toHaveText('quoted words');
  await expect(reply.locator(':not(pre) > code', { hasText: /^x$/ })).toBeVisible();
  // A single newline does not start a paragraph.
  await expect(reply.locator('p', { hasText: 'first half' })).toContainText('second half');

  const block = reply.locator('pre');
  await expect(block).toHaveText('def f():\n    return 1');
  await expect(reply.getByText('python', { exact: true })).toBeVisible();
  // The highlighter is a lazy chunk.
  await expect(block.locator('span[class^="hljs-"]').first()).toBeAttached();
  await reply.getByRole('button', { name: 'Copy code' }).click();
  await expect(reply.getByRole('button', { name: 'Copied' })).toBeVisible();
  // The fence's text as written, with the newline that ends its last line.
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('def f():\n    return 1\n');
});

test('unsafe HTML in a reply stays inert text', async ({ page, project, model }) => {
  const errors = pageErrors(page);
  model.reply('show unsafe', [
    'Before <script>window.__x=1</script> after.',
    '<img src=x onerror="window.__x=2">',
    '[click](javascript:window.__x=3)',
    '<!-- secret-comment -->',
    'This is <b>bold?</b> text.',
  ].join('\n\n'));
  await startThread(page, project, 'show unsafe');
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();
  const reply = conv.locator('[data-reply]');
  await expect(reply).toContainText('<script>window.__x=1</script>');
  await expect(reply).toContainText('<b>bold?</b>');
  await expect(reply.locator('b')).toHaveCount(0);
  await expect(reply.locator('script')).toHaveCount(0);
  await expect(reply.locator('img[src="x"]')).toHaveCount(0);
  await expect(page.locator('a[href^="javascript:"]')).toHaveCount(0);
  const link = reply.getByText('click', { exact: true });
  if (await link.count()) await link.click();
  await expect(page.locator('body')).not.toContainText('secret-comment');
  expect(await page.evaluate(() => (window as { __x?: number }).__x)).toBeUndefined();
  expect(errors).toEqual([]);
});

test('a wide table scrolls in its own box, and a long table sorts, pages and filters', async ({ page, project, model }) => {
  const heads = Array.from({ length: 20 }, (_, i) => `ColumnHeadingNumber${String(i + 1).padStart(2, '0')}`);
  const wide = [`| ${heads.join(' | ')} |`, `|${heads.map(() => '---').join('|')}|`, ...Array.from({ length: 5 }, (_, r) => `| ${heads.map((_, c) => `r${r}c${c}`).join(' | ')} |`)].join('\n');
  // n = 1 to 150, out of order: 1, 76, 2, 77, ...
  const order = Array.from({ length: 150 }, (_, i) => (i % 2 ? 75 + (i + 1) / 2 : i / 2 + 1));
  const long = ['| id | n |', '|---|---|', ...order.map((n, i) => `| row${i} | ${n} |`)].join('\n');
  model.reply('show tables', `Wide:\n\n${wide}\n\nLong:\n\n${long}\n`);
  await startThread(page, project, 'show tables');
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();

  const wideTable = conv.getByRole('region', { name: /^Table: ColumnHeadingNumber01/ });
  await expect(wideTable).toBeVisible();
  expect(await page.evaluate(() => document.scrollingElement!.scrollWidth <= document.scrollingElement!.clientWidth)).toBe(true);
  expect(await conv.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  expect(await wideTable.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
  // No heading breaks mid-word: each one is a single line box.
  const wrapped = await wideTable.locator('th').evaluateAll((ths) => ths.filter((th) => {
    const walker = document.createTreeWalker(th, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (!node.textContent?.trim()) continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      if ([...range.getClientRects()].filter((r) => r.width > 0).length > 1) return true;
    }
    return false;
  }).map((th) => th.textContent));
  expect(wrapped).toEqual([]);

  const longTable = conv.getByRole('region', { name: /^Table: id, n/ });
  const body = longTable.locator('tbody tr');
  await expect(body).toHaveCount(100);
  const status = conv.getByRole('status').filter({ hasText: /of 150 rows/ });
  await expect(status).toHaveText('1–100 of 150 rows');
  await longTable.getByRole('button', { name: 'Sort or filter n' }).click();
  await page.getByRole('button', { name: 'Sort ascending' }).click();
  await expect(body.first()).toContainText('row0');
  await expect(body.first().locator('td').last()).toHaveText('1');
  await page.keyboard.press('Escape');
  await conv.getByRole('button', { name: 'Next rows' }).click();
  await expect(status).toHaveText('101–150 of 150 rows');
  await expect(body.first().locator('td').last()).toHaveText('101');

  await longTable.getByRole('button', { name: 'Sort or filter n' }).click();
  await page.getByRole('textbox', { name: 'n contains' }).fill('149');
  await page.keyboard.press('Escape');
  await expect(body).toHaveCount(1);
  await expect(body.first().locator('td').last()).toHaveText('149');
  await conv.getByRole('button', { name: 'Clear filters' }).click();
  await expect(body).toHaveCount(100);
  await expect(status).toHaveText('1–100 of 150 rows');
});
