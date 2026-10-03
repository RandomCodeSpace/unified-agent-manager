// Background AI (Settings): the words and numbers behind the Utility log. Pure, so the unit tests run in node.

import type { UtilityCall, UtilityDay } from '../api';
import { compactTokens, formatCredits } from './cost.ts';

const PURPOSES: Record<string, string> = {
  title: 'Task title',
  'subagent-summary': 'Subagent summary',
  'planner-triage': 'Planner triage',
  'planner-suggest': 'Planner suggestion',
  'commit-message': 'Commit message',
  'configuration-draft': 'Configuration draft',
  'suggest-replies': 'Suggested replies',
  outcome: 'Outcome line',
};

/** "Task title" for `title`; an unknown purpose reads as words. */
export function purposeLabel(purpose: string): string {
  return PURPOSES[purpose] ?? purpose.replaceAll('-', ' ');
}

/** What became of a call that did not run cleanly: "Skipped: daily limit", "Skipped: Background AI off", "Failed"; "" when it ran. */
export function outcomeLabel(call: Pick<UtilityCall, 'outcome' | 'reason'>): string {
  if (call.outcome === 'skipped') return call.reason === 'off' ? 'Skipped: Background AI off' : 'Skipped: daily limit';
  return call.outcome === 'error' ? 'Failed' : '';
}

/** The model a call used: "gpt-6-luna", or "gpt-6-luna (the task's model)" when no Utility model is set. */
export function modelText(call: Pick<UtilityCall, 'model' | 'session_model'>): string {
  if (!call.model) return '';
  return call.session_model ? `${call.model} (the task's model)` : call.model;
}

const count = (n: number) => n.toLocaleString('en-US');

/** "1,240 → 38 characters". */
export function sizeText(c: Pick<UtilityCall, 'prompt_chars' | 'reply_chars'>): string {
  return `${count(c.prompt_chars)} → ${count(c.reply_chars)} characters`;
}

/** "1.2K in, 12 out tokens", with "≈" when the provider reported none and they are estimates. */
export function tokenText(c: Pick<UtilityCall, 'input_tokens' | 'output_tokens' | 'estimated'>): string {
  return `${c.estimated ? '≈' : ''}${compactTokens(c.input_tokens)} in, ${compactTokens(c.output_tokens)} out tokens`;
}

/** "850 ms", "1.2 s", "75 s". */
export function durationText(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  return ms < 10000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms / 1000)} s`;
}

/** The server-local clock time of a call, from its offset timestamp ("2026-10-01T14:03:09+02:00" → "14:03"). */
export function clockText(at: string): string {
  return at.slice(11, 16);
}

/** A day's totals in one line: "37 calls · 2 failed · 3 skipped · ≈12K in, 800 out tokens · 0.07 credits". */
export function dayText(d: UtilityDay): string {
  const parts = [`${count(d.calls)} ${d.calls === 1 ? 'call' : 'calls'}`];
  if (d.errors) parts.push(`${count(d.errors)} failed`);
  if (d.skipped) parts.push(`${count(d.skipped)} skipped`);
  if (d.calls) parts.push(tokenText({ input_tokens: d.input_tokens, output_tokens: d.output_tokens, estimated: d.estimated }));
  if (d.credits) parts.push(`${formatCredits(d.credits)} credits`);
  return parts.join(' · ');
}

/** "Today", "Yesterday", else "Mon 28 Sep", for a server-local YYYY-MM-DD day against today's. */
export function dayLabel(day: string, today: string): string {
  if (day === today) return 'Today';
  const date = (d: string) => new Date(Number(d.slice(0, 4)), Number(d.slice(5, 7)) - 1, Number(d.slice(8, 10)));
  const yesterday = date(today);
  yesterday.setDate(yesterday.getDate() - 1);
  const pad = (n: number) => String(n).padStart(2, '0');
  if (day === `${yesterday.getFullYear()}-${pad(yesterday.getMonth() + 1)}-${pad(yesterday.getDate())}`) return 'Yesterday';
  return date(day).toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short' });
}

/**
 * The newest page merged over the calls already shown (newest first; IDs grow by one): new calls go on top. When
 * the newest page does not reach back to them (more new calls than a page), it replaces them and its `next` applies.
 */
export function mergeCalls(shown: UtilityCall[], shownNext: number | undefined, newest: UtilityCall[], newestNext: number | undefined): { calls: UtilityCall[]; next: number | undefined } {
  const top = shown[0]?.id ?? 0;
  const oldest = newest[newest.length - 1]?.id ?? 0;
  if (!shown.length || (newestNext !== undefined && oldest > top + 1)) return { calls: newest, next: newestNext };
  return { calls: [...newest.filter((c) => c.id > top), ...shown], next: shownNext };
}

/** Calls grouped by their server-local day, in order. */
export function byDay(calls: UtilityCall[]): { day: string; calls: UtilityCall[] }[] {
  const groups: { day: string; calls: UtilityCall[] }[] = [];
  for (const c of calls) {
    const last = groups[groups.length - 1];
    if (last?.day === c.day) last.calls.push(c);
    else groups.push({ day: c.day, calls: [c] });
  }
  return groups;
}
