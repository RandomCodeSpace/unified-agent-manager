import assert from 'node:assert/strict';
import test from 'node:test';
import { callWords, changedFiles, currentStep, firstErrorLine, foldLabel, itemTook, promoted, scratchPlan, summarizeTurn, timelineRows, toolKind } from '../src/lib/transcript.ts';
import { DECLINED_OUTPUT } from '../src/lib/transcript.ts';

const at = (s) => `2026-09-25T10:00:${String(s).padStart(2, '0')}Z`;
const tool = (name, input, extra = {}) => ({ name, status: 'completed', input, ...extra });
const call = (id, t, time = at(2), extra = {}) => ({ id, kind: 'tool', tool: t, time, ...extra });
const thought = (id, time, text = 'Because.', ended) => ({ id, kind: 'reasoning', time, text, ...(ended ? { ended_at: ended } : {}) });
const prose = (id, time, text = 'Done.') => ({ id, kind: 'assistant', time, text });
const user = (id, time, text = 'Do it', extra = {}) => ({ id, kind: 'user', time, text, ...extra });
const perm = (id, extra = {}) => ({ id, kind: 'permission', title: 'Run shell command', detail: 'ls', state: 'answered', resolution: 'allowed (yolo)', time: at(3), ...extra });
const q = (id, text, extra = {}) => ({ id, kind: 'question', title: 'Question', state: 'answered', resolution: 'Answered: red', time: at(5), questions: [{ text, choices: ['red', 'blue'], custom: false }], ...extra });
const asked = (id, question, time, extra = {}) => call(id, tool('ask_user', JSON.stringify({ question, choices: ['red', 'blue'] }), { output: 'User selected: blue', ...extra }), time);
const bash = (id, cmd, time = at(2), extra = {}) => call(id, tool('bash', JSON.stringify({ command: cmd }), extra), time);
const read = (id, path, time = at(2)) => call(id, tool('view', JSON.stringify({ path })), time);
const edit = (id, path, time = at(2), extra = {}) => call(id, tool('edit', JSON.stringify({ path }), extra), time);
const entries = (...items) => items.map((item) => ({ item }));

test('a tool call is a command, a file, a search, a subagent or a question by its name; anything else is a tool', () => {
  assert.equal(toolKind('bash'), 'command');
  assert.equal(toolKind('PowerShell'), 'command');
  for (const n of ['view', 'read', 'edit', 'write', 'create', 'list']) assert.equal(toolKind(n), 'file');
  for (const n of ['grep', 'glob', 'web_fetch', 'web_search']) assert.equal(toolKind(n), 'search');
  assert.equal(toolKind('task'), 'subagent');
  assert.equal(toolKind('ask_user'), 'question');
  assert.equal(toolKind('mcp__x__y'), 'tool');
});

test('the turn line counts thoughts, and what the tools did by kind and distinct path', () => {
  const turn = entries(
    thought('r1', at(0), undefined, at(3)), thought('r2', at(3), undefined, at(5)), // r1 took 3s, r2 took 2s
    bash('c1', 'ls', at(5)), bash('c2', 'ls', at(6)),
    read('c3', 'a.ts', at(7)), read('c4', 'a.ts', at(8)), read('c5', 'b.ts', at(9)),
    edit('c6', 'a.ts', at(10)), call('c7', tool('grep', '{"pattern":"x"}'), at(11)), call('c8', tool('task', '{"description":"d"}'), at(12)), call('c9', tool('mcp__x', '{"a":"b"}'), at(13)),
    prose('m1', at(14)),
  );
  const s = summarizeTurn(turn, { live: false });
  // A subagent's call is the reply's subagent chip, never a count here.
  assert.deepEqual(s.parts.map((p) => p.text), ['2 thoughts', '2 commands', '1 file changed', '2 files read', '1 search', '1 tool']);
  assert.ok(s.parts.every((p) => p.tone === 'muted'));
  assert.equal(s.label, s.parts.map((p) => p.text).join(' · '));
  assert.equal(s.tone, 'muted');
  assert.equal(s.count, 10);
  // Nor is a failed or unfinished one: the chip says how its subagent did.
  const spawned = (status) => call('t9', tool('task', '{"description":"d"}', { status }), at(20));
  for (const status of ['failed', 'running', 'completed']) assert.deepEqual(summarizeTurn(entries(spawned(status)), { live: false }), { parts: [], label: '', tone: 'muted', count: 0 });
  // Prose alone folds nothing: the head row stays a plain "Took".
  assert.deepEqual(summarizeTurn(entries(prose('m1', at(1))), { live: false }), { parts: [], label: '', tone: 'muted', count: 0 });
  // A thought without text or still streaming is not counted; two searches read "searches".
  assert.deepEqual(summarizeTurn(entries(thought('r1', at(0), ''), thought('r2', at(1)), call('g1', tool('grep', '{}'), at(2)), call('g2', tool('glob', '{}'), at(3))), { live: true, streamingId: 'r2' }).parts, [{ text: '2 searches', tone: 'muted' }]);
});

