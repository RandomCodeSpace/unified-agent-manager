// Drives the real Excalidraw editor in the dev mock under the service's CSP, in the local Chromium:
// 0 external requests, 0 CSP violations, 0 page errors, and an edit through the UI (a double-click
// rename) makes a change pill. Screenshots land in /tmp/drawings-check for a look.
//
//   cd web && npm run dev -- --port 5177   # in one shell, then
//   node scripts/check-drawings.mjs http://127.0.0.1:5177
//
// Vite's dev page injects an inline module (React refresh) and <style> elements, which the
// production page never has: the script allows that one inline script by its hash and inline
// styles, and keeps every other directive as internal/web/server.go sends it.
/* global window, document -- inside page.evaluate callbacks, which run in the browser */
import { createHash } from 'node:crypto';
import { mkdirSync } from 'node:fs';
import { chromium } from '/home/dev/.local/lib/uam-scripts/node_modules/playwright/index.mjs';

const base = (process.argv[2] ?? 'http://127.0.0.1:5173').replace(/\/+$/, '');
const out = '/tmp/drawings-check';
mkdirSync(out, { recursive: true });
const host = new URL(base).host;

const html = await (await fetch(`${base}/?mock`)).text();
const inline = [...html.matchAll(/<script type="module">([\s\S]*?)<\/script>/g)].map((m) => `'sha256-${createHash('sha256').update(m[1]).digest('base64')}'`);
const csp = `default-src 'self'; script-src 'self' ${inline.join(' ')}; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self' ws://${host}; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'`;

const browser = await chromium.launch({ headless: true });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2, colorScheme: 'light' });
const page = await ctx.newPage();
await page.route(`${base}/**`, async (route) => {
  const response = await route.fetch();
  const headers = { ...response.headers() };
  if ((headers['content-type'] ?? '').includes('text/html') && !route.request().url().includes('diagram-frame')) headers['content-security-policy'] = csp;
  await route.fulfill({ response, headers });
});
const external = [], errors = [], violations = [];
page.on('request', (r) => {
  const url = r.url();
  if (!url.startsWith(base) && !url.startsWith(`ws://${host}`) && !url.startsWith('data:') && !url.startsWith('blob:')) external.push(url);
});
page.on('pageerror', (e) => errors.push(String(e)));
page.on('console', (m) => {
  if (/Content.Security|CSP|Refused to/i.test(m.text())) violations.push(m.text());
  else if (m.type() === 'error' && !/favicon|React DevTools|WebSocket|\[vite\]|Vite server/.test(m.text())) errors.push(m.text());
});
const failures = [];
const check = (ok, what) => { if (!ok) failures.push(what); console.log(`${ok ? 'ok  ' : 'FAIL'} ${what}`); };
const shot = (name) => page.screenshot({ path: `${out}/${name}.png` });

