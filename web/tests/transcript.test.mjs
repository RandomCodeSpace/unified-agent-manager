import assert from 'node:assert/strict';
import test from 'node:test';
import { DECLINED_OUTPUT, approvalMark, foldWindow, linkInteractions, mainArgument, mergeByTime, questionOf, summarizeTools, toolLabel } from '../src/lib/transcript.ts';

const tool = (name, input, extra = {}) => ({ name, status: 'completed', input, ...extra });

test('the main argument is the command, URL, path, pattern, query, skill or subagent name of its tool', () => {
  assert.equal(mainArgument('bash', '{"command":"ls -la","description":"List files"}'), 'ls -la');
  assert.equal(mainArgument('web_fetch', '{"url":"https://example.com","prompt":"summarize"}'), 'https://example.com');
  assert.equal(mainArgument('view', '{"path":"internal/vterm/redraw.go"}'), 'internal/vterm/redraw.go');
  assert.equal(mainArgument('grep', '{"pattern":"1004","path":"internal/vterm"}'), '1004');
  assert.equal(mainArgument('sql', '{"query":"select 1"}'), 'select 1');
  assert.equal(mainArgument('skill', '{"skill":"release-notes","args":"v0.7"}'), 'release-notes');
  assert.equal(mainArgument('task', '{"prompt":"long prompt","description":"Check contrast"}'), 'Check contrast');
});

test('unknown tools fall back to a known key, then the first string value, then the JSON itself', () => {
  assert.equal(mainArgument('mcp__x__y', '{"count":2,"url":"https://a.b"}'), 'https://a.b');
  assert.equal(mainArgument('mcp__x__y', '{"count":2,"target":"main","other":"z"}'), 'main');
  assert.equal(mainArgument('mcp__x__y', '{"count":2}'), '{"count":2}');
});

test('non-JSON input shows as is, on one line, clipped', () => {
  assert.equal(mainArgument('bash', 'go test ./...\n  -run Redraw'), 'go test ./... -run Redraw');
  assert.equal(mainArgument('bash', ''), '');
  assert.equal(mainArgument('bash', '{"command":"' + 'x'.repeat(400) + '"}').length, 300);
  assert.equal(mainArgument('bash', '{not json'), '{not json');
});

test('the row label is the tool name and its argument, with the title as the fallback', () => {
  assert.deepEqual(toolLabel(tool('view', '{"path":"a.go"}', { title: 'Read a.go' })), { name: 'view', arg: 'a.go' });
  assert.deepEqual(toolLabel(tool('edit', undefined, { title: 'Edit cmd/doctor.go' })), { name: 'edit', arg: 'cmd/doctor.go' });
  assert.deepEqual(toolLabel(tool('bash', undefined, { title: 'go test ./cmd/...' })), { name: 'bash', arg: 'go test ./cmd/...' });
  assert.deepEqual(toolLabel({ name: '', status: 'completed', title: 'Read templates/post.html' }), { name: 'Read', arg: 'templates/post.html' });
  assert.deepEqual(toolLabel({ name: 'bash', status: 'running' }), { name: 'bash', arg: '' });
  assert.deepEqual(toolLabel(undefined), { name: 'Tool', arg: '' });
});

test('a run of more than 8 calls keeps the first 2 and the last 3', () => {
  assert.deepEqual(foldWindow(8), { head: 8, hidden: 0, tail: 0 });
  assert.deepEqual(foldWindow(9), { head: 2, hidden: 4, tail: 3 });
  assert.deepEqual(foldWindow(16), { head: 2, hidden: 11, tail: 3 });
});

const item = (id, agent_id, time = '2026-09-24T10:00:00Z', t = tool('bash', '{"command":"ls"}')) => ({ id, kind: 'tool', tool: t, time, agent_id });
const ix = (id, extra) => ({ id, kind: 'permission', title: 'Run shell command', state: 'answered', resolution: 'allowed (yolo)', time: '2026-09-24T10:00:01Z', ...extra });
const asked = (id, question, time, extra = {}) => item(id, undefined, time, tool('ask_user', JSON.stringify({ question, choices: ['red', 'blue'] }), { output: 'User selected: blue', ...extra }));
const q = (id, text, extra = {}) => ({ id, kind: 'question', title: 'Question from Copilot', state: 'answered', time: '2026-09-24T10:00:05Z', questions: [{ text, choices: ['red', 'blue'], custom: false }], ...extra });

