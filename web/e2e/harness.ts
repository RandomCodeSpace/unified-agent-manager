// The browser regression pack's harness (docs/testing.md, "Browser regression pack").
// Each Playwright worker runs its own aimock (the model) and its own uam service
// on free loopback ports with a fresh home; each test gets a fresh Project
// directory and a script for the model scoped to that directory.
import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { LLMock, type ChatCompletionRequest, type Fixture, type FixtureMatch, type FixtureOpts, type FixtureResponse } from '@copilotkit/aimock';
import { test as base, expect, type Page } from '@playwright/test';

export { expect };

/** The custom model every Thread runs on unless a case picks another. */
export const MOCK_MODEL = { name: 'mock', displayName: 'Mock', modelId: 'mock-1' };

/**
 * What aimock answers to every request no script matched: uam's own background
 * calls (titles, suggested replies, the outcome line). No case asserts it.
 */
export const BACKGROUND_REPLY = 'aimock background reply';

/** A model request aimock served, as the case's journal records it. */
export type ServedRequest = { fixture: string; request: ChatCompletionRequest };

type Service = {
  url: string;
  token: string;
  home: string;
  log: string;
  mock: LLMock;
  /** Calls the service's HTTP API signed in. */
  api: (method: string, route: string, body?: unknown) => Promise<unknown>;
  /** The scripts of the running test and the requests it was served. */
  scripts: Fixture[];
  served: ServedRequest[];
  /** Main-agent requests in the test's directory that no script answered. */
  unscripted: ChatCompletionRequest[];
  dir: string;
  apply: () => void;
};

export type Project = { id: string; name: string; dir: string };

type ToolCall = { id: string; name: string; arguments: Record<string, unknown> };

const textOf = (content: unknown): string =>
  typeof content === 'string' ? content
    : Array.isArray(content) ? content.map((p) => (typeof p?.text === 'string' ? p.text : '')).join('') : '';

/** The main agent's requests carry its tools and the session's directory in the system prompt; uam's background calls do neither. */
const isAgentIn = (req: ChatCompletionRequest, dir: string) =>
  (req.tools?.length ?? 0) > 0 && req.messages.some((m) => m.role === 'system' && textOf(m.content).includes(dir));

/** The text of the last user message, which carries the prompt. */
export const lastUserText = (req: ChatCompletionRequest) =>
  textOf([...req.messages].reverse().find((m) => m.role === 'user')?.content);

/**
 * Scripts the model for one test. Every reply answers only the main agent of a
 * Thread in this test's Project directory, so neither another test nor a
 * background call can take it. Register in the order a turn needs them:
 * aimock serves the first script that matches.
 */
export class Model {
  constructor(private readonly svc: Service) {}

  /** Answers a request matching `match` (plus this test's directory) with `response`. */
  on(match: FixtureMatch, response: FixtureResponse, opts: FixtureOpts = {}, label = JSON.stringify(match)): this {
    const dir = this.svc.dir;
    const own = match.predicate;
    return this.add({ ...match, predicate: (req) => isAgentIn(req, dir) && (!own || own(req)) }, response, opts, label);
  }

  /**
   * Answers uam's background title request for the Thread whose first message
   * contains `prompt` (a background call: no tools, no directory).
   */
  title(prompt: string, title: string): this {
    const isTitle = (req: ChatCompletionRequest) => !req.tools?.length && req.messages.some((m) => m.role === 'system' && textOf(m.content).includes('title'));
    return this.add({ userMessage: prompt, predicate: isTitle }, { content: title }, {}, `title ${prompt}`);
  }

  private add(match: FixtureMatch, response: FixtureResponse, opts: FixtureOpts, label: string): this {
    const svc = this.svc;
    svc.scripts.push({
      ...opts,
      match,
      response: (req) => {
        svc.served.push({ fixture: label, request: req });
        return response;
      },
    });
    svc.apply();
    return this;
  }

