// Why a failed Thread's turn failed, in plain words, with the one thing to do about it
// (DESIGN.md Thread states). Pure, so the unit tests run in node.

import type { Item, SessionSummary } from '../api';

type FailureKind = NonNullable<SessionSummary['failure_kind']>;

export type FailureRecovery = 'account';

export interface FailureWords {
  /** The headline, the row tip and the header chip's hover words: "Rate limit reached". */
  cause: string;
  /** The failure as one plain sentence for the transcript. */
  sentence: string;
  /** Real recovery action before Resend, when one exists. */
  recovery?: FailureRecovery;
  /** Button label for the recovery action, when one exists. */
  recoveryLabel?: string;
}

const WORDS: Partial<Record<FailureKind, FailureWords>> = {
  rate_limit: { cause: 'Rate limit reached', sentence: 'Rate limit reached; wait a moment, then send your message again.' },
  authentication: {
    cause: 'Sign-in needed',
    sentence: 'Sign in to your account in Settings, then send your message again.',
    recovery: 'account',
    recoveryLabel: 'Sign in again',
  },
  authorization: {
    cause: 'No access',
    sentence: 'This account does not have access; check which account is signed in in Settings.',
    recovery: 'account',
    recoveryLabel: 'Check account',
  },
  quota: { cause: 'Out of quota', sentence: 'Out of quota; check your Copilot plan and usage.' },
  context_limit: { cause: 'Context full', sentence: 'Context is full; run /compact or start a new thread.' },
  policy: { cause: 'Blocked by policy', sentence: 'Blocked by your Copilot policy.' },
  query: { cause: 'Provider error', sentence: 'The provider stopped with an error; send your message again.' },
};

/** Fallback sentence for a failed Thread when the service gave no known cause. */
export const UNKNOWN_FAILURE_SENTENCE = 'Stopped with an error.';

// shortcut: message matching, upgrade when the backend sends an error kind
const AUTH_ERROR = /\b(?:http\s*)?401\b|bad credentials|\bsigned out\b|\bnot signed in\b|\bunauthenticated\b/i;

/** Items from the latest top-level turn (after the last user message). */
function lastTurnItems(items: readonly Item[] | undefined): readonly Item[] {
  if (!items?.length) return [];
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.kind === 'user' && !it.agent_id) return items.slice(i + 1);
  }
  return items;
}

/**
 * Classifies a failed Thread's error kind: prefers the backend's `failure_kind`,
 * falling back to message matching on `state_detail` and the failed turn's notices
 * or failed tool outputs when the backend did not send a specific cause.
 */
export function classifyFailure(
  s: Pick<SessionSummary, 'state' | 'failure_kind' | 'state_detail'>,
  items?: readonly Item[],
): FailureKind | null {
  if (s.state !== 'failed') return null;
  if (s.failure_kind && s.failure_kind !== 'other' && s.failure_kind !== 'query') {
    return WORDS[s.failure_kind] ? s.failure_kind : null;
  }
  // shortcut: message matching, upgrade when the backend sends an error kind
  if (s.state_detail && AUTH_ERROR.test(s.state_detail)) return 'authentication';
  for (const it of lastTurnItems(items)) {
    if (it.agent_id) continue;
    if (it.kind === 'notice' && it.text && AUTH_ERROR.test(it.text)) return 'authentication';
    if (it.kind === 'tool' && it.tool?.status === 'failed' && it.tool.output && AUTH_ERROR.test(it.tool.output)) return 'authentication';
  }
  if (s.failure_kind && WORDS[s.failure_kind]) return s.failure_kind;
  return null;
}

/**
 * A failed Thread's cause, one-sentence summary and recovery action; null when it did not fail
 * or the service gave no known kind (`other`, an older service, a conversation that could not open).
 */
export function failureWords(
  s: Pick<SessionSummary, 'state' | 'failure_kind' | 'state_detail'>,
  items?: readonly Item[],
): FailureWords | null {
  const kind = classifyFailure(s, items);
  return (kind && WORDS[kind]) || null;
}

/**
 * The failed turn's notice that already holds `state_detail`, so the transcript can fold
 * it into the failure's disclosure instead of showing the raw error twice.
 */
export function failureNoticeItem(
  s: Pick<SessionSummary, 'state' | 'state_detail'>,
  items: readonly Item[],
): Item | null {
  if (s.state !== 'failed') return null;
  const detail = s.state_detail?.trim().toLowerCase();
  if (!detail) return null;
  const turn = lastTurnItems(items);
  for (let i = turn.length - 1; i >= 0; i--) {
    const it = turn[i];
    if (!it.agent_id && it.kind === 'notice' && (it.text ?? '').toLowerCase().includes(detail)) return it;
  }
  return null;
}
