import assert from 'node:assert/strict';
import test from 'node:test';
import { away, checkFrom, checkOf, claimTopics, countsOf, editedPaths, exitCode, finalMessage, judgeClaims, lastTurn, sentences, sinceYouLeft } from '../src/lib/evidence.ts';

const W = '/repo';
const bash = (id, command, output, extra = {}) => ({ id, kind: 'tool', time: '2026-10-01T10:00:00Z', ended_at: '2026-10-01T10:00:01.200Z', tool: { name: 'bash', status: 'completed', input: JSON.stringify({ command }), output }, ...extra });
const edit = (id, path) => ({ id, kind: 'tool', time: '2026-10-01T10:00:00Z', tool: { name: 'edit', status: 'completed', path: `${W}/${path}` } });

test('commands are recognised as checks by their first word, past cd, env and wrappers', () => {
  assert.deepEqual(checkOf('go test ./...'), { kind: 'test', piped: false });
  assert.deepEqual(checkOf('cd web && CI=1 npm run test'), { kind: 'test', piped: false });
  assert.deepEqual(checkOf('go vet ./...'), { kind: 'vet', piped: false });
  assert.deepEqual(checkOf('npx tsc --noEmit'), { kind: 'typecheck', piped: false });
  assert.deepEqual(checkOf('npm run lint'), { kind: 'lint', piped: false });
  assert.deepEqual(checkOf('go build ./cmd/uam'), { kind: 'build', piped: false });
  assert.deepEqual(checkOf('go test ./... 2>&1 | tail -20'), { kind: 'test', piped: true });
  assert.deepEqual(checkOf('set -o pipefail; go test ./... | tail'), { kind: 'test', piped: false });
  assert.equal(checkOf('git status'), null);
  assert.equal(checkOf('cat go.test.md'), null);
  assert.equal(checkOf('echo go test'), null);
});

test('the exit code is read from the shell tool’s closing line', () => {
  assert.equal(exitCode('FAIL\n<shellId: 0 completed with exit code 1>'), 1);
  assert.equal(exitCode('\n<shellId: 1 completed with exit code 0>\n'), 0);
  assert.equal(exitCode('<exited with exit code 2>'), 2);
  assert.equal(exitCode('ok'), null);
  assert.equal(exitCode(undefined), null);
});

test('counts come from the common test summaries', () => {
  assert.equal(countsOf('ok  \texample.com/a\t0.01s\nok  \texample.com/b\t0.02s\n')?.label, '2 packages ok');
  assert.deepEqual(countsOf('--- FAIL: TestAdd (0.00s)\nFAIL\nFAIL\texample.com/calc\t0.003s\nFAIL\n'), { passed: 0, failed: 2, label: '1 package failed, 1 test failed' });
  // Copilot's real go test output, its exit line included.
  assert.equal(countsOf('--- FAIL: TestAdd (0.00s)\n    calc_test.go:7: Add(2, 3) = -1, want 5\nFAIL\nFAIL\texample.com/calc\t0.003s\nFAIL\n<shellId: 0 completed with exit code 1>')?.label, '1 package failed, 1 test failed');
  assert.equal(countsOf('Tests:       1 failed, 12 passed, 13 total')?.label, '12 passed, 1 failed');
  assert.equal(countsOf(' Tests  14 passed (14)')?.label, '14 passed');
  assert.equal(countsOf('===== 3 passed in 0.12s =====')?.label, '3 passed');
  assert.equal(countsOf('test result: ok. 4 passed; 0 failed; 0 ignored')?.label, '4 passed');
  assert.equal(countsOf('# tests 5\n# pass 5\n# fail 0')?.label, '5 passed');
  assert.equal(countsOf('nothing here'), null);
});

