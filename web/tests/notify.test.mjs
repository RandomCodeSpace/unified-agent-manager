import assert from 'node:assert/strict';
import test from 'node:test';
import { registerHooks } from 'node:module';

// The browser resolves extensionless TypeScript imports; node's strip-types runner does not.
const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    return nextResolve(['../api', './lib/models', './lib/reads', './lib/preview'].includes(specifier) ? `${specifier}.ts` : specifier, context);
  },
});
const { NOTIFY_KEY, claimNotice, needsHomeScreen, noticeKey, parseNotifyMode } = await import('../src/lib/notify.ts');
hooks.deregister();

test('the stored notification mode is push, page or off', () => {
  assert.equal(NOTIFY_KEY, 'uam.notify');
  assert.equal(parseNotifyMode('push'), 'push');
  assert.equal(parseNotifyMode('page'), 'page');
  assert.equal(parseNotifyMode(null), null);
  assert.equal(parseNotifyMode('on'), null);
});

test('a notice is claimed by one tab only, and old claims are dropped', () => {
  const key = noticeKey({ session_id: 't1', kind: 'question', seq: 7 });
  assert.equal(key, 't1:question:7');
  const first = claimNotice({ old: 0 }, key, 1_000_000);
  assert.deepEqual(first, { [key]: 1_000_000 });
  assert.equal(claimNotice(first, key, 1_000_500), null);
  // Ten minutes on, a server restart may reuse the key: it is a new notice then.
  assert.deepEqual(claimNotice(first, key, 1_000_000 + 10 * 60 * 1000), { [key]: 1_000_000 + 10 * 60 * 1000 });
});

test('iPhone and iPad tabs need the Home Screen app; the installed app and other browsers do not', () => {
  const iphone = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1';
  const ipad = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15';
  assert.equal(needsHomeScreen(iphone, 5, false), true);
  assert.equal(needsHomeScreen(iphone, 5, true), false);
  assert.equal(needsHomeScreen(ipad, 5, false), true);
  assert.equal(needsHomeScreen(ipad, 0, false), false); // a Mac
  assert.equal(needsHomeScreen('Mozilla/5.0 (X11; Linux x86_64) Chrome/140.0', 0, false), false);
});
