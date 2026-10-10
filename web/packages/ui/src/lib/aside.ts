// Ask aside (`/btw`): the follow-up question built from the Task's earlier asides on this page.

/** The service's limit on an aside question (internal/web/aside.go `maxAsideQuestionBytes`), in UTF-8 bytes. */
export const ASIDE_QUESTION_BYTES = 16 << 10;
/** At most this many earlier asides go along with a new one. */
export const ASIDE_FOLLOW_UPS = 5;

export interface PriorAside {
  question: string;
  answer: string;
}

const encoder = new TextEncoder();
const bytes = (text: string) => encoder.encode(text).length;

/**
 * The question sent for a new aside: the newest earlier asides (at most five) with their answers as
 * context, then the new question. The oldest are left out first until the whole text fits the
 * service's byte limit; with none that fits, the question goes alone.
 */
export function asideQuestion(prior: readonly PriorAside[], question: string): string {
  const recent = prior.slice(-ASIDE_FOLLOW_UPS);
  for (let from = 0; from < recent.length; from++) {
    const earlier = recent.slice(from).map((p) => `Q: ${p.question}\nA: ${p.answer}`).join('\n\n');
    const text = `Earlier by-the-way questions in this Task and their answers:\n${earlier}\n\nNew question: ${question}`;
    if (bytes(text) <= ASIDE_QUESTION_BYTES) return text;
  }
  return question;
}
