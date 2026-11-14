// Helpers of the settings cases (settings-*.spec.ts).
import { expect, type Page } from '@playwright/test';

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
