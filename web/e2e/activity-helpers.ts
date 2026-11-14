// Helpers for the activity specs: multi-step scripts, subagent requests and the mode menu.
import type { ChatCompletionRequest, FixtureMatch, FixtureOpts } from '@copilotkit/aimock';
import { expect, lastUserText, type Model } from './harness';
import type { Page } from '@playwright/test';

export type Call = { id: string; name: string; arguments: Record<string, unknown> };
/** One model answer: text, or tool calls. */
export type Step = string | Call[];

const answer = (step: Step) => typeof step === 'string'
  ? { content: step }
  : { toolCalls: step.map((c) => ({ id: c.id, name: c.name, arguments: JSON.stringify(c.arguments) })) };

/**
 * True for the request right after the model's call `id`: the last assistant message made it. Unlike
 * aimock's toolCallId this still matches when the CLI adds a user message after the result (it does after `task`).
 */
export const after = (id: string) => (req: ChatCompletionRequest) =>
  !!req.messages.findLast((m) => m.role === 'assistant')?.tool_calls?.some((c) => c.id === id);

/**
 * Scripts a conversation of several model answers: `steps[0]` answers `match`, and each later step answers the
 * request after the previous step's last call. Later steps are registered first, since the first match still
 * matches every later request of the same turn.
 */
export function script(model: Model, match: FixtureMatch, steps: Step[], opts: FixtureOpts = {}, label = 'script') {
  for (let i = steps.length - 1; i > 0; i--) {
    const prev = steps[i - 1];
    if (typeof prev === 'string') throw new Error('only a tool-call step can be followed by another step');
    const own = after(prev[prev.length - 1].id);
    model.on({ predicate: own }, answer(steps[i]), {}, `${label} step ${i}`);
  }
  model.on(match, answer(steps[0]), opts, `${label} step 0`);
}

/** The prompt uam gives a subagent ends with the parent's `task` prompt. */
export const subagentAsked = (prompt: string): FixtureMatch => ({
  predicate: (req) => {
    const text = lastUserText(req);
    return text.includes('You are a subagent in a uam Thread') && text.trimEnd().endsWith(prompt);
  },
});

/** Picks an item of the composer's Permissions and execution menu ("Yolo", "Autopilot", ...). */
export async function pickMode(page: Page, item: string) {
  await page.getByRole('button', { name: /^Permissions and execution/ }).click();
  await page.getByRole('menuitemradio', { name: new RegExp(`^${item} `) }).click();
  const menu = page.getByRole('menu', { name: /^Permissions and execution/ });
  if (await menu.isVisible()) await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();
}
