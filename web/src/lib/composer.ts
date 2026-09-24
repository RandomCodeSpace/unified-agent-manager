// Pure text rules for the composer's `/` and `@` pickers. No DOM, so the unit tests run in node.

import type { Command } from '../api';

export type TriggerKind = '/' | '@';

/** The token the caret is in: `[start, end)` of the text, `query` without its sigil. */
export interface Trigger {
  kind: TriggerKind;
  start: number;
  end: number;
  query: string;
}

/**
 * Which picker the caret sits in, if any. `/` opens only as the first character of the
 * text, `@` at the start or after whitespace. The token runs from its sigil to the caret and
 * holds no whitespace: a space after `/review` closes the picker and starts the arguments.
 */
export function triggerAt(text: string, caret: number): Trigger | null {
  const before = text.slice(0, caret);
  let start = before.length;
  while (start > 0 && !/\s/.test(before[start - 1])) start--;
  const token = before.slice(start);
  if (token.startsWith('/')) return start === 0 ? { kind: '/', start, end: caret, query: token.slice(1) } : null;
  if (token.startsWith('@')) return { kind: '@', start, end: caret, query: token.slice(1) };
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
export function parseCommand(text: string, commands: readonly Pick<Command, 'name'>[]): { name: string; args: string } | null {
  const m = /^\/(\S+)(?:\s+([\s\S]*))?$/.exec(text.trim());
  if (!m || !commands.some((c) => c.name === m[1])) return null;
  return { name: m[1], args: (m[2] ?? '').trim() };
}

/**
 * True while `text` is shaped like `/name…` and the command list is still on its way
 * (`commands` null, no `error`): sending then would hand an unresolved command to the
 * model as plain text. A loaded list, even an empty one, or a failed fetch never waits.
 */
export function commandPending(text: string, commands: readonly Pick<Command, 'name'>[] | null, error: string | null): boolean {
  return commands === null && !error && /^\/\S/.test(text.trim());
}

/** Commands matching `query`: a name starting with it first, then a name or description containing it. Case-insensitive; empty query keeps all. */
export function filterCommands<T extends Pick<Command, 'name' | 'description'>>(commands: readonly T[], query: string): T[] {
  const q = query.toLowerCase();
  const rank = (c: T) => {
    if (!q) return 0;
    const name = c.name.toLowerCase();
    if (name.startsWith(q)) return 0;
    if (name.includes(q)) return 1;
    if (c.description.toLowerCase().includes(q)) return 2;
    return -1;
  };
  return commands
    .map((c, i) => ({ c, i, r: rank(c) }))
    .filter((x) => x.r >= 0)
    .sort((a, b) => a.r - b.r || a.i - b.i)
    .map((x) => x.c);
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