test('the turn line keeps what needs attention explicit: failures, calls without a result, questions, requests, images', () => {
  const failed = summarizeTurn(entries(bash('c1', 'ls', at(1), { status: 'failed', output: 'boom' }), bash('c2', 'ls', at(2))), { live: false });
  assert.deepEqual(failed, { parts: [{ text: '1 command', tone: 'muted' }, { text: '1 failed', tone: 'error' }], label: '1 command · 1 failed', tone: 'error', count: 2 });
  // The turn ended before the call reported: no result. Live, the foot line names it and the line stays quiet.
  const open = bash('c3', 'npm test', at(3), { status: 'running', output: undefined });
  assert.deepEqual(summarizeTurn(entries(open), { live: false }).parts.map((p) => p.text), ['1 without a result']);
  assert.deepEqual(summarizeTurn(entries(open), { live: true }).parts, []);
  // A request that waits for the user turns the line to attention.
  const waiting = new Map([['c3', [perm('p1', { tool_call_id: 'c3', state: 'pending', resolution: undefined })]]]);
  assert.equal(summarizeTurn(entries(open), { live: true, approvals: waiting }).tone, 'attention');
  const auto = new Map([['c3', [perm('p1', { tool_call_id: 'c3', state: 'pending', resolution: undefined, auto: true })]]]);
  assert.equal(summarizeTurn(entries(open), { live: true, approvals: auto }).tone, 'muted');
  // Questions by outcome, never as tool calls; decided requests; images.
  const declined = asked('a2', 'Which?', at(4), { output: `User responded: ${DECLINED_OUTPUT}` });
  const none = asked('a3', 'Which?', at(5), { status: 'running', output: undefined });
  const mixed = [...entries(asked('a1', 'Which?', at(3)), declined, none, { ...read('c4', 'x.png', at(6)), images: [{ id: 'i1' }, { id: 'i2' }] }), { interaction: perm('p2') }, { interaction: q('q1', 'Then?') }];
  assert.deepEqual(summarizeTurn(mixed, { live: false }).parts.map((p) => p.text), ['1 file read', '2 questions answered', '1 question declined', '1 request decided', '1 question not answered', '2 images']);
  // A failed question counts with the failures.
  assert.deepEqual(summarizeTurn(entries(asked('a4', 'Which?', at(3), { status: 'failed', output: 'boom' })), { live: false }), { parts: [{ text: '1 failed', tone: 'error' }], label: '1 failed', tone: 'error', count: 1 });
});

