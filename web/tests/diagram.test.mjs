import assert from 'node:assert/strict';
import test from 'node:test';
import { DiagramError, DiagramQueue, fenceClosed, intrinsicSize, parseReply, parseRequest, renderDiagram, svgDataUrl } from '../src/lib/diagram.ts';

const flow = 'flowchart LR\n  A --> B';
const theme = { primaryColor: '#f3f2ee' };

test('requests and replies are validated on both sides', () => {
  assert.deepEqual(parseRequest({ id: 'd1', source: flow, theme: { a: '#fff', b: 3 } }), { id: 'd1', source: flow, theme: { a: '#fff' } });
  assert.equal(parseRequest({ id: 'd1', source: flow }), null);
  assert.equal(parseRequest({ id: 1, source: flow, theme: {} }), null);
  assert.equal(parseRequest('d1'), null);

  assert.deepEqual(parseReply({ id: 'd1', svg: '<svg/>', width: 10, height: 5 }), { id: 'd1', svg: '<svg/>', width: 10, height: 5 });
  assert.deepEqual(parseReply({ id: 'd1', error: 'Parse error' }), { id: 'd1', error: 'Parse error' });
  assert.equal(parseReply({ id: 'd1', svg: '<svg/>' }), null);
  assert.equal(parseReply({ svg: '<svg/>', width: 1, height: 1 }), null);
  assert.equal(parseReply(null), null);
});

test('the root gets the viewBox size as its intrinsic size, so the image is not 300×150', () => {
  const mermaid = '<svg aria-roledescription="sequence" viewBox="-50 -10 720.5 380" style="max-width: 720px;" width="100%" id="d1"><g viewBox="0 0 1 1"/></svg>';
  assert.deepEqual(intrinsicSize(mermaid), {
    svg: '<svg width="721" height="380" aria-roledescription="sequence" viewBox="-50 -10 720.5 380" style="max-width: 720px;" id="d1"><g viewBox="0 0 1 1"/></svg>',
    width: 721,
    height: 380,
  });
  assert.deepEqual(intrinsicSize("<svg viewBox='0,0,300,150'></svg>"), { svg: '<svg width="300" height="150" viewBox=\'0,0,300,150\'></svg>', width: 300, height: 150 });
  // Without a viewBox (gantt) the root's own pixel size counts; percentages do not.
  assert.deepEqual(intrinsicSize('<svg width="640" height="120.4"><rect/></svg>'), { svg: '<svg width="640" height="121"><rect/></svg>', width: 640, height: 121 });
  assert.equal(intrinsicSize('<svg width="100%" height="10"></svg>'), null);
  assert.equal(intrinsicSize('<svg viewBox="0 0 0 10"></svg>'), null);
  assert.equal(intrinsicSize('no svg here'), null);
});

test('the SVG is shown as a data: image, never inlined', () => {
  const url = svgDataUrl('<svg><style>#a{fill:#fff}</style></svg>');
  assert.ok(url.startsWith('data:image/svg+xml;charset=utf-8,'));
  assert.ok(!url.includes('#') && !url.includes('<'));
});

test('a diagram renders once its fence has closed', () => {
  const closed = 'Text\n\n```mermaid\nflowchart LR\n  A --> B\n```\n\nMore';
  const start = closed.indexOf('```');
  assert.equal(fenceClosed(closed, start, closed.indexOf('```\n\nMore') + 3), true);

  const open = 'Text\n\n```mermaid\nflowchart LR\n  A --> B\n';
  assert.equal(fenceClosed(open, open.indexOf('```'), open.length), false);
  assert.equal(fenceClosed('```mermaid', 0, 10), false);

  const tildes = '~~~mermaid\nflowchart LR\n~~~';
  assert.equal(fenceClosed(tildes, 0, tildes.length), true);
  const mixed = '~~~mermaid\nflowchart LR\n```';
  assert.equal(fenceClosed(mixed, 0, mixed.length), false);
  const longer = '````mermaid\nflowchart LR\n```\nstill code';
  assert.equal(fenceClosed(longer, 0, longer.length), false);

  const listed = '- item\n\n   ```mermaid\n   flowchart LR\n   ```';
  assert.equal(fenceClosed(listed, listed.indexOf('```'), listed.length), true);
  const quoted = '> ```mermaid\n> flowchart LR\n> ```';
  assert.equal(fenceClosed(quoted, 0, quoted.length), true);
  const indented = '    flowchart LR';
  assert.equal(fenceClosed(indented, 0, indented.length), true);
});

