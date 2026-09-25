import { FolderMinus, FolderOpen, History } from 'lucide-react';
import { useRef, useState, type FormEvent } from 'react';
import { api, describeError, isStatus, type Project, type SessionSummary } from '../api';
import { Note, ProjectBadge, useApp } from './common';
import { cn } from '../lib/cn';
import { FolderPicker } from './FolderPicker';
import { PreviousSessionsDialog, canImport } from './PreviousSessions';
import { Field } from './TaskDefaults';
import { Button } from './ui/button';
import { AlertDialog, Dialog } from './ui/dialog';
import { Collapse, usePresence } from './ui/collapse';
import { Input } from './ui/input';
import { Select } from './ui/select';

/** Add a project by directory. A 409 means the directory already has one: that project is selected instead. */
/** Shared by the three dialogs: `open` drives the transition, `onClosed` fires after it, then the owner unmounts. */
export interface DialogLifecycle {
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
}

export function AddProjectDialog({ open, onClose, onClosed, onAdded, onExisting }: DialogLifecycle & { onAdded: (p: Project) => void; onExisting: (projectId: string) => void }) {
  const { meta } = useApp();
  const recent = meta?.recent_workdirs ?? [];
  const first = useRef<HTMLInputElement>(null);
  const browse = useRef<HTMLButtonElement>(null);
  const [dir, setDir] = useState('');
  const [browsing, setBrowsing] = useState(false);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The picker collapses in and out (grid rows) so the dialog's height glides instead of jumping.
  const picker = usePresence(browsing);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onAdded(await api.createProject({ dir: dir.trim(), name: name.trim() || undefined }));
      onClose();
    } catch (err) {
      const existing = isStatus(err, 409) ? err.body.project_id : undefined;
      if (typeof existing === 'string' && existing) {
        onExisting(existing);
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
      title="Add a project"
      description="A directory on this host. Tasks run inside it."
      // The dialog widens while the folder picker is open (DESIGN.md: 560px) and settles back once a folder is chosen.
      className={cn('transition-[opacity,transform,max-width]', browsing && 'max-w-sheet-wide')}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="add-project" variant="primary" loading={busy} disabled={!dir.trim()}>
            Add project
          </Button>
        </>
      }
    >
      <form id="add-project" className="flex flex-col gap-4" onSubmit={submit}>
        <Field id="add-dir" label="Directory on the host">
          <div className="flex gap-2">
            <Input
              id="add-dir"
              className="text-ui"
              required
              ref={first}
              spellCheck={false}
              placeholder="/path/to/project"
              value={dir}
              onChange={(e) => setDir(e.target.value)}
            />
            <Button ref={browse} variant="secondary" size="lg" aria-expanded={browsing} aria-controls={browsing ? 'add-dir-picker' : undefined} onClick={() => setBrowsing(!browsing)}>
              <FolderOpen />
              Browse
            </Button>
          </div>
          {recent.length > 0 && (
            <Select
              aria-label="Recent folders"
              value=""
              className="mt-2"
              items={[{ value: '', label: 'Recent folders…', hidden: true }, ...recent.map((w) => ({ value: w, label: w }))]}
              onValueChange={(w) => w && setDir(w)}
            />
          )}
        </Field>
        {picker.mounted && (
          <Collapse open={browsing} appear onClosed={picker.onClosed} className="-mt-4" inner="pt-4">
            <FolderPicker
              id="add-dir-picker"
              start={dir}
              onUse={(p) => {
                setDir(p);
                setBrowsing(false);
                first.current?.focus();
              }}
              onClose={() => {
                setBrowsing(false);
                browse.current?.focus();
              }}
            />
          </Collapse>
        )}
        <Field id="add-name" label="Name" hint="The project gets a two-letter badge from this name, on a colour of its own.">
          <Input id="add-name" placeholder="Defaults to the folder name" aria-describedby="add-name-hint" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        {(name.trim() || dir.trim()) && <Note>Badge assigned when added. Its letters and colour are chosen from those still available.</Note>}
        {error && (
          <Note tone="error" role="alert">
            {error}
          </Note>
        )}
      </form>
    </Dialog>
  );
}

/**
 * The one place for a Project: its name, with Previous sessions (import) and Remove project
 * opening over it, so closing either lands back here. What new Tasks start with is in Settings.
 */
export function EditProjectDialog({ open, onClose, onClosed, project, tasks, onUpdated, onRemoved }: DialogLifecycle & { project: Project; tasks: SessionSummary[]; onUpdated: (p: Project) => void; onRemoved: (id: string) => void }) {
  const { meta } = useApp();
  const first = useRef<HTMLInputElement>(null);
  const [name, setName] = useState(project.name);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [previous, setPrevious] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [removeOpen, setRemoveOpen] = useState(false);

  async function submit(e: FormEvent) {
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
        {/* The Project's other two doors, secondary here: each opens over this dialog and lands back on its button. */}
        <div className="fade-rule mt-1" aria-hidden="true" />
        <div className="flex flex-wrap gap-2 pt-3">
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
      {previous && <PreviousSessionsDialog project={project} onClose={() => setPrevious(false)} />}
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
        if (isStatus(e, 409)) throw new Error('Archive every task in this project before removing it.', { cause: e });
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
          This removes the project and its {tasks.length === 1 ? 'one task' : `${tasks.length} tasks`} from UAM. The directory <code className="rounded-xs bg-sunken px-1 font-sans text-caption">{project.dir}</code> and the
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
          {unarchived === 1 ? 'One task is' : `${unarchived} tasks are`} not archived. Archive every task before removing this project.
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