test('the foot line names thinking that streams or the last open call, waiting for the user when its request does', () => {
  const streaming = thought('r1', at(1));
  // A streaming thought and a running call carry their item: the foot shows them unfolded.
  assert.deepEqual(currentStep([user('u1', at(0)), streaming], { live: true, streamingId: 'r1' }), { label: 'Thinking…', tone: 'muted', shimmer: true, item: streaming });
  assert.equal(currentStep([user('u1', at(0)), thought('r1', at(1))], { live: true, streamingId: 'm9' }), null);
  const running = bash('c1', 'npm test', at(2), { status: 'running' });
  assert.deepEqual(currentStep([running], { live: true }), { label: 'Running: bash npm test', tone: 'muted', shimmer: false, item: running });
  // A question keeps its own place: the foot names it but does not unfold it.
  assert.equal(currentStep([asked('a1', 'Which?', at(2), { status: 'running', output: undefined })], { live: true }).item, undefined);
  // A subagent's call is never the step: the live subagent card shows it.
  assert.equal(currentStep([call('t1', tool('task', '{"description":"d"}', { status: 'running' }))], { live: true }), null);
  const approvals = new Map([['c1', [perm('p1', { tool_call_id: 'c1', state: 'pending', resolution: undefined })]]]);
  assert.deepEqual(currentStep([running], { live: true, approvals }), { label: 'Waiting for your approval: bash npm test', tone: 'attention', shimmer: false });
  const question = new Map([['c1', [q('q1', 'Which?', { tool_call_id: 'c1', state: 'pending' })]]]);
  assert.equal(currentStep([running], { live: true, approvals: question }).label, 'Waiting for your answer: bash npm test');
  // Not live, completed, prose, or a subagent's call: the verb stands.
  assert.equal(currentStep([running], { live: false }), null);
  assert.equal(currentStep([bash('c2', 'ls', at(2))], { live: true }), null);
  assert.equal(currentStep([prose('m1', at(3))], { live: true, streamingId: 'm1' }), null);
  assert.equal(currentStep([running], { live: true }, (item) => item.id === 'c1'), null);
  assert.equal(currentStep([], { live: true }), null);
});

test('promotion: failures, answered questions, images, subagents, question requests and prose stand in the answer; routine work and thoughts fold', () => {
  const ctx = { live: false };
  assert.equal(promoted({ item: prose('m1', at(1)) }, ctx), true);
  assert.equal(promoted({ item: { id: 'n1', kind: 'notice', time: at(1), text: 'Turn stopped.' } }, ctx), true);
  assert.equal(promoted({ item: user('u2', at(1), 'steer', { delivery: 'steer' }) }, ctx), true);
  assert.equal(promoted({ item: thought('r1', at(1)) }, ctx), false);
  assert.equal(promoted({ item: bash('c1', 'ls') }, ctx), false);
  assert.equal(promoted({ item: read('c2', 'a.ts') }, ctx), false);
  assert.equal(promoted({ item: edit('c3', 'a.ts') }, ctx), false);
  // A failed call folds like any other; the turn line's "1 failed" carries it.
  assert.equal(promoted({ item: bash('c4', 'ls', at(2), { status: 'failed' }) }, ctx), false);
  assert.equal(promoted({ item: { ...read('c5', 'x.png'), images: [{ id: 'i1' }] } }, ctx), true);
  assert.equal(promoted({ item: { ...read('c6', 'x.png'), images_note: '1 image was left out' } }, ctx), true);
  assert.equal(promoted({ item: asked('a1', 'Which?', at(3)) }, ctx), true);
  // A question still waiting is the action card; its call folds until it is answered.
  assert.equal(promoted({ item: asked('a2', 'Which?', at(3), { status: 'running', output: undefined }) }, { live: true }), false);
  assert.equal(promoted({ item: call('t1', tool('task', '{"description":"d"}')) }, ctx, (item) => item.id === 't1'), true);
  assert.equal(promoted({ item: call('t1', tool('task', '{"description":"d"}')) }, ctx), false);
  assert.equal(promoted({ interaction: q('q1', 'Then?') }, ctx), true);
  assert.equal(promoted({ interaction: perm('p1') }, ctx), false);
});

