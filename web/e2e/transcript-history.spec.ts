// Older history: the service sends the newest 64 KiB of a conversation (internal/web/history_page.go,
// 50 items or 64 KiB a page) and the page reads older pages as the reader scrolls up. Twelve turns
// of about 16 KiB each make a history of four or more pages without sixty scripted turns; each
// reply's bulk is one code block, which the page caps at 480 px, so the history stays a few screens tall.
import type { Browser, Page, Route } from '@playwright/test';
import { conversation, expect, send, startThread, test, threadState, type Model, type Project } from './harness';
import { expectPinned, frames, line, lines, offsetOf, perLine, settledScrollTop, topItem } from './transcript-helpers';

const TURNS = 12;
const ask = (n: number) => `history turn ${String(n).padStart(2, '0')}`;
const answer = (n: number) => `Reply ${n} of the history`;
const bulk = (n: number) => Array.from({ length: 10 }, (_, k) => `${n}.${k} ${'x'.repeat(1600)}`).join('\n');

/** Twelve finished turns in one Thread; returns its URL. */
async function history(page: Page, project: Project, model: Model) {
  for (let n = 1; n <= TURNS; n++) model.reply(ask(n), `${answer(n)}\n\n\`\`\`text\n${bulk(n)}\n\`\`\`\n`, { chunkSize: 8000 });
  const url = await startThread(page, project, ask(1));
  for (let n = 1; n <= TURNS; n++) {
    if (n > 1) await send(page, ask(n));
    await expect(conversation(page).getByText(answer(n), { exact: true })).toBeAttached();
    await expect(threadState(page, 'Finished')).toBeVisible();
  }
  return url;
}

/** A second reader with no cached pages: a fresh browser context signed in. */
async function freshReader(browser: Browser, url: string, token: string): Promise<Page> {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const res = await context.request.post(new URL('/api/login', url).href, { data: { token } });
  expect(res.status()).toBe(204);
  return context.newPage();
}

/** Holds every older-history request until the case lets it go, so the reader's place can be read before the page lands. */
async function holdHistory(page: Page) {
  const held: Route[] = [];
  const failed: number[] = [];
  await page.route(/\/api\/sessions\/[^/]+\/history\?/, (route) => { held.push(route); });
  page.on('response', (r) => { if (/\/api\/sessions\/[^/]+\/history\?/.test(r.url()) && r.status() >= 400) failed.push(r.status()); });
  return { held, failed };
}

/** Scrolls the conversation up, with the pointer in its margin so no code block takes the wheel. */
async function wheelUp(page: Page, px: number) {
  const box = (await conversation(page).boundingBox())!;
  await page.mouse.move(box.x + 8, box.y + box.height / 2);
  await page.mouse.wheel(0, -px);
  await settledScrollTop(page);
}

const replyCounts = (page: Page) => conversation(page).evaluate((el) => {
  const counts: Record<string, number> = {};
  for (const p of el.querySelectorAll('p')) {
    const m = /^Reply (\d+) of the history$/.exec(p.textContent ?? '');
    if (m) counts[m[1]] = (counts[m[1]] ?? 0) + 1;
  }
  return counts;
});

/**
 * Scrolls up until `done`, letting each held page land only after the topmost item's place was
 * read, and checks that place after the page landed (±4 px). Returns every reply seen.
 */
