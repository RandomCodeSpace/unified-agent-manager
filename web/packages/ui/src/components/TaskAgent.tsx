import { Radio } from '@base-ui/react/radio';
import { RadioGroup } from '@base-ui/react/radio-group';
import { Check, Sparkles } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type ConfigurationDefinition, type LoadedSkill, type SessionSummary } from '../api';
import { clockTime, dateTime } from './common';

export interface TaskAgents { agents: ConfigurationDefinition[] | null; error: string }

/**
 * The custom agents a Task of the Project can run as, read when `read` is set and again whenever
 * `revision` changes (each time the Tools panel opens), so the Agent tab shows only when there is one.
 */
export function useTaskAgents(session: Pick<SessionSummary, 'project_id' | 'provider'>, read: boolean, revision: unknown): TaskAgents {
  const api = useApi();
  const [state, setState] = useState<TaskAgents & { key: string }>({ key: '', agents: null, error: '' });
  const key = `${session.project_id}\u0000${session.provider}`;
  useEffect(() => {
    if (!read) return;
    let current = true;
    api.taskAgents(session.project_id, session.provider).then(
      (r) => current && setState({ key, agents: r.agents, error: '' }),
      (e: unknown) => current && setState({ key, agents: null, error: describeError(e) }),
    );
    return () => { current = false; };
  }, [api, key, session.project_id, session.provider, read, revision]);
  return state.key === key ? state : { agents: null, error: '' };
}

/**
 * The Task's custom agent, as the Tools panel's Agent tab: the provider's default agent unless the
 * owner picks one of the Project's agents for this Task. The service selects the agent between turns
 * only, and a Task whose agent is gone fails to open rather than run as another agent, so the owner
 * picks again here.
 */
export function AgentChoices({ session, agents, error, reason, onChange }: Readonly<{
  session: SessionSummary;
  agents: ConfigurationDefinition[] | null;
  error: string;
  /** Why the agent cannot change now; the choices are disabled while set. */
  reason?: string;
  onChange: (agent: string) => void;
}>) {
  const chosen = session.agent ?? '';
  const known = agents?.find((a) => a.id === chosen);
  // A chosen agent that discovery no longer lists stays visible, so the owner sees why the Task cannot open.
  const missing = chosen && agents && !known;
  const rows = [
    { id: '', name: 'Default', description: '', disabled: false },
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
    </div>
  );
}

/**
 * The skills the Thread's agents loaded, first loaded first. Skill loads stay out of the conversation;
 * this list keeps their names reachable, with each SKILL.md description, the first load time and how
 * often it loaded.
 */
export function LoadedSkills({ skills }: Readonly<{ skills: readonly LoadedSkill[] }>) {
  return (
    <ul aria-label="Skills loaded" className="-mx-2 flex flex-col">
      {skills.map((s) => (
        <li key={s.name} className="flex min-h-8 items-start gap-2 rounded-sm px-2 py-1.5">
          <Sparkles aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-faint" />
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="font-mono text-code-sm text-ink [overflow-wrap:anywhere]">{s.name}</span>
            {s.description && <span className="line-clamp-2 text-caption text-muted [overflow-wrap:anywhere]" title={s.description}>{s.description}</span>}
          </span>
          <span className="flex shrink-0 flex-col items-end text-meta text-muted tabular-nums">
            <time dateTime={s.first_at} title={`First loaded ${dateTime(s.first_at)}`}>{clockTime(s.first_at)}</time>
            {s.count > 1 && <span>{s.count} loads</span>}
          </span>
        </li>
      ))}
    </ul>
  );
}
