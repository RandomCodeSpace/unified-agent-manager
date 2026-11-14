import { Check } from 'lucide-react';
import { useEffect, useId, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type SkillOption } from '../api';
import { byCodeUnit } from '../lib/order';
import { Note } from './common';
import { Spinner } from './ui/spinner';
import { Switch } from './ui/switch';

interface Options { key: string; skills: SkillOption[] | null; project?: string[]; error: string }

/** The skills a Thread of the Project can be offered and the Project's own list, read once per Project. */
function useSkillOptions(projectId: string): Options {
  const api = useApi();
  const [state, setState] = useState<Options>({ key: '', skills: null, error: '' });
  useEffect(() => {
    let current = true;
    api.projectSkills(projectId).then(
      (r) => current && setState({ key: projectId, skills: r.skills, project: r.project_skills, error: '' }),
      (e: unknown) => current && setState({ key: projectId, skills: null, error: describeError(e) }),
    );
    return () => { current = false; };
  }, [api, projectId]);
  return state.key === projectId ? state : { key: projectId, skills: null, error: '' };
}

/**
 * Which skills an agent is offered, besides uam's own: every skill, or only the ones checked. Copilot
 * describes skills within a fixed budget in every request and fills it from the rest, so only a short
 * list saves context. For a Thread (`thread`), absent `value` inherits the Project's list; picking
 * every skill while the Project has a list names every skill found now.
 */
export function SkillChoices({ projectId, value, thread, reason, onChange }: Readonly<{
  projectId: string;
  /** The chosen names; absent offers every skill (for a Thread: the Project's list). */
  value?: string[];
  thread?: boolean;
  /** Why the list cannot change: a started Thread keeps the list it began with. */
  reason?: string;
  onChange: (value: string[] | undefined) => void;
}>) {
  const id = useId();
  const { skills, project, error } = useSkillOptions(projectId);
  const effective = value ?? (thread ? project : undefined);
  const names = skills?.map((s) => s.name) ?? [];
  // Over a Project list a Thread says every skill by naming each one found.
  const everyNamed = !!thread && project !== undefined && value !== undefined && names.length > 0 && value.length === names.length && names.every((n) => value.includes(n));
  const every = effective === undefined || everyNamed;
  const setEvery = (on: boolean) => {
    if (on) onChange(thread && project ? names : undefined);
    else onChange(everyNamed ? undefined : names);
  };
  const toggle = (name: string) => {
    const from = effective ?? names;
    onChange(from.includes(name) ? from.filter((n) => n !== name) : [...from, name].sort(byCodeUnit));
  };
  const missing = (effective ?? []).filter((n) => skills && !names.includes(n));
  return (
    <div className="flex flex-col gap-2">
      <div className="flex min-h-8 items-center gap-2">
        <span className="flex-1 text-ui text-ink">Every skill</span>
        <Switch aria-label="Every skill" aria-describedby={`${id}-note`} checked={every} disabled={!!reason || !skills} onCheckedChange={setEvery} />
      </div>
      {error && <Note tone="error" role="alert">{error}</Note>}
      {!skills && !error && <Spinner className="size-4 self-center text-muted" />}
      {skills && !every && (
        <ul aria-label="Skills" className="-mx-2 flex flex-col">
          {/* A started Thread lists only what it pinned. */}
          {(reason ? skills.filter((s) => effective.includes(s.name)) : skills).map((s) => {
            const on = effective.includes(s.name);
            return (
              <li key={s.name}>
                <button type="button" role="checkbox" aria-checked={on} disabled={!!reason} onClick={() => toggle(s.name)} className="flex min-h-8 w-full cursor-default items-start gap-2 rounded-sm px-2 py-1.5 text-left outline-hidden not-disabled:hover:bg-tint-hover focus-visible:outline-2 focus-visible:-outline-offset-2 disabled:opacity-45 pointer-coarse:min-h-11">
                  <Check aria-hidden="true" strokeWidth={2.5} className={on ? 'mt-0.5 size-3.5 shrink-0 text-accent' : 'invisible mt-0.5 size-3.5 shrink-0'} />
                  <span className="flex min-w-0 flex-col">
                    <span className="text-ui text-ink">{s.name}</span>
                    {s.description && <span className="line-clamp-2 text-caption text-muted">{s.description}</span>}
                  </span>
                </button>
              </li>
            );
          })}
          {missing.map((n) => <li key={n} className="px-2 py-1.5 text-ui text-muted">{n} <span className="text-caption">(not installed)</span></li>)}
        </ul>
      )}
      <p id={`${id}-note`} className="text-caption text-muted">
        {skills && !every && `${effective.filter((n) => names.includes(n)).length} of ${names.length} offered. `}
        uam&apos;s own skills are always offered; a short list saves the most context.
        {thread && (reason ? ` ${reason}` : ' Fixed when the Thread starts. The Project default is in Edit project.')}
      </p>
    </div>
  );
}
