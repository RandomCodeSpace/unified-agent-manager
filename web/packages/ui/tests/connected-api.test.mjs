import assert from 'node:assert/strict';
import test from 'node:test';
import { registerHooks } from 'node:module';
const hooks = registerHooks({ resolve(specifier, context, next) { return next(['./lib/models', './lib/reads', './lib/preview'].includes(specifier) ? `${specifier}.ts` : specifier, context); } });
const { api, createApiClient, onUnauthorized, errorCode } = await import('../src/api.ts');
hooks.deregister();
const connection = (id = 'remote-b') => ({ id, instance_id: `instance-${id}`, label: id, enabled: true, generation: 3, capabilities: ['files-v1', 'terminal-v1', 'configuration-v1', 'provider-accounts-v1', 'routines-v1', 'usage-v1'] });

test('GitLab reviews require their own capability and retain connection ownership on every route', async () => {
  const original = globalThis.fetch, requests = [];
  globalThis.fetch = async (url, options) => { requests.push({ url, options }); return new Response('{}'); };
  try {
    const pipelineOnly = createApiClient({ ...connection(), capabilities: ['gitlab-pipelines-v1'] });
    await assert.rejects(pipelineOnly.gitlabReview('same/id', 42, 'abc'), error => error.status === 412);
    await assert.rejects(pipelineOnly.postGitlabComment('same/id', 42, { commit_sha: 'abc', body: 'reply' }), error => error.status === 412);
    await assert.rejects(pipelineOnly.resolveGitlabThread('same/id', 42, { commit_sha: 'abc', discussion_id: 'thread', resolved: true }), error => error.status === 412);
    await assert.rejects(pipelineOnly.gitlabReviewAction('same/id', 42, { commit_sha: 'abc', discussion_id: 'thread', action: 'fix', request_id: 'request', mode: 'send' }), error => error.status === 412);
    assert.equal(requests.length, 0);
    const client = createApiClient({ ...connection(), capabilities: ['gitlab-reviews-v1'] });
    await client.gitlabReview('same/id', 42, 'sha/encoded');
    await client.postGitlabComment('same/id', 42, { commit_sha: 'abc', discussion_id: 'thread', body: 'reply' });
    await client.resolveGitlabThread('same/id', 42, { commit_sha: 'abc', discussion_id: 'thread', resolved: true });
    await client.gitlabReviewAction('same/id', 42, { commit_sha: 'abc', discussion_id: 'thread', action: 'fix', request_id: 'request', mode: 'queue' });
    assert.equal(requests[0].url, '/api/connected/remote-b/api/sessions/same%2Fid/gitlab/merge-requests/42?commit_sha=sha%2Fencoded&uam_generation=3');
    for (const request of requests) assert.match(request.url, /^\/api\/connected\/remote-b\/api\/sessions\/same%2Fid\/gitlab\/merge-requests\/42[/?].*uam_generation=3$/);
    assert.equal(JSON.parse(requests[3].options.body).mode, 'queue');
  } finally { globalThis.fetch = original; }
});

