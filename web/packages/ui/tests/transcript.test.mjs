import assert from 'node:assert/strict';
import test from 'node:test';
import { DECLINED_OUTPUT, approvalMark, callProduct, firstLine, foldWindow, isFileDeclaration, linkInteractions, mainArgument, mergeByTime, promoted, questionOf, readableInput, subagentSummary, summarizeTurn, toolLabel, transcriptWindowStart, windowInteractions } from '../src/lib/transcript.ts';

test('a recorded file declaration folds into activity without a separate file card', () => {
  const declaration = { artifact_id: 'artifact-1', path: '/repo/README' };
  const call = { id: 'call-1', agent_id: 'child-1', kind: 'tool', time: '2026-09-27T10:00:00Z', tool: { name: 'uam_show_file', status: 'completed', declaration } };
  assert.equal(isFileDeclaration(call), true);
  assert.equal(callProduct(call), null);
  assert.equal(promoted({ item: call }, { live: false, approvals: new Map() }, item => callProduct(item) === 'chart'), false);
  // The call itself is counted on the turn line (and drawn in its timeline) like any other tool.
  assert.deepEqual(summarizeTurn([{ item: call }], { live: false }).parts.map(p => p.text), ['1 tool']);
  assert.equal(call.agent_id, 'child-1');
  for (const tool of [
    { ...call.tool, status: 'running' },
    { ...call.tool, status: 'failed' },
    { ...call.tool, declaration: undefined },
    { ...call.tool, declaration: { ...declaration, path: '' } },
    { ...call.tool, declaration: { ...declaration, path: 'README' } },
    { ...call.tool, name: 'foreign_tool' },
  ]) {
    assert.equal(isFileDeclaration({ ...call, tool }), false);
    assert.equal(callProduct({ ...call, tool }), null);
  }
});

test('a call that returned images folds; only its images stand in the answer', () => {
  const view = { id: 'v1', kind: 'tool', time: '2026-09-27T10:00:00Z', tool: { name: 'view', status: 'completed', input: '{"path":"shot.png"}' } };
  const shot = { ...view, images: [{ id: 'img-1', mime: 'image/png', size: 10 }] };
  assert.equal(callProduct(shot), 'images');
  assert.equal(callProduct({ ...view, images_note: '1 image was left out' }), 'images');
  assert.equal(callProduct(view), null);
  assert.equal(callProduct({ id: 'm1', kind: 'assistant', time: view.time, text: 'Done.', images: shot.images }), null);
  assert.equal(promoted({ item: shot }, { live: false }), true);
  assert.deepEqual(summarizeTurn([{ item: shot }], { live: false }).parts.map(p => p.text), ['1 file read', '1 image']);
});

test('recent history starts at a whole turn and paging reveals every retained item', () => {
  const items = Array.from({ length: 345 }, (_, index) => ({ id: String(index), kind: index % 30 === 0 ? 'user' : 'assistant' }));
  items[240].delivery = 'steer';
  assert.equal(transcriptWindowStart(items), 210);
  assert.equal(transcriptWindowStart(items, 210), 90);
  assert.equal(transcriptWindowStart(items, 90), 0);
  assert.equal(transcriptWindowStart([]), 0);
  assert.equal(transcriptWindowStart(items.slice(0, 60)), 0);
  // A long single turn keeps its complete summary and timing.
  assert.equal(transcriptWindowStart(items.map((it, i) => ({ ...it, kind: i === 0 ? 'user' : 'tool' }))), 0);
});

test('older interactions stay with their hidden rows, including decisions made later', () => {
  const items = [{ id: 'old', time: '01' }, { id: 'new', time: '10' }];
  const interactions = [
    { id: 'old-decision', tool_call_id: 'old', time: '12' },
    { id: 'new-decision', tool_call_id: 'new', time: '09' },
    { id: 'old-loose', time: '02' },
    { id: 'new-loose', time: '11' },
  ];
  assert.deepEqual(windowInteractions(items, interactions, 1).map((ix) => ix.id), ['new-decision', 'new-loose']);
  assert.equal(windowInteractions(items, interactions, 0), interactions);
  assert.deepEqual(windowInteractions(items.slice(1), interactions, 0, true).map((ix) => ix.id), ['new-decision', 'new-loose']);
});

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

test('an input that is one JSON string shows decoded; a patch shows the files it names', () => {
  const patch = JSON.stringify('*** Begin Patch\n*** Add File: web/report.html\n+<!doctype html>\n*** Update File: README.md\n@@\n-a\n+b\n*** End Patch\n');
  assert.equal(mainArgument('apply_patch', patch), 'web/report.html, README.md');
  assert.equal(mainArgument('note', JSON.stringify('first line\n"quoted"')), 'first line "quoted"');
});

