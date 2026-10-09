import { Bot, ChevronDown } from 'lucide-react';
import { useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type ConfigurationDefinition, type SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { Button } from './ui/button';
import { Menu } from './ui/menu';
import { Tip } from './ui/tooltip';

/**
 * The Task's custom agent: the provider's default agent unless the owner picks one of the
 * Project's agents for this Task. The list loads when the menu opens. The service selects the
 * agent between turns only, and a Task whose agent is gone fails to open rather than run as
 * another agent, so the owner picks again here.
 */
export function AgentPicker({ session, reason, onChange, menuClass }: Readonly<{
  session: SessionSummary;
  /** Why the agent cannot change now; the picker is disabled while set. */
  reason?: string;
  onChange: (agent: string) => void;
  menuClass?: string;
}>) {
  const api = useApi();
  const [agents, setAgents] = useState<ConfigurationDefinition[] | null>(null);
  const [error, setError] = useState('');
  const current = session.agent ?? '';
  const known = agents?.find((a) => a.id === current);
  const display = current ? known?.display_name || known?.name || current : 'Default agent';
  const face = (
    <>
      <Bot aria-hidden="true" className="text-faint" />
      {current && <span className="max-w-28 truncate max-sm:hidden in-data-[fold~=tuning]:hidden">{display}</span>}
      <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
    </>
  );
  if (reason) {
    return (
      <Tip label={<>{`Agent: ${display}`}<span className="block text-on-primary/70">{reason}</span></>}>
        <Button id="composer-agent" size="sm" variant="subtle" aria-disabled="true" aria-label={`Agent: ${display}. ${reason}`} className="text-muted">
          {face}
        </Button>
      </Tip>
    );
  }
  const load = (open: boolean) => {
    if (!open) return;
    setError('');
    api.taskAgents(session.project_id, session.provider).then(
      (r) => setAgents(r.agents),
      (e: unknown) => setError(describeError(e)),
    );
  };
  const listed = agents ?? [];
  // A chosen agent that discovery no longer lists stays visible, so the owner sees why the Task cannot open.
  const missing = current && agents && !known;
  return (
    <Menu.Root modal={false} onOpenChange={load}>
      <Tip label={`Agent: ${display}`}>
        <Menu.Trigger render={<Button id="composer-agent" size="sm" variant="subtle" aria-label={`Agent: ${display}`} className="text-body" />}>{face}</Menu.Trigger>
      </Tip>
      <Menu.Content side="top" align="start" sideOffset={6} className={cn('min-w-52 max-w-80', menuClass)}>
        <Menu.RadioGroup value={current} onValueChange={(v) => v !== current && onChange(v as string)}>
          <Menu.Label>Agent</Menu.Label>
          <Menu.RadioItem value="" description="Copilot's own agent">Default</Menu.RadioItem>
          {missing && <Menu.RadioItem value={current} disabled description="Not found in this project">{current}</Menu.RadioItem>}
          {listed.map((a) => (
            <Menu.RadioItem key={a.id} value={a.id} description={a.description || undefined}>
              {a.display_name || a.name}
            </Menu.RadioItem>
          ))}
        </Menu.RadioGroup>
        {error && <p className="max-w-64 px-2 py-1 text-caption text-error">{error}</p>}
        {!error && !agents && <p className="px-2 py-1 text-caption text-muted">Loading agents…</p>}
        {!error && agents?.length === 0 && <p className="max-w-64 px-2 py-1 text-caption text-muted">This project has no custom agents.</p>}
      </Menu.Content>
    </Menu.Root>
  );
}
