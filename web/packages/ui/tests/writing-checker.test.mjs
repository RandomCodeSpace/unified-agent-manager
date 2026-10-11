import assert from 'node:assert/strict';
import test from 'node:test';
import { checkWriting, MAX_WRITING_CHECK_LENGTH } from '../src/lib/writing-checker.ts';
import { checkWritingText } from '../src/lib/writing-checker.worker.ts';

test('the local engine returns real spelling and grammar replacements at UTF-16 offsets', async () => {
  const text = '😀 Please reveiw this. We was ready. This is a example. The the build works.';
  const issues = await checkWritingText(text);
  for (const [original, replacement, kind] of [['reveiw', 'review', 'spelling'], ['was', 'were', 'grammar'], ['a', 'an', 'grammar'], ['The the', 'The', 'grammar']]) {
    const issue = issues.find(value => value.original === original);
    assert.ok(issue, `missing issue for ${original}`);
    assert.equal(issue.kind, kind);
    assert.ok(issue.replacements.includes(replacement));
    assert.equal(text.slice(issue.start, issue.end), original);
  }
  assert.equal(issues.find(issue => issue.original === 'reveiw').start, 10);
});

test('code, paths, URLs, references, flags and identifiers are protected while surrounding prose is checked', async () => {
  const text = 'Please reveiw `reveiw` and `fooBar reveiw` and ``nested ` reveiw``.\n'
    + 'Read @src/reveiw.ts src/reveiw.ts C:\\reveiw\\file.ts https://reveiw.example.com --reveiw /reveiw $reveiw fooBar snake_case UAM_API.\n'
    + '```typescript\nconst reveiw = fooBar;\n```\nWe was ready.';
  const issues = await checkWritingText(text);
  assert.deepEqual(issues.map(issue => [issue.original, issue.replacements[0]]), [['reveiw', 'review'], ['was', 'were']]);
});

test('correct prose and oversized drafts produce no correction proposals', async () => {
  assert.deepEqual(await checkWritingText('Please review the changes. We were ready.'), []);
  assert.deepEqual(await checkWritingText('reveiw '.repeat(MAX_WRITING_CHECK_LENGTH)), []);
  assert.deepEqual(await checkWritingText(''), []);
});

test('worker requests are lazy, reused, correlated and reject errors before recovering', async t => {
  const instances = [];
  class FakeWorker {
    sent = [];
    terminated = false;
    constructor(url, options) { this.url = url; this.options = options; instances.push(this); }
    postMessage(data) { this.sent.push(data); }
    terminate() { this.terminated = true; }
  }
  const previous = globalThis.Worker;
  globalThis.Worker = FakeWorker;
  t.after(() => { if (previous) globalThis.Worker = previous; else delete globalThis.Worker; });
  assert.deepEqual(await checkWriting(''), []);
  assert.deepEqual(await checkWriting('x'.repeat(MAX_WRITING_CHECK_LENGTH + 1)), []);
  assert.equal(instances.length, 0);

  const first = checkWriting('first');
  const second = checkWriting('second');
  const instance = instances[0];
  assert.equal(instances.length, 1);
  assert.equal(instance.options.type, 'module');
  instance.onmessage({ data: { id: instance.sent[1].id, issues: [] } });
  assert.deepEqual(await second, []);
  const issue = { start: 0, end: 5, original: 'first', kind: 'spelling', replacements: ['test'], message: 'test' };
  instance.onmessage({ data: { id: instance.sent[0].id, issues: [issue] } });
  assert.deepEqual(await first, [issue]);

  const failed = assert.rejects(checkWriting('third'), /could not load/);
  instance.onerror();
  await failed;
  assert.equal(instance.terminated, true);

  const recovered = checkWriting('fourth');
  const replacement = instances[1];
  instance.onerror();
  assert.equal(replacement.terminated, false);
  replacement.onmessage({ data: { id: replacement.sent[0].id, issues: [] } });
  assert.deepEqual(await recovered, []);

  const engineFailure = assert.rejects(checkWriting('fifth'), /unavailable/);
  replacement.onmessage({ data: { id: replacement.sent[1].id, error: 'Engine failed' } });
  await engineFailure;

  t.mock.timers.enable({ apis: ['setTimeout'] });
  const timedOut = assert.rejects(checkWriting('sixth'), /took too long/);
  t.mock.timers.tick(20_000);
  await timedOut;
  assert.equal(instances[2].terminated, true);
});
