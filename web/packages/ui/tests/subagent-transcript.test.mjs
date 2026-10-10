import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import test from 'node:test';
import { runInNewContext } from 'node:vm';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import ts from 'typescript';
import * as chart from '../src/lib/chart.ts';
import * as transcript from '../src/lib/transcript.ts';
import * as history from '../src/lib/historyState.ts';
import * as subagentsLib from '../src/lib/subagents.ts';
import * as receiptsLib from '../src/lib/receipts.ts';

// Render the real transcript component with local UI shells and no browser or data reads.
const require = createRequire(import.meta.url);
const source = await readFile(new URL('../src/components/Transcript.tsx', import.meta.url), 'utf8');
const exports = {};
let expanded = false;
// What the Task's subagent scope would hand the transcript; unset, the transcript works the replies out itself.
let scopeReplies;
let liveIds;
const disclosureValues = new Map();
const disclosureKeys = [];
const shell = ({ children, render }) => render ? React.cloneElement(render, {}, children) : children;
const menu = { Root: shell, Trigger: shell, Content: shell, Actions: () => null };
const element = tag => ({ children }) => React.createElement(tag, null, children);
const modules = {
  './Details': { DetailVisibility: shell, useDetailVisibility: () => true, BodyNotice: () => null, useBodyCopy: () => ({}), useDisclosure: key => { disclosureKeys.push(key); return React.useState(disclosureValues.get(key) ?? expanded); }, useItemBody: item => ({ item }) },
  '../api': { modelName: (_meta, _provider, model) => model },
  '../lib/chart': chart,
  './Chart': { ChartCard: () => null },
  '../lib/clipboard': { useCopied: () => [false, () => {}] },
  '../lib/cn': { cn: (...values) => values.filter(value => typeof value === 'string').join(' ') },
  '../lib/transcript': transcript,
  '../lib/cost': { compactTokens: (n) => String(n) },
  '../lib/historyState': history,
  '../lib/subagents': subagentsLib,
  '../lib/receipts': receiptsLib,
  './Receipts': { Receipts: ({ stamps }) => React.createElement('ul', { 'aria-label': 'Receipts' }, stamps.map((st) => React.createElement('li', { key: `${st.kind}:${st.claim}` }, `${st.verdict} ${st.claim}`))) },
  // The subagent UI itself needs its Task scope; here it only reports what the transcript hands it.
  './Subagents': {
    SubagentChip: ({ subagents, calls, tones, open }) => React.createElement('button', { 'aria-expanded': String(open), 'data-tones': subagents.map((s) => tones.get(s.id) ?? '-').join(',') }, `CHIP ${subagents.map((s) => s.name).join(',')} OF ${calls}`),
    SubagentList: ({ id, subagents, calls = subagents.length }) => React.createElement('ul', { id }, `LIST OF ${calls}`, subagents.map((s) => React.createElement('li', { key: s.id, id: `item-${s.parent_tool_call_id}` }, `LISTED ${s.name}`))),
    SubagentRow: ({ subagent, anchor }) => React.createElement('div', { id: anchor ? `item-${subagent.parent_tool_call_id}` : undefined }, `ROW ${subagent.name}`),
    LiveSubagents: () => React.createElement('section', null, 'LIVE'),
    useSubagentDisclosure: (key) => { disclosureKeys.push(key); return React.useState(disclosureValues.get(key) ?? expanded); },
    useSubagentReplies: () => scopeReplies,
    useLiveSubagentIds: () => liveIds,
  },
  '../lib/verbs': { turnVerb: () => 'Working' },
  './Attachments': { ImageThumbs: () => null, ItemAttachments: () => null },
  './common': { CodeBlock: element('pre'), Markdown: ({ text }) => React.createElement('p', null, text), SessionContext: React.createContext(''), WorkdirContext: React.createContext(''), Spinner: () => null, Dot: () => null, SubagentIdleIcon: () => null, WorkingMark: () => null, useApp: () => ({ meta: null }), clockTime: (at) => new Date(at).toISOString().slice(11, 19), dateTime: (at) => new Date(at).toISOString() },
  './Interactions': { DecidedRow: ({ interaction }) => React.createElement('p', null, interaction.id) },
  './LiveOutput': { LiveOutput: () => null },
  './Todos': { TurnTodo: () => null },
  './TurnWaybill': { TurnWaybill: () => null },
  './VisualBoundary': { VisualBoundary: ({ children }) => children },
  './Plan': { PlanNotice: () => null },
  './ui/button': { Button: element('button') },
  './ui/chip': { Chip: element('span') },
  './ui/collapse': { Collapse: ({ open, children }) => open ? children : null, usePresence: open => ({ mounted: open, onClosed: () => {} }) },
  './ui/menu': { ContextMenu: menu, Menu: menu },
  './ui/tooltip': { Tip: shell },
};
runInNewContext(ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX, target: ts.ScriptTarget.ES2022 } }).outputText, {
  exports, require: name => modules[name] ?? require(name), sessionStorage: { getItem: () => null },
});

