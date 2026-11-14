// Helpers for the workspace specs (changes, rewind, worktrees, git, terminal).
import { execFileSync } from 'node:child_process';
import { expect, type Page } from '@playwright/test';
import { conversation, type Model } from './harness';

/** Runs git in `dir` and returns its trimmed output. */
export const git = (dir: string, ...args: string[]) =>
  execFileSync('git', ['-c', 'user.name=uam e2e', '-c', 'user.email=e2e@example.invalid', ...args], { cwd: dir, stdio: 'pipe' }).toString('utf8').trim();

/**
 * Approves `count` permission requests in the order they show (Safe mode asks
 * for create, edit and bash). Each answered card must leave before the next
 * click: a round's requests show at once, and the next round's can show while
 * the answered card is still leaving.
 */
export async function allowEach(page: Page, count: number) {
  const allow = conversation(page).getByRole('button', { name: 'Allow once' }).first();
  for (let i = 0; i < count; i++) {
    await expect(allow).toBeVisible();
    const button = await allow.elementHandle();
    await button!.click();
    await button!.waitForElementState('hidden');
  }
}

/** The worktrees git lists for `repo` besides the repository itself. */
export function worktrees(repo: string): { path: string; branch: string }[] {
  return git(repo, 'worktree', 'list', '--porcelain').split('\n\n').slice(1).filter(Boolean).map((block) => ({
    path: /^worktree (.*)$/m.exec(block)?.[1] ?? '',
    branch: (/^branch refs\/heads\/(.*)$/m.exec(block)?.[1]) ?? '',
  }));
}

type ToolCall = { id: string; name: string; arguments: Record<string, unknown> };

/**
 * Answers `prompt` with one round of tool calls per entry of `rounds`, each
 * round sent after the result of the previous round's last call, then `final`.
 * The follow-ups are registered first: the prompt stays the last user message.
 */
export function chain(model: Model, prompt: string, rounds: ToolCall[][], final: string) {
  const calls = (round: ToolCall[]) => ({ toolCalls: round.map((c) => ({ id: c.id, name: c.name, arguments: JSON.stringify(c.arguments) })) });
  model.on({ toolCallId: rounds[rounds.length - 1].at(-1)!.id }, { content: final }, {}, `final of ${prompt}`);
  for (let i = rounds.length - 1; i > 0; i--) model.on({ toolCallId: rounds[i - 1].at(-1)!.id }, calls(rounds[i]), {}, `round ${i + 1} of ${prompt}`);
  model.on({ userMessage: prompt }, calls(rounds[0]), {}, `round 1 of ${prompt}`);
}

/** A bash call with the description the CLI requires. */
export const bash = (id: string, command: string): ToolCall => ({ id, name: 'bash', arguments: { command, description: `Run ${id}` } });

/**
 * Commits `files` from an agent shell. uam's git guard refuses blanket staging
 * (`git add -A`), and the service's HOME has no git identity.
 */
export const commit = (message: string, ...files: string[]) => `git add ${files.join(' ')} && git -c user.name=agent -c user.email=agent@example.invalid commit -q -m '${message}'`;

/**
 * Waits until uam has read the native captures of the `index`th turn with
 * changes: its Changed chip then opens the turn's reader. uam reads them once
 * the CLI is idle, retrying for a while; a prompt sent before that leaves them
 * unknown for good, and a rewind sent before then can find the CLI busy.
 */
export async function capturedTurn(page: Page, index: number) {
  await expect(conversation(page).getByRole('button', { name: /^Changed \d+ files?/ }).nth(index)).toHaveAttribute('aria-haspopup', 'dialog', { timeout: 45_000 });
}