  /** Answers the prompt containing `prompt` with `text`. opts slow it down (latency per chunk in ms, chunkSize in characters). */
  reply(prompt: string, text: string, opts: FixtureOpts = {}): this {
    return this.on({ userMessage: prompt }, { content: text }, opts, `reply ${prompt}`);
  }

  /**
   * Answers `prompt` with tool calls, then the result of the last call with
   * `final`. The result's script is registered first: the prompt is still the
   * last user message when the results come back, and would match again.
   */
  toolRound(prompt: string, calls: ToolCall[], final: string, opts: FixtureOpts = {}): this {
    const last = calls[calls.length - 1].id;
    this.on({ toolCallId: last }, { content: final }, {}, `result of ${last}`);
    return this.on({ userMessage: prompt }, {
      toolCalls: calls.map((c) => ({ id: c.id, name: c.name, arguments: JSON.stringify(c.arguments) })),
    }, opts, `tools for ${prompt}`);
  }

  /** Answers `prompt` with an upstream error; the CLI retries it before the turn fails. */
  fail(prompt: string, status: number, message: string): this {
    return this.on({ userMessage: prompt }, { status, error: { message, type: status === 429 ? 'rate_limit_error' : 'server_error' } }, {}, `fail ${prompt}`);
  }

  /** Every request of this test that a script answered, oldest first. */
  served(): ServedRequest[] {
    return [...this.svc.served];
  }
}

const workerRoot = (index: number) => fs.mkdtempSync(path.join(os.tmpdir(), `uam-e2e-w${index}-`));

export function binary(): string {
  const bin = process.env.UAM_E2E_BIN;
  if (!bin) throw new Error('UAM_E2E_BIN must name a built uam binary (make build writes bin/uam; make test-ui sets it)');
  if (!path.isAbsolute(bin)) throw new Error(`UAM_E2E_BIN must be an absolute path, got ${bin}`);
  try {
    fs.accessSync(bin, fs.constants.X_OK);
  } catch {
    throw new Error(`UAM_E2E_BIN ${bin} is not an executable file: run make build`);
  }
  const onPath = (process.env.PATH ?? '').split(path.delimiter).some((d) => d && fs.existsSync(path.join(d, 'copilot')));
  if (!onPath) throw new Error('the Copilot CLI (copilot) must be on PATH: npm install -g @github/copilot');
  return bin;
}

/** Starts `uam __web`, the service process `uam web` spawns, so the harness owns its PID, its process group and its readiness report. */
export async function startService(bin: string, root: string, listen = '127.0.0.1:0'): Promise<{ proc: ChildProcess; url: string }> {
  const dirs = { home: path.join(root, 'home'), config: path.join(root, 'config'), run: path.join(root, 'run'), cache: path.join(root, 'cache') };
  for (const d of Object.values(dirs)) fs.mkdirSync(d, { recursive: true, mode: 0o700 });
  // Keeps uam from installing a systemd unit for this service.
  fs.writeFileSync(path.join(dirs.config, 'boot-service-removed'), '');
  const out = fs.openSync(path.join(root, 'stdout.log'), 'a');
  // No GH_*, GITHUB_*, COPILOT_* or XDG_* variable: the CLI stays signed out and touches nothing of the developer's.
  const env = { PATH: process.env.PATH ?? '', HOME: dirs.home, LANG: 'C.UTF-8', UAM_CONFIG_DIR: dirs.config, UAM_SESSION_DIR: dirs.run, UAM_CACHE_DIR: dirs.cache, UAM_WEB_READY_FD: '3' };
  const proc = spawn(bin, ['__web', '--listen', listen], { cwd: dirs.home, env, stdio: ['ignore', out, out, 'pipe'], detached: true });
  fs.closeSync(out);
  const ready = proc.stdio[3] as NodeJS.ReadableStream;
  let report = '';
  ready.setEncoding('utf8');
  ready.on('data', (chunk: string) => { report += chunk; });
  await Promise.race([
    once(ready, 'end'),
    once(proc, 'exit'),
    new Promise((_, reject) => setTimeout(() => reject(new Error('uam did not report ready within 30s')), 30_000)),
  ]);
  if (report.trim() !== 'ok') {
    throw new Error(`uam did not start: ${report.trim() || 'exited'}\n${fs.readFileSync(path.join(root, 'stdout.log'), 'utf8')}`);
  }
  const state = JSON.parse(fs.readFileSync(path.join(dirs.run, 'web.json'), 'utf8')) as { listen: string };
  return { proc, url: `http://${state.listen}` };
}

