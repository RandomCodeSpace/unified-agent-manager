// Answering an agent's question from the composer (DESIGN.md Composer, answer mode). The
// provider takes a plain string per question, so the answer is exactly one of the staged
// options or the typed text, never both, and nothing goes along with it. Pure rules; the
// Composer owns the requests.

import type { Question } from '../api';

/** How an option the agent recommends ends: "(Recommended)", in any case, trailing space allowed. */
const RECOMMENDED = /\(recommended\)\s*$/i;

/** The first option the agent marked as recommended, staged when its question arrives; undefined when none is. */
export const recommendedChoice = (choices: readonly string[] | undefined): string | undefined => choices?.find((c) => RECOMMENDED.test(c));

/** The option staged when a question arrives: the recommended one, else the first of a single-choice question; a "choose any" question starts empty. */
export const initialChoice = (question: Pick<Question, 'choices' | 'multiple'>): string | undefined => recommendedChoice(question.choices) ?? (question.multiple ? undefined : question.choices?.[0]);

/** Whether the composer can send now: an option is staged, or the question takes free text and some is typed. */
export const canAnswer = (question: Pick<Question, 'custom'>, staged: readonly string[], text: string): boolean => staged.length > 0 || (question.custom && !!text.trim());

/** The textarea's invitation: typed text is the answer, replacing a staged option; a question without free text takes only an option. */
export function answerPlaceholder(question: Pick<Question, 'custom'>, staged: boolean): string {
  if (!question.custom) return 'Choose an option above…';
  return staged ? 'Or type your own answer…' : 'Type your answer…';
}

/**
 * The answer for one question, or null when nothing can be sent: the typed text where the
 * question takes free text, else the staged options. The composer clears one when the other
 * is set, so both never stand.
 */
export function answerFromComposer(question: Pick<Question, 'custom'>, text: string, staged: readonly string[]): string[][] | null {
  const typed = text.trim();
  if (question.custom && typed) return [[typed]];
  return staged.length ? [[...staged]] : null;
}
