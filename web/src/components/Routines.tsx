import { ChevronRight, Pause, Pencil, Play, Plus, Trash2, X, Zap } from 'lucide-react';
import { useEffect, useRef, useState, type SubmitEvent } from 'react';
import { api, describeError, modelCatalog, resolveTaskDefaults, type Project, type Routine, type RoutineInput, type RoutineRun, type SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { modelChoices } from '../lib/models';
import { SCHEDULE_KINDS, TRIGGER_LABEL, WEEKDAYS, describeSchedule, outcomeLabel, outcomeTone, runTime, scheduleOf, untilText, type OutcomeTone, type ScheduleKind } from '../lib/routines';
import { Note, ProjectBadge, ScrollSentinel, Skeleton, useApp, useMinuteTick, useScrolled } from './common';
import { Field, choiceLabel } from './TaskDefaults';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Collapse } from './ui/collapse';
import { AlertDialog, Dialog, useConfirm } from './ui/dialog';
import { Input } from './ui/input';
import { Segmented } from './ui/segmented';
import { Select } from './ui/select';
import { Tip } from './ui/tooltip';

/** How often the open view reads the routines again, for next runs and outcomes. */
const POLL_MS = 5000;

const TONE: Record<OutcomeTone, string> = {
  accent: 'text-accent',
  attention: 'text-attention',
  error: 'text-error',
  warning: 'text-warning',
  muted: 'text-muted',
  ink: 'text-ink',
};

const areaClass =
  'min-h-28 w-full resize-y rounded-sm bg-sunken px-2.5 py-2 text-ui text-ink shadow-well placeholder:text-muted focus-visible:bg-raised focus-visible:shadow-focus focus-visible:outline-none';

/**
 * A Project's routines (docs/web.md, Routines): a view in the main pane like Settings, `#routines=<project>`
 * in the URL. Each routine is a card with its schedule, next run and last outcome, Run now, Pause or Resume,
 * Edit and Delete, and its run history on demand. Each run is a normal Task in the Project.
 */