test('a turn changed the distinct paths its completed edit, write and create calls named', () => {
  const turn = entries(edit('c1', 'a.ts'), edit('c2', 'a.ts'), call('c3', tool('write', '{"path":"b.ts"}')), call('c4', tool('create', '{"file_path":"c.ts"}')), edit('c5', 'd.ts', at(2), { status: 'failed' }), read('c6', 'e.ts'), edit('c7', 'f.ts', at(2), { status: 'running' }));
  assert.deepEqual(changedFiles(turn), ['a.ts', 'b.ts', 'c.ts']);
  assert.deepEqual(changedFiles(entries(prose('m1', at(1)))), []);
  // Without a recorded end a thought counts but adds no time: nothing is inferred from what followed.
  assert.deepEqual(summarizeTurn(entries(thought('r1', at(0)), prose('m1', at(9))), { live: false }).parts.map((p) => p.text), ['1 thought']);
});

test('a step reads as plain words with its recorded result: the call kind, the file, the outcome, a decision', () => {
  const words = (item, live = false, approvals) => { const w = callWords(item, live, approvals); return [w.label, w.result.map((r) => r.text).join(' · '), w.tone]; };
  assert.deepEqual(words(read('v1', 'internal/vterm/redraw.go')), ['Read redraw.go', '', 'muted']);
  assert.deepEqual(words(read('v2', 'docs/attach-flow.png')), ['Viewed attach-flow.png', '', 'muted']);
  assert.deepEqual(words(call('g1', tool('grep', '{"pattern":"1004"}'))), ['Searched “1004”', '', 'muted']);
  assert.deepEqual(words(call('g2', tool('grep', '', { title: 'Search "<h1>" in templates' }))), ['Searched "<h1>" in templates', '', 'muted'], 'a title that already says what was searched');
  assert.deepEqual(words(call('g3', tool('glob', '{"pattern":"**/*_test.go"}'))), ['Found **/*_test.go', '', 'muted']);
  assert.deepEqual(words(call('f1', tool('web_fetch', '{"url":"https://example.com/a"}'))), ['Fetched example.com/a', '', 'muted']);
  assert.deepEqual(words(call('s1', tool('skill', '{"skill":"tdd"}'))), ['Loaded skill tdd', '', 'muted']);
  assert.deepEqual(words(bash('b1', 'go test ./...', at(2), { tests: { passed: 12, failed: 0 } })), ['Ran go test ./...', 'ok · 12 passed', 'muted']);
  assert.deepEqual(words(bash('b2', 'go vet ./...', at(2), { status: 'failed', exit_code: 1, output: '# pkg\nredraw.go:88:2: unreachable code\nexit status 1' })), ['Ran go vet ./...', 'exit 1 · redraw.go:88:2: unreachable code', 'error']);
  assert.deepEqual(words(bash('b6', 'make', at(2), { exit_code: 2, output: 'make: *** [all] Error 2' })), ['Ran make', 'exit 2 · make: *** [all] Error 2', 'error'], 'a completed call that exited non-zero failed');
  assert.deepEqual(words(bash('b7', 'go test', at(2), { tests: { passed: 3, failed: 1 } })), ['Ran go test', '3 passed · 1 failed', 'error']);
  assert.deepEqual(words(bash('b3', 'sleep 9', at(2), { status: 'running' }), true), ['Running sleep 9', '', 'muted']);
  assert.deepEqual(words(bash('b4', 'sleep 9', at(2), { status: 'running' })), ['Ran sleep 9', 'no result', 'muted']);
  assert.deepEqual(words(edit('e1', 'a/redraw.go', at(2), { file_edits: [{ path: 'a/redraw.go', kind: 'update', diff_status: 'available', additions: 12, deletions: 3 }] }), false, [perm('p1')]), ['Edited redraw.go', '+12 −3 · auto-approved', 'muted']);
  assert.deepEqual(words(bash('b5', 'rm -rf build/'), false, [perm('p2', { resolution: 'allowed once' })]), ['Ran rm -rf build/', 'allowed', 'attention']);
  assert.deepEqual(words(asked('a1', 'Which?', at(2))), ['Which?', 'answered', 'attention']);
  const intent = callWords(call('i1', tool('report_intent', '{"intent":"Running the tests"}')), false);
  assert.equal(intent.intent, true);
  assert.equal(intent.label, 'Running the tests');
  assert.equal(firstErrorLine('\nok\n'), 'ok');
});

