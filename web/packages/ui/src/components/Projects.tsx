import { ApiContext, useApi } from '../ApiContext';
import type { Machine, MachineChoice } from '../FederationContext';
import { ArrowRight, Check, Clock, FolderMinus, FolderOpen, History } from 'lucide-react';
import { Suspense, lazy, useEffect, useRef, useState, type SubmitEvent } from 'react';
import { describeError, isStatus, type Project, type SessionSummary } from '../api';
import { Loading, Note, ProjectBadge, useApp } from './common';
import { cn } from '../lib/cn';
import { cleanPath } from '../lib/folders';
import { PreviousSessionsDialog, canImport } from './PreviousSessions';
import { Field } from './TaskDefaults';
import { Button } from './ui/button';
import { usePresence } from './ui/collapse';
import { AlertDialog, Dialog } from './ui/dialog';
import { Input } from './ui/input';
import { Select } from './ui/select';

/** The folder picker loads when Add project opens, never with the app. */
const FolderPicker = lazy(() => import('./FolderPicker').then((m) => ({ default: m.FolderPicker })));

/** Add a project by directory. A 409 means the directory already has one: that project is selected instead. */
/** Shared by the three dialogs: `open` drives the transition, `onClosed` fires after it, then the owner unmounts. */
export interface DialogLifecycle {
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
}

