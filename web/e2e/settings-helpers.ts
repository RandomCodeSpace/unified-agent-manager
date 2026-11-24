// Helpers of the settings cases (settings-*.spec.ts).
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import tls from 'node:tls';
import { expect, type Page } from '@playwright/test';
import { binary } from './harness';

type Api = { api: (method: string, route: string, body?: unknown) => Promise<unknown> };

export type Settings = Record<string, unknown> & {
  title_model?: Record<string, string>;
  custom_models?: Record<string, unknown>[];
  task_defaults?: Record<string, string>;
};

export const readSettings = async (uam: Api) => (await uam.api('GET', '/api/settings')) as Settings;

/**
 * What PATCH puts back for a key GET /api/settings leaves out (its default).
 * task_defaults cannot be cleared once set: the default it is put back to is
 * what a new Thread starts with when none is set (the mock, Safe).
 */
const UNSET: Record<string, unknown> = {
  send_default: 'steer', suggest_replies: true, thread_messages: false, terminal: false,
  compact_threshold: null, utility_daily_limit: null, utility_unlimited: false,
  task_defaults: { provider: 'copilot', model: 'mock/mock-1', effort: '', context_size: 'default', mode: 'safe' },
};

/**
 * Reads `keys` of the worker's service settings and returns the function that
 * puts them back. Settings belong to the worker's service, so a case calls it
 * in `finally`. Custom models come back without their keys: the service keeps
 * a saved key for an entry of the same provider name and base URL.
 */
export async function keepSettings(uam: Api, keys: string[]): Promise<() => Promise<void>> {
  const before = await readSettings(uam);
  return async () => {
    const patch: Record<string, unknown> = {};
    for (const key of keys) {
      if (key === 'title_model') patch[key] = { copilot: before.title_model?.copilot ?? '' };
      else if (key === 'custom_models') patch[key] = (before.custom_models ?? []).map(({ key_present: _, ...m }) => m);
      else patch[key] = key in before ? before[key] : UNSET[key];
    }
    // task_defaults names a model, so it goes back before the models do.
    if ('task_defaults' in patch) await uam.api('PATCH', '/api/settings', { task_defaults: patch.task_defaults });
    delete patch.task_defaults;
    await uam.api('PATCH', '/api/settings', patch);
    const after = await readSettings(uam);
    for (const key of keys.filter((k) => k !== 'task_defaults')) expect(after[key], `setting ${key} put back`).toEqual(before[key]);
  };
}

/**
 * Turns off uam's own model calls (titles, suggested replies) until the
 * returned function puts the settings back, so a turn's token counts are the
 * scripted ones alone.
 */
export async function quietBackground(uam: Api): Promise<() => Promise<void>> {
  const restore = await keepSettings(uam, ['title_model', 'suggest_replies']);
  await uam.api('PATCH', '/api/settings', { title_model: { copilot: 'none' }, suggest_replies: false });
  return restore;
}

/** A short random tag: a case names its own models and providers, since the worker's service is shared. */
export const tag = () => Math.random().toString(36).slice(2, 8);

/**
 * Adds models to the mock's provider (they share its endpoint and saved key),
 * beside the custom models already there. A case that calls it puts
 * `custom_models` back with keepSettings.
 */
export async function addMockModels(uam: Api, models: { model_id: string; display_name: string }[]) {
  const saved = (await readSettings(uam)).custom_models!;
  const mock = saved.find((m) => m.model_id === 'mock-1')!;
  await uam.api('PATCH', '/api/settings', {
    custom_models: [...saved.map(({ key_present: _, ...m }) => m), ...models.map((m) => ({ name: mock.name, base_url: mock.base_url, api_key_env: '', ...m }))],
  });
}

/** Picks `item` of the composer's model menu, under the provider group `provider` when given. */
export async function pickModel(page: Page, item: string, provider?: string) {
  await page.getByRole('button', { name: /^Model:/ }).click();
  const menu = page.getByRole('menu');
  await (provider ? menu.getByRole('group', { name: provider }) : menu).getByRole('menuitemradio', { name: item }).click();
  await expect(page.getByRole('button', { name: `Model: ${item}` })).toBeVisible();
}