test('a decided request sits on the tool row it names, for the same agent only', () => {
  const items = [item('call_1'), item('call_2', 'agent-1')];
  const list = [
    ix('p1', { tool_call_id: 'call_1' }),
    ix('p2', { tool_call_id: 'call_2', agent_id: 'agent-1' }),
    ix('p3', { tool_call_id: 'call_2' }), // wrong agent: loose on the main transcript
    ix('p4', { tool_call_id: 'call_9' }), // unknown call
    ix('p5', {}), // no call
    ix('p6', { tool_call_id: 'call_1', state: 'pending', resolution: undefined }), // pending: on the row too, but it stays a card
  ];
  const main = linkInteractions(items, list);
  assert.deepEqual([...main.linked.keys()], ['call_1']);
  assert.deepEqual(main.linked.get('call_1').map((i) => i.id), ['p1', 'p6']);
  assert.deepEqual(main.loose.map((i) => i.id), ['p3', 'p4', 'p5']);
  assert.deepEqual(main.questions, []);
  const agent = linkInteractions(items, list, 'agent-1');
  assert.deepEqual([...agent.linked.keys()], ['call_2']);
  assert.deepEqual(agent.loose, []);
});

test('when two requests name one call both are kept, oldest first', () => {
  const { linked } = linkInteractions([item('call_1')], [ix('b', { tool_call_id: 'call_1', time: '2026-09-24T10:00:02Z' }), ix('a', { tool_call_id: 'call_1', resolution: 'Deny', state: 'rejected' })]);
  assert.deepEqual(linked.get('call_1').map((i) => i.id), ['a', 'b']);
});

test('questions without a call take the ask_user rows with their text in time order', () => {
  const items = [asked('call_a', 'Colour?', '2026-09-24T10:00:00Z'), asked('call_b', 'Colour?', '2026-09-24T10:01:00Z'), asked('call_c', 'Size?', '2026-09-24T10:02:00Z')];
  const late = q('q2', 'Colour?', { time: '2026-09-24T10:01:30Z' });
  const early = q('q1', 'Colour?', { time: '2026-09-24T10:00:30Z' });
  const byId = q('q3', 'Size?', { tool_call_id: 'call_c', time: '2026-09-24T10:02:30Z' });
  const spare = q('q4', 'Colour?', { time: '2026-09-24T10:03:00Z' });
  const other = { ...q('q5', 'Shape?'), tool_call_id: undefined };
  const { linked, questions, loose } = linkInteractions(items, [late, early, byId, spare, other]);
  assert.equal(linked.get('call_a')[0], early);
  assert.equal(linked.get('call_b')[0], late);
  assert.equal(linked.get('call_c')[0], byId);
  assert.deepEqual(questions.map((i) => i.id), ['q5', 'q4']);
  assert.deepEqual(loose, []);
});

test('the approval word is auto for yolo, allowed or denied for a person, and the full resolution stays behind it', () => {
  assert.deepEqual(approvalMark(ix('a', {})), { word: 'auto', full: 'Run shell command · Answered · allowed (yolo)', tone: 'ok' });
  assert.equal(approvalMark(ix('a', { resolution: 'Allow once' })).word, 'allowed');
  assert.equal(approvalMark(ix('a', { state: 'rejected', resolution: 'Deny' })).word, 'denied');
  assert.deepEqual(approvalMark(ix('a', { state: 'expired', resolution: undefined })), { word: 'expired', full: 'Run shell command · Expired', tone: 'gone' });
  assert.equal(approvalMark({ id: 'q', kind: 'question', title: 'Question', state: 'rejected', time: '' }).word, 'declined');
});

