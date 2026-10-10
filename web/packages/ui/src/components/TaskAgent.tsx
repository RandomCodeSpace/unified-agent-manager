import { Radio } from '@base-ui/react/radio';
import { RadioGroup } from '@base-ui/react/radio-group';
import { Check } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type ConfigurationDefinition, type SessionSummary } from '../api';

/**
 * The Task's custom agent, as the Tools panel's Agent tab: the provider's default agent unless the
 * owner picks one of the Project's agents for this Task. The list loads when the tab shows. The
 * service selects the agent between turns only, and a Task whose agent is gone fails to open rather
 * than run as another agent, so the owner picks again here.
 */
export function AgentChoices({ session, reason, onChange }: Readonly<{
  session: SessionSummary;
  /** Why the agent cannot change now; the choices are disabled while set. */
  reason?: string;
  onChange: (agent: string) => void;
}>) {
  const api = useApi();
  const [agents, setAgents] = useState<ConfigurationDefinition[] | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    let current = true;
    api.taskAgents(session.project_id, session.provider).then(
      (r) => current && setAgents(r.agents),
      (e: unknown) => current && setError(describeError(e)),
    );
    return () => { current = false; };
  }, [api, session.project_id, session.provider]);
  const chosen = session.agent ?? '';
  const known = agents?.find((a) => a.id === chosen);
  // A chosen agent that discovery no longer lists stays visible, so the owner sees why the Task cannot open.
  const missing = chosen && agents && !known;
  const rows = [
    { id: '', name: 'Default', description: "Copilot's own agent", disabled: false },
    ...(missing ? [{ id: chosen, name: chosen, description: 'Not found in this project', disabled: true }] : []),
    ...(agents ?? []).map((a) => ({ id: a.id, name: a.display_name || a.name, description: a.description, disabled: false })),
  ];
  return (
    <div className="flex flex-col gap-1.5">
      <RadioGroup aria-label="Agent" value={chosen} disabled={!!reason} onValueChange={(v) => v !== chosen && onChange(v as string)} className="-mx-2 flex flex-col">
        {rows.map((r) => (
          <Radio.Root key={r.id} value={r.id} disabled={r.disabled} className="group/agent flex min-h-8 w-full cursor-default items-start gap-2 rounded-sm px-2 py-1.5 text-left outline-hidden not-data-disabled:hover:bg-tint-hover focus-visible:bg-tint-hover data-disabled:opacity-45 pointer-coarse:min-h-11">
            <Check aria-hidden="true" strokeWidth={2.5} className="invisible mt-0.5 size-3.5 shrink-0 text-accent group-data-checked/agent:visible" />
            <span className="flex min-w-0 flex-col">
              <span className="text-ui text-ink">{r.name}</span>
              {r.description && <span className="text-caption text-muted">{r.description}</span>}
            </span>
          </Radio.Root>
        ))}
      </RadioGroup>
      {reason && <p className="text-caption text-muted">{reason}</p>}
      {error && <p role="status" className="text-caption text-error">{error}</p>}
      {!error && !agents && <p className="text-caption text-muted">Loading agents…</p>}
      {!error && agents?.length === 0 && <p className="text-caption text-muted">This project has no custom agents.</p>}
    </div>
  );
}
