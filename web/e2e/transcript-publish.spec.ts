// Published pages (uam_publish): the card, the page opened from its snapshot, a republish adding a version, and the Thread's published pages panel.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test } from './harness';

test('a published site opens from its card, republishing adds a version, and the published pages panel lists and deletes versions', async ({ page, project, model, workdir, uam }) => {
  const site = path.join(workdir, 'site');
  fs.mkdirSync(site);
  // Another origin of this same service: reachable, so only the page's policy can stop it.
  const other = new URL(uam.url);
  other.hostname = other.hostname === 'localhost' ? '127.0.0.1' : 'localhost';
  fs.writeFileSync(path.join(site, 'index.html'), '<!doctype html><script>addEventListener("securitypolicyviolation", (e) => { document.getElementById("blocked").textContent += e.violatedDirective + " "; });</script>' +
    '<link rel="stylesheet" href="style.css"><h1>Demo title</h1><p id="out"></p><p id="blocked"></p><img id="own" src="logo.svg" alt="own">' +
    `<img alt="external" src="${other.origin}/favicon.svg"><script src="${other.origin}/sw.js"></script><script src="app.js"></script>\n`);
  fs.writeFileSync(path.join(site, 'logo.svg'), '<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>\n');
  fs.writeFileSync(path.join(site, 'style.css'), 'h1 { color: rgb(200, 0, 0) }\n');
  fs.writeFileSync(path.join(site, 'app.js'), "document.getElementById('out').textContent = 'first build';\n");
  model.toolRound('publish the site', [{ id: 'call_pub1', name: 'uam_publish', arguments: { path: 'site', title: 'Demo site' } }], 'Published the site.');
  model.toolRound('publish it again', [{ id: 'call_pub2', name: 'uam_publish', arguments: { path: 'site', title: 'Demo site' } }], 'Published again.');
  await startThread(page, project, 'publish the site');
  const conv = conversation(page);
  await expect(conv.getByText('Published the site.')).toBeVisible();
  const cards = conv.getByRole('region', { name: 'Published: Demo site' });
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toContainText('Version 1');

  await cards.first().getByRole('button', { name: 'Open' }).click();
  const preview = page.getByRole('complementary', { name: 'Preview Demo site' });
  const frame = preview.locator('iframe').contentFrame();
  await expect(frame.getByText('first build')).toBeVisible();
  await expect.poll(() => frame.getByRole('heading', { name: 'Demo title' }).evaluate((el) => getComputedStyle(el).color)).toBe('rgb(200, 0, 0)');
  // The page's own image loads; an image and a script from another origin are refused by its policy.
  await expect.poll(() => frame.locator('#own').evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth)).toBe(10);
  await expect(frame.locator('#blocked')).toContainText('img-src');
  await expect(frame.locator('#blocked')).toContainText('script-src');

  // The snapshot keeps the first build; publishing again makes version 2.
  fs.writeFileSync(path.join(site, 'app.js'), "document.getElementById('out').textContent = 'second build';\n");
  await preview.getByRole('button', { name: 'Reload' }).click();
  await expect(frame.getByText('first build')).toBeVisible();
  await preview.getByRole('button', { name: 'Close preview' }).click();
  await send(page, 'publish it again');
  await expect(conv.getByText('Published again.')).toBeVisible();
  await expect(cards).toHaveCount(2);
  await expect(cards.last()).toContainText('Version 2');
  await page.reload();
  await expect(cards.first()).toContainText('Version 1 of 2');
  await cards.first().getByRole('button', { name: 'Open version 2' }).click();
  await expect(page.getByRole('complementary', { name: 'Preview Demo site' }).locator('iframe').contentFrame().getByText('second build')).toBeVisible();
  await page.getByRole('button', { name: 'Close preview' }).click();

  // The header's Pages opens the Thread's published pages, each version newest first.
  await page.getByRole('button', { name: 'Published pages, 1' }).click();
  const panel = page.getByRole('complementary', { name: 'Published pages' });
  const versions = panel.getByRole('list', { name: 'Versions of Demo site' });
  await expect(versions.getByRole('listitem')).toHaveCount(2);
  await expect(versions.getByRole('listitem').first()).toContainText('Version 2');
  await panel.getByRole('button', { name: 'Delete version 1 of Demo site' }).click();
  await page.getByRole('alertdialog', { name: 'Delete version 1?' }).getByRole('button', { name: 'Delete' }).click();
  await expect(versions.getByRole('listitem')).toHaveCount(1);
  await panel.getByRole('button', { name: 'Open version 2 of Demo site' }).click();
  await expect(page.getByRole('complementary', { name: 'Preview Demo site' }).locator('iframe').contentFrame().getByText('second build')).toBeVisible();
});

test('a published module page runs in the preview: its imports, JSON and CSS are bundled and its font is inlined', async ({ page, project, model, workdir }) => {
  const site = path.join(workdir, 'modsite');
  fs.mkdirSync(path.join(site, 'lib'), { recursive: true });
  fs.writeFileSync(path.join(site, 'index.html'), '<!doctype html><link rel="stylesheet" href="fonts.css"><p id="out"></p><p id="font"></p>' +
    '<script type="module" src="main.js"></script><script type="module">import { greet } from "./lib/greet.js"; document.getElementById("out").dataset.inline = greet("inline");</script>\n');
  fs.writeFileSync(path.join(site, 'main.js'), "import { greet } from './lib/greet.js';\nimport data from './data.json';\nimport './style.css';\n" +
    "document.getElementById('out').textContent = greet(data.name);\n" +
    "document.fonts.load('16px Published').then(() => { document.getElementById('font').textContent = document.fonts.check('16px Published') ? 'font loaded' : 'font missing'; });\n");
  fs.writeFileSync(path.join(site, 'lib', 'greet.js'), 'export const greet = (name) => `module says ${name}`;\n');
  fs.writeFileSync(path.join(site, 'data.json'), '{ "name": "hello from JSON" }\n');
  fs.writeFileSync(path.join(site, 'style.css'), '#out { color: rgb(0, 120, 0) }\n');
  fs.writeFileSync(path.join(site, 'fonts.css'), '@font-face { font-family: Published; src: url(face.woff2) format("woff2") }\n');
  fs.copyFileSync(path.join(import.meta.dirname, '..', 'node_modules', 'katex', 'dist', 'fonts', 'KaTeX_Main-Regular.woff2'), path.join(site, 'face.woff2'));
  model.toolRound('publish the module site', [{ id: 'call_mod1', name: 'uam_publish', arguments: { path: 'modsite', title: 'Module site' } }], 'Published the module site.');
  await startThread(page, project, 'publish the module site');
  const conv = conversation(page);
  await expect(conv.getByText('Published the module site.')).toBeVisible();
  await conv.getByRole('region', { name: 'Published: Module site' }).getByRole('button', { name: 'Open' }).click();
  const frame = page.getByRole('complementary', { name: 'Preview Module site' }).locator('iframe').contentFrame();
  await expect(frame.locator('#out')).toHaveText('module says hello from JSON');
  await expect(frame.locator('#out')).toHaveAttribute('data-inline', 'module says inline');
  await expect.poll(() => frame.locator('#out').evaluate((el) => getComputedStyle(el).color)).toBe('rgb(0, 120, 0)');
  await expect(frame.locator('#font')).toHaveText('font loaded');
});
