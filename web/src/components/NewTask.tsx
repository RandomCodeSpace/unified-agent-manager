import { useRef, useState, type FormEvent } from 'react';
import { api, describeError, modelCatalog, newRequestId, type Project, type SessionSummary } from '../api';
import { useApp } from './common';

export function NewTask({
  project,
  onCreated,
  onCancel,
}: {
  project: Project;
  onCreated: (s: SessionSummary) => void;
  onCancel: () => void;
}) {
  const { meta } = useApp();
  const providers = meta?.providers ?? [];
  const chosen = providers.find((p) => p.available) ?? providers[0];
  const catalog = chosen ? modelCatalog(meta, chosen.name) : [];
  const [model, setModel] = useState(() => (catalog.some((m) => m.id === 'auto') ? 'auto' : (catalog[0]?.id ?? '')));
  const [name, setName] = useState('');
  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // One request ID per form so a retry after a network failure is idempotent.
  const requestId = useRef(newRequestId());

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!chosen) return;
    setBusy(true);
    setError(null);
    try {
      onCreated(
        await api.createSession({
          project_id: project.id,
          provider: chosen.name,
          model: model || undefined,
          name: name.trim() || undefined,
          prompt: prompt.trim(),
          request_id: requestId.current,
        }),
      );
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  const unavailable = chosen && !chosen.available ? chosen.reason || 'not installed' : null;

  return (
    <div className="column">
      <div className="kicker">
        <span>{project.name}</span>
      </div>
      <h1 className="display title">New task</h1>
      <p className="muted lede">
        Runs in <span className="mono">{project.dir}</span>. Leave the name empty and the provider titles it from your first
        message.
      </p>
      <form className="form newtask" onSubmit={submit}>
        <div className="row wrap controls">
          <span className="control">
            <span className="control-label">Provider</span>
            <span className="control-static">{chosen ? chosen.display_name : meta ? 'No providers' : 'Loading…'}</span>
          </span>
          {catalog.length > 0 && (
            <span className="control">
              <label htmlFor="nt-model" className="control-label">
                Model
              </label>
              <select id="nt-model" className="select" value={model} onChange={(e) => setModel(e.target.value)}>
                {catalog.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.name}
                  </option>
                ))}
              </select>
            </span>
          )}
        </div>
        {unavailable && (
          <p className="error" role="alert">
            {chosen?.display_name} is unavailable: {unavailable}
          </p>
        )}
        <label className="field">
          <span className="control-label">Name</span>
          <input
            className="input"
            type="text"
            autoFocus
            placeholder="Optional"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label className="field">
          <span className="control-label">First message</span>
          <textarea
            className="input"
            rows={4}
            value={prompt}
            placeholder="What should the agent do?"
            onChange={(e) => setPrompt(e.target.value)}
          />
        </label>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="row">
          <button type="submit" className="pill pill-primary" disabled={busy || !chosen?.available || !prompt.trim()}>
            {busy ? 'Starting…' : 'Start task'}
          </button>
          <button type="button" className="pill pill-text" onClick={onCancel}>
            Cancel
          </button>
        </div>
      </form>
    </div>
  );
}
