import assert from 'node:assert/strict';
import test from 'node:test';
import { registerHooks } from 'node:module';
const hooks = registerHooks({ resolve(specifier, context, next) { return next(['./lib/models', './lib/reads', './lib/preview'].includes(specifier) ? `${specifier}.ts` : specifier, context); } });
const { api, createApiClient, onUnauthorized, errorCode } = await import('../src/api.ts');
hooks.deregister();
const connection = (id = 'remote-b') => ({ id, instance_id: `instance-${id}`, label: id, enabled: true, generation: 3, capabilities: ['files-v1', 'terminal-v1', 'configuration-v1', 'provider-accounts-v1', 'routines-v1', 'usage-v1'] });

test('colliding task and project IDs always route through the captured owner and generation', async () => {
  const original = globalThis.fetch, requests = [];
  globalThis.fetch = async (url, options) => { requests.push({ url, options }); return new Response('{}'); };
  try {
    const b = createApiClient(connection()), c = createApiClient(connection('remote-c'));
    await Promise.all([b.prompt('same/id', 'B', 'request-b'), c.prompt('same/id', 'C', 'request-c'), api.prompt('same/id', 'A', 'request-a')]);
    await b.routines('same-project');
    await c.configuration('same-project');
    assert.match(requests[0].url, /^\/api\/connected\/remote-b\/api\/sessions\/same%2Fid\/prompt\?uam_generation=3$/);
    assert.match(requests[1].url, /^\/api\/connected\/remote-c\//);
    assert.equal(requests[2].url, '/api/sessions/same%2Fid/prompt');
    assert.match(requests[3].url, /^\/api\/connected\/remote-b\/api\/projects\/same-project\/routines\?uam_generation=3$/);
    assert.match(requests[4].url, /^\/api\/connected\/remote-c\/api\/configuration\?project_id=same-project&uam_generation=3$/);
    assert.ok(requests.every(request => request.options.credentials === 'same-origin'));
  } finally { globalThis.fetch = original; }
});

test('old clients fail closed before issuing requests after a generation is invalidated', async () => {
  const original = globalThis.fetch; let current = true, calls = 0;
  globalThis.fetch = async () => { calls++; return new Response('{}'); };
  try {
    const b = createApiClient(connection(), () => current);
    current = false;
    await assert.rejects(b.prompt('same', 'stale text', 'request'), error => error.status === 409 && errorCode(error) === 'connection_changed');
    await assert.rejects(b.exportMarkdown('same'), error => error.status === 409);
    assert.match(b.eventsUrl('same'), /uam_generation=3$/, 'native URLs retain their old generation so the server rejects them');
    assert.equal(calls, 0);
  } finally { globalThis.fetch = original; }
});

test('remote authentication loss cannot sign out the hosting UAM for JSON, exports or file reads', async () => {
  const original = globalThis.fetch; let signedOut = 0;
  onUnauthorized(() => signedOut++);
  globalThis.fetch = async () => new Response(JSON.stringify({ error: 'Remote key expired', code: 'remote_auth_required' }), { status: 424 });
  try {
    const b = createApiClient(connection());
    for (const read of [() => b.meta(), () => b.exportMarkdown('task'), () => b.filePreview(b.viewFileUrl('task', 'a.txt'), new AbortController().signal)]) {
      await assert.rejects(read(), error => errorCode(error) === 'remote_auth_required');
    }
    assert.equal(signedOut, 0);
    globalThis.fetch = async () => new Response('{}', { status: 401 });
    await assert.rejects(b.meta());
    assert.equal(signedOut, 1, 'a real home-cookie 401 still signs out the home UI');
  } finally { globalThis.fetch = original; onUnauthorized(() => {}); }
});

test('resource and stream URLs retain owner, generation and encoded identifiers', () => {
  const b = createApiClient(connection());
  for (const url of [b.attachmentUrl('same/id', 'a/b'), b.rawFileUrl('same', '/tmp/a b'), b.viewFileUrl('same', 'dir/a b.html'), b.eventsUrl('same'), b.detailEventsUrl('same', 'agent', [], undefined, 'epoch'), b.url('/api/projects/p/terminal?cols=80&rows=24')]) {
    const parsed = new URL(url, 'https://home.test');
    assert.ok(parsed.pathname.startsWith('/api/connected/remote-b/api/'));
    assert.equal(parsed.searchParams.get('uam_generation'), '3');
  }
  const signed = '/api/connected/remote-b/grants/task/grant/signed';
  assert.equal(b.url(signed), signed);
  assert.throws(() => b.url('https://remote.test/api/sessions/x'));
});

test('owner storage and archives cannot collide while existing local keys stay compatible', () => {
  const b = createApiClient(connection()), c = createApiClient(connection('remote-c'));
  assert.equal(api.storageKey('uam.draft.same'), 'uam.draft.same');
  assert.equal(api.cacheKey('same'), 'same');
  assert.notEqual(b.storageKey('uam.draft.same'), c.storageKey('uam.draft.same'));
  assert.notEqual(b.cacheKey('same'), c.cacheKey('same'));
  assert.ok(!b.storageKey('uam.draft.same').startsWith('uam.draft.'));
});

test('missing optional capability fails with an explicit unavailable error without network traffic', async () => {
  const original = globalThis.fetch; let calls = 0;
  globalThis.fetch = async () => { calls++; return new Response('{}'); };
  try {
    const b = createApiClient({ ...connection(), capabilities: [] });
    assert.equal(b.supports('configuration-v1'), false);
    await assert.rejects(b.configuration(), error => errorCode(error) === 'feature_unavailable' && /does not support configuration/.test(error.message));
    assert.match(b.viewFileUrl('task', 'a.html'), /^\/api\/connected\/remote-b\//, 'rendering an unavailable native link must not throw');
    await assert.rejects(b.filePreview(b.viewFileUrl('task', 'a.html'), new AbortController().signal), error => errorCode(error) === 'feature_unavailable');
    assert.equal(calls, 0);
  } finally { globalThis.fetch = original; }
});
