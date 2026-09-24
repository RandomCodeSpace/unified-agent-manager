import type { Model } from '../api';

/** The same provider catalog drives creation and next-turn controls. */
export function GenerationSettings({ prefix, model, effort, contextSize, contextSupported, disabled, onChange }: {
  prefix: string;
  model?: Model;
  effort: string;
  contextSize: string;
  contextSupported: boolean;
  disabled: boolean;
  onChange: (value: { effort?: string; context_size?: string }) => void;
}) {
  const sizes = model?.context_sizes ?? [];
  const contextReason = !contextSupported ? 'Context size selection is unavailable for this provider.'
    : sizes.length === 0 ? 'This model uses its default context size.' : '';
  return <>
    <label className="control generation-control" htmlFor={`${prefix}-effort`}>
      <span className="control-label">Effort</span>
      <select id={`${prefix}-effort`} className="input mono" disabled={disabled} value={effort} onChange={(e) => onChange({ effort: e.target.value })}>
        <option value="">Default</option>
        {(model?.efforts ?? []).map((value) => <option key={value} value={value}>{value}</option>)}
      </select>
    </label>
    <label className="control generation-control" htmlFor={`${prefix}-context-size`}>
      <span className="control-label">Context size</span>
      <select id={`${prefix}-context-size`} className="input mono" disabled={disabled || !!contextReason} title={contextReason || undefined}
        aria-describedby={contextReason ? `${prefix}-context-reason` : undefined}
        value={contextSize || 'default'} onChange={(e) => onChange({ context_size: e.target.value })}>
        {!sizes.some((s) => s.id === 'default') && <option value="default">Default</option>}
        {sizes.map((size) => <option key={size.id} value={size.id}>{size.id === 'long_context' ? 'Long context' : 'Default'} · {size.tokens.toLocaleString()} tokens</option>)}
      </select>
    </label>
    {contextReason && <span id={`${prefix}-context-reason`} className="caption settings-note">{contextReason}</span>}
    {contextSize === 'long_context' && <span className="caption settings-note">Long context may cost more.</span>}
  </>;
}
