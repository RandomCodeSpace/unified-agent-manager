// GitLab through glab (docs/web.md, "GitLab command bounds"). The pack has no GitLab, so each case runs a
// service of its own with a stand-in glab first on its PATH: it records every call and answers the few
// reads these cases need, as a signed-in glab would.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import type { ChatCompletionRequest } from '@copilotkit/aimock';
import { type Browser } from '@playwright/test';
import { conversation, expect, send, test, threadState, toolResult } from './harness';
import { openSettings } from './settings-helpers';
import { ownService } from './shell-helpers';
import { bash, commit, git, startYoloThread } from './workspace-helpers';

const HOST = 'gitlab.example.invalid';

/** What the stand-in glab answers to `glab api <endpoint>`: JSON, or the text of a job log. */
type Api = Record<string, unknown>;
const ACCOUNT: Api = {
  user: { id: 7, username: 'tester', name: 'Test Er' },
  'personal_access_tokens/self': { id: 3, scopes: ['api', 'read_repository'], expires_at: '2027-01-31' },
  'projects/group%2Fproj': { id: 42, path_with_namespace: 'group/proj', permissions: { project_access: { access_level: 40 } } },
};

const FAKE = `#!/usr/bin/env node
const fs = require('fs');
const [log, host, apiFile] = process.argv.slice(2, 5);
const args = process.argv.slice(5);
const at = args.indexOf('--description-file');
const description = at >= 0 && fs.existsSync(args[at + 1]) ? fs.readFileSync(args[at + 1], 'utf8') : null;
fs.appendFileSync(log, JSON.stringify({ args, description }) + '\\n');
const [a, b] = args;
if (a === 'auth' && b === 'status') process.exit(0);
if (a === 'config' && b === 'get') { console.log(host); process.exit(0); }
if (a === 'ci' && b === 'lint') {
  console.log(args.find((x) => x.startsWith('./')) + ' is invalid.');
  console.log('jobs config should contain at least one visible job');
  process.exit(1);
}
if (a === 'api') {
  // uam's item reads put the endpoint first and ask for the headers; its status check puts it last.
  const endpoint = args[1].startsWith('-') ? args[args.length - 1] : args[1];
  const all = JSON.parse(fs.readFileSync(apiFile, 'utf8'));
  const body = all[endpoint] ?? all[endpoint.split('?')[0]];
  const text = body === undefined ? '{"message":"404 Not found"}' : typeof body === 'string' ? body : JSON.stringify(body);
  if (args.includes('--include')) process.stdout.write((body === undefined ? 'HTTP/2.0 404 Not Found' : 'HTTP/2.0 200 OK') + '\\r\\nContent-Type: application/json\\r\\n\\r\\n' + text);
  else if (body === undefined) process.exit(1);
  else process.stdout.write(text);
  process.exit(0);
}
if (a === 'mr' && b === 'create') console.log('https://' + host + '/group/proj/-/merge_requests/1');
`;

/** A service of the test's own whose glab is the stand-in answering `api`, with `dir` (a GitLab origin) as its Project. */
async function glabService(browser: Browser, mockURL: string, dir: string, api: Api = {}) {
  const bin = fs.mkdtempSync(path.join(os.tmpdir(), 'uam-e2e-glab-'));
  const log = path.join(bin, 'calls.jsonl');
  const script = path.join(bin, 'fake-glab.js');
  const apiFile = path.join(bin, 'api.json');
  fs.writeFileSync(script, FAKE, { mode: 0o755 });
  fs.writeFileSync(apiFile, JSON.stringify({ ...ACCOUNT, ...api }));
  fs.writeFileSync(path.join(bin, 'glab'), `#!/bin/sh\nexec node '${script}' '${log}' '${HOST}' '${apiFile}' "$@"\n`, { mode: 0o755 });
  fs.writeFileSync(log, '');
  git(dir, 'remote', 'add', 'origin', `https://${HOST}/group/proj.git`);
  git(dir, 'update-ref', 'refs/remotes/origin/main', 'HEAD');
  git(dir, 'symbolic-ref', 'refs/remotes/origin/HEAD', 'refs/remotes/origin/main');
  // The service takes its PATH when it starts, at once inside the call.
  const before = process.env.PATH;
  process.env.PATH = `${bin}${path.delimiter}${before}`;
  const started = ownService(browser, mockURL, dir);
  process.env.PATH = before;
  const own = await started;
  const calls = () => fs.readFileSync(log, 'utf8').split('\n').filter(Boolean).map((l) => JSON.parse(l) as { args: string[]; description: string | null });
  /** Changes what the stand-in answers to `glab api` from now on. */
  const answer = (next: Api) => fs.writeFileSync(apiFile, JSON.stringify({ ...ACCOUNT, ...next }));
  return { ...own, calls, answer, stop: async () => { await own.stop(); fs.rmSync(bin, { recursive: true, force: true }); } };
}

