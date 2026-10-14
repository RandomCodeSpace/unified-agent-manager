import assert from 'node:assert/strict';
import test from 'node:test';
import { enableSocketStreams, eventSocketUrl, openEventStream, resetSocketStreams, SocketEventSource, STREAM_SILENCE_MS } from '../src/lib/eventStream.ts';

const sockets = [];
class FakeSocket {
  constructor(url) { this.url = url; this.closed = false; sockets.push(this); }
  close() { this.closed = true; }
  status(code) { this.onmessage({ data: String(code) }); }
  send(text) { this.onmessage({ data: new TextEncoder().encode(text).buffer }); }
  drop() { this.onclose({}); }
}
const natives = [];
class FakeEventSource extends EventTarget {
  constructor(url) { super(); this.url = url; this.readyState = 0; natives.push(this); }
  close() { this.readyState = 2; }
}
globalThis.window = { location: { protocol: 'http:', host: 'uam.test:8260' } };
globalThis.WebSocket = FakeSocket;
globalThis.EventSource = FakeEventSource;

function reset() {
  resetSocketStreams();
  sockets.length = 0;
  natives.length = 0;
}

/** Records what a source tells its listeners, as `type` or `type:data`. */
function heard(source, ...names) {
  const log = [];
  source.onopen = () => log.push(`open@${source.readyState}`);
  source.onerror = () => log.push(`error@${source.readyState}`);
  for (const name of names) source.addEventListener(name, (event) => log.push(`${name}:${event.data}`));
  return log;
}

test('the event socket carries the stream path on the page host and scheme', () => {
  assert.equal(eventSocketUrl({ protocol: 'http:', host: '127.0.0.1:8260' }, '/api/events?session=a&page=p'), 'ws://127.0.0.1:8260/api/events/socket?path=%2Fapi%2Fevents%3Fsession%3Da%26page%3Dp');
  assert.equal(eventSocketUrl({ protocol: 'https:', host: 'example.test' }, '/api/connected/c1/api/events'), 'wss://example.test/api/events/socket?path=%2Fapi%2Fconnected%2Fc1%2Fapi%2Fevents');
});

test('streams go over EventSource until sockets are enabled', () => {
  reset();
  assert.ok(openEventStream('/api/events') instanceof SocketEventSource);
  assert.equal(natives.length, 1);
  assert.equal(sockets.length, 0);
  enableSocketStreams();
  assert.ok(openEventStream('/api/events') instanceof SocketEventSource);
  assert.equal(sockets.length, 1);
  assert.equal(natives.length, 1);
});

test('a 200 opens the stream and its frames arrive as named events, across message boundaries', () => {
  reset();
  enableSocketStreams();
  const source = new SocketEventSource('/api/events');
  const log = heard(source, 'snapshot', 'delta', 'message');
  const [socket] = sockets;
  assert.equal(source.readyState, SocketEventSource.CONNECTING);
  socket.status(200);
  assert.equal(source.readyState, SocketEventSource.OPEN);
  socket.send('retry: 2000\n\nevent: snapshot\ndata: {"a":');
  socket.send('1}\n\n: keep-alive\n\nevent: delta\r\ndata: one\r\ndata: two\r\n\r\ndata: plain\n\n');
  assert.deepEqual(log, ['open@1', 'snapshot:{"a":1}', 'delta:one\ntwo', 'message:plain']);
});

test('a refused stream is closed for good, as EventSource closes on a status other than 200', (t) => {
  reset();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  enableSocketStreams();
  const source = new SocketEventSource('/api/events');
  const log = heard(source);
  sockets[0].status(401);
  sockets[0].drop();
  t.mock.timers.tick(60_000);
  assert.deepEqual(log, ['error@2']);
  assert.equal(sockets.length, 1);
  assert.ok(sockets[0].closed);
});

test('a dropped stream reports the error while reconnecting, after the stream\'s retry time', (t) => {
  reset();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  enableSocketStreams();
  const source = new SocketEventSource('/api/events');
  const log = heard(source, 'snapshot');
  sockets[0].status(200);
  sockets[0].send('retry: 2000\n\nevent: snapshot\ndata: {"half"');
  sockets[0].drop();
  assert.deepEqual(log, ['open@1', 'error@0']);
  t.mock.timers.tick(1999);
  assert.equal(sockets.length, 1);
  t.mock.timers.tick(1);
  assert.equal(sockets.length, 2);
  // The new stream starts clean: the half frame is gone.
  sockets[1].status(200);
  sockets[1].send('event: snapshot\ndata: {"b":2}\n\n');
  assert.deepEqual(log, ['open@1', 'error@0', 'open@1', 'snapshot:{"b":2}']);
});

