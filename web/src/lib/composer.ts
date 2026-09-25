// Pure text rules for the composer's `/` and `@` pickers. No DOM, so the unit tests run in node.

import type { Command, PromptMode, SendDefault } from '../api';

/**
 * What Enter and Ctrl/Cmd+Enter submit. With no turn running both send. While a turn runs,
 * Enter does the setting's action and the modifier the other; when a steer is impossible
 * (the provider cannot steer) both queue, and the composer says why.
 */
export function enterActions(live: boolean, sendDefault: SendDefault, steerBlocked: boolean): { enter: PromptMode; modified: PromptMode } {
  if (!live) return { enter: 'send', modified: 'send' };
  if (steerBlocked) return { enter: 'queue', modified: 'queue' };
  return sendDefault === 'queue' ? { enter: 'queue', modified: 'steer' } : { enter: 'steer', modified: 'queue' };
}

/** `/` commands, `@` files, and `$` skills: a second way into the `/` list, filtered to skills (issue #186). */
export type TriggerKind = '/' | '@' | '$';

/** The token the caret is in: `[start, end)` of the text, `query` without its sigil. */
export interface Trigger {
  kind: TriggerKind;
  start: number;
  end: number;
  query: string;
}

/**
 * Which picker the caret sits in, if any. `/` and `$` open only as the first character of
 * the text, `@` at the start or after whitespace. The token runs from its sigil to the caret
 * and holds no whitespace: a space after `/review` closes the picker and starts the arguments.
 */
export function triggerAt(text: string, caret: number): Trigger | null {
  const before = text.slice(0, caret);
  let start = before.length;
  while (start > 0 && !/\s/.test(before[start - 1])) start--;
  const token = before.slice(start);
  const sigil = token.charAt(0);
  if (sigil === '/' || sigil === '$') return start === 0 ? { kind: sigil, start, end: caret, query: token.slice(1) } : null;
  if (sigil === '@') return { kind: '@', start, end: caret, query: token.slice(1) };
  return null;
}

/** Replaces the trigger's token with `value` and a space; an existing space is reused and the caret lands after it. */
export function applyPick(text: string, trigger: Trigger, value: string): { text: string; caret: number } {
  const after = text.slice(trigger.end);
  const head = text.slice(0, trigger.start) + value;
  if (after.startsWith(' ')) return { text: head + after, caret: head.length + 1 };
  const space = /^\s/.test(after) ? '' : ' ';
  return { text: head + space + after, caret: head.length + space.length };
}

/** `/name arguments` when `name` is a listed command; null means the text is plain. */
export function parseCommand(text: string, commands: readonly (Pick<Command, 'name'> & Partial<Pick<Command, 'kind'>> & { aliases?: string[] })[]): { name: string; args: string } | null {
  const m = /^([/$])(\S+)(?:\s+([\s\S]*))?$/.exec(text.trim());
  if (!m) return null;
  const offered = commands.filter((c) => m[1] !== '$' || c.kind === 'skill');
  const command = offered.find((c) => c.name === m[2]) ?? offered.find((c) => c.aliases?.includes(m[2]));
  return command ? { name: command.name, args: (m[3] ?? '').trim() } : null;
}

/**
 * True while `text` is shaped like `/name…` and the command list is still on its way
 * (`commands` null, no `error`): sending then would hand an unresolved command to the
 * model as plain text. A loaded list, even an empty one, or a failed fetch never waits.
 */
export function commandPending(text: string, commands: readonly Pick<Command, 'name'>[] | null, error: string | null): boolean {
  return commands === null && !error && /^[/$]\S/.test(text.trim());
}

/** Commands matching `query`: a name starting with it first, then a name or description containing it. Case-insensitive; empty query keeps all. */
export function filterCommands<T extends Pick<Command, 'name' | 'description'> & { aliases?: string[] }>(commands: readonly T[], query: string): T[] {
  const q = query.toLowerCase();
  const rank = (c: T) => {
    if (!q) return 0;
    const names = [c.name, ...(c.aliases ?? [])].map((name) => name.toLowerCase());
    if (names.some((name) => name.startsWith(q))) return 0;
    if (names.some((name) => name.includes(q))) return 1;
    if (c.description.toLowerCase().includes(q)) return 2;
    return -1;
  };
  return commands
    .map((c, i) => ({ c, i, r: rank(c) }))
    .filter((x) => x.r >= 0)
    .sort((a, b) => a.r - b.r || a.i - b.i)
    .map((x) => x.c);
}

/** A known command stays a command even when it cannot run. */
export function commandReason(command: Command | undefined, live: boolean): string {
  if (!command) return '';
  return command.disabled_reason || (live && !command.allow_during_turn ? `/${command.name} runs between turns; wait for this turn to finish.` : '');
}

/**
 * What Enter does while a picker is open: picks the highlighted row; with no row, closes the
 * list, so a half-typed `@path` or `/name` is not sent as text (the next Enter sends). An
 * argument list with nothing to pick stands aside: Enter runs the command as typed.
 */
export function enterInPicker(items: number, argumentList: boolean): 'pick' | 'close' | 'submit' {
  if (items > 0) return 'pick';
  return argumentList ? 'submit' : 'close';
}

/** Literal argument choices replace only the argument before the caret. */
export function argumentTrigger(text: string, caret: number, commands: readonly Command[]): { trigger: Trigger; command: Command } | null {
  const before = text.slice(0, caret);
  const match = /^([/$]\S+\s+)([^\n]*)$/.exec(before);
  if (!match) return null;
  const parsed = parseCommand(match[1].trim(), commands);
  const command = commands.find((c) => c.name === parsed?.name);
  if (!command?.input_choices?.length) return null;
  return { command, trigger: { kind: '/', start: match[1].length, end: caret, query: match[2] } };
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** Matches `@path` as a whole token: at the start or after whitespace, then the end or whitespace. */
const tokenRe = (path: string) => new RegExp(`(^|\\s)@${escapeRe(path)}(?=\\s|$)`);

/** The referenced files whose `@path` token is still in the text; the chip goes when the token goes. */
export function pruneFiles(text: string, files: readonly string[]): string[] {
  return files.filter((f) => tokenRe(f).test(text));
}

/** Removes the first `@path` token and one space after it, so removing a chip also cleans the text. */
export function removeToken(text: string, path: string): string {
  return text.replace(new RegExp(`(^|\\s)@${escapeRe(path)}(?=\\s|$) ?`), '$1');
}
