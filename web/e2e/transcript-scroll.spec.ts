// Reading while a reply streams: holding still when scrolled up, following again, reloading mid-stream, a dropped event stream.
import type { WebSocketRoute } from '@playwright/test';
import { conversation, expect, startThread, test, threadState } from './harness';
import { expectPinned, line, lines, occurrences, perLine, settledScrollTop } from './transcript-helpers';

test('scrolling up mid-stream holds still, and Jump to bottom follows the stream again', async ({ page, project, model }) => {
  model.reply('stream and scroll', lines(200), perLine(60));
  await startThread(page, project, 'stream and scroll');
  const conv = conversation(page);
  const jump = page.getByRole('button', { name: 'Jump to bottom' });
  await expect(conv.getByText(line(60), { exact: true })).toBeAttached();
  await expectPinned(page);
  // A log, but not a live region: each streamed delta would be read out again.
  await expect(conv.getByRole('log')).toHaveAttribute('aria-live', 'off');

  await conv.hover();
  await page.mouse.wheel(0, -800);
  await expect(jump.first()).toBeVisible();
  // Floats over the conversation: its float shadow wins over the secondary button's own highlight.
  await expect(jump.first()).toHaveCSS('box-shadow', /0px 8px 24px -4px/);
  const held = await settledScrollTop(page);
  // The first line fully inside the box, which must stay where the reader put it.
  const firstShown = () => conv.evaluate((el) => {
    const top = el.getBoundingClientRect().top;
    return [...el.querySelectorAll('p')].find((p) => /^stream line \d{3}$/.test(p.textContent ?? '') && p.getBoundingClientRect().top >= top)?.textContent;
  });
  const first = await firstShown();
  expect(first).toMatch(/^stream line \d{3}$/);
  const arrived = await conv.getByText(/^stream line \d{3}$/).count();
  // Twenty more lines arrive below while the reader stays put.
  await expect(conv.getByText(line(arrived + 20), { exact: true })).toBeAttached();
  expect(Math.abs((await settledScrollTop(page)) - held)).toBeLessThanOrEqual(2);
  expect(await firstShown()).toBe(first);
  await expect(threadState(page, 'Working')).toBeVisible();

  await jump.first().click();
  await expectPinned(page);
  await expect(jump).toHaveCount(0);
  // Following again: still pinned while the rest streams in.
  const now = await conv.getByText(/^stream line \d{3}$/).count();
  if (now < 180) {
    await expect(conv.getByText(line(now + 15), { exact: true })).toBeAttached();
    await expectPinned(page);
  }
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expectPinned(page);
  await expect(conv.getByText(line(200), { exact: true })).toBeInViewport();
});

test('reloading mid-stream shows the text so far, and the reply ends whole and once', async ({ page, project, model }) => {
  model.reply('stream then reload', lines(200), perLine(60));
  await startThread(page, project, 'stream then reload');
  const conv = conversation(page);
  await expect(conv.getByText(line(50), { exact: true })).toBeAttached();

  await page.reload();
  // Still streaming after the reload, with what had arrived.
  await expect(conv.getByText(line(50), { exact: true })).toBeAttached();
  await expect(threadState(page, 'Working')).toBeVisible();
  await expect(conv.getByText(line(1), { exact: true })).toHaveCount(1);

  await expect(threadState(page, 'Finished')).toBeVisible();
  const counts = await occurrences(conv, /stream line \d{3}/);
  expect(Object.keys(counts)).toHaveLength(200);
  expect(Object.entries(counts).filter(([, n]) => n !== 1)).toEqual([]);
  await expect(conv.locator('[data-reply]')).toHaveCount(1);
  await expect(conv.getByText('stream then reload', { exact: true })).toHaveCount(1);
  await expectPinned(page);
});

test('a dropped event stream reconnects on its own, and the streaming reply ends whole and once', async ({ page, project, model }) => {
  // On plain HTTP the page reads its event streams over the service's event socket; each passes through here, so the case can drop it.
  const sockets: WebSocketRoute[] = [];
  await page.routeWebSocket(/\/api\/events\/socket/, (ws) => {
    ws.connectToServer();
    sockets.push(ws);
  });
  model.reply('stream through a drop', lines(200), perLine(60));
  await startThread(page, project, 'stream through a drop');
  const conv = conversation(page);
  await expect(conv.getByText(line(30), { exact: true })).toBeAttached();
  await expect(page.getByRole('status').filter({ hasText: /^Connected$/ })).toBeAttached();

  const lost = page.getByRole('status').filter({ hasText: 'Connection lost, reconnecting. Work continues on the server.' });
  const dropped = sockets.splice(0);
  expect(dropped.length).toBeGreaterThan(0);
  for (const ws of dropped) await ws.close({ code: 1006, reason: 'dropped by the case' }).catch(() => ws.close());
  await expect(lost).toBeAttached();
  await expect(lost).toHaveCount(0);
  await expect(page.getByRole('status').filter({ hasText: /^Connected$/ })).toBeAttached();
  expect(sockets.length, 'the page opened its streams again').toBeGreaterThan(0);
  // Back while the reply still streams: the rest arrives over the new stream.
  await expect(threadState(page, 'Working')).toBeVisible();

  await expect(threadState(page, 'Finished')).toBeVisible();
  const counts = await occurrences(conv, /stream line \d{3}/);
  expect(Object.keys(counts)).toHaveLength(200);
  expect(Object.entries(counts).filter(([, n]) => n !== 1)).toEqual([]);
  await expect(conv.locator('[data-reply]')).toHaveCount(1);
  await expectPinned(page);
  // Reconnecting resent nothing: the prompt went to the model once.
  expect(model.served().filter((s) => s.fixture === 'reply stream through a drop')).toHaveLength(1);
});
