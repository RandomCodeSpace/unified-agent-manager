// Helpers for the transcript specs (transcript-*.spec.ts).
import { expect, type Locator, type Page } from '@playwright/test';
import { conversation } from './harness';

/** `stream line 001`: one line of a long scripted reply. */
export const line = (n: number) => `stream line ${String(n).padStart(3, '0')}`;

/** A reply of `count` lines, each its own paragraph. */
export const lines = (count: number) => Array.from({ length: count }, (_, i) => line(i + 1)).join('\n\n');

/** Streams one line per chunk, `latency` ms apart. */
export const perLine = (latency: number) => ({ latency, chunkSize: line(1).length + 2 });

/** How far the conversation is from its bottom, in pixels (pinned below 32, Task.tsx BOTTOM_SLACK). */
export const gap = (page: Page) => conversation(page).evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);

export const expectPinned = async (page: Page) => expect.poll(() => gap(page)).toBeLessThan(32);

/** The conversation's scrollTop once two reads one frame apart agree (a wheel scroll can still be moving). */
export async function settledScrollTop(page: Page): Promise<number> {
  return conversation(page).evaluate(async (el) => {
    const frame = () => new Promise((r) => requestAnimationFrame(() => r(null)));
    let last = -1;
    for (;;) {
      await frame();
      await frame();
      if (el.scrollTop === last) return last;
      last = el.scrollTop;
    }
  });
}

/** The topmost paragraph at least partly inside the conversation's box (messages carry no ids, so it is known by its text), and its offset from the box's top. */
export async function topItem(page: Page): Promise<{ text: string; offset: number }> {
  return conversation(page).evaluate((el) => {
    const top = el.getBoundingClientRect().top;
    for (const p of el.querySelectorAll<HTMLElement>('p')) {
      const r = p.getBoundingClientRect();
      if (r.bottom > top + 1 && r.height > 0 && p.textContent) return { text: p.textContent, offset: r.top - top };
    }
    throw new Error('no paragraph in view');
  });
}

/** The offset from the conversation's top of the one paragraph reading `text`, or null once it left the DOM. */
export const offsetOf = (page: Page, text: string) => conversation(page).evaluate((el, text) => {
  const found = [...el.querySelectorAll('p')].filter((p) => p.textContent === text);
  if (found.length > 1) throw new Error(`${found.length} paragraphs read ${text}`);
  return found.length ? found[0].getBoundingClientRect().top - el.getBoundingClientRect().top : null;
}, text);

/** How many times each match of `pattern` occurs in the conversation's text. */
export const occurrences = (conv: Locator, pattern: RegExp) => conv.evaluate((el, source) => {
  const counts: Record<string, number> = {};
  for (const m of (el.textContent ?? '').matchAll(new RegExp(source, 'g'))) counts[m[0]] = (counts[m[0]] ?? 0) + 1;
  return counts;
}, pattern.source);

/** Waits two animation frames: layout effects and the frame after them have run. */
export const frames = (page: Page) => page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(() => r(null)))));

/** The open Thread's id, from the URL. */
export const threadId = (page: Page) => /#task=([0-9a-f-]{36})$/.exec(page.url())![1];

/** Collects uncaught page errors; assert the list is empty at the end. */
export function pageErrors(page: Page): Error[] {
  const errors: Error[] = [];
  page.on('pageerror', (e) => errors.push(e));
  return errors;
}

type Api = (method: string, route: string, body?: unknown) => Promise<unknown>;

/** Puts the open Thread in Yolo mode, which also allows any request already waiting. */
export const yolo = (page: Page, api: Api) => api('PATCH', `/api/sessions/${threadId(page)}`, { mode: 'yolo' });
