import type { Fusion, FusionPhase, SubagentStatus } from '../api';

/** Copilot Fusion's phases by their kind; another reported kind shows as it came. */
const STEP: Record<string, string> = { primary: 'Solver', draft: 'Draft', critic: 'Critic', revision: 'Revision', judge: 'Judge', repair: 'Repair', follow_up: 'Follow-up' };
const LEVEL: Record<string, string> = { standard: 'Standard', high: 'High', max: 'Max' };
const PATTERN: Record<string, string> = { single: 'Single', cascade: 'Cascade', critique: 'Critique' };

export const fusionStep = (kind: string) => STEP[kind] ?? kind;

/** "Fusion High", or "Fusion" for a level it does not name. */
export const fusionLevel = (level?: string) => (level && LEVEL[level] ? `Fusion ${LEVEL[level]}` : 'Fusion');

const PHASE_STATUS: Partial<Record<string, SubagentStatus>> = { running: 'running', succeeded: 'completed', failed: 'failed', cancelled: 'cancelled' };

/** A phase's state as a subagent's is read (its glyph, the list's status filters); none while planned. */
export const phaseStatus = (p: Pick<FusionPhase, 'status'>): SubagentStatus | undefined => PHASE_STATUS[p.status];

/** The phases that review another's result instead of working in the conversation. */
export const reviews = (p: Pick<FusionPhase, 'kind'>) => p.kind === 'critic' || p.kind === 'judge';

/** The turn head's route: "Fusion High · Critique · 2 models", or with one model "Fusion High · Claude Opus 5.5". */
export function fusionRoute(f: Fusion, name: (id: string) => string): string {
  const models = f.models ?? [];
  const lead = fusionLevel(f.level);
  if (f.pattern === 'single' || models.length < 2) return [lead, models[0] ? name(models[0]) : ''].filter(Boolean).join(' · ');
  return `${lead} · ${PATTERN[f.pattern] ?? f.pattern} · ${models.length} models`;
}

/** A phase row's caption: what it does now, a reviewer's verdict, or why it failed. */
export function phaseCaption(p: FusionPhase): string {
  switch (p.status) {
    case 'planned':
      return p.conditional ? 'Runs only if the review asks for changes' : 'Next';
    case 'running':
      return reviews(p) ? 'Reviewing' : 'Working in the conversation';
    case 'failed':
      return p.note || 'Failed';
    case 'cancelled':
      return 'Stopped';
  }
  if (!reviews(p)) return 'Done';
  const verdict = p.verdict === 'approve' ? 'Approved' : p.verdict === 'revise' ? 'Asked for changes' : p.verdict ?? '';
  return [verdict, p.note].filter(Boolean).join(': ') || 'Done';
}
