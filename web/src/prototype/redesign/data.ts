// PROTOTYPE (throwaway, branch prototype/ui-redesign): read-only data for the redesign
// variants, taken straight from the dev mock seed. Never part of a production bundle.

import { ATTENTION, LIVE, taskName, type Interaction, type Item, type Project, type SessionState } from '../../api';
import { seed, type MockTask } from '../../mock/data';

const s = seed();

export const projects: Project[] = s.projects;
export const tasks: MockTask[] = s.tasks.filter((t) => t.stage !== 'archived');
export const meta = s.meta;
export const changes = s.changes;

export const projectOf = (t: { project_id: string }) => projects.find((p) => p.id === t.project_id)!;
export const nameOf = (t: MockTask) => taskName(t) || 'New task';
export const needsYou = (t: MockTask) => ATTENTION.includes(t.state);
export const live = (t: MockTask) => LIVE.includes(t.state) && !ATTENTION.includes(t.state);
export const pendingInteraction = (t: MockTask): Interaction | undefined => t.interactions.find((i) => i.state === 'pending');

export const byRecent = (a: MockTask, b: MockTask) => b.updated_at.localeCompare(a.updated_at);

export function ago(iso: string): string {
  const m = Math.max(0, Math.round((Date.now() - Date.parse(iso)) / 60000));
  if (m < 1) return 'now';
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  return h < 24 ? `${h}h` : `${Math.round(h / 24)}d`;
}

export const stateLabel: Record<SessionState, string> = {
  idle: 'Idle',
  starting: 'Starting',
  working: 'Working',
  awaiting_permission: 'Needs permission',
  awaiting_answer: 'Needs answer',
  completed: 'Completed',
  cancelled: 'Cancelled',
  failed: 'Failed',
  interrupted: 'Interrupted',
  closed: 'Closed',
};

export const stateTone = (st: SessionState): 'live' | 'attention' | 'ok' | 'bad' | 'quiet' =>
  ATTENTION.includes(st) ? 'attention' : LIVE.includes(st) ? 'live' : st === 'completed' ? 'ok' : st === 'failed' ? 'bad' : 'quiet';

/** The Task every variant opens for its "task" screenshot. */
export const focusTask = tasks.find((t) => t.id === 't1')!;
export const visibleItems = (t: MockTask): Item[] => t.items.filter((i) => !i.agent_id);

/** Plain-language status: what the agent is doing, in words a newcomer reads without a glossary. */
export function sentence(t: MockTask): string {
  const i = pendingInteraction(t);
  switch (t.state) {
    case 'awaiting_permission':
      return i ? `Wants your OK to ${lower(i.title)}` : 'Wants your OK to continue';
    case 'awaiting_answer':
      return i ? `Asks: ${i.title}` : 'Has a question for you';
    case 'working':
    case 'starting':
      return t.subagents_running ? `Working with ${t.subagents_running} helpers` : ago(t.created_at) === 'now' ? 'Just started' : `Working for ${ago(t.created_at)}`;
    case 'completed':
      return 'Finished — ready for your review';
    case 'failed':
      return 'Stopped with an error';
    case 'interrupted':
      return 'Paused midway — can pick up again';
    case 'cancelled':
      return 'You stopped it';
    default:
      return 'Waiting for your next message';
  }
}

const lower = (s: string) => s.charAt(0).toLowerCase() + s.slice(1);

/** The one obvious next step for a Task, as a button label. */
export function nextAction(t: MockTask): string | null {
  if (t.state === 'awaiting_permission') return 'Review request';
  if (t.state === 'awaiting_answer') return 'Answer';
  if (t.state === 'completed') return 'Review changes';
  if (t.state === 'failed' || t.state === 'interrupted') return 'Try again';
  return null;
}

/** What the agent did, counted from its tool calls: "read 2 files · searched once · edited 1 file". */
export function receipt(items: Item[]): string {
  const n = (name: string) => items.filter((i) => i.kind === 'tool' && i.tool?.name === name).length;
  const parts = [
    n('view') && `read ${n('view')} file${n('view') > 1 ? 's' : ''}`,
    n('grep') && `searched ${n('grep') > 1 ? `${n('grep')} times` : 'once'}`,
    n('edit') && `edited ${n('edit')} file${n('edit') > 1 ? 's' : ''}`,
    n('bash') && `ran ${n('bash')} command${n('bash') > 1 ? 's' : ''}`,
  ].filter(Boolean);
  return parts.join(' · ');
}

export const projectChanges = (t: MockTask) => changes[t.project_id] ?? [];

export const files = s.files;

/** PROTOTYPE: the evidence a finish card would derive from the Task's own events (commands, exit codes, edits). */
export const evidence: { claim: string; check: string | null; result: 'pass' | 'fail' | 'unverified'; detail: string }[] = [
  { claim: 'Focus events replay after re-attach', check: 'go test ./internal/vterm/... -run Redraw', result: 'pass', detail: 'exit 0 · 14 tests · 1.2 s' },
  { claim: 'A regression test covers it', check: 'edit internal/vterm/redraw_test.go', result: 'pass', detail: 'TestRedrawReplaysFocusEvents, +12 lines' },
  { claim: 'The docs describe the new behaviour', check: null, result: 'unverified', detail: 'no command checked docs/terminal.md' },
];

/** PROTOTYPE: a risky change the review demos list first. */
export const riskyChange = { path: '.github/workflows/ci.yml', status: 'M', additions: 2, deletions: 1, patch: '' };
