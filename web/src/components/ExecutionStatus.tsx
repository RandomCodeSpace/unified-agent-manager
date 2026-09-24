import { ChevronDown, Repeat2 } from 'lucide-react';
import type { ExecutionState } from '../api';
import { formatCredits } from '../lib/cost';
import { Button } from './ui/button';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

/** Runtime observations only. Unknown data keeps its last observation visibly qualified. */
export function ExecutionStatus({ execution, supported }: { execution: ExecutionState | null | undefined; supported: boolean }) {
  if (!supported) return null;
  const objective = execution?.objective;
  const current = execution?.known === true;
  const mode = execution?.mode;
  const label = current && mode ? mode.charAt(0).toUpperCase() + mode.slice(1) : 'Status unavailable';
  return (
    <Popover.Root>
      <Tip label={`Execution: ${label}${current && objective ? ` · ${objective.status}` : ''}`}>
        <Popover.Trigger render={<Button id="composer-execution" size="sm" variant="subtle" aria-label={`Execution: ${label}`} />}>
          <Repeat2 aria-hidden="true" className="text-faint" />
          <span>{label}</span>
          <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
        </Popover.Trigger>
      </Tip>
      <Popover.Content>
        <Popover.Title>Execution mode</Popover.Title>
        <p role="status">{label}</p>
        {!current && mode && <p className="text-caption text-muted">Last reported: {mode}</p>}
        {objective && <>
          <p className="text-caption text-muted">{current ? 'Objective: ' : 'Last reported objective: '}{objective.status}</p>
          <p className="break-words">{objective.objective}</p>
          <div className="space-y-1 text-caption text-muted">
            <p>{objective.turn_count} {objective.turn_count === 1 ? 'turn' : 'turns'} reported{objective.credits_used !== undefined ? ` · ${formatCredits(objective.credits_used)} credits used` : ''}{objective.credit_limit !== undefined ? ` · ${formatCredits(objective.credit_limit)} credit limit` : ''}</p>
            {objective.pause_reason && <p>{objective.pause_reason}</p>}
            {objective.completion_summary && <p className="whitespace-pre-wrap">{objective.completion_summary}</p>}
          </div>
        </>}
      </Popover.Content>
    </Popover.Root>
  );
}
