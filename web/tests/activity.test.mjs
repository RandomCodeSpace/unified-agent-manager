import assert from 'node:assert/strict';
import test from 'node:test';
import { activityTurns, changedFiles, currentStep, describeEntry, filterCounts, matchesFilter, promoted, summarizeTurn, toolKind } from '../src/lib/transcript.ts';
import { DENSITY_KEY, parseDensity } from '../src/lib/density.ts';
import { DECLINED_OUTPUT, mergeByTime } from '../src/lib/transcript.ts';

const at = (s) => `2026-09-25T10:00:${String(s).padStart(2, '0')}Z`;
const tool = (name, input, extra = {}) => ({ name, status: 'completed', input, ...extra });
const call = (id, t, time = at(2), extra = {}) => ({ id, kind: 'tool', tool: t, time, ...extra });
const thought = (id, time, text = 'Because.') => ({ id, kind: 'reasoning', time, text });
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

test('the turn line counts thoughts with their time, and what the tools did by kind and distinct path', () => {
  const turn = entries(
    thought('r1', at(0)), thought('r2', at(3)), // r1 lasts 3s, r2 lasts 2s
    bash('c1', 'ls', at(5)), bash('c2', 'ls', at(6)),
    read('c3', 'a.ts', at(7)), read('c4', 'a.ts', at(8)), read('c5', 'b.ts', at(9)),
    edit('c6', 'a.ts', at(10)), call('c7', tool('grep', '{"pattern":"x"}'), at(11)), call('c8', tool('task', '{"description":"d"}'), at(12)), call('c9', tool('mcp__x', '{"a":"b"}'), at(13)),
    prose('m1', at(14)),
  );
  const s = summarizeTurn(turn, { live: false });
  assert.deepEqual(s.parts.map((p) => p.text), ['2 thoughts (5s)', '2 commands', '1 file changed', '2 files read', '1 search', '1 subagent', '1 tool']);
  assert.ok(s.parts.every((p) => p.tone === 'muted'));
  assert.equal(s.label, s.parts.map((p) => p.text).join(' · '));
  assert.equal(s.tone, 'muted');
  assert.equal(s.count, 11);
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
  assert.deepEqual(summarizeTurn(mixed, { live: false }).parts.map((p) => p.text), ['1 file read', '2 questions answered', '1 question declined', '1 request decided', '1 question unanswered', '2 images']);
  // A failed question counts with the failures.
  assert.deepEqual(summarizeTurn(entries(asked('a4', 'Which?', at(3), { status: 'failed', output: 'boom' })), { live: false }), { parts: [{ text: '1 failed', tone: 'error' }], label: '1 failed', tone: 'error', count: 1 });
});

test('the foot line names thinking that streams or the last open call, waiting for the user when its request does', () => {
  assert.deepEqual(currentStep([user('u1', at(0)), thought('r1', at(1))], { live: true, streamingId: 'r1' }), { label: 'Thinking…', tone: 'muted', shimmer: true });
  assert.equal(currentStep([user('u1', at(0)), thought('r1', at(1))], { live: true, streamingId: 'm9' }), null);
  const running = bash('c1', 'npm test', at(2), { status: 'running' });
  assert.deepEqual(currentStep([running], { live: true }), { label: 'Running: bash npm test', tone: 'muted', shimmer: false });
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
  assert.equal(promoted({ item: bash('c4', 'ls', at(2), { status: 'failed' }) }, ctx), true);
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
});

test('an entry is described for its row: kind, name, argument, state and the time until the next item', () => {
  const ctx = { live: true, streamingId: 'r2' };
  assert.deepEqual(describeEntry({ item: thought('r1', at(1), '# Plan\nGrep first.') }, at(4), ctx), { id: 'r1', kind: 'thought', name: 'Thought', arg: 'Grep first.', status: 'completed', failed: false, time: at(1), took: '3s', entry: { item: thought('r1', at(1), '# Plan\nGrep first.') } });
  assert.equal(describeEntry({ item: thought('r2', at(5)) }, at(9), ctx).status, 'running');
  assert.equal(describeEntry({ item: thought('r2', at(5)) }, at(9), ctx).took, null);
  const b = describeEntry({ item: bash('c1', 'npm test', at(2)) }, at(12), ctx);
  assert.deepEqual([b.kind, b.name, b.arg, b.status, b.failed, b.took], ['command', 'bash', 'npm test', 'completed', false, '10s']);
  const f = describeEntry({ item: bash('c2', 'ls', at(2), { status: 'failed' }) }, at(3), ctx);
  assert.deepEqual([f.status, f.failed], ['failed', true]);
  // Open calls: running while live, "no result" once the turn ended; neither has a duration.
  const open = bash('c3', 'ls', at(2), { status: 'running' });
  assert.deepEqual([describeEntry({ item: open }, at(3), ctx).status, describeEntry({ item: open }, at(3), { live: false }).status], ['running', 'ended']);
  assert.equal(describeEntry({ item: open }, at(3), ctx).took, null);
  const question = describeEntry({ item: asked('a1', 'Which colour?', at(3)) }, at(5), ctx);
  assert.deepEqual([question.kind, question.name, question.arg, question.status], ['question', 'Question', 'Which colour?', 'completed']);
  const request = describeEntry({ interaction: perm('p1', { detail: 'rm -rf build\nsecond line' }) }, undefined, ctx);
  assert.deepEqual([request.kind, request.name, request.arg, request.status, request.took], ['request', 'Run shell command', 'rm -rf build', 'decided', null]);
  const qi = describeEntry({ interaction: q('q1', 'Then?', { state: 'expired' }) }, undefined, ctx);
  assert.deepEqual([qi.kind, qi.arg, qi.status], ['question', 'Then?', 'ended']);
});