test('loose requests join the rows by time', () => {
  const items = [item('a', undefined, '2026-09-24T10:00:00Z'), item('b', undefined, '2026-09-24T10:00:10Z')];
  const list = [ix('late', { time: '2026-09-24T10:00:20Z' }), ix('mid', { time: '2026-09-24T10:00:05Z' })];
  assert.deepEqual(
    mergeByTime(items, list).map((e) => e.item?.id ?? e.interaction.id),
    ['a', 'mid', 'b', 'late'],
  );
});

test('an ask_user call reads back its question, choices and answer, live and from history', () => {
  const input = '{"question":"Which colour?","choices":["red","blue"]}';
  const one = { questions: [{ text: 'Which colour?', choices: ['red', 'blue'] }] };
  assert.deepEqual(questionOf(tool('ask_user', input, { status: 'running' }), undefined, true), { ...one, chosen: [], outcome: 'pending' });
  assert.deepEqual(questionOf(tool('ask_user', input, { output: 'User selected: blue' }), undefined, false), { ...one, chosen: ['blue'], answer: 'blue', outcome: 'answered' });
  assert.deepEqual(questionOf(tool('ask_user', input, { output: 'User responded: teal' }), undefined, false).chosen, []);
  assert.equal(questionOf(tool('ask_user', input, { output: 'teal' }), undefined, false).answer, 'teal');
  assert.deepEqual(questionOf(tool('ask_user', input, { status: 'failed', output: 'boom' }), undefined, false), { ...one, chosen: [], outcome: 'failed', error: 'boom' });
  assert.equal(questionOf(tool('bash', '{"command":"ls"}'), undefined, true), null);
});

test('a call left open when the turn is not live got no answer', () => {
  const open = tool('ask_user', '{"question":"Which?","choices":["a"]}', { status: 'running' });
  assert.equal(questionOf(open, undefined, false).outcome, 'none');
  assert.equal(questionOf(open, undefined, true).outcome, 'pending');
  assert.equal(questionOf(open, q('q', 'Which?', { state: 'pending' }), false).outcome, 'pending');
  assert.equal(questionOf(open, q('q', 'Which?', { state: 'expired' }), false).outcome, 'none');
});

test('a declined question reads as declined from its rejected interaction or the exact recorded output, never from a substring', () => {
  const input = '{"question":"Licence?","choices":["MIT","GPL-3.0"]}';
  const recorded = tool('ask_user', input, { output: `User responded: ${DECLINED_OUTPUT}` });
  const rejected = q('q', 'Licence?', { state: 'rejected' });
  assert.equal(questionOf(recorded, rejected, false).outcome, 'declined');
  assert.equal(questionOf(recorded, undefined, false).outcome, 'declined');
  assert.equal(questionOf(recorded, undefined, false).answer, undefined);
  assert.equal(questionOf(tool('ask_user', input, { status: 'failed', output: 'the user declined to answer' }), undefined, false).outcome, 'declined');
  const typed = questionOf(tool('ask_user', input, { output: 'User responded: I declined the MIT offer last year' }), undefined, false);
  assert.equal(typed.outcome, 'answered');
  assert.equal(typed.answer, 'I declined the MIT offer last year');
  // A real answer stays an answer even when the interaction says otherwise.
  assert.equal(questionOf(tool('ask_user', input, { output: 'User selected: MIT' }), { ...rejected, state: 'answered' }, false).answer, 'MIT');
});

test('a question interaction alone drives the block, so any provider gets it', () => {
  const two = {
    id: 'que_1',
    kind: 'question',
    title: 'Question',
    state: 'answered',
    resolution: 'Answered: A; X, Y',
    time: '',
    questions: [
      { text: 'Pick one', header: 'Pick', choices: ['A', 'B'], custom: true },
      { text: 'Pick many', header: 'Many', choices: ['X', 'Y', 'Z'], multiple: true, custom: false },
    ],
  };
  assert.deepEqual(questionOf(undefined, two, false), {
    questions: [
      { text: 'Pick one', header: 'Pick', choices: ['A', 'B'] },
      { text: 'Pick many', header: 'Many', choices: ['X', 'Y', 'Z'] },
    ],
    chosen: ['A', 'X', 'Y'],
    answer: 'A; X, Y',
    outcome: 'answered',
  });
  assert.equal(questionOf(undefined, { ...two, state: 'rejected', resolution: 'Dismissed' }, false).outcome, 'declined');
  assert.equal(questionOf(undefined, { ...two, state: 'pending', resolution: undefined }, false).outcome, 'pending');
  assert.equal(questionOf(undefined, { ...two, kind: 'permission' }, false), null);
  // Linked to an OpenCode tool part by tool_call_id, it sits on that row like any request.
  const { linked, questions } = linkInteractions([item('prt_q', undefined, '', tool('question', '{}'))], [{ ...two, tool_call_id: 'prt_q' }]);
  assert.equal(linked.get('prt_q')[0].id, 'que_1');
  assert.deepEqual(questions, []);
});


