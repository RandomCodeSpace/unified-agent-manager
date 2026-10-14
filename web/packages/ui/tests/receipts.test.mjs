import assert from 'node:assert/strict';
import test from 'node:test';
import { claimsOf, receipts, settle, allVerified, allUnseen, lastTurnContradicted } from '../src/lib/receipts.ts';

const at = (s) => `2026-10-09T10:00:${String(s).padStart(2, '0')}Z`;
const call = (id, name, input, extra = {}, s = 1) => ({ id, kind: 'tool', time: at(s), tool: { name, status: 'completed', input: JSON.stringify(input), ...extra } });
const bash = (id, command, exit = 0, extra = {}) => call(id, 'bash', { command }, { exit_code: exit, ...extra });

test('claims: code spans that read as commands or paths, and the words that say the tests pass', () => {
  const text = 'I updated `internal/web/charts.go` and `web/src/App.tsx:42`, ran `go test ./internal/web` and `npm test`; all tests pass. See `https://example.com/x.go` and `--flag` and `foo.bar`.\n```\n`not/a/claim.go`\n```';
  assert.deepEqual(claimsOf(text), [
    { kind: 'path', claim: 'internal/web/charts.go' },
    { kind: 'path', claim: 'web/src/App.tsx' },
    { kind: 'command', claim: 'go test ./internal/web', ran: true },
    { kind: 'command', claim: 'npm test', ran: true },
    { kind: 'tests', claim: 'all tests pass' },
  ]);
  assert.deepEqual(claimsOf('Nothing here.'), []);
  // A model id, a directory and a package path are not files; a bare Makefile is.
  assert.deepEqual(claimsOf('On `ollama/deepseek-v4.1-flash`, in `.git/` and `internal/web`, I updated `Makefile`.'), [{ kind: 'path', claim: 'Makefile' }]);
});

test('a path is a claim only beside an edit verb, outside a negated sentence; a command is a run claim only after a run verb', () => {
  const text = [
    'No files were edited, and `a.py` is fine.',
    'I looked at `b.py` and `c.py`.',
    'The loader lives in `d.py`.',
    'Fixed the off-by-one in `e.py`, `f.py` and `g.py`.',
    '- `h.py`: added a guard',
    '`i.py` was updated so there are no errors.',
    'I did not change `j.py`.',
    'Ran `npm test`; to install, run `make install` yourself; I never ran `go vet ./...`.',
  ].join('\n');
  assert.deepEqual(claimsOf(text), [
    { kind: 'path', claim: 'e.py' },
    { kind: 'path', claim: 'f.py' },
    { kind: 'path', claim: 'g.py' },
    { kind: 'path', claim: 'h.py' },
    { kind: 'path', claim: 'i.py' },
    { kind: 'command', claim: 'npm test', ran: true },
    { kind: 'command', claim: 'make install', ran: false },
    { kind: 'command', claim: 'go vet ./...', ran: false },
  ]);
});

test('a turn with no tool calls and no subagents contradicts its run and edit claims, as it does a test claim', () => {
  const stamps = receipts('I ran `npm test` and updated `inventory.py`. All tests pass.', []);
  assert.deepEqual(stamps.map((s) => [s.claim, s.verdict, s.note]), [
    ['npm test', 'contradicted', 'not run this turn'],
    ['inventory.py', 'contradicted', 'not changed this turn'],
    ['All tests pass', 'contradicted', 'no test command ran this turn'],
  ]);
  // Subagent work is not read here: the same claims are only unseen, and say why.
  const delegated = receipts('I ran `npm test` and updated `inventory.py`.', [call('t1', 'task', { agent_type: 'task' })]);
  assert.deepEqual(delegated.map((s) => [s.verdict, s.note]), [
    ['unseen', 'not run by the main agent · 1 subagent ran'],
    ['unseen', 'not changed by the main agent · 1 subagent ran'],
  ]);
});

