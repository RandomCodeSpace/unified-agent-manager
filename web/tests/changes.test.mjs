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
  const code = source.statements.filter(node => !ts.isImportDeclaration(node)).map(node => node.getText(source)).join('\n') + '\nexport { FileView };';
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
    useMemo(fn) { return fn(); },
    useEffectEvent(fn) {
      const [holder] = hooks.useState(() => ({ fn, event: (...args) => holder.fn(...args) }));
      holder.fn = fn;
      return holder.event;
    },
  };
  const exports = {};
  const names = ['Copy', 'Ellipsis', 'FileDiff', 'RefreshCw', 'X', 'Note', 'Skeleton', 'PanelHeader', 'SidePanel', 'Button', 'ContextMenu', 'Menu', 'Segmented', 'Tip'];
  runInNewContext(ts.transpileModule(code, { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, jsxFactory: 'jsxNode', jsxFragmentFactory: 'Fragment' } }).outputText, {
    exports, ...hooks, api, AbortController, ...environment, ...Object.fromEntries(names.map(name => [name, name])), Fragment: 'fragment',
    jsxNode: (type, props, ...children) => ({ type, key: props?.key, props: { ...props, children } }),
    describeError: String, structuredPatch: require('diff').structuredPatch, parsePatch: require('diff').parsePatch,
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

test('default and alternate Changes scopes update a selected diff without token-driven reads', async () => {
  for (const alternate of [false, true]) {
    let content = 'first\n';
    let reads = 0, lists = 0;
    const listing = () => ({ supported: true, files: [{ path: 'sample.txt', additions: 1, deletions: 1 }] });
    const api = {
      changes: async () => { lists++; return listing(); },
      changeFile: async () => { reads++; return { path: 'sample.txt', before: 'base\n', after: content }; },
    };
    const { ChangesSheet, FileView, mount } = await components(api, environment());
    let props = { session: { id: 'owned', capabilities: { session_diff: alternate } }, changes: listing(), changesError: null, isDefaultPending: () => false, open: true, active: true,
      onChanges: changes => { props = { ...props, changes }; sheet.update(props); },
      onRefresh: () => { props = { ...props, changes: listing() }; sheet.update(props); },
    };
    const sheet = mount(ChangesSheet, props);
    if (alternate) {
      const tree = await sheet.flush();
      find(tree, node => node.type === 'Segmented').props.onValueChange('workspace');
    }
    let file, key;
    async function render() {
      const tree = await sheet.flush();
      const child = find(tree, node => node.type === FileView);
      assert.ok(child);
      if (!file || child.key !== key) { file?.close(); file = mount(FileView, child.props); key = child.key; }
      else file.update(child.props);
      return file.flush();
    }
    assert.match(JSON.stringify(await render()), /first/);
    assert.equal(reads, alternate ? 2 : 1);
    content = 'other\n';
    props = { ...props, changes: listing() };
    sheet.update(props);
    assert.match(JSON.stringify(await render()), /other/, 'an automatic listing refresh updates the selected diff even with unchanged path/counts');
    assert.equal(reads, alternate ? 3 : 2);
    assert.equal(lists, alternate ? 2 : 0);
    for (let i = 0; i < 20; i++) { sheet.update({ ...props, session: { ...props.session, title: `token-${i}` } }); await render(); }
    assert.equal(reads, alternate ? 3 : 2, 'unrelated parent renders must not refetch the diff');
    assert.equal(lists, alternate ? 2 : 0, 'unrelated parent renders must not refetch the listing');
    content = 'third\n';
    find(await sheet.flush(), node => node.props?.['aria-label'] === 'Refresh').props.onClick();
    assert.match(JSON.stringify(await render()), /third/, 'manual Refresh remains available');
    assert.equal(reads, alternate ? 4 : 3);
    sheet.close(); file.close();
  }
});

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

test('open visible Changes polls every five seconds without overlap, queued work or token resets', async () => {
  const clock = environment();
  const listing = { supported: true, files: [{ path: 'sample.txt', additions: 1, deletions: 1 }] };
  let content = 'first', listHold, fileHold;
  const lists = [], files = [];
  const api = {
    changes: async (id, scope, signal) => { lists.push({ scope, signal }); return listHold ? listHold.promise : { ...listing }; },
    changeFile: async (id, scope, path, signal) => { files.push({ scope, path, signal }); return fileHold ? fileHold.promise : { path, before: 'base', after: content }; },
  };
  const { ChangesSheet, FileView, mount } = await components(api, clock);
  let props = { session: { id: 'owned', capabilities: { session_diff: false } }, changes: listing, changesError: null, isDefaultPending: () => false, active: true, open: true,
    onChanges: changes => { props = { ...props, changes }; sheet.update(props); },
  };
  const sheet = mount(ChangesSheet, props);
  const rendered = async () => {
    const child = find(await sheet.flush(), node => node.type === FileView);
    return child ? JSON.stringify(await mount(FileView, child.props).flush()) : '';
  };
  assert.match(await rendered(), /first/);
  assert.equal(lists.length, 0, 'opening reuses the accepted default list');
  assert.equal(files.length, 1);
  content = 'other';
  clock.advance(4000);
  for (let i = 0; i < 20; i++) { sheet.update({ ...props, session: { ...props.session, title: `token-${i}` } }); await sheet.flush(); }
  clock.advance(999);
  assert.equal(lists.length, 0);
  clock.advance(1);
  assert.match(await rendered(), /other/, 'external edits appear on the first five-second tick');
  assert.equal(lists.length, 1);
  assert.equal(files.length, 2);

  listHold = deferred(); fileHold = deferred();
  clock.advance(5000); await sheet.flush();
  clock.advance(15000); await sheet.flush();
  assert.equal(lists.length, 2, 'slow list reads suppress later ticks without queuing');
  assert.match(await rendered(), /other/, 'the prior diff stays visible during the list read');
  listHold.resolve({ ...listing }); listHold = undefined; await sheet.flush();
  assert.equal(files.length, 3);
  clock.advance(10000); await sheet.flush();
  assert.equal(lists.length, 2, 'the same cycle stays busy through its diff read');
  assert.match(await rendered(), /other/, 'the prior diff stays visible during the diff read');
  fileHold.resolve({ path: 'sample.txt', before: 'base', after: 'third' }); fileHold = undefined;
  assert.match(await rendered(), /third/);
  assert.equal(lists.length, 2, 'finishing a read must not drain a refresh queue');

  clock.visibility('hidden'); clock.advance(10000); await sheet.flush();
  assert.equal(lists.length, 2);
  assert.equal(clock.timerCount(), 0, 'hidden panels stop their timer');
  clock.visibility('visible'); await sheet.flush();
  assert.equal(lists.length, 3, 'visibility return refreshes immediately');
  for (const disabled of [{ open: false }, { active: false }]) {
    listHold = deferred(); clock.advance(5000); await sheet.flush();
    const pending = lists.at(-1);
    props = { ...props, ...disabled }; sheet.update(props); await sheet.flush();
    assert.equal(pending.signal.aborted, true, 'closing or deactivating aborts the cycle');
    const count = lists.length;
    clock.advance(10000); clock.visibility('visible'); await sheet.flush();
    assert.equal(lists.length, count);
    assert.equal(clock.timerCount(), 0);
    listHold.resolve({ supported: true, files: [{ path: 'late.txt', additions: 1, deletions: 1 }] }); listHold = undefined;
    assert.doesNotMatch(JSON.stringify(await sheet.flush()), /late.txt/);
    props = { ...props, open: true, active: true }; sheet.update(props); await sheet.flush();
  }
  sheet.close();
});

test('scope changes and hiding abort a cycle and reject late results without polling unseen scopes', async () => {
  const clock = environment();
  const listing = { supported: true, files: [{ path: 'sample.txt', additions: 1, deletions: 1 }] };
  let held = deferred();
  const lists = [], files = [];
  let hold = false;
  const api = {
    changes: async (id, scope, signal) => { lists.push({ scope, signal }); return hold ? held.promise : { ...listing }; },
    changeFile: async (id, scope, path, signal) => { files.push({ scope, signal }); return { path, before: 'base', after: scope }; },
  };
  const { ChangesSheet, FileView, mount } = await components(api, clock);
  const props = { session: { id: 'owned', capabilities: { session_diff: true } }, changes: listing, changesError: null, isDefaultPending: () => false, active: true, open: true, onChanges() {} };
  const sheet = mount(ChangesSheet, props);
  await sheet.flush();
  hold = true; clock.advance(5000); await sheet.flush();
  const old = lists.at(-1);
  assert.ok(old, 'the five-second timer starts a refresh cycle');
  hold = false;
  find(await sheet.flush(), node => node.type === 'Segmented').props.onValueChange('workspace');
  await sheet.flush();
  assert.equal(old.signal.aborted, true);
  assert.equal(lists.at(-1).scope, 'workspace');
  held.resolve({ supported: true, files: [{ path: 'stale.txt', additions: 1, deletions: 1 }] });
  const tree = await sheet.flush();
  assert.doesNotMatch(JSON.stringify(tree), /stale.txt/);
  const child = find(tree, node => node.type === FileView);
  assert.match(JSON.stringify(await mount(FileView, child.props).flush()), /workspace/);
  const before = lists.length;
  clock.advance(5000); await sheet.flush();
  assert.deepEqual(lists.slice(before).map(request => request.scope), ['workspace']);
  assert.equal(files.at(-1).scope, 'workspace');
  held = deferred(); hold = true; clock.advance(5000); await sheet.flush();
  const hiding = lists.at(-1);
  clock.visibility('hidden');
  assert.equal(hiding.signal.aborted, true);
  clock.advance(10000); await sheet.flush();
  assert.equal(lists.at(-1), hiding);
  sheet.close();
});

test('a pending Task listing suppresses timer, visibility and manual reads, but failure permits retry', async () => {
  for (const failed of [false, true]) {
    const clock = environment();
    const listing = { supported: true, files: [{ path: 'sample.txt', additions: 1, deletions: 1 }] };
    let pending = true, lists = 0, files = 0;
    const api = {
      changes: async () => { lists++; return listing; },
      changeFile: async (_id, _scope, path) => { files++; return { path, before: 'base', after: 'current' }; },
    };
    const { ChangesSheet, mount } = await components(api, clock);
    let props = { session: { id: 'owned', capabilities: { session_diff: false } }, changes: null, changesError: null,
      isDefaultPending: () => pending, active: true, open: true, onChanges() {},
    };
    const sheet = mount(ChangesSheet, props);
    await sheet.flush();
    clock.advance(15000); await sheet.flush();
    clock.visibility('hidden'); clock.visibility('visible'); await sheet.flush();
    find(await sheet.flush(), node => node.props?.['aria-label'] === 'Refresh').props.onClick();
    await sheet.flush();
    assert.equal(lists, 0, 'all refresh triggers wait for the pending Task default list');
    assert.equal(files, 0);
    pending = false;
    props = { ...props, changes: failed ? null : listing, changesError: failed ? 'initial read failed' : null };
    sheet.update(props); await sheet.flush();
    if (failed) { clock.advance(5000); await sheet.flush(); }
    assert.equal(lists, failed ? 1 : 0, 'failure ends pending status and permits a later retry');
    assert.equal(files, 1);
    sheet.close();
  }
});

test('a newer Task listing replaces a pending panel list or detail without an older publication', async () => {
  for (const phase of ['list', 'detail']) {
    const clock = environment();
    const listing = name => ({ label: name, supported: true, files: [{ path: `${name}.txt`, additions: 1, deletions: 1 }] });
    const first = listing('first'), newer = listing('newer'), held = deferred();
    let hold = false, oldSignal;
    const filePaths = [];
    const api = {
      changes: async (_id, _scope, signal) => {
        if (hold && phase === 'list') { oldSignal = signal; return held.promise; }
        return first;
      },
      changeFile: async (_id, _scope, path, signal) => {
        filePaths.push(path);
        if (hold && phase === 'detail' && path === 'first.txt') { oldSignal = signal; return held.promise; }
        return { path, before: 'base', after: path };
      },
    };
    const { ChangesSheet, FileView, mount } = await components(api, clock);
    let badge;
    let props = { session: { id: 'owned', capabilities: { session_diff: false } }, changes: first, changesError: null,
      isDefaultPending: () => false, active: true, open: true,
      onChanges: changes => { badge = changes; props = { ...props, changes }; sheet.update(props); },
    };
    const sheet = mount(ChangesSheet, props);
    await sheet.flush();
    hold = true; clock.advance(5000); await sheet.flush();
    assert.ok(oldSignal);
    badge = newer; props = { ...props, changes: newer }; sheet.update(props); await sheet.flush();
    held.resolve(phase === 'list' ? first : { path: 'first.txt', before: 'base', after: 'obsolete' });
    const tree = await sheet.flush();
    assert.equal(badge, newer, 'an obsolete panel completion must not overwrite the newer Task badge');
    assert.equal(oldSignal.aborted, true);
    assert.ok(filePaths.includes('newer.txt'), 'the newer listing must get its selected detail');
    const child = find(tree, node => node.type === FileView);
    const rendered = JSON.stringify(await mount(FileView, child.props).flush());
    assert.match(rendered, /newer.txt/);
    assert.doesNotMatch(rendered, /obsolete|first.txt/);
    sheet.close();
  }
});

test('accepted list counts survive a failed detail refresh while the prior diff and error stay visible', async () => {
  const clock = environment();
  const first = { supported: true, files: [{ path: 'sample.txt', additions: 1, deletions: 1 }] };
  const next = { ...first, files: [...first.files, { path: 'another.txt', additions: 1, deletions: 0 }] };
  let fail = false, content = 'previous-content', badge = first;
  const api = {
    changes: async () => next,
    changeFile: async (_id, _scope, path) => { if (fail) throw new Error('detail refresh failed'); return { path, before: 'base', after: content }; },
  };
  const { ChangesSheet, FileView, mount } = await components(api, clock);
  let props = { session: { id: 'owned', capabilities: { session_diff: false } }, changes: first, changesError: null,
    isDefaultPending: () => false, active: true, open: true,
    onChanges: changes => { badge = changes; props = { ...props, changes }; sheet.update(props); },
  };
  const sheet = mount(ChangesSheet, props);
  const rendered = async () => {
    const child = find(await sheet.flush(), node => node.type === FileView);
    return JSON.stringify(await mount(FileView, child.props).flush());
  };
  assert.match(await rendered(), /previous-content/);
  fail = true; clock.advance(5000);
  const failed = await rendered();
  assert.equal(badge.files.length, 2, 'a successful listing updates the Task count even if detail fails');
  assert.match(failed, /previous-content/, 'a background detail error must not hide the last same-path diff');
  assert.match(failed, /detail refresh failed/);
  fail = false; content = 'replacement-content'; clock.advance(5000);
  const recovered = await rendered();
  assert.match(recovered, /replacement-content/);
  assert.doesNotMatch(recovered, /previous-content|detail refresh failed/);
  sheet.close();
});