test('tool summaries count successful distinct paths and report failed or missing results', async () => {
  const { summarizeTools } = await import('../src/lib/transcript.ts');
  const row = (name, status = 'completed', input) => ({ kind: 'tool', tool: { name, status, input } });
  const rows = [row('edit', 'completed', '{"path":"a.ts"}'), row('edit', 'completed', '{"path":"a.ts"}'), row('bash'), row('bash', 'failed'), row('read', 'completed', '{"path":"b.ts"}'), row('mcp__unknown'), row('edit', 'running')];
  assert.equal(summarizeTools(rows, true), 'Changed 1 file, ran 1 command, read 1 file and used 1 tool · 1 running · 1 failed');
  assert.equal(summarizeTools(rows, false), 'Changed 1 file, ran 1 command, read 1 file and used 1 tool · 1 failed · 1 without a result');
  assert.equal(summarizeTools([row('edit')], false), 'Used 1 tool');
});

test('foreground elapsed uses recorded turn evidence across steer and rejects unknown times', async () => {
  const { foregroundStart, elapsedSince } = await import('../src/lib/transcript.ts');
  const start = '2026-09-24T12:00:00Z';
  const timings = [{ id: 'turn', user_item_id: 'user', started_at: start, state: 'working' }];
  assert.equal(foregroundStart(timings), start);
  assert.equal(foregroundStart([]), undefined);
  assert.equal(elapsedSince(start, Date.parse('2026-09-24T12:13:00Z')), '13m');
  assert.equal(elapsedSince(undefined, Date.now()), null);
  assert.equal(elapsedSince('invalid', Date.now()), null);
  assert.equal(elapsedSince(start, Date.parse('2026-09-24T11:59:00Z')), null);
});


test('all current turn segments stay live across steer, while earlier turns stay ended', async () => {
  const { foregroundItems } = await import('../src/lib/transcript.ts');
  const history = [{ id: 'old-user', kind: 'user' }, { id: 'old-tool', kind: 'tool' }, { id: 'user', kind: 'user' }, { id: 'pending-tool', kind: 'tool' }, { id: 'steer', kind: 'user', delivery: 'steer' }, { id: 'progress', kind: 'assistant' }];
  assert.deepEqual(foregroundItems(history).map((item) => item.id), ['user', 'pending-tool', 'steer', 'progress']);
  assert.deepEqual(foregroundItems([{ id: 'orphan', kind: 'tool' }]).map((item) => item.id), ['orphan']);
});

test('recorded final duration is stable and unknown history has no invented interval', async () => {
  const { foregroundStart, completedDuration, timingForTurn } = await import('../src/lib/transcript.ts');
  const working = { id: 'turn', user_item_id: 'user', started_at: '2026-09-24T12:00:00Z', state: 'working' };
  assert.equal(foregroundStart([working]), working.started_at);
  assert.equal(completedDuration(working), null);
  for (const state of ['completed', 'cancelled', 'failed']) {
    const ended = { ...working, ended_at: '2026-09-24T12:00:42Z', state };
    assert.equal(completedDuration(ended), '42s');
    assert.equal(foregroundStart([ended]), undefined);
    assert.deepEqual(timingForTurn([ended], 'user'), ended);
  }
  assert.equal(timingForTurn([working], 'imported-user'), undefined);
  assert.equal(completedDuration(undefined), null);
  assert.equal(completedDuration({ ...working, state: 'unknown' }), null);
  assert.equal(foregroundStart([{ ...working, state: 'unknown' }]), undefined);
  assert.equal(completedDuration({ ...working, state: 'completed', ended_at: 'invalid' }), null);
  assert.equal(completedDuration({ ...working, state: 'completed', ended_at: '2026-09-24T11:59:00Z' }), null);
});

