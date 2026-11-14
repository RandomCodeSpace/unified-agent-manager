import { conversation, expect, startThread, test, threadState } from './harness';

const line = (n: number) => `stream line ${String(n).padStart(3, '0')}`;

test('a streaming reply stays pinned to the bottom, and a reload shows the conversation once', async ({ page, project, model }) => {
  // 150 chunks of one line each, 60 ms apart: several screens of text over about 9 s.
  const lines = Array.from({ length: 150 }, (_, i) => line(i + 1));
  model.reply('stream please', lines.join('\n\n'), { latency: 60, chunkSize: line(1).length + 2 });
  await startThread(page, project, 'stream please');
  const conv = conversation(page);
  // Without touching the page, the view follows: once a line has arrived, the
  // scroller holding it reaches its bottom (within one line).
  const gap = (n: number) => conv.getByText(line(n), { exact: true }).evaluate((el) => {
    let s: HTMLElement | null = el.parentElement;
    while (s && !(/(auto|scroll)/.test(getComputedStyle(s).overflowY) && s.scrollHeight > s.clientHeight)) s = s.parentElement;
    return s ? s.scrollHeight - s.scrollTop - s.clientHeight : Number.POSITIVE_INFINITY;
  });
  for (const n of [40, 80, 120]) {
    await expect(conv.getByText(line(n), { exact: true })).toBeAttached();
    await expect.poll(() => gap(n)).toBeLessThan(32);
    // Still streaming: the view follows the stream, not only its end.
    if (n === 40) await expect(conv.getByText(line(150), { exact: true })).toHaveCount(0);
  }
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect.poll(() => gap(150)).toBeLessThan(32);
  await expect(conv.getByText(line(150), { exact: true })).toBeInViewport();
  await expect(conv.getByText(line(1), { exact: true })).not.toBeInViewport();
  await expect(page.getByRole('button', { name: /Jump to bottom/ })).toHaveCount(0);

  await page.reload();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByText(line(150), { exact: true })).toBeInViewport();
  for (const n of [1, 75, 150]) await expect(conv.getByText(line(n), { exact: true })).toHaveCount(1);
  await expect(conv.getByText('stream please', { exact: true })).toHaveCount(1);
});

test('Markdown tables and code blocks render', async ({ page, project, model }) => {
  model.reply('show markdown', 'Here is a table.\n\n| Fruit | Count |\n|---|---|\n| apple | 3 |\n| pear | 5 |\n\n```python\ndef answer():\n    return 42\n```\n');
  await startThread(page, project, 'show markdown');
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();
  const table = conv.getByRole('table');
  await expect(table.getByRole('columnheader')).toHaveCount(2);
  await expect(table.getByRole('row', { name: 'apple 3' })).toBeVisible();
  await expect(table.getByRole('row', { name: 'pear 5' })).toBeVisible();
  const code = conv.locator('pre code');
  await expect(code).toHaveText('def answer():\n    return 42');
  await expect(conv.getByRole('button', { name: 'Copy code' })).toBeVisible();
  await expect(conv.getByText('python', { exact: true })).toBeVisible();
});