test('the timeline folds runs of one kind, marks gaps over 30s and caps at 20 steps, every step one press away', () => {
  const sec = (s) => new Date(Date.parse('2026-09-25T10:00:00Z') + s * 1000).toISOString();
  const done = (item, s, took = 1) => ({ ...item, time: sec(s), ended_at: sec(s + took) });
  const turn = entries(
    done(thought('r1', sec(0), 'Plan.'), 0, 2),
    done(read('c1', 'a.go'), 3), done(read('c2', 'b.go'), 5), done(read('c3', 'c.go'), 7),
    done(bash('c4', 'go test'), 50), // 42s after the last read ended
    done(bash('c5', 'rm -rf x'), 52), // a person allowed it: it stands alone
    done(bash('c6', 'ls'), 200),
  );
  const approvals = new Map([['c5', [perm('p5', { resolution: 'allowed once' })]]]);
  const rows = timelineRows(turn, { live: false, approvals });
  assert.deepEqual(rows.map((r) => r.type === 'fold' ? `fold:${r.items.length}` : r.type === 'gap' ? `gap:${r.ms / 1000}:${r.you ? 'you' : 'model'}` : `entry:${r.key}`), ['entry:r1', 'fold:3', 'gap:42:model', 'entry:c4', 'entry:c5', 'gap:147:model', 'entry:c6']);
  // The person was asked before the call ran: the gap after it is the model's. Asked within the gap, it was theirs; a yolo answer never is.
  const later = new Map([['c5', [perm('p5', { resolution: 'allowed once', time: sec(100) })]]]);
  assert.equal(timelineRows(turn, { live: false, approvals: later }).find((r) => r.type === 'gap' && r.ms === 147_000).you, true);
  const yolo = new Map([['c5', [perm('p5', { time: sec(100) })]]]);
  assert.equal(timelineRows(turn, { live: false, approvals: yolo }).find((r) => r.type === 'gap' && r.ms === 147_000).you, false);
  // Right after a decided request standing alone, the gap is the person's.
  assert.deepEqual(timelineRows([...entries(done(bash('d1', 'a'), 0)), { interaction: perm('p9', { time: sec(2), resolution: 'allowed once' }) }, ...entries(done(bash('d2', 'b'), 60))], { live: false }).filter((r) => r.type === 'gap').map((r) => r.you), [true]);
  assert.equal(foldLabel('read', 3), 'Read 3 files');
  // No recorded end, no gap: nothing is inferred from what followed.
  assert.equal(timelineRows(entries(read('x1', 'a.go', sec(0)), bash('x2', 'ls', sec(90))), { live: false }).filter((r) => r.type === 'gap').length, 0);
  // A failed call never folds into a run, nor one that completed with a non-zero exit or failed tests.
  assert.equal(timelineRows(entries(done(bash('f1', 'a'), 0), done(bash('f2', 'b', at(2), { status: 'failed' }), 2)), { live: false }).length, 2);
  assert.equal(timelineRows(entries(done(bash('f3', 'a'), 0), done(bash('f4', 'b', at(2), { exit_code: 2 }), 2)), { live: false }).length, 2);
  assert.equal(timelineRows(entries(done(bash('f5', 'a'), 0), done(bash('f6', 'b', at(2), { tests: { passed: 3, failed: 1 } }), 2)), { live: false }).length, 2);
  // A subagent's call is left out where its row stands elsewhere.
  assert.equal(timelineRows(entries(call('t1', tool('task', '{"description":"d"}'))), { live: false, skip: () => true }).length, 0);
  // 30 steps of alternating kinds: the first, "17 earlier steps · Show all 30 calls", the last 12.
  const many = entries(...Array.from({ length: 30 }, (_, i) => done(i % 2 ? read(`m${i}`, `${i}.go`) : bash(`m${i}`, `echo ${i}`), i * 2)));
  const capped = timelineRows(many, { live: false });
  assert.equal(capped.length, 14);
  assert.equal(capped[0].key, 'm0');
  assert.deepEqual(capped[1], { type: 'more', key: 'more', hidden: 17, calls: 30 });
  assert.deepEqual(capped.slice(2).map((r) => r.key), Array.from({ length: 12 }, (_, i) => `m${18 + i}`));
  assert.equal(timelineRows(many, { live: false, all: true }).length, 30);
});

