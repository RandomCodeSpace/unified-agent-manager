import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { runInNewContext } from 'node:vm';
import test from 'node:test';
const require = createRequire(import.meta.url);
const ts = require('typescript');

// Execute the real sheet and selected-file component with a small effect runner.
// No DOM or network is required; browser interaction remains a separate check.
async function components(api, environment = {}) {
  const path = new URL('../src/components/Changes.tsx', import.meta.url);
  const source = ts.createSourceFile('Changes.tsx', await readFile(path, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const code = source.statements.filter(node => !ts.isImportDeclaration(node)).map(node => node.getText(source)).join('\n') + '\nexport { FileView, FileCard, DiffRows, DiffSlice };';
  let current;
  const hooks = {
    useState(initial) {
      const owner = current, index = owner.cursor++;
      if (!(index in owner.slots)) owner.slots[index] = typeof initial === 'function' ? initial() : initial;
      return [owner.slots[index], value => { owner.slots[index] = typeof value === 'function' ? value(owner.slots[index]) : value; owner.dirty = true; }];
    },
    useRef(initial) { const [value] = hooks.useState(() => ({ current: initial })); return value; },
    useEffect(effect, deps) {
      const owner = current, index = owner.cursor++, old = owner.slots[index];
      if (old && deps.length === old.deps.length && deps.every((value, i) => Object.is(value, old.deps[i]))) return;
      owner.effects.push(() => { old?.cleanup?.(); owner.slots[index] = { deps, cleanup: effect() }; });
    },
    useLayoutEffect(effect, deps) { return hooks.useEffect(effect, deps); },
    useMemo(fn) { return fn(); },
    useCallback(fn) { return fn; },
    useId() { return 'glyph'; },
    memo(component) { return component; },
    startTransition(fn) { fn(); },
    useEffectEvent(fn) {
      const [holder] = hooks.useState(() => ({ fn, event: (...args) => holder.fn(...args) }));
      holder.fn = fn;
      return holder.event;
    },
  };
  const exports = {};
  const client = { cacheKey: id => id, ...api };
  const names = ['ChevronDown', 'Copy', 'Ellipsis', 'Eye', 'FileDiff', 'MessageSquarePlus', 'RefreshCw', 'ShieldAlert', 'Undo2', 'X', 'FileIcon', 'InstanceName', 'Note', 'Skeleton', 'PanelHeader', 'SidePanel', 'Button', 'Menu', 'Tip', 'AlertDialog'];
  const lens = await import('../src/lib/lens.ts');
  const review = await import('../src/lib/review.ts');
  runInNewContext(ts.transpileModule(code, { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, jsxFactory: 'jsxNode', jsxFragmentFactory: 'Fragment' } }).outputText, {
    exports, ...hooks, api: client, useApi: () => client, AbortController, ...review, ...lens, CSS: { escape: String }, requestAnimationFrame: () => 0,
    useConfirm: () => { const [state, set] = hooks.useState({ target: null, open: false }); return { target: state.target, ask: (t) => set({ target: t, open: true }), close: () => set({ target: state.target, open: false }), props: { open: state.open } }; },
    useCopied: () => [false, () => {}], LIVE: ['starting', 'working'], readOnly: () => false, newRequestId: () => 'request', cn: (...c) => c.filter(Boolean).join(' '), ...environment, ...Object.fromEntries(names.map(name => [name, name])), Fragment: 'fragment',
    jsxNode: (type, props, ...children) => ({ type, key: props?.key, props: { ...props, children } }),
    useLazyContextMenu: () => ({ shell: {}, menu: () => null }), describeError: String, structuredPatch: require('diff').structuredPatch, parsePatch: require('diff').parsePatch, diffWordsWithSpace: require('diff').diffWordsWithSpace,
  });
  function mount(Component, props) {
    const owner = { slots: [], effects: [], props, dirty: true, cursor: 0, tree: null };
    return {
      update(props) { owner.props = props; owner.dirty = true; },
      async flush() {
        for (let i = 0; i < 20; i++) {
          if (owner.dirty) {
            owner.dirty = false; owner.cursor = 0; current = owner;
            owner.tree = Component(owner.props);
            owner.effects.splice(0).forEach(effect => effect());
          }
          await Promise.resolve();
        }
        return owner.tree;
      },
      close() { owner.slots.forEach(slot => slot?.cleanup?.()); },
    };
  }
  return { ...exports, mount };
}
function find(node, predicate) {
  if (!node || typeof node !== 'object') return null;
  if (predicate(node)) return node;
  for (const child of Array.isArray(node) ? node : node.props?.children ?? []) {
    const found = find(child, predicate);
    if (found) return found;
  }
  return null;
}

function environment() {
  let now = 0, id = 0;
  const timers = new Map(), listeners = new Set();
  const document = {
    visibilityState: 'visible',
    addEventListener: (type, listener) => { if (type === 'visibilitychange') listeners.add(listener); },
    removeEventListener: (type, listener) => { if (type === 'visibilitychange') listeners.delete(listener); },
  };
  return { document, window: {
    setInterval(callback, ms) { const key = ++id; timers.set(key, { callback, ms, due: now + ms }); return key; },
    clearInterval(key) { timers.delete(key); },
  },
  advance(ms) {
    const end = now + ms;
    for (;;) {
      const due = [...timers.values()].filter(timer => timer.due <= end).sort((a, b) => a.due - b.due)[0];
      if (!due) break;
      now = due.due; due.due += due.ms; due.callback();
    }
    now = end;
  },
  visibility(value) { document.visibilityState = value; listeners.forEach(listener => listener()); },
  timerCount: () => timers.size,
  };
}

function deferred() { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; }


const session = { id: 'owned', state: 'idle', capabilities: { session_diff: false } };
const cards = (tree, FileCard) => { const out = []; (function walk(n) { if (!n || typeof n !== 'object') return; if (Array.isArray(n)) { n.forEach(walk); return; } if (n.type === FileCard) out.push(n); (n.props?.children ?? []).forEach?.(walk); })(tree); return out; };

test('the stack reuses the Thread list, polls every five seconds without overlap, and stops when hidden or closed', async () => {
  const clock = environment();
  const listing = { supported: true, files: [{ path: 'sample.txt', additions: 1, deletions: 1, digest: 'a' }] };
  let hold;
  const lists = [];
  const api = { changes: async (id, scope, signal) => { lists.push({ scope, signal }); return hold ? hold.promise : { ...listing }; } };
  const { ChangesSheet, FileCard, mount } = await components(api, clock);
  let props = { session, changes: listing, changesError: null, isDefaultPending: () => false, active: true, open: true,
    onChanges: changes => { props = { ...props, changes }; sheet.update(props); } };
  const sheet = mount(ChangesSheet, props);
  assert.equal(cards(await sheet.flush(), FileCard).length, 1);
  assert.equal(lists.length, 0, 'opening reuses the accepted Thread list');
  for (let i = 0; i < 20; i++) { sheet.update({ ...props, session: { ...session, title: `token-${i}` } }); await sheet.flush(); }
  clock.advance(4999);
  assert.equal(lists.length, 0, 'unrelated renders do not read the list');
  clock.advance(1); await sheet.flush();
  assert.deepEqual(lists.map(l => l.scope), ['task'], 'only the Thread scope is read');
  hold = deferred();
  clock.advance(5000); await sheet.flush();
  clock.advance(15000); await sheet.flush();
  assert.equal(lists.length, 2, 'a slow read suppresses later ticks without queuing');
  hold.resolve({ ...listing }); hold = undefined; await sheet.flush();
  clock.visibility('hidden'); clock.advance(10000); await sheet.flush();
  assert.equal(lists.length, 2);
  assert.equal(clock.timerCount(), 0, 'hidden panels stop their timer');
  clock.visibility('visible'); await sheet.flush();
  assert.equal(lists.length, 3, 'visibility return refreshes immediately');
  for (const disabled of [{ open: false }, { active: false }]) {
    hold = deferred(); clock.advance(5000); await sheet.flush();
    const pending = lists.at(-1);
    props = { ...props, ...disabled }; sheet.update(props); await sheet.flush();
    assert.equal(pending.signal.aborted, true, 'closing or deactivating aborts the read');
    const count = lists.length;
    clock.advance(10000); await sheet.flush();
    assert.equal(lists.length, count);
    hold.resolve({ supported: true, files: [{ path: 'late.txt', additions: 1, deletions: 1 }] }); hold = undefined;
    assert.doesNotMatch(JSON.stringify(await sheet.flush()), /late.txt/);
    props = { ...props, open: true, active: true }; sheet.update(props); await sheet.flush();
  }
  sheet.close();
});

test('in a subfolder Project, Open strips the folder from repository paths and is absent for files outside it', async () => {
  const environmentClock = environment();
  const listing = { supported: true, prefix: 'sub/', files: [{ path: 'sub/a.go', additions: 1, deletions: 0, digest: 'a' }, { path: 'top.go', additions: 1, deletions: 0, digest: 'b' }] };
  const opened = [];
  const { ChangesSheet, FileCard, mount } = await components({ changes: async () => listing }, environmentClock);
  const sheet = mount(ChangesSheet, { session, changes: listing, changesError: null, isDefaultPending: () => false, active: true, open: true, onChanges: () => {}, onOpenFile: (path) => opened.push(path) });
  const [inside, outside] = cards(await sheet.flush(), FileCard);
  inside.props.onOpenFile('opener');
  assert.deepEqual(opened, ['a.go']);
  assert.equal(outside.props.onOpenFile, undefined);
  sheet.close();
});

test('a card reads its diff once unfolded, again only when its digest moves, and keeps it through a failed reread', async () => {
  let content = 'first\n', fail = false;
  const reads = [];
  const api = { changeFile: async (id, scope, path) => { reads.push(scope); if (fail) throw new Error('diff read failed'); return { path, before: 'base\n', after: content }; } };
  const { FileCard, FileView, mount } = await components(api);
  const file = digest => ({ path: 'sample.txt', status: 'modified', additions: 1, deletions: 1, digest });
  let props = { sessionId: 'owned', file: file('a'), viewed: 'unviewed', folded: true, onFold() {}, onViewed() {}, onOpenFile() {}, comments: [], onRemoveComment() {} };
  const card = mount(FileCard, props);
  const shown = async () => { const v = find(await card.flush(), n => n.type === FileView); return v ? JSON.stringify(await mount(FileView, v.props).flush()) : ''; };
  assert.equal(await shown(), '', 'a folded card shows no diff');
  assert.equal(reads.length, 0, 'and reads none');
  props = { ...props, folded: false }; card.update(props);
  assert.match(await shown(), /first/);
  assert.deepEqual(reads, ['task']);
  for (let i = 0; i < 10; i++) { card.update({ ...props, file: { ...file('a') } }); await card.flush(); }
  assert.equal(reads.length, 1, 'the same digest is not read again');
  content = 'second\n'; props = { ...props, file: file('b') }; card.update(props);
  assert.match(await shown(), /second/);
  fail = true; props = { ...props, file: file('c') }; card.update(props);
  const failed = await shown();
  assert.match(failed, /second/, 'the last diff stays on screen');
  assert.match(failed, /diff read failed/);
  card.close();
});

test('a pending Task listing suppresses timer, visibility and manual reads', async () => {
  const clock = environment();
  let pending = true, lists = 0;
  const api = { changes: async () => { lists++; return { supported: true, files: [] }; } };
  const { ChangesSheet, mount } = await components(api, clock);
  const sheet = mount(ChangesSheet, { session, changes: null, changesError: null, isDefaultPending: () => pending, active: true, open: true, onChanges() {} });
  await sheet.flush();
  clock.advance(15000); await sheet.flush();
  clock.visibility('hidden'); clock.visibility('visible'); await sheet.flush();
  find(await sheet.flush(), node => node.props?.['aria-label'] === 'Refresh').props.onClick();
  await sheet.flush();
  assert.equal(lists, 0, 'every refresh waits for the pending Thread list');
  pending = false;
  clock.advance(5000); await sheet.flush();
  assert.equal(lists, 1);
  sheet.close();
});

function memoryStorage(seed = {}) {
  const data = new Map(Object.entries(seed));
  return { data, getItem: k => data.get(k) ?? null, setItem: (k, v) => data.set(k, String(v)), removeItem: k => data.delete(k) };
}

test('viewed marks fold a card and clear when the file changes, and comments go out as one queued message', async () => {
  const storage = memoryStorage({ 'uam.review.owned': JSON.stringify({ viewed: {}, comments: [
    { id: 'c1', path: 'b.go', line: 7, side: 'new', code: 'x := 1', body: 'Name this better.' },
    { id: 'c2', path: 'a.go', line: 3, side: 'old', code: 'old()', body: 'Keep this call.' },
  ] }) });
  const sent = [];
  const listing = digest => ({ supported: true, scope: 'task', files: [
    { path: 'a.go', status: 'modified', additions: 1, deletions: 1, digest },
    { path: '.github/workflows/ci.yml', status: 'modified', additions: 2, deletions: 0, digest: 'w' },
  ] });
  const api = {
    changes: async () => listing('one'),
    prompt: async (id, text, requestId, mode) => { sent.push({ id, text, requestId, mode }); return { request_id: requestId, status: 'queued', time: '' }; },
  };
  const { ChangesSheet, FileCard, mount } = await components(api, { ...environment(), localStorage: storage });
  const props = { session: { ...session, state: 'working' }, changes: listing('one'), changesError: null, isDefaultPending: () => false, open: true, active: true, onChanges: () => {} };
  const sheet = mount(ChangesSheet, props);
  let tree = await sheet.flush();
  assert.deepEqual(cards(tree, FileCard).map(c => c.props.file.path), ['.github/workflows/ci.yml', 'a.go'], 'risky files first');
  assert.deepEqual(cards(tree, FileCard).map(c => c.props.folded), [false, false]);
  cards(tree, FileCard)[1].props.onViewed(true);
  tree = await sheet.flush();
  assert.equal(cards(tree, FileCard)[1].props.viewed, 'viewed');
  assert.equal(cards(tree, FileCard)[1].props.folded, true, 'a viewed file folds');
  assert.equal(JSON.parse(storage.data.get('uam.review.owned')).viewed['a.go'], 'one');
  sheet.update({ ...props, changes: listing('two') });
  tree = await sheet.flush();
  assert.equal(cards(tree, FileCard)[1].props.viewed, 'changed', 'a new digest clears the mark');

  const batch = find(tree, node => node.type?.name === 'CommentBatch');
  assert.equal(batch.props.comments.length, 2);
  batch.props.onSend();
  await sheet.flush();
  assert.equal(sent.length, 1);
  assert.equal(sent[0].mode, 'queue');
  assert.match(sent[0].text, /2 comments[\s\S]*1\. a\.go, removed line 3:[\s\S]*> old\(\)[\s\S]*Keep this call\.[\s\S]*2\. b\.go, line 7:[\s\S]*Name this better\./);
  assert.equal(JSON.parse(storage.data.get('uam.review.owned') ?? '{"comments":[]}').comments.length, 0, 'sent comments leave storage');
  sheet.close();
});

test('Revert sends one hunk as its patch text, and Revert file asks before it reverts the whole file', async () => {
  const reverts = [];
  const api = { revertChange: async (id, path, hunk) => { reverts.push({ path, hunk }); }, changes: async () => ({ supported: true, files: [] }) };
  const { ChangesSheet, FileCard, FileView, DiffRows, DiffSlice, mount } = await components(api, environment());
  const listing = { supported: true, files: [{ path: 'a.txt', status: 'modified', additions: 1, deletions: 1, digest: 'd' }] };
  const sheet = mount(ChangesSheet, { session, changes: listing, changesError: null, isDefaultPending: () => false, open: true, active: true, onChanges() {} });
  let tree = await sheet.flush();
  const card = cards(tree, FileCard)[0];
  await card.props.onRevert('@@ -1,1 +1,1 @@\n-a\n+b\n');
  assert.deepEqual(reverts, [{ path: 'a.txt', hunk: '@@ -1,1 +1,1 @@\n-a\n+b\n' }]);
  card.props.onRevertFile();
  tree = await sheet.flush();
  const dialog = find(tree, node => node.type === 'AlertDialog');
  assert.equal(dialog.props.open, true, 'a whole-file revert asks first');
  assert.equal(reverts.length, 1);
  dialog.props.onConfirm();
  await sheet.flush();
  assert.deepEqual(reverts.at(-1), { path: 'a.txt', hunk: undefined });
  // The diff's hunk bar carries the hunk's own text to Revert.
  const hunks = [];
  const view = mount(FileView, { path: 'a.txt', file: { path: 'a.txt', before: 'one\ntwo\n', after: 'one\nTWO\n' }, error: null, bare: true, onRevert: async (hunk) => { hunks.push(hunk); } });
  const rows = find(await view.flush(), node => node.type === DiffRows);
  const slice = find(await mount(DiffRows, rows.props).flush(), node => node.type === DiffSlice);
  const bar = find(await mount(DiffSlice, slice.props).flush(), node => node.type === 'Button' && JSON.stringify(node.props.children).includes('Revert'));
  assert.ok(bar, 'each hunk has a Revert');
  bar.props.onClick();
  assert.deepEqual(hunks, ['@@ -1,2 +1,2 @@\n one\n-two\n+TWO\n']);
  sheet.close(); view.close();
});

test('native binary and truncated changes do not claim zero line changes or an empty text diff', async () => {
  const { FileView, FileCard, mount } = await components({ changeFile: async () => new Promise(() => {}) });
  for (const flags of [{ binary: true }, { truncated: true }]) {
    const file = { path: 'native.bin', status: 'modified', additions: 0, deletions: 0, counts_unknown: true, ...flags };
    const card = mount(FileCard, { sessionId: 'owned', file, viewed: 'unviewed', folded: true, onFold() {}, onViewed() {}, onOpenFile() {}, comments: [], onRemoveComment() {} });
    const tree = await card.flush();
    assert.equal(JSON.stringify(tree).includes('+0'), false);
    assert.match(JSON.stringify(tree), flags.binary ? /"binary"/ : /"—"/);
    const view = mount(FileView, { path: file.path, file, error: null });
    const shown = JSON.stringify(await view.flush());
    assert.match(shown, flags.binary ? /Binary file/ : /omitted|size limit/);
    assert.equal(shown.includes('No textual changes'), false);
    card.close(); view.close();
  }
});
