import fs from 'node:fs';
import path from 'node:path';
import type { BrowserContext, Page, WebSocketRoute } from '@playwright/test';
import { conversation, expect, openHome, send, startThread, test, threadState, type Project } from './harness';
import { ownService } from './shell-helpers';

/** The mock model as the Copilot provider lists it, for Threads made through the API. */
const MOCK_MODEL_ID = 'mock/mock-1';

type Api = (method: string, route: string, body?: unknown) => Promise<unknown>;

const idleThread = async (api: Api, project: Project, name: string) =>
  ((await api('POST', '/api/sessions', { project_id: project.id, provider: 'copilot', model: MOCK_MODEL_ID, name })) as { id: string }).id;

const threads = (page: Page) => page.getByRole('navigation', { name: 'Threads' });
const LOST = 'Connection lost, reconnecting. Work continues on the server.';

test('four tabs on plain HTTP each take live updates over one WebSocket, with no EventSource', async ({ context, page, project, model }) => {
  model.title('four tabs prompt', 'Four tabs thread').reply('four tabs prompt', 'one two three', { latency: 200, chunkSize: 4 });
  const url = await startThread(page, project, 'four tabs prompt');
  await expect(threadState(page, 'Finished')).toBeVisible();
  model.reply('second for all tabs', 'Every tab sees this.', { latency: 100, chunkSize: 4 });

  const tabs: Page[] = [];
  const sockets: string[][] = [];
  const sources: string[] = [];
  for (let i = 0; i < 4; i++) {
    const tab = await context.newPage();
    const own: string[] = [];
    sockets.push(own);
    tab.on('websocket', (ws) => own.push(new URL(ws.url()).pathname));
    tab.on('request', (r) => { if (r.resourceType() === 'eventsource') sources.push(r.url()); });
    await tab.goto(url);
    await expect(conversation(tab).getByText('one two three')).toBeVisible();
    tabs.push(tab);
  }
  await send(tabs[0], 'second for all tabs');
  for (const tab of tabs) await expect(conversation(tab).getByText('Every tab sees this.')).toBeVisible();
  // One socket per stream the tab follows, and no other kind.
  for (const own of sockets) expect([...new Set(own)]).toEqual(['/api/events/socket']);
  expect(sources).toEqual([]);
  // The last tab still answers ordinary requests at once: no connection pool is exhausted.
  await threads(tabs[3]).getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(tabs[3].getByRole('navigation', { name: 'Settings sections' })).toBeVisible();
  for (const tab of tabs) await tab.close();
});

test('a stream that goes silent without closing is shown as lost and reopened, and live updates resume', async ({ page, uam, project, model }) => {
  const id = await idleThread(uam.api, project, 'Silent stream thread');
  model.reply('after the silence', 'Live again after the silence.');
  // The sockets open when `silent` is set stop passing the service's frames on; a socket opened later is left alone.
  let silent = false;
  let openedBefore = Infinity;
  let heartbeats = 0;
  const opened: WebSocketRoute[] = [];
  await page.routeWebSocket(/\/api\/events\/socket/, (ws) => {
    const index = opened.push(ws) - 1;
    const server = ws.connectToServer();
    server.onMessage((m) => {
      if (String(m).includes('keep-alive')) heartbeats++;
      if (!silent || index >= openedBefore) ws.send(m);
    });
    ws.onMessage((m) => server.send(m));
  });
  await page.clock.install();
  await page.goto(`/#task=${id}`);
  await expect(page.getByRole('heading', { level: 1, name: 'Silent stream thread' })).toBeVisible();
  await expect(page.getByRole('status').filter({ hasText: /^Connected$/ })).toBeAttached();
  // The silence counts from a heartbeat: the service writes one every 15 s.
  await expect.poll(() => heartbeats, { timeout: 30_000 }).toBeGreaterThan(0);
  openedBefore = opened.length;
  silent = true;

  await page.clock.fastForward('00:40');
  await expect(page.getByRole('status').filter({ hasText: LOST })).toBeAttached();
  await page.clock.fastForward('00:10');
  await expect.poll(() => opened.length).toBeGreaterThan(openedBefore);
  await expect(page.getByRole('status').filter({ hasText: /^Connected$/ })).toBeAttached();
  await send(page, 'after the silence');
  await expect(conversation(page).getByText('Live again after the silence.')).toBeVisible();
});