test('autopilot user continuations retain the original foreground across prose segments', async () => {
  const { foregroundItems, foregroundStart, timingForTurn } = await import('../src/lib/transcript.ts');
  const items = [{ id: 'old-user', kind: 'user' }, { id: 'old-reply', kind: 'assistant' }, { id: 'user', kind: 'user' }, { id: 'progress', kind: 'assistant' }, { id: 'auto', kind: 'user', delivery: 'autopilot' }, { id: 'later', kind: 'assistant' }, { id: 'steer', kind: 'user', delivery: 'steer' }, { id: 'final', kind: 'assistant' }];
  const timing = { id: 'turn', user_item_id: 'user', state: 'working', started_at: '2026-09-24T12:00:00Z' };
  const foreground = foregroundItems(items);
  assert.deepEqual(foreground.map((item) => item.id), ['user', 'progress', 'auto', 'later', 'steer', 'final']);
  assert.equal(timingForTurn([timing], foreground[0].id), timing);
  assert.equal(foregroundStart([timing]), timing.started_at);
});

test('an empty response keeps its recorded duration after the next ordinary prompt arrives', async () => {
  const { showTurnEnd } = await import('../src/lib/transcript.ts');
  const cancelled = { id: 'turn1', user_item_id: 'user1', started_at: '2026-09-24T12:00:00Z', ended_at: '2026-09-24T12:00:02Z', state: 'cancelled' };
  const latestEmpty = { hasContent: false, boundary: true, last: true, live: false };
  assert.equal(showTurnEnd(cancelled, latestEmpty), true);
  assert.equal(showTurnEnd(cancelled, { ...latestEmpty, last: false, live: true }), true);
  assert.equal(showTurnEnd(cancelled, { ...latestEmpty, last: false, boundary: false }), false);
  assert.equal(showTurnEnd(undefined, latestEmpty), false);
  assert.equal(showTurnEnd({ ...cancelled, ended_at: undefined, state: 'working' }, { ...latestEmpty, live: true }), false);
});

test('links are the same objects while only text streams, and change when a tool call arrives', () => {
  const call = { id: 'c1', kind: 'tool', time: '2026-09-24T12:00:01Z', tool: tool('bash', '{"command":"ls"}') };
  const ask = { id: 'c2', kind: 'tool', time: '2026-09-24T12:00:02Z', tool: tool('ask_user', '{"question":"Which?"}') };
  const interactions = [
    { id: 'p1', kind: 'permission', title: 'Run', state: 'answered', time: '2026-09-24T12:00:01Z', tool_call_id: 'c1' },
    { id: 'q1', kind: 'question', title: 'Q', state: 'answered', time: '2026-09-24T12:00:02Z', questions: [{ text: 'Which?' }] },
  ];
  const reply = (text) => ({ id: 'm1', kind: 'assistant', time: '2026-09-24T12:00:03Z', text });
  const first = linkInteractions([call, ask, reply('a')], interactions);
  const again = linkInteractions([call, ask, reply('ab')], interactions);
  assert.equal(again, first);
  assert.equal(again.linked.get('c1'), first.linked.get('c1'));
  assert.deepEqual(first.linked.get('c2').map((ix) => ix.id), ['q1']);
  const more = linkInteractions([call, ask, reply('ab'), { id: 'c3', kind: 'tool', time: '2026-09-24T12:00:04Z', tool: tool('view', '{"path":"a"}') }], interactions);
  assert.notEqual(more, first);
  assert.deepEqual(more.linked.get('c1').map((ix) => ix.id), ['p1']);
  // New requests are new inputs.
  assert.notEqual(linkInteractions([call, ask, reply('ab')], [...interactions]), first);
});