try {
  await page.goto(`${base}/?mock#task=t-chart`);
  const pair = page.locator('[data-drawing$="diagram-d4-0.excalidraw"]');
  await pair.locator('img').first().waitFor({ timeout: 30_000 });
  await page.waitForTimeout(500);
  await pair.scrollIntoViewIfNeeded();
  await shot('1-card-pair');
  check(await pair.getByText('Yours v2 · 4 changes').count() === 1, 'the fixture card shows the Yours pill with 4 changes');

  // Edit as drawing asks first: Escape on the confirmation writes nothing.
  const cards = () => page.locator('[data-drawing]').count();
  const before = await cards();
  await page.getByRole('button', { name: 'Edit as drawing' }).click();
  const ask = page.getByRole('alertdialog');
  await ask.waitFor();
  check(await ask.getByText('docs/diagram-d2-0.excalidraw').count() === 1, 'the confirmation names the file');
  await shot('2a-confirm');
  await page.keyboard.press('Escape');
  await ask.waitFor({ state: 'hidden' });
  await page.waitForTimeout(500);
  check(await cards() === before && await page.getByRole('button', { name: 'Edit as drawing' }).count() === 1, 'cancelling the confirmation creates no drawing');
  // Create drawing converts d2's flowchart and opens the editor; Escape on the canvas closes it.
  await page.getByRole('button', { name: 'Edit as drawing' }).click();
  await ask.getByRole('button', { name: 'Create drawing' }).click();
  const editor = page.getByRole('dialog', { name: 'Edit diagram-d2-0.excalidraw' });
  await editor.waitFor({ timeout: 30_000 });
  await page.waitForFunction(() => !!window.__uamExcalidraw);
  await page.waitForTimeout(800);
  check(await editor.getByRole('button', { name: 'Drawing menu' }).count() === 1, 'the Excalidraw menu trigger is named Drawing menu');
  const unnamed = await page.evaluate(() => [...document.querySelectorAll('.excalidraw button, .excalidraw input, .excalidraw [role="button"], .excalidraw [role="radio"]')]
    .filter((el) => el.getClientRects().length && !el.getAttribute('aria-label') && !el.getAttribute('title') && !el.getAttribute('aria-labelledby') && !el.textContent.trim() && !el.closest('label')?.getAttribute('title') && !el.closest('label')?.textContent.trim())
    .map((el) => el.outerHTML.slice(0, 120)));
  check(unnamed.length === 0, `every visible Excalidraw control has a name${unnamed.length ? `: ${unnamed.join(' | ')}` : ''}`);
  // Escape with Excalidraw's menu open closes the menu only; the next one closes the editor.
  await editor.getByRole('button', { name: 'Drawing menu' }).click();
  await page.waitForTimeout(300);
  await page.keyboard.press('Escape');
  await page.waitForTimeout(400);
  check(await editor.isVisible(), 'Escape with the Excalidraw menu open keeps the editor');
  await page.locator('.excalidraw-container').focus();
  await page.keyboard.press('Escape');
  await editor.waitFor({ state: 'hidden', timeout: 5_000 });
  check(true, 'Escape on the canvas closes the editor');
  const card = page.locator('[data-drawing$="diagram-d2-0.excalidraw"]');
  await card.locator('img').waitFor({ timeout: 30_000 });
  await card.scrollIntoViewIfNeeded();
  await shot('2-card-converted');
  check(await card.getByText('Agent’s v1').count() === 1, 'the converted card is the agent’s v1');
  check(await page.getByRole('button', { name: 'Open drawing' }).count() === 2, 'both diagrams stay, linking to their drawings');
  await card.getByRole('button', { name: 'Edit', exact: true }).click();
  await editor.waitFor();
  await page.waitForFunction(() => !!window.__uamExcalidraw);
  await page.waitForTimeout(800);
  await shot('3-editor');
  check(await editor.getByText('Same as the agent’s').count() === 1, 'the editor opens on Yours with no changes');
  const at = await page.evaluate(() => {
    const api = window.__uamExcalidraw;
    const s = api.getAppState();
    const label = api.getSceneElements().find((e) => e.type === 'text' && e.text === 'Browser');
    const el = api.getSceneElements().find((e) => e.id === label.containerId);
    return { x: (el.x + el.width / 2 + s.scrollX) * s.zoom.value + s.offsetLeft, y: (el.y + el.height / 2 + s.scrollY) * s.zoom.value + s.offsetTop };
  });
  await page.mouse.dblclick(at.x, at.y);
  await page.waitForTimeout(200);
  await page.keyboard.press('ControlOrMeta+a');
  await page.keyboard.type('Web browser');
  await page.keyboard.press('Escape');
  await editor.getByText('Yours · 1 change').waitFor({ timeout: 10_000 });
  await shot('4-editor-edited');
  check(true, 'a double-click rename makes the "Yours · 1 change" pill');
  await editor.getByRole('radio', { name: /^Changes/ }).click();
  await page.waitForTimeout(800);
  const lines = await editor.getByRole('list', { name: 'Changes' }).getByRole('listitem').allTextContents();
  console.log('changes:', lines);
  check(lines.some((l) => l.includes('renamed "Web browser"')), 'the Changes view lists the rename');
  await shot('5-changes');
  await editor.getByRole('radio', { name: /^Agent’s/ }).click();
  await page.waitForTimeout(600);
  await shot('6-agents');
  await editor.getByRole('button', { name: 'Done' }).click();
  await editor.waitFor({ state: 'hidden' });
  await page.waitForTimeout(600);
  await card.scrollIntoViewIfNeeded();
  await shot('7-card-after-edit');
  check(await card.getByText(/^Yours v\d+ · 1 change$/).count() === 1, 'the card shows the pair after the edit');
  check(await page.getByText('1 change by you', { exact: false }).count() >= 1, 'the composer chip says 1 change by you');

  // Revert: one save of the agent's file, and the scene Excalidraw reports back is not another.
  await card.getByRole('button', { name: 'Edit', exact: true }).click();
  await editor.waitFor();
  await page.waitForFunction(() => !!window.__uamExcalidraw);
  await editor.getByRole('button', { name: 'Revert to agent’s' }).click();
  await editor.getByText('Same as the agent’s').waitFor({ timeout: 10_000 });
  await page.waitForTimeout(1500);
  await shot('7b-reverted');
  await editor.getByRole('button', { name: 'Done' }).click();
  await editor.waitFor({ state: 'hidden' });
  await page.waitForTimeout(600);
  check(await card.getByText(/^Agent’s v\d+$/).count() === 1 && await card.getByText(/^Yours v/).count() === 0, 'after Revert the card is the agent’s version alone');

  // Delete, once confirmed, removes the drawing and its card; the diagram offers Edit as drawing again.
  await card.getByRole('button', { name: 'Delete', exact: true }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete drawing' }).click();
  await card.waitFor({ state: 'detached', timeout: 10_000 });
  check(await page.getByRole('button', { name: 'Edit as drawing' }).count() === 1, 'Delete removes the drawing and the diagram offers Edit as drawing again');

  // The diagram's lightbox: its heading inside the surface.
  await page.getByRole('button', { name: 'Open diagram' }).first().click();
  const box = page.getByRole('dialog', { name: 'Diagram' });
  await box.waitFor();
  await page.waitForTimeout(400);
  await shot('7c-lightbox');
  await page.keyboard.press('Escape');
  await box.waitFor({ state: 'hidden' });

  // Dark scheme: the pictures draw again on the raised token.
  await page.evaluate(() => localStorage.setItem('uam.theme', 'dark'));
  await page.reload();
  await pair.locator('img').first().waitFor({ timeout: 30_000 });
  await page.waitForTimeout(800);
  await pair.scrollIntoViewIfNeeded();
  await shot('8-dark-card');
  await pair.getByRole('button', { name: 'Compare' }).click();
  const dark = page.getByRole('dialog', { name: 'Edit diagram-d4-0.excalidraw' });
  await dark.locator('img').waitFor({ timeout: 30_000 });
  await page.waitForTimeout(600);
  await shot('9-dark-changes');
  await dark.getByRole('radio', { name: /^Yours/ }).click();
  await page.waitForFunction(() => !!window.__uamExcalidraw);
  await page.waitForTimeout(800);
  await shot('10-dark-editor');
} catch (e) {
  failures.push(`script: ${String(e).split('\n')[0]}`);
  console.log(`FAIL script: ${String(e).split('\n')[0]}`);
  await shot('failed').catch(() => {});
}
check(external.length === 0, `external requests: ${external.length ? external.join(', ') : 'none'}`);
check(violations.length === 0, `CSP violations: ${violations.length ? [...new Set(violations)].join(' | ') : 'none'}`);
check(errors.length === 0, `page errors: ${errors.length ? [...new Set(errors)].join(' | ') : 'none'}`);
await browser.close();
console.log(failures.length ? `FAILED (${failures.length})` : 'PASSED', `shots in ${out}`);
process.exit(failures.length ? 1 : 0);