test('colliding task and project IDs always route through the captured owner and generation', async () => {
  const original = globalThis.fetch, requests = [];
  globalThis.fetch = async (url, options) => { requests.push({ url, options }); return new Response('{}'); };
  try {
    const b = createApiClient(connection()), c = createApiClient(connection('remote-c'));
    await Promise.all([b.prompt('same/id', 'B', 'request-b'), c.prompt('same/id', 'C', 'request-c'), api.prompt('same/id', 'A', 'request-a')]);
    await b.routines('same-project');
    await c.configuration('same-project');
    assert.match(requests[0].url, /^\/api\/connected\/remote-b\/api\/sessions\/same%2Fid\/prompt\?uam_generation=3$/);
    assert.match(requests[1].url, /^\/api\/connected\/remote-c\//);
    assert.equal(requests[2].url, '/api/sessions/same%2Fid/prompt');
    assert.match(requests[3].url, /^\/api\/connected\/remote-b\/api\/projects\/same-project\/routines\?uam_generation=3$/);
    assert.match(requests[4].url, /^\/api\/connected\/remote-c\/api\/configuration\?project_id=same-project&uam_generation=3$/);
    assert.ok(requests.every(request => request.options.credentials === 'same-origin'));
  } finally { globalThis.fetch = original; }
});

test('old clients fail closed before issuing requests after a generation is invalidated', async () => {
  const original = globalThis.fetch; let current = true, calls = 0;
  globalThis.fetch = async () => { calls++; return new Response('{}'); };
  try {
    const b = createApiClient(connection(), () => current);
    current = false;
    await assert.rejects(b.prompt('same', 'stale text', 'request'), error => error.status === 409 && errorCode(error) === 'connection_changed');
    await assert.rejects(b.exportMarkdown('same'), error => error.status === 409);
    assert.match(b.eventsUrl('same'), /uam_generation=3$/, 'native URLs retain their old generation so the server rejects them');
    assert.equal(calls, 0);
  } finally { globalThis.fetch = original; }
});

test('remote authentication loss cannot sign out the hosting UAM for JSON, exports or file reads', async () => {
  const original = globalThis.fetch; let signedOut = 0;
  onUnauthorized(() => signedOut++);
  globalThis.fetch = async () => new Response(JSON.stringify({ error: 'Remote key expired', code: 'remote_auth_required' }), { status: 424 });
  try {
    const b = createApiClient(connection());
    for (const read of [() => b.meta(), () => b.exportMarkdown('task'), () => b.filePreview(b.viewFileUrl('task', 'a.txt'), new AbortController().signal)]) {
      await assert.rejects(read(), error => errorCode(error) === 'remote_auth_required');
    }
    assert.equal(signedOut, 0);
    globalThis.fetch = async () => new Response('{}', { status: 401 });
    await assert.rejects(b.meta());
    assert.equal(signedOut, 1, 'a real home-cookie 401 still signs out the home UI');
  } finally { globalThis.fetch = original; onUnauthorized(() => {}); }
});

test('resource and stream URLs retain owner, generation and encoded identifiers', () => {
  const b = createApiClient(connection());
  for (const url of [b.attachmentUrl('same/id', 'a/b'), b.rawFileUrl('same', '/tmp/a b'), b.viewFileUrl('same', 'dir/a b.html'), b.eventsUrl('same'), b.detailEventsUrl('same', 'agent', [], undefined, 'epoch'), b.url('/api/projects/p/terminal?cols=80&rows=24')]) {
    const parsed = new URL(url, 'https://home.test');
    assert.ok(parsed.pathname.startsWith('/api/connected/remote-b/api/'));
    assert.equal(parsed.searchParams.get('uam_generation'), '3');
  }
  const signed = '/api/connected/remote-b/grants/task/grant/signed';
  assert.equal(b.url(signed), signed);
  assert.throws(() => b.url('https://remote.test/api/sessions/x'));
});

test('owner storage and archives cannot collide while existing local keys stay compatible', () => {
  const b = createApiClient(connection()), c = createApiClient(connection('remote-c'));
  assert.equal(api.storageKey('uam.draft.same'), 'uam.draft.same');
  assert.equal(api.cacheKey('same'), 'same');
  assert.notEqual(b.storageKey('uam.draft.same'), c.storageKey('uam.draft.same'));
  assert.notEqual(b.cacheKey('same'), c.cacheKey('same'));
  assert.ok(!b.storageKey('uam.draft.same').startsWith('uam.draft.'));
});

test('missing optional capability fails with an explicit unavailable error without network traffic', async () => {
  const original = globalThis.fetch; let calls = 0;
  globalThis.fetch = async () => { calls++; return new Response('{}'); };
  try {
    const b = createApiClient({ ...connection(), capabilities: [] });
    assert.equal(b.supports('configuration-v1'), false);
    await assert.rejects(b.configuration(), error => errorCode(error) === 'feature_unavailable' && /does not support configuration/.test(error.message));
    assert.match(b.viewFileUrl('task', 'a.html'), /^\/api\/connected\/remote-b\//, 'rendering an unavailable native link must not throw');
    await assert.rejects(b.filePreview(b.viewFileUrl('task', 'a.html'), new AbortController().signal), error => errorCode(error) === 'feature_unavailable');
    assert.equal(calls, 0);
  } finally { globalThis.fetch = original; }
});

test('GitLab status uses the selected instance and refuses older instances', async () => {
  const original = globalThis.fetch, requests = [];
  globalThis.fetch = async (url) => { requests.push(url); return new Response('{}'); };
  try {
    const old = createApiClient(connection());
    await assert.rejects(old.gitlabStatus(), error => errorCode(error) === 'feature_unavailable');
    assert.equal(requests.length, 0);
    const remote = createApiClient({ ...connection(), capabilities: ['gitlab-status-v1'] });
    await remote.gitlabStatus('project/id');
    assert.equal(requests[0], '/api/connected/remote-b/api/glab?project_id=project%2Fid&uam_generation=3');
  } finally { globalThis.fetch = original; }
});

test('GitLab pipelines keep the owning instance and exact commit in every operation', async () => {
  const original = globalThis.fetch, requests = [];
  globalThis.fetch = async (url, init) => { requests.push({ url, init }); return new Response('{}'); };
  try {
    const old = createApiClient(connection());
    await assert.rejects(old.gitlabPipeline('task'), error => errorCode(error) === 'feature_unavailable');
    await assert.rejects(old.investigateGitlabPipeline('task', 'sha', 3), error => errorCode(error) === 'feature_unavailable');
    assert.equal(requests.length, 0);
    const remote = createApiClient({ ...connection(), capabilities: ['gitlab-pipelines-v1'] });
    await remote.gitlabPipeline('task/id');
    await remote.refreshGitlabPipeline('task/id');
    await remote.investigateGitlabPipeline('task/id', 'exact-sha', 1842);
    await remote.gitlabJobTrace('task/id', 22, 'exact-sha', 1842);
    assert.deepEqual(requests.map(r => r.url), [
      '/api/connected/remote-b/api/sessions/task%2Fid/gitlab?uam_generation=3',
      '/api/connected/remote-b/api/sessions/task%2Fid/gitlab/refresh?uam_generation=3',
      '/api/connected/remote-b/api/sessions/task%2Fid/gitlab/investigate?uam_generation=3',
      '/api/connected/remote-b/api/sessions/task%2Fid/gitlab/jobs/22/trace?commit_sha=exact-sha&pipeline_id=1842&uam_generation=3',
    ]);
    assert.equal(requests[1].init.method, 'POST');
    assert.deepEqual(JSON.parse(requests[2].init.body), { commit_sha: 'exact-sha', pipeline_id: 1842 });
  } finally { globalThis.fetch = original; }
});

test('a route an older connected instance predates reads as a missing feature, not a failure', async () => {
  const original = globalThis.fetch;
  globalThis.fetch = async () => Response.json({ error: 'this route is not available to connected instances' }, { status: 403 });
  try {
    const old = createApiClient(connection());
    await assert.rejects(old.taskTimeSplit("task"), error => error.status === 412 && errorCode(error) === 'feature_unavailable' && /does not support this yet/.test(error.message));
    await assert.rejects(api.taskTimeSplit("task"), error => error.status === 403);
  } finally { globalThis.fetch = original; }
});