/** A browser with page notifications and no service worker; every notification shown and permission request is recorded. */
async function notifyingContext(context: BrowserContext) {
  await context.addInitScript(() => {
    const w = window as unknown as { shown: { title: string; note: unknown }[]; asked: number };
    w.shown = [];
    w.asked = 0;
    Object.defineProperty(Navigator.prototype, 'serviceWorker', { configurable: true, get: () => undefined });
    // The browser's own prompt (the context grants it). Headless Chromium reports `permission` as denied even then,
    // so the answer is kept here, for every tab of the context, as the browser would keep it.
    const Real = window.Notification;
    class Recorded {
      static get permission(): NotificationPermission {
        return localStorage.getItem('e2e.notify-permission') === 'granted' ? 'granted' : 'default';
      }
      static async requestPermission() {
        w.asked++;
        const answer = await Real.requestPermission();
        localStorage.setItem('e2e.notify-permission', answer);
        return answer;
      }
      onclick: (() => void) | null = null;
      constructor(public title: string) {
        w.shown.push({ title, note: this });
      }
      close() {}
    }
    Object.defineProperty(window, 'Notification', { configurable: true, value: Recorded });
  });
}
const shown = (page: Page) => page.evaluate(() => (window as unknown as { shown: { title: string }[] }).shown.map((n) => n.title));
const asked = (page: Page) => page.evaluate(() => (window as unknown as { asked: number }).asked);

test('notifications: permission is asked only when turned on; a Thread finishing off screen is announced once across tabs, never while on screen; clicking opens it', async ({ browser, uam, project, model }) => {
  const context = await browser.newContext({ baseURL: uam.url, viewport: { width: 1280, height: 900 } });
  try {
    expect((await context.request.post('/api/login', { data: { token: uam.token } })).status()).toBe(204);
    await context.grantPermissions(['notifications']);
    await notifyingContext(context);
    const bystander = await idleThread(uam.api, project, 'Notify bystander');
    // Named by hand: the notice carries the name the Thread has when its turn ends, and a background title may land later.
    const target = await idleThread(uam.api, project, 'Notify target');
    const url = `${uam.url}/#task=${target}`;
    model.reply('notify me later', Array.from({ length: 6 }, (_, i) => `notify line ${i + 1}`).join('\n\n'), { latency: 100, chunkSize: 4 });
    model.reply('again while watching', 'Watched reply.');
    model.reply('third off screen', Array.from({ length: 6 }, (_, i) => `third line ${i + 1}`).join('\n\n'), { latency: 100, chunkSize: 4 });
    const a = await context.newPage();
    const b = await context.newPage();
    await a.goto('/#settings');
    await a.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'Appearance & alerts', exact: true }).click();
    const toggle = a.getByRole('switch', { name: 'Notify me when a thread needs me or finishes' });
    await expect(toggle).not.toBeChecked();
    expect(await asked(a)).toBe(0);
    await toggle.click();
    await expect(toggle).toBeChecked();
    expect(await asked(a)).toBe(1);
    await expect(a.getByText(/only while UAM is open in a tab/)).toBeVisible();
    await openHome(b);

    await a.goto(url);
    await send(a, 'notify me later');
    await expect(conversation(a).getByText('notify me later')).toBeVisible();
    await a.goto(`/#task=${bystander}`);
    await expect(a.getByRole('heading', { level: 1, name: 'Notify bystander' })).toBeVisible();
    await expect.poll(async () => [...await shown(a), ...await shown(b)]).toEqual(['Notify target: Finished']);

    // On screen in a visible page: not announced.
    await a.goto(url);
    await send(a, 'again while watching');
    await expect(conversation(a).getByText('Watched reply.')).toBeVisible();
    await expect(threadState(a, 'Finished')).toBeVisible();
    // A turn that ends off screen afterwards is announced: the watched one before it was not.
    await send(a, 'third off screen');
    await a.goto(`/#task=${bystander}`);
    await expect.poll(async () => [...await shown(a), ...await shown(b)]).toEqual(['Notify target: Finished', 'Notify target: Finished']);

    // A click on the notice opens its Thread in the tab that showed it.
    const holder = (await shown(a)).length ? a : b;
    await holder.evaluate(() => (window as unknown as { shown: { note: { onclick: () => void } }[] }).shown[0].note.onclick());
    await expect(holder).toHaveURL(url);
    await expect(holder.getByRole('heading', { level: 1, name: 'Notify target' })).toBeVisible();
  } finally {
    await context.close();
  }
});

