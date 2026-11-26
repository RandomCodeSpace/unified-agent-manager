// Published pages (uam_publish): the card, the page opened from its snapshot, a republish adding a version, and the Thread's published pages panel.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test } from './harness';

test('a published site opens from its card, republishing adds a version, and the published pages panel lists and deletes versions', async ({ page, project, model, workdir }) => {
  const site = path.join(workdir, 'site');
  fs.mkdirSync(site);
  fs.writeFileSync(path.join(site, 'index.html'), '<!doctype html><link rel="stylesheet" href="style.css"><h1>Demo title</h1><p id="out"></p><script src="app.js"></script>\n');
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