export function AddProjectDialog({ open, onClose, onClosed, onAdded, onExisting, machines, choices }: DialogLifecycle & {
  /** `machine`: the machine it was added on, with connected instances. */
  onAdded: (p: Project, machine?: string) => void;
  onExisting: (projectId: string, machine?: string) => void;
  /** With connected instances: the machines a Project can be added on (the one on screen first chosen), and every instance with why it cannot be chosen. */
  machines?: Machine[];
  choices?: MachineChoice[];
}) {
  const shown = useApi();
  const [machine, setMachine] = useState(() => shown.owner?.id ?? '');
  // The folder picker and the request go to the chosen machine.
  const target = machines?.find((m) => m.id === machine);
  const api = target?.client ?? shown;
  const elsewhere = api !== shown;
  const { meta } = useApp();
  // Recent folders are the machine on screen's.
  const recent = elsewhere ? [] : meta?.recent_workdirs ?? [];
  const browsable = api.supports('files-v1');
  const first = useRef<HTMLInputElement>(null);
  const chooser = useRef<HTMLDivElement>(null);
  const [dir, setDir] = useState<string | null>(null);
  const [start, setStart] = useState('');
  const [browseKey, setBrowseKey] = useState(0);
  const [review, setReview] = useState(false);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const available = !machines || !!target;
  const ready = available && !!dir?.trim().startsWith('/');
  const instanceName = target?.label ?? shown.owner?.label ?? 'This instance';
  const folderName = cleanPath(dir ?? '').split('/').filter(Boolean).at(-1) ?? '/';

  useEffect(() => {
    if (review) first.current?.focus();
    else (chooser.current?.querySelector<HTMLElement>('[role="listbox"]') ?? chooser.current?.querySelector<HTMLInputElement>('input'))?.focus();
  }, [review]);

  function choose(path: string) {
    if (!available || !path.startsWith('/')) return;
    setDir(path);
    setError(null);
    setReview(true);
  }

  function openRecent(path: string) {
    setDir(browsable ? null : path);
    setStart(path);
    setBrowseKey((key) => key + 1);
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!review || !ready || !dir || busy) return;
    setBusy(true);
    setError(null);
    try {
      const added = await api.createProject({ dir: dir.trim(), name: name.trim() || undefined });
      if (target) onAdded(added, target.id);
      else onAdded(added);
      onClose();
    } catch (err) {
      const existing = isStatus(err, 409) ? err.body.project_id : undefined;
      if (typeof existing === 'string' && existing) {
        if (target) onExisting(existing, target.id);
        else onExisting(existing);
        onClose();
        return;
      }
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
      title={review ? 'Add a project' : 'Choose a project folder'}
      description="Threads will run inside the folder you choose."
      className="max-w-sheet-wide"
      footer={
        <>
          {!review && dir && (
            <div className="mr-auto flex min-w-0 flex-1 items-center gap-2 self-center text-caption text-muted max-sm:basis-full">
              <FolderOpen aria-hidden="true" className="size-4 shrink-0" />
              <span className="min-w-0 truncate" title={dir}>{dir}</span>
            </div>
          )}
          <Button variant="secondary" disabled={busy} onClick={() => review ? setReview(false) : onClose()}>
            {review ? 'Back' : 'Cancel'}
          </Button>
          {review ? (
            <Button type="submit" form="add-project" variant="primary" loading={busy} disabled={!ready}>
              Add project
            </Button>
          ) : (
            <Button variant="primary" disabled={!ready} onClick={(e) => {
              // React can reuse this button as the review's submit button during this click.
              e.preventDefault();
              if (dir) choose(cleanPath(dir));
            }}>
              Continue
              <ArrowRight />
            </Button>
          )}
        </>
      }
    >
      <ol aria-label="Project setup" className="mb-5 flex items-center gap-3 text-caption">
        <li aria-current={!review ? 'step' : undefined} className={cn('flex items-center gap-2', review ? 'text-muted' : 'text-ink')}>
          <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-primary text-on-primary">{review ? <Check aria-hidden="true" className="size-3.5" /> : '1'}</span>
          Choose folder
        </li>
        <li aria-hidden="true" className="h-px min-w-3 flex-1 bg-hairline" />
        <li aria-current={review ? 'step' : undefined} className={cn('flex items-center gap-2', review ? 'text-ink' : 'text-muted')}>
          <span className={cn('flex size-6 shrink-0 items-center justify-center rounded-full', review ? 'bg-primary text-on-primary' : 'bg-sunken')}>2</span>
          Review project
        </li>
      </ol>
      {/* Keep the browser mounted through review so Back restores the same folder and selection. */}
      <div ref={chooser} hidden={review} className={cn('flex-col gap-4', review ? 'hidden' : 'flex')}>
        {machines && choices ? (
          <Field id="add-machine" label="Instance">
            <Select
              id="add-machine"
              value={machine}
              onValueChange={(id) => {
                setMachine(id);
                setDir(null);
                setStart('');
                setName('');
                setError(null);
                setBrowseKey((key) => key + 1);
              }}
              items={choices.map((c) => {
                const enabled = machines.some((m) => m.id === c.id);
                return { value: c.id, label: c.label, disabled: !enabled || !!c.reason, description: c.reason ?? (enabled ? undefined : 'Unavailable') };
              })}
            />
          </Field>
        ) : <Note>Folders on {instanceName}</Note>}
        {available && browsable ? (
          <ApiContext.Provider value={api}>
            <Suspense fallback={<Loading />}>
              <FolderPicker key={`${machine}:${browseKey}`} id="add-dir-picker" start={start} variant="project" onTargetChange={setDir} onUse={choose} onClose={onClose} />
            </Suspense>
          </ApiContext.Provider>
        ) : available ? (
          <Field id="add-dir" label="Directory on the host">
            <Input
              id="add-dir"
              className="text-ui"
              ref={first}
              spellCheck={false}
              placeholder="/path/to/project"
              value={dir ?? ''}
              onChange={(e) => setDir(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && ready && dir) {
                  e.preventDefault();
                  choose(cleanPath(dir));
                }
              }}
            />
            <Note className="mt-2">{`${instanceName} does not offer folder browsing to connected instances. Type the folder's path, or update UAM there.`}</Note>
            {dir && !ready && <Note tone="error" className="mt-2">Enter an absolute folder path.</Note>}
          </Field>
        ) : <Note tone="error">This instance is unavailable. Choose another instance.</Note>}
        {recent.length > 0 && available && (
          <Select
            aria-label="Recent folders"
            value=""
            items={[{ value: '', label: 'Recent folders…', hidden: true }, ...recent.map((w) => ({ value: w, label: w }))]}
            onValueChange={(path) => path && openRecent(path)}
          />
        )}
      </div>
      {review && (
        <form id="add-project" className="flex flex-col gap-4" onSubmit={submit}>
          <div className="flex items-start gap-3">
            <FolderOpen aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-muted" />
            <div className="min-w-0">
              <h3 className="break-words text-title text-ink">Ready to add {name.trim() || folderName}?</h3>
              <Note>Review where your threads will run.</Note>
            </div>
          </div>
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-3 rounded-sm bg-canvas p-3 text-ui">
            <dt className="text-muted">Instance</dt><dd className="break-words text-ink">{instanceName}</dd>
            <dt className="text-muted">Folder</dt><dd className="break-all text-ink">{dir}</dd>
          </dl>
          <Field id="add-name" label="Project name" hint="Leave empty to use the folder name.">
            <Input id="add-name" ref={first} placeholder="Defaults to the folder name" aria-describedby="add-name-hint" value={name} disabled={busy} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Note>Badge assigned when added. Its letters and colour are chosen from those still available.</Note>
          {error && <Note tone="error" role="alert">{error}</Note>}
        </form>
      )}
    </Dialog>
  );
}

