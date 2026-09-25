import { ChevronDown, Repeat2 } from 'lucide-react';
import type { ExecutionState } from '../api';
import { formatCredits } from '../lib/cost';
import { Button } from './ui/button';
import { Menu } from './ui/menu';
import { Tip } from './ui/tooltip';

interface ExecutionProps {
  execution: ExecutionState | null | undefined;
  reason: string;
  busy: boolean;
  onChange: (mode: 'interactive' | 'autopilot') => void;
  onRetry?: () => void;
}

/** The mode radio group with its reason and objective, for the toolbar picker and the phone's More menu. */
export function ExecutionItems({ execution, reason, busy, onChange, onRetry }: ExecutionProps) {
  const objective = execution?.objective;
  const current = execution?.known === true;
  const mode = execution?.mode;
  return (
    <>
      <Menu.RadioGroup value={current ? mode ?? '' : ''} onValueChange={(value) => {
        if (!reason && !busy && (value === 'interactive' || value === 'autopilot')) onChange(value);
      }}>
        <Menu.Label>Execution mode</Menu.Label>
        <Menu.RadioItem value="interactive" disabled={!!reason || busy} description="Responds to each message. Does not stop a running turn.">Interactive</Menu.RadioItem>
        <Menu.RadioItem value="autopilot" disabled={!!reason || busy} description="Continues working automatically between turns.">Autopilot</Menu.RadioItem>
      </Menu.RadioGroup>
      {reason && <p className="px-2 py-1 text-caption text-muted" role="status">{reason}</p>}
      {onRetry && <Menu.Item closeOnClick={false} onClick={onRetry}>Retry commands</Menu.Item>}
      {!current && <p className="px-2 py-1 text-caption text-muted">{mode ? `Last reported: ${mode}` : 'Execution status unavailable.'}</p>}
      {objective && <div className="fade-rule my-1" aria-hidden="true" />}
      {objective && <div className="space-y-1 px-2 py-1 text-ui">
        <p className="text-caption text-muted">{current ? 'Objective: ' : 'Last reported objective: '}{objective.status}</p>
        <p className="break-words">{objective.objective}</p>
        <div className="space-y-1 text-caption text-muted">
          <p>{objective.turn_count} {objective.turn_count === 1 ? 'turn' : 'turns'} reported{objective.credits_used !== undefined ? ` · ${formatCredits(objective.credits_used)} credits used` : ''}{objective.credit_limit !== undefined ? ` · ${formatCredits(objective.credit_limit)} credit limit` : ''}</p>
          {objective.pause_reason && <p>{objective.pause_reason}</p>}
          {objective.completion_summary && <p className="whitespace-pre-wrap">{objective.completion_summary}</p>}
        </div>
      </div>}
    </>
  );
}

/** Selection follows runtime observations; changing a mode never submits the draft. Hidden on a phone, where the More menu carries it. */
export function ExecutionStatus({ execution, supported, reason, busy, onOpenChange, onChange, onRetry }: ExecutionProps & { supported: boolean; onOpenChange: (open: boolean) => void }) {
  if (!supported) return null;
  const objective = execution?.objective;
  const current = execution?.known === true;
  const mode = execution?.mode;
  const label = current && mode ? mode.charAt(0).toUpperCase() + mode.slice(1) : 'Status unavailable';
  return (
    <Menu.Root modal={false} onOpenChange={onOpenChange}>
      <Tip label={`Execution: ${label}${current && objective ? ` · ${objective.status}` : ''}`}>
        <Menu.Trigger render={<Button id="composer-execution" size="sm" variant="subtle" aria-label={`Execution: ${label}`} aria-busy={busy} className="max-sm:hidden" />}>
          <Repeat2 aria-hidden="true" className="text-faint" />
          <span>{label}</span>
          <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
        </Menu.Trigger>
      </Tip>
      <Menu.Content side="top" align="start" className="max-w-80">
        <ExecutionItems execution={execution} reason={reason} busy={busy} onChange={onChange} onRetry={onRetry} />
      </Menu.Content>
    </Menu.Root>
  );
}
