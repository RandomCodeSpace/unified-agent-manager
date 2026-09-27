import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8');

test('the manifest is fetched with cookies, so a sign-in proxy in front of the service lets it through', () => {
  const link = html.match(/<link rel="manifest"[^>]*>/)?.[0];
  assert.ok(link, 'index.html links the manifest');
  assert.match(link, /\scrossorigin="use-credentials"/);
});
