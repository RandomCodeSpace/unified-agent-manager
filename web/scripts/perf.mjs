// Performance acceptance on the mock's 2,000-item Thread (`?mock&perf=2000`, mock/data.ts perfTask), in the
// local Chromium against the production React build. No model call and no service: the mock plays a ~65 s stream.
//
//   cd web && VITE_UAM_MOCK=1 npx vite build --outDir /tmp/uam-perf --emptyOutDir
//   npx vite preview --outDir /tmp/uam-perf --port 5178 --strictPort   # in one shell, then
//   node scripts/perf.mjs http://127.0.0.1:5178 [items=2000]     # SCROLL_STEP=120 for a fling-speed stress run
//
// Prints p95 (and max) of: Thread switch (click to the target's transcript painted), scroll frames while
// scrolling to the top of the Thread (older pages load on the way), keydown-to-paint while a reply streams,
// and long tasks during that turn; then the hardware.
/* global window, document, requestAnimationFrame -- page.evaluate callbacks run in the browser */
import os from 'node:os';
import { chromium } from '/home/dev/.local/lib/uam-scripts/node_modules/playwright/index.mjs';

const base = (process.argv[2] ?? 'http://127.0.0.1:5178').replace(/\/+$/, '');
const count = Number(process.argv[3] ?? 2000);
const p95 = (xs) => (xs.length ? [...xs].sort((a, b) => a - b)[Math.min(xs.length - 1, Math.ceil(xs.length * 0.95) - 1)] : NaN);
const max = (xs) => (xs.length ? Math.max(...xs) : NaN);
const ms = (x) => (Number.isNaN(x) ? '-' : `${x.toFixed(1)} ms`);
const rows = [];
const report = (metric, bar, values, ok) => rows.push({ metric, bar, p95: ms(p95(values)), max: ms(max(values)), n: values.length, result: ok(values) ? 'PASS' : 'FAIL' });

const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const errors = [];
page.on('pageerror', (e) => errors.push(String(e)));
await page.addInitScript(() => {
  window.__longtasks = [];
  new PerformanceObserver((list) => { for (const e of list.getEntries()) window.__longtasks.push({ start: e.startTime, duration: e.duration }); }).observe({ type: 'longtask', buffered: true });
});
await page.goto(`${base}/?mock&perf=${count}#task=t1`);
await page.locator('button[data-nav="t-perf"]').waitFor({ timeout: 30_000 });
await page.waitForTimeout(1500);

// Each Thread's last transcript paragraph that no other of the three shows: a switch is done when it is painted.
const conversation = 'section[aria-label="Conversation"]';
const paragraphs = () => page.evaluate((sel) => [...document.querySelectorAll(`${sel} [role=log] p`)].map((p) => p.textContent), conversation);
const threads = ['t-perf', 't15', 't1'];
const seen = {};
for (const id of threads) {
  await page.click(`button[data-nav="${id}"]`);
  await page.waitForTimeout(2500);
  seen[id] = await paragraphs();
}
const marker = Object.fromEntries(threads.map((id) => [id, seen[id].findLast((text) => threads.every((other) => other === id || !seen[other].includes(text)))]));
if (Object.values(marker).some((text) => !text)) throw new Error(`no Thread-unique paragraph: ${JSON.stringify(marker)}`);

// Thread switch: click to the first frame after the target's last paragraph is in the DOM.
const switches = [];
for (let round = 0; round < 10; round++) {
  for (const id of threads) {
    await page.waitForTimeout(400);
    switches.push(await page.evaluate(({ id, text, sel }) => new Promise((resolve) => {
      const start = performance.now();
      document.querySelector(`button[data-nav="${id}"]`).click();
      const check = () => {
        const done = [...document.querySelectorAll(`${sel} [role=log] p`)].some((p) => p.textContent === text);
        if (done) requestAnimationFrame(() => resolve(performance.now() - start));
        else if (performance.now() - start > 10_000) resolve(Infinity);
        else requestAnimationFrame(check);
      };
      requestAnimationFrame(check);
    }), { id, text: marker[id], sel: conversation }));
  }
}
report('Thread switch', 'p95 ≤200 ms', switches, (v) => p95(v) <= 200);

