// Answering an agent's question from the composer (DESIGN.md Composer, answer mode). The
// provider takes a plain string per question, so the answer is exactly one of the staged
// options or the typed text, never both, and nothing goes along with it. Pure rules; the
// Composer owns the requests.

import type { FormField, Question } from '../api';

/** How an option the agent recommends ends: "(Recommended)", in any case, trailing space allowed. */
const RECOMMENDED = /\(recommended\)\s*$/i;

/** The first option the agent marked as recommended, staged when its question arrives; undefined when none is. */
export const recommendedChoice = (choices: readonly string[] | undefined): string | undefined => choices?.find((c) => RECOMMENDED.test(c));

/**
 * The option staged when a question arrives: the recommended one, else the first of a single-choice question; a "choose any"
 * question starts empty. A form field starts empty too: it is the user's data, never a guess.
 */
export const initialChoice = (question: Pick<Question, 'choices' | 'multiple' | 'field'>): string | undefined =>
  question.field ? undefined : (recommendedChoice(question.choices) ?? (question.multiple ? undefined : question.choices?.[0]));

/** A form field the user may leave empty. */
const optional = (question: Pick<Question, 'field'>): boolean => !!question.field && !question.field.required;

/** Whether the composer can send now: an option is staged, the question takes free text and some is typed, or the field is optional. */
export const canAnswer = (question: Pick<Question, 'custom' | 'field'>, staged: readonly string[], text: string): boolean => staged.length > 0 || (question.custom && !!text.trim()) || optional(question);

/** The textarea's invitation: typed text is the answer, replacing a staged option; a question without free text takes only an option. */
export function answerPlaceholder(question: Pick<Question, 'custom' | 'field'>, staged: boolean): string {
  if (!question.custom) return 'Choose an option above…';
  if (staged) return 'Or type your own answer…';
  const number = question.field?.type === 'number' || question.field?.type === 'integer';
  const ask = number ? 'Type a number' : 'Type your answer';
  return optional(question) ? `${ask}, or leave it empty…` : `${ask}…`;
}

/** What a form field takes, in a few words: whether it is optional, and its type and bounds. Null for any other question. */
export function fieldHint(field: FormField | undefined): string | null {
  if (!field) return null;
  const parts = [field.required ? 'Required' : 'Optional'];
  const range = (lo: number | undefined, hi: number | undefined, unit: string) => {
    if (lo !== undefined && hi !== undefined) parts.push(`${lo} to ${hi}${unit}`);
    else if (lo !== undefined) parts.push(`at least ${lo}${unit}`);
    else if (hi !== undefined) parts.push(`at most ${hi}${unit}`);
  };
  if (field.type === 'integer') parts.push('whole number');
  else if (field.type === 'number') parts.push('number');
  if (field.format) parts.push({ email: 'email address', uri: 'URL', date: 'date (YYYY-MM-DD)', 'date-time': 'date and time (RFC 3339)' }[field.format]);
  range(field.minimum, field.maximum, '');
  range(field.min_length, field.max_length, ' characters');
  range(field.min_items, field.max_items, ' choices');
  return parts.join(' · ');
}

/**
 * The answer for one question, or null when nothing can be sent: the typed text where the
 * question takes free text, else the staged options, else nothing for an optional form field.
 * The composer clears one when the other is set, so both never stand.
 */
export function answerFromComposer(question: Pick<Question, 'custom' | 'field'>, text: string, staged: readonly string[]): string[][] | null {
  const typed = text.trim();
  if (question.custom && typed) return [[typed]];
  if (staged.length) return [[...staged]];
  return optional(question) ? [[]] : null;
}
