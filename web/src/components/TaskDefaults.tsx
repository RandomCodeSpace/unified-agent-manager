import { modelCatalog, provider, type TaskDefaults } from '../api';
import { useApp } from './common';
import { GenerationSettings } from './GenerationSettings';

/** The settings a new Task starts with: model, effort, context size and mode. Used by the Add and Edit project dialogs. */
export function TaskDefaultsFields({ prefix, value, disabled, onChange }: {
  prefix: string;
  value: TaskDefaults;
  disabled: boolean;
  onChange: (next: TaskDefaults) => void;
}) {
  const { meta } = useApp();
  const chosen = provider(meta, value.provider);
  const catalog = modelCatalog(meta, value.provider);
  return <>
    {catalog.length > 0 && (
      <div className="form-row">
        <label className="control">
          <span className="control-label">Model</span>
          <select id={`${prefix}-model`} className="input mono" value={value.model} disabled={disabled} onChange={(e) => {
            const next = catalog.find((m) => m.id === e.target.value);
            onChange({
              ...value,
              model: e.target.value,
              effort: next?.efforts?.includes(value.effort) ? value.effort : '',
              context_size: next?.context_sizes?.some((s) => s.id === value.context_size) ? value.context_size : 'default',
            });
          }}>
            {catalog.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
        </label>
      </div>
    )}
    <div className="generation-settings">
      <GenerationSettings prefix={prefix} model={catalog.find((m) => m.id === value.model)} effort={value.effort} contextSize={value.context_size}
        contextSupported={!!chosen?.capabilities.context_size} disabled={disabled} onChange={(next) => {
          onChange({ ...value, effort: next.effort ?? value.effort, context_size: next.context_size ?? value.context_size });
        }} />
      <label className="control"><span className="control-label">Mode</span>
        <select id={`${prefix}-mode`} className="input" disabled={disabled} value={value.mode} onChange={(e) => onChange({ ...value, mode: e.target.value as 'safe' | 'yolo' })}>
          <option value="safe">Safe</option><option value="yolo">Yolo</option>
        </select>
      </label>
    </div>
    <p className="caption">{value.mode === 'yolo' ? 'Yolo allows permission requests automatically. Questions and managed-policy requests still need you.' : 'Safe asks before allowing permission requests.'}</p>
  </>;
}