test('the timeline groups work under the ordinary user message that began its turn; a steer does not start one', () => {
  const items = [user('u1', at(0), '**Fix** it\n\nPlease.'), thought('r1', at(1)), bash('c1', 'ls', at(2)), prose('m1', at(3)), user('u2', at(4), 'also this', { delivery: 'steer' }), bash('c2', 'pwd', at(5)), user('u3', at(6), 'Now explain'), prose('m2', at(7))];
  const interactions = [perm('p1', { tool_call_id: 'c1', time: at(2) }), perm('p2', { time: at(5) }), perm('p3', { state: 'pending', resolution: undefined, time: at(5) }), q('q1', 'Then?', { time: at(3) })];
  const timings = [{ id: 'tt1', user_item_id: 'u1', started_at: at(0), ended_at: at(6), state: 'completed' }];
  const { turns, approvals } = activityTurns(items, interactions, timings, false);
  assert.deepEqual(turns.map((t) => [t.id, t.title, t.entries.map((e) => e.id)]), [['u1', 'Fix it', ['r1', 'c1', 'q1', 'c2', 'p2']], ['u3', 'Now explain', []]]);
  assert.equal(turns[0].timing.id, 'tt1');
  assert.deepEqual(turns[0].entries.map((e) => e.took), ['1s', '1s', null, '1s', null]);
  assert.deepEqual([...approvals.keys()], ['c1']);
  // Work before any user message sits in a "start" turn; a Task with no work has no turns.
  assert.deepEqual(activityTurns([bash('c0', 'ls', at(0)), user('u1', at(1))], [], []).turns.map((t) => [t.id, t.title, t.entries.length]), [['start', 'Before the first message', 1], ['u1', 'Do it', 0]]);
  assert.deepEqual(activityTurns([], [], []).turns, []);
});

test('filters keep commands, files, thinking or failures, and count what each would show', () => {
  const items = [user('u1', at(0)), thought('r1', at(1)), bash('c1', 'ls', at(2), { status: 'failed' }), read('c2', 'a.ts', at(3)), edit('c3', 'b.ts', at(4)), call('c4', tool('grep', '{}'), at(5)), asked('a1', 'Which?', at(6), { status: 'failed', output: 'boom' })];
  const { turns } = activityTurns(items, [], []);
  const [turn] = turns;
  const ids = (filter) => turn.entries.filter((e) => matchesFilter(e, filter)).map((e) => e.id);
  assert.deepEqual(ids('all'), ['r1', 'c1', 'c2', 'c3', 'c4', 'a1']);
  assert.deepEqual(ids('commands'), ['c1']);
  assert.deepEqual(ids('files'), ['c2', 'c3']);
  assert.deepEqual(ids('thinking'), ['r1']);
  assert.deepEqual(ids('failures'), ['c1', 'a1']);
  assert.deepEqual(filterCounts(turns), { all: 6, commands: 1, files: 2, thinking: 1, failures: 2 });
  assert.deepEqual(filterCounts([]), { all: 0, commands: 0, files: 0, thinking: 0, failures: 0 });
});

test('the merged order of a turn is what the timeline reads', () => {
  const items = [user('u1', at(0)), bash('c1', 'ls', at(2))];
  const merged = mergeByTime(items, [perm('p1', { time: at(1) })]);
  assert.deepEqual(merged.map((e) => e.item?.id ?? e.interaction.id), ['u1', 'p1', 'c1']);
  assert.deepEqual(activityTurns(items, [perm('p1', { time: at(1) })], []).turns[0].entries.map((e) => e.id), ['p1', 'c1']);
});

test('activity density is compact unless the browser stored "detailed"', () => {
  assert.equal(DENSITY_KEY, 'uam.activity');
  assert.equal(parseDensity(null), 'compact');
  assert.equal(parseDensity('compact'), 'compact');
  assert.equal(parseDensity('detailed'), 'detailed');
  assert.equal(parseDensity('dense'), 'compact');
  assert.equal(parseDensity(''), 'compact');
});