/** SIGTERM lets uam stop its CLIs; whatever of its process group is left then gets SIGKILL. */
export async function stopService(proc: ChildProcess) {
  if (proc.exitCode === null && proc.signalCode === null) {
    const exited = once(proc, 'exit');
    proc.kill('SIGTERM');
    await Promise.race([exited, new Promise((r) => setTimeout(r, 20_000))]);
  }
  try {
    process.kill(-proc.pid!, 'SIGKILL');
  } catch {
    // The group is already gone.
  }
}

let projects = 0;

export const test = base.extend<{ workdir: string; project: Project; model: Model; journal: void }, { uam: Service }>({
  // eslint-disable-next-line no-empty-pattern -- Playwright reads a fixture's dependencies from this pattern.
  uam: [async ({}, use, workerInfo) => {
    const bin = binary();
    const root = workerRoot(workerInfo.workerIndex);
    const mock = new LLMock({ host: '127.0.0.1', port: 0, logLevel: 'silent' });
    await mock.start();
    const { proc, url } = await startService(bin, root);
    const token = fs.readFileSync(path.join(root, 'config', 'web-token'), 'utf8').trim();
    const login = await fetch(`${url}/api/login`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token }) });
    if (login.status !== 204) throw new Error(`login answered ${login.status}`);
    const cookie = login.headers.getSetCookie().map((c) => c.split(';')[0]).join('; ');
    const api = async (method: string, route: string, body?: unknown) => {
      const res = await fetch(url + route, { method, headers: { 'Content-Type': 'application/json', Cookie: cookie }, body: body === undefined ? undefined : JSON.stringify(body) });
      if (!res.ok) throw new Error(`${method} ${route}: ${res.status} ${await res.text()}`);
      return res.status === 204 ? null : res.json();
    };
    const svc: Service = {
      url, token, mock, api, home: path.join(root, 'home'), log: path.join(root, 'cache', 'uam.log'),
      scripts: [], served: [], unscripted: [], dir: '',
      apply: () => {
        mock.clearFixtures();
        mock.addFixtures([...svc.scripts, catchAll]);
      },
    };
    const catchAll: Fixture = {
      match: {},
      response: (req) => {
        if (svc.dir && isAgentIn(req, svc.dir)) svc.unscripted.push(req);
        return { content: BACKGROUND_REPLY };
      },
    };
    svc.apply();
    await api('PATCH', '/api/settings', {
      custom_models: [{ name: MOCK_MODEL.name, display_name: MOCK_MODEL.displayName, base_url: `${mock.url}/v1`, model_id: MOCK_MODEL.modelId, api_key: 'mock-key', api_key_env: '' }],
    });
    try {
      await use(svc);
    } finally {
      await stopService(proc);
      await mock.stop();
      fs.rmSync(root, { recursive: true, force: true, maxRetries: 3 });
    }
  }, { scope: 'worker', timeout: 90_000 }],

  baseURL: async ({ uam }, use) => use(uam.url),

  // Every page starts signed in; the sign-in case opens its own context.
  context: async ({ context, uam }, use) => {
    const res = await context.request.post(`${uam.url}/api/login`, { data: { token: uam.token } });
    expect(res.status()).toBe(204);
    await use(context);
  },

  workdir: async ({ uam }, use) => {
    const dir = path.join(uam.home, 'projects', `p${++projects}-${Math.random().toString(36).slice(2, 8)}`);
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, 'README.md'), '# Fixture\n');
    const git = (...args: string[]) => execFileSync('git', ['-c', 'user.name=uam e2e', '-c', 'user.email=e2e@example.invalid', ...args], { cwd: dir, stdio: 'pipe' });
    git('init', '-q', '-b', 'main');
    git('add', 'README.md');
    git('commit', '-q', '-m', 'init');
    uam.dir = dir;
    await use(dir);
  },

  project: async ({ uam, workdir }, use) => {
    const p = (await uam.api('POST', '/api/projects', { dir: workdir })) as Project;
    await use(p);
  },

  model: async ({ uam, workdir: _ }, use) => {
    await use(new Model(uam));
  },

  // Runs around every test: resets the scripts, attaches the journal and the
  // service log when the test failed, and fails a test whose agent made a
  // model call no script answered.
  journal: [async ({ uam }, use, testInfo) => {
    const logStart = fs.existsSync(uam.log) ? fs.statSync(uam.log).size : 0;
    uam.scripts = [];
    uam.served = [];
    uam.unscripted = [];
    uam.apply();
    await use();
    if (testInfo.status !== testInfo.expectedStatus) {
      const compact = (r: ChatCompletionRequest) => ({ model: r.model, tools: r.tools?.map((t) => t.function.name), messages: r.messages });
      await testInfo.attach('aimock-journal.json', {
        contentType: 'application/json',
        body: JSON.stringify({ served: uam.served.map((s) => ({ fixture: s.fixture, ...compact(s.request) })), unscripted: uam.unscripted.map(compact), http: uam.mock.getRequests() }, null, 1),
      });
      const log = fs.existsSync(uam.log) ? fs.readFileSync(uam.log).subarray(logStart).toString('utf8') : '';
      await testInfo.attach('uam.log', { contentType: 'text/plain', body: log });
    }
    const unscripted = uam.unscripted.map(lastUserText);
    uam.dir = '';
    uam.scripts = [];
    uam.apply();
    expect(unscripted, 'agent model calls no script answered').toEqual([]);
  }, { auto: true }],
});