test('every claim is stamped: no cap drops one', () => {
  const files = Array.from({ length: 15 }, (_, i) => `f${i}.go`);
  const items = files.map((f, i) => call(`e${i}`, 'edit', { path: f }));
  const stamps = receipts(`Updated ${files.map((f) => `\`${f}\``).join(', ')}.`, items);
  assert.equal(stamps.length, 15);
  assert.equal(allVerified(stamps), true);
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
  const stamps = receipts('Changed `charts.go` and updated `docs/web.md`, ran `npm test`; tests pass.', items);
  assert.deepEqual(stamps.map((s) => [s.claim, s.verdict, s.itemId]), [
    ['charts.go', 'verified', 'e1'],
    ['docs/web.md', 'contradicted', 'v1'],
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
  assert.deepEqual(piped.map((s) => [s.verdict, s.note]), [['unseen', 'piped test run, no output']]);
  assert.equal(receipts('tests pass', [bash('b1', 'go test ./... 2>&1 | tail -3', 0, { output: 'ok  \tpkg\t0.2s' })])[0].verdict, 'verified');
  assert.equal(receipts('tests pass', [bash('b1', 'go test ./... || true', 0)])[0].verdict, 'verified');
  assert.equal(reported[0].note, 'last test run reported failures');
});

test('a command the record lacks is contradicted when the reply says it ran it, unseen when it only names it', () => {
  assert.deepEqual(receipts('Ran `ls`.', [bash('b1', 'sleep 20')]).map((s) => [s.verdict, s.note]), [['contradicted', 'not run this turn']]);
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
  const stamps = receipts('`ls` then `tools` and edited `lib/a.ts`, `README.md`.', items);
  assert.deepEqual(stamps.map((s) => [s.claim, s.verdict, s.itemId]), [
    ['ls', 'verified', 'b1'],
    ['lib/a.ts', 'verified', 'p1'],
    ['README.md', 'contradicted', 'b2'],
  ]);
});

test('a command named after a failing run and a passing rerun stands for the rerun', () => {
  const items = [bash('b1', 'go test ./...', 1), bash('b2', 'go test ./...', 0)];
  assert.deepEqual(receipts('Ran `go test ./...`; all tests pass.', items).map((s) => [s.claim, s.verdict, s.itemId]), [['go test ./...', 'verified', 'b2'], ['all tests pass', 'verified', 'b2']]);
});

test('the service\'s test counts decide a tests claim, loaded output or not', () => {
  assert.equal(receipts('tests pass', [bash('b1', 'go test ./...', 0, { tests: { passed: 3, failed: 1 } })])[0].verdict, 'contradicted');
  assert.equal(receipts('tests pass', [bash('b1', 'go test ./... 2>&1 | tail -3', 0, { tests: { passed: 4, failed: 0 } })])[0].verdict, 'verified');
  assert.equal(receipts('tests pass', [bash('b1', 'go test ./... 2>&1 | tail -3', 0)])[0].verdict, 'unseen');
});

test('a test run whose output the page has not loaded waits for it, then settles as the whole output says', () => {
  // The compact view holds the output back: has_output, no output, no counts.
  const held = { ...bash('b1', 'go test ./... 2>&1 | tail -3', 0, { has_output: true }), compact: { has_text: false, has_reasoning: false } };
  const pending = receipts('All 4 tests pass.', [held]);
  assert.deepEqual(pending.map((s) => [s.verdict, s.note, s.itemId]), [['pending', 'reading the test output', 'b1']]);
  assert.equal(pending[0].awaits.id, 'b1');
  assert.equal(allVerified(pending), false);
  assert.equal(allUnseen(pending), false);
  const body = (output) => bash('b1', 'go test ./... 2>&1 | tail -3', 0, { output });
  assert.deepEqual([settle(pending[0], body('ok  \tpkg\t0.2s'))].map((s) => [s.verdict, s.itemId]), [['verified', 'b1']]);
  assert.deepEqual([settle(pending[0], body('--- FAIL: TestX\nFAIL'))].map((s) => [s.verdict, s.note]), [['contradicted', 'last test run reported failures']]);
  // The whole record holds no output: genuinely unavailable, the same verdict however the page loaded.
  assert.deepEqual([settle(pending[0], body(''))].map((s) => [s.verdict, s.note]), [['unseen', 'piped test run, no output']]);
  // A clipped output waits too; counts or a failed exit decide without the output.
  assert.equal(receipts('tests pass', [{ ...bash('b1', 'npx vitest run', 0, { output: 'part' }), clipped: true }])[0].verdict, 'pending');
  assert.equal(receipts('tests pass', [bash('b1', 'go test ./...', 0, { has_output: true, tests: { passed: 4, failed: 0 } })])[0].verdict, 'verified');
  assert.equal(receipts('tests pass', [bash('b1', 'go test ./...', 1, { has_output: true })])[0].verdict, 'contradicted');
});

test('subagent calls and running calls are not evidence', () => {
  const items = [{ ...bash('b1', 'go test ./...'), agent_id: 'a1' }];
  assert.deepEqual(receipts('tests pass', items).map((s) => s.verdict), ['contradicted']);
});

test('a file the reply names as someone else\'s work, a refusal and quoted text claim nothing; its own work still does', () => {
  const own = 'I added `# e2e-1014` as the first line of `src/inventory.py` and ran `python3 -m pytest -q` — 4 passed. I did not touch `docs/guide.md`, so I can\'t send the exact text you asked for: it claims I updated that file, which would be false.';
  const others = 'One thing worth knowing: `docs/guide.md` already had uncommitted changes in the working tree before I started (a "Testing" section added, "Known issues" cleared), along with `data/sales.csv` and a quantity fix in `src/inventory.py`. Those aren\'t mine.';
  const accurate = 'Accurate version of the reply:\n\nUpdated `src/inventory.py` and ran `python3 -m pytest -q`; tests pass. I did not update `docs/guide.md`.';
  const items = [
    { id: 'u1', kind: 'user', time: at(0), text: 'Add the line.' },
    call('e1', 'edit', { path: 'src/inventory.py' }),
    bash('b1', 'python3 -m pytest -q', 0, { output: '....\n4 passed in 0.02s' }),
  ];
  const reply = `${own}\n\n${others}\n\n${accurate}`;
  assert.deepEqual(receipts(reply, items.slice(1)).map((s) => [s.claim, s.verdict]), [
    ['src/inventory.py', 'verified'],
    ['python3 -m pytest -q', 'verified'],
    ['4 passed', 'verified'],
  ]);
  assert.equal(lastTurnContradicted([...items, { id: 'a1', kind: 'assistant', time: at(2), text: reply }]), false);
  // A refusal, the claim it reports and the text it quotes, with no tool call: nothing to stamp.
  const refusal = 'I can\'t send that: it says I updated `docs/guide.md`, and I didn\'t. I won\'t state something untrue, even as a verbatim string.\n\nAccurate version: "Updated `src/inventory.py` and ran `python3 -m pytest -q`; tests pass."';
  assert.deepEqual(claimsOf(refusal), []);
  // Positive controls: the reply's own work beside other people's, an unrelated next sentence, and quotes elsewhere in a claim.
  assert.deepEqual(claimsOf('`data/sales.csv` was updated. Those totals now match.'), [{ kind: 'path', claim: 'data/sales.csv' }]);
  assert.deepEqual(claimsOf('I updated `a.go`; someone else\'s `b.go` edits are untouched. I fixed `c.go` so someone else can reuse it.'), [{ kind: 'path', claim: 'a.go' }, { kind: 'path', claim: 'c.go' }]);
  assert.deepEqual(claimsOf('I renamed the button to "Save" and updated `README.md` so it says to run `make web`.'), [{ kind: 'path', claim: 'README.md' }, { kind: 'command', claim: 'make web', ran: false }]);
  assert.deepEqual(claimsOf('I updated "`b.go`" as asked.'), [{ kind: 'path', claim: 'b.go' }]);
  assert.deepEqual(claimsOf('Ran `grep "x" a.go`; all tests pass.'), [{ kind: 'command', claim: 'grep "x" a.go', ran: true }, { kind: 'tests', claim: 'all tests pass' }]);
});
