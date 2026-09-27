// Answering an agent's question from the composer (DESIGN.md Composer, answer mode). The
// provider takes a plain string per question, so the staged options or the typed text answer
// it, and what cannot be the answer (a note beside a staged option, every file) is steered
// into the same turn first. Pure rules; the Composer owns the requests.

import type { Question } from '../api';

/** What the composer holds in answer mode. */
export interface AnswerInput {
  text: string;
  /** The options staged from the card, in the order chosen. */
  staged: readonly string[];
  /** Picked `@` file references. */
  files: readonly string[];
  /** Finished uploads, by id. */
  attachments: readonly string[];
}

/** A message steered into the turn before the answer, carrying what the answer cannot. */
export interface Alongside {
  text: string;
  files: string[];
  attachments: string[];
}

/** A prompt needs text: this stands in when only files ride along. */
export const ALONGSIDE_TEXT = 'Files for my answer.';

/** Whether the composer can send now: an option is staged, or the question takes free text and some is typed. */
export const canAnswer = (question: Pick<Question, 'custom'>, staged: readonly string[], text: string): boolean => staged.length > 0 || (question.custom && !!text.trim());

/** The textarea's invitation: the text is the answer, or a note beside the staged option(s). */
export const answerPlaceholder = (question: Pick<Question, 'custom'>, staged: boolean): string => (question.custom && !staged ? 'Type your answer…' : 'Add a note (sent with your answer)…');

/**
 * The answer for one question and what rides alongside it, or null when nothing can be sent.
 * The staged options are the answer; without any, the typed text is, where the question allows
 * it. Text that is not the answer and every file go first, as a steer, so they are waiting when
 * the model resumes with the answer.
 */
export function answerFromComposer(question: Pick<Question, 'custom'>, input: AnswerInput): { answers: string[][]; alongside: Alongside | null } | null {
  const text = input.text.trim();
  let answer: string[];
  let note = '';
  if (input.staged.length) {
    answer = [...input.staged];
    note = text;
  } else if (question.custom && text) {
    answer = [text];
  } else {
    return null;
  }
  const files = [...input.files];
  const attachments = [...input.attachments];
  const alongside = note || files.length || attachments.length ? { text: note || ALONGSIDE_TEXT, files, attachments } : null;
  return { answers: [answer], alongside };
}