test('subagent compact density uses turn heads, follow-up boundaries and lazy timelines', () => {
  const item = (id, kind, rest = {}) => ({ id, kind, agent_id: 'child', time: `2026-09-26T12:00:0${id.slice(-1)}Z`, ...rest });
  const items = [
    item('user1', 'user', { text: 'First request' }),
    item('think2', 'reasoning', { text: 'Private thought body' }),
    item('tool3', 'tool', { tool: { name: 'bash', input: 'PRIVATE_COMMAND', status: 'completed' } }),
    item('answer4', 'assistant', { text: 'First answer' }),
    item('user5', 'user', { text: 'Follow-up request' }),
    item('tool6', 'tool', { tool: { name: 'view', input: 'PRIVATE_PATH', status: 'completed' } }),
    item('answer7', 'assistant', { text: 'Follow-up answer' }),
  ];
  const question = (id, text, agent_id) => ({ id, kind: 'question', state: 'answered', agent_id, time: '2026-09-26T12:00:06Z', questions: [{ text, choices: [], custom: true }], resolution: 'Answered' });
  const interactions = [question('own-question', 'Child question', 'child'), question('parent-question', 'Parent question', undefined), question('other-question', 'Other child question', 'sibling')];
  const props = { sessionId: 'task', provider: 'copilot', workdir: '/project', agentId: 'child', items, interactions, live: false };
  const draw = density => renderToStaticMarkup(React.createElement(exports.AgentItems, { ...props, density }));
  const compact = draw('compact');
  assert.equal((compact.match(/activity of this turn/g) ?? []).length, 2);
  assert.doesNotMatch(compact, /data-activity|PRIVATE_COMMAND|PRIVATE_PATH|Private thought body|Took /);
  assert.match(compact, /First answer/);
  assert.match(compact, /Follow-up request/);
  assert.match(compact, /Follow-up answer/);
  assert.match(compact, /Child question/);
  assert.doesNotMatch(compact, /Parent question|Other child question/);
  assert.match(draw('detailed'), /data-activity/);
  expanded = true;
  try {
    assert.match(draw('compact'), /PRIVATE_COMMAND/);
  } finally {
    expanded = false;
  }
  assert.ok(items.every(entry => entry.agent_id === 'child'));
  const running = { ...items[5], tool: { ...items[5].tool, status: 'running' } };
  const live = renderToStaticMarkup(React.createElement(exports.AgentItems, { ...props, items: [...items.slice(0, 5), running], live: true, density: 'compact' }));
  // The running call is the panel's live step: its tool row, unfolded at the foot.
  assert.match(live, /animate-rise[\s\S]*>view<[\s\S]*, running/);
  assert.doesNotMatch(live, /Busy for|Took /);
  const main = renderToStaticMarkup(React.createElement(exports.Transcript, { ...props, agentId: undefined, items: [], subagents: [], working: false, density: 'compact' }));
  assert.match(main, /Parent question/);
  assert.doesNotMatch(main, /Child question|Other child question/);
});

