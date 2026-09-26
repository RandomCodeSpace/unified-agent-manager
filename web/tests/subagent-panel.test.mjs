import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import test from 'node:test';
import { runInNewContext } from 'node:vm';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import ts from 'typescript';
import * as transcript from '../src/lib/transcript.ts';
import * as history from '../src/lib/historyState.ts';

// Render the real transcript component with local UI shells and no browser or data reads.
const require = createRequire(import.meta.url);
const source = await readFile(new URL('../src/components/Transcript.tsx', import.meta.url), 'utf8');
const exports = {};
let expanded = false;
const disclosureValues = new Map();
const disclosureKeys = [];
const shell = ({ children, render }) => render ? React.cloneElement(render, {}, children) : children;
const menu = { Root: shell, Trigger: shell, Content: shell, Actions: () => null };
const element = tag => ({ children }) => React.createElement(tag, null, children);
const modules = {
  './Details': { DetailVisibility: shell, BodyNotice: () => null, useBodyCopy: () => ({}), useDisclosure: key => { disclosureKeys.push(key); return React.useState(disclosureValues.get(key) ?? expanded); }, useItemBody: item => ({ item }) },
  '../api': { modelName: (_meta, _provider, model) => model },
  '../lib/clipboard': { useCopied: () => [false, () => {}] },
  '../lib/cn': { cn: (...values) => values.filter(value => typeof value === 'string').join(' ') },
  '../lib/transcript': transcript,
  '../lib/historyState': history,
  '../lib/verbs': { turnVerb: () => 'Working' },
  './Attachments': { ImageThumbs: () => null, ItemAttachments: () => null },
  './common': { CodeBlock: element('pre'), Markdown: ({ text }) => React.createElement('p', null, text), SessionContext: React.createContext(''), WorkdirContext: React.createContext(''), Spinner: () => null, SubagentIdleIcon: () => null, WorkingMark: () => null, useApp: () => ({ meta: null }) },
  './Interactions': { DecidedRow: ({ interaction }) => React.createElement('p', null, interaction.id) },
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
  assert.match(live, /Running:/);
  assert.doesNotMatch(live, /Busy for|Took /);
  const main = renderToStaticMarkup(React.createElement(exports.Transcript, { ...props, agentId: undefined, items: [], subagents: [], working: false, density: 'compact' }));
  assert.match(main, /Parent question/);
  assert.doesNotMatch(main, /Child question|Other child question/);
});

// Exercise the actual table override through react-markdown without importing the app shell.
test('Markdown keeps native table semantics inside a keyboard reachable scroll region', async () => {
  const common = ts.createSourceFile('common.tsx', await readFile(new URL('../src/components/common.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  let table;
  const visit = node => {
    if (ts.isVariableDeclaration(node) && node.name.getText(common) === 'mdComponents') {
      table = node.initializer.properties.find(property => property.name?.getText(common) === 'table');
    }
    ts.forEachChild(node, visit);
  };
  visit(common);
  const scope = {};
  if (table) runInNewContext(ts.transpileModule(`export const components = { ${table.getText(common)} };`, { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX } }).outputText, { exports: scope, require });
  const markdown = '| Name | Value |\n| --- | ---: |\n| original path | ' + 'very-long-value'.repeat(20) + ' |';
  const rendered = renderToStaticMarkup(React.createElement(ReactMarkdown, { remarkPlugins: [remarkGfm], components: scope.components }, markdown));
  assert.match(rendered, /<div[^>]*role="region"[^>]*aria-label="Table"/);
  assert.match(rendered, /tabindex="0"/);
  assert.match(rendered, /overflow-x-auto/);
  assert.match(rendered, /<table[^>]*><thead><tr><th>Name<\/th><th style="text-align:right">Value<\/th>/);
  assert.match(rendered, /<tbody><tr><td>original path<\/td>/);
  assert.match(rendered, new RegExp('very-long-value'.repeat(20)));
  assert.doesNotMatch(rendered, /role="table"|display:block|overflow-hidden/);
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
