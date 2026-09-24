import { useRef, useState, type FormEvent } from 'react';
import { api, describeError, modelCatalog, newRequestId, type Project, type SessionSummary } from '../api';
import { GenerationSettings } from './GenerationSettings';
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
  const [effort, setEffort] = useState('');
  const [contextSize, setContextSize] = useState('default');
  const [mode, setMode] = useState<'safe' | 'yolo'>('safe');
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
          effort,
          context_size: contextSize,
          mode,
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
    <div className="page-column">
      <div className="eyebrow">{project.name}</div>
      <h1 className="display-md">New task</h1>
      <p className="lede">
        Runs in <code>{project.dir}</code>. Leave the name empty and the provider titles it from your first message.
      </p>
      <form className="form newtask" onSubmit={submit}>
        <div className="form-row">
          <span className="control">
            <span className="control-label">Provider</span>
            <span className="control-static">{chosen ? chosen.display_name : meta ? 'No providers' : 'Loading…'}</span>
          </span>
          {catalog.length > 0 && (
            <label className="control">
              <span className="control-label">Model</span>
              <select id="nt-model" className="input mono" value={model} disabled={busy} onChange={(e) => {
                const next = catalog.find((m) => m.id === e.target.value);
                setModel(e.target.value);
                if (!next?.efforts?.includes(effort)) setEffort('');
                if (!next?.context_sizes?.some((s) => s.id === contextSize)) setContextSize('default');
              }}>
                {catalog.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.name}
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
        <div className="generation-settings">
          <GenerationSettings prefix="nt" model={catalog.find((m) => m.id === model)} effort={effort} contextSize={contextSize}
            contextSupported={!!chosen?.capabilities.context_size} disabled={busy} onChange={(value) => {
              if (value.effort !== undefined) setEffort(value.effort);
              if (value.context_size !== undefined) setContextSize(value.context_size);
            }} />
          <label className="control"><span className="control-label">Mode</span>
            <select id="nt-mode" className="input" disabled={busy} value={mode} onChange={(e) => setMode(e.target.value as 'safe' | 'yolo')}>
              <option value="safe">Safe</option><option value="yolo">Yolo</option>
            </select>
          </label>
        </div>
        <p className="caption">{mode === 'yolo' ? 'Yolo allows permission requests automatically. Questions and managed-policy requests still need you.' : 'Safe asks before allowing permission requests.'}</p>
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
        <div className="actions actions-start">
          <button type="submit" className="btn btn-primary" disabled={busy || !chosen?.available || !prompt.trim()}>
            {busy ? 'Starting…' : 'Start task'}
          </button>
          <button type="button" className="btn btn-ghost" onClick={onCancel}>
            Cancel
          </button>
        </div>
      </form>
    </div>
  );
}
