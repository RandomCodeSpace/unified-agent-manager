import assert from 'node:assert/strict';
import test from 'node:test';
import { FileCache, FileReferences, FILE_CACHE_BYTES, fitsFileBatch, hintPath, taskFileCache } from '../src/lib/fileReferences.ts';

const tool = (name, file_paths, status = 'completed') => ({ id: 'tool', kind: 'tool', time: '', tool: { name, file_paths, status } });

test('file-like inline names are looked up; extensionless bare names need an exact local hint; paths remain literal', () => {
  const owner = new FileReferences('/repo', async paths => answer(paths));
  for (const name of ['package.json', '.gitignore', 'main.go:3']) assert.equal(owner.eligible(name, name.replace(/:\d+$/, '')), true, name);
  assert.equal(owner.eligible('Makefile', 'Makefile'), false);
  owner.setItems('main', [tool('view', ['Makefile'])]);
  assert.equal(owner.eligible('Makefile', 'Makefile'), true);
  owner.setItems('main', [tool('create', ['/repo/Makefile'])]);
  assert.equal(owner.eligible('Makefile', 'Makefile'), true);
  for (const name of ['github-mcp-server-get_file_contents', 'glob', 'grep', 'rg', 'read', 'write']) {
    owner.setItems('main', [tool(name, ['Makefile'])]);
    assert.equal(owner.eligible('Makefile', 'Makefile'), false);
  }
  assert.equal(owner.eligible('./package.json', 'package.json'), true);
  assert.equal(hintPath('/repo/./文%23#?.txt', '/repo'), '文%23#?.txt');
  assert.equal(hintPath('/elsewhere/package.json', '/repo'), undefined);
});

test('a completed declaration hints only its exact workdir path without asserting existence', () => {
  const owner = new FileReferences('/repo', async paths => answer(paths));
  const declaration = { artifact_id: 'artifact-1', path: '/repo/README', title: 'Untrusted display' };
  const item = { id: 'call-1', kind: 'tool', tool: { name: 'uam_show_file', status: 'completed', declaration, file_paths: ['/repo/README', '/repo/other'] } };
  owner.setItems('main', [item]);
  assert.equal(owner.eligible('README', 'README'), true);
  assert.equal(owner.eligible('other', 'other'), false);
  assert.equal(owner.cache.get('README'), undefined);
  owner.setItems('main', [{ ...item, tool: { ...item.tool, status: 'failed' } }]);
  assert.equal(owner.eligible('README', 'README'), false);
  owner.setItems('main', [{ ...item, tool: { ...item.tool, declaration: undefined } }]);
  assert.equal(owner.eligible('README', 'README'), false);
  owner.setItems('main', [{ ...item, tool: { ...item.tool, file_paths: ['/tmp/README'] } }]);
  assert.equal(owner.eligible('README', 'README'), false);
  for (const bad of [
    { ...declaration, artifact_id: '' },
    { ...declaration, artifact_id: undefined },
    { ...declaration, path: 'README' },
    { ...declaration, path: '' },
  ]) {
    owner.setItems('main', [{ ...item, tool: { ...item.tool, declaration: bad, file_paths: [bad.path] } }]);
    assert.equal(owner.eligible('README', 'README'), false);
  }
});

test('positive/negative TTLs and both LRU caps are enforced', () => {
  const cache = new FileCache();
  cache.set('yes', true, 0); cache.set('no', false, 0);
  assert.equal(cache.get('no', 4999), false);
  assert.equal(cache.get('no', 5000), undefined);
  assert.equal(cache.get('yes', 29999), true);
  assert.equal(cache.fresh('yes', 29999), true);
  // An expired positive answer stays shown until a fresh answer replaces it.
  assert.equal(cache.get('yes', 30000), true);
  assert.equal(cache.fresh('yes', 30000), false);
  for (let i = 0; i < 256; i++) cache.set(`p${i}`, true, 0);
  cache.get('p0', 1); cache.set('next', true, 1);
  assert.equal(cache.size, 256); assert.equal(cache.get('p1', 1), undefined); assert.equal(cache.get('p0', 1), true);
  cache.clear();
  for (let i = 0; i < 256; i++) cache.set(`${i}/${'x'.repeat(4090)}`, true, 0);
  assert.ok(cache.bytes <= FILE_CACHE_BYTES); assert.ok(cache.size < 256);
});

test('batch count, escaped serialized bytes, UTF-8 size, and invalid paths are bounded', () => {
  assert.equal(fitsFileBatch(Array(64).fill('x'), 'next'), false);
  assert.equal(fitsFileBatch([], '文'.repeat(1366)), false);
  assert.equal(fitsFileBatch([], 'bad\0path'), false);
  assert.equal(fitsFileBatch(Array(30).fill('\u0001'.repeat(4096)), 'next'), false);
  assert.equal(fitsFileBatch([], '文%#.txt'), true);
});

