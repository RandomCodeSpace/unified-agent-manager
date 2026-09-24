import { modelCatalog, provider, type TaskDefaults } from '../api';
import { modelChoices } from '../lib/models';
import { MODE_TEXT, contextReason, effortReason, sizeLabel } from './Composer';
import { Note, useApp } from './common';
import { Select } from './ui/select';

export function Field({ id, label, hint, children }: { id: string; label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor={id} className="text-caption text-muted">
        {label}
      </label>
      {children}
      {hint && <Note id={`${id}-hint`}>{hint}</Note>}
    </div>
  );
}

/** The settings a new Task starts with: model, effort, context size and mode. Used by the Add and Edit project dialogs. */
export function TaskDefaultsFields({ prefix, value, disabled, onChange }: { prefix: string; value: TaskDefaults; disabled: boolean; onChange: (next: TaskDefaults) => void }) {
  const { meta, settings } = useApp();
  const chosen = provider(meta, value.provider);
  const catalog = modelCatalog(meta, value.provider);
  const choices = modelChoices(catalog, settings.hidden_models?.[value.provider], value.model);
  const model = catalog.find((m) => m.id === value.model);
  const noEffort = effortReason(model);
  const noContext = contextReason(model, !!chosen?.capabilities.context_size);
  const sizes = model?.context_sizes ?? [];
  return (
    <div className="grid grid-cols-2 gap-x-3 gap-y-3 max-sm:grid-cols-1">
      {catalog.length > 0 && (
        <div className="col-span-full">
          <Field id={`${prefix}-model`} label="Model">
            <Select
              id={`${prefix}-model`}
              mono
              value={value.model}
              disabled={disabled}
              items={choices.map(({ model: m, note }) => ({ value: m.id, label: `${m.name}${note ? ` (${note.toLowerCase()})` : ''}`, hidden: !!note }))}
              onValueChange={(id) => {
                const next = catalog.find((m) => m.id === id);
                onChange({
                  ...value,
                  model: id,
                  effort: next?.efforts?.includes(value.effort) ? value.effort : '',
                  context_size: next?.context_sizes?.some((s) => s.id === value.context_size) ? value.context_size : 'default',
                });
              }}
            />
          </Field>
        </div>
      )}
      <Field id={`${prefix}-effort`} label="Effort" hint={noEffort || undefined}>
        <Select
          id={`${prefix}-effort`}
          mono
          value={value.effort}
          disabled={disabled || !!noEffort}
          aria-describedby={noEffort ? `${prefix}-effort-hint` : undefined}
          items={[{ value: '', label: 'Default' }, ...(model?.efforts ?? []).map((e) => ({ value: e, label: e }))]}
          onValueChange={(effort) => onChange({ ...value, effort })}
        />
      </Field>
      <Field id={`${prefix}-context-size`} label="Context size" hint={noContext || (value.context_size === 'long_context' ? 'Long context may cost more.' : undefined)}>
        <Select
          id={`${prefix}-context-size`}
          mono
          value={value.context_size || 'default'}
          disabled={disabled || !!noContext}
          aria-describedby={noContext ? `${prefix}-context-size-hint` : undefined}
          items={[
            ...(sizes.some((s) => s.id === 'default') ? [] : [{ value: 'default', label: 'Default' }]),
            ...sizes.map((s) => ({ value: s.id, label: `${sizeLabel(s.id)} · ${s.tokens.toLocaleString()}` })),
          ]}
          onValueChange={(context_size) => onChange({ ...value, context_size })}
        />
      </Field>
      <div className="col-span-full">
        <Field id={`${prefix}-mode`} label="Mode" hint={MODE_TEXT[value.mode]}>
          <Select
            id={`${prefix}-mode`}
            value={value.mode}
            disabled={disabled}
            items={[
              { value: 'safe', label: 'Safe' },
              { value: 'yolo', label: 'Yolo' },
            ]}
            onValueChange={(mode) => onChange({ ...value, mode: mode as 'safe' | 'yolo' })}
          />
        </Field>
      </div>
    </div>
  );
}
