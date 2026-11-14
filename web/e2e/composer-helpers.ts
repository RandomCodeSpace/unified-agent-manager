// Helpers of the composer cases (composer-*.spec.ts).
import type { ChatCompletionRequest, Fixture } from '@copilotkit/aimock';
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

const systemText = (req: ChatCompletionRequest) =>
  req.messages.filter((m) => m.role === 'system').map((m) => (typeof m.content === 'string' ? m.content : JSON.stringify(m.content))).join('\n');

/**
 * uam's suggested-reply call (internal/web/assist.go): a background Utility
 * call whose system prompt starts "You predict what the user of a coding
 * agent will most likely send next" and whose user message quotes the last
 * prompt as <user_message>…</user_message>.
 */
export const isSuggestion = (req: ChatCompletionRequest) => systemText(req).includes('You predict what the user of a coding agent');

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
