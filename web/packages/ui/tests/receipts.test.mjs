import assert from 'node:assert/strict';
import test from 'node:test';
import { claimsOf, receipts, allVerified, allUnseen } from '../src/lib/receipts.ts';

const at = (s) => `2026-10-09T10:00:${String(s).padStart(2, '0')}Z`;
const call = (id, name, input, extra = {}, s = 1) => ({ id, kind: 'tool', time: at(s), tool: { name, status: 'completed', input: JSON.stringify(input), ...extra } });
const bash = (id, command, exit = 0, extra = {}) => call(id, 'bash', { command }, { exit_code: exit, ...extra });

test('claims: code spans that read as commands or paths, and the words that say the tests pass', () => {
  const text = 'I updated `internal/web/charts.go` and `web/src/App.tsx:42`, ran `go test ./internal/web` and `npm test`; all tests pass. See `https://example.com/x.go` and `--flag` and `foo.bar`.\n```\n`not/a/claim.go`\n```';
  assert.deepEqual(claimsOf(text), [
    { kind: 'path', claim: 'internal/web/charts.go' },
    { kind: 'path', claim: 'web/src/App.tsx' },
    { kind: 'command', claim: 'go test ./internal/web' },
    { kind: 'command', claim: 'npm test' },
    { kind: 'tests', claim: 'all tests pass' },
  ]);
  assert.deepEqual(claimsOf('Nothing here.'), []);
  // A model id, a directory and a package path are not files; a bare Makefile is.
  assert.deepEqual(claimsOf('On `ollama/deepseek-v4.1-flash`, in `.git/` and `internal/web`, via `Makefile`.'), [{ kind: 'path', claim: 'Makefile' }]);
});

test('claims the record cannot see either way collapse: all unseen, none with evidence', () => {
  const delegated = receipts('Ran `ls` on `README.md`.', [call('t1', 'task', { agent_type: 'task' })]);
  assert.equal(allUnseen(delegated), true);
  assert.equal(allUnseen(receipts('Read `README.md`.', [call('v1', 'view', { path: 'README.md' })])), false);
});

test('a changed path, a run command and a green test run are verified, each with its evidence', () => {
  const items = [
    call('e1', 'edit', { path: 'internal/web/charts.go' }),
    call('v1', 'view', { path: 'docs/web.md' }),
    bash('b1', 'cd web && npm test'),
    bash('b2', 'go test ./internal/web -run TestX', 0, { output: 'ok  \tinternal/web\t0.4s' }),
  ];
  const stamps = receipts('Changed `charts.go`, read `docs/web.md`, ran `npm test`; tests pass.', items);
  assert.deepEqual(stamps.map((s) => [s.claim, s.verdict, s.itemId]), [
    ['charts.go', 'verified', 'e1'],
    ['docs/web.md', 'unseen', 'v1'],
    ['npm test', 'verified', 'b1'],
    ['tests pass', 'verified', 'b2'],
  ]);
  assert.equal(stamps[1].note, 'viewed, not changed');
  assert.equal(stamps[2].note, 'exit 0');
  assert.equal(allVerified(stamps), false);
  assert.equal(allVerified(stamps.filter((s) => s.verdict === 'verified')), true);
});

test('a test claim with no test command, or a failing one, is contradicted', () => {
  assert.deepEqual(receipts('All tests pass.', [bash('b1', 'ls')]).map((s) => [s.verdict, s.note]), [['contradicted', 'no test command ran this turn']]);
  const failed = receipts('The tests pass now.', [bash('b1', 'go test ./...', 1)]);
  assert.deepEqual(failed.map((s) => [s.verdict, s.note, s.itemId]), [['contradicted', 'last test run exit 1', 'b1']]);
  const reported = receipts('tests passed', [bash('b1', 'npx vitest run', 0, { output: 'Tests  2 failed | 10 passed' })]);
  assert.equal(reported[0].verdict, 'contradicted');
  const piped = receipts('tests pass', [bash('b1', 'go test ./... 2>&1 | tail -3', 0)]);
  assert.deepEqual(piped.map((s) => [s.verdict, s.note]), [['unseen', 'piped test run, output not loaded']]);
  assert.equal(receipts('tests pass', [bash('b1', 'go test ./... 2>&1 | tail -3', 0, { output: 'ok  \tpkg\t0.2s' })])[0].verdict, 'verified');
  assert.equal(receipts('tests pass', [bash('b1', 'go test ./... || true', 0)])[0].verdict, 'verified');
  assert.equal(reported[0].note, 'last test run reported failures');
});

test('a command the record lacks is unseen, never contradicted: the reply may only suggest it', () => {
  assert.deepEqual(receipts('Ran `ls`.', [bash('b1', 'sleep 20')]).map((s) => [s.verdict, s.note]), [['unseen', 'not run this turn']]);
  assert.deepEqual(receipts('To install, run `make install` yourself.', [bash('b1', 'go build ./...')]).map((s) => [s.verdict, s.note]), [['unseen', 'not run this turn']]);
  assert.deepEqual(receipts('Ran `make install`.', [bash('b1', 'make install', 2)]).map((s) => [s.verdict, s.note]), [['contradicted', 'exit 2']]);
  const delegated = receipts('Ran `ls` and `uname -a`; tests pass.', [call('t1', 'task', { agent_type: 'task', description: 'runner' }), bash('b1', 'sleep 20')]);
  assert.deepEqual(delegated.map((s) => [s.verdict, s.note]), [
    ['unseen', 'not run by the main agent · 1 subagent ran'],
    ['unseen', 'not run by the main agent · 1 subagent ran'],
    ['unseen', 'no test command ran in the main agent’s work · 1 subagent ran'],
  ]);
});

test('a command matches whole at a boundary, a path by its tail; a patch names its files; a shell read counts as viewed', () => {
  const items = [bash('b1', 'ls -la tools'), call('p1', 'apply_patch', '*** Begin Patch\n*** Update File: web/src/lib/a.ts\n*** End Patch'), bash('b2', 'cat README.md')];
  items[1].tool.input = JSON.stringify('*** Begin Patch\n*** Update File: web/src/lib/a.ts\n*** End Patch');
  const stamps = receipts('`ls` then `tools` and `lib/a.ts`, `README.md`.', items);
  assert.deepEqual(stamps.map((s) => [s.claim, s.verdict, s.itemId]), [
    ['ls', 'verified', 'b1'],
    ['lib/a.ts', 'verified', 'p1'],
    ['README.md', 'unseen', 'b2'],
  ]);
});

test('subagent calls and running calls are not evidence', () => {
  const items = [{ ...bash('b1', 'go test ./...'), agent_id: 'a1' }];
  assert.deepEqual(receipts('tests pass', items).map((s) => s.verdict), ['contradicted']);
});
