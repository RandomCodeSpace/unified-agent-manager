// Why a failed Thread's turn failed, in plain words, with the one thing to do about it
// (DESIGN.md Thread states). Pure, so the unit tests run in node.

import type { SessionSummary } from '../api';

type FailureKind = NonNullable<SessionSummary['failure_kind']>;

export interface FailureWords {
  /** The headline, the row tip and the header chip's hover words: "Rate limit reached". */
  cause: string;
  /** What to do, as a sentence. */
  action: string;
}

const WORDS: Partial<Record<FailureKind, FailureWords>> = {
  rate_limit: { cause: 'Rate limit reached', action: 'Wait, then send your message again.' },
  authentication: { cause: 'Sign-in needed', action: 'Sign in in Settings, then send your message again.' },
  authorization: { cause: 'No access', action: 'Check which account is signed in, in Settings.' },
  quota: { cause: 'Out of quota', action: 'Check your Copilot plan and usage.' },
  context_limit: { cause: 'Context full', action: 'Run /compact, or start a new thread.' },
  policy: { cause: 'Blocked by policy', action: 'Your Copilot policy does not allow this request.' },
  query: { cause: 'Provider error', action: 'Send your message again.' },
};

/**
 * A failed Thread's cause and action; null when it did not fail or the service gave no known kind
 * (`other`, an older service, a conversation that could not open): then its own detail is all there is.
 */
export function failureWords(s: Pick<SessionSummary, 'state' | 'failure_kind'>): FailureWords | null {
  return (s.state === 'failed' && s.failure_kind && WORDS[s.failure_kind]) || null;
}
