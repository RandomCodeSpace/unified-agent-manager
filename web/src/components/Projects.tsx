import { useRef, useState, type FormEvent } from 'react';
import { api, describeError, isStatus, resolveTaskDefaults, type Project, type SessionSummary, type TaskDefaults } from '../api';
import { Note, useApp } from './common';
import { Field, TaskDefaultsFields } from './TaskDefaults';
import { Button } from './ui/button';
import { AlertDialog, Dialog } from './ui/dialog';

const inputClass = 'h-9 w-full rounded-sm border border-hairline-strong bg-raised px-2.5 text-ui text-ink outline-hidden transition-colors placeholder:text-muted focus:border-accent disabled:opacity-45';

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
  const [dir, setDir] = useState('');
  const [name, setName] = useState('');
  // Defaults start from what New task would use today; the dialog mounts fresh each time it opens.
  const [defaults, setDefaults] = useState<TaskDefaults | null>(() => resolveTaskDefaults(meta));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onAdded(await api.createProject({ dir: dir.trim(), name: name.trim() || undefined, defaults: defaults ?? undefined }));
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
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} onClosed={onClosed} initialFocus={first} title="Add a project" description="A directory on this host. Tasks run inside it.">
      <form id="add-project" className="flex flex-col gap-4" onSubmit={submit}>
        <Field id="add-dir" label="Directory on the host">
          <input
            id="add-dir"
            className={`${inputClass} font-mono text-code-sm`}
            type="text"
            list="recent-workdirs"
            required
            ref={first}
            spellCheck={false}
            placeholder="/path/to/project"
            value={dir}
            onChange={(e) => setDir(e.target.value)}
          />
          <datalist id="recent-workdirs">
            {recent.map((w) => (
              <option key={w} value={w} />
            ))}
          </datalist>
        </Field>
        <Field id="add-name" label="Name">
          <input id="add-name" className={inputClass} type="text" placeholder="Defaults to the folder name" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        {defaults && (
          <section aria-labelledby="add-defaults-title" className="mt-1 border-t border-hairline pt-4">
            <h3 id="add-defaults-title" className="mb-3 text-title text-ink">
              Defaults for new tasks
            </h3>
            <TaskDefaultsFields prefix="add" value={defaults} disabled={busy} onChange={setDefaults} />
          </section>
        )}
        {error && (
          <Note tone="error" role="alert">
            {error}
          </Note>
        )}
        <div className="flex flex-wrap justify-end gap-2 pt-1 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={busy || !dir.trim()}>
            {busy ? 'Adding…' : 'Add project'}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/** Name and defaults for new Tasks; the defaults start from what New task would use today. */
export function EditProjectDialog({ open, onClose, onClosed, project, onUpdated }: DialogLifecycle & { project: Project; onUpdated: (p: Project) => void }) {
  const { meta } = useApp();
  const first = useRef<HTMLInputElement>(null);
  const [name, setName] = useState(project.name);
  const [defaults, setDefaults] = useState(() => resolveTaskDefaults(meta, project.defaults));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onUpdated(await api.updateProject(project.id, { name: name.trim(), defaults: defaults ?? undefined }));
      onClose();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} onClosed={onClosed} initialFocus={first} title="Edit project" description={<span className="font-mono text-code-sm">{project.dir}</span>}>
      <form className="flex flex-col gap-4" onSubmit={submit}>
        <Field id="edit-name" label="Name">
          <input id="edit-name" className={inputClass} type="text" ref={first} value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        {defaults && (
          <section aria-labelledby="edit-defaults-title" className="mt-1 border-t border-hairline pt-4">
            <h3 id="edit-defaults-title" className="mb-3 text-title text-ink">
              Defaults for new tasks
            </h3>
            <TaskDefaultsFields prefix="edit" value={defaults} disabled={busy} onChange={setDefaults} />
          </section>
        )}
        {error && (
          <Note tone="error" role="alert">
            {error}
          </Note>
        )}
        <div className="flex flex-wrap justify-end gap-2 pt-1 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={busy || !name.trim()}>
            {busy ? 'Saving…' : 'Save'}
          </Button>
        </div>
      </form>
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
          This removes the project and its {tasks.length === 1 ? 'one task' : `${tasks.length} tasks`} from UAM. The directory <code className="rounded-xs bg-sunken px-1 font-mono text-code-sm">{project.dir}</code> and the
          provider conversations in it are untouched.
        </>
      }
      confirmLabel="Remove project"
      busy={busy}
      disabled={unarchived > 0}
      onConfirm={() => void confirm()}
    >
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