// Scroll: from the bottom of t-perf to its first item at 40 px a frame (2,400 px/s at 60 Hz, a fast skim) with the
// pointer over the transcript; older pages load and render on the way. Only frames that moved the view count.
await page.click('button[data-nav="t-perf"]');
await page.waitForTimeout(2500);
await page.mouse.move(900, 400);
const scroll = await page.evaluate(({ sel, step: px }) => new Promise((resolve) => {
  const el = document.querySelector(sel);
  const frames = [];
  const start = performance.now();
  let last = start, stillSince = 0;
  const step = (now) => {
    if (!stillSince) frames.push(now - last);
    last = now;
    el.scrollTop -= px;
    if (el.scrollTop > 0) stillSince = 0;
    else stillSince ||= now;
    // At the top for 3 s with nothing older to load, or 120 s in all.
    if ((stillSince && now - stillSince > 3000) || now - start > 120_000) resolve({ frames: frames.slice(1), seconds: (now - start) / 1000, top: el.querySelector('[role=log] p')?.textContent ?? '' });
    else requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}), { sel: conversation, step: Number(process.env.SCROLL_STEP ?? 40) });
report('Scroll frame (to the first item)', 'p95 ≤20 ms', scroll.frames, (v) => p95(v) <= 20);

// A 60 s turn: reopen t-perf at its live end, send a prompt, type into the composer while the reply streams, and
// watch long tasks.
await page.goto('about:blank');
await page.goto(`${base}/?mock&perf=${count}#task=t-perf`);
await page.locator(`${conversation} [role=log] p`).first().waitFor({ timeout: 30_000 });
await page.waitForTimeout(2500);
await page.evaluate(() => {
  window.__keys = [];
  document.addEventListener('keydown', (e) => { const t0 = e.timeStamp; requestAnimationFrame(() => setTimeout(() => window.__keys.push(performance.now() - t0))); }, true);
});
const composer = page.locator('#composer-text');
await composer.click();
await composer.fill('Stream the whole audit for the performance check.');
const turnStart = await page.evaluate(() => performance.now());
await page.keyboard.press('Enter');
await page.waitForTimeout(3000);
await page.evaluate(() => { window.__keys = []; });
const phrase = 'typing while the reply streams into a long transcript ';
const typingUntil = Date.now() + 50_000;
while (Date.now() < typingUntil) {
  await composer.pressSequentially(phrase, { delay: 90 });
  await composer.fill('');
}
await page.locator('button[data-nav="t-perf"]').getByText('Finished').waitFor({ timeout: 120_000 });
const turnEnd = await page.evaluate(() => performance.now());
const keys = await page.evaluate(() => window.__keys);
report('Typing during a stream (keydown to paint)', 'p95 ≤50 ms, max ≤100 ms', keys, (v) => p95(v) <= 50 && max(v) <= 100);
const tasks = (await page.evaluate(() => window.__longtasks)).filter((t) => t.start >= turnStart && t.start <= turnEnd).map((t) => t.duration);
rows.push({ metric: `Long tasks in the ${((turnEnd - turnStart) / 1000).toFixed(0)} s turn`, bar: 'none >200 ms', p95: ms(p95(tasks)), max: ms(max(tasks)), n: tasks.length, result: max(tasks) > 200 ? 'FAIL' : 'PASS' });

const slow = scroll.frames.filter((f) => f > 20).length;
console.log(`Fixture: ${count} items; scroll ran ${scroll.seconds.toFixed(1)} s, ${slow} of ${scroll.frames.length} frames (${((100 * slow) / scroll.frames.length).toFixed(1)}%) over 20 ms, and reached "${scroll.top.slice(0, 60)}"`);
console.table(rows);
const cpus = os.cpus();
console.log(`Hardware: ${cpus[0].model}, ${cpus.length} cores, ${(os.totalmem() / 2 ** 30).toFixed(0)} GiB, ${os.platform()} ${os.release()}; Chromium ${browser.version()} headless, viewport 1440×900`);
if (errors.length) console.log('Page errors:', errors);
await browser.close();