test('Settings → GitLab shows the glab instance, the account and the token, and Check access reads again', async ({ browser, uam, workdir }) => {
  const own = await glabService(browser, uam.mock.url, workdir);
  try {
    const { page } = own;
    await openSettings(page, 'GitLab');
    const status = page.getByRole('status').filter({ hasText: HOST });
    await expect(status).toContainText('Authenticated');
    const facts = page.getByRole('definition');
    await expect(page.getByRole('term').filter({ hasText: 'Account' })).toBeVisible();
    await expect(facts.filter({ hasText: '@tester' })).toHaveText('Test Er @tester');
    await expect(facts.filter({ hasText: 'read_repository' })).toHaveText('apiread_repository');
    await expect(facts.filter({ hasText: '2027-01-31' })).toBeVisible();
    const reads = own.calls().filter((c) => c.args[0] === 'api' && c.args.at(-1) === 'user').length;
    await page.getByRole('button', { name: 'Check access' }).click();
    await expect.poll(() => own.calls().filter((c) => c.args[0] === 'api' && c.args.at(-1) === 'user').length).toBe(reads + 1);
    await expect(status).toContainText('Authenticated');
  } finally {
    await own.stop();
  }
});

test("agents' glab calls: api is refused with a replacement, lists get a page size, CI changes are linted before a push, and mr create gets a description", async ({ browser, uam, model, workdir }) => {
  // uam's notes on a call (a capped list, say) follow its result as a user message, so a follow-up is
  // matched on the call's result coming after the turn's prompt, the newest of these, rather than on the last message.
  const PROMPTS = ['hello gitlab', 'use glab', 'push the ci', 'open a merge request'];
  const turn = (prompt: string, calls: ReturnType<typeof bash>[], final: string) => {
    const after = (id: string) => ({ predicate: (req: ChatCompletionRequest) => {
      const said = (i: number) => JSON.stringify(req.messages[i]?.content ?? '');
      const p = req.messages.findLastIndex((m, i) => m.role === 'user' && PROMPTS.some((x) => said(i).includes(x)));
      return p >= 0 && said(p).includes(prompt) && req.messages.findLastIndex((m) => m.role === 'tool' && m.tool_call_id === id) > p;
    } });
    const round = (c: ReturnType<typeof bash>) => ({ toolCalls: [{ id: c.id, name: c.name, arguments: JSON.stringify(c.arguments) }] });
    model.on(after(calls.at(-1)!.id), { content: final }, {}, `final of ${prompt}`);
    for (let i = calls.length - 1; i > 0; i--) model.on(after(calls[i - 1].id), round(calls[i]), {}, `round ${i + 1} of ${prompt}`);
    model.on({ userMessage: prompt }, round(calls[0]), {}, `round 1 of ${prompt}`);
  };
  model.reply('hello gitlab', 'Hello from GitLab.');
  turn('use glab', [bash('call_api', 'glab api projects'), bash('call_lists', 'glab mr list && glab issue list -P 50')], 'Used glab.');
  turn('push the ci', [bash('call_ci', `printf 'nothing: here\\n' > .gitlab-ci.yml && ${commit('ci: add pipeline', '.gitlab-ci.yml')}`), bash('call_push', 'git push -u origin HEAD')], 'Tried the push.');
  turn('open a merge request', [bash('call_mr', `git switch -q -c feat/d && echo d > d.txt && ${commit('feat: add d', 'd.txt')} && glab mr create --title 'Add d' -y`)], 'Opened it.');
  const own = await glabService(browser, uam.mock.url, workdir);
  try {
    const { page } = own;
    await startYoloThread(page, own.project, 'hello gitlab');
    const conv = conversation(page);
    await expect(conv.getByText('Hello from GitLab.')).toBeVisible();
    // The shell rules apply once uam has seen glab signed in for the origin's host, at Thread open.
    await expect.poll(() => own.calls().some((c) => c.args.join(' ') === `auth status --hostname ${HOST}`)).toBe(true);

    await send(page, 'use glab');
    await expect(conv.getByText('Used glab.')).toBeVisible();
    expect(toolResult(model, 'call_api')).toContain('uam glab: use glab issue list, glab mr list, or glab ci get instead of glab api/search.');
    const ran = () => own.calls().map((c) => c.args.join(' '));
    expect(ran()).not.toContain('api projects');
    expect(ran()).toContain('mr list -P 10');
    expect(ran()).toContain('issue list -P 20');

    await send(page, 'push the ci');
    await expect(conv.getByText('Tried the push.')).toBeVisible();
    expect(ran()).toContain(`ci lint -R https://${HOST}/group/proj ./.gitlab-ci.yml --dry-run --ref main --include-jobs`);
    expect(toolResult(model, 'call_push')).toContain('uam CI config invalid (.gitlab-ci.yml):\njobs config should contain at least one visible job');
    // Refused, the push never ran: origin's ref is where it was.
    expect(git(workdir, 'rev-parse', 'refs/remotes/origin/main')).toBe(git(workdir, 'rev-parse', 'HEAD~1'));

    await send(page, 'open a merge request');
    await expect(conv.getByText('Opened it.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    const create = own.calls().find((c) => c.args[0] === 'mr' && c.args[1] === 'create')!;
    const at = create.args.indexOf('--description-file');
    expect(create.args.slice(0, 5)).toEqual(['mr', 'create', '--title', 'Add d', '-y']);
    expect(at).toBeGreaterThan(0);
    expect(create.description?.trim()).toBeTruthy();
    // The file is removed when the call ends.
    await expect.poll(() => fs.existsSync(create.args[at + 1])).toBe(false);
    await openSettings(page, 'Background AI');
    await page.getByRole('button', { name: 'Show log' }).click();
    const logged = page.getByRole('listitem').filter({ hasText: 'Merge request description' });
    await expect(logged).toHaveCount(1);
    await expect(logged).not.toContainText(/Skipped|Failed/);
  } finally {
    await own.stop();
  }
});

const WEB = `https://${HOST}/group/proj/-`;
/** A merge request with a running head pipeline, a failed pipeline with a failed test job and a runner failure, and an open issue. */
const ITEMS: Api = {
  'projects/42/merge_requests/2': { iid: 2, project_id: 42, title: 'Draft: Add d', state: 'opened', draft: true, web_url: `${WEB}/merge_requests/2`, source_branch: 'feat/d', target_branch: 'main', head_pipeline: { id: 90, status: 'running', ref: 'feat/d', sha: 'def', web_url: `${WEB}/pipelines/90` } },
  'projects/42/pipelines/91': { id: 91, project_id: 42, ref: 'main', sha: 'abc', status: 'failed', web_url: `${WEB}/pipelines/91` },
  'projects/42/jobs/501': { id: 501, name: 'unit', stage: 'test', status: 'failed', web_url: `${WEB}/jobs/501`, pipeline: { id: 91, project_id: 42, sha: 'abc' } },
  'projects/42/jobs/501/trace': 'Running tests\n--- FAIL: TestOne (0.00s)\n--- FAIL: TestTwo (0.00s)\nFAIL\n',
  'projects/42/jobs/502': { id: 502, name: 'e2e', stage: 'test', status: 'failed', failure_reason: 'runner_system_failure', web_url: `${WEB}/jobs/502`, pipeline: { id: 91, project_id: 42, sha: 'abc' } },
  'projects/42/merge_requests/2/discussions': [{ id: 'd1'.padEnd(40, '0'), individual_note: false, notes: [{ id: 1, body: 'Please **rename** this.', author: { id: 9, username: 'rev', name: 'Rev Iewer' }, created_at: '2026-11-20T10:00:00Z', resolvable: true, resolved: false }] }],
  'projects/42/pipelines/91/jobs': [
    { id: 500, name: 'compile', stage: 'build', status: 'success', web_url: `${WEB}/jobs/500`, pipeline: { id: 91, project_id: 42, sha: 'abc', ref: 'main' } },
    { id: 501, name: 'unit', stage: 'test', status: 'failed', web_url: `${WEB}/jobs/501`, pipeline: { id: 91, project_id: 42, sha: 'abc', ref: 'main' } },
  ],
  'projects/42/issues/14': { iid: 14, project_id: 42, title: 'Login breaks', state: 'opened', issue_type: 'issue', web_url: `${WEB}/issues/14` },
};

test('a reply that touched GitLab items carries one chip per item with its state and failure kind, and a chip opens the item in the GitLab panel', async ({ browser, uam, model, workdir }) => {
  model.reply('gitlab items', `See ${WEB}/merge_requests/2, ${WEB}/pipelines/91, ${WEB}/jobs/501, ${WEB}/jobs/502 and ${WEB}/issues/14.`);
  const own = await glabService(browser, uam.mock.url, workdir, ITEMS);
  try {
    const { page } = own;
    await startYoloThread(page, own.project, 'gitlab items');
    await expect(threadState(page, 'Finished')).toBeVisible();
    // In the order the turn named them, each with its state; a failed job says what failed.
    const chips = conversation(page).getByRole('group', { name: 'GitLab items in this turn' }).getByRole('link');
    const names = ['!2 draft pipeline running', 'Pipeline #91 failed', 'unit failed Tests failed · 2 tests', 'e2e failed Runner failure · not your change', '#14 open'];
    await expect(chips).toHaveCount(names.length);
    for (const [i, name] of names.entries()) await expect(chips.nth(i)).toHaveAccessibleName(name);
    // GitLab's runner failure reason names it without a log read; the test job's log is read for its count.
    const read = own.calls().map((c) => c.args[1]);
    expect(read).toContain('projects/42/jobs/501/trace');
    expect(read).not.toContain('projects/42/jobs/502/trace');

    await chips.first().click();
    const panel = page.getByRole('complementary', { name: 'GitLab' });
    await expect(panel.getByRole('heading', { name: 'Add d', level: 2 })).toBeVisible();
    await expect(panel.getByRole('region', { name: 'Pipeline' }).getByRole('button', { name: '#90 feat/d Running' })).toBeVisible();
    const note = panel.getByRole('region', { name: 'Discussions' }).getByRole('article');
    await expect(note).toContainText('Rev Iewer');
    // The note renders as Markdown.
    await expect(note.locator('strong')).toHaveText('rename');
    await expect(panel.getByRole('link', { name: 'Open in GitLab' })).toHaveAttribute('href', `${WEB}/merge_requests/2`);

    const items = panel.getByRole('group', { name: "This turn's GitLab items" });
    await items.getByRole('button', { name: '#91' }).click();
    await expect(panel.getByRole('heading', { name: 'Pipeline #91', level: 2 })).toBeVisible();
    const jobs = panel.getByRole('region', { name: 'Jobs' });
    await expect(jobs.getByRole('group', { name: 'Stage build' }).getByRole('button', { name: 'compile success' })).toBeVisible();
    // The failed job is selected, with the end of its log.
    await expect(jobs.getByRole('group', { name: 'Stage test' }).getByRole('button', { name: 'unit failed' })).toHaveAttribute('aria-pressed', 'true');
    const log = panel.getByRole('region', { name: 'unit log' });
    await expect(log).toContainText('--- FAIL: TestTwo');
    await expect(log.getByRole('link', { name: 'Full log' })).toHaveAttribute('href', `${WEB}/jobs/501`);

    await items.getByRole('button', { name: '#14' }).click();
    await expect(panel.getByRole('heading', { name: 'Login breaks', level: 2 })).toBeVisible();
    await expect(panel.getByRole('link', { name: 'Open in GitLab' })).toHaveAttribute('href', `${WEB}/issues/14`);
  } finally {
    await own.stop();
  }
});

test('the GitLab panel links the project, a merge request\'s branches and compare, and a pipeline\'s ref and commit; the project path in a reply links to the project', async ({ browser, uam, model, workdir }) => {
  model.reply('gitlab places', `Pushed to group/proj: ${WEB}/merge_requests/2 and ${WEB}/pipelines/91.`);
  const own = await glabService(browser, uam.mock.url, workdir, ITEMS);
  try {
    const { page } = own;
    await startYoloThread(page, own.project, 'gitlab places');
    await expect(threadState(page, 'Finished')).toBeVisible();
    const project = `https://${HOST}/group/proj`;
    await expect(conversation(page).getByRole('link', { name: 'group/proj', exact: true })).toHaveAttribute('href', project);
    await conversation(page).getByRole('group', { name: 'GitLab items in this turn' }).getByRole('link').first().click();
    const panel = page.getByRole('complementary', { name: 'GitLab' });
    await expect(panel.getByRole('heading', { name: 'Add d', level: 2 })).toBeVisible();
    await expect(panel.getByRole('link', { name: 'group/proj', exact: true })).toHaveAttribute('href', project);
    await expect(panel.getByRole('link', { name: 'feat/d', exact: true })).toHaveAttribute('href', `${WEB}/tree/feat/d`);
    await expect(panel.getByRole('link', { name: 'main', exact: true })).toHaveAttribute('href', `${WEB}/tree/main`);
    await expect(panel.getByRole('link', { name: 'Compare' })).toHaveAttribute('href', `${WEB}/compare/main...feat/d`);
    await panel.getByRole('group', { name: "This turn's GitLab items" }).getByRole('button', { name: '#91' }).click();
    await expect(panel.getByRole('heading', { name: 'Pipeline #91', level: 2 })).toBeVisible();
    await expect(panel.getByRole('link', { name: 'main', exact: true })).toHaveAttribute('href', `${WEB}/tree/main`);
    await expect(panel.getByRole('link', { name: 'abc', exact: true })).toHaveAttribute('href', `${WEB}/commit/abc`);
  } finally {
    await own.stop();
  }
});

test('a running pipeline is read again after 20 s and an open merge request after a minute', async ({ browser, uam, model, workdir }) => {
  // Two real waits: uam's own read of an item is kept 15 s, so a page clock alone would not show a new state.
  test.setTimeout(240_000);
  const mr = { iid: 2, project_id: 42, title: 'Add d', state: 'opened', web_url: `${WEB}/merge_requests/2`, source_branch: 'feat/d', target_branch: 'main' };
  const pipeline = { id: 91, project_id: 42, ref: 'main', sha: 'abc', status: 'running', web_url: `${WEB}/pipelines/91` };
  model.reply('gitlab rereads', `See ${WEB}/merge_requests/2 and ${WEB}/pipelines/91.`);
  const own = await glabService(browser, uam.mock.url, workdir, { 'projects/42/merge_requests/2': mr, 'projects/42/pipelines/91': pipeline });
  try {
    const { page } = own;
    const asked: { at: number; refs: string[] }[] = [];
    page.on('request', (r) => { if (r.url().includes('/gitlab/items?')) asked.push({ at: Date.now(), refs: new URL(r.url()).searchParams.getAll('ref') }); });
    await startYoloThread(page, own.project, 'gitlab rereads');
    await expect(threadState(page, 'Finished')).toBeVisible();
    const chips = conversation(page).getByRole('group', { name: 'GitLab items in this turn' }).getByRole('link');
    await expect(chips.nth(0)).toHaveAccessibleName('!2 open');
    await expect(chips.nth(1)).toHaveAccessibleName('Pipeline #91 running');
    const first = asked.length;
    expect(first).toBeGreaterThan(0);
    const start = asked[first - 1].at;
    own.answer({ 'projects/42/merge_requests/2': { ...mr, state: 'merged' }, 'projects/42/pipelines/91': { ...pipeline, status: 'success' } });

    // The running pipeline alone is asked for again, 20 s on; the merge request keeps what the page last read.
    await expect(chips.nth(1)).toHaveAccessibleName('Pipeline #91 success', { timeout: 60_000 });
    const fast = asked.slice(first);
    expect(fast.map((a) => a.refs.length)).toEqual([1]);
    expect(fast[0].at - start).toBeGreaterThanOrEqual(19_000);
    await expect(chips.nth(0)).toHaveAccessibleName('!2 open');

    // The open merge request is asked for a minute on; the finished pipeline never again.
    await expect(chips.nth(0)).toHaveAccessibleName('!2 merged', { timeout: 90_000 });
    const slow = asked.slice(first + 1);
    expect(slow).toHaveLength(1);
    expect(slow[0].refs).not.toEqual(fast[0].refs);
    expect(slow[0].at - start).toBeGreaterThanOrEqual(59_000);
  } finally {
    await own.stop();
  }
});