test('a tool call input is parsed once however often its turn re-renders', () => {
  const items = [{ id: 'c1', kind: 'tool', time: 't', tool: tool('edit', '{"path":"a.go"}') }, { id: 'c2', kind: 'tool', time: 't', tool: tool('view', '{"path":"b.go"}') }];
  const parse = JSON.parse;
  let calls = 0;
  JSON.parse = (...args) => (calls++, parse(...args));
  try {
    const first = summarizeTools(items, false);
    const before = calls;
    for (let i = 0; i < 50; i++) assert.equal(summarizeTools(items, false), first);
    assert.equal(calls, before);
    assert.equal(first, 'Changed 1 file and read 1 file');
  } finally {
    JSON.parse = parse;
  }
});

const reasoning = (id, time, text = 'because') => ({ id, kind: 'reasoning', time, text });
const prose = (id, time, kind = 'assistant') => ({ id, kind, time, text: 'hello' });
const at = (s) => `2026-09-24T10:00:${String(s).padStart(2, '0')}Z`;

test('work between two messages is one segment; prose and notices stand alone', async () => {
  const { segmentActivity } = await import('../src/lib/transcript.ts');
  const decided = ix('p1', { time: at(3) });
  const question = q('q1', 'Which?', { time: at(6) });
  const entries = mergeByTime([prose('u1', at(0), 'user'), reasoning('r1', at(1)), item('c1', undefined, at(2)), item('c2', undefined, at(4)), prose('m1', at(5)), item('c3', undefined, at(7)), prose('n1', at(8), 'notice'), reasoning('r2', at(9))], [decided, question]);
  const segments = segmentActivity(entries);
  assert.deepEqual(segments.map((s) => [s.key, s.work, s.entries.map((e) => e.item?.id ?? e.interaction.id)]), [
    ['u1', false, ['u1']],
    ['r1', true, ['r1', 'c1', 'p1', 'c2']],
    ['m1', false, ['m1']],
    ['q1', true, ['q1', 'c3']],
    ['n1', false, ['n1']],
    ['r2', true, ['r2']],
  ]);
  // The caller can keep a tool call out of the run (a subagent row): it splits the run and stands alone.
  const split = segmentActivity(entries, (e) => e.item?.id !== 'c1' && (e.item?.kind === 'reasoning' || e.item?.kind === 'tool' || (e.interaction && e.interaction.kind === 'permission')));
  assert.deepEqual(split.slice(1, 4).map((s) => [s.key, s.work]), [['r1', true], ['c1', false], ['p1', true]]);
});

test('a question that no longer waits is work: it joins its run and merges the runs around it', async () => {
  const { isWork, segmentActivity } = await import('../src/lib/transcript.ts');
  // Copilot's ask_user calls are tool items; other providers' questions are interactions.
  const items = [prose('u1', at(0), 'user'), reasoning('r1', at(1)), item('c1', undefined, at(2)), asked('a1', 'Which?', at(3)), item('c2', undefined, at(4)), asked('a2', 'Then?', at(5)), reasoning('r2', at(7)), prose('m1', at(9))];
  const opencode = q('q1', 'And?', { time: at(6) });
  const segments = segmentActivity(mergeByTime(items, [opencode]));
  assert.deepEqual(segments.map((s) => [s.key, s.work, s.entries.map((e) => e.item?.id ?? e.interaction.id)]), [
    ['u1', false, ['u1']],
    ['r1', true, ['r1', 'c1', 'a1', 'c2', 'a2', 'q1', 'r2']],
    ['m1', false, ['m1']],
  ]);
  for (const state of ['answered', 'rejected', 'expired']) assert.equal(isWork({ interaction: q('q1', 'And?', { state }) }), true);
  // A question still waiting is the action card, not work.
  assert.equal(isWork({ interaction: q('q1', 'And?', { state: 'pending' }) }), false);
});

