import assert from 'node:assert/strict';
import test from 'node:test';
import { registerHooks } from 'node:module';

const hooks = registerHooks({ resolve(specifier, context, next) { return next(['./lib/models', './lib/reads', './lib/preview'].includes(specifier) ? `${specifier}.ts` : specifier, context); } });
const { createApiClient, onUnauthorized, errorCode } = await import('../src/api.ts');
hooks.deregister();

const connection = (id) => ({ id, instance_id: `instance-${id}`, label: id, enabled: true, generation: 7, capabilities: ['files-v1'] });

function xhrFixture() {
  const original = globalThis.XMLHttpRequest;
  const requests = [];
  globalThis.XMLHttpRequest = class {
    upload = {};
    headers = new Map();
    constructor() { requests.push(this); }
    open(method, url) { this.method = method; this.url = url; }
    setRequestHeader(name, value) { this.headers.set(name, value); }
    send(body) { this.body = body; }
    respond(status, data) { this.status = status; this.statusText = ''; this.responseText = JSON.stringify(data); this.onload?.(); }
    abort() { this.onabort?.(); }
  };
  return { requests, restore: () => { globalThis.XMLHttpRequest = original; onUnauthorized(() => {}); } };
}

test('uploads preserve captured owner, generation, body, and progress with colliding task IDs', async () => {
  const fixture = xhrFixture();
  try {
    const b = createApiClient(connection('b')), c = createApiClient(connection('c'));
    const file = new File(['payload'], 'same name.png');
    const progress = [];
    const first = b.upload('same/task', file, value => progress.push(value), 'model/a');
    const second = c.upload('same/task', file, () => {});
    const [bx, cx] = fixture.requests;
    assert.equal(bx.method, 'POST');
    assert.equal(bx.url, '/api/connected/b/api/sessions/same%2Ftask/attachments?name=same%20name.png&model=model%2Fa&uam_generation=7');
    assert.match(cx.url, /^\/api\/connected\/c\//);
    assert.equal(bx.body, file);
    assert.deepEqual([...bx.headers], [['Content-Type', 'application/octet-stream']]);
    bx.upload.onprogress({ lengthComputable: true, loaded: 1, total: 2 });
    bx.upload.onprogress({ lengthComputable: true, loaded: 3, total: 2 });
    assert.deepEqual(progress, [0.5, 1]);
    bx.respond(201, { id: 'b-attachment' });
    cx.respond(201, { id: 'c-attachment' });
    assert.equal((await first.done).id, 'b-attachment');
    assert.equal((await second.done).id, 'c-attachment');
  } finally { fixture.restore(); }
});

test('remote upload authorization loss stays scoped; a real home 401 still signs out', async () => {
  const fixture = xhrFixture();
  let logouts = 0;
  const errors = [];
  onUnauthorized(() => logouts++);
  try {
    const b = createApiClient(connection('b'), () => true, error => errors.push(error));
    const refused = b.upload('task', new File(['x'], 'x.txt'), () => {});
    fixture.requests[0].respond(424, { error: 'Renew remote access', code: 'remote_auth_required' });
    await assert.rejects(refused.done, error => error.status === 424 && errorCode(error) === 'remote_auth_required');
    assert.equal(logouts, 0);
    assert.equal(errors.length, 1);
    const expired = b.upload('task', new File(['x'], 'x.txt'), () => {});
    fixture.requests[1].respond(401, { error: 'authentication required' });
    await assert.rejects(expired.done, error => error.status === 401);
    assert.equal(logouts, 1);
  } finally { fixture.restore(); }
});

test('invalidated upload clients never send; user cancellation does not report the source offline', async () => {
  const fixture = xhrFixture();
  let current = true;
  const errors = [];
  try {
    const b = createApiClient(connection('b'), () => current, error => errors.push(error));
    const upload = b.upload('task', new File(['x'], 'x.txt'), () => {});
    upload.abort();
    await assert.rejects(upload.done, /Upload cancelled/);
    assert.equal(errors.length, 0);
    current = false;
    const stale = b.upload('task', new File(['x'], 'x.txt'), () => {});
    await assert.rejects(stale.done, error => error.status === 409 && errorCode(error) === 'connection_changed');
    assert.equal(fixture.requests.filter(request => request.body).length, 1);
  } finally { fixture.restore(); }
});

test('canceling a fetch preserves AbortError without reporting a disconnected instance', async () => {
  const original = globalThis.fetch;
  const errors = [];
  globalThis.fetch = async (_url, { signal }) => new Promise((_resolve, reject) => {
    const abort = () => reject(new DOMException('Canceled', 'AbortError'));
    if (signal.aborted) abort();
    else signal.addEventListener('abort', abort, { once: true });
  });
  try {
    const b = createApiClient(connection('b'), () => true, error => errors.push(error));
    const controller = new AbortController();
    const read = b.history('task', 'cursor', controller.signal);
    controller.abort();
    await assert.rejects(read, error => error.name === 'AbortError');
    assert.equal(errors.length, 0);
  } finally { globalThis.fetch = original; }
});