export function RoutinesView({ leading, project, sessions, onOpenTask, onClose }: Readonly<{ leading?: React.ReactNode; project: Project; sessions: SessionSummary[]; onOpenTask: (id: string) => void; onClose: () => void }>) {
  const [routines, setRoutines] = useState<Routine[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ routine?: Routine } | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [scrolled, sentinel] = useScrolled();
  const removal = useConfirm<Routine>();
  const [removing, setRemoving] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);

  const [tick, setTick] = useState(0);
  const load = () => setTick((n) => n + 1);

  // The view is keyed by its Project; it reads the list again every few seconds while the page is visible.
  useEffect(() => {
    let current = true;
    api
      .routines(project.id)
      .then((res) => {
        if (!current) return;
        setRoutines(res.routines);
        setLoadError(null);
      })
      .catch((e: unknown) => current && setLoadError(describeError(e)));
    return () => {
      current = false;
    };
  }, [project.id, tick]);
  useEffect(() => {
    const timer = window.setInterval(() => document.visibilityState === 'visible' && setTick((n) => n + 1), POLL_MS);
    return () => window.clearInterval(timer);
  }, []);

  const put = (r: Routine) => setRoutines((list) => (list ?? []).some((x) => x.id === r.id) ? (list ?? []).map((x) => (x.id === r.id ? r : x)) : [...(list ?? []), r]);
  const edit = (routine?: Routine) => {
    setEditing({ routine });
    setEditOpen(true);
  };

  async function remove() {
    const r = removal.target;
    if (!r) return;
    setRemoving(true);
    setRemoveError(null);
    try {
      await api.deleteRoutine(r.id);
      setRoutines((list) => (list ?? []).filter((x) => x.id !== r.id));
      removal.close();
    } catch (e) {
      setRemoveError(describeError(e));
    } finally {
      setRemoving(false);
    }
  }

  let body: React.ReactNode;
  if (routines === null && loadError) {
    body = (
      <div className="flex flex-col items-start gap-2">
        <Note tone="error" role="alert">Could not load the routines: {loadError}</Note>
        <Button variant="secondary" onClick={load}>Retry</Button>
      </div>
    );
  } else if (routines === null) {
    body = (
      <section aria-busy="true" className="rounded-lg bg-raised p-5 shadow-raised">
        <Skeleton label="Loading routines…" rows={3} />
      </section>
    );
  } else if (routines.length === 0) {
    body = (
      <section className="flex flex-col items-start gap-3 rounded-lg bg-raised p-5 shadow-raised">
        <h2 className="text-title text-ink">No routines yet</h2>
        <p className="max-w-3xl text-ui text-body">
          A routine starts a task in this project on a schedule, for example every weekday at 09:00 to check dependencies for updates, or every 6 hours to run the flaky tests and report failures. Each run shows in the task list like any task.
        </p>
        <Button variant="primary" onClick={() => edit()}>
          <Plus />
          New routine
        </Button>
      </section>
    );
  } else {
    body = routines.map((r) => <RoutineCard key={r.id} routine={r} sessions={sessions} onChange={put} onEdit={() => edit(r)} onDelete={() => { setRemoveError(null); removal.ask(r); }} onOpenTask={onOpenTask} />);
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col animate-rise">
      <header className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3" data-scrolled={scrolled || undefined}>
        {leading}
        <ProjectBadge badge={project.badge} className="shrink-0" />
        <h1 className="min-w-0 flex-1 truncate text-display-sm text-ink">
          Routines<span className="text-muted"> · {project.name}</span>
        </h1>
        {loadError && routines !== null && (
          <Tip label={`Could not refresh: ${loadError}`}>
            <span className="text-caption text-warning">Not refreshed</span>
          </Tip>
        )}
        {routines !== null && routines.length > 0 && (
          <Button variant="secondary" aria-label="New routine" onClick={() => edit()}>
            <Plus />
            <span className="max-[480px]:hidden">New routine</span>
          </Button>
        )}
        <Tip label="Close routines">
          <Button size="icon-md" aria-label="Close routines" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        </Tip>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <ScrollSentinel sentinelRef={sentinel} />
        <div className="flex w-full min-w-0 flex-col gap-4 px-4 py-4 md:px-6">
          <p className="text-caption text-muted">Times follow the clock of the host uam runs on. A run is skipped while the previous one is still running.</p>
          {body}
        </div>
      </div>
      {editing && (
        <RoutineDialog
          open={editOpen}
          project={project}
          routine={editing.routine}
          onClose={() => setEditOpen(false)}
          onClosed={() => setEditing(null)}
          onSaved={put}
        />
      )}
      <AlertDialog
        {...removal.props}
        title={`Delete ${removal.target?.name ?? 'this routine'}?`}
        description="It stops running and its run history goes with it. The tasks its runs started stay."
        confirmLabel="Delete routine"
        busy={removing}
        onConfirm={() => void remove()}
      >
        {removeError && (
          <Note tone="error" role="alert" className="mt-3">
            {removeError}
          </Note>
        )}
      </AlertDialog>
    </div>
  );
}