test('segment keys hold while a run grows, so the row keeps its state as items stream in', async () => {
  const { segmentActivity } = await import('../src/lib/transcript.ts');
  const items = [prose('u1', at(0), 'user'), reasoning('r1', at(1))];
  const keys = () => segmentActivity(mergeByTime(items, [])).map((s) => `${s.key}:${s.entries.length}`);
  assert.deepEqual(keys(), ['u1:1', 'r1:1']);
  items.push(item('c1', undefined, at(2)));
  items.push(item('c2', undefined, at(3)));
  assert.deepEqual(keys(), ['u1:1', 'r1:3']);
  items.push(prose('m1', at(4)));
  items.push(reasoning('r2', at(5)));
  assert.deepEqual(keys(), ['u1:1', 'r1:3', 'm1:1', 'r2:1']);
  // An empty reasoning item is not drawn but still anchors its run, so the key does not move once its text arrives.
  items.push(prose('m2', at(6)), reasoning('r3', at(7), ''));
  assert.equal(keys().at(-1), 'r3:1');
  items[items.length - 1] = reasoning('r3', at(7), 'now with text');
  items.push(item('c3', undefined, at(8)));
  assert.equal(keys().at(-1), 'r3:2');
});

test('the activity label counts thoughts and what the tools did, with a duration once the run ended', async () => {
  const { summarizeActivity } = await import('../src/lib/transcript.ts');
  const run = (id, t) => ({ item: item(id, undefined, at(2), t) });
  const entries = [
    { item: reasoning('r1', at(1)) }, { item: reasoning('r2', at(1)) }, { item: reasoning('r3', at(1)) }, { item: reasoning('r4', at(1)) },
    run('c1', tool('bash', '{"command":"ls"}')), run('c2', tool('bash', '{"command":"ls"}')), run('c3', tool('bash', '{"command":"ls"}')), run('c4', tool('bash', '{"command":"ls"}')),
    run('c5', tool('view', '{"path":"a.ts"}')), run('c6', tool('read', '{"path":"b.ts"}')),
  ];
  assert.deepEqual(summarizeActivity(entries, { live: false, endedAt: '2026-09-24T10:01:05Z' }), { label: 'Thought 4×, ran 4 commands and read 2 files · 1m 4s', tone: 'muted', active: false });
  assert.equal(summarizeActivity([{ item: reasoning('r1', at(1)) }], { live: false, endedAt: at(1) }).label, 'Thought · <1s');
  assert.equal(summarizeActivity([{ item: reasoning('r1', at(1)) }], { live: true }).label, 'Thought');
  assert.equal(summarizeActivity([{ item: reasoning('r1', at(1)) }, { interaction: ix('p1') }, { interaction: ix('p2') }], { live: false }).label, 'Thought and decided 2 requests');
  assert.equal(summarizeActivity([run('c1', tool('edit', '{"path":"a.ts"}')), { item: { ...item('c2', undefined, at(2), tool('view', '{"path":"x.png"}')), images: [{ id: 'i1' }, { id: 'i2' }] } }], { live: false }).label, 'Changed 1 file and read 1 file · 2 images');
  // Nothing drawn, nothing said: the row is not shown.
  assert.equal(summarizeActivity([{ item: reasoning('r1', at(1), '') }], { live: false }).label, '');
});

