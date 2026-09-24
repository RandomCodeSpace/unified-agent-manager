import { X } from 'lucide-react';
import { useRef, useState, type FormEvent, type ReactNode } from 'react';
import { api, describeError, type CustomModel, type Model, type SendDefault, type Settings } from '../api';
import { Note, Spinner, useApp } from './common';
import { Field, inputClass } from './TaskDefaults';
import { modelCostLine } from '../lib/cost';
import { modelChoices } from '../lib/models';
import { Select } from './ui/select';
import { Switch } from './ui/switch';
import { Button } from './ui/button';
import { Segmented } from './ui/segmented';
import { Tip } from './ui/tooltip';

/** One titled group of settings; a new group is another `Section` below the last. */
function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section aria-labelledby={`${id}-title`} className="flex flex-col gap-3 border-t border-hairline py-4 first:border-t-0 first:pt-0">
      <h2 id={`${id}-title`} className="text-title text-ink">
        {title}
      </h2>
      {children}
    </section>
  );
}

/** A label and its help on the left, the control on the right; stacked on a phone. */
function Row({ id, label, help, children }: { id: string; label: string; help: ReactNode; children: ReactNode }) {
  return (
    <div className="grid items-start gap-x-6 gap-y-2 sm:grid-cols-[minmax(0,1fr)_auto]">
      <div className="flex min-w-0 max-w-3xl flex-col gap-1">
        <span id={`${id}-label`} className="text-ui font-medium text-ink">
          {label}
        </span>
        <Note id={`${id}-help`}>{help}</Note>
      </div>
      {children}
    </div>
  );
}

const emptyCustom = { display_name: '', name: '', base_url: '', model_id: '', api_key_env: '' };

/**
 * Custom (BYOM) models: OpenAI-compatible endpoints offered next to Copilot's models. The key
 * never passes through here; each model names the service environment variable that holds it.
 */
function CustomModels({ models, disabled, onSave }: { models: CustomModel[]; disabled: boolean; onSave: (next: CustomModel[]) => Promise<boolean> }) {
  const [draft, setDraft] = useState(emptyCustom);
  const bare = (list: CustomModel[]) => list.map(({ key_present: _, ...m }) => m);
  const field = (key: keyof typeof emptyCustom, label: string, placeholder: string, mono = false) => (
    <Field id={`custom-${key}`} label={label}>
      <input
        id={`custom-${key}`}
        className={mono ? `${inputClass} font-mono text-code-sm` : inputClass}
        type="text"
        spellCheck={false}
        autoComplete="off"
        required={key !== 'display_name'}
        placeholder={placeholder}
        disabled={disabled}
        value={draft[key]}
        onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
      />
    </Field>
  );
  async function add(e: FormEvent) {
    e.preventDefault();
    const entry: CustomModel = { ...draft, display_name: draft.display_name.trim() || undefined, name: draft.name.trim(), base_url: draft.base_url.trim(), model_id: draft.model_id.trim(), api_key_env: draft.api_key_env.trim() };
    if (await onSave([...bare(models), entry])) setDraft(emptyCustom);
  }
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-ui font-medium">Custom models</h3>
      <Note>
        OpenAI-compatible endpoints, offered with GitHub Copilot's models. The API key stays in the service's environment: name the variable that holds it, export it where the service starts,
        and restart the service.
      </Note>
      {models.map((m, i) => (
        <div key={`${m.name}/${m.model_id}`} className="flex min-h-12 items-center gap-3 border-b border-hairline py-2">
          <div className="flex min-w-0 flex-1 flex-col">
            <span className="text-ui font-medium text-ink">{m.display_name || `${m.name}/${m.model_id}`}</span>
            <span className="min-w-0 break-all font-mono text-keycap text-muted">
              {m.name}/{m.model_id} · {m.base_url}
            </span>
            <Note tone={m.key_present ? 'muted' : 'warn'}>{m.key_present ? `Key from ${m.api_key_env}` : `${m.api_key_env} is not set in the service's environment`}</Note>
          </div>
          <Button size="sm" variant="danger" disabled={disabled} aria-label={`Remove ${m.display_name || `${m.name}/${m.model_id}`}`} onClick={() => void onSave(bare(models.filter((_, j) => j !== i)))}>
            Remove
          </Button>
        </div>
      ))}
      <form aria-label="Add a custom model" className="grid grid-cols-1 items-end gap-3 sm:grid-cols-2 xl:grid-cols-3" onSubmit={(e) => void add(e)}>
        {field('display_name', 'Display name', 'Optional')}
        {field('name', 'Provider name', 'openrouter', true)}
        {field('base_url', 'Base URL', 'https://openrouter.ai/api/v1', true)}
        {field('model_id', 'Model ID', 'qwen/qwen3-coder', true)}
        {field('api_key_env', 'API key variable', 'OPENROUTER_API_KEY', true)}
        <Button type="submit" variant="secondary" size="lg" disabled={disabled} className="justify-self-start">
          Add model
        </Button>
      </form>
    </div>
  );
}

/**
 * The Settings view (issue #183): a page in the main pane, not a dialog, reached from the
 * sidebar's gear and `#settings`. A change shows at once and is saved through PATCH; a
 * refusal puts the old value back and says why.
 */
