import { useRef, useState, type FormEvent } from 'react';
import {
  api,
  basename,
  describeError,
  newRequestId,
  pendingCount,
  providerLabel,
  sessionName,
  type Meta,
  type SessionSummary,
} from '../api';
import { Dialog, Sep, StateBadge } from './common';

interface Props {
  sessions: SessionSummary[];
  selectedId: string | null;
  meta: Meta | null;
  onSelect: (id: string) => void;
  onCreated: (s: SessionSummary) => void;
}

export function Sidebar({ sessions, selectedId, meta, onSelect, onCreated }: Props) {
  const [creating, setCreating] = useState(false);
  return (
    <>
      <div className="panel-head">
        <h2>Sessions</h2>
        <button type="button" className="btn small" onClick={() => setCreating(true)}>
          New session
        </button>
      </div>
      {sessions.length === 0 && <p className="muted pad">No sessions yet.</p>}
      <ul className="session-list">
        {sessions.map((s) => {
          const pending = pendingCount(s);
          const selected = s.id === selectedId;
          return (
            <li key={s.id}>
              <button
                type="button"
                className={selected ? 'session-row selected' : 'session-row'}
                aria-current={selected ? 'true' : undefined}
                onClick={() => onSelect(s.id)}
              >
                <span className="row-top">
                  <span className="session-name">{sessionName(s)}</span>
                  {pending > 0 && (
                    <>
                      <Sep />
                      <span className="pending-badge">Needs input{pending > 1 ? ` (${pending})` : ''}</span>
                    </>
                  )}
                </span>
                <Sep />
                <span className="row-meta">
                  <span>{providerLabel(meta, s.provider)}</span>
                  <Sep />
                  <span className="project" title={s.workdir}>
                    {basename(s.workdir)}
                  </span>
                </span>
                <Sep />
                <StateBadge state={s.state} />
              </button>
            </li>
          );
        })}
      </ul>
      {creating && (
        <CreateDialog
          meta={meta}
          onClose={() => setCreating(false)}
          onCreated={(s) => {
            setCreating(false);
            onCreated(s);
          }}
        />
      )}
    </>
  );
}

function CreateDialog({
  meta,
  onClose,
  onCreated,
}: {
  meta: Meta | null;
  onClose: () => void;
  onCreated: (s: SessionSummary) => void;
}) {
  const providers = meta?.providers ?? [];
  const recent = meta?.recent_workdirs ?? [];
  const [provider, setProvider] = useState(providers.find((p) => p.available)?.name ?? '');
  const [workdir, setWorkdir] = useState(recent[0] ?? '');
  const [name, setName] = useState('');
  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // One request ID per dialog so a retry after a network failure is idempotent.
  const requestId = useRef(newRequestId());

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const text = prompt.trim();
      onCreated(
        await api.createSession({
          provider,
          workdir: workdir.trim(),
          name: name.trim(),
          prompt: text || undefined,
          request_id: requestId.current,
        }),
      );
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog title="New session" onClose={onClose}>
      <form className="form" onSubmit={submit}>
        <label>
          Provider
          <select value={provider} onChange={(e) => setProvider(e.target.value)} required>
            {providers.length === 0 && <option value="">{meta ? 'No providers' : 'Loading…'}</option>}
            {providers.map((p) => (
              <option key={p.name} value={p.name} disabled={!p.available}>
                {p.display_name}
                {p.available ? '' : ` — unavailable: ${p.reason || 'not installed'}`}
              </option>
            ))}
          </select>
        </label>
        <label>
          Project directory
          <input
            type="text"
            list="recent-workdirs"
            required
            spellCheck={false}
            value={workdir}
            onChange={(e) => setWorkdir(e.target.value)}
          />
          <datalist id="recent-workdirs">
            {recent.map((w) => (
              <option key={w} value={w} />
            ))}
          </datalist>
        </label>
        <label>
          Name
          <input type="text" required value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label>
          First prompt (optional)
          <textarea rows={3} value={prompt} onChange={(e) => setPrompt(e.target.value)} />
        </label>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn primary" disabled={busy || !provider}>
            Create
          </button>
        </div>
      </form>
    </Dialog>
  );
}