/**
 * Opens a new Thread draft in `project` from the sidebar: with more than one
 * Project the New thread palette asks which.
 */
export async function newThread(page: Page, project: Project) {
  await page.getByRole('navigation', { name: 'Threads' }).or(page.getByRole('navigation', { name: 'Sidebar' }))
    .getByRole('button', { name: 'New thread', exact: true }).click();
  const palette = page.getByRole('dialog', { name: 'New thread' });
  const draft = page.getByText(`New thread in ${project.name}`, { exact: true });
  await expect(palette.or(draft)).toBeVisible();
  if (await palette.isVisible()) {
    await palette.getByRole('combobox', { name: 'Search projects' }).fill(project.name);
    await palette.getByRole('option', { name: new RegExp(`^${project.name} `) }).click();
  }
  await expect(draft).toBeVisible();
}

/** Types `text` into the composer and sends it. */
export async function send(page: Page, text: string) {
  await page.getByRole('textbox', { name: 'Message' }).fill(text);
  await page.getByRole('button', { name: 'Send', exact: true }).click();
}

/**
 * Opens the app on Home. Home shows once the first snapshot has loaded; until
 * then the sidebar is open even when it will fold to the rail (no Threads yet).
 */
export async function openHome(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('region', { name: 'Home' })).toBeVisible();
}

/** Opens the app, starts a Thread in `project` with `prompt` and returns its URL. */
export async function startThread(page: Page, project: Project, prompt: string) {
  await openHome(page);
  await newThread(page, project);
  await send(page, prompt);
  await expect(page).toHaveURL(/#task=[0-9a-f-]{36}$/);
  return page.url();
}

/** The tool result the model received for call `id` in the requests a script answered. */
export function toolResult(model: Model, id: string): string | undefined {
  for (const { request } of model.served()) {
    const m = request.messages.find((x) => x.role === 'tool' && x.tool_call_id === id);
    if (m) return textOf(m.content);
  }
  return undefined;
}

/** The open Thread's state, as its header group names it ("State: Finished"). */
export const threadState = (page: Page, state: string) => page.getByRole('group', { name: `State: ${state}` });

/** The open Thread's conversation. */
export const conversation = (page: Page) => page.getByRole('region', { name: 'Conversation' });