test('a check passes or fails on its exit status; a pipe makes the status unclear unless the output counts', () => {
  const pass = checkFrom(bash('a', 'go test ./...', 'ok  \tx\t0.1s\n<shellId: 0 completed with exit code 0>'));
  assert.equal(pass.outcome, 'pass');
  assert.equal(pass.exit, 0);
  assert.equal(pass.took, '1s');
  assert.equal(pass.counts.label, '1 package ok');
  assert.equal(checkFrom(bash('b', 'go test ./...', 'FAIL\tx\t0.1s\n<shellId: 0 completed with exit code 1>')).outcome, 'fail');
  assert.equal(checkFrom(bash('c', 'go test ./... | tail', 'whatever\n<shellId: 0 completed with exit code 0>')).outcome, 'unclear');
  assert.equal(checkFrom(bash('d', 'go test ./... | tail', 'ok  \tx\t0.1s\n<shellId: 0 completed with exit code 0>')).outcome, 'pass');
  assert.equal(checkFrom(bash('e', 'ls', '<shellId: 0 completed with exit code 0>')), null);
  // A compact item has no output: its whole body decides.
  const compact = { id: 'f', kind: 'tool', time: 't', compact: { has_text: false }, tool: { name: 'bash', status: 'completed', display_arg: 'npm test', has_output: true } };
  assert.equal(checkFrom(compact).outcome, 'unclear');
  assert.equal(checkFrom(compact, { ...compact, tool: { ...compact.tool, output: '# pass 3\n<shellId: 0 completed with exit code 0>' } }).outcome, 'pass');
});

test('claims are positive sentences about checks or docs; negations and failures are not claims', () => {
  assert.deepEqual(claimTopics('All tests pass now.'), ['test']);
  assert.deepEqual(claimTopics('The build succeeds and go vet is clean.'), ['build', 'vet']);
  assert.deepEqual(claimTopics('I updated the README to describe Add.'), ['docs']);
  assert.deepEqual(claimTopics('Lint passes without errors.'), ['lint']);
  assert.deepEqual(claimTopics('go vet ./... passed with no reported issues.'), ['vet']);
  assert.deepEqual(claimTopics('The tests failed on TestAdd.'), []);
  assert.deepEqual(claimTopics('I did not run the tests.'), []);
  assert.deepEqual(claimTopics('I changed Add to return a + b.'), []);
});

test('sentences skip code blocks and list marks', () => {
  assert.deepEqual(sentences('Done. Tests pass.\n\n```\ngo test\n```\n- Updated `README.md`, e.g. the usage. docs/x.md too.'), ['Done.', 'Tests pass.', 'Updated README.md, e.g. the usage.', 'docs/x.md too.']);
});

test('a Go package pattern (./...) does not end a sentence', () => {
  const text = 'Fixed `Add` and documented it in `README.md`. `go test ./...` passes.';
  assert.deepEqual(sentences(text), ['Fixed Add and documented it in README.md.', 'go test ./... passes.']);
  assert.deepEqual(claimTopics('go test ./... passes.'), ['test']);
});

test('claims are judged against the turn’s checks and edits', () => {
  const turn = [
    { id: 'u', kind: 'user', time: 't', text: 'fix it' },
    edit('e1', 'calc.go'),
    bash('t1', 'go test ./...', 'ok  \tx\t0.1s\n<shellId: 0 completed with exit code 0>'),
    edit('e2', 'README.md'),
    { id: 'a', kind: 'assistant', time: 't', text: 'Fixed Add. All tests pass. I updated the README. The CHANGELOG is updated too. docs/terminal.md describes it. Lint is clean.' },
  ];
  const checks = turn.map((item) => checkFrom(item)).filter(Boolean);
  const claims = judgeClaims(finalMessage(turn), checks, turn, W);
  assert.deepEqual(claims.map((c) => [c.text, c.verified, c.detail]), [
    // README was edited after the test run: a doc edit leaves the run's result standing.
    ['All tests pass.', true, 'go test ./... · exit 0'],
    ['I updated the README.', true, 'edited README.md'],
    ['The CHANGELOG is updated too.', false, 'Not verified · no edit to CHANGELOG in this turn'],
    ['docs/terminal.md describes it.', false, 'Not verified · no edit to docs/terminal.md in this turn'],
    ['Lint is clean.', false, 'Not verified · no lint run in this turn'],
  ]);
  // Code edited after the run: the run no longer speaks for it.
  const stale = [turn[0], turn[2], turn[1], turn[4]];
  const judged = judgeClaims(finalMessage(stale), stale.map((item) => checkFrom(item)).filter(Boolean), stale, W);
  assert.deepEqual(judged[0], { text: 'All tests pass.', topics: ['test'], verified: false, detail: 'Not verified · files changed after the last run' });
  const failed = [turn[0], bash('t2', 'go test ./...', 'FAIL\n<shellId: 0 completed with exit code 1>'), turn[4]];
  assert.equal(judgeClaims(finalMessage(failed), failed.map((item) => checkFrom(item)).filter(Boolean), failed, W)[0].detail, 'Not verified · the last run failed (exit 1)');
});