test('a compact turn line with nothing counted yet draws no bare chevron; the foot names a call waiting for permission', () => {
  const items = [
    { id: 'u1', kind: 'user', time: '2026-09-26T12:00:01Z', text: 'Clean up' },
    { id: 'c2', kind: 'tool', time: '2026-09-26T12:00:02Z', tool: { name: 'bash', input: '{"command":"rm -rf build"}', status: 'running' } },
  ];
  const waiting = { id: 'p1', kind: 'permission', state: 'pending', tool_call_id: 'c2', title: 'Run shell command', time: '2026-09-26T12:00:03Z' };
  const draw = interactions => renderToStaticMarkup(React.createElement(exports.Transcript, { sessionId: 'task', provider: 'copilot', workdir: '/project', items, interactions, subagents: [], live: true, working: true, density: 'compact', footVerb: false }));
  const pending = draw([waiting]);
  assert.doesNotMatch(pending, /activity of this turn/);
  assert.match(pending, /Waiting for your approval: bash rm -rf build/);
  // Running, the call is the live step at the foot, and the line still waits for its first count.
  const running = draw([]);
  assert.doesNotMatch(running, /activity of this turn/);
  assert.match(running, /animate-rise[\s\S]*>bash<[\s\S]*, running/);
});

test('same-ID main and child turns keep disclosure state and DOM targets independent', () => {
  const mainItems = [
    { id: 'request', kind: 'user', text: 'Request', time: '2026-09-26T12:00:00Z' },
    { id: 'shared', kind: 'reasoning', text: 'Private thought', time: '2026-09-26T12:00:01Z' },
    { id: 'answer', kind: 'assistant', text: 'Answer', time: '2026-09-26T12:00:02Z' },
  ];
  const props = { sessionId: 'task', provider: 'copilot', workdir: '/project', interactions: [], live: false, working: false, density: 'compact', subagents: [] };
  const draw = child => renderToStaticMarkup(React.createElement(exports.Transcript, { ...props, agentId: child ? 'child' : undefined, items: child ? mainItems.map(item => ({ ...item, agent_id: 'child' })) : mainItems }));
  disclosureValues.set('turn:shared', true);
  disclosureKeys.length = 0;
  try {
    const main = draw(false);
    const mainKey = disclosureKeys[0];
    disclosureKeys.length = 0;
    const child = draw(true);
    const childKey = disclosureKeys[0];
    assert.equal(mainKey, 'turn:shared');
    assert.notEqual(childKey, mainKey);
    assert.match(main, /aria-expanded="true"/);
    assert.match(child, /aria-expanded="false"/);
    assert.match(main, /id="turn-shared-timeline"/);
    assert.doesNotMatch(child, /id="[^"]*-timeline"/);
    disclosureValues.set(childKey, true);
    const openChild = draw(true);
    const target = markup => /aria-controls="([^"]+)"/.exec(markup)?.[1];
    assert.equal(target(main), 'turn-shared-timeline');
    assert.notEqual(target(openChild), target(main));
    assert.ok(openChild.includes(`id="${target(openChild)}"`));
    const ids = [...(main + openChild).matchAll(/\sid="([^"]+)"/g)].map(match => match[1]);
    assert.equal(new Set(ids).size, ids.length);
    // Scroll restoration remains keyed to the original item/group identity.
    assert.match(main, /data-history-anchor="turn-head-shared"/);
    assert.match(openChild, /data-history-anchor="turn-head-shared"/);
  } finally {
    disclosureValues.clear();
    disclosureKeys.length = 0;
  }
});

