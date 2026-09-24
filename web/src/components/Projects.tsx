import { useState, type FormEvent } from 'react';
import { api, describeError, isStatus, resolveTaskDefaults, type Project, type SessionSummary, type TaskDefaults } from '../api';
import { ConfirmDialog, Dialog, useApp } from './common';
import { TaskDefaultsFields } from './TaskDefaults';

/** Add a project by directory. A 409 means the directory already has one: that project is selected instead. */
export function AddProjectDialog({
  onAdded,
  onExisting,
  onClose,
}: {
  onAdded: (p: Project) => void;
  onExisting: (projectId: string) => void;
  onClose: () => void;
}) {
  const { meta } = useApp();
  const recent = meta?.recent_workdirs ?? [];
  const [dir, setDir] = useState('');
  const [name, setName] = useState('');
  const [defaults, setDefaults] = useState(() => resolveTaskDefaults(meta));
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
    <Dialog title="Add a project" onClose={onClose}>
      <form className="form" onSubmit={submit}>
        <label className="field">
          <span className="control-label">Directory on the host</span>
          <input
            className="input mono"
            type="text"
            list="recent-workdirs"
            required
            autoFocus
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
        </label>
        <label className="field">
          <span className="control-label">Name</span>
          <input className="input" type="text" placeholder="Defaults to the folder name" value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <DefaultsSection prefix="add" value={defaults} disabled={busy} onChange={setDefaults} />
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          <button type="button" className="btn btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy || !dir.trim()}>
            Add project
          </button>
        </div>
      </form>
    </Dialog>
  );
}

/** Name and defaults for new Tasks; the defaults start from what New task would use today. */
export function EditProjectDialog({ project, onUpdated, onClose }: { project: Project; onUpdated: (p: Project) => void; onClose: () => void }) {
  const { meta } = useApp();
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
    <Dialog title="Edit project" onClose={onClose}>
      <form className="form" onSubmit={submit}>
        <label className="field">
          <span className="control-label">Name</span>
          <input className="input" type="text" autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <DefaultsSection prefix="edit" value={defaults} disabled={busy} onChange={setDefaults} />
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          <button type="button" className="btn btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy || !name.trim()}>
            Save
          </button>
        </div>
      </form>
    </Dialog>
  );
}

/** Hidden until the provider list has loaded; the dialog then submits without defaults. */
function DefaultsSection({ prefix, value, disabled, onChange }: { prefix: string; value: TaskDefaults | null; disabled: boolean; onChange: (next: TaskDefaults) => void }) {
  if (!value) return null;
  return (
    <div className="dialog-section">
      <h3 className="eyebrow">Defaults for new tasks</h3>
      <TaskDefaultsFields prefix={prefix} value={value} disabled={disabled} onChange={onChange} />
    </div>
  );
}

export function RemoveProjectDialog({
  project,
  tasks,
  onRemoved,
  onClose,
}: {
  project: Project;
  tasks: SessionSummary[];
  onRemoved: (id: string) => void;
  onClose: () => void;
}) {
  const unarchived = tasks.filter((t) => t.stage !== 'archived').length;
  return (
    <ConfirmDialog
      title={`Remove ${project.name}?`}
      confirmLabel="Remove project"
      disabled={unarchived > 0}
      onClose={onClose}
      onConfirm={async () => {
        try {
          await api.deleteProject(project.id);
        } catch (e) {
          if (isStatus(e, 409)) throw new Error('Archive every task in this project before removing it.');
          throw e;
        }
        onRemoved(project.id);
      }}
    >
      <p>
        This removes the project and its {tasks.length === 1 ? 'one task' : `${tasks.length} tasks`} from UAM. The directory{' '}
        <code>{project.dir}</code> and the provider conversations in it are untouched.
      </p>
      {unarchived > 0 && (
        <p className="warn">
          {unarchived === 1 ? 'One task is' : `${unarchived} tasks are`} not archived. Archive every task before removing this project.
        </p>
      )}
    </ConfirmDialog>
  );
}