async function pageUp(page: Page, hold: Awaited<ReturnType<typeof holdHistory>>, done: () => Promise<boolean>) {
  const seen = new Set<string>();
  let pages = 0;
  const sample = async () => {
    const counts = await replyCounts(page);
    expect(Object.entries(counts).filter(([, c]) => c > 1), 'a reply shown twice').toEqual([]);
    for (const n of Object.keys(counts)) seen.add(n);
  };
  await sample();
  for (let i = 0; i < 40 && !(await done()); i++) {
    await wheelUp(page, 1500);
    await sample();
    if (!hold.held.length) continue;
    await expect(conversation(page).getByText('Loading earlier messages…')).toBeVisible();
    const anchor = await topItem(page);
    await hold.held.shift()!.continue();
    await expect(conversation(page).getByText('Loading earlier messages…')).toHaveCount(0);
    await frames(page);
    const moved = await offsetOf(page, anchor.text);
    expect(moved, 'the item in view stayed in the DOM').not.toBeNull();
    expect(Math.abs(moved! - anchor.offset), 'the item in view stayed put').toBeLessThanOrEqual(4);
    pages++;
    await sample();
  }
  return { seen, pages };
}

test('scrolling up pages older history to the first message, with no gap, duplicate or jump', async ({ page, project, model, browser, uam }) => {
  test.setTimeout(90_000);
  const url = await history(page, project, model);
  const reader = await freshReader(browser, url, uam.token);
  const hold = await holdHistory(reader);
  await reader.goto(url);
  const conv = conversation(reader);
  await expect(conv.getByText(answer(TURNS), { exact: true })).toBeVisible();
  await expect(conv.getByText(answer(1), { exact: true })).toHaveCount(0);
  await expect(conv.getByText('Scroll up for earlier messages')).toBeAttached();

  const first = conv.getByText(ask(1), { exact: true });
  const { seen, pages } = await pageUp(reader, hold, async () => (await first.count()) > 0);
  await expect(first).toBeAttached();
  expect(pages, 'older pages read').toBeGreaterThanOrEqual(2);
  expect([...seen].map(Number).sort((a, b) => a - b)).toEqual(Array.from({ length: TURNS }, (_, i) => i + 1));
  await expect(conv.getByText('Scroll up for earlier messages')).toHaveCount(0);
  await expect(conv.getByText('Earlier history was truncated', { exact: false })).toHaveCount(0);
  expect(hold.failed).toEqual([]);
  await reader.context().close();
});

test('reading old history while a new turn streams holds still, and Jump to bottom lands on the new reply', async ({ page, project, model, browser, uam }) => {
  test.setTimeout(90_000);
  const url = await history(page, project, model);
  model.reply('history live turn', lines(120), perLine(60));
  const reader = await freshReader(browser, url, uam.token);
  const hold = await holdHistory(reader);
  await reader.goto(url);
  const conv = conversation(reader);
  await expect(conv.getByText(answer(TURNS), { exact: true })).toBeVisible();
  await pageUp(reader, hold, async () => (await conv.getByText(answer(4), { exact: true }).count()) > 0);
  // Let any further page land, then hold the reader's place.
  await reader.unroute(/\/api\/sessions\/[^/]+\/history\?/);
  for (const r of hold.held.splice(0)) await r.continue();
  await expect(conv.getByText('Loading earlier messages…')).toHaveCount(0);
  await settledScrollTop(reader);
  const anchor = await topItem(reader);

  // The other tab sends the next prompt; the reader sees it stream in below.
  await send(page, 'history live turn');
  await expect(conv.getByText(line(30), { exact: true })).toBeAttached();
  await expect(threadState(reader, 'Working')).toBeVisible();
  await expect(conv.getByText(line(60), { exact: true })).toBeAttached();
  expect(Math.abs((await offsetOf(reader, anchor.text))! - anchor.offset)).toBeLessThanOrEqual(4);
  const jump = reader.getByRole('button', { name: 'Jump to bottom' });
  await expect(jump.first()).toBeVisible();

  await jump.first().click();
  await expect(threadState(reader, 'Finished')).toBeVisible();
  await expect(conv.getByText(line(120), { exact: true })).toBeInViewport();
  await expectPinned(reader);
  await expect(conv.getByText(answer(TURNS), { exact: true })).toHaveCount(1);
  const counts = await replyCounts(reader);
  expect(Object.entries(counts).filter(([, c]) => c > 1)).toEqual([]);
  await reader.context().close();
});