/**
 * The one place for a Project: its name, with Previous sessions (import) and Remove project
 * opening over it, so closing either lands back here. What new Tasks start with is in Settings.
 */
export function EditProjectDialog({ open, onClose, onClosed, project, tasks, onUpdated, onRemoved, onRoutines }: DialogLifecycle & { project: Project; tasks: SessionSummary[]; onUpdated: (p: Project) => void; onRemoved: (id: string) => void; onRoutines?: () => void }) {
  const api = useApi();
  const { meta } = useApp();
  const first = useRef<HTMLInputElement>(null);
  const [name, setName] = useState(project.name);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [previous, setPrevious] = useState(false);
  const previousPresence = usePresence(previous);
  const [removing, setRemoving] = useState(false);
  const [removeOpen, setRemoveOpen] = useState(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onUpdated(await api.updateProject(project.id, { name: name.trim() }));
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
      title={
        <span className="flex items-center gap-2">
          <ProjectBadge badge={project.badge} />
          Edit project
        </span>
      }
      description={<span className="text-caption">{project.dir}</span>}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="edit-project" variant="primary" loading={busy} disabled={!name.trim()}>
            Save
          </Button>
        </>
      }
    >
      <form id="edit-project" className="flex flex-col gap-4" onSubmit={submit}>
        <Field id="edit-name" label="Name">
          <Input id="edit-name" ref={first} value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        {error && (
          <Note tone="error" role="alert">
            {error}
          </Note>
        )}
        {/* The Project's other doors, secondary here: Routines opens its view in place of this dialog; the others open over it and land back on their button. */}
        <div className="fade-rule mt-1" aria-hidden="true" />
        <div className="flex flex-wrap gap-2 pt-3">
          {onRoutines && (
            <Button
              variant="secondary"
              onClick={() => {
                onClose();
                onRoutines();
              }}
            >
              <Clock />
              Routines
            </Button>
          )}
          {canImport(meta) && (
            <Button variant="secondary" onClick={() => setPrevious(true)}>
              <History />
              Previous sessions
            </Button>
          )}
          <Button
            variant="danger"
            onClick={() => {
              setRemoving(true);
              setRemoveOpen(true);
            }}
          >
            <FolderMinus />
            Remove project
          </Button>
        </div>
      </form>
      {previousPresence.mounted && <PreviousSessionsDialog open={previous} project={project} onClose={() => setPrevious(false)} onClosed={previousPresence.onClosed} />}
      {removing && (
        <RemoveProjectDialog
          open={removeOpen}
          onClose={() => setRemoveOpen(false)}
          onClosed={() => setRemoving(false)}
          project={project}
          tasks={tasks}
          onRemoved={(id) => {
            onRemoved(id);
            onClose();
          }}
        />
      )}
    </Dialog>
  );
}

export function RemoveProjectDialog({ open, onClose, onClosed, project, tasks, onRemoved }: DialogLifecycle & { project: Project; tasks: SessionSummary[]; onRemoved: (id: string) => void }) {
  const api = useApi();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const unarchived = tasks.filter((t) => t.stage !== 'archived').length;

  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      try {
        await api.deleteProject(project.id);
      } catch (e) {
        if (isStatus(e, 409)) throw new Error('Archive every thread in this project before removing it.', { cause: e });
        throw e;
      }
      onRemoved(project.id);
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(o) => !o && onClose()}
      onClosed={onClosed}
      title={`Remove ${project.name}?`}
      description={
        <>
          This removes the project and its {tasks.length === 1 ? 'one thread' : `${tasks.length} threads`} from UAM. The directory <code className="rounded-xs bg-sunken px-1 font-sans text-caption">{project.dir}</code> and the
          provider conversations in it are untouched.
        </>
      }
      confirmLabel="Remove project"
      busy={busy}
      disabled={unarchived > 0}
      onConfirm={() => void confirm()}
    >
      <div className="mt-3 flex min-w-0 items-center gap-2 text-ui font-medium text-ink">
        <ProjectBadge badge={project.badge} />
        <span className="truncate" title={project.name}>{project.name}</span>
      </div>
      {unarchived > 0 && (
        <Note tone="warn" className="mt-3">
          {unarchived === 1 ? 'One thread is' : `${unarchived} threads are`} not archived. Archive every thread before removing this project.
        </Note>
      )}
      {error && (
        <Note tone="error" role="alert" className="mt-3">
          {error}
        </Note>
      )}
    </AlertDialog>
  );
}
