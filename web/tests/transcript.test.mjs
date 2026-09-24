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
  assert.deepEqual(toolLabel(tool('edit', undefined, { title: 'Edit cmd/doctor.go' })), { name: 'edit', arg: 'Edit cmd/doctor.go' });
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
