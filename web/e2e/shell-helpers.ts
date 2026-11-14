// Helpers for the shell-* specs (app shell and navigation).
import type { ChildProcess } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import type { Browser, BrowserContext, Page } from '@playwright/test';
import { binary, expect, MOCK_MODEL, startService, stopService, type Project } from './harness';

/**
 * A service of the test's own, for cases that restart it: the worker's service
 * is shared by every test of the worker. It uses the worker's aimock, so the
 * test's `model` scripts answer it; `dir` is registered as its Project.
 */
export type OwnService = { url: string; context: BrowserContext; page: Page; project: Project; restart: () => Promise<void>; stop: () => Promise<void> };

export async function ownService(browser: Browser, mockURL: string, dir: string): Promise<OwnService> {
  const bin = binary();
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'uam-e2e-own-'));
  let proc: ChildProcess | undefined;
  let context: BrowserContext | undefined;
  const stop = async () => {
    await context?.close();
    if (proc) await stopService(proc);
    fs.rmSync(root, { recursive: true, force: true, maxRetries: 3 });
  };
  try {
    const started = await startService(bin, root);
    proc = started.proc;
    const url = started.url;
    const token = fs.readFileSync(path.join(root, 'config', 'web-token'), 'utf8').trim();
    context = await browser.newContext({ baseURL: url, viewport: { width: 1280, height: 900 } });
    expect((await context.request.post(`${url}/api/login`, { data: { token } })).status()).toBe(204);
    const api = async (method: string, route: string, data?: unknown) => {
      const res = await context!.request.fetch(url + route, { method, data });
      expect(res.ok(), `${method} ${route}: ${res.status()}`).toBe(true);
      return res.status() === 204 ? null : res.json();
    };
    await api('PATCH', '/api/settings', {
      custom_models: [{ name: MOCK_MODEL.name, display_name: MOCK_MODEL.displayName, base_url: `${mockURL}/v1`, model_id: MOCK_MODEL.modelId, api_key: 'mock-key', api_key_env: '' }],
    });
    const project = (await api('POST', '/api/projects', { dir })) as Project;
    const page = await context.newPage();
    // Stops the service and starts it again on the same address with the same state.
    const restart = async () => {
      await stopService(proc!);
      proc = (await startService(bin, root, new URL(url).host)).proc;
    };
    return { url, context, page, project, restart, stop };
  } catch (err) {
    await stop();
    throw err;
  }
}