test('the activity label keeps what needs attention: the running call, a waiting permission, failures, thinking', async () => {
  const { summarizeActivity } = await import('../src/lib/transcript.ts');
  const running = item('c2', undefined, at(2), tool('bash', '{"command":"npm test"}', { status: 'running' }));
  const entries = [{ item: reasoning('r1', at(1)) }, { item: item('c1', undefined, at(2)) }, { item: running }];
  const live = summarizeActivity(entries, { live: true, endedAt: at(9) });
  assert.deepEqual(live, { label: 'Thought and ran 1 command · Running: bash npm test', tone: 'muted', active: true });
  const approvals = new Map([['c2', [ix('p1', { tool_call_id: 'c2', state: 'pending', resolution: undefined })]]]);
  const waiting = summarizeActivity(entries, { live: true, approvals });
  assert.deepEqual(waiting, { label: 'Thought and ran 1 command · Waiting for your approval: bash npm test', tone: 'attention', active: true });
  // A request yolo mode is answering does not wait for the user.
  const auto = new Map([['c2', [ix('p1', { tool_call_id: 'c2', state: 'pending', resolution: undefined, auto: true })]]]);
  assert.deepEqual(summarizeActivity(entries, { live: true, approvals: auto }), live);
  const { awaitsUser } = await import('../src/lib/transcript.ts');
  assert.deepEqual([ix('p', { state: 'pending' }), ix('p', { state: 'pending', auto: true }), ix('p', { auto: true })].map(awaitsUser), [true, false, false]);
  // The turn ended before the call reported: no longer active, its duration known.
  assert.deepEqual(summarizeActivity(entries, { live: false, endedAt: at(9) }), { label: 'Thought and ran 1 command · 1 without a result · 8s', tone: 'muted', active: false });
  const failed = [{ item: item('c1', undefined, at(2), tool('bash', '{"command":"ls"}', { status: 'failed' })) }, { item: item('c3', undefined, at(2)) }];
  assert.deepEqual(summarizeActivity(failed, { live: false }), { label: 'Ran 1 command · 1 failed', tone: 'error', active: false });
  // Failure outranks a waiting permission in the tone; the label keeps both.
  assert.equal(summarizeActivity([...failed, { item: running }], { live: true, approvals }).tone, 'error');
  // Thinking that still streams is named, not counted, and holds the duration back.
  const thinking = summarizeActivity([{ item: reasoning('r1', at(1)) }, { item: reasoning('r2', at(3)) }], { live: true, streamingId: 'r2', endedAt: at(9) });
  assert.deepEqual(thinking, { label: 'Thought · Thinking…', tone: 'muted', active: true });
  assert.equal(summarizeActivity([{ item: reasoning('r2', at(3), '') }], { live: true, streamingId: 'r2' }).label, 'Thinking…');
});

test('the activity label counts questions by outcome, never as tool calls', async () => {
  const { summarizeActivity } = await import('../src/lib/transcript.ts');
  const run = [{ item: item('c1', undefined, at(2)) }, { item: asked('a1', 'Which?', at(3)) }, { item: item('c2', undefined, at(4)) }, { interaction: q('q1', 'Then?', { time: at(5), resolution: 'Answered: red' }) }];
  assert.deepEqual(summarizeActivity(run, { live: false, endedAt: at(9) }), { label: 'Ran 2 commands and answered 2 questions · 7s', tone: 'muted', active: false });
  const declined = asked('a2', 'Which?', at(3), { output: `User responded: ${DECLINED_OUTPUT}` });
  assert.equal(summarizeActivity([{ item: asked('a1', 'Which?', at(3)) }, { item: declined }, { interaction: q('q1', 'Then?', { state: 'rejected' }) }], { live: false }).label, 'Answered 1 question and declined 2 questions');
  // A call left open by a stopped turn got no answer; an expired request neither.
  const open = asked('a3', 'Which?', at(3), { status: 'running', output: undefined });
  assert.equal(summarizeActivity([{ item: open }, { interaction: q('q1', 'Then?', { state: 'expired' }) }], { live: false }).label, '2 questions unanswered');
  // A failed question counts with the failures and turns the row to error.
  const failed = asked('a4', 'Which?', at(3), { status: 'failed', output: 'boom' });
  assert.deepEqual(summarizeActivity([{ item: item('c1', undefined, at(2)) }, { item: failed }], { live: false }), { label: 'Ran 1 command · 1 failed', tone: 'error', active: false });
  // The question request on the call is the one read: declined there means declined.
  const approvals = new Map([['a1', [q('q1', 'Which?', { state: 'rejected', tool_call_id: 'a1' })]]]);
  assert.equal(summarizeActivity([{ item: asked('a1', 'Which?', at(3), { output: '' }) }], { live: false, approvals }).label, 'Declined 1 question');
  // A question still waiting stays the running call.
  const waiting = asked('a5', 'Which?', at(3), { status: 'running', output: undefined });
  assert.deepEqual(summarizeActivity([{ item: item('c1', undefined, at(2)) }, { item: waiting }], { live: true }), { label: 'Ran 1 command · Running: ask_user Which?', tone: 'muted', active: true });
  // Its pending question request waits for an answer, not an approval.
  const pending = new Map([['a5', [q('q1', 'Which?', { state: 'pending', tool_call_id: 'a5' })]]]);
  assert.deepEqual(summarizeActivity([{ item: waiting }], { live: true, approvals: pending }), { label: 'Waiting for your answer: ask_user Which?', tone: 'attention', active: true });
});
