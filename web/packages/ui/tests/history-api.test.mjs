import assert from 'node:assert/strict';
import test from 'node:test';
import { registerHooks } from 'node:module';

// The browser resolves extensionless TypeScript imports; node's strip-types runner does not.
const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    return nextResolve(['./lib/models', './lib/reads', './lib/preview'].includes(specifier) ? `${specifier}.ts` : specifier, context);
  },
});
const { api, ApiError, PAGE_ID, READ_TIMEOUT_MS, UPDATE_EVENTS } = await import('../src/api.ts');
hooks.deregister();

test('selected task streams request and listen for incremental tool output', () => {
  assert.equal(api.eventsUrl('task/id'), `/api/events?session=task%2Fid&tool_output=delta&history=recent&view=compact-v1&page=${PAGE_ID}`);
  assert.equal(api.eventsUrl(null), `/api/events?page=${PAGE_ID}`);
  assert.ok(UPDATE_EVENTS.includes('tool_output'));
  assert.ok(UPDATE_EVENTS.includes('items_trimmed'));
});

/** A request the service never answers: it ends only when its signal aborts, as fetch does. */
const unanswered = (options) => new Promise((_resolve, reject) => options.signal?.addEventListener('abort', () => reject(options.signal.reason)));

test('transcript, history and body reads carry their cancellation', async () => {
  const original = globalThis.fetch;
  globalThis.fetch = (_path, options) => unanswered(options);
  try {
    for (const read of [
      (signal) => api.subagent('task', 'helper', signal),
      (signal) => api.history('task', 'cursor', signal),
      (signal) => api.itemBody('task', 'item', 'agent', signal),
      (signal) => api.subagentHistory('task', 'agent', 'cursor', signal),
    ]) {
      const controller = new AbortController();
      const cancelled = assert.rejects(read(controller.signal), { name: 'AbortError' });
      controller.abort();
      await cancelled;
    }
  } finally { globalThis.fetch = original; }
});

test('a read the service never answers is given up on and frees its place; a write keeps waiting', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const original = globalThis.fetch, started = [];
  globalThis.fetch = (path, options) => {
    started.push(path);
    return path.includes('/history') ? Promise.resolve(new Response('{"seq":10,"items":[],"before":""}')) : unanswered(options);
  };
  const late = (error) => error instanceof ApiError && error.status === 0 && error.message === 'The server did not answer in 60 seconds';
  try {
    // Two stalled reads hold both places: the page waits behind them.
    const stalled = [api.itemBody('task', 'a', ''), api.itemBody('task', 'b', '')].map((read) => assert.rejects(read, late));
    const page = api.history('task', 'cursor');
    // A lookup of files is a read too, though it posts.
    const lookup = assert.rejects(api.resolveFiles('task', ['a.txt']), late);
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(started.length, 2);
    t.mock.timers.tick(READ_TIMEOUT_MS - 1);
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(started.length, 2);
    t.mock.timers.tick(1);
    await Promise.all(stalled);
    assert.deepEqual(await page, { seq: 10, items: [], before: '' });
    await new Promise((resolve) => setImmediate(resolve));
    t.mock.timers.tick(READ_TIMEOUT_MS);
    await lookup;
    // A write may run long by design: it has no bound of its own.
    let signal = 'unset';
    globalThis.fetch = async (_path, options) => { signal = options.signal; return new Response('{}'); };
    await api.rename('task', 'name');
    assert.equal(signal, undefined);
  } finally { globalThis.fetch = original; }
});

test('older history uses an escaped cursor and cancellable read-only request', async () => {
  const original = globalThis.fetch;
  const controller = new AbortController();
  globalThis.fetch = async (path, options) => {
    assert.equal(path, '/api/sessions/task%2Fid/history?before=opaque%2Fcursor&view=compact-v1');
    assert.equal(options.method, 'GET');
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
    assert.equal(options.method, 'GET'); paths.push(path); return new Response('{}');
  };
  try {
    await api.itemBody('task/id', 'item/id', 'agent/id', controller.signal);
    await api.subagentHistory('task/id', 'agent/id', 'cursor/id', controller.signal);
    assert.deepEqual(paths, ['/api/sessions/task%2Fid/items/item%2Fid?agent_id=agent%2Fid', '/api/sessions/task%2Fid/subagents/agent%2Fid/history?before=cursor%2Fid&view=compact-v1']);
  } finally { globalThis.fetch = original; }
});