function RoutineCard({ routine: r, sessions, onChange, onEdit, onDelete, onOpenTask }: Readonly<{ routine: Routine; sessions: SessionSummary[]; onChange: (r: Routine) => void; onEdit: () => void; onDelete: () => void; onOpenTask: (id: string) => void }>) {
  const { meta } = useApp();
  useMinuteTick();
  const [busy, setBusy] = useState<'run' | 'pause' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [history, setHistory] = useState(false);
  // The last run is the newest that started a Task (or tried to); a newer skip shows under it.
  const last = r.runs.find((run) => run.outcome !== 'skipped') ?? r.runs[0];
  const skip = r.runs[0] !== last ? r.runs[0] : undefined;
  const model = modelCatalog(meta, r.provider).find((m) => m.id === r.model)?.name ?? r.model;
  const titleId = `routine-${r.id}-title`;

  async function act(kind: 'run' | 'pause', call: () => Promise<Routine>) {
    setBusy(kind);
    setError(null);
    try {
      onChange(await call());
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <section aria-labelledby={titleId} className="flex flex-col gap-3 rounded-lg bg-raised p-5 shadow-raised max-sm:p-4">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <h2 id={titleId} className="min-w-0 truncate text-title text-ink">
          {r.name}
        </h2>
        {!r.enabled && <Chip fill="outline">Paused</Chip>}
        {r.mode === 'yolo' && <Chip tone="warning" fill="well"><Zap className="size-3" />Yolo</Chip>}
      </div>
      <dl className="grid gap-x-6 gap-y-1.5 text-ui sm:grid-cols-[max-content_minmax(0,1fr)] max-sm:gap-y-0 max-sm:[&>dt]:mt-2 max-sm:[&>dt]:text-caption max-sm:[&>dt:first-child]:mt-0">
        <dt className="text-muted">When</dt>
        <dd className="text-ink">
          {describeSchedule(r.schedule)}
          {r.enabled && r.next_run ? <span className="text-muted"> · next {runTime(r.next_run)} ({untilText(r.next_run)})</span> : <span className="text-muted"> · paused, no next run</span>}
        </dd>
        <dt className="text-muted">Last run</dt>
        <dd className="min-w-0">
          {last ? <RunLine run={last} sessions={sessions} onOpenTask={onOpenTask} /> : <span className="text-muted">Not run yet</span>}
          {skip && (
            <span className="block text-caption text-muted">
              Skipped {runTime(skip.at)}: {skip.reason}
            </span>
          )}
        </dd>
        <dt className="text-muted">Runs with</dt>
        <dd className="text-body">
          {model} · {r.mode === 'yolo' ? 'Yolo' : 'Safe'} · at most {r.max_runs_per_day} {r.max_runs_per_day === 1 ? 'run' : 'runs'} a day, stopped after {r.max_minutes} min
        </dd>
      </dl>
      <p className="line-clamp-2 text-caption whitespace-pre-line text-muted">{r.prompt}</p>
      {error && (
        <Note tone="error" role="alert">
          {error}
        </Note>
      )}
      <div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="secondary" loading={busy === 'run'} disabled={!!busy} onClick={() => void act('run', () => api.runRoutine(r.id))}>
            <Play />
            Run now
          </Button>
          <Button variant="subtle" loading={busy === 'pause'} disabled={!!busy} onClick={() => void act('pause', () => api.updateRoutine(r.id, { enabled: !r.enabled }))}>
            {r.enabled ? <Pause /> : <Play />}
            {r.enabled ? 'Pause' : 'Resume'}
          </Button>
          <Button variant="subtle" onClick={onEdit}>
            <Pencil />
            Edit
          </Button>
          <Button variant="danger" onClick={onDelete}>
            <Trash2 />
            Delete
          </Button>
          {r.runs.length > 0 && (
            <Button variant="subtle" className="ml-auto text-muted" aria-expanded={history} aria-controls={`routine-${r.id}-history`} onClick={() => setHistory((o) => !o)}>
              <ChevronRight className={cn('transition-transform duration-160', history && 'rotate-90')} />
              History · {r.runs.length}
            </Button>
          )}
        </div>
        <Collapse open={history} inner="pt-3">
          <ol id={`routine-${r.id}-history`} aria-label={`${r.name} runs`} className="flex flex-col rounded-md bg-sunken p-1">
            {r.runs.map((run) => (
              // A phone stacks the outcome under the time and the trigger.
              <li key={run.id} className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-0.5 rounded-sm px-2 py-1.5 text-ui sm:grid-cols-[9.5rem_minmax(0,1fr)_auto]">
                <span className="tabular-nums text-body">{runTime(run.at)}</span>
                <span className="col-span-full row-start-2 min-w-0 sm:col-span-1 sm:col-start-2 sm:row-start-1">
                  <RunLine run={run} sessions={sessions} onOpenTask={onOpenTask} time={false} />
                </span>
                <span className="text-right text-caption text-muted">{TRIGGER_LABEL[run.trigger]}</span>
              </li>
            ))}
          </ol>
        </Collapse>
      </div>
    </section>
  );
}

/** A run's outcome (and why), when it fired unless `time` is off, and Open task while its Task exists. */
function RunLine({ run, sessions, onOpenTask, time = true }: Readonly<{ run: RoutineRun; sessions: SessionSummary[]; onOpenTask: (id: string) => void; time?: boolean }>) {
  const task = run.task_id ? sessions.find((s) => s.id === run.task_id) : undefined;
  const label = outcomeLabel(run, task?.state);
  return (
    <span className="inline-flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
      <span className={cn('font-medium', TONE[outcomeTone(run, task?.state)])}>{label}</span>
      {run.reason && <span className="min-w-0 text-body [overflow-wrap:anywhere]">{run.reason}</span>}
      {time && <span className="text-muted">{runTime(run.at)}</span>}
      {task && (
        <Button variant="subtle" size="sm" className="text-accent" onClick={() => onOpenTask(task.id)}>
          Open task
        </Button>
      )}
    </span>
  );
}

/** Create or edit a routine. A new one runs with the model New task starts with, in Safe mode, until changed here. */
function RoutineDialog({ open, project, routine, onClose, onClosed, onSaved }: Readonly<{ open: boolean; project: Project; routine?: Routine; onClose: () => void; onClosed: () => void; onSaved: (r: Routine) => void }>) {
  const { meta, settings } = useApp();
  const first = useRef<HTMLInputElement>(null);
  const defaults = resolveTaskDefaults(meta, settings.task_defaults, settings.hidden_models);
  const providerName = routine?.provider ?? defaults?.provider ?? '';
  const catalog = modelCatalog(meta, providerName);
  const s = routine?.schedule;
  const [name, setName] = useState(routine?.name ?? '');
  const [prompt, setPrompt] = useState(routine?.prompt ?? '');
  const [model, setModel] = useState(routine?.model ?? defaults?.model ?? '');
  const [kind, setKind] = useState<ScheduleKind>(s?.kind ?? 'weekdays');
  const [time, setTime] = useState(s && s.kind !== 'hours' ? s.time : '09:00');
  const [weekday, setWeekday] = useState(s?.kind === 'weekly' ? s.weekday : 1);
  const [hours, setHours] = useState(String(s?.kind === 'hours' ? s.hours : 6));
  const [mode, setMode] = useState<'safe' | 'yolo'>(routine?.mode ?? 'safe');
  const [perDay, setPerDay] = useState(String(routine?.max_runs_per_day ?? 24));
  const [minutes, setMinutes] = useState(String(routine?.max_minutes ?? 30));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const choices = modelChoices(catalog, settings.hidden_models?.[providerName], model);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const body: Omit<RoutineInput, 'enabled'> = {
      name: name.trim(),
      prompt,
      model,
      schedule: scheduleOf(kind, time, weekday, Number(hours)),
      mode,
      max_runs_per_day: Number(perDay),
      max_minutes: Number(minutes),
    };
    try {
      onSaved(routine ? await api.updateRoutine(routine.id, body) : await api.createRoutine(project.id, { ...body, enabled: true }));
      onClose();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => !o && onClose()}
      onClosed={onClosed}
      initialFocus={first}
      title={routine ? 'Edit routine' : 'New routine'}
      description={
        <span className="flex min-w-0 items-center gap-1.5">
          <ProjectBadge badge={project.badge} />
          <span className="truncate">Each run starts a task in {project.name}.</span>
        </span>
      }
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="routine-form" variant="primary" loading={busy} disabled={!name.trim() || !prompt.trim() || !model}>
            {routine ? 'Save' : 'Create routine'}
          </Button>
        </>
      }
    >
      <form id="routine-form" className="flex flex-col gap-4" onSubmit={submit}>
        <Field id="routine-name" label="Name" hint="Each run's task is named after it, with the date.">
          <Input id="routine-name" ref={first} required aria-describedby="routine-name-hint" placeholder="Dependency check" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field id="routine-prompt" label="What the agent should do">
          <textarea
            id="routine-prompt"
            required
            className={areaClass}
            placeholder="Check the dependencies for updates and write a short summary of what changed."
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
          />
        </Field>
        <div className="grid gap-x-3 gap-y-3 sm:grid-cols-2">
          <div className="sm:col-span-2">
            <Field id="routine-when" label="When" hint="In the local time of the host uam runs on.">
              <Select id="routine-when" aria-describedby="routine-when-hint" value={kind} items={SCHEDULE_KINDS} onValueChange={(k) => setKind(k as ScheduleKind)} />
            </Field>
          </div>
          {kind === 'weekly' && (
            <Field id="routine-day" label="Day">
              <Select id="routine-day" value={String(weekday)} items={[1, 2, 3, 4, 5, 6, 0].map((d) => ({ value: String(d), label: WEEKDAYS[d] }))} onValueChange={(d) => setWeekday(Number(d))} />
            </Field>
          )}
          {kind === 'hours' ? (
            <Field id="routine-hours" label="Every how many hours (1 to 24)">
              <Input id="routine-hours" type="number" inputMode="numeric" min={1} max={24} required value={hours} onChange={(e) => setHours(e.target.value)} />
            </Field>
          ) : (
            <Field id="routine-time" label="At">
              <Input id="routine-time" type="time" required value={time} onChange={(e) => setTime(e.target.value)} />
            </Field>
          )}
        </div>
        <Field id="routine-model" label="Model">
          <Select id="routine-model" value={model} items={choices.map(({ model: m, note }) => ({ value: m.id, label: choiceLabel(m.name, note), hidden: !!note }))} onValueChange={setModel} />
        </Field>
        <div className="flex flex-col gap-1">
          <span id="routine-mode-label" className="text-caption text-muted">
            Permissions
          </span>
          <Segmented
            aria-labelledby="routine-mode-label"
            aria-describedby="routine-mode-hint"
            className="self-start"
            value={mode}
            items={[
              { value: 'safe', label: 'Safe' },
              { value: 'yolo', label: 'Yolo' },
            ]}
            onValueChange={(v) => setMode(v as 'safe' | 'yolo')}
          />
          {mode === 'safe' ? (
            <Note id="routine-mode-hint">Each permission request waits for you, and the run shows as Needs you until you answer.</Note>
          ) : (
            <Note id="routine-mode-hint" tone="warn" role="status">
              Yolo allows every permission request without asking, while nobody is watching: the agent can run any command and change any file it can reach. Use it only for work you would let run unattended.
            </Note>
          )}
        </div>
        <div className="grid gap-x-3 gap-y-3 sm:grid-cols-2">
          <Field id="routine-per-day" label="Runs a day, at most" hint="Run now counts too.">
            <Input id="routine-per-day" type="number" inputMode="numeric" min={1} max={100} required aria-describedby="routine-per-day-hint" value={perDay} onChange={(e) => setPerDay(e.target.value)} />
          </Field>
          <Field id="routine-minutes" label="Stop a run after (minutes)" hint="A turn still running then is cancelled.">
            <Input id="routine-minutes" type="number" inputMode="numeric" min={1} max={720} required aria-describedby="routine-minutes-hint" value={minutes} onChange={(e) => setMinutes(e.target.value)} />
          </Field>
        </div>
        {error && (
          <Note tone="error" role="alert">
            {error}
          </Note>
        )}
      </form>
    </Dialog>
  );
}
