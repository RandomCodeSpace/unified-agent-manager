import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { runInNewContext } from 'node:vm';
import ts from 'typescript';
import { PREVIEW_BYTES, PreviewOwner, downloadUrl, fileLabel, previewClick, previewMetadata, readTextPreview, tempFile } from '../src/lib/preview.ts';

const bytes = text => new TextEncoder().encode(text);
const response = (body, status = 206, headers = {}) => new Response(body, { status, headers: { 'Content-Type': 'text/plain', ...headers } });

test('a temp action inside a file link opens its exact target without ancestor preview or navigation', async () => {
  // Exercise the actual inline handler without adding a production seam or DOM dependency.
  const source = ts.createSourceFile('common.tsx', await readFile(new URL('../src/components/common.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const component = source.statements.find(node => ts.isFunctionDeclaration(node) && node.name?.text === 'TempFileAction');
  let handler;
  const visit = node => {
    if (ts.isJsxAttribute(node) && node.name.getText(source) === 'onClick') handler = node.initializer.expression;
    ts.forEachChild(node, visit);
  };
  assert.ok(component);
  visit(component);
  assert.ok(handler);
  const opened = [];
  const exports = {};
  runInNewContext(ts.transpileModule(`export default ${handler.getText(source)}`, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText, {
    exports, file: { path: '/tmp/owned.png', hash: '#detail' }, preview: (target, opener) => opened.push({ path: target.tempPath, hash: target.hash, opener }),
  });
  const opener = {};
  const event = { currentTarget: opener, defaultPrevented: false, stopped: false, preventDefault() { this.defaultPrevented = true; }, stopPropagation() { this.stopped = true; } };
  exports.default(event);
  let navigated = false;
  if (!event.stopped) opened.push({ path: 'report.html' });
  if (!event.defaultPrevented) navigated = true;
  assert.deepEqual(opened, [{ path: '/tmp/owned.png', hash: '#detail', opener }]);
  assert.equal(navigated, false);
});

test('file labels use basename and case-insensitive format hints, with a generic fallback', () => {
  const examples = { 'image.PNG': 'image', 'report.PdF': 'pdf', 'app.TsX': 'code', 'data.JSON': 'data', '.settings.JSON': 'data', 'bundle.TAR.GZ': 'archive', 'sound.MP3': 'audio', 'clip.WebM': 'video', 'table.CsV': 'sheet', 'notes.Md': 'text', 'thing.weird': 'file', README: 'file', '.gitignore': 'file' };
  for (const [name, format] of Object.entries(examples)) assert.deepEqual(fileLabel(`some/folder/${name}`), { path: `some/folder/${name}`, name, format });
  const first = fileLabel('one/report.html');
  const second = fileLabel('two/report.html');
  assert.equal(first.name, second.name);
  assert.notEqual(first.path, second.path);
  assert.deepEqual(fileLabel('out/a#100%.TXT'), { path: 'out/a#100%.TXT', name: 'a#100%.TXT', format: 'text' });
});

test('temp actions require a bounded exact absolute path under the configured root or fixed alias', () => {
  const meta = { temp_root: '/private/tmp', temp_root_aliases: ['/tmp'] };
  assert.deepEqual(tempFile('/tmp/report%20one.html#result', '/project', meta), { path: '/tmp/report one.html', hash: '#result' });
  assert.deepEqual(tempFile('file:///private/tmp/%E2%82%AC%23%25.txt', '/project', meta), { path: '/private/tmp/€#%.txt', hash: '' });
  const literal = '/tmp/100%#done.txt';
  assert.deepEqual(tempFile(literal.split('/').map(encodeURIComponent).join('/'), '/project', meta), { path: literal, hash: '' });
  for (const path of ['/tmp', '/private/tmp/', '/tmp-other/a', 'tmp/a', './tmp/a', 'https://host/tmp/a', '/tmp/a/../b', '/tmp/./a', '/tmp//a', '/tmp/a/', '/tmp/%00x', '/tmp/%0ax', `/tmp/${'€'.repeat(1400)}`]) {
    assert.equal(tempFile(path, '/project', meta), null, path);
  }
  assert.equal(tempFile('/elsewhere/a', '/project', { ...meta, temp_root_aliases: ['/tmp', '/elsewhere'] }), null);
  assert.equal(tempFile('/tmp/a', '/project', null), null);
});

test('workdir scope wins under either spelling of the temp root', () => {
  const meta = { temp_root: '/private/tmp', temp_root_aliases: ['/tmp'] };
  assert.equal(tempFile('/tmp/project/report.html', '/tmp/project', meta), null);
  assert.equal(tempFile('/tmp/project/report.html', '/private/tmp/project', meta), null);
  assert.equal(tempFile('/private/tmp/project/report.html', '/tmp/project', meta), null);
  assert.equal(tempFile('/private/tmp/report.html', '/tmp', meta), null);
  assert.deepEqual(tempFile('/tmp/project-other/report.html', '/tmp/project', meta), { path: '/tmp/project-other/report.html', hash: '' });
});

test('eligibility and constructing an inactive preview owner do no network work', async t => {
  const fetch = globalThis.fetch;
  let requests = 0;
  globalThis.fetch = async () => { requests++; throw new Error('No request is allowed'); };
  t.after(() => { globalThis.fetch = fetch; });
  const owner = new PreviewOwner();
  for (let i = 0; i < 20; i++) tempFile('/tmp/report.html', '/project', { temp_root: '/tmp' });
  await Promise.resolve();
  owner.stop();
  assert.equal(requests, 0);
});

test('closing before grant creation completes revokes the late grant without accepting it', async () => {
  const owner = new PreviewOwner();
  const request = owner.start();
  let complete;
  const creation = new Promise(resolve => { complete = resolve; });
  let revocations = 0;
  const accepted = creation.then(() => owner.retain(request, async () => { revocations++; }));
  owner.stop();
  assert.equal(request.signal.aborted, true);
  complete();
  assert.equal(await accepted, false);
  assert.equal(revocations, 1);
  assert.equal(owner.owns(request), false);
});

test('replacement and repeated clicks leave one owner; disposal revokes each known grant once', async () => {
  const owner = new PreviewOwner();
  const revoked = [];
  const first = owner.start();
  owner.retain(first, async () => { revoked.push('first'); });
  const second = owner.start();
  assert.equal(first.signal.aborted, true);
  assert.equal(owner.owns(first), false);
  assert.equal(owner.owns(second), true);
  assert.deepEqual(revoked, ['first']);
  const third = owner.start();
  assert.equal(second.signal.aborted, true);
  assert.equal(owner.retain(second, async () => { revoked.push('second-late'); }), false);
  owner.retain(third, async () => { revoked.push('third'); });
  owner.stop();
  owner.stop();
  await Promise.resolve();
  assert.equal(third.signal.aborted, true);
  assert.deepEqual(revoked, ['first', 'second-late', 'third']);
});

test('disposal remains final when revocation fails after auth or network loss', async () => {
  const owner = new PreviewOwner();
  const request = owner.start();
  owner.retain(request, async () => { throw new Error('401'); });
  owner.stop();
  await Promise.resolve();
  assert.equal(request.signal.aborted, true);
  assert.equal(owner.owns(request), false);
});

test('preview type and size come from response headers, and links retain native modified clicks', () => {
  assert.deepEqual(previewMetadata(new Headers({ 'Content-Type': 'text/html; charset=utf-8', 'Content-Length': '42' })), { mime: 'text/html', size: 42, kind: 'html' });
  assert.equal(previewMetadata(new Headers({ 'Content-Type': 'application/problem+json' })).kind, 'text');
  assert.equal(previewMetadata(new Headers({ 'Content-Type': 'application/octet-stream' })).kind, 'binary');
  assert.equal(previewMetadata(new Headers({ 'Content-Length': '-1' })).size, undefined);
  assert.equal(downloadUrl('/file/a%23b?version=2#L4'), '/file/a%23b?version=2&download=1#L4');
  const click = { button: 0, altKey: false, ctrlKey: false, metaKey: false, shiftKey: false, defaultPrevented: false };
  assert.equal(previewClick(click), true);
  for (const key of ['altKey', 'ctrlKey', 'metaKey', 'shiftKey', 'defaultPrevented']) assert.equal(previewClick({ ...click, [key]: true }), false);
  assert.equal(previewClick({ ...click, button: 1 }), false);
});

test('short ranged files and empty 200 or 416 responses keep their complete text', async () => {
  assert.deepEqual(await readTextPreview(response('hello', 206, { 'Content-Range': 'bytes 0-4/5', 'Content-Length': '5' })), { text: 'hello', truncated: false });
  assert.deepEqual(await readTextPreview(response('', 200, { 'Content-Length': '0' })), { text: '', truncated: false });
  assert.deepEqual(await readTextPreview(response(null, 416, { 'Content-Range': 'bytes */0' })), { text: '', truncated: false });
  await assert.rejects(readTextPreview(response(null, 416, { 'Content-Range': 'bytes */7' })), /could not provide/);
  await assert.rejects(readTextPreview(response('abc', 206, { 'Content-Range': 'bytes 1-3/4' })), /invalid preview range/);
  await assert.rejects(readTextPreview(response('abc', 206, { 'Content-Range': 'bytes 0-4/5' })), /ended before/);
  await assert.rejects(readTextPreview(response('abc', 206, { 'Content-Range': 'bytes 0-4/1000000' })), /ended before/);
  await assert.rejects(readTextPreview(response('abcdef', 206, { 'Content-Range': 'bytes 0-4/5' })), /exceeded/);
});

test('ignored Range and oversized chunks are cancelled after the hard prefix cap', async () => {
  let cancelled = false;
  const body = new ReadableStream({ start(controller) { controller.enqueue(bytes('x'.repeat(PREVIEW_BYTES + 8192))); }, cancel() { cancelled = true; } });
  const result = await readTextPreview(response(body, 200));
  assert.equal(bytes(result.text).byteLength, PREVIEW_BYTES);
  assert.equal(result.truncated, true);
  assert.equal(cancelled, true);
  const exact = await readTextPreview(response('x'.repeat(PREVIEW_BYTES), 206, { 'Content-Range': `bytes 0-${PREVIEW_BYTES - 1}/${PREVIEW_BYTES}` }));
  assert.equal(exact.truncated, false);
});

test('streamed UTF-8 preserves split characters and bounds malformed decoded bytes', async () => {
  const body = new ReadableStream({ start(controller) { controller.enqueue(new Uint8Array([0xe2])); controller.enqueue(new Uint8Array([0x82, 0xac, 0xe2])); controller.close(); } });
  assert.deepEqual(await readTextPreview(response(body, 206, { 'Content-Range': 'bytes 0-3/9' })), { text: '€�', truncated: true });
  const malformed = await readTextPreview(response(new Uint8Array(PREVIEW_BYTES).fill(0xff), 206, { 'Content-Range': `bytes 0-${PREVIEW_BYTES - 1}/${PREVIEW_BYTES}` }));
  assert.ok(bytes(malformed.text).byteLength <= PREVIEW_BYTES);
  assert.equal(malformed.truncated, true);
  const cut = new Uint8Array(PREVIEW_BYTES).fill(0x61);
  cut[PREVIEW_BYTES - 1] = 0xe2;
  const clipped = await readTextPreview(response(cut, 206, { 'Content-Range': `bytes 0-${PREVIEW_BYTES - 1}/${PREVIEW_BYTES + 2}` }));
  assert.ok(clipped.text.endsWith('\uFFFD'));
  assert.equal(bytes(clipped.text).byteLength, PREVIEW_BYTES);
});

test('unexpected encoding is refused and cancellation closes a pending stream', async () => {
  await assert.rejects(readTextPreview(response('text', 200, { 'Content-Encoding': 'gzip' })), /encoded.*unexpectedly/);
  const controller = new AbortController();
  let cancelled = false;
  const body = new ReadableStream({ cancel() { cancelled = true; } });
  const reading = readTextPreview(response(body, 200), controller.signal);
  controller.abort();
  await assert.rejects(reading, { name: 'AbortError' });
  assert.equal(cancelled, true);
});