test('subagents leave the answer: a chip on the turn line (Compact), rows in the activity (Detailed), the live card at the foot', () => {
  const task = (id, description) => ({ id, kind: 'tool', time: `2026-09-26T12:00:0${id.slice(-1)}Z`, tool: { name: 'task', input: JSON.stringify({ description }), status: id === 'call2' ? 'running' : 'completed' } });
  const items = [
    { id: 'request1', kind: 'user', text: 'Request', time: '2026-09-26T12:00:01Z' },
    task('call2', 'RUNNING_AGENT'),
    task('call3', 'SETTLED_AGENT'),
    { id: 'answer4', kind: 'assistant', text: 'Answer', time: '2026-09-26T12:00:04Z' },
  ];
  const subagents = [
    { id: 'a2', name: 'RUNNING_AGENT', status: 'running', parent_tool_call_id: 'call2' },
    { id: 'a3', name: 'SETTLED_AGENT', status: 'completed', parent_tool_call_id: 'call3' },
  ];
  const draw = (density, liveCard = true) => renderToStaticMarkup(React.createElement(exports.Transcript, { sessionId: 'task', provider: 'copilot', workdir: '/project', items, interactions: [], subagents, live: true, working: true, density, liveCard }));
  const compact = draw('compact');
  // One chip for the reply, in spawn order with identity tones; no row at the running call, no count on the turn line.
  assert.match(compact, /data-tones="violet,pink"[^>]*>CHIP RUNNING_AGENT,SETTLED_AGENT OF 2</);
  assert.doesNotMatch(compact, /ROW |LISTED |subagent/);
  // The live card sits after the reply's answer, before the foot.
  assert.match(compact, /Answer[\s\S]*LIVE/);
  assert.doesNotMatch(draw('compact', false), /LIVE</);
  // Its open state is remembered per reply, like the turn line's.
  assert.ok(disclosureKeys.includes('subagents:call2'));
  expanded = true;
  try {
    const open = draw('compact');
    assert.match(open, /LISTED RUNNING_AGENT[\s\S]*LISTED SETTLED_AGENT/);
    // The timeline leaves the calls to the list, so each row's target id is there once.
    const ids = [...open.matchAll(/\sid="(item-[^"]+)"/g)].map((match) => match[1]);
    assert.deepEqual(ids.sort(), ['item-call2', 'item-call3']);
  } finally {
    expanded = false;
  }
  const detailed = draw('detailed');
  assert.doesNotMatch(detailed, /CHIP |ROW /);
  assert.match(detailed, /LIVE</);
  // Its run is named by its subagents, never counted among the calls.
  assert.match(detailed, />2 subagents · 2s</);
  expanded = true;
  try {
    const open = draw('detailed');
    assert.match(open, /ROW RUNNING_AGENT[\s\S]*ROW SETTLED_AGENT/);
    const ids = [...open.matchAll(/\sid="(item-[^"]+)"/g)].map((match) => match[1]);
    assert.deepEqual(ids.sort(), ['item-call2', 'item-call3']);
  } finally {
    expanded = false;
  }
  // A subagent's own transcript draws none of it.
  const child = renderToStaticMarkup(React.createElement(exports.AgentItems, { sessionId: 'task', provider: 'copilot', workdir: '/project', agentId: 'a2', items: items.map((item) => ({ ...item, agent_id: 'a2' })), interactions: [], live: true, density: 'compact' }));
  assert.doesNotMatch(child, /CHIP |LIVE<|ROW /);
});

test('a reply past five subagents draws them neutral; a turn of only subagent calls still gets its line', () => {
  const items = [{ id: 'u1', kind: 'user', text: 'Fan out', time: '2026-09-26T12:00:00Z' }];
  const subagents = [];
  for (let n = 1; n <= 6; n++) {
    items.push({ id: `c${n}`, kind: 'tool', time: `2026-09-26T12:00:0${n}Z`, tool: { name: 'task', status: 'completed' } });
    subagents.push({ id: `a${n}`, name: `AGENT_${n}`, status: 'completed', parent_tool_call_id: `c${n}` });
  }
  const html = renderToStaticMarkup(React.createElement(exports.Transcript, { sessionId: 'task', provider: 'copilot', workdir: '/project', items, interactions: [], subagents, live: false, working: false, density: 'compact' }));
  assert.match(html, /data-tones="-,-,-,-,-,-"[^>]*>CHIP AGENT_1/);
  assert.doesNotMatch(html, /activity of this turn/);
});