test('a step took the span from its recorded start to its recorded end; nothing is inferred without an end', () => {
  assert.equal(itemTook(thought('r1', at(1), 'Plan.', at(4))), '3s');
  assert.equal(itemTook({ ...bash('c1', 'npm test', at(2)), ended_at: at(12) }), '10s');
  assert.equal(itemTook(bash('c2', 'ls', at(2))), null);
  assert.equal(itemTook(thought('r2', at(5))), null);
});

test('an apply_patch call changes every file its patch names, framed loosely or not', async () => {
  const { completedChanges, patchPaths } = await import('../src/lib/transcript.ts');
  const patch = '*** Begin Patch\n*** Add File: new.ts\n+x\n*** Update File: a.ts\n*** Move to: b.ts\n@@\n-x\n+y\n*** Delete File: old.ts\n*** End Patch';
  assert.deepEqual([...patchPaths(patch)], ['new.ts', 'a.ts', 'b.ts', 'old.ts']);
  assert.deepEqual([...patchPaths(JSON.stringify(patch))], ['new.ts', 'a.ts', 'b.ts', 'old.ts']);
  assert.deepEqual([...patchPaths(JSON.stringify({ input: patch }))], ['new.ts', 'a.ts', 'b.ts', 'old.ts']);
  assert.deepEqual([...patchPaths(JSON.stringify(patch).slice(0, -12))], ['new.ts', 'a.ts', 'b.ts', 'old.ts'], 'a clipped JSON string keeps the headers before the cut');
  assert.deepEqual([...patchPaths(JSON.stringify(patch).slice(0, -20))], ['new.ts', 'a.ts', 'b.ts'], 'and drops a header the cut went through');
  const turn = entries(call('p1', tool('apply_patch', patch)), call('p2', tool('apply_patch', '', { file_paths: ['z.ts'] })), edit('c1', 'a.ts'));
  assert.deepEqual(changedFiles(turn), ['new.ts', 'a.ts', 'b.ts', 'old.ts', 'z.ts']);
  assert.equal(completedChanges(turn.map((e) => e.item)), 3);
  assert.equal(toolKind('apply_patch'), 'file');
});

test("the Task's scratch plan, by its exact path, is not a changed file of the turn", () => {
  const isScratch = scratchPlan('/home/u/.copilot/session-state/s1/plan.md', '/home/u/.copilot/session-state/s1');
  const turn = entries(edit('e1', '/w/a.go'), edit('e2', 'b.go'), call('e3', tool('create', JSON.stringify({ path: '/home/u/.copilot/session-state/s1/plan.md' }))), edit('e4', 'plan.md'));
  assert.ok(summarizeTurn(turn, { live: false, scratch: isScratch }).parts.some((p) => p.text === '2 files changed'));
  assert.deepEqual(changedFiles(turn, isScratch), ['/w/a.go', 'b.go']);
  assert.ok(summarizeTurn(turn, { live: false }).parts.some((p) => p.text === '4 files changed'));
  assert.equal(scratchPlan(undefined, '/w'), undefined);
  assert.equal(scratchPlan('/w/plan.md', '/w')('./plan.md'), true);
  assert.equal(scratchPlan('/w/plan.md', '/w')('docs/plan.md'), false);
});