function dom(t, count) {
  const beforeWindow = globalThis.window, beforeObserver = globalThis.IntersectionObserver;
  const beforeFrame = globalThis.requestAnimationFrame, beforeCancel = globalThis.cancelAnimationFrame;
  globalThis.requestAnimationFrame = callback => setTimeout(callback, 0);
  globalThis.cancelAnimationFrame = clearTimeout;
  globalThis.window = { innerHeight: 1000 };
  globalThis.IntersectionObserver = class {
    constructor(callback) { this.callback = callback; }
    observe(target) { this.callback([{ target, isIntersecting: true }]); }
    unobserve() {}
    disconnect() {}
  };
  t.after(() => { globalThis.window = beforeWindow; globalThis.IntersectionObserver = beforeObserver; globalThis.requestAnimationFrame = beforeFrame; globalThis.cancelAnimationFrame = beforeCancel; });
  const candidates = Array.from({ length: count }, (_, i) => ({
    dataset: { fileReference: `src/${i}.ts` }, getBoundingClientRect: () => ({ top: 10, bottom: 20 }), getClientRects: () => [1],
  }));
  return { querySelectorAll: () => candidates };
}
const tick = () => new Promise(resolve => setTimeout(resolve, 5));
const answer = paths => ({ files: paths.map(path => ({ path, exists: true, kind: 'file' })) });

test('visible overflow drains once without starvation or eviction retry loops', async t => {
  const root = dom(t, 300), batches = [];
  const owner = new FileReferences('/repo', async paths => { batches.push(paths); return answer(paths); });
  t.after(() => owner.stop());
  owner.observe(root); owner.start();
  for (let i = 0; i < 20; i++) await tick();
  assert.deepEqual(batches.map(paths => paths.length), [64, 64, 64, 64, 44]);
  assert.equal(new Set(batches.flat()).size, 300);
  assert.equal(owner.cache.size, 256);
});

for (const exists of [true, false]) test(`an expired ${exists ? 'available' : 'missing'} reference resolves again without leaving the conversation`, async t => {
  const root = dom(t, 1); let calls = 0;
  const owner = new FileReferences('/repo', async paths => {
    calls++;
    return calls === 1 && !exists ? { files: paths.map(path => ({ path, exists: false, kind: 'unavailable' })) } : answer(paths);
  });
  t.after(() => owner.stop());
  owner.observe(root); owner.start();
  await tick(); await tick();
  assert.equal(owner.cache.get('src/0.ts'), exists);
  // A later React snapshot reads the same path after its positive or negative TTL: a link
  // stays a link while it is rechecked; a missing answer reads as unknown.
  assert.equal(owner.cache.get('src/0.ts', Date.now() + (exists ? 30_001 : 5_001)), exists ? true : undefined);
  if (exists) owner.cache.set('src/0.ts', true, Date.now() - 30_001);
  owner.demand();
  await tick(); await tick();
  assert.equal(calls, 2);
  assert.equal(owner.cache.get('src/0.ts'), true);
});

test('errors remain unknown and retry with backoff or on new demand; disposal rejects a late response', async t => {
  const root = dom(t, 1); let calls = 0, finish, signal;
  const owner = new FileReferences('/repo', async (paths, controllerSignal) => {
    calls++; signal = controllerSignal;
    if (calls === 1) throw new Error('transport');
    return new Promise(resolve => { finish = () => resolve(answer(paths)); });
  });
  t.after(() => owner.stop());
  owner.observe(root); owner.start();
  await tick(); await tick();
  assert.equal(calls, 1); assert.equal(owner.cache.get('src/0.ts'), undefined);
  owner.scrolled(); await tick(); await tick();
  assert.equal(calls, 1, 'scrolling with the same visible candidate must not retry errors at once');
  owner.focus(); await tick(); assert.equal(calls, 2);
  owner.stop(); assert.equal(signal.aborted, true);
  finish(); await tick(); assert.equal(owner.cache.size, 0);
});

test('a failed lookup retries its visible candidates after a delay', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const root = dom(t, 1); let calls = 0;
  const owner = new FileReferences('/repo', async paths => { calls++; if (calls === 1) throw new Error('remote unavailable'); return answer(paths); });
  t.after(() => owner.stop());
  owner.observe(root); owner.start();
  for (let i = 0; i < 5; i++) { t.mock.timers.tick(0); await Promise.resolve(); }
  assert.equal(calls, 1);
  t.mock.timers.tick(2_000);
  for (let i = 0; i < 5; i++) { t.mock.timers.tick(0); await Promise.resolve(); }
  assert.equal(calls, 2);
  assert.equal(owner.cache.get('src/0.ts'), true);
});

test('answers outlive one lookup lifetime of the same client and Task folder', () => {
  const client = {};
  const cache = taskFileCache(client, 'task', '/repo');
  const first = new FileReferences('/repo', async paths => answer(paths), cache);
  first.cache.set('a.ts', true); first.stop();
  assert.equal(new FileReferences('/repo', async paths => answer(paths), taskFileCache(client, 'task', '/repo')).cache.get('a.ts'), true);
  assert.equal(taskFileCache(client, 'other', '/repo').get('a.ts'), undefined);
  assert.equal(taskFileCache({}, 'task', '/repo').get('a.ts'), undefined);
});

test('completed local edits invalidate hints, including delete, without asserting missing', async t => {
  dom(t, 0);
  const owner = new FileReferences('/repo', async paths => answer(paths));
  owner.setItems('main', [tool('apply_patch', ['old.ts', 'new.ts'], 'running')]);
  owner.cache.set('old.ts', true); owner.cache.set('new.ts', false);
  owner.setItems('main', [tool('apply_patch', ['old.ts', 'new.ts'])]);
  assert.equal(owner.cache.get('old.ts'), undefined); assert.equal(owner.cache.get('new.ts'), undefined);
  owner.setItems('agent', [tool('view', ['Makefile'])]);
  assert.equal(owner.eligible('Makefile', 'Makefile'), true);
  // Closing a subagent's panel keeps its hints, so links that rely on them stay links.
  owner.removeItems('agent'); assert.equal(owner.eligible('Makefile', 'Makefile'), true);
});