test('the queue posts one request at a time and matches replies by id', async () => {
  const sent = [];
  const q = new DiagramQueue((req) => sent.push(req), 1000);
  const a = q.render(flow, theme);
  const b = q.render('sequenceDiagram\n  A->>B: hi', theme);
  assert.deepEqual(sent, [{ id: 'd1', source: flow, theme }]);

  assert.equal(q.receive('noise'), false);
  assert.equal(q.receive({ id: 'd9', svg: '<svg/>', width: 1, height: 1 }), false);
  assert.equal(q.receive({ id: 'd1', svg: '<svg/>', width: 300, height: 150 }), true);
  assert.deepEqual(await a, { svg: '<svg/>', width: 300, height: 150 });
  assert.equal(sent.length, 2);
  assert.equal(sent[1].id, 'd2');

  assert.equal(q.receive({ id: 'd2', error: 'Parse error on line 2' }), true);
  await assert.rejects(b, (err) => err instanceof DiagramError && err.message === 'Parse error on line 2' && err.transient === false);
});

test('a stalled render times out, reports the stall and lets the next one through', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const sent = [];
  let stalls = 0;
  const q = new DiagramQueue((req) => sent.push(req), 500, () => stalls++);
  const a = q.render(flow, theme);
  const b = q.render(flow + '\n  B --> C', theme);
  t.mock.timers.tick(499);
  assert.equal(sent.length, 1);
  t.mock.timers.tick(1);
  await assert.rejects(a, (err) => err instanceof DiagramError && err.transient === true);
  assert.equal(stalls, 1);
  assert.equal(sent.length, 2);
  assert.equal(q.receive({ id: 'd1', svg: '<svg/>', width: 1, height: 1 }), false, 'a late reply to the timed-out request is ignored');
  assert.equal(q.receive({ id: 'd2', svg: '<svg/>', width: 2, height: 2 }), true);
  assert.deepEqual(await b, { svg: '<svg/>', width: 2, height: 2 });
});

test('a failed frame load and a stalled renderer reject their whole batch and allow a fresh frame', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const frames = [];
  const messages = [];
  const page = new EventTarget();
  class Frame extends EventTarget {
    contentWindow = { postMessage: (request) => messages.push({ frame: this, request }) };
    removed = false;
    setAttribute() {}
    remove() { this.removed = true; }
  }
  const globals = { window: globalThis.window, document: globalThis.document, getComputedStyle: globalThis.getComputedStyle };
  Object.assign(globalThis, {
    window: page,
    document: { createElement: () => new Frame(), body: { append: (frame) => frames.push(frame) } },
    getComputedStyle: () => ({ getPropertyValue: () => '' }),
  });
  t.after(() => Object.assign(globalThis, globals));
  const transient = (error) => error instanceof DiagramError && error.transient;
  const loading = renderDiagram('load failure');
  const alsoLoading = renderDiagram('another load failure');
  const loadingChecks = [assert.rejects(loading, transient), assert.rejects(alsoLoading, transient)];
  t.mock.timers.tick(10_000);
  await Promise.all(loadingChecks);
  assert.equal(frames[0].removed, true);
  assert.equal(messages.length, 0);

  const stalled = renderDiagram('stalled');
  const queued = renderDiagram('queued behind stall');
  const stallChecks = [assert.rejects(stalled, transient), assert.rejects(queued, transient)];
  frames[1].dispatchEvent(new Event('load'));
  await Promise.resolve();
  assert.equal(messages.length, 1);
  t.mock.timers.tick(10_000);
  await Promise.all(stallChecks);
  assert.equal(frames[1].removed, true);
  assert.equal(messages.length, 1, 'no request is posted to the discarded frame');

  const retry = renderDiagram('queued behind stall');
  frames[2].dispatchEvent(new Event('load'));
  await Promise.resolve();
  const { frame, request } = messages.at(-1);
  const reply = { id: request.id, svg: '<svg/>', width: 100, height: 50 };
  page.dispatchEvent(Object.assign(new Event('message'), { source: frame.contentWindow, data: reply }));
  assert.deepEqual(await retry, { svg: '<svg/>', width: 100, height: 50 });
  assert.equal(renderDiagram('queued behind stall'), retry, 'successful results stay cached');
});
