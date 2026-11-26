// Helpers of the composer cases (composer-*.spec.ts).
import type { ChatCompletionRequest, Fixture } from '@copilotkit/aimock';
import type { Page } from '@playwright/test';
import { lastUserText, MOCK_MODEL, type ServedRequest } from './harness';

/** The parts of the harness's worker service these helpers use. */
type Service = {
  api: (method: string, route: string, body?: unknown) => Promise<unknown>;
  scripts: Fixture[];
  served: ServedRequest[];
  apply: () => void;
  mock: { url: string };
};

/** The provider custom models run under, and the mock model's id there (name/model id). */
const PROVIDER = 'copilot';
const MOCK_MODEL_ID = `${MOCK_MODEL.name}/${MOCK_MODEL.modelId}`;

export const systemText = (req: ChatCompletionRequest) =>
  req.messages.filter((m) => m.role === 'system').map((m) => (typeof m.content === 'string' ? m.content : JSON.stringify(m.content))).join('\n');

/**
 * uam's suggested-reply call (internal/web/assist.go): a background Utility
 * call whose system prompt starts "You predict how the user of a coding
 * agent will answer" and whose user message quotes the last
 * prompt as <user_message>…</user_message>.
 */
export const isSuggestion = (req: ChatCompletionRequest) => systemText(req).includes('You predict how the user of a coding agent');

/** Answers the suggested-reply call for the Thread whose last prompt contains `prompt` with `reply`. */
export function suggest(uam: Service, prompt: string, reply: string) {
  uam.scripts.push({
    match: { predicate: (req) => isSuggestion(req) && lastUserText(req).includes(`<user_message>\n${prompt}`) },
    response: (req) => {
      uam.served.push({ fixture: `suggest ${prompt}`, request: req });
      return { content: JSON.stringify({ reply }) };
    },
  });
  uam.apply();
}

/**
 * Points the Utility model at the mock, so suggested replies run, until the
 * returned function undoes it. Settings belong to the worker's service, so a
 * case restores them before the next case of the worker runs.
 */
export async function useMockUtility(uam: Service): Promise<() => Promise<void>> {
  await uam.api('PATCH', '/api/settings', { title_model: { [PROVIDER]: MOCK_MODEL_ID } });
  return async () => {
    await uam.api('PATCH', '/api/settings', { title_model: { [PROVIDER]: '' } });
  };
}

/** Marks the mock model as taking images until the returned function undoes it. */
export async function useVisionMock(uam: Service): Promise<() => Promise<void>> {
  const set = (vision: boolean) => uam.api('PATCH', '/api/settings', {
    custom_models: [{ name: MOCK_MODEL.name, display_name: MOCK_MODEL.displayName, base_url: `${uam.mock.url}/v1`, model_id: MOCK_MODEL.modelId, api_key: 'mock-key', api_key_env: '', vision }],
  });
  await set(true);
  return async () => {
    await set(false);
  };
}

/** A file as the browser's chooser, a paste or a drop hands it over. */
export type Upload = { name: string; mimeType: string; buffer: Buffer };

/** A 2×2 PNG. */
export const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAEElEQVR4nGP4z8AARAwQCgAf7gP9i18U1AAAAABJRU5ErkJggg==', 'base64');

/** Chooses `files` with the composer's Attach files button. */
export async function attach(page: Page, files: Upload[]) {
  const chooser = page.waitForEvent('filechooser');
  await page.getByRole('button', { name: /^Attach files/ }).click();
  await (await chooser).setFiles(files);
}

/** The sidebar row of the unsettled Thread titled `title`. */
export const sidebarRow = (page: Page, title: string) => page.getByRole('list', { name: 'Unsettled threads' }).getByRole('button').filter({ hasText: title });

/** A second model of the mock's provider, served by the same aimock under its own model id. */
export const SECOND_MODEL = { displayName: 'Claude Two', modelId: 'claude-mock-2' };

/** Offers SECOND_MODEL beside the mock until the returned function undoes it. */
export async function useSecondModel(uam: Service): Promise<() => Promise<void>> {
  const mock = { name: MOCK_MODEL.name, display_name: MOCK_MODEL.displayName, base_url: uam.mock.url + '/v1', model_id: MOCK_MODEL.modelId, api_key: 'mock-key', api_key_env: '' };
  await uam.api('PATCH', '/api/settings', { custom_models: [mock, { ...mock, display_name: SECOND_MODEL.displayName, model_id: SECOND_MODEL.modelId }] });
  return async () => {
    await uam.api('PATCH', '/api/settings', { custom_models: [mock] });
  };
}

/**
 * Answers the `/btw` aside asking `question` with `answer`. The Thread's model answers an aside without tools
 * (the CLI's system prompt starts "You are answering a quick side question"), so no script of `model` matches it.
 */
export function aside(uam: Service, question: string, answer: string) {
  uam.scripts.push({
    match: { predicate: (req) => systemText(req).includes('quick side question') && lastUserText(req).includes(question) },
    response: (req) => {
      uam.served.push({ fixture: `aside ${question}`, request: req });
      return { content: answer };
    },
  });
  uam.apply();
}