/** Opens Settings on the page named `label` in its rail. */
export async function openSettings(page: Page, label: string) {
  if (!page.url().endsWith('#settings')) await page.goto('/#settings');
  await page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: label, exact: true }).click();
}

/** A scripted reply whose usage the model reports as these token counts. */
export const withUsage = (content: string, prompt_tokens: number, completion_tokens: number) => ({ content, usage: { prompt_tokens, completion_tokens } });

/** The open Thread's id, from the URL. */
export const threadId = (page: Page) => {
  const id = page.url().split('#task=')[1];
  expect(id).toMatch(/^[0-9a-f-]{36}$/);
  return id;
};

/**
 * A remote MCP server on loopback that offers `tools` (streamable HTTP, JSON
 * answers), for a Copilot user configuration to name. It serves until `close`.
 */
export async function mockMcpServer(tools: { name: string; description: string }[]): Promise<{ url: string; close: () => Promise<void> }> {
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (chunk: Buffer) => { body += chunk.toString(); });
    req.on('end', () => {
      if (req.method !== 'POST') return res.writeHead(405).end();
      const call = JSON.parse(body) as { id?: number; method: string };
      // A notification has no id and gets no answer.
      if (call.id === undefined) return res.writeHead(202).end();
      let result: unknown = {};
      if (call.method === 'initialize') result = { protocolVersion: '2025-03-26', capabilities: { tools: {} }, serverInfo: { name: 'pack', version: '1' } };
      if (call.method === 'tools/list') result = { tools: tools.map((t) => ({ ...t, inputSchema: { type: 'object', properties: {} } })) };
      return res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({ jsonrpc: '2.0', id: call.id, result }));
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as { port: number };
  return {
    url: `http://127.0.0.1:${port}/mcp`,
    close: () => new Promise((resolve) => { server.closeAllConnections(); server.close(() => resolve()); }),
  };
}

/**
 * A program that starts the built uam with `env` added to the fixed environment
 * startService gives it, for ownService. `remove` deletes it.
 */
export function binaryWith(env: Record<string, string>): { bin: string; remove: () => void } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'uam-e2e-env-'));
  const bin = path.join(dir, 'uam');
  const exports = Object.entries(env).map(([k, v]) => `export ${k}='${v}'\n`).join('');
  fs.writeFileSync(bin, `#!/bin/sh\n${exports}exec '${binary()}' "$@"\n`, { mode: 0o755 });
  return { bin, remove: () => fs.rmSync(dir, { recursive: true, force: true }) };
}

/**
 * HTTPS on loopback in front of the plain-HTTP service at `target`, with a
 * self-signed certificate for 127.0.0.1 at `cert` (a service trusts it through
 * SSL_CERT_FILE). It serves until `close`.
 */
export async function tlsFront(target: string): Promise<{ url: string; cert: string; close: () => Promise<void> }> {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'uam-e2e-tls-'));
  const [key, cert] = [path.join(dir, 'key.pem'), path.join(dir, 'cert.pem')];
  execFileSync('openssl', ['req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1', '-nodes', '-days', '1', '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1', '-keyout', key, '-out', cert], { stdio: 'ignore' });
  const { hostname, port } = new URL(target);
  const sockets = new Set<net.Socket>();
  const server = tls.createServer({ key: fs.readFileSync(key), cert: fs.readFileSync(cert) }, (front) => {
    const back = net.connect(Number(port), hostname);
    for (const s of [front, back]) {
      sockets.add(s);
      s.on('error', () => { front.destroy(); back.destroy(); });
      s.on('close', () => sockets.delete(s));
    }
    front.pipe(back).pipe(front);
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  return {
    url: `https://127.0.0.1:${(server.address() as net.AddressInfo).port}`,
    cert,
    close: () => new Promise((resolve) => {
      for (const s of sockets) s.destroy();
      server.close(() => { fs.rmSync(dir, { recursive: true, force: true }); resolve(); });
    }),
  };
}