test('reply membership comes from the scope\'s identity index: a partly loaded reply says how many calls it has', () => {
  const items = [{ id: 'u1', kind: 'user', text: 'Fan out', time: '2026-09-26T12:00:00Z' }, { id: 'c3', kind: 'tool', time: '2026-09-26T12:00:03Z', tool: { name: 'task', status: 'completed' } }];
  const index = [items[0], ...[1, 2, 3].map((n) => ({ id: `c${n}`, kind: 'tool', time: `2026-09-26T12:00:0${n}Z`, tool: { name: 'task' } }))];
  const subagents = [{ id: 'a1', name: 'AGENT_1', status: 'running', parent_tool_call_id: 'c1' }, { id: 'a3', name: 'AGENT_3', status: 'completed', parent_tool_call_id: 'c3' }];
  scopeReplies = subagentsLib.replyIndex(index, subagents);
  try {
    const html = renderToStaticMarkup(React.createElement(exports.Transcript, { sessionId: 'task', provider: 'copilot', workdir: '/project', items, identityItems: index, interactions: [], subagents, live: false, working: false, density: 'compact' }));
    // AGENT_1's call is outside the window, yet it belongs to this reply; the chip counts all three calls.
    assert.match(html, /CHIP AGENT_1,AGENT_3 OF 3/);
  } finally {
    scopeReplies = undefined;
  }
});

test('while the live card shows every subagent of a reply, that reply\'s chip waits; an open list keeps it', () => {
  const items = [{ id: 'u1', kind: 'user', text: 'Go', time: '2026-09-26T12:00:00Z' }, { id: 'c1', kind: 'tool', time: '2026-09-26T12:00:01Z', tool: { name: 'task', status: 'running' } }, { id: 'c2', kind: 'tool', time: '2026-09-26T12:00:02Z', tool: { name: 'task', status: 'running' } }];
  const subagents = [{ id: 'a1', name: 'AGENT_1', status: 'running', parent_tool_call_id: 'c1' }, { id: 'a2', name: 'AGENT_2', status: 'running', parent_tool_call_id: 'c2' }];
  const draw = (liveCard) => renderToStaticMarkup(React.createElement(exports.Transcript, { sessionId: 'task', provider: 'copilot', workdir: '/project', items, interactions: [], subagents, live: true, working: true, density: 'compact', liveCard }));
  liveIds = new Set(['a1', 'a2']);
  try {
    assert.doesNotMatch(draw(true), /CHIP /);
    // No card (a window away from the foot): the chip is the only place.
    assert.match(draw(false), /CHIP AGENT_1,AGENT_2/);
    liveIds = new Set(['a1']);
    assert.match(draw(true), /CHIP AGENT_1,AGENT_2/);
    liveIds = new Set(['a1', 'a2']);
    expanded = true;
    assert.match(draw(true), /CHIP AGENT_1,AGENT_2/);
  } finally {
    expanded = false;
    liveIds = undefined;
  }
});

test('Detailed: a reply past eight shows one grouped list at its first call, and its activity is named by its subagents', () => {
  const items = [{ id: 'u1', kind: 'user', text: 'Fan out', time: '2026-09-26T12:00:00Z' }];
  const subagents = [];
  for (let n = 1; n <= 9; n++) {
    items.push({ id: `c${n}`, kind: 'tool', time: `2026-09-26T12:00:0${n}Z`, tool: { name: 'task', status: 'completed' } });
    subagents.push({ id: `a${n}`, name: `AGENT_${n}`, status: 'completed', parent_tool_call_id: `c${n}` });
  }
  const draw = () => renderToStaticMarkup(React.createElement(exports.Transcript, { sessionId: 'task', provider: 'copilot', workdir: '/project', items, interactions: [], subagents, live: false, working: false, density: 'detailed' }));
  const closed = draw();
  assert.match(closed, />9 subagents</);
  // The run holds the whole reply's list, so locate opens it for any of the nine.
  assert.match(closed, /data-subagent-items="\[&quot;c1&quot;,(&quot;c\d&quot;,?){8}\]"/);
  expanded = true;
  try {
    const open = draw();
    assert.equal((open.match(/LIST OF 9/g) ?? []).length, 1);
    assert.doesNotMatch(open, /ROW /);
    assert.equal((open.match(/LISTED /g) ?? []).length, 9);
  } finally {
    expanded = false;
  }
});

