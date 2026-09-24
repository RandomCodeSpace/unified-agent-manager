import assert from 'node:assert/strict';
import test from 'node:test';
import { registerHooks } from 'node:module';

// The browser resolves extensionless TypeScript imports; node's strip-types runner does not.
const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    return nextResolve(specifier === './lib/models' ? './lib/models.ts' : specifier, context);
  },
});
const { api, UPDATE_EVENTS } = await import('../src/api.ts');
hooks.deregister();

test('history and discovery use read-only routes; importing posts only an empty JSON object', async () => {
  const requests = [];
  const original = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    requests.push({ path, ...options });
    return new Response(JSON.stringify({}), { status: 200 });
  };
  try {
    await api.session('task/id');
    await api.previousCounts();
    await api.previous('project/id');
    assert.ok(requests.every((r) => r.method === 'GET' && r.body === undefined));
    await api.importPrevious('project/id', 'conversation/id');
    assert.deepEqual(requests.map((r) => r.path), [
      '/api/sessions/task%2Fid', '/api/previous/counts', '/api/projects/project%2Fid/previous',
      '/api/projects/project%2Fid/previous/conversation%2Fid/import',
    ]);
    assert.equal(requests[3].method, 'POST');
    assert.equal(requests[3].body, '{}');
    assert.equal(requests[3].headers['Content-Type'], 'application/json');
    assert.ok(UPDATE_EVENTS.includes('history'));
  } finally {
    globalThis.fetch = original;
  }
});