export function SettingsView({ leading, onClose }: { leading?: ReactNode; onClose: () => void }) {
  const { settings, dispatch, meta } = useApp();
  const saveSequence = useRef(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save(patch: Partial<Settings>) {
    const sequence = ++saveSequence.current;
    const before = settings;
    dispatch({ type: 'settings', settings: { ...settings, ...patch } });
    setError(null);
    setSaving(true);
    try {
      const saved = await api.updateWebSettings(patch);
      if (sequence === saveSequence.current) dispatch({ type: 'settings', settings: saved });
    } catch (e) {
      if (sequence === saveSequence.current) {
        dispatch({ type: 'settings', settings: before });
        setError(`Could not save the setting: ${describeError(e)}`);
      }
    } finally {
      if (sequence === saveSequence.current) setSaving(false);
    }
  }

  /** Custom models are saved without the optimistic step: the model lists reload from what the service stored. */
  async function saveCustom(custom_models: CustomModel[]) {
    const sequence = ++saveSequence.current;
    setError(null);
    setSaving(true);
    try {
      const saved = await api.updateWebSettings({ custom_models });
      if (sequence === saveSequence.current) dispatch({ type: 'settings', settings: saved });
      return true;
    } catch (e) {
      if (sequence === saveSequence.current) setError(`Could not save the custom models: ${describeError(e)}`);
      return false;
    } finally {
      if (sequence === saveSequence.current) setSaving(false);
    }
  }

  const other = settings.send_default === 'steer' ? 'Queue' : 'Steer';

  return (
    <div className="flex min-h-0 flex-1 flex-col animate-rise">
      <header className="flex h-header shrink-0 items-center gap-1.5 border-b border-hairline pr-2 pl-3">
        {leading}
        <h1 className="min-w-0 flex-1 truncate text-display-sm text-ink">Settings</h1>
        {saving && <Spinner className="shrink-0" />}
        <Tip label="Close settings">
          <Button size="icon-md" aria-label="Close settings" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        </Tip>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <div className="flex w-full min-w-0 flex-col px-4 py-4 md:px-6">
          <p className="mb-4 text-caption text-muted">Kept by the service, so they apply in every browser.</p>
          <Section id="composer" title="Composer">
            <Row
              id="send-default"
              label="While a task is running, Enter…"
              help={
                <>
                  Steer adds the message to the running turn; Queue holds it for the next one. {other} stays on Ctrl+Enter (⌘+Enter on a Mac) and on its own button. A message with files or
                  attachments always queues.
                </>
              }
            >
              <Segmented
                aria-labelledby="send-default-label"
                aria-describedby="send-default-help"
                disabled={saving}
                value={settings.send_default}
                onValueChange={(v) => void save({ send_default: v as SendDefault })}
                items={[
                  { value: 'steer', label: 'Steer' },
                  { value: 'queue', label: 'Queue' },
                ]}
              />
            </Row>
          </Section>
          <Section id="titles" title="Task titles">
            {(meta?.providers ?? []).filter((p) => p.capabilities.titles).map((p) => {
              const current = settings.title_model?.[p.name] ?? '';
              const choices = modelChoices(p.models, settings.hidden_models?.[p.name], current);
              return <Row key={p.name} id={`titles-${p.name}`} label={p.display_name} help="Use the provider's own title, or generate a short title with a chosen model. Applies to new tasks without a name.">
                <Select aria-label={`${p.display_name} task title model`} value={current} disabled={saving} className="sm:w-64" items={[
                  { value: '', label: `${p.display_name}'s own title` },
                  ...choices.map(({ model, note }) => ({ value: model.id, label: `${model.name}${note ? ` (${note.toLowerCase()})` : ''}`, hidden: !!note })),
                ]} onValueChange={(id) => void save({ title_model: { ...settings.title_model, [p.name]: id } })} />
              </Row>;
            })}
          </Section>
          <Section id="models" title="Models">
            <Note>Hidden models leave the selection menus. Tasks already using one keep it. New models appear automatically.</Note>
            <CustomModels models={settings.custom_models ?? []} disabled={saving} onSave={saveCustom} />
            {(meta?.providers ?? []).map((p) => {
              const hidden = settings.hidden_models?.[p.name] ?? [];
              const models: Model[] = [...p.models, ...hidden.filter((id) => !p.models.some((m) => m.id === id)).map((id) => ({ id, name: id }))];
              return <div key={p.name} className="flex flex-col gap-1">
                <h3 className="mb-1 text-ui font-medium">{p.display_name}</h3>
                <div className="grid grid-cols-1 gap-x-6 lg:grid-cols-2 xl:grid-cols-3">
                {models.map((m) => {
                  const shown = !hidden.includes(m.id);
                  const offered = p.models.some((v) => v.id === m.id);
                  return <div key={m.id} className="flex min-h-12 items-center gap-3 border-b border-hairline py-2">
                    <div className="flex min-w-0 flex-1 flex-col">
                      <span className="text-ui font-medium text-ink">{m.name}</span>
                      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-keycap text-muted">
                        <span className="min-w-0 break-all font-mono">{m.id}{!offered ? ' · not offered now' : ''}</span>
                        {p.capabilities.usage && <span>{modelCostLine(m)}</span>}
                      </div>
                    </div>
                    <span className="text-keycap text-muted">{shown ? 'Visible' : 'Hidden'}</span>
                    <Switch aria-label={`Show ${m.name}`} checked={shown} disabled={saving} onCheckedChange={(value) => void save({ hidden_models: { ...settings.hidden_models, [p.name]: value ? hidden.filter((id) => id !== m.id) : [...hidden, m.id] } })} />
                  </div>;
                })}
                </div>
              </div>;
            })}
          </Section>
          {error && (
            <Note tone="error" role="alert">
              {error}
            </Note>
          )}
        </div>
      </div>
    </div>
  );
}