test('the last turn starts at the last prompt that was not a steer', () => {
  const items = [{ id: 'u1', kind: 'user' }, { id: 'a1', kind: 'assistant', text: 'one' }, { id: 'u2', kind: 'user' }, { id: 's', kind: 'user', delivery: 'steer' }, { id: 'a2', kind: 'assistant', text: 'two' }];
  assert.deepEqual(lastTurn(items).map((i) => i.id), ['u2', 's', 'a2']);
  assert.equal(finalMessage(lastTurn(items)), 'two');
  assert.deepEqual(editedPaths([edit('x', 'a.go'), edit('y', 'a.go'), edit('z', 'docs/b.md')], W), ['a.go', 'docs/b.md']);
  // Copilot's apply_patch: whole (the patch as a JSON string) and compact (file_paths).
  const patch = { id: 'p', kind: 'tool', time: 't', tool: { name: 'apply_patch', status: 'completed', input: JSON.stringify(`*** Begin Patch\n*** Update File: ${W}/calc.go\n@@\n-a\n+b\n*** Add File: ${W}/README.md\n+x\n*** End Patch`) } };
  assert.deepEqual(editedPaths([patch], W), ['calc.go', 'README.md']);
  assert.deepEqual(editedPaths([{ ...patch, tool: { name: 'apply_patch', status: 'completed', file_paths: [`${W}/calc.go`] } }], W), ['calc.go']);
});

test('since you left counts what happened after the mark', () => {
  const at = (min) => new Date(Date.parse('2026-10-01T10:00:00Z') + min * 60000).toISOString();
  const items = [
    { id: 'old', kind: 'assistant', time: at(-5), text: 'seen' },
    { ...edit('e1', 'calc.go'), time: at(1) },
    { ...bash('t1', 'go test ./...', ''), time: at(2) },
    { ...bash('t2', 'git diff', ''), time: at(3) },
    { ...edit('e2', 'README.md'), time: at(4) },
    { ...edit('e3', 'docs/x.md'), time: at(4) },
    { id: 'a', kind: 'assistant', time: at(5), text: 'done' },
  ];
  const timings = [{ id: 'tt', started_at: at(0.5), ended_at: at(5), state: 'completed' }];
  const since = sinceYouLeft(items, timings, [], at(0), at(60), W, false);
  assert.equal(since.text, 'the agent finished, ran the tests plus 1 other command, and changed 3 files');
  assert.deepEqual(since.ids, ['e1', 't1', 't2', 'e2', 'e3', 'a']);
  assert.equal(sinceYouLeft(items, timings, [], at(6), at(60), W, false), null);
  const question = { id: 'q', kind: 'question', title: 'Which?', state: 'pending', time: at(5.5) };
  assert.equal(sinceYouLeft(items.slice(0, 2), [], [question], at(0), at(60), W, true).text, 'the agent is still working, changed 1 file, and asked you a question');
  // What happens after the owner came back is seen, not news.
  assert.equal(sinceYouLeft(items, timings, [], at(0), at(1.5), W, false).text, 'changed 1 file');
  assert.equal(away(at(0), Date.parse(at(42))), '42 min');
  assert.equal(away(at(0), Date.parse(at(0.5))), '<1 min');
  assert.equal(away(at(0), Date.parse(at(180))), '3 h');
});
