import type { ExecutionState } from '../api';
import { formatCredits } from '../lib/cost';

/** Runtime observations only. Unknown data keeps its last observation visibly qualified. */
export function ExecutionStatus({ execution, supported }: { execution: ExecutionState | null | undefined; supported: boolean }) {
  if (!supported) return null;
  const objective = execution?.objective;
  const current = execution?.known === true;
  const mode = execution?.mode;
  return (
    <div className="border-b border-hairline px-3.5 py-2 text-caption text-muted">
      <div role="status" className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="font-medium text-body">Execution: {current && mode ? mode.charAt(0).toUpperCase() + mode.slice(1) : 'Status unavailable'}</span>
        {!current && mode && <span>Last reported: {mode}</span>}
        {objective && <span>{current ? '' : 'Last reported objective: '}{objective.status}</span>}
      </div>
      {objective && <details className="mt-1">
        <summary className="cursor-pointer break-words py-1 text-body pointer-coarse:min-h-11">{objective.objective}</summary>
        <div className="space-y-1 pb-1">
          <p>{objective.turn_count} {objective.turn_count === 1 ? 'turn' : 'turns'} reported{objective.credits_used !== undefined ? ` · ${formatCredits(objective.credits_used)} credits used` : ''}{objective.credit_limit !== undefined ? ` · ${formatCredits(objective.credit_limit)} credit limit` : ''}</p>
          {objective.pause_reason && <p>{objective.pause_reason}</p>}
          {objective.completion_summary && <p className="whitespace-pre-wrap">{objective.completion_summary}</p>}
        </div>
      </details>}
    </div>
  );
}
