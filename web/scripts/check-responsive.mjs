// Checks the dev mock at phone widths in the local Chromium: no page-level horizontal scroll, and no
// control or diff code line left outside the viewport unless a horizontal scroller holds it. Screenshots land in
// /tmp/responsive-check for a look.
//
//   cd web && npm run dev -- --port 5177   # in one shell, then
//   node scripts/check-responsive.mjs http://localhost:5177
/* global document, innerWidth, getComputedStyle -- inside page.evaluate callbacks, which run in the browser */
import { mkdirSync } from 'node:fs';
import { chromium } from '/home/dev/.local/lib/uam-scripts/node_modules/playwright/index.mjs';

const base = `${(process.argv[2] ?? 'http://localhost:5173').replace(/\/+$/, '')}/?mock`;
const out = '/tmp/responsive-check';
mkdirSync(out, { recursive: true });

const measure = (page) => page.evaluate(() => {
  const off = [];
  for (const e of document.querySelectorAll('button,a[href],input,textarea,select,[role=button],[role=tab],[role=radio],[role=switch],[role=menuitem],.diff td.code-cell')) {
    const r = e.getBoundingClientRect();
    if (!r.width || !r.height || (r.right <= innerWidth + 1 && r.left >= -1)) continue;
    // The closed drawer is aria-hidden and skipped (content-visibility: hidden), so its controls are not reachable anyway.
    if (e.closest('[inert],[aria-hidden=true]')) continue;
    let a = e.parentElement, scrolls = false;
    for (; a && a !== document.body && !scrolls; a = a.parentElement) scrolls = /(auto|scroll)/.test(getComputedStyle(a).overflowX) && a.scrollWidth > a.clientWidth;
    if (!scrolls) off.push(`${(e.getAttribute('aria-label') || e.textContent || e.tagName).trim().slice(0, 40)} [${Math.round(r.left)}..${Math.round(r.right)}]`);
  }
  return { scrollWidth: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth), innerWidth, off };
});

const inThread = (id, then) => async (page) => { await page.goto(`${base}#task=${id}`); await page.waitForTimeout(1500); await then?.(page); };
const views = {
  home: (page) => page.goto(base),
  thread: inThread('t1'),
  sidebar: inThread('t1', (page) => page.getByRole('button', { name: /^Show sidebar/ }).first().click()),
  changes: inThread('t1', (page) => page.getByRole('button', { name: /^Open changes/ }).click()),
  settings: (page) => page.goto(`${base}#settings`),
  'subagent sheet': inThread('t22', (page) => page.getByRole('button', { name: /failed.*running.*done/ }).first().click()),
  'composer settings': inThread('t1', (page) => page.getByRole('button', { name: /^More settings/ }).click()),
  'composer send options': inThread('t1', async (page) => {
    await page.locator('main textarea').first().fill('a prompt long enough to fill the composer on a narrow screen');
    await page.getByRole('button', { name: 'More send options' }).click();
  }),
};

const browser = await chromium.launch({ headless: true });
let failures = 0;
for (const width of [360, 390]) {
  for (const [name, open] of Object.entries(views)) {
    const ctx = await browser.newContext({ viewport: { width, height: 780 } });
    const page = await ctx.newPage();
    try {
      await open(page);
      await page.waitForTimeout(1500);
      const m = await measure(page);
      const ok = m.scrollWidth <= m.innerWidth && !m.off.length;
      if (!ok) failures++;
      console.log(`${ok ? 'ok  ' : 'FAIL'} ${width} ${name}: scrollWidth ${m.scrollWidth} innerWidth ${m.innerWidth}${m.off.length ? ` outside: ${m.off.join('; ')}` : ''}`);
      await page.screenshot({ path: `${out}/${width}-${name.replace(/\W+/g, '-')}.png` });
    } catch (e) {
      failures++;
      console.log(`FAIL ${width} ${name}: ${String(e).split('\n')[0]}`);
    }
    await ctx.close();
  }
}
await browser.close();
process.exit(failures ? 1 : 0);