test('the app manifest is served and the service worker registers when notifications are turned on', async ({ browser, uam }) => {
  const context = await browser.newContext({ baseURL: uam.url, viewport: { width: 1280, height: 900 } });
  try {
    expect((await context.request.post('/api/login', { data: { token: uam.token } })).status()).toBe(204);
    await context.grantPermissions(['notifications']);
    const page = await context.newPage();
    await page.goto('/#settings');
    const href = await page.locator('link[rel="manifest"]').getAttribute('href');
    const manifest = await (await context.request.get(href!)).json() as { name: string; start_url: string; display: string; icons: { src: string }[] };
    expect(manifest).toMatchObject({ name: 'UAM', start_url: '/', display: 'standalone' });
    for (const icon of manifest.icons) expect((await context.request.get(icon.src)).status()).toBe(200);
    expect(await page.evaluate(async () => (await navigator.serviceWorker.getRegistration('/')) === undefined)).toBe(true);

    await page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'Appearance & alerts', exact: true }).click();
    const toggle = page.getByRole('switch', { name: 'Notify me when a thread needs me or finishes' });
    await toggle.click();
    await expect(toggle).toBeChecked();
    await expect.poll(() => page.evaluate(async () => (await navigator.serviceWorker.getRegistration('/'))?.active?.scriptURL ?? '')).toMatch(/\/sw\.js$/);
  } finally {
    await context.close();
  }
});

test('a save the service refuses shows a strip over the pane until a save succeeds', async ({ browser, uam, workdir }) => {
  const own = await ownService(browser, uam.mock.url, workdir);
  const config = path.join(own.root, 'config');
  try {
    const { page } = own;
    const api = async (method: string, route: string, data?: unknown) => (await own.context.request.fetch(route, { method, data })).json();
    const id = await idleThread(api, own.project, 'Save target');
    await page.goto(`/#task=${id}`);
    await expect(page.getByRole('heading', { level: 1, name: 'Save target' })).toBeVisible();
    const strip = page.getByText(/^Threads could not be saved: .*UAM tries again with the next change\.$/);
    await expect(strip).toHaveCount(0);

    fs.chmodSync(config, 0o500);
    await api('PATCH', `/api/sessions/${id}`, { name: 'Save refused' });
    await expect(strip).toBeVisible();
    await expect(page.getByRole('heading', { level: 1, name: 'Save refused' })).toBeVisible();
    fs.chmodSync(config, 0o700);
    await api('PATCH', `/api/sessions/${id}`, { name: 'Save accepted' });
    await expect(page.getByRole('heading', { level: 1, name: 'Save accepted' })).toBeVisible();
    await expect(strip).toHaveCount(0);
    expect(fs.readFileSync(path.join(config, 'sessions.json'), 'utf8')).toContain('Save accepted');
  } finally {
    fs.chmodSync(config, 0o700);
    await own.stop();
  }
});

test('with browser storage refusing every read and write the app renders, opens Threads and shelves', async ({ browser, uam, project }) => {
  const open = await idleThread(uam.api, project, 'Storage open thread');
  const settled = await idleThread(uam.api, project, 'Storage settled thread');
  await uam.api('POST', `/api/sessions/${settled}/settle`, {});
  const context = await browser.newContext({ baseURL: uam.url, viewport: { width: 1280, height: 900 } });
  try {
    expect((await context.request.post('/api/login', { data: { token: uam.token } })).status()).toBe(204);
    await context.addInitScript(() => {
      const refuse = () => { throw new DOMException('Access is denied.', 'SecurityError'); };
      Storage.prototype.getItem = refuse;
      Storage.prototype.setItem = refuse;
      Storage.prototype.removeItem = refuse;
    });
    const page = await context.newPage();
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await openHome(page);
    await threads(page).getByRole('button', { name: / Storage open thread / }).click();
    await expect(page).toHaveURL(new RegExp(`#task=${open}$`));
    await expect(page.getByRole('heading', { level: 1, name: 'Storage open thread' })).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Message' })).toBeVisible();
    const shelf = threads(page).getByRole('button', { name: /^Settled \d+/ });
    const was = await shelf.getAttribute('aria-expanded');
    await shelf.click();
    await expect(shelf).toHaveAttribute('aria-expanded', was === 'true' ? 'false' : 'true');
    await expect(page.getByText('UAM could not show this page')).toHaveCount(0);
    expect(errors).toEqual([]);
  } finally {
    await context.close();
  }
});

