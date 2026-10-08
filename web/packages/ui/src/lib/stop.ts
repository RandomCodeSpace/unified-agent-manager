// Why a Task stopped, and a retried model call, in words (DESIGN.md Status line). A cancelled
// turn reads "Stopped" everywhere, with ": reason" when something other than your Stop did it.
// Pure, so the unit tests run in node.

import type { Retry, SessionSummary } from '../api';
import { formatCredits } from './cost.ts';

type StopReason = NonNullable<SessionSummary['stop_reason']>;

const SHORT: Record<StopReason, string> = {
  owner: 'Stopped',
  time_limit: 'Stopped: time limit',
  credit_limit: 'Stopped: credit limit',
  remote: 'Stopped: remote command',
  mcp: 'Stopped: MCP server',
};

export interface StopWords {
  /** The header chip, and the status line on a phone: "Stopped: credit limit". */
  short: string;
  /** The status line on a wide screen: a credit limit adds the autopilot run's numbers. */
  long: string;
  /** The tooltip and the Task row's text: "You stopped it", or what did. */
  title: string;
  /** Something other than your Stop did it. */
  notYours: boolean;
}

/**
 * A cancelled Task's words. Without a known reason (an older service, or a provider that gave
 * none) it is "Stopped", and the tooltip keeps the service's detail when it has one.
 */
export function stopWords(s: Pick<SessionSummary, 'stop_reason' | 'state_detail' | 'execution'>): StopWords {
  const reason = s.stop_reason && s.stop_reason in SHORT ? s.stop_reason : undefined;
  if (!reason || reason === 'owner') return { short: 'Stopped', long: 'Stopped', title: (!reason && s.state_detail) || 'You stopped it', notYours: false };
  const short = SHORT[reason];
  const title = s.state_detail || short;
  if (reason !== 'credit_limit') return { short, long: reason === 'time_limit' ? title : short, title, notYours: true };
  const objective = s.execution?.objective;
  const parts = ['Autopilot stopped: credit limit reached'];
  if (objective?.credits_used !== undefined && objective.credit_limit !== undefined) parts.push(`${formatCredits(objective.credits_used)} of ${formatCredits(objective.credit_limit)} credits used`);
  if (objective) parts.push(`${objective.turn_count} ${objective.turn_count === 1 ? 'turn' : 'turns'}`);
  const long = parts.join(' · ');
  return { short, long, title: long, notYours: true };
}

/** A retried model call: "Retrying, attempt 2", what failed (["HTTP 429", "rate limited"]), and the words a screen reader hears. */
export function retryWords(r: Retry): { lead: string; parts: string[]; cue: string } {
  const status = r.network ? 'connection failed' : r.status ? `HTTP ${r.status}` : '';
  const reason = r.reason?.replaceAll('_', ' ') ?? '';
  const why = reason || status;
  return { lead: `Retrying, attempt ${r.count + 1}`, parts: [status, reason].filter(Boolean), cue: why ? `Retrying: ${why}` : 'Retrying' };
}