test('closing in the error handler stops the reconnect', (t) => {
  reset();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  enableSocketStreams();
  const source = new SocketEventSource('/api/events');
  source.onerror = () => source.close();
  sockets[0].status(200);
  sockets[0].drop();
  t.mock.timers.tick(60_000);
  assert.equal(sockets.length, 1);
  assert.equal(source.readyState, SocketEventSource.CLOSED);
});

test('when no socket ever opened on the page, EventSource takes over for this stream and later ones', (t) => {
  reset();
  enableSocketStreams();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const source = openEventStream('/api/events?page=p');
  const log = heard(source, 'snapshot');
  sockets[0].drop();
  t.mock.timers.tick(0);
  assert.equal(natives.length, 1);
  assert.equal(natives[0].url, '/api/events?page=p');
  natives[0].readyState = 1;
  natives[0].onopen(new Event('open'));
  natives[0].dispatchEvent(new MessageEvent('snapshot', { data: '{}' }));
  assert.deepEqual(log, ['error@0', 'open@1', 'snapshot:{}']);
  openEventStream('/api/events');
  assert.equal(natives.length, 2);
  assert.equal(sockets.length, 1);
  source.close();
  assert.equal(natives[0].readyState, 2);
});

test('a socket stream that carries nothing for two and a half heartbeats is dropped and reopened', (t) => {
  reset();
  enableSocketStreams();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const source = openEventStream('/api/events');
  const log = heard(source, 'snapshot');
  sockets[0].status(200);
  sockets[0].send('retry: 2000\n\nevent: snapshot\ndata: {}\n\n');
  t.mock.timers.tick(STREAM_SILENCE_MS - 1);
  // A heartbeat is bytes like any other: the watch starts over.
  sockets[0].send(': keep-alive\n\n');
  t.mock.timers.tick(STREAM_SILENCE_MS - 1);
  assert.deepEqual(log, ['open@1', 'snapshot:{}']);
  assert.equal(sockets.length, 1);
  assert.ok(!sockets[0].closed);
  t.mock.timers.tick(1);
  assert.deepEqual(log, ['open@1', 'snapshot:{}', 'error@0']);
  assert.ok(sockets[0].closed);
  // The dead socket's close event, when the browser gets to it, is not this stream's any more.
  sockets[0].drop();
  t.mock.timers.tick(1999);
  assert.equal(sockets.length, 1);
  t.mock.timers.tick(1);
  assert.equal(sockets.length, 2);
  sockets[1].status(200);
  sockets[1].send('event: snapshot\ndata: {"b":2}\n\n');
  assert.deepEqual(log, ['open@1', 'snapshot:{}', 'error@0', 'open@1', 'snapshot:{"b":2}']);
});

test('an EventSource stream is watched for silence once it has sent a heartbeat as an event', (t) => {
  reset();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const source = openEventStream('/api/events');
  const log = heard(source, 'snapshot');
  natives[0].readyState = 1;
  natives[0].onopen(new Event('open'));
  natives[0].dispatchEvent(new MessageEvent('snapshot', { data: '{}' }));
  // EventSource never shows a comment: a stream whose heartbeats are comments is never taken for silent.
  t.mock.timers.tick(10 * STREAM_SILENCE_MS);
  assert.equal(natives.length, 1);
  natives[0].dispatchEvent(new MessageEvent('keep-alive', { data: '{}' }));
  t.mock.timers.tick(STREAM_SILENCE_MS - 1);
  natives[0].dispatchEvent(new MessageEvent('keep-alive', { data: '{}' }));
  t.mock.timers.tick(STREAM_SILENCE_MS - 1);
  assert.deepEqual(log, ['open@1', 'snapshot:{}']);
  assert.equal(natives.length, 1);
  t.mock.timers.tick(1);
  assert.deepEqual(log, ['open@1', 'snapshot:{}', 'error@0']);
  assert.equal(natives[0].readyState, 2);
  t.mock.timers.tick(3000);
  assert.equal(natives.length, 2);
  natives[1].readyState = 1;
  natives[1].onopen(new Event('open'));
  natives[1].dispatchEvent(new MessageEvent('snapshot', { data: '{"b":2}' }));
  assert.deepEqual(log, ['open@1', 'snapshot:{}', 'error@0', 'open@1', 'snapshot:{"b":2}']);
  // Closed streams watch nothing.
  source.close();
  natives[1].dispatchEvent(new MessageEvent('keep-alive', { data: '{}' }));
  t.mock.timers.tick(10 * STREAM_SILENCE_MS);
  assert.equal(natives.length, 2);
});

test('after a socket has opened, a socket that cannot connect is a dropped connection, not a reason to stop using sockets', (t) => {
  reset();
  enableSocketStreams();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  openEventStream('/api/events');
  sockets[0].status(200);
  const later = openEventStream('/api/events');
  sockets[1].drop();
  t.mock.timers.tick(3000);
  assert.equal(natives.length, 0);
  assert.equal(sockets.length, 3);
  assert.ok(later instanceof SocketEventSource);
});