test('the details and the copy action read the input as its command, or decoded', () => {
  assert.deepEqual(readableInput('bash', '{"command":"rtk pwd && rtk ls","description":"Look"}'), { kind: 'command', text: 'rtk pwd && rtk ls' });
  assert.deepEqual(readableInput('bash', 'go test ./...'), { kind: 'command', text: 'go test ./...' });
  assert.deepEqual(readableInput('bash', '{"timeout":5}'), { kind: 'input', text: '{"timeout":5}' });
  assert.deepEqual(readableInput('apply_patch', JSON.stringify('*** Begin Patch\n+<a>\n')), { kind: 'input', text: '*** Begin Patch\n+<a>\n' });
  assert.deepEqual(readableInput('view', '{"path":"a.go"}'), { kind: 'input', text: '{"path":"a.go"}' });
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
  // Linked to a tool part by tool_call_id, it sits on that row like any request.
  const { linked, questions } = linkInteractions([item('prt_q', undefined, '', tool('question', '{}'))], [{ ...two, tool_call_id: 'prt_q' }]);
  assert.equal(linked.get('prt_q')[0].id, 'que_1');
  assert.deepEqual(questions, []);
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


test('completed duration excludes recorded time waiting for user input', async () => {
  const { completedDuration } = await import('../src/lib/transcript.ts');
  const timing = { id: 'turn', state: 'completed', started_at: '2026-09-28T12:00:00Z', ended_at: '2026-09-28T12:01:10Z', paused_ms: 60_000 };
  assert.equal(completedDuration(timing), '10s');
});

test('a turn and its thoughts are timed alike, so a part never reads longer than its whole', async () => {
  const { completedDuration, formatMs } = await import('../src/lib/transcript.ts');
  const at = (ms) => new Date(Date.parse('2026-10-06T00:41:32Z') + ms).toISOString();
  // The turn ran 1.73s and its thought 1.62s inside it.
  const turn = { id: 'turn', user_item_id: 'u', state: 'completed', started_at: at(251), ended_at: at(1981) };
  const reasoning = { id: 'r', kind: 'reasoning', time: at(336), ended_at: at(1956), text: 'Thinking' };
  assert.equal(completedDuration(turn), '2s');
  assert.equal(summarizeTurn([{ item: reasoning }], { live: false }).parts[0].text, '1 thought (2s)');
  // Past a minute both keep the seconds, and past an hour both read hours and minutes.
  assert.equal(completedDuration({ ...turn, ended_at: at(251 + 64_400) }), '1m 4s');
  assert.equal(formatMs(64_400), '1m 4s');
  assert.equal(completedDuration({ ...turn, ended_at: at(251 + 4_530_000) }), '1h 15m');
  assert.equal(formatMs(4_530_000), '1h 15m');
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

test('a question input is parsed once however often its turn re-renders', () => {
  const call = tool('ask_user', '{"question":"Which file?","choices":["a.go","b.go"]}');
  const parse = JSON.parse;
  let calls = 0;
  JSON.parse = (...args) => (calls++, parse(...args));
  try {
    const first = questionOf(call, undefined, false);
    const before = calls;
    for (let i = 0; i < 50; i++) assert.deepEqual(questionOf(call, undefined, false), first);
    assert.equal(calls, before);
    assert.deepEqual(first.questions, [{ text: 'Which file?', choices: ['a.go', 'b.go'] }]);
  } finally {
    JSON.parse = parse;
  }
});

test('a settled question belongs to the timeline; a pending question stays an action card', async () => {
  const { isWork } = await import('../src/lib/transcript.ts');
  for (const state of ['answered', 'rejected', 'expired']) assert.equal(isWork({ interaction: q('q1', 'And?', { state }) }), true);
  // A question still waiting is the action card, not work.
  assert.equal(isWork({ interaction: q('q1', 'And?', { state: 'pending' }) }), false);
});

test('completed edit, write and create calls are counted for the live Changes count; running, failed and other tools are not', async () => {
  const { completedChanges } = await import('../src/lib/transcript.ts');
  const t = (name, status) => ({ id: `${name}-${status}`, kind: 'tool', time: '2026-09-25T00:00:00Z', tool: { name, status, input: '{"path":"a.go"}' } });
  assert.equal(completedChanges([t('edit', 'completed'), t('Write', 'completed'), t('create', 'completed'), t('edit', 'running'), t('write', 'failed'), t('bash', 'completed'), t('read', 'completed'), { id: 'u', kind: 'user', time: '', text: 'edit' }]), 3);
  assert.equal(completedChanges([]), 0);
});

test('the first line of a report reads as plain text, whatever markdown it wore', () => {
  assert.equal(firstLine('## Heading order\n\nBody'), 'Body');
  assert.equal(firstLine('## Only a heading\n'), 'Only a heading');
  assert.equal(firstLine('\n\n- **Fixed** the `alt` text in [post.html](templates/post.html).\n- More'), 'Fixed the alt text in post.html.');
  assert.equal(firstLine('```sh\nnpm test\n```\n> _Quoted_ line with ~~struck~~ words'), 'Quoted line with struck words');
  assert.equal(firstLine('---\n1) `parent_tool_call_id` stays snake_case, *and* so does __this__'), 'parent_tool_call_id stays snake_case, and so does this');
  assert.equal(firstLine('   \n\n'), '');
  assert.equal(firstLine(undefined), '');
});

test('a subagent row summarises its current step, its error, or the first line of its report', () => {
  const running = { id: 'a1', name: 'Survey', status: 'running' };
  const shell = { id: 's1', kind: 'tool', time: 't', tool: tool('bash', '{"command":"npx html-validate templates/"}', { status: 'running' }) };
  assert.equal(subagentSummary(running, undefined, undefined), '');
  assert.equal(subagentSummary(running, [], undefined), '');
  assert.equal(subagentSummary(running, [shell], undefined), 'Running: bash npx html-validate templates/');
  assert.equal(subagentSummary(running, [shell, { id: 's2', kind: 'reasoning', time: 't', text: 'Grep first.' }], undefined), 'Thinking…');
  assert.equal(subagentSummary(running, [shell, { id: 's3', kind: 'assistant', time: 't', text: 'Done.' }], undefined), '');
  // The report is the parent `task` call's output; the row never reads the subagent's own items for it.
  const report = { name: 'task', status: 'completed', output: '**Two templates changed.**\n\n- `templates/post.html`' };
  assert.equal(subagentSummary({ ...running, status: 'completed' }, [shell], report), 'Two templates changed.');
  assert.equal(subagentSummary({ ...running, status: 'idle' }, [shell], report), 'Two templates changed.');
  assert.equal(subagentSummary({ ...running, status: 'cancelled' }, [shell], undefined), '');
  assert.equal(subagentSummary({ ...running, status: 'cancelled' }, [shell], { ...report, output: 'Stopped after step 2.' }), 'Stopped after step 2.');
  assert.equal(subagentSummary({ ...running, status: 'failed', error: 'axe-core is not installed.' }, [shell], { ...report, status: 'failed', output: 'other' }), 'axe-core is not installed.');
  assert.equal(subagentSummary({ ...running, status: 'failed' }, [shell], { ...report, status: 'failed', output: 'npm ERR! could not determine executable to run\nstack…' }), 'npm ERR! could not determine executable to run');
  // A background launch's call output only acknowledges the launch: never its line.
  const ack = { ...report, output: 'Agent started in background with agent_id: a1.' };
  assert.equal(subagentSummary({ ...running, status: 'completed', background: true }, [shell], ack), '');
  assert.equal(subagentSummary({ ...running, status: 'completed', background: true, result_summary: 'Found the pages.' }, [shell], ack), 'Found the pages.');
});

test('compact tool labels and exact semantic paths preserve summaries without deferred input', () => {
  const a = { id: 'a', kind: 'tool', tool: { name: 'edit', status: 'completed', path: '/long/exact/path.ts', display_arg: '/long/exact/…', has_input: true, has_output: true } };
  const b = { ...a, id: 'b', tool: { ...a.tool, display_arg: 'a different clipped label' } };
  assert.deepEqual(toolLabel(a.tool), { name: 'edit', arg: '/long/exact/…' });
  assert.equal(summarizeTurn([a, b].map(item => ({ item })), { live: false }).label, '1 file changed');
  assert.equal(subagentSummary({ status: 'running', preview: 'Reviewing parser behavior' }, undefined, undefined), 'Reviewing parser behavior');
  assert.equal(subagentSummary({ status: 'completed', result_summary: 'Found two edge cases' }, undefined, undefined), 'Found two edge cases');
});

test('compact completed subagent previews preserve the meaningful plain report line', () => {
  const result_summary = '## Summary\n\n**Two** templates changed.\nNext steps follow.';
  for (const status of ['completed', 'idle', 'failed']) {
    assert.equal(subagentSummary({ status, result_summary }, undefined, undefined), 'Two templates changed.');
  }
});


test('a done subagent\'s line is its own result, never a generated summary an older service sent', () => {
  const child = { summary: 'Verified the cache stays bounded.', result_summary: '## Report\nExisting report line.', preview: 'Checking file references', error: 'The check failed.' };
  for (const status of ['completed', 'idle']) {
    assert.equal(subagentSummary({ ...child, status }, undefined, undefined), 'Existing report line.');
  }
  assert.equal(subagentSummary({ ...child, status: 'running' }, undefined, undefined), 'Checking file references');
  assert.equal(subagentSummary({ ...child, status: 'failed' }, undefined, undefined), 'The check failed.');
  assert.equal(subagentSummary({ ...child, status: 'cancelled' }, undefined, undefined), 'Existing report line.');
});

test('a cancelled elicitation counts and marks as cancelled, not declined', () => {
  const form = (id, resolution) => ({ id, kind: 'question', title: 'Form from release-mcp', state: 'rejected', resolution, time: '2026-10-09T10:00:05Z', elicitation: { mode: 'form' }, questions: [{ text: 'Channel', custom: true }] });
  assert.equal(questionOf(undefined, form('c', 'cancelled'), false).outcome, 'cancelled');
  assert.equal(questionOf(undefined, form('d', 'declined'), false).outcome, 'declined');
  assert.equal(approvalMark(form('c', 'cancelled')).word, 'cancelled');
  assert.deepEqual(summarizeTurn([{ interaction: form('c', 'cancelled') }, { interaction: form('d', 'declined') }], { live: false }).parts.map((p) => p.text), ['1 question declined', '1 question cancelled']);
});
