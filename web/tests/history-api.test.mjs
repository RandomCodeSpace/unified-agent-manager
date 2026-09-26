import assert from 'node:assert/strict';
import test from 'node:test';
import { registerHooks } from 'node:module';

// The browser resolves extensionless TypeScript imports; node's strip-types runner does not.
const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    return nextResolve(['./lib/models', './lib/reads', './lib/preview'].includes(specifier) ? `${specifier}.ts` : specifier, context);
  },
});
const { api, UPDATE_EVENTS } = await import('../src/api.ts');
hooks.deregister();

test('selected task streams request and listen for incremental tool output', () => {
  assert.equal(api.eventsUrl('task/id'), '/api/events?session=task%2Fid&tool_output=delta&history=recent&view=compact-v1');
  assert.equal(api.eventsUrl(null), '/api/events');
  assert.ok(UPDATE_EVENTS.includes('tool_output'));
  assert.ok(UPDATE_EVENTS.includes('items_trimmed'));
});

test('subagent transcript requests carry their cancellation signal', async () => {
  const original = globalThis.fetch;
  const controller = new AbortController();
  globalThis.fetch = async (_path, options) => {
    assert.equal(options.signal, controller.signal);
    return new Response('{}');
  };
  try { await api.subagent('task', 'helper', controller.signal); }
  finally { globalThis.fetch = original; }
});

test('older history uses an escaped cursor and cancellable read-only request', async () => {
  const original = globalThis.fetch;
  const controller = new AbortController();
  globalThis.fetch = async (path, options) => {
    assert.equal(path, '/api/sessions/task%2Fid/history?before=opaque%2Fcursor&view=compact-v1');
    assert.equal(options.method, 'GET');
    assert.equal(options.signal, controller.signal);
    return new Response('{"seq":10,"items":[],"before":""}');
  };
  try { assert.deepEqual(await api.history('task/id', 'opaque/cursor', controller.signal), { seq: 10, items: [], before: '' }); }
  finally { globalThis.fetch = original; }
});

test('history and discovery use read-only routes; importing posts only an empty JSON object', async () => {
  const requests = [];
  const original = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    requests.push({ path, ...options });
    return new Response(JSON.stringify({}), { status: 200 });
  };
  try {
    await api.session('task/id');
    await api.previous('project/id');
    assert.ok(requests.every((r) => r.method === 'GET' && r.body === undefined));
    await api.importPrevious('project/id', 'conversation/id');
    assert.deepEqual(requests.map((r) => r.path), [
      '/api/sessions/task%2Fid?history=recent&view=compact-v1', '/api/projects/project%2Fid/previous',
      '/api/projects/project%2Fid/previous/conversation%2Fid/import',
    ]);
    assert.equal(requests[2].method, 'POST');
    assert.equal(requests[2].body, '{}');
    assert.equal(requests[2].headers['Content-Type'], 'application/json');
    assert.ok(UPDATE_EVENTS.includes('history'));
  } finally {
    globalThis.fetch = original;
  }
});

test('detail interest URLs preserve agent identity and resume only explicitly covered bodies', () => {
  const url = new URL(api.detailEventsUrl('task/id', 'helper', [{ agentId: '', itemId: 'shared', coveredSeq: 9 }, { agentId: 'helper', itemId: 'shared' }], 'cursor', 'epoch'), 'http://localhost');
  assert.equal(url.searchParams.get('session'), 'task/id');
  assert.equal(url.searchParams.get('agent'), 'helper');
  assert.equal(url.searchParams.get('agent_before'), 'cursor');
  assert.equal(url.searchParams.get('epoch'), 'epoch');
  assert.deepEqual(url.searchParams.getAll('item').map(JSON.parse), [['', 'shared', 9], ['helper', 'shared']]);
});

test('one-off bodies and agent pages use identity-scoped cancellable reads', async () => {
  const original = globalThis.fetch, controller = new AbortController(), paths = [];
  globalThis.fetch = async (path, options) => {
    assert.equal(options.method, 'GET'); assert.equal(options.signal, controller.signal); paths.push(path); return new Response('{}');
  };
  try {
    await api.itemBody('task/id', 'item/id', 'agent/id', controller.signal);
    await api.subagentHistory('task/id', 'agent/id', 'cursor/id', controller.signal);
    assert.deepEqual(paths, ['/api/sessions/task%2Fid/items/item%2Fid?agent_id=agent%2Fid', '/api/sessions/task%2Fid/subagents/agent%2Fid/history?before=cursor%2Fid&view=compact-v1']);
  } finally { globalThis.fetch = original; }
});
