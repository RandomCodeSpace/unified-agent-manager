import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { parsePatch } from 'diff';
import { hunkText, lensChanges, runText } from '../src/lib/lens.ts';

const before = ['a', 'b', 'c', 'd', 'e', 'f', 'g'].join('\n') + '\n';
// Two runs in one hunk: b changed, then e removed and e2 + e3 added.
const after = ['a', 'B', 'c', 'd', 'e2', 'e3', 'f', 'g'].join('\n') + '\n';

function repo(fn) {
  const dir = mkdtempSync(join(tmpdir(), 'lens-'));
  const git = (...args) => execFileSync('git', ['-c', 'core.hooksPath=/dev/null', ...args], { cwd: dir, encoding: 'utf8' });
  try {
    git('init', '-q');
    writeFileSync(join(dir, 'f.txt'), before);
    git('add', '.');
    git('-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', 'base');
    writeFileSync(join(dir, 'f.txt'), after);
    const patch = git('diff', 'HEAD', '--', 'f.txt');
    return fn({ dir, patch, apply: (hunk) => execFileSync('git', ['apply', '-R'], { cwd: dir, input: `diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n${hunk}` }), read: () => readFileSync(join(dir, 'f.txt'), 'utf8') });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test('lensChanges marks each run on the new file, with the lines it removed', () => repo(({ patch }) => {
  const changes = lensChanges(parsePatch(patch)[0].hunks);
  assert.deepEqual(changes.map(({ line, added, removed, run }) => ({ line, added, removed, run })), [
    { line: 2, added: 1, removed: ['b'], run: 0 },
    { line: 5, added: 2, removed: ['e'], run: 1 },
  ]);
}));

test('runText reverses one run and leaves the others in the file', () => repo(({ patch, apply, read }) => {
  const [h] = parsePatch(patch)[0].hunks;
  apply(runText(h, 1));
  assert.equal(read(), ['a', 'B', 'c', 'd', 'e', 'f', 'g'].join('\n') + '\n');
}));

test('runText of the first run, then hunkText of the rest, takes the file back to HEAD', () => repo(({ patch, apply, read }) => {
  const [h] = parsePatch(patch)[0].hunks;
  apply(runText(h, 0));
  assert.equal(read(), ['a', 'b', 'c', 'd', 'e2', 'e3', 'f', 'g'].join('\n') + '\n');
  // The whole hunk no longer applies: the file moved on.
  assert.throws(() => apply(hunkText(h)));
}));
