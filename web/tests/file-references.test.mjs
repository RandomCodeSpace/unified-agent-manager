import assert from 'node:assert/strict';
import test from 'node:test';
import { FileCache, FileReferences, FILE_CACHE_BYTES, fitsFileBatch, hintPath } from '../src/lib/fileReferences.ts';

const tool = (name, file_paths, status = 'completed') => ({ id: 'tool', kind: 'tool', time: '', tool: { name, file_paths, status } });

test('only exact local hints authorize bare inline names; paths remain literal', () => {
  const owner = new FileReferences('/repo', async paths => answer(paths));
  assert.equal(owner.eligible('package.json', 'package.json'), false);
  owner.setItems('main', [tool('view', ['.gitignore'])]);
  assert.equal(owner.eligible('.gitignore', '.gitignore'), true);
  owner.setItems('main', [tool('create', ['/repo/package.json'])]);
  assert.equal(owner.eligible('package.json', 'package.json'), true);
  for (const name of ['github-mcp-server-get_file_contents', 'glob', 'grep', 'rg', 'read', 'write']) {
    owner.setItems('main', [tool(name, ['package.json'])]);
    assert.equal(owner.eligible('package.json', 'package.json'), false);
  }
  assert.equal(owner.eligible('./package.json', 'package.json'), true);
  assert.equal(hintPath('/repo/./文%23#?.txt', '/repo'), '文%23#?.txt');
  assert.equal(hintPath('/elsewhere/package.json', '/repo'), undefined);
});

test('positive/negative TTLs and both LRU caps are enforced', () => {
  const cache = new FileCache();
  cache.set('yes', true, 0); cache.set('no', false, 0);
  assert.equal(cache.get('no', 4999), false);
  assert.equal(cache.get('no', 5000), undefined);
  assert.equal(cache.get('yes', 29999), true);
  assert.equal(cache.get('yes', 30000), undefined);
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
    getAttribute: () => `src/${i}.ts`, getBoundingClientRect: () => ({ top: 10, bottom: 20 }), getClientRects: () => [1],
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

test('errors remain unknown and wait for new demand; disposal rejects a late response', async t => {
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
  assert.equal(calls, 1, 'scrolling with the same visible candidate must not retry errors');
  owner.focus(); await tick(); assert.equal(calls, 2);
  owner.stop(); assert.equal(signal.aborted, true);
  finish(); await tick(); assert.equal(owner.cache.size, 0);
});

test('completed local edits invalidate hints, including delete, without asserting missing', async t => {
  dom(t, 0);
  const owner = new FileReferences('/repo', async paths => answer(paths));
  owner.setItems('main', [tool('apply_patch', ['old.ts', 'new.ts'], 'running')]);
  owner.cache.set('old.ts', true); owner.cache.set('new.ts', false);
  owner.setItems('main', [tool('apply_patch', ['old.ts', 'new.ts'])]);
  assert.equal(owner.cache.get('old.ts'), undefined); assert.equal(owner.cache.get('new.ts'), undefined);
  owner.setItems('agent', [tool('view', ['package.json'])]);
  assert.equal(owner.eligible('package.json', 'package.json'), true);
  owner.removeItems('agent'); assert.equal(owner.eligible('package.json', 'package.json'), false);
});
