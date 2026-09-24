import { useState, type FormEvent } from 'react';
import { api, describeError, isStatus, type Project, type SessionSummary } from '../api';
import { ConfirmDialog, Dialog, NameDialog, useApp } from './common';

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
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

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

export function RenameProjectDialog({ project, onRenamed, onClose }: { project: Project; onRenamed: (p: Project) => void; onClose: () => void }) {
  return (
    <NameDialog
      title="Rename project"
      label="Name"
      initial={project.name}
      onSubmit={async (name) => onRenamed(await api.renameProject(project.id, name))}
      onClose={onClose}
    />
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
